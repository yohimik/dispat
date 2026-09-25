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
// and its answer is lost. The node reads the branch, finds its own claim there
// and runs the task; it neither leaves the work for the orchestrator to wait
// out nor claims it a second time.
func TestExecutionLostClaimResponseRunsTheTaskOnce(t *testing.T) {
	rig := newExecutionPushOutcomeRig(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*[0-9]-build-*", Nth: 1, After: true, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	started := time.Now()
	res := rig.release()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the claim push")
	assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the task ran exactly once: %v", rig.runs())
	assert.Less(t, time.Since(started), 150*time.Second, "and nobody waited out the task deadline")
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
	assert.Empty(t, rig.branches(), "the run closed the branches it created")
}

// TestExecutionLostReadyResponsePublishes: a publishing node's ready push
// applies and its answer is lost. The node reads its ready on the branch and
// waits for the authorization as it would have, so the package publishes and
// is recorded, no unknown outcome is reported and the lock goes back.
func TestExecutionLostReadyResponsePublishes(t *testing.T) {
	rig := newExecutionPushOutcomeRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
	})
	// The node's first push onto the publish branch is its claim and the
	// second is its ready.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*[0-9]-publish-*", Nth: 2, After: true, Output: executionLostPushReply})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	res := rig.release()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, fault.Matches(), "the fault reached the ready push")
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"a ready that landed is no unknown publication\nstdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"), "the command ran once")
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "the publication is recorded: %v", rig.repo.TagList())
	assert.False(t, remoteHoldsLock(t, rig.origin), "and the lock goes back")
	assert.Empty(t, rig.branches())
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

// The node's second push onto a build branch is its result, after its claim.
// The date before the kind is what keeps the node's own name, build-a, from
// matching its probe branch too.
// This shim answers it the way a server rule refuses an update, with a
// porcelain `[remote rejected]` line for the ref and exit code 1, and never
// runs it. Every other invocation is the real git. Only the worker receives
// this shim.
const executionRefusedResultScript = `#!/bin/sh
set -eu
case "$*" in
*push*[0-9]-build-*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_REFUSED_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq 2 ]; then
  for refspec in "$@"; do :; done
  printf '!\t%s\t[remote rejected] (pre-receive hook declined)\n' "$refspec"
  exit 1
 fi
 ;;
esac
exec "$DISPAT_IT_REFUSED_GIT" "$@"
`

// TestExecutionLostAssignmentCreateLeavesNoBranch: the orchestrator's first
// assignment push loses its answer, once after it applied and once without
// applying. Either way the run settles it by reading the branch: an
// assignment on it goes on as offered, and one that never landed is revoked
// under a lease on itself and offered again. The package is built once, the
// release succeeds, and no coordination branch outlives the run.
func TestExecutionLostAssignmentCreateLeavesNoBranch(t *testing.T) {
	for name, isApplied := range map[string]bool{"applied": true, "never applied": false} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionPushOutcomeRig(t)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*push*[0-9]-build-*", Nth: 1, After: isApplied, Output: executionLostPushReply})

			res := rig.release(fault.Env()...)
			stopAll(t, []*executionWorker{worker})

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the assignment push")
			assert.Equal(t, 1, executionBuildsOf(rig, "core"), "the package was built once: %v", rig.runs())
			assert.True(t, rig.repo.IsTagged("core@0.1.0"), "tags: %v", rig.repo.TagList())
			assert.Empty(t, rig.branches(), "no coordination branch outlives the run")
			assert.Empty(t, executionMailboxBranches(t, rig.mailbox))
		})
	}
}

// TestExecutionSlowResultPushDoesNotStarveAnAuthorization: a node of capacity
// two pushes one task's result slowly, the way a large output set travels,
// while it waits for another task's publication authorization. The mailbox is
// not held across the transfer, so the publisher reads its authorization and
// runs its command while the first push is still in flight.
//
// The first push is held until the publication has run, so a node that
// serialized its mailbox behind the transfer would never get there.
func TestExecutionSlowResultPushDoesNotStarveAnAuthorization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the held-push fixture uses a POSIX shell")
	}
	repo := harness.New(t)
	repo.SeedPackage("packages", "api")
	repo.SeedPackage("packages", "core")
	mailbox := executionMailbox(t)
	cfg := libsConfig(executionRecordingScript, 2)
	cfg.LogLevel = "debug"
	cfg.Scripts["publish"] = models.Script{executionPublishProbe}
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 300, Cancel: 20},
	}
	pinPackages(map[string]*models.RunOnly{
		"api":  placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker),
		"core": placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator),
	})(&cfg)
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(api,core): bootstrap")
	rig := newExecutionRigOver(t, repo, mailbox)

	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	release := filepath.Join(shim, "release")
	require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionHeldResultScript), 0o755))
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(2) }), 0,
		"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_HELD_GIT="+realGit, "DISPAT_IT_HELD_DIR="+shim)
	started := rig.repo.StartReleaseEnv(rig.env(), "release")

	executionAwaitProbe(t, rig, "probe-publish")
	_, err = os.Stat(filepath.Join(shim, "held"))
	require.NoError(t, err, "the result push was in flight when the publication ran")
	require.NoError(t, os.WriteFile(release, nil, 0o600))
	res := started.Wait()
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, executionNode, rig.nodesByPackage()["core"], "core was built on the node: %v", rig.runs())
	assert.Equal(t, []string{"api"}, executionProbedPackagesOn(rig, executionNode),
		"api was published on the node while core's result push was held")
	assert.ElementsMatch(t, []string{"api@0.1.0", "core@0.1.0"}, executionReleaseTags(rig))
	assert.Empty(t, rig.branches())
}

// executionProbedPackagesOn is every package whose publish probe fired on one
// node.
func executionProbedPackagesOn(rig *executionRig, node string) []string {
	var names []string
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+"publish" && run.Dir == node {
			names = append(names, run.Package)
		}
	}
	return names
}

// The node's second push onto a build branch is its result, after its claim.
// This shim holds it until the test says so, marking that it is holding, and
// then runs it. Only the worker receives this shim.
const executionHeldResultScript = `#!/bin/sh
set -eu
case "$*" in
*push*[0-9]-build-*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_HELD_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq 2 ]; then
  : > "$DISPAT_IT_HELD_DIR/held"
  ticks=0
  while [ ! -f "$DISPAT_IT_HELD_DIR/release" ] && [ "$ticks" -lt 3000 ]; do
   ticks=$((ticks + 1))
   sleep 0.1
  done
 fi
 ;;
esac
exec "$DISPAT_IT_HELD_GIT" "$@"
`

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
