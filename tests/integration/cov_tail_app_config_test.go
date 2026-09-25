// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the configuration the loader refuses and the
// shorthand shapes it accepts. A refused configuration is the cheapest kind of
// failure there is — nothing has run yet — so each one is asserted on the
// sentence the operator reads, and the shapes that are accepted are asserted
// on the plan they produce, because "read as something else" and "not read at
// all" are the two ways a shorthand can be wrong.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailRefused runs `dispat status` and requires a refusal whose text
// carries want.
//
// The comparison is made against the output with one level of quoting taken
// out, because a refusal reaches the reader through whichever writer is
// already standing — the JSON logger once the config loaded, the boot logger
// when it did not — and the two escape the quotes in a label such as
// spaces["libs"] differently. Neither spelling is what the scenario is about.
func covTailRefused(t *testing.T, r *harness.Repo, want string) {
	t.Helper()
	res := r.Status()
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, strings.ReplaceAll(res.Stdout+res.Stderr, `\"`, `"`), want)
}

// TestCovTailConfigRefusesAVersionGroupItCannotResolve: a versionGroup is a
// name in the same namespace as the spaces, and it may name a group or a space
// that versions as one. Each way of naming something else is refused with the
// name that was written, because a group silently resolving to nothing is a
// release train nobody is on.
func TestCovTailConfigRefusesAVersionGroupItCannotResolve(t *testing.T) {
	base := func() models.File {
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["apps"] = models.SpaceConfig{Path: models.PathList{"apps"}, Flow: buildPublish()}
		return cfg
	}

	t.Run("a group with no name", func(t *testing.T) {
		r := harness.New(t)
		cfg := base()
		cfg.VersionGroups = map[string]models.VersionGroupConfig{"": {Versioning: "fixed"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		covTailRefused(t, r, "group name must not be empty")
	})

	t.Run("a group named after a space", func(t *testing.T) {
		r := harness.New(t)
		cfg := base()
		cfg.VersionGroups = map[string]models.VersionGroupConfig{"libs": {Versioning: "fixed"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		covTailRefused(t, r, "group and space names share one namespace")
	})

	t.Run("a space that versions independently", func(t *testing.T) {
		r := harness.New(t)
		cfg := base()
		cfg.Spaces["apps"] = models.SpaceConfig{
			Path: models.PathList{"apps"}, Flow: buildPublish(), VersionGroup: "libs",
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("apps", "site")
		r.Commit("feat(core,site): bootstrap")
		covTailRefused(t, r, "does not version as a group")
	})

	t.Run("a space that is itself in a group", func(t *testing.T) {
		r := harness.New(t)
		cfg := base()
		cfg.VersionGroups = map[string]models.VersionGroupConfig{"train": {Versioning: "fixed"}}
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path: models.PathList{"packages"}, Flow: buildPublish(), VersionGroup: "train",
		}
		cfg.Spaces["apps"] = models.SpaceConfig{
			Path: models.PathList{"apps"}, Flow: buildPublish(), VersionGroup: "libs",
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("apps", "site")
		r.Commit("feat(core,site): bootstrap")
		covTailRefused(t, r, "name that group directly")
	})

	t.Run("a space that does version as one is the accepted shape", func(t *testing.T) {
		r := harness.New(t)
		cfg := base()
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path: models.PathList{"packages"}, Flow: buildPublish(), Versioning: "fixed",
		}
		cfg.Spaces["apps"] = models.SpaceConfig{
			Path: models.PathList{"apps"}, Flow: buildPublish(), VersionGroup: "libs",
		}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("apps", "site")
		r.Commit("feat(core,site): bootstrap")

		res := r.StatusOK()
		assert.NotEmpty(t, res.Events, "the two spaces share one train; stdout:\n%s", res.Stdout)
	})
}

// TestCovTailConfigRefusesAnAutoVersionOnlyNamingNoPackage: autoVersion.only
// narrows a rewrite to named providers, so a name that is no package narrows
// it to nothing — a typo that would otherwise present as "the rewrite silently
// stopped happening". It is refused wherever the block was written.
func TestCovTailConfigRefusesAnAutoVersionOnlyNamingNoPackage(t *testing.T) {
	for name, tc := range map[string]struct {
		adjust func(*models.File)
		want   string
	}{
		"declared by the space": {
			adjust: func(cfg *models.File) {
				cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Only: []string{"nobody"}})
			},
			want: `space "libs": autoVersion.only: unknown package "nobody"`,
		},
		"declared by one package of the space": {
			adjust: func(cfg *models.File) {
				cfg.Spaces["libs"] = models.SpaceConfig{
					Path: models.PathList{"packages"}, Flow: buildPublish(),
					Packages: map[string]models.PackageConfig{
						"core": {AutoVersion: &models.AutoVersionConfig{Only: []string{"nobody"}}},
					},
				}
			},
			want: `space "libs": package "core": autoVersion.only: unknown package "nobody"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			tc.adjust(&cfg)
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")
			covTailRefused(t, r, tc.want)
		})
	}
}

// TestCovTailConfigRefusesACommitTypeWithTwoBumps: a section's bump merges
// into the commit parser, and the parser is one table for the whole
// repository while sections are per package and per destination. So the fold
// runs across every layer that may declare one, and a type two of them
// disagree about is refused naming the layer it was read in.
func TestCovTailConfigRefusesACommitTypeWithTwoBumps(t *testing.T) {
	conflicting := &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			Sections: []models.SectionConfig{{Title: "Chores", Types: []string{"chore"}, Bump: "minor"}},
		},
	}

	for name, tc := range map[string]struct {
		adjust func(*models.File)
		want   string
	}{
		"the root record objects": {
			adjust: func(cfg *models.File) { cfg.Changelog = conflicting },
			want:   "changelog/github: sections:",
		},
		"a root package entry": {
			adjust: func(cfg *models.File) {
				cfg.Packages = map[string]models.PackageConfig{"core": {Changelog: conflicting}}
			},
			want: `packages["core"]: sections:`,
		},
		"a space": {
			adjust: func(cfg *models.File) {
				s := cfg.Spaces["libs"]
				s.Changelog = conflicting
				cfg.Spaces["libs"] = s
			},
			want: `spaces["libs"]: sections:`,
		},
		"a package entry inside a space": {
			adjust: func(cfg *models.File) {
				s := cfg.Spaces["libs"]
				s.Packages = map[string]models.PackageConfig{"core": {Changelog: conflicting}}
				cfg.Spaces["libs"] = s
			},
			want: `spaces["libs"]: packages["core"]: sections:`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			// The parser states one bump for the type; the section below
			// states another for the same one.
			cfg.Parser = &models.ParserConfig{Types: map[string]string{"chore": "patch"}}
			tc.adjust(&cfg)
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")

			covTailRefused(t, r, tc.want)
			covTailRefused(t, r, "a commit type has one bump for the whole repository")
		})
	}
}

// TestCovTailConfigRefusesAnEnvNameAScriptCouldNotRead: static env becomes
// real environment variables, so a key that is not a variable name is refused
// where it was written rather than exported as something no shell can read.
func TestCovTailConfigRefusesAnEnvNameAScriptCouldNotRead(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	s := cfg.Spaces["libs"]
	s.Env = map[string]string{"DISPAT_SNEAKY": "x"}
	cfg.Spaces["libs"] = s
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	covTailRefused(t, r, `space "libs": env: key "DISPAT_SNEAKY" uses the reserved DISPAT_ prefix`)
}

// TestCovTailConfigRefusesAnIgnoreFileItCannotRead: .dispatignore is read per
// folder, and a folder of that name is not a file of ignore patterns. Reading
// it as "no patterns" would silently widen what a package is changed by, so it
// is refused naming the file.
func TestCovTailConfigRefusesAnIgnoreFileItCannotRead(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	// A folder where the ignore file goes. Git does not track an empty one,
	// so it carries a file, which is also what makes the read fail.
	r.WriteFile("packages/core/.dispatignore/keep.txt", "not patterns\n")
	r.Commit("feat(core): bootstrap")

	res := r.Status()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, ".dispatignore")
}
