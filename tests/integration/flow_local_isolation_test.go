// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Two unordered producers may run at once on the orchestrator. Each consumes
// the build's admitted bytes, sees none of its peer's in-flight bytes, and the
// local publisher sees both completed output sets together.
func TestExecutionLocalParallelStagesHaveIsolatedOutputs(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		two := 2
		cfg.Concurrency = []int{2, 1}
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Concurrency = &two
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator, Publish: models.RunOnlyOrchestrator}
		cfg.BuildOutputs = []string{"dist"}
		cfg.Scripts["build"] = models.Script{`test -f dist/seed && mkdir -p dist && echo base > dist/base &&
echo "DIST_DIR=$PWD/dist" >> "$DISPAT_OUTPUT"`}
		cfg.Scripts["left"] = models.Script{`printf 'left %s\n' "$PWD" >> "$DISPAT_IT_EXECUTION_LOG" &&
test -f dist/seed && test -f dist/base && test -f "$DISPAT_OUTPUT_DIST_DIR/base" && mkdir -p dist &&
echo left > dist/left && touch "$DISPAT_IT_EXECUTION_LOG.left" &&
i=0; while [ ! -f "$DISPAT_IT_EXECUTION_LOG.right" ] && [ "$i" -lt 100 ]; do sleep 0.1; i=$((i+1)); done &&
test -f "$DISPAT_IT_EXECUTION_LOG.right" && test ! -e dist/right`}
		cfg.Scripts["right"] = models.Script{`printf 'right %s\n' "$PWD" >> "$DISPAT_IT_EXECUTION_LOG" &&
test -f dist/seed && test -f dist/base && test -f "$DISPAT_OUTPUT_DIST_DIR/base" && mkdir -p dist &&
echo right > dist/right && touch "$DISPAT_IT_EXECUTION_LOG.right" &&
i=0; while [ ! -f "$DISPAT_IT_EXECUTION_LOG.left" ] && [ "$i" -lt 100 ]; do sleep 0.1; i=$((i+1)); done &&
test -f "$DISPAT_IT_EXECUTION_LOG.left" && test ! -e dist/left`}
		cfg.Scripts["publish"] = models.Script{`test -f dist/seed && test -f dist/base && test -f dist/left && test -f dist/right &&
test "$DISPAT_OUTPUT_DIST_DIR" = "$PWD/dist"`}
		space := cfg.Spaces["libs"]
		space.Flow.Stages = map[string]models.StageConfig{
			"left": {Script: []string{"left"}}, "right": {Script: []string{"right"}},
		}
		cfg.Spaces["libs"] = space
	})
	rig.repo.WriteFile(".gitignore", "dist/\n")
	rig.repo.WriteFile("packages/core/dist/seed", "seed\n")
	rig.repo.Git("add", "-f", "packages/core/dist/seed")
	rig.repo.Commit("chore(core): ignore declared outputs")
	worker := rig.startWorkers([]string{executionNode}, 1)

	result := rig.release()
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	data, err := os.ReadFile(rig.builds)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)
	assert.NotEqual(t, strings.Fields(lines[0])[1], strings.Fields(lines[1])[1],
		"unordered stages must have distinct checkouts")
	for _, line := range lines {
		assert.Contains(t, line, "dispat-local-frame-", "the stage ran in a private source checkout")
	}
	assert.True(t, rig.repo.IsTagged("core@0.1.0"), "the merged output view reached the local publisher")
	stopAll(t, worker)
}

// A local publish still owns the source repository: a nested record step
// must write its commit and tag there, not into an isolated build checkout.
func TestExecutionLocalPublishKeepsReleaseStepAuthority(t *testing.T) {
	bin, _ := harness.Build(t)
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyOrchestrator, Publish: models.RunOnlyOrchestrator}
		cfg.BuildOutputs = []string{"dist"}
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
			Name: "local-steps", Email: "local-steps@dispat.test"}
		cfg.Scripts["build"] = models.Script{`mkdir -p dist && echo artifact > dist/artifact`}
		cfg.Scripts["publish"] = models.Script{`test -f dist/artifact`, bin + " changelog", bin + " commit --tag --push"}
	})
	rig.repo.WriteFile(".gitignore", "dist/\n")
	rig.repo.Commit("chore(core): ignore outputs")
	workers := rig.startWorkers([]string{executionNode}, 1)

	result := rig.release()

	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.True(t, rig.repo.IsTagged("core@0.1.0"))
	assert.NotEmpty(t, bareGit(t, rig.origin, "rev-parse", "refs/tags/core@0.1.0"))
	assert.True(t, harness.IsCodePresent(result.Events, "W223"),
		"the outer recorder recognized the nested publish step's tag")
	stopAll(t, workers)
}
