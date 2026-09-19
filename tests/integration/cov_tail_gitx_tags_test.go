// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: what a tag format writes, and what a tag listing is
// allowed to mean.
//
// A format that spells the prerelease out has two shapes, and a release has to
// render the right one without being told which: the stable shape drops the
// whole prerelease section, separators included. A tag listing, in the other
// direction, is a namespace dispat shares with whoever else tags this
// repository — including its own coordination refs — so the glob that fetches
// candidates is never the last word on which of them are releases.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailPrereleaseSpellingFormatRendersBothShapes: one format, two
// releases. The stable release renders neither the channel, the counter, nor
// the separators around them — the version a script is handed is the plain
// core — and the prerelease renders all three. The alias written beside each
// release is built from the version's parts rather than from the version, so
// it exercises the other half of the renderer in the same run.
func TestCovTailPrereleaseSpellingFormatRendersBothShapes(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("echo tagversion=$DISPAT_TAG_VERSION", 1)
	cfg.TagFormat = "{name}@{version}-{channel}.{counter}"
	cfg.AliasTags = []models.AliasTagConfig{
		{Format: "{name}-v{major}.{minor}.{patch}", Moving: true},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first work")

	stable := r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the stable release drops the prerelease section entirely; tags: %v", r.TagList())
	assert.True(t, r.IsTagged("core-v0.1.0"),
		"and the alias names the three parts of the version; tags: %v", r.TagList())
	assert.Contains(t, stable.Stdout, "tagversion=0.1.0",
		"the version section handed to a script is the plain core: %s", stable.Stdout)

	r.Commit("chore(release): record the changelog")
	r.WriteFile("packages/core/more.txt", "work\n")
	r.Commit("fix(core)%beta: enter a train")

	prerelease := r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.1-beta.0"),
		"the prerelease renders channel and counter; tags: %v", r.TagList())
	assert.Contains(t, prerelease.Stdout, "tagversion=0.1.1-beta.0",
		"and the version section carries them too: %s", prerelease.Stdout)
	assert.True(t, r.IsTagged("core-v0.1.1"),
		"while the alias still names the core parts; tags: %v", r.TagList())
}

// TestCovTailTagInventoryIsNotTheGlobThatFetchedIt: the glob a package's
// format produces is a filter, not a decision. It is deliberately loose — "*"
// spans any run of characters — so the listing it returns holds refs that are
// not this package's releases and, for a format broad enough, are not releases
// at all: dispat's own release-lock ref is on HEAD for the whole of a run, and
// a ref that is exactly the format's literal prefix with nothing where the
// version goes matches the glob and no version. Neither may be adopted as a
// baseline, which is what `compute` reports here by proposing an initial for
// the package whose listing holds nothing readable.
func TestCovTailTagInventoryIsNotTheGlobThatFetchedIt(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// "{version}" is the broadest format there is: its glob is "*", so this
	// package's listing is every ref in the repository.
	cfg.Packages = map[string]models.PackageConfig{"solo": {TagFormat: "{version}"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "solo")
	r.WriteFile("packages/core/package.json", `{"name": "core", "version": "1.4.2"}`)
	r.WriteFile("packages/solo/package.json", `{"name": "solo", "version": "2.1.0"}`)
	r.Commit("feat(core,solo): bootstrap")

	tagAt(r, "core@1.0.0", "HEAD")
	// A ref that is the format's literal prefix and nothing else, which the
	// glob matches and the format cannot read.
	r.Git("tag", "core@")
	// dispat's own coordination ref, which the "{version}" glob also returns.
	r.Git("tag", lockTag)

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "+ initial core",
		"the package whose listing holds a real release tag needs no initial: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "+ initial solo 2.1.0",
		"while the one whose listing holds nothing readable does: %s", res.Stdout)
	assert.NotContains(t, res.Stdout, lockTag,
		"and no proposal is justified by dispat's own lock ref: %s", res.Stdout)
}
