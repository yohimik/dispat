// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the worker's last remote check before an irreversible publish.
// These cases pause that exact read after the worker has authenticated a valid
// authorization, then change the branch before the read completes.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionPublicationFenceSeesRemoteWithdrawal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git boundary fixture uses a POSIX shell")
	}
	for _, change := range []string{"withdrawn", "deleted"} {
		t.Run(change, func(t *testing.T) {
			rig := newExecutionRig(t)
			orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
			state := orchestrator.prepareInputState("fenced-" + change)
			branch := executionCraftedBranchName("publish", "fenced-"+change)
			assignment := orchestrator.offer(branch, orchestrator.publication(branch, "fenced", state))
			fenceDir := t.TempDir()
			shim := filepath.Join(fenceDir, "git")
			realGit, err := exec.LookPath("git")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(shim, []byte(executionAuthorizationFenceScript), 0o755))
			t.Cleanup(func() { _ = os.WriteFile(filepath.Join(fenceDir, "release"), nil, 0o600) })
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), executionRefusalIdleSeconds,
				"PATH="+fenceDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DISPAT_IT_REAL_GIT="+realGit, "DISPAT_IT_FENCE_DIR="+fenceDir)

			executionAwaitMessage(t, rig.mailbox, branch, "ready")
			ready := executionTipOID(t, rig.mailbox, branch)
			require.NoError(t, os.WriteFile(filepath.Join(fenceDir, "ready"), []byte(ready), 0o600))
			authorized := orchestrator.answer(branch, "go", ready,
				orchestrator.authorization(branch, "fenced", assignment, ready))
			require.Eventually(t, func() bool {
				_, err := os.Stat(filepath.Join(fenceDir, "at-fence"))
				return err == nil
			}, 30*time.Second, 10*time.Millisecond,
				"the worker must reach its final remote read after seeing the authorization")

			if change == "withdrawn" {
				orchestrator.answer(branch, "cancel", authorized,
					orchestrator.withdrawal(branch, "fenced", assignment, authorized))
			} else {
				bareGit(t, rig.mailbox, "update-ref", "-d", "refs/heads/"+branch)
			}
			require.NoError(t, os.WriteFile(filepath.Join(fenceDir, "release"), nil, 0o600))
			if change == "withdrawn" {
				ack := executionAwaitMessage(t, rig.mailbox, branch, "ack")
				assert.Equal(t, "authorization-wait", ack["phase"])
				assert.NotEqual(t, true, ack["commandStarted"])
			}
			reply := executionServeUntilIdle(t, worker)
			assert.Empty(t, executionRecordedTasks(rig, "published"),
				"the command cannot start after the remote fence changed: %v", rig.runs())
			assert.Contains(t, reply.Stdout, "the publication branch no longer carries the authorization")
			if change == "deleted" {
				assert.NotContains(t, rig.branches(), branch)
			} else {
				assert.Equal(t, []string{"assignment", "claim", "ready", "go", "cancel", "ack"},
					executionChain(t, rig.mailbox, branch))
			}
		})
	}
}

// The first branch-specific ls-remote that sees a tip other than ready hands
// the authorization to the worker. Its next such read is the effect-site
// fence; wait there until the test has withdrawn or deleted the branch. The
// command is re-run after that change so the worker sees the actual new tip.
const executionAuthorizationFenceScript = `#!/bin/sh
set -eu
case "$*" in
*ls-remote*'-publish-'*)
 out="$("$DISPAT_IT_REAL_GIT" "$@")" || exit "$?"
 if [ -f "$DISPAT_IT_FENCE_DIR/ready" ]; then
  ready="$(cat "$DISPAT_IT_FENCE_DIR/ready")"
  case "$out" in
  "$ready"*) ;;
  *)
   if [ ! -f "$DISPAT_IT_FENCE_DIR/saw-go" ]; then
    : > "$DISPAT_IT_FENCE_DIR/saw-go"
   elif [ ! -f "$DISPAT_IT_FENCE_DIR/released" ]; then
    : > "$DISPAT_IT_FENCE_DIR/at-fence"
    ticks=0
    while [ ! -f "$DISPAT_IT_FENCE_DIR/release" ]; do
     ticks=$((ticks + 1))
     [ "$ticks" -lt 3000 ] || exit 2
     sleep 0.01
    done
    : > "$DISPAT_IT_FENCE_DIR/released"
    exec "$DISPAT_IT_REAL_GIT" "$@"
   fi
  esac
 fi
 printf '%s\n' "$out"
 exit 0
 ;;
esac
exec "$DISPAT_IT_REAL_GIT" "$@"
`
