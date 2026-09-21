// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: what discovery refuses before a package exists.
//
// The root file is only half a configuration. The rest is on disk — the
// folders a space spans, the files those folders carry, the entries a folder's
// own config file adds — and every one of them can be wrong in a way the root
// file is not. Discovery is where a name that folds onto another name, a
// folder input that cannot be read, and a folder config file reaching above
// its own level are all caught, and each of them is caught with the folder or
// the file named, because "invalid configuration" points at the wrong file.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// refuseStatus requires that `dispat status` refuses this repository as it
// stands, naming want and releasing nothing.
func refuseStatus(t *testing.T, r *harness.Repo, want string) {
	t.Helper()
	res := r.Status("--log-format", "json")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), want)
	assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
}

// writeJSON writes any config-shaped value as a folder's own dispat.json.
func writeJSON(t *testing.T, r *harness.Repo, relPath string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	r.WriteFile(relPath, string(data))
}

// TestCovConfigRefusesUnreadableFolderInputs: the change-scope ignore file and
// the package-exclude file are read at three levels, and a level that cannot
// carry out what its file says stops the run naming that level's folder.
func TestCovConfigRefusesUnreadableFolderInputs(t *testing.T) {
	t.Run("repository ignore file", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		r.WriteFile(".dispatignore", "docs/\n!\n")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "re-includes nothing")
	})

	t.Run("space ignore file", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		r.WriteFile("packages/.dispatignore", "/\n")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "names nothing")
	})

	t.Run("package ignore file", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		r.WriteFile("packages/core/.dispatignore", "!\n")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "re-includes nothing")
	})

	t.Run("exclude file that is a folder", func(t *testing.T) {
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		require.NoError(t, os.MkdirAll(r.Path("packages", ".dispatexclude"), 0o755))
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, ".dispatexclude")
	})
}

// TestCovConfigRefusesCollidingPackageIdentities: a package name identifies
// one package for the whole repository, so two folders that fold onto one
// name are refused wherever they sit, and both spellings are shown.
func TestCovConfigRefusesCollidingPackageIdentities(t *testing.T) {
	t.Run("two folders of one space", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		s := cfg.Spaces["libs"]
		s.Path = models.PathList{"packages", "vendored"}
		cfg.Spaces["libs"] = s
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("vendored", "Core")
		r.Commit("feat(core): two folders, one name")
		refuseStatus(t, r, "exists in two folders of space")
	})

	t.Run("two spaces", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"services"}, Flow: buildPublish()}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("services", "CORE")
		r.Commit("feat(core): two spaces, one name")
		refuseStatus(t, r, "exists in both space")
	})

	t.Run("a space package given a path of its own", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Packages = map[string]models.PackageConfig{"core": {Path: "elsewhere/core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "its location is the space folder")
	})

	t.Run("a standalone package whose path names a file", func(t *testing.T) {
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
		cfg.Flow = buildPublish()
		cfg.Packages = map[string]models.PackageConfig{"core": {Path: "notes.txt"}}
		r.WriteConfigModel(cfg)
		r.WriteFile("notes.txt", "a file, not a folder\n")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "is not a folder")
	})

	// A standalone path naming the repository itself is no longer one of
	// these: it declares the single-package repository, which root_path_test.go
	// covers.

	t.Run("a standalone package whose path is absolute", func(t *testing.T) {
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
		cfg.Flow = buildPublish()
		cfg.Packages = map[string]models.PackageConfig{"core": {Path: r.Path("packages", "core")}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "must be a repository-relative path")
	})

	t.Run("a nameless package entry", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Packages = map[string]models.PackageConfig{"": {Path: "packages/core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "package name must not be empty")
	})
}

