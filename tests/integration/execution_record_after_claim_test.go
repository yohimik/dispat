// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a worker that claimed work but cannot persist its replay record
// must execute no part of the claimed publication.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestExecutionClaimWithoutDurableReplayRecordCannotPublish(t *testing.T) {
	signals := t.TempDir()
	built := filepath.Join(signals, "built")
	continueBuild := filepath.Join(signals, "continue")
	wait, cancel := 5, 2
	if harness.IsTinyGo() {
		wait, cancel = 20, 10
	}
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: wait, Cancel: cancel}
		cfg.Scripts["build"] = models.Script{fmt.Sprintf(
			"touch %q; while [ ! -f %q ]; do sleep 0.05; done", built, continueBuild)}
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
	})
	t.Cleanup(func() { _ = os.WriteFile(continueBuild, nil, 0o600) })
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	t.Cleanup(func() {
		if worker != nil {
			worker.stop(t)
		}
	})
	running := rig.repo.StartReleaseEnv(rig.env(), "release")
	isFinished := false
	t.Cleanup(func() {
		if !isFinished {
			running.Signal(os.Interrupt)
			_ = running.Wait()
		}
	})
	require.Eventually(t, func() bool { _, err := os.Stat(built); return err == nil },
		20*time.Second, 20*time.Millisecond, "preflight succeeded and the local build started")
	obstruction := filepath.Join(worker.stateDir, executionNode, "seen.json.tmp")
	require.NoError(t, os.Mkdir(obstruction, 0o755), "block only the next durable replay write")
	require.NoError(t, os.WriteFile(continueBuild, nil, 0o600))

	failed := running.Wait()
	isFinished = true
	require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Empty(t, executionAuthorizations(failed), "no effect is permitted when replay protection is not durable")
	assert.Empty(t, executionProbeValues(rig, "publish"))
	assert.Empty(t, executionReleaseTags(rig))
	assert.False(t, remoteHoldsLock(t, rig.origin))
	assert.Empty(t, rig.branches(), "the claimed but unreported attempt is fenced")

	if err := os.Remove(obstruction); err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	retried := rig.release()
	require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.Len(t, executionAuthorizations(retried), 1)
	assert.Equal(t, []string{"core"}, executionProbedPackages(rig, "publish"))
	assert.Equal(t, 1, rig.repo.TagCount("core@0.1.0"))
	reply := worker.stop(t)
	worker = nil
	assert.Contains(t, reply.Stdout+reply.Stderr, "seen.json.tmp",
		"the worker reports why it would not execute the first claim")
	assert.Contains(t, reply.Stdout, `"task":"core:publish"`, "the worker took the publish assignment")
	assert.Contains(t, reply.Stdout, `"message":"task claimed"`, "the refusal happened after a claim")
}
