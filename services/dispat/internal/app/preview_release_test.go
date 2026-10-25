// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// releaseTag is one parsed release tag of the fixtures below.
func releaseTag(t *testing.T, name, version, commit string) gitx.Tag {
	t.Helper()
	return gitx.Tag{Name: name, Version: mustVersion(t, version), Parsed: true, Commit: commit}
}

// previewInventory is a two-package workspace: dispat tagged under a path
// format with a v{version} and a moving v{major} alias, and web under the
// default format. dispat has released twice and web once, beside dispat's
// second release on the same commit.
func previewInventory(t *testing.T, selected ...string) releaseInventory {
	t.Helper()
	dispat := &model.Package{Name: "dispat", Space: &model.Space{
		Name: "services", TagFormat: "services/{name}/v{version}",
		AliasTags: []model.AliasTag{{Format: "v{version}"}, {Format: "v{major}", Moving: true}},
	}}
	web := &model.Package{Name: "web", Space: &model.Space{Name: "apps"}}
	aliases := map[string]gitx.Tag{
		"v1.11.0-rc.8": {Name: "v1.11.0-rc.8", Commit: "c1"},
		"v1":           {Name: "v1", Commit: "c2"},
	}
	return releaseInventory{
		packages: []*model.Package{dispat, web},
		tags: map[string]gitx.Tags{
			"dispat": {
				releaseTag(t, "services/dispat/v1.11.0", "1.11.0", "c2"),
				releaseTag(t, "services/dispat/v1.11.0-rc.8", "1.11.0-rc.8", "c1"),
				releaseTag(t, "services/dispat/v1.10.0", "1.10.0", "c0"),
			},
			"web": {releaseTag(t, "web@0.3.0", "0.3.0", "c2")},
		},
		selected: selected,
		findTag: func(name string) (gitx.Tag, bool, error) {
			tag, ok := aliases[name]
			return tag, ok, nil
		},
	}
}

func TestPreviewReleaseResolvesAVersionOfTheSelectedPackage(t *testing.T) {
	released, err := previewInventory(t, "dispat").resolve("1.11.0-rc.8")
	require.NoError(t, err)
	assert.Equal(t, "dispat", released.pkg.Name)
	assert.Equal(t, "services/dispat/v1.11.0-rc.8", released.tag.Name)
	assert.Equal(t, "c1", released.tag.Commit)
	assert.ElementsMatch(t, []string{"services/dispat/v1.11.0", "services/dispat/v1.11.0-rc.8"}, released.masked,
		"the release and every later one are masked; nothing else sits on its commit")
}

func TestPreviewReleaseResolvesLatestAsTheHighestRelease(t *testing.T) {
	released, err := previewInventory(t, "dispat").resolve(LatestRelease)
	require.NoError(t, err)
	assert.Equal(t, "services/dispat/v1.11.0", released.tag.Name)
	assert.ElementsMatch(t, []string{"services/dispat/v1.11.0", "web@0.3.0"}, released.masked,
		"another package's tag on the release's commit was written by the same run")
}

func TestPreviewReleaseResolvesAFullTagWithoutASelection(t *testing.T) {
	inventory := previewInventory(t, "dispat", "web")
	released, err := inventory.resolve("services/dispat/v1.10.0")
	require.NoError(t, err)
	assert.Equal(t, "dispat", released.pkg.Name)
	assert.Equal(t, "1.10.0", released.tag.Version.String())

	released, err = inventory.resolve("web@0.3.0")
	require.NoError(t, err)
	assert.Equal(t, "web", released.pkg.Name)
	assert.ElementsMatch(t, []string{"web@0.3.0", "services/dispat/v1.11.0"}, released.masked)
}

func TestPreviewReleaseResolvesAnAliasThroughItsCommit(t *testing.T) {
	inventory := previewInventory(t, "dispat", "web")
	released, err := inventory.resolve("v1.11.0-rc.8")
	require.NoError(t, err)
	assert.Equal(t, "services/dispat/v1.11.0-rc.8", released.tag.Name)

	released, err = inventory.resolve("v1")
	require.NoError(t, err)
	assert.Equal(t, "services/dispat/v1.11.0", released.tag.Name,
		"a moving alias names the release it was last written for")
}

func TestPreviewReleaseRefusals(t *testing.T) {
	tests := map[string]struct {
		selected []string
		value    string
		message  string
	}{
		"an unknown version": {
			selected: []string{"dispat"}, value: "1.12.0",
			message: "dispat has no release 1.12.0",
		},
		"a version beside several packages": {
			selected: []string{"dispat", "web"}, value: "1.11.0-rc.8",
			message: "--version 1.11.0-rc.8 needs one package, and the selection covers dispat, web",
		},
		"latest beside several packages": {
			selected: []string{"dispat", "web"}, value: LatestRelease,
			message: "--version latest needs one package",
		},
		"a tag of another package": {
			selected: []string{"web"}, value: "services/dispat/v1.10.0",
			message: "services/dispat/v1.10.0 is a release of dispat, which the selection (web) does not cover",
		},
		"a name that is no release": {
			selected: []string{"dispat"}, value: "services/dispat/v9.9.9",
			message: "services/dispat/v9.9.9 is not a version, latest, or a release tag of any package",
		},
		"an alias that was never written": {
			selected: []string{"dispat"}, value: "v2",
			message: "v2 is not a version, latest, or a release tag of any package",
		},
		"nothing selected": {
			value: "1.11.0", message: "no package to preview",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := previewInventory(t, tc.selected...).resolve(tc.value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestPreviewReleaseRefusesATagSeveralPackagesRead(t *testing.T) {
	inventory := previewInventory(t, "dispat", "web")
	inventory.tags["web"] = append(inventory.tags["web"], releaseTag(t, "services/dispat/v1.10.0", "1.10.0", "c0"))

	_, err := inventory.resolve("services/dispat/v1.10.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reads as a release of several packages (dispat, web)")

	inventory.selected = []string{"web"}
	released, err := inventory.resolve("services/dispat/v1.10.0")
	require.NoError(t, err, "a selection that keeps one reader settles it")
	assert.Equal(t, "web", released.pkg.Name)
}

func TestPreviewReleaseKeepsThePackagesOwnEarlierReleasesOnTheCommit(t *testing.T) {
	inventory := previewInventory(t, "web")
	inventory.tags["web"] = gitx.Tags{
		releaseTag(t, "web@1.0.0", "1.0.0", "c3"),
		releaseTag(t, "web@1.0.0-rc.1", "1.0.0-rc.1", "c3"),
	}
	released, err := inventory.resolve("1.0.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"web@1.0.0"}, released.masked,
		"the candidate on the same commit was the package's history when it released")
}
