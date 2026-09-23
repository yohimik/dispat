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

// executionLockReadPattern selects the one read of the remote release lock an
// ownership check makes, and nothing else a release asks a remote: the
// records comparison lists every tag without naming this one, and giving the
// lock back is a push.
const executionLockReadPattern = "*ls-remote --tags -- *refs/tags/" + lockTag

// executionLockReadRetry is the line each failed read that is read again
// writes.
const executionLockReadRetry = "the release lock could not be read; reading it again"

// TestExecutionOwnershipReadIsRetriedBeforeALoss: a read of the lock that
// fails once says nothing about the lock, so it is read again and the release
// carries on. A remote that answers none of the three reads is a lock this run
// cannot show it owns, which stops every new effect exactly as a lost lock
// does and says which of the two it was; the lock itself is still this run's,
// so it is given back rather than left for an operator.
func TestExecutionOwnershipReadIsRetriedBeforeALoss(t *testing.T) {
	for name, tc := range map[string]struct {
		fault  harness.GitFault
		isLost bool
	}{
		"the first read fails": {fault: harness.GitFault{Pattern: executionLockReadPattern, Nth: 1}},
		"every read fails":     {fault: harness.GitFault{Pattern: executionLockReadPattern}, isLost: true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Scripts["publish"] = models.Script{executionPublishProbe}
			})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, tc.fault)

			res := rig.release(fault.Env()...)
			stopAll(t, []*executionWorker{worker})

			assert.False(t, remoteHoldsLock(t, rig.origin), "the lock this run still owned is given back")
			if !tc.isLost {
				require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Equal(t, 1, executionLineCount(res, executionLockReadRetry),
					"the failed read is read again once\nstdout:\n%s", res.Stdout)
				assert.Zero(t, executionLineCount(res, "the release lock was lost, so no new effect may start"))
				assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"))
				assert.NotEmpty(t, executionReleaseTags(rig), "the release was recorded")
				return
			}
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, 3, fault.Matches(), "three reads, then the answer")
			assert.Equal(t, 2, executionLineCount(res, executionLockReadRetry))
			lost, isLost := executionLine(res, "the release lock was lost, so no new effect may start")
			require.True(t, isLost, "stdout:\n%s", res.Stdout)
			assert.Equal(t, "unverified", lost.Str("reason"), "a lock nobody could read is not a lock read as gone")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode))
			assert.Empty(t, executionProbedPackages(rig, "publish"), "no publish command ran")
			assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
		})
	}
}

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
