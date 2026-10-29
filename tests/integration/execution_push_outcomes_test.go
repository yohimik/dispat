// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a coordination push whose answer never arrived, or that the remote
// refused, on whichever of the two machines made it.
//
// A push is never repeated to learn its outcome. The party that did not hear
// success reads the branch instead: its message on the tip, or under what the
// other party wrote on top of it, landed; a refused message the branch does not
// carry did not; anything else is unknown. Each scenario below fails one push
// of one party with a stand-in git and proves what the run then does: the work
// runs exactly once, nothing is reported unknown that is known, a refusal is
// answered long before the task deadline, and no coordination branch outlives
// the run.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const executionPublicationUnknownCode = "E228"

// executionLostPushReply is what the stand-in answers a push with in place of
// git's own report: a line that names no ref, which is how a push whose
// response was lost reads to the process that made it.
const executionLostPushReply = "Done\n"

// newExecutionPushOutcomeRig is the one-package rig with its build pinned to
// the worker, recording every build, and with a task deadline long enough that
// a run which waited it out could not pass for one that did not.
func newExecutionPushOutcomeRig(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	return newExecutionRig(t, append([]func(*models.File){func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.Scripts["build"] = models.Script{executionRecordingScript}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 300, Cancel: 20}
	}}, adjust...)...)
}

// executionBuildsOf is how many times the fixture recorded a build of one
// package, wherever it ran.
func executionBuildsOf(rig *executionRig, name string) int {
	count := 0
	for _, run := range rig.runs() {
		if run.Package == name && run.Node == executionNode {
			count++
		}
	}
	return count
}

// TestExecutionPeerNeverPushesClaimRef is the live v3 transport control. A
// stand-in Git would lose any peer task-ref push reply; no such push occurs.
// The first node still records one claim and the build runs exactly once.
func TestExecutionPeerNeverPushesClaimRef(t *testing.T) {
	rig := newExecutionPushOutcomeRig(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*dispat-task-*", Nth: 1, After: true, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)
	started := time.Now()
	res := rig.release()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Zero(t, fault.Matches(), "a v3 peer must return Claim over HTTPS without pushing a task ref")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the task ran once: %v", rig.runs())
	assert.Less(t, time.Since(started), 150*time.Second, "the first node settled the return without waiting out the task deadline")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"the accepted Claim leaves nothing unknown\nstdout:\n%s", res.Stdout)
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the completed run released its lock")
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
		"the first node retains the signed settlement feed")
}

// TestExecutionUnreadableTaskRefWaitsWithoutDuplicateClaim makes the peer's
// read-only view of an offered v3 task temporarily unavailable. Once the
// exact ref is readable, it claims once through the return channel and builds
// once; no timeout authorizes a second command.
func TestExecutionUnreadableTaskRefWaitsWithoutDuplicateClaim(t *testing.T) {
	rig := newExecutionPushOutcomeRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts.Admission = 15
	})
	reads := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*ls-remote*refs/heads/dispat-task-*",
		Nth:     1, Onward: true, Through: 2, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, reads.Env()...)

	started := time.Now()
	res := rig.release()
	served := worker.stop(t)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.GreaterOrEqual(t, reads.Matches(), 2, "two exact task-ref reads were unavailable before recovery")
	assert.Equal(t, 1, strings.Count(served.Stdout, `"message":"task claimed"`),
		"recovery of a temporarily unreadable task ref must not duplicate the Claim")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the build ran once: %v", rig.runs())
	assert.Less(t, time.Since(started), 150*time.Second, "and nobody waited out the task deadline")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"one accepted Claim leaves nothing unknown\nstdout:\n%s", res.Stdout)
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
		"the first node retains signed finality after the read fault")
}

