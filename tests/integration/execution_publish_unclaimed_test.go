// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

// TestExecutionUnclaimedPublicationCanRetry: a worker may disappear after
// preflight while a local build runs. An unclaimed publication must expire
// without a permit or retained lock, then release once on a healthy next run.
func TestExecutionUnclaimedPublicationCanRetry(t *testing.T) {
	signals := t.TempDir()
	built := filepath.Join(signals, "built")
	continueBuild := filepath.Join(signals, "continue")
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 5, Cancel: 2}
		cfg.Scripts["build"] = models.Script{fmt.Sprintf(
			"touch %q; while [ ! -f %q ]; do sleep 0.05; done", built, continueBuild)}
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
	})
	t.Cleanup(func() { _ = os.WriteFile(continueBuild, nil, 0o600) })
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	running := rig.repo.StartReleaseEnv(rig.env(), "release")
	require.Eventually(t, func() bool { _, err := os.Stat(built); return err == nil }, 20*time.Second, 20*time.Millisecond)
	worker.stop(t)
	require.NoError(t, os.WriteFile(continueBuild, nil, 0o600))

	failed := running.Wait()

	require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout, "no node claimed this task")
	assert.Empty(t, executionAuthorizations(failed))
	assert.Empty(t, executionProbeValues(rig, "publish"))
	assert.Empty(t, rig.repo.TagList())
	assert.Empty(t, rig.branches())
	assert.Empty(t, rig.repo.Git("ls-remote", "--refs", "origin", "refs/tags/dispat-release-lock"))

	worker = rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	retried := rig.release()
	require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.Equal(t, 1, rig.repo.TagCount("core@0.1.0"))
	assert.Len(t, executionAuthorizations(retried), 1)
	assert.Equal(t, executionNode, executionProbeValues(rig, "publish")["core"])
	assert.Empty(t, rig.branches())
	worker.stop(t)
}
