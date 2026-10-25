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
		firstParents: fakeFirstParents(fakeCommit{"c0", "feat(dispat): first"},
			fakeCommit{"c1", "feat(dispat): second"}, fakeCommit{"c2", "feat(dispat): third"}),
	}
}

// fakeCommit is one commit of a fake first-parent line.
type fakeCommit struct{ sha, message string }

// fakeFirstParents is a first-parent line, oldest commit first, read the way
// the inventory reads one: from a commit down, at most limit commits.
func fakeFirstParents(line ...fakeCommit) func(string, int) ([]gitx.CommitMessage, error) {
	return func(commit string, limit int) ([]gitx.CommitMessage, error) {
		var out []gitx.CommitMessage
		for i := len(line) - 1; i >= 0; i-- {
			if len(out) == 0 && line[i].sha != commit {
				continue
			}
			if len(out) == limit {
				break
			}
			out = append(out, gitx.CommitMessage{SHA: line[i].sha, Message: line[i].message + "\n"})
		}
		return out, nil
	}
}

// runInventory is a run in which every package committed for itself: on top
// of the planned commit p1, utils and then web each got a release commit, and
// core, which consumes both, got the last one. utils released once before, in
// an earlier run whose release commit r0 sits below p0.
func runInventory(t *testing.T) releaseInventory {
	t.Helper()
	core := &model.Package{Name: "core"}
	utils := &model.Package{Name: "utils"}
	web := &model.Package{Name: "web"}
	return releaseInventory{
		packages: []*model.Package{core, utils, web},
		tags: map[string]gitx.Tags{
			"core": {releaseTag(t, "core@0.2.0", "0.2.0", "r3")},
			"utils": {
				releaseTag(t, "utils@0.0.2", "0.0.2", "r1"),
				releaseTag(t, "utils@0.0.1", "0.0.1", "r0"),
			},
			"web": {releaseTag(t, "web@1.1.0", "1.1.0", "r2")},
		},
		selected: []string{"core"},
		firstParents: fakeFirstParents(
			fakeCommit{"r0", "chore(release): utils@0.0.1"},
			fakeCommit{"p0", "fix(utils): first fix"},
			fakeCommit{"p1", "feat(core): a feature"},
			fakeCommit{"r1", "chore(release): utils@0.0.2"},
			fakeCommit{"r2", "chore(release): web@1.1.0"},
			fakeCommit{"r3", "chore(release): core@0.2.0"},
		),
	}
}

func TestPreviewReleaseMasksEveryReleaseTheRunCommittedBelowIt(t *testing.T) {
	released, err := runInventory(t).resolve("0.2.0")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core@0.2.0", "utils@0.0.2", "web@1.1.0"}, released.masked,
		"the walk crosses the run's release commits and stops at the commit it planned at, "+
			"below which utils@0.0.1 is an earlier run's")
}

func TestPreviewReleaseKeepsAnEarlierRunsReleases(t *testing.T) {
	inventory := runInventory(t)
	inventory.tags["utils"] = gitx.Tags{releaseTag(t, "utils@0.0.1", "0.0.1", "r0")}
	inventory.tags["web"] = nil
	inventory.firstParents = fakeFirstParents(
		fakeCommit{"r0", "chore(release): utils@0.0.1"},
		fakeCommit{"p1", "feat(core): a feature"},
		fakeCommit{"r3", "chore(release): core@0.2.0"},
	)
	released, err := inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"core@0.2.0"}, released.masked,
		"utils released in a run that ended below the commit this one planned at")

	inventory = runInventory(t)
	inventory.tags["core"] = gitx.Tags{releaseTag(t, "core@0.2.0", "0.2.0", "p1")}
	released, err = inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"core@0.2.0"}, released.masked,
		"a tag on an ordinary commit was written by a run that made no release commit")
}

func TestPreviewReleaseReadsTheConfiguredCommitMessage(t *testing.T) {
	inventory := runInventory(t)
	inventory.commitFormat = "release {packages}: {tags}"
	inventory.firstParents = fakeFirstParents(
		fakeCommit{"p1", "feat(core): a feature"},
		fakeCommit{"r1", "chore(release): utils@0.0.2"},
		fakeCommit{"r2", "release web: web@1.1.0"},
		fakeCommit{"r3", "release core: core@0.2.0"},
	)
	released, err := inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core@0.2.0", "web@1.1.0"}, released.masked,
		"a message the configured format does not render ends the run")
}

func TestPreviewReleaseRecognisesACommitRecordingSeveralReleases(t *testing.T) {
	inventory := runInventory(t)
	inventory.tags["web"] = gitx.Tags{releaseTag(t, "web@1.1.0", "1.1.0", "r1")}
	inventory.firstParents = fakeFirstParents(
		fakeCommit{"p1", "feat(core): a feature"},
		fakeCommit{"r1", "chore(release): web@1.1.0, utils@0.0.2"},
		fakeCommit{"r3", "chore(release): core@0.2.0"},
	)
	released, err := inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core@0.2.0", "utils@0.0.2", "web@1.1.0"}, released.masked,
		"the message names its releases in the order the run planned them")
}

func TestPreviewReleaseStopsAtACommitTaggedByAnotherRun(t *testing.T) {
	inventory := runInventory(t)
	inventory.tags["web"] = gitx.Tags{releaseTag(t, "web@1.1.0", "1.1.0", "r1")}
	inventory.firstParents = fakeFirstParents(
		fakeCommit{"p1", "feat(core): a feature"},
		fakeCommit{"r1", "chore(release): utils@0.0.2"},
		fakeCommit{"r3", "chore(release): core@0.2.0"},
	)
	released, err := inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"core@0.2.0"}, released.masked,
		"web's tag on r1 is not in its message, so r1 was tagged again later and is no proof of this run")
}

func TestPreviewReleaseBoundsTheWalkByThePackageCount(t *testing.T) {
	inventory := runInventory(t)
	var asked int
	walk := inventory.firstParents
	inventory.firstParents = func(commit string, limit int) ([]gitx.CommitMessage, error) {
		asked = limit
		return walk(commit, limit)
	}
	_, err := inventory.resolve("0.2.0")
	require.NoError(t, err)
	assert.Equal(t, 4, asked, "one release commit per package and one for the run")
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
