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

// TestExecutionPublishInputPreparationFaults isolates the input snapshot made
// after a local build and before a delegated publication. A failed snapshot
// commit, snapshot offer or relay of the build's own output must never
// authorize the publisher, and a later run must be able to finish the same
// release without stale transport or release locks.
func TestExecutionPublishInputPreparationFaults(t *testing.T) {
	for name, row := range map[string]struct {
		pattern      string
		buildOutputs bool
	}{
		"snapshot cannot be committed":  {pattern: "*commit-tree*dispat transport snapshot*"},
		"snapshot cannot be offered":    {pattern: "*push*[0-9]-snapshot-*"},
		"own outputs cannot be relayed": {pattern: "*push*-relay-*", buildOutputs: true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.RunOnly = placedOn(models.RunOnlyOrchestrator, models.RunOnlyWorker)
				cfg.Scripts["publish"] = models.Script{executionPublishProbe}
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 25}
				if row.buildOutputs {
					cfg.BuildOutputs = []string{"dist"}
					cfg.Scripts["build"] = models.Script{"mkdir -p dist && printf 'built\\n' > dist/bundle.js"}
					cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
				}
			})
			if row.buildOutputs {
				rig.repo.WriteFile(".gitignore", "dist/\n")
				rig.repo.Commit("chore(core): ignore build outputs")
			}
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: row.pattern, Nth: 1, Onward: true})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

			failed := rig.release(fault.Env()...)

			require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.Positive(t, fault.Matches(), "the publication input fault was exercised")
			assert.True(t, harness.IsCodePresent(executionEvents(failed), executionIntegrityCode),
				"the failed preparation refuses the task with E227\nstdout:\n%s", failed.Stdout)
			assert.Empty(t, executionAuthorizations(failed), "the worker was never authorized")
			assert.Empty(t, executionProbeValues(rig, "publish"), "no publish command ran")
			assert.Empty(t, executionReleaseTags(rig), "no release was recorded")
			assert.Empty(t, rig.branches(), "the known failed attempt closed its transport refs")
			assert.False(t, remoteHoldsLock(t, rig.origin), "the failed attempt released its lock")

			retried := rig.release()
			require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
			assert.Equal(t, []string{"core@0.1.0"}, executionReleaseTags(rig))
			assert.Equal(t, executionNode, executionProbeValues(rig, "publish")["core"])
			assert.Len(t, executionAuthorizations(retried), 1, "only the healthy run authorized a publish")
			assert.Empty(t, rig.branches())
			assert.False(t, remoteHoldsLock(t, rig.origin))
			stopAll(t, []*executionWorker{worker})
		})
	}
}
