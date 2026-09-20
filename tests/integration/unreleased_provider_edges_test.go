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

func unreleasedProviderRepo(t *testing.T, releasePolicy bool) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.UnsafeDisableLock = true
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	if releasePolicy {
		space := cfg.Spaces["libs"]
		space.AutoVersion = &models.AutoVersionConfig{Enabled: models.Bool(true), Manifests: "root"}
		cfg.Spaces["libs"] = space
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"0.0.0","metadata":{"owner":"platform"}}`)
	r.WriteFile("packages/web/package.json", `{"name":"@acme/web","version":"0.0.0","dependencies":{"@acme/core":"^0.0.0","left-pad":"^1.3.0"},"metadata":{"keep":true}}`)
	r.Commit("chore(core): add a provider that has never released")
	r.WriteFile("packages/web/feature.txt", "consumer feature\n")
	r.Commit("feat(web): release only the consumer")
	return r
}

func TestAutoWriterResolvesANeverReleasedProviderToItsCurrentVersion(t *testing.T) {
	r := unreleasedProviderRepo(t, false)
	beforeCore := readRepoFile(t, r, "packages/core/package.json")

	res := r.Command("autowriter", "--package", "web", "--set", "@acme/core=^{version}")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	web := readRepoFile(t, r, "packages/web/package.json")
	assert.Contains(t, web, `"@acme/core":"^0.0.0"`, "the unreleased provider resolves to its current version")
	assert.Contains(t, web, `"left-pad":"^1.3.0"`, "an unrelated dependency survives")
	assert.Contains(t, web, `"metadata":{"keep":true}`, "manifest metadata survives")
	assert.Equal(t, beforeCore, readRepoFile(t, r, "packages/core/package.json"))
	assert.Empty(t, r.TagList(), "the writer does not invent a provider release")
}

func TestReleaseAutoVersionKeepsANeverReleasedProviderAtCurrentVersion(t *testing.T) {
	r := unreleasedProviderRepo(t, true)
	beforeCore := readRepoFile(t, r, "packages/core/package.json")

	res := r.Command("release")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	web := readRepoFile(t, r, "packages/web/package.json")
	assert.Contains(t, web, `"version":"0.1.0"`, "the releasing consumer receives its planned version")
	assert.Contains(t, web, `"@acme/core":"^0.0.0"`, "the provider has no baseline, so its current version is retained")
	assert.Contains(t, web, `"left-pad":"^1.3.0"`, "an unrelated dependency survives")
	assert.Contains(t, web, `"metadata":{"keep":true}`, "manifest metadata survives")
	assert.Equal(t, beforeCore, readRepoFile(t, r, "packages/core/package.json"))
	assert.False(t, r.IsTagged("core@0.1.0"), "a chore-only provider is not released")
	assert.True(t, r.IsTagged("web@0.1.0"), "the consumer still releases")
}
