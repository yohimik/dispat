// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a distributed run does when something stops answering.
//
// The scenarios here are about the two clocks a delegated task lives under and
// about the branches a node is asked to ignore. Both are properties of the
// whole exchange rather than of any one message, so both are driven through
// two real processes and asserted on what they left on the mailbox and in
// their logs.

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionQueuedWorkIsNotATimeout (spec vector 21): a node's capacity is
// the node's, so two runs sharing one node of capacity one make the second
// run's assignment wait in the mailbox. Queue time is not run time: the task
// deadline measures the work, so a wait that is longer than one build and
// shorter than two still completes both runs, and neither takes a healthy node
// out of its pool.
//
// Before this gate the deadline started when the orchestrator began waiting,
// so the queued assignment timed out without ever having run: it leaked the
// node's slot, marked a node that was working perfectly well as unhealthy, and
// failed a package for the sin of being second.
func TestExecutionQueuedWorkIsNotATimeout(t *testing.T) {
	build, wait := executionQueueTimings()
	mailbox := executionMailbox(t)
	first := newExecutionQueuedRepository(t, mailbox, "first", build, wait)
	second := newExecutionQueuedRepository(t, mailbox, "second", build, wait)
	worker := startWorker(t, first.repo, executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionBuildLogEnv+"="+first.builds)

	started := first.repo.StartReleaseEnv(first.env(), "release")
	other := second.release()
	res := started.Wait()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.Equal(t, 0, other.Code, "stdout:\n%s\nstderr:\n%s", other.Stdout, other.Stderr)
	for name, run := range map[string]harness.RunResult{"first": res, "second": other} {
		t.Run(name, func(t *testing.T) {
			_, isUnhealthy := executionLine(run, "worker marked unhealthy")
			assert.False(t, isUnhealthy,
				"a node that was busy with another run's task is not a node that stopped answering")
		})
	}
	timeline := append(first.repo.Timeline("timeline.log"), second.repo.Timeline("timeline.log")...)
	require.Len(t, timeline, 2, "both releases built their package")
	harness.AssertConcurrencyBudget(t, timeline, 1)
	worker.proc.Signal(syscall.SIGINT)
	require.Equal(t, 0, worker.proc.Wait().Code)
}

// executionQueueTimings are a build long enough to make the second run queue
// behind the first, and a task deadline that is longer than one of them and
// shorter than two. The pair is what makes the claim testable at all: with a
// deadline longer than both, nothing is being asserted.
func executionQueueTimings() (build time.Duration, wait int) {
	if harness.IsTinyGo() {
		return 15 * time.Second, 25
	}
	return 3 * time.Second, 5
}

// newExecutionQueuedRepository is one independent single-package repository
// delegating to a shared mailbox, with a build that takes long enough to make
// the other run wait and a task deadline that would have expired during that
// wait.
func newExecutionQueuedRepository(t *testing.T, mailbox, label string,
	build time.Duration, wait int) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", label)
	cfg := libsConfig(executionRecordingScript+" && "+
		repo.TsmarkScript("timeline.log", label, build), 1)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: 10},
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(" + label + "): bootstrap")
	return newExecutionRigOver(t, repo, mailbox)
}

// TestExecutionTransportBranchesAreNotRejectedAssignments: a prepared input
// state and a relayed result both appear in the namespace addressed to a node,
// and neither is work. A node that read them found no message of this protocol
// and reported a branch the run itself had created as an assignment nobody
// could read, once per snapshot, at warning level and under the authority code
// a real forgery uses.
func TestExecutionTransportBranchesAreNotRejectedAssignments(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		space := cfg.Spaces["libs"]
		space.RunOnly = &models.RunOnly{Build: "worker", Publish: "orchestrator"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	// The build was delegated, so the run pushed a prepared input state onto
	// this node's namespace: that is what makes the absence below a claim
	// rather than a coincidence. The branch itself is gone by now, because a
	// run closes the branches it created on its way out.
	assert.Equal(t, map[string]string{"core": executionNode}, rig.nodesByPackage())
	served := worker.stop(t)

	assert.Empty(t, executionRejections(served),
		"a branch the run itself created is not a rejected assignment")
	_, isWarned := executionLine(served, "assignment rejected")
	assert.False(t, isWarned, "and it produces no warning for an operator to chase")
}

// TestExecutionStaleReceiptIsRejected (spec vector 7b): every identity a reply
// is bound to, wrong in turn.
//
// The orchestrator accepts a receipt only when its signature, its header and
// every one of the identities it echoes are this attempt's: the run, the plan
// it executes, the task, the attempt number, the ownership generation, the
// node it was addressed to, the branch it was found on, and the exact
// assignment object it answers. A reply that fails any of them is ignored with
// one warning naming the reason and nothing it contained, and the attempt goes
// on waiting, so the package fails on its own deadline rather than on somebody
// else's word.
//
// The last row is the control: the same saboteur, bound correctly, is accepted
// and the package is released. Without it the rows above would pass for a
// fixture that simply never worked.
func TestExecutionStaleReceiptIsRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		mangle func(executionOrderedJSON) executionOrderedJSON
		secret string
		reason string
	}{
		"a reply of another run": {
			mangle: executionRebind("run", "0123456789abcdef0123456789abcdef"), reason: "replay"},
		"a reply of another planning": {
			mangle: executionRebind("planDigest",
				strings.Repeat("a", 64)), reason: "replay"},
		"a reply of another attempt": {
			mangle: executionRebind("attempt", 2), reason: "replay"},
		"a reply under another ownership": {
			mangle: executionRebind("generation", "an-older-acquisition"), reason: "replay"},
		"a reply answering another assignment": {
			mangle: executionRebind("assignment", strings.Repeat("0", 40)), reason: "replay"},
		"a reply from another node": {
			mangle: executionRebind("node", executionSecondNode), reason: "node"},
		"a reply found on another branch": {
			mangle: executionRebind("branch", "dispat-worker-build-a-20260922-build-elsewhere"),
			reason: "branch"},
		"a reply signed with another secret": {
			secret: "not-the-runs-secret", reason: "signature"},
		"a reply bound to this very attempt": {},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSaboteurRig(t)
			done := executionSabotage(t, rig, tc.mangle, tc.secret)
			res := rig.release()
			executionAwaitAnswered(t, done)

			if tc.reason == "" {
				require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.NotEmpty(t, rig.repo.TagList(), "the correctly bound receipt released the package")
				return
			}
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, executionRefusedReplies(res), tc.reason,
				"the reply is ignored for exactly one reason\nstdout:\n%s", res.Stdout)
			assert.Empty(t, rig.repo.TagList(), "and grants the attempt nothing")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
				"the package fails on its own deadline")
		})
	}
}

