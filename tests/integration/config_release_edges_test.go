// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Configuration failures whose meaning depends on composing a real central
// workspace rather than decoding one file in isolation.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// External CI configuration keeps package paths anchored to --root while an
// absolute --config names exactly one file, without falling back to a local one.
func TestConfigAbsoluteFileKeepsTheRequestedRepositoryRoot(t *testing.T) {
	repo := singlePackageRepo(t, echoBuild)
	repo.Commit("feat(core): seed package")
	body, err := os.ReadFile(repo.Path("dispat.json"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "release.json")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	repo.WriteFile("dispat.json", "{broken local configuration")
	result := repo.Status("--config", path)
	require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Equal(t, []string{"core"}, plannedPackages(result))
	assert.Empty(t, repo.TagList())
	assert.Equal(t, "{broken local configuration", readRepoFile(t, repo, "dispat.json"))
}

// TestConfigReleaseRejectsAShallowImportedPolicyOwner proves a source must
// have complete history at the point its repository-owned policy is admitted.
// No package planning may occur against a shallow policy owner.
func TestConfigReleaseRejectsAShallowImportedPolicyOwner(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "sdk")
	writePolyrepoJSON(t, source, "dispat.json", map[string]any{
		"packages": map[string]any{"sdk": map[string]any{"path": "packages/sdk"}},
	})
	source.Commit("feat(sdk): add imported package")

	control := harness.New(t)
	addPolyrepoSource(t, control, "sdk-source", "sources/sdk", source)
	cfg := polyrepoFile()
	cfg["configs"] = []string{"sources/sdk/dispat.json"}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: import source policy")

	head := control.Git("-C", "sources/sdk", "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(control.Path(".git", "modules", "sdk-source", "shallow"),
		[]byte(head+"\n"), 0o644))
	require.Equal(t, "true", control.Git("-C", "sources/sdk", "rev-parse", "--is-shallow-repository"))

	res := control.Status("--package", "*")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E330")
	assert.Contains(t, res.Stdout+res.Stderr, "sdk-source")
	assert.Contains(t, res.Stdout+res.Stderr, "is shallow; complete history is required")
	assert.Empty(t, plannedPackages(res))
}

// TestConfigReleaseRejectsUnreadableExclusionPolicy proves configuration
// discovery cannot ignore a broken .dispatexclude and silently choose one of
// several candidate files without applying its exclusions.
func TestConfigReleaseRejectsUnreadableExclusionPolicy(t *testing.T) {
	repo := harness.New(t)
	repo.SeedPackage("packages", "core")
	repo.WriteConfigModel(harness.BaseFile(1))
	repo.Commit("feat(core): seed package")
	require.NoError(t, os.Symlink(".dispatexclude", repo.Path(".dispatexclude")))

	res := repo.Status("--package", "*")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, ".dispatexclude")
	assert.Empty(t, plannedPackages(res))
}
