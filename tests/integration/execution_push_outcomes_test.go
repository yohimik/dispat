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

// TestExecutionLostClaimResponseRunsTheTaskOnce: the node's claim push applies
// and its answer is lost. The
// node reads the branch, finds its own claim there and runs the build exactly once.
func TestExecutionLostClaimResponseRunsTheTaskOnce(t *testing.T) {
	for _, row := range []struct {
		name    string
		adjust  func(*models.File)
		pattern string
		ranOnce func(*executionRig) bool
	}{
		{
			name:    "a build claim",
			pattern: "*push*dispat-task-*",
			ranOnce: func(rig *executionRig) bool { return executionBuildsOf(rig, "core") == 1 },
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var adjust []func(*models.File)
			if row.adjust != nil {
				adjust = append(adjust, row.adjust)
			}
			rig := newExecutionPushOutcomeRig(t, adjust...)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: row.pattern, Nth: 1, After: true, Output: executionLostPushReply})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

			started := time.Now()
			res := rig.release()
			stopAll(t, []*executionWorker{worker})

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the claim push")
			assert.True(t, row.ranOnce(rig), "the task ran exactly once: %v", rig.runs())
			assert.Less(t, time.Since(started), 150*time.Second, "and nobody waited out the task deadline")
			assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
				"a claim that landed leaves nothing unknown\nstdout:\n%s", res.Stdout)
			assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
			assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
			assert.Empty(t, rig.branches(), "the run closed the branches it created")
		})
	}
}

// TestExecutionUnreadableClaimIsAdoptedWhenItSurfaces: the node's claim push
// applies and its answer is lost. Bounded exact reads that would settle it
// fail as well, so the node cannot tell whether the claim landed. It neither
// claims the work a second time nor gives it up: it remembers the claim, and
// the next poll, with the remote readable again, finds that claim on the
// branch and runs the build under it. The package is built exactly once and
// the release succeeds well inside the task deadline with nothing unknown.
func TestExecutionUnreadableClaimIsAdoptedWhenItSurfaces(t *testing.T) {
	rig := newExecutionPushOutcomeRig(t)
	// The task push is selected independently of the worker's concurrent
	// exact-ref polls. After that push begins, bounded read failures hide the
	// landed claim until a later poll can adopt it.
	push, reads := harness.NewGitFaultChain(t,
		harness.GitFault{Pattern: "*push*--force-with-lease=refs/heads/dispat-task-*",
			Nth: 1, After: true, Output: executionLostPushReply},
		harness.GitFault{Pattern: "*ls-remote*refs/heads/dispat-task-*",
			ArmAfter: "*push*--force-with-lease=refs/heads/dispat-task-*",
			Nth:      1, Onward: true, Through: 12, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, push.Env()...)

	started := time.Now()
	res := rig.release()
	served := worker.stop(t)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, push.Matches(), "the claim push lost its response")
	assert.GreaterOrEqual(t, reads.Matches(), 3, "the claim's exact settlement reads failed")
	assert.Contains(t, served.Stdout, "the claim push has no known outcome",
		"the node could not settle its claim\nworker:\n%s", served.Stdout)
	assert.Contains(t, served.Stdout, "a claim whose push had no known outcome is on the branch and is adopted",
		"and adopted it once it surfaced\nworker:\n%s", served.Stdout)
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the build ran once: %v", rig.runs())
	assert.Less(t, time.Since(started), 150*time.Second, "and nobody waited out the task deadline")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"an adopted claim leaves nothing unknown\nstdout:\n%s", res.Stdout)
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
	assert.Empty(t, rig.branches(), "the run closed the branches it created")
}

// TestExecutionLostAcknowledgementResponseSettlesTheWithdrawal: the release
// is interrupted while a node builds, the node stops the build and pushes its
// acknowledgement, and the push applies while its answer is lost. The node
// reads the branch instead of pushing again, so the run hears the
// acknowledgement it wrote: the withdrawn attempt is settled as acknowledged,
// with the phase the frame had reached, nothing is reported unknown, nothing is
// recorded and the lock goes back.
func TestExecutionLostAcknowledgementResponseSettlesTheWithdrawal(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{executionRecordingScript + " && sleep 600"}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 600, Cancel: 60}
		cfg.LogLevel = "debug"
	})
	// The node's second push onto the build branch: the first is its claim,
	// and the build never finishes, so the second is its acknowledgement.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*dispat-task-*", Nth: 2, After: true, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	require.Eventually(t, func() bool { return len(rig.runs()) == 1 }, 60*time.Second, 50*time.Millisecond,
		"the build did not start before the interruption")
	started.Signal(syscall.SIGINT)
	res := started.Wait()
	served := worker.stop(t)

	assert.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero")
	assert.Equal(t, 2, fault.Matches(), "the fault reached the acknowledgement push, and nothing pushed it again")
	settled, isSettled := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isSettled, "the run heard the acknowledgement whose answer was lost\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "commands", settled.Str("phase"))
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"an acknowledged withdrawal leaves nothing unknown\nstdout:\n%s", res.Stdout)
	assert.Contains(t, served.Stdout, `"message":"cancellation acknowledged"`)
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock went back")
}

