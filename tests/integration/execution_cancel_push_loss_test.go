// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a lost response to a successful withdrawal is not permission to
// stack another withdrawal or discard an unanswered publication's evidence.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionLostWithdrawalPushResponseRetainsUnknownAuthorization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git response fault uses a POSIX shell")
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.LogLevel = "debug"
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 45, Cancel: 5}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, nil)
	worker.isReadyOnly = true
	worker.serve()
	t.Cleanup(worker.close)
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	directory := t.TempDir()
	trace := filepath.Join(directory, "publish-pushes")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "git"), []byte(`#!/bin/sh
for argument do
 case "$argument" in
  --force-with-lease=refs/heads/dispat-worker-build-a-*-publish-*:*)
   printf '%s\n' "$argument" >> "$DISPAT_IT_WITHDRAWAL_PUSHES"
   count=$(wc -l < "$DISPAT_IT_WITHDRAWAL_PUSHES")
   if [ "$count" -eq 3 ]; then
    "$DISPAT_IT_REAL_GIT" "$@" >/dev/null 2>&1 || exit "$?"
    printf '%s\n' 'injected lost withdrawal response' >&2
    exit 97
   fi
   break ;;
 esac
done
exec "$DISPAT_IT_REAL_GIT" "$@"
`), 0o755))
	env := append(rig.env(), "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DISPAT_IT_REAL_GIT="+realGit, "DISPAT_IT_WITHDRAWAL_PUSHES="+trace)
	release := rig.repo.StartReleaseEnv(env, "release")
	isFinished := false
	t.Cleanup(func() {
		if !isFinished {
			release.Signal(syscall.SIGINT)
			_ = release.Wait()
		}
	})
	branch := executionAwaitBranch(t, rig, "publish")
	executionAwaitMessage(t, rig.mailbox, branch, "go")
	release.Signal(syscall.SIGINT)
	result := release.Wait()
	isFinished = true
	require.NotEqual(t, 0, result.Code, "stdout:\n%s", result.Stdout)
	pushes, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.Len(t, strings.Fields(string(pushes)), 3, "assignment, Go and one withdrawal only")
	assert.Equal(t, []string{"assignment", "claim", "ready", "go", "cancel"},
		executionChain(t, rig.mailbox, branch), "the successful withdrawal is recognized, never repeated")
	_, isWithdrawn := executionLine(result, "attempt withdrawn")
	assert.True(t, isWithdrawn, "the durable cancellation is recognized\nstdout:\n%s", result.Stdout)
	_, isUnknown := executionLine(result, executionUnknownPublicationMessage)
	assert.True(t, isUnknown, "an unacknowledged authorization remains unknown\nstdout:\n%s", result.Stdout)
	assert.True(t, remoteHoldsLock(t, rig.origin), "retain the lock until the publisher is reconciled")
	assert.Empty(t, executionReleaseTags(rig))
	assert.Empty(t, executionProbedPackages(rig, "publish"))
	assert.Equal(t, []string{"refs/heads/" + branch}, rig.branches(), "retain the authorization and withdrawal evidence")
}
