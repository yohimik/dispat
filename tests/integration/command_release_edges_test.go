// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A tolerant inventory cannot certify that an unreadable replacement section
// contains no local links. The release gate must fail without editing it.
func TestScannerLinkGateRefusesALaxGoManifest(t *testing.T) {
	repo := harness.New(t)
	manifest := "module example.com/core\n\ngo 1.24\nreplace malformed\n"
	repo.WriteFile("go.mod", manifest)
	result := repo.Command("scanner", "--verify-unlinked", "--log-format", "json")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "cannot read the link directives")
	assert.Equal(t, manifest, readRepoFile(t, repo, "go.mod"))
	assert.Empty(t, repo.TagList())
}

func TestScannerPrintsAVersionWithoutAPackageName(t *testing.T) {
	repo := harness.New(t)
	repo.WriteFile("package.json", `{"version":"1.2.3"}`)
	result := repo.Command("scanner")
	require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "package.json  npm  1.2.3")
	assert.Contains(t, result.Stdout, "1 manifest(s)")

	result = repo.Command("scanner", "package.json")
	require.NotZero(t, result.Code)
	assert.Contains(t, result.Stdout+result.Stderr, "not a folder")
}

func TestForRefusesAnUnknownWorkingPackageBeforeRunning(t *testing.T) {
	repo := forRepo(t)
	result := repo.Command("for", "one", "--in", "pkg:absent", "--do", "touch should-not-exist")
	require.NotZero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout+result.Stderr, "invalid --in")
	assert.Contains(t, result.Stdout+result.Stderr, "absent")
	assert.NoFileExists(t, repo.Path("should-not-exist"))
}

func TestPlanningCommandsRequireGitBeforeReadingHistory(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig(echoBuild, 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): pending release")
	before := repo.Git("rev-parse", "HEAD")
	for _, command := range []string{"status", "release"} {
		t.Run(command, func(t *testing.T) {
			result := repo.CommandEnv([]string{"PATH=" + t.TempDir()}, command)
			require.NotZero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
			assert.Contains(t, result.Stdout+result.Stderr, "git executable not found in PATH")
			assert.Empty(t, plannedPackages(result))
			assert.Equal(t, before, repo.Git("rev-parse", "HEAD"))
			assert.Empty(t, repo.TagList())
		})
	}
}
