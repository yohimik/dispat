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

// A selected package has one release identity. Greek final sigma and ordinary
// sigma compare equal under the same case folding used by --package.
func TestUnicodePackageSelectionRefusesAnAmbiguousRelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"services"}, Flow: buildPublish()}
	r.WriteConfigModel(cfg)
	r.WriteFile("services/.keep", "")
	r.SeedPackage("packages", "Σ")
	r.Commit("feat(Σ): first feature")

	aliased := r.Command("status", "--package", "ς")
	require.Equal(t, 0, aliased.Code, "stdout:\n%s\nstderr:\n%s", aliased.Stdout, aliased.Stderr)
	assert.Contains(t, aliased.Stdout, `"selection":"Σ"`)
	assert.Contains(t, aliased.Stdout, `"releasing":["Σ"]`)

	r.SeedPackage("services", "ς")
	r.Commit("feat(ς): another package")
	ambiguous := r.Command("release", "--package", "ς")
	require.Equal(t, 1, ambiguous.Code, "stdout:\n%s\nstderr:\n%s", ambiguous.Stdout, ambiguous.Stderr)
	assert.Contains(t, ambiguous.Stdout+ambiguous.Stderr, "package names must be unique")
	assert.Empty(t, r.TagList(), "no package may publish under an ambiguous selector")
}

// A dependency declared through a Unicode case alias of its provider's name
// resolves to the provider's exact published name, so the release tags are
// read back on the next run and nothing is released twice.
func TestUnicodeDependencyAliasConvergesOnTheNextRun(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "ς"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "Σ")
	r.SeedPackage("packages", "app")
	r.Commit("feat(Σ,app): publish both packages")

	first := r.Release()
	require.Equal(t, 0, first.Code, "stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
	assert.Equal(t, 1, r.TagCount("Σ@0.1.0"))
	assert.Equal(t, 1, r.TagCount("app@0.1.0"))
	status := r.Status()
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.Contains(t, status.Stdout, `"releasing":0`)
}
