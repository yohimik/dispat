// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: distinct package identities must retain distinct worker checkouts
// even when a path-safe spelling would collapse them to the same name.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestExecutionConcurrentPackageNamesKeepSeparateWorkerCheckouts(t *testing.T) {
	// Both names became "-" under the old lossy task-folder mapper.
	names := []string{"α", "β"}
	rig := newExecutionPlacementRig(t, names, executionTimedBuild(executionStageWindow),
		func(cfg *models.File) {
			cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
			cfg.Execution.Concurrency = models.Int(2)
		})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(2) }), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	runs := rig.runs()
	require.Len(t, runs, 2, "both packages built exactly once: %v", runs)
	assert.NotEqual(t, runs[0].Dir, runs[1].Dir, "concurrent packages own different checkouts")
	for _, run := range runs {
		assert.Equal(t, executionNode, run.Node)
	}
	timeline := rig.repo.Timeline("timeline.log")
	require.Len(t, timeline, 2)
	harness.AssertOverlaps(t, timeline[0], timeline[1])
	assert.ElementsMatch(t, []string{"α@0.1.0", "β@0.1.0"}, rig.repo.TagList())
	stopAll(t, []*executionWorker{worker})
}