// TestCovConfigRefusesFolderConfigFilesThatOverstepTheirLevel: a folder's own
// config file configures that folder. It may not move the package it sits in,
// declare a repository-wide commit type, or carry the spaces and packages of
// somewhere else, and it is held to the same validation the root file is.
func TestCovConfigRefusesFolderConfigFilesThatOverstepTheirLevel(t *testing.T) {
	seed := func(t *testing.T) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		r.WriteConfigModel(libsConfig(echoBuild, 1))
		r.SeedPackage("packages", "core")
		return r
	}

	t.Run("a package folder naming its own path", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/core/dispat.json", models.PackageConfig{Path: "elsewhere"})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "path")
	})

	t.Run("a space folder declaring a section bump", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/dispat.json", models.SpaceFile{
			Changelog: &models.ChangelogConfig{
				EntryFormatConfig: models.EntryFormatConfig{
					Sections: []models.SectionConfig{{Title: "Performance", Types: []string{"perf"}, Bump: "patch"}},
				},
			},
		})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "bump cannot be set in a folder's own config file")
	})

	t.Run("a space folder with an invalid setting", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/dispat.json", models.SpaceFile{Versioning: "calver"})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "unknown versioning")
	})

	t.Run("a space folder with a nameless package entry", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/dispat.json", models.SpaceFile{
			Packages: map[string]models.PackageConfig{"": {TagFormat: "x-{version}"}},
		})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "package name must not be empty")
	})

	t.Run("a space folder with an unusable dependency object", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/dispat.json", models.SpaceFile{
			Dependencies: models.Dependencies{{Consumer: "core", Provider: ""}},
		})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "consumer and provider are required")
	})

	t.Run("a space folder package entry holding packages of its own", func(t *testing.T) {
		r := seed(t)
		r.WriteFile("packages/dispat.json",
			`{"packages":{"core":{"packages":{"nested":{"path":"nested"}}}}}`)
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "cannot be set on a package entry")
	})

	t.Run("a package entry overriding the space login", func(t *testing.T) {
		r := seed(t)
		writeJSON(t, r, "packages/dispat.json", models.SpaceFile{
			Packages: map[string]models.PackageConfig{
				"core": {Flow: &models.SpaceFlowConfig{Login: []string{"build"}}},
			},
		})
		r.Commit("feat(core): bootstrap")
		refuseStatus(t, r, "flow.login cannot be overridden per package")
	})
}

// TestCovConfigRefusesUnusableSpaceDependencyObjects: a space's own
// `dependencies` object is held to the same two rules the root object is,
// with the space named.
func TestCovConfigRefusesUnusableSpaceDependencyObjects(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): bootstrap")

	for _, tc := range []struct {
		name string
		deps models.Dependencies
		want string
	}{
		{"no provider", models.Dependencies{{Consumer: "core"}}, "consumer and provider are required"},
		{"depending on itself", models.Dependencies{{Consumer: "core", Provider: "CORE"}},
			"cannot depend on itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := libsConfig(echoBuild, 1)
			s := cfg.Spaces["libs"]
			s.Dependencies = tc.deps
			cfg.Spaces["libs"] = s
			r.WriteConfigModel(cfg)
			refuseStatus(t, r, tc.want)
		})
	}

	t.Run("a package list naming nothing", func(t *testing.T) {
		cfg := libsConfig(echoBuild, 1)
		cfg.Packages = map[string]models.PackageConfig{"core": {Dependencies: models.ProviderList{{Provider: " "}}}}
		r.WriteConfigModel(cfg)
		refuseStatus(t, r, "provider name must not be empty")
	})

	t.Run("a package list naming the package itself", func(t *testing.T) {
		cfg := libsConfig(echoBuild, 1)
		cfg.Packages = map[string]models.PackageConfig{"core": {Dependencies: models.ProviderList{{Provider: "core"}}}}
		r.WriteConfigModel(cfg)
		refuseStatus(t, r, "cannot depend on itself")
	})
}

// TestCovConfigSpaceAtTheRepositoryRootKeepsOneConfiguration: a space whose
// path is the repository root has the root config file sitting in its folder.
// That file is the configuration, not a folder override of it, so it is read
// once and the space's packages are the root's direct sub-folders.
func TestCovConfigSpaceAtTheRepositoryRootKeepsOneConfiguration(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"root": {Path: models.PathList{"."}, Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage(".", "core")
	r.Commit("feat(core): a space at the repository root")

	res := r.StatusOK("--log-format", "json")
	assert.NotEmpty(t, harness.GraphLine(res.Events, "core"), "core must be discovered:\n%s", res.Stdout)
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
}
