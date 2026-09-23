// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the one condition of this profile that outlives the run.
//
// A publication this run authorized and cannot establish the outcome of is
// different from every other way a distributed release goes wrong, because the
// thing that may have happened is a write to somebody else's registry. So the
// run says so with a class of its own, makes no second attempt under that
// authorization, and decides one further question: may it give the exclusion
// back. The answer is the publisher's own acknowledgement and nothing else. A
// node that confirmed it has stopped is quiesced and the lock goes back; a node
// that never answered may still be uploading, so the repository it was
// publishing into stays locked for an operator.
//
// The third scenario here is the case that never becomes unknown at all: an
// authorization that was refused before it was written. Nothing was ever told
// to publish, so the withdrawal is answered at the gate and the package simply
// fails.

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionUnknownPublicationMessage is the line that reports the class, and
// executionRetainedLockMessage the one a run writes when it leaves an
// exclusion behind on purpose.
const (
	executionUnknownPublicationMessage = "the outcome of an authorized publication cannot be established"
	executionRetainedLockMessage       = "release lock retained"
	// executionPublicationUnknownCode is §28.9's `publication-unknown` in
	// dispat's own numbering.
	executionPublicationUnknownCode = "E228"
)

// newExecutionSlowPublishRig is one package whose publish is delegated and
// whose publish command does not finish by itself, so that a scenario can
// decide what interrupts it.
func newExecutionSlowPublishRig(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	return newExecutionRig(t, append([]func(*models.File){func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe + " && sleep 600"}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: 600, Cancel: 60}
	}}, adjust...)...)
}

// TestExecutionInterruptedPublisherIsQuiescedAndGivesTheLockBack: the run is
// interrupted while a node it authorized is inside its publish command.
//
// Nobody can say whether the registry was written, so the outcome is unknown
// and the package fails with the class that says exactly that. What the run
// can establish is the other half: the node answered, and its answer says the
// command had begun. A publisher that answered has provably stopped, so the
// exclusion is not the operator's problem and the lock goes back as it always
// does.
func TestExecutionInterruptedPublisherIsQuiescedAndGivesTheLockBack(t *testing.T) {
	rig := newExecutionSlowPublishRig(t)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	// The publish command is running on the node: an interrupt before it would
	// be a scenario about an attempt that was never authorized.
	executionAwaitProbe(t, rig, "probe-publish")
	started.Signal(syscall.SIGINT)
	res := started.Wait()

	assert.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero")
	unknown, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	require.True(t, isUnknown, "the run reported the class\nstdout:\n%s", res.Stdout)
	assert.Equal(t, true, unknown["quiesced"],
		"the publisher answered, so it has provably stopped")
	assert.Equal(t, executionNode, unknown.Str("worker"), "and the node is named")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"with the code beside the class\nstdout:\n%s", res.Stdout)

	settled, isSettled := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isSettled, "stdout:\n%s", res.Stdout)
	assert.Equal(t, true, settled["commandStarted"],
		"the node said its own command had begun, which is what makes the outcome unknown")

	_, isRetained := executionLine(res, executionRetainedLockMessage)
	assert.False(t, isRetained, "a quiesced publisher retains no exclusion")
	assert.False(t, remoteHoldsLock(t, rig.origin), "so the lock went back")
	assert.Empty(t, executionReleaseTags(rig), "and nothing was recorded")
	branches := rig.branches()
	require.Len(t, branches, 1, "the unknown publication's branch remains for reconciliation")
	assert.Contains(t, branches[0], "-publish-", "the unrelated preflight branch was cleaned")
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionUnansweredPublisherRetainsTheLock: the same class, decided the
// other way.
//
// The node is authorized, runs its publish command and can no longer write to
// its own branch, so neither its result nor its acknowledgement ever arrives.
// The run waits out its own wait, withdraws the attempt, hears nothing, and is
// left with a machine that may still be publishing. It therefore refuses to
// report a clean cleanup: the release lock of the repository that publisher was
// writing into stays on the remote, with the order of recovery named.
func TestExecutionUnansweredPublisherRetainsTheLock(t *testing.T) {
	wait, cancel := 6, 4
	if harness.IsTinyGo() {
		wait, cancel = 30, 20
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: wait, Cancel: cancel}
	})
	// The node's third push onto its publish branch is its result: the first
	// two are its claim and its ready. Everything from there on fails, so the
	// acknowledgement never arrives either.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*-publish-*", Nth: 3, Onward: true})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"),
		"the publish command did run, which is the whole reason the outcome matters: %v", rig.runs())
	unknown, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	require.True(t, isUnknown, "stdout:\n%s", res.Stdout)
	assert.NotEqual(t, true, unknown["quiesced"], "nothing answered, so nothing is known to have stopped")
	retained, isRetained := executionLine(res, executionRetainedLockMessage)
	require.True(t, isRetained, "the run said it was leaving the exclusion behind\nstdout:\n%s", res.Stdout)
	assert.Equal(t, lockTag, retained.Str("tag"))
	assert.True(t, remoteHoldsLock(t, rig.origin),
		"and the lock is still on the remote for an operator")
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	branches := rig.branches()
	require.Len(t, branches, 1, "the unanswered publisher's branch remains for reconciliation")
	assert.Contains(t, branches[0], "-publish-", "the unrelated preflight branch was cleaned")
	stopAll(t, []*executionWorker{worker})
}

