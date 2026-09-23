// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type boundaryReadFailureGit struct {
	*fakeGit
	boundary string
	err      error
}

func (g *boundaryReadFailureGit) Commits(ctx context.Context, boundary string) ([]gitx.Commit, error) {
	if boundary == g.boundary {
		return nil, g.err
	}
	return g.fakeGit.Commits(ctx, boundary)
}

type projectingControlGit struct {
	*fakeGit
	links map[string]string
	err   error
}

func (g *projectingControlGit) GitlinksAt(context.Context, string) (map[string]string, error) {
	if g.err != nil {
		return nil, g.err
	}
	return g.links, nil
}

func TestComposedUnparseableTagUsesInitialWithoutReplayingHistory(t *testing.T) {
	source := newFakeGit(commit{sha: "c1", message: "feat(app): already published"}).tag("app", "garbage", "c1")
	pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	rel := pl.Releases["app"]
	assert.Equal(t, v(1, 0, 0), rel.Current)
	assert.True(t, rel.FromInitials)
	assert.Equal(t, "c1", rel.StableCommit)
	assert.False(t, rel.IsReleasing(), "the malformed tag is still the history boundary")
}

func TestComposedVersioningNoneSkipsReleaseTagInventory(t *testing.T) {
	source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), tagErr: errors.New("must not read tags")}
	opts := composedHistoryFailureOptions(source)
	opts.Packages[0].Space.Versioning = model.VersioningNone
	delete(opts.Initials, "app")

	pl, err := Compute(t.Context(), newFakeGit(), opts)
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.False(t, pl.Releases["app"].IsReleasable())
}

func TestComposedControlCheckpointReadFailureStopsPlanning(t *testing.T) {
	control := &failingControlHistoryGit{fakeGit: newFakeGit(), err: errors.New("control log unavailable")}
	opts := composedHistoryFailureOptions(newFakeGit())
	opts.Repositories["control"] = RepositoryHistory{Name: "control", Root: "/w", Git: control, Control: true}

	pl, err := Compute(t.Context(), control, opts)
	require.Nil(t, pl)
	require.Error(t, err)
	assert.ErrorContains(t, err, "indexing control checkpoints: control log unavailable")
}

func TestComposedPlannerRejectsInvalidGlobalInputs(t *testing.T) {
	t.Run("unknown dependency", func(t *testing.T) {
		opts := composedHistoryFailureOptions(newFakeGit())
		opts.Dependencies = []model.Dependency{{Consumer: "app", Provider: "missing"}}
		pl, err := Compute(t.Context(), newFakeGit(), opts)
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, `unknown node "missing"`)
	})

	t.Run("invalid control parser", func(t *testing.T) {
		opts := composedHistoryFailureOptions(newFakeGit())
		opts.ParserConfig = ccme.Config{Separator: "ab"}
		pl, err := Compute(t.Context(), newFakeGit(), opts)
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "separator")
	})
}

func TestComposedPrereleaseRequiresItsOwnProviderBaseline(t *testing.T) {
	libGit := newFakeGit(
		commit{sha: "l1", message: "feat(lib): stable"},
		commit{sha: "l2", message: "fix(lib): after stable"},
	).tag("lib", "1.0.0", "l1")
	appGit := newFakeGit(
		commit{sha: "a1", message: "feat(app): stable"},
		commit{sha: "a2", message: "feat(app)%beta: beta"},
	).tag("app", "1.0.0", "a1").tag("app", "1.1.0-beta.0", "a2")
	control := &composedControlGit{fakeGit: newFakeGit()}
	opts := Options{
		Packages: []*model.Package{
			{Name: "lib", Dir: "/w/lib/lib", RepoRoot: "/w/lib", Repository: "lib-source", Space: &model.Space{Name: "libs"}},
			{Name: "app", Dir: "/w/app/app", RepoRoot: "/w/app", Repository: "app-source", Space: &model.Space{Name: "apps"}},
		},
		Dependencies: []model.Dependency{{Consumer: "app", Provider: "lib"}},
		Repositories: map[string]RepositoryHistory{
			"control":    {Name: "control", Root: "/w", Git: control, Control: true},
			"lib-source": {Name: "lib-source", Root: "/w/lib", Path: "lib", Git: libGit},
			"app-source": {Name: "app-source", Root: "/w/app", Path: "app", Git: appGit},
		},
		RepositoryBaselines: []RepositoryBaseline{{
			Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "lib-source", Revision: "l1",
		}},
	}

	pl, err := Compute(t.Context(), control, opts)
	require.NoError(t, err)
	require.True(t, pl.IsFatal())
	assert.True(t, hasCode(pl, CodeRepositoryBoundary))
	assert.Contains(t, pl.Diagnostics[0].Message, "app@1.1.0-beta.0",
		"the stable tuple cannot silently stand in for the prerelease boundary")
}

