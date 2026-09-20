// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestStandaloneStepsRefuseAFatalPlanBeforeWriting proves the native step
// commands share release's planning safety boundary. A dependency cycle has no
// valid package order, so none of the commands may run scripts, write records,
// or create a release through a partial plan.
func TestStandaloneStepsRefuseAFatalPlanBeforeWriting(t *testing.T) {
	for _, command := range []string{"changelog", "autoversion", "commit", "github"} {
		t.Run(command, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(markerBuild, 1)
			cfg.Dependencies = []models.DependencyConfig{
				{Consumer: "app", Provider: "core"},
				{Consumer: "core", Provider: "app"},
			}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.SeedPackage("packages", "app")
			r.Commit("feat(core,app): cyclic pending work")
			before := r.Git("status", "--porcelain=v1")

			res := r.Command(command)
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, "repository cannot produce a correct plan")
			assert.True(t, harness.IsCodePresent(res.Events, "E200"), "stdout:\n%s", res.Stdout)
			assert.Equal(t, before, r.Git("status", "--porcelain=v1"))
			assert.Empty(t, r.TagList())
			assert.Zero(t, buildRuns(r))
		})
	}
}

// TestStandaloneAutoversionReportsSyncLockFailureAfterTruthfulWrites proves a
// failed lockfile generator cannot be mistaken for a successful standalone
// version step. The manifest edits already completed and remain visible for
// repair, while the command creates no commit or tag.
func TestStandaloneAutoversionReportsSyncLockFailureAfterTruthfulWrites(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["fail-lock"] = models.Script{"printf attempted > ../../sync-lock-attempted; exit 19"}
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{SyncLock: []string{"fail-lock"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name":"core","version":"0.0.0"}`)
	r.Commit("feat(core): pending work")
	before := r.Git("rev-parse", "HEAD")

	res := r.Command("autoversion")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "syncLock failed")
	assert.FileExists(t, r.Path("sync-lock-attempted"))
	manifest, err := os.ReadFile(r.Path("packages", "core", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), `"version":"0.1.0"`)
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Empty(t, r.TagList())
}

// TestStandaloneCommitNoForceOverridesConfiguredForce exercises the monorepo
// commit path's per-invocation safety override. The configuration permits
// forced tag updates, but this invocation explicitly declines that authority
// while still creating an ordinary new release tag.
func TestStandaloneCommitNoForceOverridesConfiguredForce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git pre-commit hook is a POSIX shell script")
	}
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): pending work")
	plannedHead := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/generated.txt", "release artifact\n")
	require.NoError(t, os.WriteFile(r.Path(".git", "hooks", "pre-commit"),
		[]byte("#!/bin/sh\ngit tag -a core@0.1.0 -m occupied HEAD\n"), 0o755))

	res := r.Command("commit", "--package", "core", "--tag", "--no-force")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E221"), "stdout:\n%s", res.Stdout)
	assert.Equal(t, 1, r.TagCount("core@0.1.0"))
	assert.Equal(t, plannedHead, r.Git("rev-parse", "core@0.1.0^{}"),
		"the invocation must not replace the tag created after planning")
	assert.NotEqual(t, plannedHead, r.Git("rev-parse", "HEAD"),
		"the truthful release commit remains available for repair")
}
