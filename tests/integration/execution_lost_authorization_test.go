// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionLostAuthorizationResponseRetainsExclusion proves that a failed
// push response cannot undo an authorization a worker has already consumed.
// A vanished or rewound branch cannot prove the command never started.
func TestExecutionLostAuthorizationResponseRetainsExclusion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lost-response fixture uses a POSIX shell")
	}
	for _, state := range []string{"visible", "deleted", "rewound"} {
		t.Run(state, func(t *testing.T) {
			rig := newExecutionSlowPublishRig(t)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
			shim := t.TempDir()
			realGit, err := exec.LookPath("git")
			require.NoError(t, err)
			resume := filepath.Join(shim, "resume")
			require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionLostAuthorizationScript), 0755))
			t.Cleanup(func() { _ = os.WriteFile(resume, nil, 0600) })
			started := rig.repo.StartReleaseEnv(rig.env(
				"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DISPAT_IT_LOST_GIT="+realGit,
				"DISPAT_IT_LOST_DIR="+shim), "release")
			executionAwaitProbe(t, rig, "probe-publish")
			branches := executionPublishBranches(rig.branches())
			require.Len(t, branches, 1)
			if state == "rewound" {
				ready := strings.TrimSpace(rig.repo.Git("--git-dir="+rig.mailbox, "rev-parse", branches[0]+"^"))
				rig.repo.Git("--git-dir="+rig.mailbox, "update-ref", branches[0], ready)
			}
			if state == "deleted" {
				rig.repo.Git("--git-dir="+rig.mailbox, "update-ref", "-d", branches[0])
				require.NotContains(t, rig.branches(), branches[0])
			}
			require.NoError(t, os.WriteFile(resume, nil, 0600))
			res := started.Wait()
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode), "stdout:\n%s", res.Stdout)
			assert.True(t, remoteHoldsLock(t, rig.origin), "the publisher already started and has not acknowledged stopping")
			assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"), "the effect starts once")
			assert.Empty(t, executionReleaseTags(rig), "an unknown effect has no successful release record")
			if state != "deleted" {
				assert.Contains(t, rig.branches(), branches[0], "retain the authorization evidence")
			}
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// The real authorization push applies, but its response is withheld until the
// test observes the publish command. This orders the failure without a sleep
// race. Only the orchestrator receives this shim.
const executionLostAuthorizationScript = `#!/bin/sh
set -eu
case "$*" in
*push*'-publish-'*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_LOST_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq 2 ]; then
  "$DISPAT_IT_LOST_GIT" "$@" >/dev/null 2>&1 || exit "$?"
  ticks=0
  while [ ! -f "$DISPAT_IT_LOST_DIR/resume" ]; do
   ticks=$((ticks + 1))
   [ "$ticks" -lt 900 ] || exit 2
   sleep 0.1
  done
  echo 'the authorization response was lost' >&2
  exit 1
 fi
 ;;
esac
exec "$DISPAT_IT_LOST_GIT" "$@"
`

// TestExecutionRefusedAuthorizationPushWithdrawsThePublisher: the remote
// answers the authorization push with a porcelain rejection, so the branch
// never took the authorization and no node can have read it. That is not an
// unknown outcome. The run withdraws the waiting publisher as it withdraws one
// whose authorization was refused, the node answers from its gate that nothing
// started, the package fails at the authorization, and the lock goes back.
func TestExecutionRefusedAuthorizationPushWithdrawsThePublisher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refused-push fixture uses a POSIX shell")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 120, Cancel: 30}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	shim := t.TempDir()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(shim, "pushes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(executionRefusedAuthorizationScript), 0o755))

	res := rig.release("PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_REFUSED_GIT="+realGit, "DISPAT_IT_REFUSED_DIR="+shim)
	stopAll(t, []*executionWorker{worker})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	failed, isFailed := executionLine(res, "pre-publish repository validation failed")
	require.True(t, isFailed, "the package failed at the authorization\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "core", failed.Str("package"))
	withheld, isWithheld := executionLine(res, "publication withheld")
	require.True(t, isWithheld, "the waiting publisher was withdrawn\nstdout:\n%s", res.Stdout)
	assert.Equal(t, executionNode, withheld.Str("worker"))
	settled, isSettled := executionLine(res, "the withdrawn attempt was acknowledged")
	require.True(t, isSettled, "the node answered from its gate\nstdout:\n%s", res.Stdout)
	assert.Equal(t, "authorization-wait", settled.Str("phase"))
	assert.False(t, harness.IsCodePresent(executionEvents(res), executionPublicationUnknownCode),
		"a refused push is not an unknown publication\nstdout:\n%s", res.Stdout)
	_, isRetained := executionLine(res, executionRetainedLockMessage)
	assert.False(t, isRetained, "so no exclusion is left behind")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the lock was given back")
	assert.Empty(t, executionProbedPackages(rig, "publish"), "no publish command ran: %v", rig.runs())
	assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
}

// The orchestrator's second push onto a publish branch is its authorization.
// This shim answers it the way a remote refusing the lease does, with a
// porcelain rejection line for the ref and exit code 1, and never runs it, so
// the branch provably never carries the authorization. Every other invocation
// is the real git. Only the orchestrator receives this shim.
const executionRefusedAuthorizationScript = `#!/bin/sh
set -eu
case "$*" in
*push*'-publish-'*)
 ordinal=1
 while ! mkdir "$DISPAT_IT_REFUSED_DIR/pushes/$ordinal" 2>/dev/null; do ordinal=$((ordinal + 1)); done
 if [ "$ordinal" -eq 2 ]; then
  for refspec in "$@"; do :; done
  printf '!\t%s\t[rejected] (stale info)\n' "$refspec"
  exit 1
 fi
 ;;
esac
exec "$DISPAT_IT_REFUSED_GIT" "$@"
`
