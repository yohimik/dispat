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

type failingRepositoryHistoryGit struct {
	*fakeGit
	tagErr, commitsErr, shallowErr, headErr error
	tagsOverride                            gitx.Tags
	head                                    string
}

func (g *failingRepositoryHistoryGit) Tags(ctx context.Context, pkg string, format gitx.TagFormat) (gitx.Tags, error) {
	if g.tagErr != nil {
		return nil, g.tagErr
	}
	if g.tagsOverride != nil {
		return g.tagsOverride, nil
	}
	return g.fakeGit.Tags(ctx, pkg, format)
}

func (g *failingRepositoryHistoryGit) Commits(ctx context.Context, boundary string) ([]gitx.Commit, error) {
	if g.commitsErr != nil {
		return nil, g.commitsErr
	}
	return g.fakeGit.Commits(ctx, boundary)
}

func (g *failingRepositoryHistoryGit) IsShallow(ctx context.Context) (bool, error) {
	if g.shallowErr != nil {
		return false, g.shallowErr
	}
	return g.fakeGit.IsShallow(ctx)
}

func (g *failingRepositoryHistoryGit) HeadSHA(context.Context) (string, error) {
	if g.headErr != nil {
		return "", g.headErr
	}
	return g.head, nil
}

func composedHistoryFailureOptions(source gitx.Git) Options {
	return Options{
		Packages: []*model.Package{{
			Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source",
			Space: &model.Space{Name: "apps"},
		}},
		Initials: map[string]ccme.Version{"app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: newFakeGit(), Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		},
	}
}

func TestComposedHistorySurfacesRepositoryReadFailures(t *testing.T) {
	t.Run("missing package history", func(t *testing.T) {
		opts := composedHistoryFailureOptions(newFakeGit())
		delete(opts.Repositories, "source")
		pl, err := Compute(t.Context(), newFakeGit(), opts)
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, `repository history "source" is missing`)
	})

	t.Run("bulk tag inventory", func(t *testing.T) {
		source := &bulkCountingGit{countingGit: counted(newFakeGit()), bulkErr: errors.New("refs unavailable")}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "repository source loading tags: refs unavailable")
	})

	t.Run("package tag inventory", func(t *testing.T) {
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), tagErr: errors.New("tag read failed")}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "app: tag read failed")
	})

	t.Run("commit window", func(t *testing.T) {
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), commitsErr: errors.New("log failed")}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "source history for app: log failed")
	})

	t.Run("shallow check", func(t *testing.T) {
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), shallowErr: errors.New("object database failed")}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "checking repository source completeness: object database failed")
	})

	t.Run("head read", func(t *testing.T) {
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), headErr: errors.New("HEAD unavailable")}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "reading repository source HEAD: HEAD unavailable")
	})

	t.Run("source parser", func(t *testing.T) {
		opts := composedHistoryFailureOptions(newFakeGit())
		source := opts.Repositories["source"]
		source.ParserConfig = ccme.Config{Separator: "ab"}
		opts.Repositories["source"] = source
		pl, err := Compute(t.Context(), newFakeGit(), opts)
		require.Nil(t, pl)
		require.Error(t, err)
		assert.ErrorContains(t, err, "repository source parser")
	})

	t.Run("duplicate semantic version tags are fatal", func(t *testing.T) {
		source := &failingRepositoryHistoryGit{fakeGit: newFakeGit(), tagsOverride: gitx.Tags{
			{Name: "app@1.0.0", Commit: "one", Version: v(1, 0, 0), Parsed: true},
			{Name: "release-1.0.0", Commit: "two", Version: v(1, 0, 0), Parsed: true},
		}}
		pl, err := Compute(t.Context(), newFakeGit(), composedHistoryFailureOptions(source))
		require.NoError(t, err)
		require.NotNil(t, pl)
		assert.True(t, pl.Fatal())
		assert.True(t, hasCode(pl, CodeDuplicateVersionTag))
	})

	t.Run("repository identity defaults to its map key", func(t *testing.T) {
		opts := composedHistoryFailureOptions(newFakeGit())
		source := opts.Repositories["source"]
		source.Name = ""
		opts.Repositories["source"] = source
		pl, err := Compute(t.Context(), newFakeGit(), opts)
		require.NoError(t, err)
		require.False(t, pl.Fatal(), "%v", pl.Diagnostics)
		assert.Contains(t, pl.RepositoryInputOrder, "source")
	})
}

