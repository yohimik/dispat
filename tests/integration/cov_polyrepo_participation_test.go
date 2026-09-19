// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for what an excluded repository takes out of the control
// configuration with it. Participation is settled from `.gitmodules` text and
// the control file alone, before any repository is initialized, so these
// scenarios check what survives the removal rather than what a release does
// with it afterwards.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoParticipationFleet links two sources that hold packages and one
// legacy source that scenarios exclude. The legacy checkout exists, because
// the point of these scenarios is what the configuration loses, not whether an
// uninitialized checkout can be skipped.
func covPolyrepoParticipationFleet(t *testing.T) *harness.Repo {
	t.Helper()
	kept := harness.New(t)
	kept.SeedPackage("packages", "kept")
	kept.Commit("feat(kept): bootstrap the participating source")

	legacy := harness.New(t)
	legacy.SeedPackage("packages", "legacy")
	legacy.SeedPackage("packages", "retired")
	legacy.WriteFile("packages/.hidden/main.txt", "not a package folder\n")
	legacy.WriteFile("packages/loose.txt", "not a folder at all\n")
	legacy.Commit("feat(legacy,retired): bootstrap the legacy source")

	archive := harness.New(t)
	archive.SeedPackage("packages", "archived")
	archive.Commit("feat(archived): bootstrap the archive source")

	control := harness.New(t)
	addPolyrepoSource(t, control, "kept-source", "sources/kept", kept)
	addPolyrepoSource(t, control, "legacy-source", "sources/legacy", legacy)
	addPolyrepoSource(t, control, "archive-source", "sources/archive", archive)
	return control
}

// TestCovPolyrepoExclusionTakesTheDeclarationsItOwns: a space path inside an
// excluded repository hosts that repository's packages, so the path stops
// contributing; a space left with no path at all goes with its own package
// entries, and a top-level entry that configured one of those packages goes
// with it rather than being reported as matching no folder. A space that keeps
// another path keeps working for the rest of the fleet.
func TestCovPolyrepoExclusionTakesTheDeclarationsItOwns(t *testing.T) {
	control := covPolyrepoParticipationFleet(t)
	cfg := covPolyrepoFile()
	cfg.Spaces = map[string]models.SpaceConfig{
		// One space spans two repositories, and only one of them is excluded.
		"shared": {Path: models.PathList{"sources/kept/packages", "sources/legacy/packages"}},
		// This one lies entirely inside the excluded repository, and carries a
		// package entry of its own that has to go with it.
		"legacyOnly": {
			Path:     models.PathList{"sources/archive/packages"},
			Packages: map[string]models.PackageConfig{"archived": {Changelog: &models.ChangelogConfig{Enabled: models.Bool(true)}}},
		},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"archived": {Changelog: &models.ChangelogConfig{Enabled: models.Bool(true)}},
	}
	cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
		"legacy-source":  {Enabled: models.Bool(false)},
		"archive-source": {Enabled: models.Bool(false)},
	}
	control.WriteConfigModel(cfg)
	control.Commit("chore: hold two sources back")

	res := control.StatusOK()
	names := map[string]bool{}
	for _, e := range res.Events {
		if pkg := e.Package(); pkg != "" {
			names[pkg] = true
		}
	}
	assert.True(t, names["kept"], "the surviving path of the shared space still contributes")
	assert.False(t, names["legacy"], "the excluded repository's packages are gone")
	assert.False(t, names["retired"])
	assert.False(t, names["archived"], "a space left with no path goes with its package entries")

	var composed harness.Event
	for _, e := range res.Events {
		if e.Str("message") == "polyrepo workspace composed" {
			composed = e
		}
	}
	require.NotNil(t, composed, "the composition line names what participation decided")
	assert.Contains(t, covPolyrepoOutput(res), "repository excluded from the release")
	assert.Contains(t, covPolyrepoOutput(res), "archive-source")
	assert.Contains(t, covPolyrepoOutput(res), "legacy-source")
}

// TestCovPolyrepoExcludedDependencyEndpointsSayWhatIsMissing: an endpoint that
// no longer exists is explained by the exclusion that took it. A name the
// exclusion can be attributed to says which repository to re-enable, and a
// name it cannot says what this run excluded, so a reader is never left
// guessing whether participation is the cause.
func TestCovPolyrepoExcludedDependencyEndpointsSayWhatIsMissing(t *testing.T) {
	t.Run("an endpoint the exclusion accounts for", func(t *testing.T) {
		control := covPolyrepoParticipationFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{
			"kept":   "sources/kept/packages",
			"legacy": "sources/legacy/packages",
		})
		cfg.Dependencies = models.Dependencies{{Consumer: "kept", Provider: "legacy"}}
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"legacy-source": {Enabled: models.Bool(false)},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: depend on a package this run excluded")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "legacy-source")
		assert.Contains(t, out, "excluded by repositoryOverrides")
	})

	t.Run("an endpoint no exclusion accounts for", func(t *testing.T) {
		control := covPolyrepoParticipationFleet(t)
		cfg := covPolyrepoFile()
		cfg.Spaces = covPolyrepoSpaces(map[string]string{"kept": "sources/kept/packages"})
		cfg.Dependencies = models.Dependencies{{Consumer: "kept", Provider: "never-existed"}}
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"legacy-source":  {Enabled: models.Bool(false)},
			"archive-source": {Enabled: models.Bool(false)},
		}
		control.WriteConfigModel(cfg)
		control.Commit("chore: depend on a package nothing declares")

		res := control.Status()
		assert.Equal(t, 1, res.Code)
		out := covPolyrepoOutput(res)
		assert.Contains(t, out, "this run excluded")
		assert.Contains(t, out, "archive-source")
		assert.Contains(t, out, "legacy-source")
	})
}