// TestExecutionRefusedResultFailsTheTaskPromptly: the mailbox refuses the
// node's result the way a hook or a size limit does, with a porcelain
// `[remote rejected]` line. The node reports once more without the outputs, as
// a failure naming the stable reason, so the run fails the package with the
// integrity code long before its task deadline instead of waiting it out.
func TestExecutionRefusedResultFailsTheTaskPromptly(t *testing.T) {
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

	started := time.Now()
	res := rig.release()
	replies := stopAll(t, []*executionWorker{worker})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Less(t, time.Since(started), 150*time.Second, "the run heard an answer rather than waiting 300 s")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
		"the package failed with the integrity code\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "transfer-refused", "naming why the node could not report its outputs")
	refused, isRefused := executionLine(replies[0], "the mailbox refused the task result; a result without it is reported instead")
	require.True(t, isRefused, "the node said so\nstdout:\n%s", replies[0].Stdout)
	assert.Contains(t, refused.Str("error"), "hook declined", "with the server's reason")
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock went back")
	assert.Empty(t, rig.branches())
}

// The node's second push onto a task ref is its result, after its claim.
// The task-ref prefix excludes the separate preflight probe branch.
// This shim answers it the way a server rule refuses an update, with a
// porcelain `[remote rejected]` line for the ref and exit code 1, and never
// runs it. Every other invocation is the real git. Only the worker receives
// this shim.
const executionRefusedResultScript = `#!/bin/sh
set -eu
case "$*" in
*push*dispat-task-*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_REFUSED_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq "${DISPAT_IT_REFUSED_ORDINAL:-2}" ]; then
  for refspec in "$@"; do :; done
  printf '!\t%s\t[remote rejected] (pre-receive hook declined)\n' "$refspec"
  exit 1
 fi
 ;;
esac
exec "$DISPAT_IT_REFUSED_GIT" "$@"
`

// TestExecutionRefusedAssignmentIsOfferedAgain: the mailbox refuses the run's
// first assignment push outright, the way a server rule does, with a porcelain
// `[remote rejected]` line. The assignment provably never landed, so it is
// no attempt anybody can be working on: the run says the mailbox did not take
// it and offers the task again, and the package is built once and released
// with no branch left behind.
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
	refused, isRefused := executionLine(res, "the mailbox did not take the assignment")
	require.True(t, isRefused, "the run said so\nstdout:\n%s", res.Stdout)
	assert.Contains(t, refused.Str("error"), "hook declined", "with the server's reason")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the package was built once: %v", rig.runs())
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
	assert.Empty(t, rig.branches(), "no coordination branch outlives the run")
}

// TestExecutionRefusedClaimIsOfferedAgain: the mailbox refuses the node's
// claim outright with a porcelain `[remote rejected]` line. The claim provably
// never landed, so the node says the mailbox refused it, with the server's
// reason, and leaves the work alone; the run, hearing no claim within its task
// deadline, revokes the assignment and offers the task again, which the node
// claims. The package is built once and released.
func TestExecutionRefusedClaimIsOfferedAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusing fixture uses a POSIX shell")
	}
	wait := 8
	if harness.IsTinyGo() {
		wait = 40
	}
	rig := newExecutionPushOutcomeRig(t, func(cfg *models.File) {
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: 20}
	})
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionRefusedResultScript), 0o755))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_REFUSED_GIT="+realGit, "DISPAT_IT_REFUSED_DIR="+shim, "DISPAT_IT_REFUSED_ORDINAL=1")

	res := rig.release()
	replies := stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refused, isRefused := executionLine(replies[0], "the mailbox refused this node's claim")
	require.True(t, isRefused, "the node said so\nworker:\n%s", replies[0].Stdout)
	assert.Contains(t, refused.Str("error"), "hook declined", "with the server's reason")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the package was built once: %v", rig.runs())
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock goes back")
	assert.Empty(t, rig.branches(), "no coordination branch outlives the run")
}

// TestExecutionLostActivationReplySettlesOrFailsClosed: an atomic task and
// notice activation whose reply is lost can continue when both exact updates
// landed. A reply with no applied update remains uncertain: an in-flight
// request may still land after bounded reads, so the run fails closed without
// authorizing another attempt. Both paths clean their owned refs.
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
			} else {
				require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Contains(t, res.Stdout, "the atomic task activation has no known outcome")
				assert.Zero(t, executionBuildsOf(rig, "core"), "uncertain placement starts no task")
				assert.Empty(t, rig.repo.TagList(), "uncertain placement records no release")
			}
			assert.Empty(t, rig.branches(), "no coordination branch outlives the run")
			assert.Empty(t, executionMailboxBranches(t, rig.mailbox))
		})
	}
}

// TestExecutionHungMailboxStillGivesTheLockBack: the mailbox stops answering
// after preflight, while a node is building, and the release is interrupted.
// The withdrawal is bounded by the cancel wait and the run's close by its own
// bound, so the release ends and gives its lock back instead of waiting on a
// remote that will never answer.
func TestExecutionHungMailboxStillGivesTheLockBack(t *testing.T) {
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
		"DISPAT_IT_HANG_MAILBOX="+rig.mailbox), "release")

	executionAwaitProbe(t, rig, executionNode)
	require.NoError(t, os.WriteFile(filepath.Join(shim, "hang"), nil, 0o600))
	interrupted := time.Now()
	started.Signal(syscall.SIGINT)
	res := started.Wait()
	elapsed := time.Since(interrupted)
	stopAll(t, []*executionWorker{worker})

	require.NotEqual(t, 0, res.Code, "an interrupted release exits non-zero\nstdout:\n%s", res.Stdout)
	assert.Less(t, elapsed, 150*time.Second, "the withdrawal and the close ended within their bounds")
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock went back\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionRetainedCode),
		"the branch the hung mailbox kept is reported\nstdout:\n%s", res.Stdout)
	assert.Empty(t, executionTransportRefs(t, rig.repo.Root), "no fetched transport ref is left behind")
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
