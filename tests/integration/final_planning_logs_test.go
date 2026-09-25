// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalPlanningWorkloadIsDebugOnlyAndPreservesThePlan proves that enabling
// production performance diagnostics reports the work actually performed,
// while preserving the plan and keeping the normal output quiet.
func TestFinalPlanningWorkloadIsDebugOnlyAndPreservesThePlan(t *testing.T) {
	for _, kind := range []string{"single repository", "source histories"} {
		t.Run(kind, func(t *testing.T) {
			var r *harness.Repo
			if kind == "single repository" {
				r = singlePackageRepo(t, echoBuild)
				r.Commit("feat(core): bootstrap")
			} else {
				r = finalPolyrepo(t).control
			}
			quiet := r.StatusOK()
			traced := r.StatusOK("--log-level", "debug")
			assert.Equal(t, plannedPackages(quiet), plannedPackages(traced))
			before, after := harness.GraphLine(quiet.Events, "core"), harness.GraphLine(traced.Events, "core")
			require.NotEmpty(t, before)
			require.NotEmpty(t, after)
			for _, field := range []string{"version", "bump", "channel", "reason", "ownCommits", "dueToProviders"} {
				assert.Equal(t, before[field], after[field], field)
			}
			for _, e := range quiet.Events {
				assert.NotEqual(t, "planning workload", e.Str("message"))
			}
			var counts []harness.Event
			for _, e := range traced.Events {
				if e.Str("message") == "planning workload" {
					counts = append(counts, e)
				}
			}
			require.Len(t, counts, 1)
			work := counts[0]
			assert.Equal(t, "debug", work.Str("level"))
			elapsed, ok := work["elapsed"].(float64)
			require.True(t, ok, "elapsed must be numeric")
			assert.GreaterOrEqual(t, elapsed, float64(0))
			assert.Empty(t, work.Str("error"))
			assert.Equal(t, float64(1), work["tagInventories"])
			for _, field := range []string{"commitWindows", "uniqueCommits", "windowCommitRefs"} {
				n, ok := work[field].(float64)
				require.True(t, ok, "%s must be a numeric count", field)
				assert.Positive(t, n, field)
			}
			if kind == "source histories" {
				assert.Positive(t, work["canonicalBytes"], "the retained source history must be accounted for")
			}
			assert.Empty(t, r.TagList(), "diagnostics never write a release record")
		})
	}
	t.Run("failed history still reports partial workload and the error", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): bootstrap")
		fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*log --format=* HEAD*"})
		res := r.CommandEnv(fault.Env(), "status", "--log-level", "debug", "--log-format", "json")
		require.NotZero(t, res.Code)
		var workload, failure int
		for _, event := range res.Events {
			if event.Str("message") == "planning workload" {
				workload++
				assert.Equal(t, "debug", event.Str("level"))
				assert.Contains(t, event.Str("error"), harness.GitFaultMarker)
			}
			if event.Str("message") == "planning failed" && event.Str("level") == "error" {
				failure++
			}
		}
		assert.Equal(t, 1, workload)
		assert.Equal(t, 1, failure)
		assert.Empty(t, r.TagList())
	})
}