func TestComposedFreshProviderWindowReadFailureIsReturned(t *testing.T) {
	libBase := newFakeGit(
		commit{sha: "l1", message: "feat(lib): stable"},
		commit{sha: "l2", message: "fix(lib): pending"},
	).tag("lib", "1.0.0", "l1")
	libGit := &boundaryReadFailureGit{fakeGit: libBase, boundary: "l2", err: errors.New("fresh window unavailable")}
	appGit := newFakeGit(
		commit{sha: "a1", message: "feat(app): stable"},
		commit{sha: "a2", message: "feat(app)%beta: beta"},
	).tag("app", "1.0.0", "a1").tag("app", "1.1.0-beta.0", "a2")
	control := &composedControlGit{fakeGit: newFakeGit()}
	opts := Options{
		Packages: []*model.Package{
			{Name: "lib", Dir: "/w/lib/lib", RepoRoot: "/w/lib", Repository: "lib-source", Space: &model.Space{Name: "libs"}},
			{Name: "app", Dir: "/w/app/app", RepoRoot: "/w/app", Repository: "app-source", Space: &model.Space{Name: "apps"}},
		},
		Dependencies: []model.Dependency{{Consumer: "app", Provider: "lib"}},
		Repositories: map[string]RepositoryHistory{
			"control":    {Name: "control", Root: "/w", Git: control, Control: true},
			"lib-source": {Name: "lib-source", Root: "/w/lib", Path: "lib", Git: libGit},
			"app-source": {Name: "app-source", Root: "/w/app", Path: "app", Git: appGit},
		},
		RepositoryBaselines: []RepositoryBaseline{
			{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "lib-source", Revision: "l1"},
			{Consumer: "app", ReleaseTag: "app@1.1.0-beta.0", Repository: "lib-source", Revision: "l2"},
		},
	}

	pl, err := Compute(t.Context(), control, opts)
	require.Nil(t, pl)
	require.Error(t, err)
	assert.ErrorContains(t, err, "lib-source history for app: fresh window unavailable")
}

func TestRepositoryInputClosureAlternatesGroupAndDependencyEdges(t *testing.T) {
	groupOne := &model.Space{Name: "one", Versioning: model.VersioningFixed, GroupIdentity: "central/one"}
	groupTwo := &model.Space{Name: "two", Versioning: model.VersioningFixed, GroupIdentity: "central/two"}
	cp := &computation{
		order: []string{"a", "b", "d", "e"},
		byName: map[string]*model.Package{
			"a": {Name: "a", Repository: "source-a", Space: groupOne},
			"b": {Name: "b", Repository: "source-b", Space: groupOne},
			"d": {Name: "d", Repository: "source-d", Space: groupTwo},
			"e": {Name: "e", Repository: "source-e", Space: groupTwo},
		},
		providers: map[string][]string{"d": {"b"}},
		repositoryReach: map[string][]string{
			"a": {"source-a"}, "b": {"source-b"}, "d": {"source-b", "source-d"}, "e": {"source-e"},
		},
		controlInputs: make(map[string]bool),
		histories: map[string]RepositoryHistory{
			"source-a": {Name: "source-a"}, "source-b": {Name: "source-b"},
			"source-d": {Name: "source-d"}, "source-e": {Name: "source-e"},
		},
	}

	order, inputs := cp.releaseRepositoryInputs()
	pl := &Plan{RepositoryInputOrder: order, RepositoryInputs: inputs}
	assert.ElementsMatch(t, []string{"source-a", "source-b", "source-d", "source-e"}, repositoryInputNames(pl, "e"),
		"group one reaches d through its dependency, then all of group two")
}

