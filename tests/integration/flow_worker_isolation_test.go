// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// One worker slot makes alpha finish and enter the shared checkout before
// beta is dispatched. Beta still receives only its graph ancestors, not the
// completed output of its unordered peer.
func TestExecutionWorkerStageUsesCleanBaselineAndGraphInputs(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Concurrency = []int{1, 1}
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyOrchestrator}
		cfg.BuildOutputs = []string{"dist"}
		cfg.Scripts["build"] = models.Script{`test "$DISPAT_EXECUTION_NODE" = build-a &&
test -f dist/seed && echo base > dist/base && echo "DIST_DIR=$PWD/dist" >> "$DISPAT_OUTPUT"`}
		cfg.Scripts["alpha"] = models.Script{`test "$DISPAT_EXECUTION_NODE" = build-a &&
test -f "$DISPAT_OUTPUT_DIST_DIR/seed" && test -f "$DISPAT_OUTPUT_DIST_DIR/base" &&
test ! -e dist/beta && echo alpha > dist/alpha`}
		cfg.Scripts["beta"] = models.Script{`test "$DISPAT_EXECUTION_NODE" = build-a &&
test -f "$DISPAT_OUTPUT_DIST_DIR/seed" && test -f "$DISPAT_OUTPUT_DIST_DIR/base" &&
test ! -e dist/alpha && echo beta > dist/beta`}
		cfg.Scripts["publish"] = models.Script{`test -f "$DISPAT_OUTPUT_DIST_DIR/seed" &&
test -f dist/base &&
test -f dist/alpha && test -f dist/beta`}
		space := cfg.Spaces["libs"]
		space.Flow.Stages = map[string]models.StageConfig{
			"alpha": {Script: []string{"alpha"}},
			"beta":  {Script: []string{"beta"}},
		}
		cfg.Spaces["libs"] = space
	})
	rig.repo.WriteFile(".gitignore", "dist/\n")
	rig.repo.WriteFile("packages/core/dist/seed", "seed\n")
	rig.repo.Git("add", "-f", "packages/core/dist/seed")
	rig.repo.Commit("chore(core): retain source under declared output root")
	worker := rig.startWorkers([]string{executionNode}, 1)
	result := rig.release()
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.True(t, rig.repo.IsTagged("core@0.1.0"))
	stopAll(t, worker)
}
