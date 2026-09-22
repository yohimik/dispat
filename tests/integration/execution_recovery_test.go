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
