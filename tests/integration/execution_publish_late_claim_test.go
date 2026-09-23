// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a claim that wins the queue-expiry revocation is running work,
// even when the coordinator's queue timer fired before it saw the claim.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestExecutionPublishClaimWinsQueuedRevocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git push rendezvous uses a POSIX shell")
	}
	taskWait := 5
	if harness.IsTinyGo() {
		taskWait = 15
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: taskWait, Cancel: 4}
	})
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	workerGate := newPublishClaimGate(t, realGit)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, workerGate.env...)
	t.Cleanup(func() {
		if worker != nil {
			worker.stop(t)
		}
	})
	revokeGate := newPublishRevocationGate(t, realGit)
	started := rig.repo.StartReleaseEnv(rig.env(revokeGate.env...), "release")
	isFinished := false
	t.Cleanup(func() {
		if !isFinished {
			started.Signal(os.Interrupt)
			_ = started.Wait()
		}
	})
	t.Cleanup(func() { _ = os.WriteFile(workerGate.proceed, nil, 0o600) })
	t.Cleanup(func() { _ = os.WriteFile(revokeGate.proceed, nil, 0o600) })

	require.True(t, waitForExecutionFile(workerGate.entered, 25*time.Second),
		"the worker did not reach its publication claim")
	require.True(t, waitForExecutionFile(revokeGate.entered, time.Duration(taskWait+20)*time.Second),
		"the coordinator did not try to revoke the queued publication")
	require.NoError(t, os.WriteFile(workerGate.proceed, nil, 0o600))
	require.Eventually(t, func() bool {
		branches := executionPublishBranches(rig.branches())
		if len(branches) != 1 {
			return false
		}
		chain := executionReadChain(rig.mailbox, strings.TrimPrefix(branches[0], "refs/heads/"))
		return len(chain) >= 2 && chain[0] == "assignment" && chain[1] == "claim"
	}, 20*time.Second, 20*time.Millisecond, "the node claimed before revocation resumed")
	require.NoError(t, os.WriteFile(revokeGate.proceed, nil, 0o600))

	res := started.Wait()
	isFinished = true
	node := worker.stop(t)
	worker = nil
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s\nworker stdout:\n%s\nworker stderr:\n%s",
		res.Stdout, res.Stderr, node.Stdout, node.Stderr)
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"),
		"the late claim became one authorized publication")
	assert.Len(t, executionAuthorizations(res), 1)
	assert.Equal(t, 1, rig.repo.TagCount("core@0.1.0"))
	assert.Empty(t, rig.branches(), "the run closed its claimed publication branch")
	assert.False(t, remoteHoldsLock(t, rig.origin))
}

type publishGitGate struct {
	env     []string
	entered string
	proceed string
}

func newPublishClaimGate(t *testing.T, realGit string) publishGitGate {
	t.Helper()
	dir := t.TempDir()
	gate := publishGitGate{entered: filepath.Join(dir, "claim-entered"), proceed: filepath.Join(dir, "claim-proceed")}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(`#!/bin/sh
for argument do
  case "$argument" in
    --force-with-lease=refs/heads/dispat-worker-*-publish-*:*)
      : > "$DISPAT_IT_CLAIM_ENTERED"
      attempts=0
      while [ ! -f "$DISPAT_IT_CLAIM_PROCEED" ] && [ "$attempts" -lt 900 ]; do
        sleep 0.05
        attempts=$((attempts + 1))
      done
      [ -f "$DISPAT_IT_CLAIM_PROCEED" ] || exit 99
      break ;;
  esac
done
exec "$DISPAT_IT_REAL_GIT" "$@"
`), 0o755))
	gate.env = []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DISPAT_IT_REAL_GIT=" + realGit,
		"DISPAT_IT_CLAIM_ENTERED=" + gate.entered,
		"DISPAT_IT_CLAIM_PROCEED=" + gate.proceed,
	}
	return gate
}

func newPublishRevocationGate(t *testing.T, realGit string) publishGitGate {
	t.Helper()
	dir := t.TempDir()
	gate := publishGitGate{entered: filepath.Join(dir, "revoke-entered"), proceed: filepath.Join(dir, "revoke-proceed")}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(`#!/bin/sh
for argument do
  case "$argument" in
    :refs/heads/dispat-worker-*-publish-*)
      : > "$DISPAT_IT_REVOKE_ENTERED"
      attempts=0
      while [ ! -f "$DISPAT_IT_REVOKE_PROCEED" ] && [ "$attempts" -lt 900 ]; do
        sleep 0.05
        attempts=$((attempts + 1))
      done
      [ -f "$DISPAT_IT_REVOKE_PROCEED" ] || exit 99
      break ;;
  esac
done
exec "$DISPAT_IT_REAL_GIT" "$@"
`), 0o755))
	gate.env = []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DISPAT_IT_REAL_GIT=" + realGit,
		"DISPAT_IT_REVOKE_ENTERED=" + gate.entered,
		"DISPAT_IT_REVOKE_PROCEED=" + gate.proceed,
	}
	return gate
}
