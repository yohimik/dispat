// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// An ignored product left by a prior attempt is not an input to a prepared
// provider's build. Its declared consumer receives only the new admitted set,
// whether the provider is placed here or on a worker.
func TestExecutionPreparedProviderExcludesStaleIgnoredOutputAtEitherPlacement(t *testing.T) {
	for _, placement := range []struct {
		name   string
		build  string
		worker string
	}{
		{name: "local", build: models.RunOnlyOrchestrator},
		{name: "worker", build: models.RunOnlyWorker, worker: executionNode},
	} {
		t.Run(placement.name, func(t *testing.T) {
			rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
				cfg.Execution.Workers = cfg.Execution.Workers[:1]
				cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
				cfg.Scripts["build"] = models.Script{`case "$DISPAT_PACKAGE" in
  assets)
    mkdir -p dist && printf 'fresh\n' > dist/generated ;;
  ui)
    test -f ../assets/dist/generated &&
    test ! -e ../assets/dist/obsolete &&
    mkdir -p dist && cp ../assets/dist/generated dist/from-assets ;;
esac`}
			}, pinPreparedPackage("assets", placedOn(placement.build, models.RunOnlyOrchestrator)))
			rig.commitTo("the page reads prepared assets", "ui")
			rig.repo.WriteFile("packages/assets/dist/obsolete", "old ignored product\n")
			workers := rig.startWorkers([]string{executionNode}, 1)

			result := rig.release()

			require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
			reports := executionPreparedReports(result)
			require.Len(t, reports, 1)
			assert.Equal(t, placement.worker, reports[0].Str("worker"))
			assert.Equal(t, []string{"assets:" + executionPrepareKind}, executionPrepareTasks(result))
			assert.True(t, rig.repo.IsTagged("assets@"+executionPrepareBaseline))
			assert.False(t, rig.repo.IsTagged("assets@1.1.0"))
			stopAll(t, workers)
		})
	}
}
