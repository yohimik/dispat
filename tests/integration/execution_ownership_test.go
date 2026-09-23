// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: ownership asked again, and remembered once it is gone.
//
// "After lock loss, no new effect may start" is a rule about moments rather
// than about a run, so the question is asked again before every assignment.
// Every new effect checks the remote because a lock can disappear between two
// assignments. A loss, once seen, is remembered because a run does not get
// its exclusion back. Both halves are visible from outside: the loss is
// decided once however many tasks it stops, and every task after it is refused
// without anybody asking a remote again.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionOwnershipPackages are three packages with no edge between them, so
// that what stops the second and the third is the lost exclusion rather than
// the failure of the first.
var executionOwnershipPackages = []string{"alpha", "beta", "gamma"}

// TestExecutionLostLockIsRememberedForEveryLaterTask: the first build to run
// takes the release lock off the remote and then holds the node while other
// tasks become ready for assignment.
//
// The next task therefore asks the remote, finds the lock gone and decides the
// loss; the one after it is refused from that decision. The loss is reported
// once, exactly one build ran anywhere, nothing is recorded, and the lock the
// run lost is neither re-created nor deleted by it.
func TestExecutionLostLockIsRememberedForEveryLaterTask(t *testing.T) {
	repo := harness.New(t)
	seedIndependentPackages(repo, executionOwnershipPackages)
	mailbox := executionMailbox(t)
	cfg := libsConfig(executionRecordingScript+
		` && { git --git-dir="`+executionOriginRef+`" tag -d `+lockTag+` || true; } && sleep `+
		executionOwnershipHold, len(executionOwnershipPackages))
	cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 120, Cancel: 10},
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("chore(alpha,beta,gamma): delegate the builds of this repository")
	rig := newExecutionRigOver(t, repo, mailbox)
	worker := rig.startWorker(executionWorkerConfig(mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0,
		executionOriginEnv+"="+rig.origin)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, executionLineCount(res, "the release lock was lost, so no new effect may start"),
		"the loss is decided once, however many tasks it stops\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode),
		"and is reported with the lock code\nstdout:\n%s", res.Stdout)
	assert.Len(t, executionBuiltPackages(rig), 1,
		"only the build that took the lock away ran: %v", rig.runs())
	assert.Empty(t, executionReleaseTags(rig), "no version was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock this run lost is not re-created by it")
	served := worker.stop(t)
	assert.NotContains(t, served.Stdout, `"message":"publication authorized"`)
}

// executionOwnershipHold keeps the first build on the only node while the
// orchestrator handles the lock loss and cancels later work.
const executionOwnershipHold = "1"

// executionOriginRef is how a fixture script names the bare remote it writes
// to, expanded by the shell that runs the script on the node.
const executionOriginRef = "$" + executionOriginEnv

// executionLineCount is how often one message appears in a run's log.
func executionLineCount(res harness.RunResult, message string) int {
	seen := 0
	for _, event := range executionEvents(res) {
		if event.Str("message") == message {
			seen++
		}
	}
	return seen
}

// executionBuiltPackages is every package whose build script ran on a node, in
// the order the fixture recorded them.
func executionBuiltPackages(rig *executionRig) []string {
	var names []string
	for _, run := range rig.runs() {
		if run.Node == executionNode {
			names = append(names, run.Package)
		}
	}
	return names
}