func TestPackagesChangedSinceComposedFailures(t *testing.T) {
	packageOnly := []*model.Package{{
		Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "apps"},
	}}
	base := func(control, source TagInventoryGitx) Options {
		return Options{Packages: packageOnly, Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Control: true, Git: control},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		}}
	}

	t.Run("missing control identity", func(t *testing.T) {
		opts := base(newFakeGit(), newFakeGit())
		control := opts.Repositories["control"]
		control.Control = false
		opts.Repositories["control"] = control
		_, err := PackagesChangedSince(t.Context(), newFakeGit(), opts, "base")
		require.Error(t, err)
		assert.ErrorContains(t, err, "composed history has no control repository")
	})

	t.Run("control cannot project gitlinks", func(t *testing.T) {
		_, err := PackagesChangedSince(t.Context(), newFakeGit(), base(newFakeGit(), newFakeGit()), "base")
		require.Error(t, err)
		assert.ErrorContains(t, err, `control repository cannot project gitlinks at "base"`)
	})

	t.Run("projection read fails", func(t *testing.T) {
		control := &projectingControlGit{fakeGit: newFakeGit(), err: errors.New("tree unavailable")}
		_, err := PackagesChangedSince(t.Context(), control, base(control, newFakeGit()), "base")
		require.Error(t, err)
		assert.ErrorContains(t, err, `resolving control gitlinks at "base": tree unavailable`)
	})

	t.Run("source range read fails", func(t *testing.T) {
		control := &projectingControlGit{fakeGit: newFakeGit(), links: map[string]string{"source": "s0"}}
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), commitsErr: errors.New("range unavailable")}
		_, err := PackagesChangedSince(t.Context(), control, base(control, source), "base")
		require.Error(t, err)
		assert.ErrorContains(t, err, `repository source commits since "s0": range unavailable`)
	})

	t.Run("source parser is invalid", func(t *testing.T) {
		control := &projectingControlGit{fakeGit: newFakeGit(), links: map[string]string{"source": "s0"}}
		opts := base(control, newFakeGit())
		source := opts.Repositories["source"]
		source.ParserConfig = ccme.Config{Separator: "ab"}
		opts.Repositories["source"] = source
		_, err := PackagesChangedSince(t.Context(), control, opts, "base")
		require.Error(t, err)
		assert.ErrorContains(t, err, "repository source parser")
	})
}

func TestControlRevisionCannotOrderAnUnpinnedSourceCommit(t *testing.T) {
	const (
		sourceCommit  = "1111111111111111111111111111111111111111"
		controlCommit = "cccccccccccccccccccccccccccccccccccccccc"
	)
	cp := &computation{
		ctx: context.Background(), controlRepo: "control",
		histories: map[string]RepositoryHistory{
			"control": {Name: "control", Control: true},
			"source":  {Name: "source", Path: "source", Git: newFakeGit(commit{sha: sourceCommit})},
		},
		controlPathIndex: map[string]int{"source": 0}, controlPathCount: 1,
		controlStates: map[string]*controlGitlinkState{
			controlCommit: {},
		},
		byKey: map[string]*commitRec{
			historyKey("source", sourceCommit):   {rank: 0},
			historyKey("control", controlCommit): {rank: 0},
		},
		ancNoGit: make(map[string]bool),
	}

	aNewer, comparable := cp.commitPrecedence(historyKey("control", controlCommit), historyKey("source", sourceCommit))
	assert.False(t, aNewer)
	assert.False(t, comparable, "a control commit with no source pin supplies no causal order")
	delete(cp.histories, "source")
	assert.False(t, cp.controlObserves(historyKey("control", controlCommit), historyKey("source", sourceCommit)),
		"an unknown repository cannot be inferred from an equal-looking object id")
}
