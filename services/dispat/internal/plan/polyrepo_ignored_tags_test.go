// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func rawVersionTags(g *fakeGit, pkg string, tags ...struct{ name, commit string }) *fakeGit {
	for _, tag := range tags {
		g.tags[tag.name] = tag.commit
		g.tagsFor[pkg] = append(g.tagsFor[pkg], tag.name)
	}
	return g
}

// TestComposedIgnoredTagsAreRepositoryQualified models a nested step for A
// after its shared@1.1.0 tag has landed. Source B already owns an unrelated
// tag with the same spelling. Replanning must expose A's pending window
// without erasing B's published baseline.
func TestComposedIgnoredTagsAreRepositoryQualified(t *testing.T) {
	aGit := rawVersionTags(newFakeGit(
		commit{sha: "a1", message: "feat(a): initial"},
		commit{sha: "a2", message: "feat(a): next"},
	), "a",
		struct{ name, commit string }{"shared@1.0.0", "a1"},
		struct{ name, commit string }{"shared@1.1.0", "a2"},
	)
	bGit := rawVersionTags(newFakeGit(
		commit{sha: "b1", message: "feat(b): already released"},
	), "b", struct{ name, commit string }{"shared@1.1.0", "b1"})
	control := &composedControlGit{fakeGit: newFakeGit()}

	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/packages/a", RepoRoot: "/w/a", Repository: "source-a", Space: &model.Space{Name: "a", TagFormat: "shared@{version}"}},
			{Name: "b", Dir: "/w/b/packages/b", RepoRoot: "/w/b", Repository: "source-b", Space: &model.Space{Name: "b", TagFormat: "shared@{version}"}},
		},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "sources/a", Git: aGit},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "sources/b", Git: bGit},
		},
		IgnoredTagsByRepository: map[string][]string{"source-a": {"shared@1.1.0"}},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.True(t, pl.Releases["a"].IsReleasing(), "A reads the baseline from before its in-flight tag")
	assert.Equal(t, "1.1.0", pl.Releases["a"].Next.String())
	assert.False(t, pl.Releases["b"].IsReleasing(), "B keeps its repository-local v1.1.0 baseline")
}

// TestFlatIgnoredTagsRemainWorkspaceWide preserves the pre-polyrepo API for
// legacy callers: a flat ignored tag still applies to every history.
func TestFlatIgnoredTagsRemainWorkspaceWide(t *testing.T) {
	aGit := rawVersionTags(newFakeGit(commit{sha: "a1", message: "feat(a): initial"}), "a",
		struct{ name, commit string }{"shared@1.1.0", "a1"})
	bGit := rawVersionTags(newFakeGit(commit{sha: "b1", message: "feat(b): initial"}), "b",
		struct{ name, commit string }{"shared@1.1.0", "b1"})
	control := &composedControlGit{fakeGit: newFakeGit()}

	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/a", RepoRoot: "/w/a", Repository: "source-a", Space: &model.Space{Name: "a", TagFormat: "shared@{version}"}},
			{Name: "b", Dir: "/w/b/b", RepoRoot: "/w/b", Repository: "source-b", Space: &model.Space{Name: "b", TagFormat: "shared@{version}"}},
		},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "sources/a", Git: aGit},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "sources/b", Git: bGit},
		},
		IgnoredTags: []string{"shared@1.1.0"},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.True(t, pl.Releases["a"].IsReleasing())
	assert.True(t, pl.Releases["b"].IsReleasing())
}