// executionProbedPackages is every package one of the fixture's probes fired
// for, sorted, so that a claim about which commands ran is a claim about a set.
func executionProbedPackages(rig *executionRig, probe string) []string {
	var names []string
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+probe {
			names = append(names, run.Package)
		}
	}
	return names
}

// TestExecutionLockLostDuringTheHookWithdrawsTheWaitingPublisher: the
// authorization that is refused rather than lost.
//
// The node's own beforePublish hook takes the release lock off the remote,
// which is the one way a fixture can lose an exclusion in the exact window the
// check exists for: between the hook and the authorization. The run finds the
// lock gone, withdraws the attempt instead of authorizing it, and the node
// answers from the gate that nothing started. Nothing is unknown here, and
// nothing is retained: a publication that was never authorized is a
// publication that did not happen.
func TestExecutionLockLostDuringTheHookWithdrawsTheWaitingPublisher(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe + ` && ` +
			`git --git-dir="$DISPAT_IT_EXECUTION_ORIGIN" tag -d ` + lockTag}
		space := cfg.Spaces["libs"]
		space.Flow.BeforePublish = []string{"prepublish"}
		cfg.Spaces["libs"] = space
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: 120, Cancel: 30}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		executionOriginEnv+"="+rig.origin)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "prepublish"),
		"the hook ran on the node, which is what made the window: %v", rig.runs())
	assert.Empty(t, executionProbedPackages(rig, "publish"),
		"and its publish command never started anywhere: %v", rig.runs())
	withheld, isWithheld := executionLine(res, "publication withheld")
	require.True(t, isWithheld, "the run said it was withholding it\nstdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, withheld.Str("worker"))
	settled, isSettled := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isSettled, "the node answered from the gate\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "authorization-wait", settled.Str("phase"),
		"and said it was still waiting to be authorized")
	assert.NotEqual(t, true, settled["commandStarted"])
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode),
		"the refusal carries the lock code\nstdout:\n%s", res.Stdout)
	_, isUnknown := executionLine(res, executionUnknownPublicationMessage)
	assert.False(t, isUnknown, "a publication nobody authorized has a perfectly known outcome")
	_, isRetained := executionLine(res, executionRetainedLockMessage)
	assert.False(t, isRetained, "so no exclusion is left behind")
	assert.Empty(t, executionReleaseTags(rig), "and nothing was recorded")
	assert.Empty(t, rig.branches(), "the run still closed the branches it created")
	stopAll(t, []*executionWorker{worker})
}
