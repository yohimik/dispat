// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A valid but stale HEAD answer from the planner must not become the release
// snapshot accepted by fleet composition, even when both commits exist.
func TestFleetPlanningHeadMismatchStopsBeforePublication(t *testing.T) {
	fleet := finalPolyrepo(t)
	marker := fleet.control.Path("published.txt")
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"},
		"publish": []string{"echo published >> " + harness.ShQuote(marker)},
	}
	writePolyrepoJSON(t, fleet.control, "dispat.json", cfg)
	fleet.control.Commit("chore: make publication observable")
	first := fleet.control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	fleet.control.WriteFile("sources/lib/packages/core/later.txt", "more\n")
	commitPolyrepoSource(t, fleet.control, "sources/lib", "feat(core): later source work")
	checkpointPolyrepoSource(t, fleet.control, "sources/lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib *rev-parse HEAD^{commit}*", Output: first,
	})

	failed := fleet.control.CommandEnv(fault.Env(), "release")
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout+failed.Stderr, "repository changed after workspace composition")
	assert.True(t, harness.IsCodePresent(failed.Events, "E330"), "stdout:\n%s", failed.Stdout)
	assert.Equal(t, 1, fault.Matches())
	assert.Empty(t, polyrepoTags(fleet.control, "sources/lib"), "a stale planning answer cannot publish")
	assert.Empty(t, fleet.control.TagList())
	assert.NoFileExists(t, marker, "the publisher did not run against an unproven snapshot")

	ready := fleet.control.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(ready.Events, "core").Str("version"))
	recovered := fleet.control.Release()
	require.Zero(t, recovered.Code, "stdout:\n%s\nstderr:\n%s", recovered.Stdout, recovered.Stderr)
	assert.Contains(t, polyrepoTags(fleet.control, "sources/lib"), "core@0.1.0")
	publication, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "published\n", string(publication), "healthy retry publishes exactly once")
}