// TestExecutionPeerAcknowledgesWithdrawalWithoutGitPush: the first node
// cancels a claimed task; the peer stops it and returns a signed Ack without
// pushing a task ref. A lost Ack return reply is tested against the exact
// receipt lookup in TestWorkerLostAcknowledgementReplyUsesExactReceipt.
func TestExecutionPeerAcknowledgesWithdrawalWithoutGitPush(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		// Keep this return-channel fixture to one directly reaped process. The
		// unsettled-process-group scenario separately covers retained descendants.
		cfg.Scripts["build"] = models.Script{executionRecordingScript + " && exec sleep 600"}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 600, Cancel: 60}
		cfg.LogLevel = "debug"
	})
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*dispat-task-*", Nth: 1, After: true, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)
	started := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)

	require.Eventually(t, func() bool { return len(rig.runs()) == 1 }, 60*time.Second, 50*time.Millisecond,
		"the build did not start before the interruption")
	started.Signal(syscall.SIGINT)
	res := started.Wait()
	served := worker.stop(t)

	assert.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero")
	assert.Zero(t, fault.Matches(), "the peer returns Ack through HTTPS, not a Git task push")
	requireChoreographyTaskChain(t, rig.mailbox, rig.branches(),
		"choreography-claim", "cancel", "ack", "choreography-decision")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"an acknowledged withdrawal leaves nothing unknown\nstdout:\n%s", res.Stdout)
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "one command started before Cancel")
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the settled cancellation releases the lock")
	assert.Equal(t, 0, served.Code, "the peer remains healthy after returning Ack")
}

// TestExecutionPeerNeverPushesResultRef makes every attempted peer task-ref
// push fail. The v3 Result travels as a bounded pack through the return
// channel, so the build and release still complete once.
func TestExecutionPeerNeverPushesResultRef(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refused-result fixture uses a POSIX shell")
	}
	rig := newExecutionPushOutcomeRig(t)
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionRefusedResultScript), 0o755))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_REFUSED_GIT="+realGit, "DISPAT_IT_REFUSED_DIR="+shim)

	res := rig.release()
	replies := stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, replies[0].Stderr)
	_, err = os.Stat(filepath.Join(shim, "refused"))
	require.True(t, os.IsNotExist(err), "the peer must not attempt any task-ref push")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "one result was admitted")
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "the first node recorded the release")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the completed run released its lock")
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
		"the first node retains the signed result and decision feed")
}

// The node's second update of a task ref is its result, after its claim.
// A nonempty expected-old lease selects updates and excludes the upfront
// create-only reservation batch, which also names task refs.
// This shim answers it the way a server rule refuses an update, with a
// porcelain `[remote rejected]` line for the ref and exit code 1, and never
// runs it. Every other invocation is the real git. The worker receives this
// shim in the result and claim cases; the orchestrator receives it in the
// assignment-refusal case.
const executionRefusedResultScript = `#!/bin/sh
set -eu
case "$*" in
*push*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_REFUSED_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq "${DISPAT_IT_REFUSED_ORDINAL:-2}" ]; then
  printf '%s\n' "$*" > "$DISPAT_IT_REFUSED_DIR/refused"
  for refspec in "$@"; do :; done
  printf '!\t%s\t[remote rejected] (pre-receive hook declined)\n' "$refspec"
  exit 1
 fi
 ;;
esac
exec "$DISPAT_IT_REFUSED_GIT" "$@"
`

// TestExecutionRefusedAssignmentIsOfferedAgain: the mailbox refuses the run's
// first atomic task, slot and ledger update outright. Exact unchanged tips
// prove the transaction never landed, so the first node retries the same
// signed attempt once and builds and releases the package once.
func TestExecutionRefusedAssignmentIsOfferedAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusing fixture uses a POSIX shell")
	}
	rig := newExecutionPushOutcomeRig(t)
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionRefusedResultScript), 0o755))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release("PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_REFUSED_GIT="+realGit, "DISPAT_IT_REFUSED_DIR="+shim, "DISPAT_IT_REFUSED_ORDINAL=1")
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refused, err := os.ReadFile(filepath.Join(shim, "refused"))
	require.NoError(t, err, "the shim refused an activation update")
	assert.Contains(t, string(refused), "push --atomic")
	assert.Contains(t, string(refused), "--force-with-lease=refs/heads/dispat-task-")
	_, isRevoked := executionLine(res, "the queued assignment was revoked and the task is placed again")
	assert.False(t, isRevoked, "an offer that never landed must not consume an attempt")
	settled, isSettled := executionLine(res, "task outcome")
	require.True(t, isSettled, "the exact offer was retried and settled\nstdout:\n%s", res.Stdout)
	assert.Equal(t, float64(1), settled["attempt"])
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the package was built once: %v", rig.runs())
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
	assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
		"the first node retains signed finality for the one admitted attempt")
}