// TestExecutionOldRunReceiptGrantsNothing (spec vector 20): a receipt of an
// earlier run, complete and correctly signed, replayed onto this run's branch.
//
// It grants nothing, and "nothing" is the whole claim: the attempt is not
// satisfied by it, no output set is admitted from it, no version is tagged and
// the run fails on the deadline of an attempt nobody answered. A receipt is a
// statement about one attempt of one run, and an implementation that read it
// as a statement about work would be one an old mailbox could release packages
// through.
func TestExecutionOldRunReceiptGrantsNothing(t *testing.T) {
	rig := newExecutionSaboteurRig(t)
	// The identities of a run that finished yesterday: its own id and the
	// ownership it held, both of which this run has never heard of.
	done := executionSabotage(t, rig, func(document executionOrderedJSON) executionOrderedJSON {
		return document.set("run", "beefbeefbeefbeefbeefbeefbeefbeef").
			set("generation", "yesterdays-acquisition")
	}, "")

	res := rig.release()
	executionAwaitAnswered(t, done)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, executionRefusedReplies(res), "replay")
	assert.Empty(t, rig.repo.TagList(), "an old run's receipt discharges nothing")
	assert.Empty(t, rig.runs(), "and no build ran anywhere on the strength of it")
}

// executionAwaitAnswered waits, bounded, for the saboteur to have answered.
// It is bounded because a fixture that never answers must fail its scenario
// rather than hang the suite: the release has already finished by the time it
// is called, so anything it waits for has either happened or never will.
func executionAwaitAnswered(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the saboteur never answered this run's build assignment")
	}
}

// executionRefusedReplies are the reasons the orchestrator refused a node's
// replies, which is the mirror of the reasons a node refuses an assignment:
// the same enum, read off the line the other party writes.
func executionRefusedReplies(res harness.RunResult) []string {
	var reasons []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "result rejected" {
			reasons = append(reasons, event.Str("reason"))
		}
	}
	return reasons
}

// newExecutionSaboteurRig is one package whose build is delegated, with a task
// deadline short enough that an attempt nobody validly answered fails quickly.
func newExecutionSaboteurRig(t *testing.T) *executionRig {
	t.Helper()
	wait := 6
	if harness.IsTinyGo() {
		wait = 30
	}
	return newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{
			Preflight: 30, Task: wait, Cancel: 5}
	})
}

// executionRebind is the mangling one row applies: exactly one identity of an
// otherwise correct receipt, replaced.
func executionRebind(key string, value any) func(executionOrderedJSON) executionOrderedJSON {
	return func(document executionOrderedJSON) executionOrderedJSON {
		return document.set(key, value)
	}
}

// executionSabotage answers this run's build assignment the way a party with
// push access to the mailbox would: it claims the branch and pushes one result
// of its own, mangled as the row asks.
//
// It answers the probe as well, because a run whose preflight nobody answers
// never dispatches anything and would be a scenario about preflight instead.
// The channel is closed once it has answered, so a scenario waits for the
// thing it is about rather than for a duration.
func executionSabotage(t *testing.T, rig *executionRig,
	mangle func(executionOrderedJSON) executionOrderedJSON, secret string) <-chan struct{} {
	t.Helper()
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.mangle = mangle
	if secret != "" {
		worker.resultSecret = secret
	}
	worker.serve()
	t.Cleanup(worker.close)
	return worker.answered
}