func TestControlCheckpointIndexRetainsExactMultiPackageSnapshot(t *testing.T) {
	const (
		appCommit      = "1111111111111111111111111111111111111111"
		providerCommit = "2222222222222222222222222222222222222222"
		oldAppCommit   = "3333333333333333333333333333333333333333"
		c1             = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		c2             = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		c3             = "cccccccccccccccccccccccccccccccccccccccc"
		zero           = "0000000000000000000000000000000000000000"
	)
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{
		{SHA: c3, Parents: []string{c2}, Message: "chore(release): unknown@1.0.0"},
		{SHA: c2, Parents: []string{c1}, Message: "chore(release): app@1.0.0", Gitlinks: map[string]gitx.GitlinkTransition{
			"app":      {From: oldAppCommit, To: appCommit},
			"provider": {From: providerCommit, To: zero},
			"ignored":  {From: oldAppCommit, To: appCommit},
		}},
		{SHA: c1, Gitlinks: map[string]gitx.GitlinkTransition{
			"app": {From: zero, To: oldAppCommit}, "provider": {From: zero, To: providerCommit},
		}},
	}}
	cp := &computation{
		ctx: context.Background(), controlRepo: "control", stats: &HistoryStats{},
		pkgs: []*model.Package{{Name: "app", Repository: "app-source"}, {Name: "tool", Repository: "control"}},
		histories: map[string]RepositoryHistory{
			"control":    {Name: "control", Control: true, Git: control},
			"app-source": {Name: "app-source", Path: "app", Git: newFakeGit()},
			"provider":   {Name: "provider", Path: "provider", Git: newFakeGit()},
		},
		tags: map[string]gitx.Tags{
			"app":  {{Name: "app@1.0.0", Commit: appCommit}},
			"tool": {{Name: "tool@1.0.0", Commit: c2}},
		},
	}

	snapshots, ambiguous, err := cp.controlCheckpoints()
	require.NoError(t, err)
	app := snapshots[checkpointTagKey("app-source", "app@1.0.0")]
	tool := snapshots[checkpointTagKey("control", "tool@1.0.0")]
	assert.Empty(t, ambiguous)
	assert.Equal(t, c2, app.commit)
	assert.Equal(t, c2, tool.commit)
	assert.Same(t, app.state, tool.state, "one control commit has one immutable causal snapshot")
	assert.Equal(t, appCommit, cp.controlSnapshotLink(app, "app-source"))
	assert.Empty(t, cp.controlSnapshotLink(app, "provider"), "an all-zero transition removes the provider pin")
	assert.NotContains(t, snapshots, checkpointTagKey("app-source", "unknown@1.0.0"))
	assert.EqualValues(t, 1, cp.stats.ControlSnapshotRuns.Load())
}

func TestControlCommitWindowIncludesMergedWorkOutsideBoundaryAncestry(t *testing.T) {
	cp := &computation{controlHistory: []gitx.ControlHistoryCommit{
		{SHA: "c4", Parents: []string{"c3"}, Message: "fix(app): after merge"},
		{SHA: "c3", Parents: []string{"c2", "b2"}, Message: "chore: merge branch"},
		{SHA: "b2", Parents: []string{"c1"}, Message: "feat(app): branch work"},
		{SHA: "c2", Parents: []string{"c1"}, Message: "chore(release): app@1.0.0"},
		{SHA: "c1", Message: "feat(app): initial"},
	}}

	window := cp.controlCommitsAfter("c2")
	require.Len(t, window, 3)
	assert.Equal(t, []string{"c4", "c3", "b2"}, []string{window[0].SHA, window[1].SHA, window[2].SHA})
	assert.Equal(t, "feat(app): branch work", window[2].Message)
	assert.Len(t, cp.controlCommitsAfter(""), 5, "an empty boundary retains the complete reachable control history")
}

func TestPropagatedControlIntentIsAConsumerHistoryInput(t *testing.T) {
	const controlCommit = "cccccccccccccccccccccccccccccccccccccccc"
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{{
		SHA: controlCommit, Message: "feat(lib)^%beta++1: coordinated rollout",
	}}}
	pl, err := Compute(t.Context(), control, Options{
		Packages: []*model.Package{
			{Name: "lib", Dir: "/w/lib/lib", RepoRoot: "/w/lib", Repository: "lib-source", Space: &model.Space{Name: "libs"}},
			{Name: "app", Dir: "/w/app/app", RepoRoot: "/w/app", Repository: "app-source", Space: &model.Space{Name: "apps"}},
		},
		Dependencies: []model.Dependency{{Consumer: "app", Provider: "lib"}},
		Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":    {Name: "control", Root: "/w", Git: control, Control: true},
			"lib-source": {Name: "lib-source", Root: "/w/lib", Path: "lib", Git: newFakeGit()},
			"app-source": {Name: "app-source", Root: "/w/app", Path: "app", Git: newFakeGit()},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.Fatal(), "%v", pl.Diagnostics)
	assert.True(t, pl.Releases["app"].Releasing())
	assert.Equal(t, "beta", pl.Releases["app"].Channel)
	assert.ElementsMatch(t, []string{"app-source", "control", "lib-source"}, repositoryInputNames(pl, "app"))
}

func TestCausalControlPrecedenceOrdersOnlyObservedSourceWork(t *testing.T) {
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
			controlCommit: {links: updatePersistentLink(nil, 0, 1, 0, sourceCommit)},
		},
		byKey: map[string]*commitRec{
			historyKey("source", sourceCommit):   {rank: 0},
			historyKey("control", controlCommit): {rank: 0},
		},
		ancNoGit: make(map[string]bool),
	}
	sourceKey, controlKey := historyKey("source", sourceCommit), historyKey("control", controlCommit)

	aNewer, comparable := cp.commitPrecedence(controlKey, sourceKey)
	assert.True(t, aNewer)
	assert.True(t, comparable)
	aNewer, comparable = cp.commitPrecedence(sourceKey, controlKey)
	assert.False(t, aNewer)
	assert.True(t, comparable)
	assert.True(t, cp.controlResolves(controlKey, []channelPick{{commit: controlKey}, {commit: sourceKey}}))
	assert.False(t, cp.controlResolves(sourceKey, []channelPick{{commit: sourceKey}}))
}