// TestExecutionLostActivationReplySettlesOrFailsClosed: an atomic task, slot
// and ledger offer whose reply is lost can continue when all exact updates
// landed. A reply with no applied update remains uncertain: an in-flight
// request may still land after bounded reads, so the run retains its lock and
// canonical evidence without authorizing another attempt.
func TestExecutionLostActivationReplySettlesOrFailsClosed(t *testing.T) {
	for name, isApplied := range map[string]bool{"applied": true, "never applied": false} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionPushOutcomeRig(t)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*",
				Nth:     1, After: isApplied, Output: executionLostPushReply})

			res := rig.release(fault.Env()...)
			stopAll(t, []*executionWorker{worker})

			assert.Positive(t, fault.Matches(), "the fault reached the assignment push")
			if isApplied {
				require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the package was built once: %v", rig.runs())
				assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
				assert.False(t, remoteHoldsLock(t, rig.origin), "the settled run releases its lock")
			} else {
				require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Contains(t, res.Stdout, "task and capacity CAS is unresolved")
				assert.Zero(t, executionBuildsOf(rig, "core"), "uncertain placement starts no task")
				assert.Empty(t, executionReleaseTags(rig), "uncertain placement records no release")
				assert.True(t, remoteHoldsLock(t, rig.origin),
					"an unknown in-flight offer keeps native authority fenced")
			}
			assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
				"the canonical feed remains for either finality or reconciliation")
		})
	}
}

// TestExecutionHungMailboxKeepsActiveClaimFenced: the mailbox stops answering
// after an accepted Claim, while a node is building, and the release is
// interrupted. Bounded cleanup returns, but cannot prove the claim stopped;
// it retains the release lock and exact refs for same-generation recovery.
func TestExecutionHungMailboxKeepsActiveClaimFenced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hung-mailbox fixture uses a POSIX shell")
	}
	rig := newExecutionPushOutcomeRig(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{executionRecordingScript, "sleep 60"}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 300, Cancel: 5}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionHungMailboxScript), 0o755))
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(shim, "release"), nil, 0o600) })
	started := rig.repo.StartReleaseEnv(rig.env(
		"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_HANG_GIT="+realGit, "DISPAT_IT_HANG_DIR="+shim,
		"DISPAT_IT_HANG_MAILBOX="+rig.mailbox), "release", "--return-endpoint", rig.returnURL)

	executionAwaitProbe(t, rig, executionNode)
	require.NoError(t, os.WriteFile(filepath.Join(shim, "hang"), nil, 0o600))
	interrupted := time.Now()
	started.Signal(syscall.SIGINT)
	res := started.Wait()
	elapsed := time.Since(interrupted)
	stopAll(t, []*executionWorker{worker})

	require.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero\nstdout:\n%s", res.Stdout)
	assert.Less(t, elapsed, 150*time.Second, "the withdrawal and the close ended within their bounds")
	assert.True(t, remoteHoldsLock(t, rig.origin),
		"an unsettled accepted Claim keeps native authority fenced\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRetainedCode),
		"the unavailable canonical ledger is reported\nstdout:\n%s", res.Stdout)
	assert.NotEmpty(t, executionTransportRefs(t, rig.repo.Root),
		"the exact fetched task evidence remains available for reconciliation")
	assert.Empty(t, executionReleaseTags(rig), "an unresolved build cannot publish")
}

// executionTransportRefs are the fetched coordination refs a repository holds.
func executionTransportRefs(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname)",
		"refs/dispat-transport/").CombinedOutput()
	require.NoError(t, err, "for-each-ref: %s", out)
	return strings.Fields(string(out))
}

// Every git invocation naming the mailbox hangs once the test says so, until
// it is released or killed. Only the orchestrator receives this shim.
const executionHungMailboxScript = `#!/bin/sh
case "$*" in
*"$DISPAT_IT_HANG_MAILBOX"*)
 if [ -f "$DISPAT_IT_HANG_DIR/hang" ]; then
  ticks=0
  while [ ! -f "$DISPAT_IT_HANG_DIR/release" ] && [ "$ticks" -lt 3000 ]; do
   ticks=$((ticks + 1))
   sleep 0.1
  done
 fi
 ;;
esac
exec "$DISPAT_IT_HANG_GIT" "$@"
`
