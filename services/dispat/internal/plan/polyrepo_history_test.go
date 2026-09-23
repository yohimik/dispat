// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func repositoryInputNames(pl *Plan, name string) []string {
	var inputs []string
	for i, repository := range pl.RepositoryInputOrder {
		words := pl.RepositoryInputs[name]
		if i/64 < len(words) && words[i/64]&(uint64(1)<<uint(i%64)) != 0 {
			inputs = append(inputs, repository)
		}
	}
	return inputs
}

type composedControlGit struct {
	*fakeGit
	control []gitx.ControlHistoryCommit
}

func (g *composedControlGit) ControlGitlinkHistory(context.Context) ([]gitx.ControlHistoryCommit, error) {
	return g.control, nil
}

func TestComposedHistoryPropagatesSourceWorkAcrossRepositories(t *testing.T) {
	control := &composedControlGit{fakeGit: newFakeGit()}
	providerGit := newFakeGit(commit{sha: "a1", message: "feat(lib)^: expose stream", files: []string{"lib.go"}})
	consumerGit := newFakeGit()
	libSpace, appSpace := &model.Space{Name: "libs"}, &model.Space{Name: "apps"}
	packages := []*model.Package{
		{Name: "lib", Dir: "/workspace/a/lib", RepoRoot: "/workspace/a", Repository: "source-a", Space: libSpace},
		{Name: "app", Dir: "/workspace/b/app", RepoRoot: "/workspace/b", Repository: "source-b", Space: appSpace},
	}
	stats := &HistoryStats{}
	pl, err := Compute(context.Background(), control, Options{
		Packages:     packages,
		Dependencies: []model.Dependency{{Consumer: "app", Provider: "lib"}},
		Initials:     map[string]ccme.Version{"lib": v(1, 0, 0), "app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/workspace", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/workspace/a", Path: "a", Git: providerGit},
			"source-b": {Name: "source-b", Root: "/workspace/b", Path: "b", Git: consumerGit},
		},
		HistoryStats: stats,
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.True(t, pl.Releases["lib"].IsReleasing())
	assert.True(t, pl.Releases["app"].IsReleasing())
	require.NotEmpty(t, pl.Releases["app"].Sources)
	assert.Equal(t, "a1", pl.Releases["app"].Sources[0].Commit, "public provenance keeps the raw source SHA")
	assert.EqualValues(t, 1, stats.ReachabilityEdges.Load())
	assert.EqualValues(t, 1, stats.ControlSnapshotRuns.Load())
}

func TestControlCheckpointsKeepEqualTagSpellingsRepositoryLocal(t *testing.T) {
	const (
		a1   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		b1   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		c1   = "1111111111111111111111111111111111111111"
		c2   = "2222222222222222222222222222222222222222"
		zero = "0000000000000000000000000000000000000000"
	)
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{
		{SHA: c2, Parents: []string{c1}, Message: "chore(release): v1.0.0", Gitlinks: map[string]gitx.GitlinkTransition{
			"sources/b": {From: zero, To: b1},
		}},
		{SHA: c1, Message: "chore(release): v1.0.0", Gitlinks: map[string]gitx.GitlinkTransition{
			"sources/a": {From: zero, To: a1},
		}},
	}}
	cp := &computation{
		ctx: context.Background(), controlRepo: "control", stats: &HistoryStats{},
		pkgs: []*model.Package{{Name: "a", Repository: "source-a"}, {Name: "b", Repository: "source-b"}},
		histories: map[string]RepositoryHistory{
			"control":  {Name: "control", Git: control, Control: true},
			"source-a": {Name: "source-a", Path: "sources/a", Git: newFakeGit()},
			"source-b": {Name: "source-b", Path: "sources/b", Git: newFakeGit()},
		},
		tags: map[string]gitx.Tags{
			"a": {{Name: "v1.0.0", Commit: a1}},
			"b": {{Name: "v1.0.0", Commit: b1}},
		},
	}
	snapshots, ambiguous, err := cp.controlCheckpoints()
	require.NoError(t, err)
	assert.False(t, ambiguous[checkpointTagKey("source-a", "v1.0.0")])
	assert.False(t, ambiguous[checkpointTagKey("source-b", "v1.0.0")])
	assert.Equal(t, c1, snapshots[checkpointTagKey("source-a", "v1.0.0")].commit)
	assert.Equal(t, c2, snapshots[checkpointTagKey("source-b", "v1.0.0")].commit)
	assert.Equal(t, a1, cp.controlSnapshotLink(snapshots[checkpointTagKey("source-b", "v1.0.0")], "source-a"))
	assert.EqualValues(t, 1, cp.stats.ControlSnapshotRuns.Load())
}

func TestMissingExternalDependencyIsReportedButNotAddedToGraph(t *testing.T) {
	consumer := &model.Package{Name: "app", Dir: "/r/app", Space: &model.Space{Name: "apps"}}
	pl, err := Compute(context.Background(), newFakeGit(), Options{
		Packages: []*model.Package{consumer},
		InactiveExternalDependencies: []model.Dependency{{
			Consumer: "app", Provider: "optional-sdk", Kind: model.KindOptionalDependencies,
		}},
	})
	require.NoError(t, err)
	require.Len(t, pl.Diagnostics, 1)
	assert.Equal(t, CodeExternalProviderAbsent, pl.Diagnostics[0].Code)
	assert.Equal(t, "app", pl.Diagnostics[0].Pkg)
	assert.Empty(t, pl.Providers["app"])
}

func TestComposedHistorySharesRepositoryInventoryAndWindows(t *testing.T) {
	const consumers = 10
	control := &composedControlGit{fakeGit: newFakeGit()}
	source := counted(newFakeGit(
		commit{sha: "a1", message: "feat(provider)^: shared change", files: []string{"provider/file"}},
	))
	space := &model.Space{Name: "workspace"}
	packages := []*model.Package{{Name: "provider", Dir: "/workspace/source/provider", RepoRoot: "/workspace/source", Repository: "source", Space: space}}
	var dependencies []model.Dependency
	for i := 0; i < consumers; i++ {
		name := fmt.Sprintf("consumer-%02d", i)
		packages = append(packages, &model.Package{Name: name, Dir: "/workspace/source/" + name,
			RepoRoot: "/workspace/source", Repository: "source", Space: space})
		dependencies = append(dependencies, model.Dependency{Consumer: name, Provider: "provider"})
	}
	stats := &HistoryStats{}
	pl, err := Compute(context.Background(), control, Options{
		Packages: packages, Dependencies: dependencies, HistoryStats: stats,
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/workspace", Git: control, Control: true},
			"source":  {Name: "source", Root: "/workspace/source", Path: "source", Git: source},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.EqualValues(t, 1, stats.TagInventories.Load(), "one complete source tag inventory")
	assert.EqualValues(t, 1, stats.CommitWindows.Load(), "one immutable source boundary window")
	assert.EqualValues(t, 1, stats.UniqueCommits.Load(), "the source record is parsed once")
	assert.EqualValues(t, 1, stats.ControlSnapshotRuns.Load(), "one bulk control transition index")
	assert.EqualValues(t, consumers, stats.ReachabilityEdges.Load(), "each graph edge is visited once")
}

func TestIncomparableSourceChannelsNeedCausalControlResolution(t *testing.T) {
	const (
		a1 = "a1"
		a2 = "a2"
		b1 = "b1"
		c1 = "c1"
	)
	aGit := newFakeGit(commit{sha: a1}, commit{sha: a2})
	bGit := newFakeGit(commit{sha: b1})
	cp := &computation{
		ctx: context.Background(), controlRepo: "control",
		histories: map[string]RepositoryHistory{
			"control":  {Name: "control", Control: true},
			"source-a": {Name: "source-a", Path: "a", Git: aGit},
			"source-b": {Name: "source-b", Path: "b", Git: bGit},
		},
		controlPathIndex: map[string]int{"a": 0, "b": 1}, controlPathCount: 2,
		controlStates: map[string]*controlGitlinkState{}, ancNoGit: map[string]bool{},
		byKey: map[string]*commitRec{
			historyKey("source-a", a1): {rank: 1}, historyKey("source-a", a2): {rank: 0},
			historyKey("source-b", b1): {rank: 0}, historyKey("control", c1): {rank: 0},
		},
	}
	links := updatePersistentLink(nil, 0, 2, 0, a1)
	links = updatePersistentLink(links, 0, 2, 1, b1)
	cp.controlStates[c1] = &controlGitlinkState{links: links}
	initial := []channelPick{
		{channel: "beta", commit: historyKey("source-a", a1)},
		{channel: "rc", commit: historyKey("source-b", b1)},
	}
	cp.proposedAll = map[string][]channelPick{"app": initial}
	cp.validateChannelPrecedence("app")
	require.Len(t, cp.diags, 1)
	assert.Equal(t, CodeRepositoryPrecedence, cp.diags[0].Code)

	cp.diags = nil
	cp.proposedAll["app"] = append(initial, channelPick{channel: "beta", commit: historyKey("control", c1)})
	cp.validateChannelPrecedence("app")
	assert.Empty(t, cp.diags, "a propagated control candidate containing both source revisions resolves precedence")

	cp.proposedAll["app"] = append(cp.proposedAll["app"], channelPick{channel: "alpha", commit: historyKey("source-a", a2)})
	cp.validateChannelPrecedence("app")
	require.Len(t, cp.diags, 1)
	assert.Equal(t, CodeRepositoryPrecedence, cp.diags[0].Code,
		"later source work restores the conflict because the old control snapshot did not observe it")
}

func TestComposedPrereleaseUsesItsOwnProviderBoundary(t *testing.T) {
	libGit := newFakeGit(
		commit{sha: "l1", message: "feat(lib): initial"},
		commit{sha: "l2", message: "feat(lib): minor"},
		commit{sha: "l3", message: "fix(lib)^: patch"},
	).tag("lib", "1.0.0", "l1").tag("lib", "1.1.0", "l2").tag("lib", "1.1.1", "l3")
	appGit := newFakeGit(
		commit{sha: "a1", message: "feat(app): stable"},
		commit{sha: "a2", message: "feat(app)%beta: beta"},
	).tag("app", "1.0.0", "a1").tag("app", "1.1.0-beta.0", "a2")
	control := &composedControlGit{fakeGit: newFakeGit()}
	stats := &HistoryStats{}
	pl, err := Compute(context.Background(), control, Options{
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
			{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "lib-source", Revision: "l3"},
			{Consumer: "app", ReleaseTag: "app@1.1.0-beta.0", Repository: "lib-source", Revision: "l2"},
		},
		HistoryStats: stats,
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	app := pl.Releases["app"]
	require.NotNil(t, app)
	assert.True(t, app.CatchUp)
	assert.Equal(t, "catch-up from lib", app.Reason())
	assert.True(t, hasCode(pl, CodeCatchUp))
	assert.EqualValues(t, 4, stats.CommitWindows.Load(), "each owner and provider keeps distinct stable and fresh shared windows")
}

// TestComposedOwedWindowOverTheProvidersRepository is §13.3's owed window in a
// composed workspace (§27): app, in its own repository, released at a boundary
// in lib's repository that holds lib's caret unit, lib failed to publish it and
// published it later in a run app sat out. Neither ordinary window over lib's
// repository holds the unit any more, so the owed window over that repository,
// after the lib release app's boundary reaches, keeps app's debt visible.
func TestComposedOwedWindowOverTheProvidersRepository(t *testing.T) {
	libGit := newFakeGit(
		commit{sha: "l1", message: "feat(lib): initial"},
		commit{sha: "l2", message: "feat(lib)^: streaming"},
		commit{sha: "l3", message: "chore(lib): retry the provider"},
	).tag("lib", "1.0.0", "l1").tag("lib", "1.1.0", "l3")
	appGit := newFakeGit(
		commit{sha: "a1", message: "feat(app): own flag"},
		commit{sha: "a2", message: "chore(app): catch up"},
	).tag("app", "1.1.0", "a1")
	control := &composedControlGit{fakeGit: newFakeGit()}
	options := Options{
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
			{Consumer: "app", ReleaseTag: "app@1.1.0", Repository: "lib-source", Revision: "l2"},
		},
	}
	stats := &HistoryStats{}
	options.HistoryStats = stats
	pl, err := Compute(context.Background(), control, options)
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.False(t, pl.Releases["lib"].IsReleasing(), "lib released everything it had")
	app := pl.Releases["app"]
	require.True(t, app.IsReleasing(), "lib still owes app l2: %v", pl.Diagnostics)
	assert.Equal(t, v(1, 1, 1), app.Next)
	assert.True(t, app.CatchUp)
	require.Len(t, app.Sources, 1)
	assert.Equal(t, "lib", app.Sources[0].Provider)
	assert.Equal(t, "l2", app.Sources[0].Commit, "public provenance keeps the raw source SHA")
	assert.EqualValues(t, 4, stats.CommitWindows.Load(),
		"three ordinary windows (lib's and app's in lib's repository, app's own) and one owed")

	// app released the catch-up at a boundary past lib's release: its window
	// over lib's repository is lib's own again and nothing is owed.
	appGit.tag("app", "1.1.1", "a2")
	options.RepositoryBaselines = append(options.RepositoryBaselines,
		RepositoryBaseline{Consumer: "app", ReleaseTag: "app@1.1.1", Repository: "lib-source", Revision: "l3"})
	settled, err := Compute(context.Background(), control, options)
	require.NoError(t, err)
	require.False(t, settled.IsFatal(), "%v", settled.Diagnostics)
	assert.False(t, settled.Releases["app"].IsReleasing(), "the catch-up delivered lib's release")
}

func TestRepositoryBaselineMustNameExactConsumerReleaseTag(t *testing.T) {
	source := newFakeGit(commit{sha: "a1", message: "feat(app): initial"}).tag("app", "1.0.0", "a1")
	control := &composedControlGit{fakeGit: newFakeGit()}
	base := Options{
		Packages: []*model.Package{{Name: "app", Dir: "/w/app", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "apps"}}},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		},
	}
	for _, tc := range []struct {
		name     string
		baseline RepositoryBaseline
	}{
		{"unknown consumer", RepositoryBaseline{Consumer: "missing", ReleaseTag: "app@1.0.0", Repository: "source", Revision: "a1"}},
		{"foreign tag", RepositoryBaseline{Consumer: "app", ReleaseTag: "lib@1.0.0", Repository: "source", Revision: "a1"}},
		{"missing tag", RepositoryBaseline{Consumer: "app", ReleaseTag: "app@2.0.0", Repository: "source", Revision: "a1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			opts.RepositoryBaselines = []RepositoryBaseline{tc.baseline}
			pl, err := Compute(context.Background(), control, opts)
			require.NoError(t, err)
			require.True(t, pl.IsFatal())
			assert.True(t, hasCode(pl, CodeRepositoryBoundary))
		})
	}
	duplicate := base
	duplicate.RepositoryBaselines = []RepositoryBaseline{
		{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "source", Revision: "a1"},
		{Consumer: "APP", ReleaseTag: "app@1.0.0", Repository: "SOURCE", Revision: "a1"},
	}
	pl, err := Compute(context.Background(), control, duplicate)
	require.NoError(t, err)
	assert.True(t, pl.IsFatal())
	assert.True(t, hasCode(pl, CodeRepositoryBoundary))
}

func TestControlHistoryAdmissionIsLazyAndRequiresAProvenBoundary(t *testing.T) {
	const (
		s1 = "1111111111111111111111111111111111111111"
		c0 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		c1 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	source := newFakeGit(commit{sha: s1, message: "feat(app): initial"}).tag("app", "1.0.0", s1)
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{
		{SHA: c1, Parents: []string{c0}, Message: "fix(app): fleet patch"},
		{SHA: c0, Message: "chore: configure workspace"},
	}}
	options := Options{
		Packages: []*model.Package{{Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "apps"}}},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		},
	}

	withoutIntent := *control
	withoutIntent.control = []gitx.ControlHistoryCommit{{SHA: c0, Message: "chore: configure workspace"}}
	options.Repositories["control"] = RepositoryHistory{Name: "control", Root: "/w", Git: &withoutIntent, Control: true}
	pl, err := Compute(context.Background(), &withoutIntent, options)
	require.NoError(t, err)
	assert.False(t, pl.IsFatal(), "a tag-only source release does not require an artificial control tuple")
	assert.ElementsMatch(t, []string{"source"}, repositoryInputNames(pl, "app"),
		"control history which cannot affect the package is not a publication input")

	options.Repositories["control"] = RepositoryHistory{Name: "control", Root: "/w", Git: control, Control: true}
	pl, err = Compute(context.Background(), control, options)
	require.NoError(t, err)
	assert.True(t, pl.IsFatal())
	assert.True(t, hasCode(pl, CodeRepositoryBoundary))

	options.RepositoryBaselines = []RepositoryBaseline{{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "control", Revision: c0}}
	pl, err = Compute(context.Background(), control, options)
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.Equal(t, v(1, 0, 1), pl.Releases["app"].Next)
	assert.ElementsMatch(t, []string{"control", "source"}, repositoryInputNames(pl, "app"))
	encoded, err := json.Marshal(&Plan{
		RepositoryInputOrder: pl.RepositoryInputOrder,
		RepositoryInputs:     pl.RepositoryInputs,
	})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "RepositoryInput", "execution bitsets do not change public JSON plans")
}

func TestHeldControlIntentRemainsARepositoryInput(t *testing.T) {
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{{
		SHA:     "cccccccccccccccccccccccccccccccccccccccc",
		Message: "release(lib): wait\n\nRelease-As: none",
	}}}
	pl, err := Compute(context.Background(), control, Options{
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
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.Equal(t, []string{"lib"}, pl.Held())
	assert.ElementsMatch(t, []string{"app-source", "control", "lib-source"}, repositoryInputNames(pl, "app"),
		"a held provider's control input remains relevant to its consumer")
}

func TestCanceledControlIntentRemainsARepositoryInput(t *testing.T) {
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{{
		SHA:     "cccccccccccccccccccccccccccccccccccccccc",
		Message: "cancel(app): discard pending work",
	}}}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{{Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "apps"}}},
		Initials: map[string]ccme.Version{"app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: newFakeGit()},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.False(t, pl.Releases["app"].IsReleasing())
	assert.ElementsMatch(t, []string{"control", "source"}, repositoryInputNames(pl, "app"),
		"a cancellation still consults control history even when no work survives")
}

func TestControlSnapshotsShareUnchangedPersistentRoots(t *testing.T) {
	const repositories = 64
	histories := map[string]RepositoryHistory{"control": {Name: "control", Control: true}}
	packages := make([]*model.Package, 0, repositories)
	for i := 0; i < repositories; i++ {
		name := fmt.Sprintf("source-%02d", i)
		histories[name] = RepositoryHistory{Name: name, Path: "sources/" + name, Git: newFakeGit()}
		packages = append(packages, &model.Package{Name: fmt.Sprintf("pkg-%02d", i), Repository: name})
	}
	const commits = 256
	rows := make([]gitx.ControlHistoryCommit, commits)
	transitions := 0
	for chronological := 0; chronological < commits; chronological++ {
		index := commits - chronological - 1
		sha := fmt.Sprintf("%040x", chronological+1)
		rows[index] = gitx.ControlHistoryCommit{SHA: sha, Message: "chore: checkpoint"}
		if chronological > 0 {
			rows[index].Parents = []string{fmt.Sprintf("%040x", chronological)}
		}
		if chronological%32 == 0 {
			path := fmt.Sprintf("sources/source-%02d", chronological%repositories)
			rows[index].Gitlinks = map[string]gitx.GitlinkTransition{path: {To: fmt.Sprintf("%040x", 1000+chronological)}}
			transitions++
		}
	}
	control := &composedControlGit{fakeGit: newFakeGit(), control: rows}
	histories["control"] = RepositoryHistory{Name: "control", Git: control, Control: true}
	stats := &HistoryStats{}
	cp := &computation{ctx: context.Background(), controlRepo: "control", pkgs: packages,
		histories: histories, tags: map[string]gitx.Tags{}, stats: stats}
	_, _, err := cp.controlCheckpoints()
	require.NoError(t, err)
	assert.Same(t, cp.controlStates[fmt.Sprintf("%040x", 2)].links, cp.controlStates[fmt.Sprintf("%040x", 31)].links,
		"commits without gitlink deltas share the same immutable root")
	assert.LessOrEqual(t, stats.PersistentLinkNodes.Load(), int64(transitions*7),
		"64 repositories require at most seven persistent nodes per changed gitlink")
	assert.EqualValues(t, 1, stats.ControlSnapshotRuns.Load())
}

func TestDistinctBoundaryWindowsRetainCanonicalPayloadOnce(t *testing.T) {
	large := fmt.Sprintf("fix(one,two): %010000d", 1)
	source := newFakeGit(
		commit{sha: "c1", message: "feat(one,two): initial"},
		commit{sha: "c2", message: large},
		commit{sha: "c3", message: large},
	).tag("one", "1.0.0", "c1").tag("two", "1.0.0", "c2")
	control := &composedControlGit{fakeGit: newFakeGit()}
	stats := &HistoryStats{}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "one", Dir: "/w/source/one", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "one"}},
			{Name: "two", Dir: "/w/source/two", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "two"}},
		},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		},
		HistoryStats: stats,
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.EqualValues(t, 2, stats.UniqueCommits.Load())
	assert.EqualValues(t, 3, stats.WindowCommitRefs.Load(), "overlapping windows retain only compact canonical keys")
	assert.Less(t, stats.CanonicalBytes.Load(), int64(2*len(large)+500), "each unique commit payload is cloned once")
}

func TestRepositoryLocalNonPackageScopesAndEqualSHAsStayIsolated(t *testing.T) {
	const shared = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	aGit := newFakeGit(
		commit{sha: shared, message: "feat(a)!: original a", author: "Author A", email: "a@example.com"},
		commit{sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", message: "fix(a): corrected a\n\nEdits: " + shared, author: "Editor A", email: "edit@example.com"},
		commit{sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab", message: "chore(internal-a): maintenance"},
	)
	bGit := newFakeGit(commit{sha: shared, message: "feat(b)!: original b", author: "Author B", email: "b@example.com"})
	control := &composedControlGit{fakeGit: newFakeGit()}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/a", RepoRoot: "/w/a", Repository: "source-a", Space: &model.Space{Name: "a"}},
			{Name: "b", Dir: "/w/b/b", RepoRoot: "/w/b", Repository: "source-b", Space: &model.Space{Name: "b"}},
		},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true, NonPackageScopes: []string{"release"}},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "a", Git: aGit, NonPackageScopes: []string{"internal-a"}},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "b", Git: bGit, NonPackageScopes: []string{"internal-b"}},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	for _, diagnostic := range pl.Diagnostics {
		assert.NotEqual(t, CodeUnknownInclude, diagnostic.Code)
		assert.NotEqual(t, CodeInertUnit, diagnostic.Code)
	}
	a, b := pl.Releases["a"], pl.Releases["b"]
	require.Len(t, a.Units, 1)
	require.Len(t, b.Units, 1)
	assert.Equal(t, []string{shared[:12]}, a.UnitCorrects(a.Units[0]), "public correction provenance uses the display SHA")
	assert.Empty(t, b.UnitCorrects(b.Units[0]), "the equal raw SHA in source-b is a different commit identity")
	assert.Equal(t, []Author{{Name: "Editor A", Email: "edit@example.com"}}, a.AuthorsFor(a.Units[0]))
	assert.Equal(t, []Author{{Name: "Author B", Email: "b@example.com"}}, b.AuthorsFor(b.Units[0]))
}

func TestReleaseAsAcrossRepositoriesNeedsCausalControl(t *testing.T) {
	const (
		s1 = "1111111111111111111111111111111111111111"
		c1 = "cccccccccccccccccccccccccccccccccccccccc"
	)
	source := newFakeGit(commit{sha: s1, message: "release(app): source pin\n\nRelease-As: 1.1.0"})
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{
		{SHA: c1, Message: "release(app): fleet pin\n\nRelease-As: 2.0.0"},
	}}
	options := Options{
		Packages: []*model.Package{{Name: "app", Dir: "/w/source/app", RepoRoot: "/w/source", Repository: "source", Space: &model.Space{Name: "apps"}}},
		Initials: map[string]ccme.Version{"app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/w", Git: control, Control: true},
			"source":  {Name: "source", Root: "/w/source", Path: "source", Git: source},
		},
	}
	pl, err := Compute(context.Background(), control, options)
	require.NoError(t, err)
	assert.True(t, pl.IsFatal())
	assert.True(t, hasCode(pl, CodeRepositoryPrecedence))

	control.control[0].Gitlinks = map[string]gitx.GitlinkTransition{"source": {To: s1}}
	pl, err = Compute(context.Background(), control, options)
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.Equal(t, "2.0.0", pl.Releases["app"].Next.String())
}

func TestFixedGroupPinsAcrossSourcesNeedCausalControl(t *testing.T) {
	aGit := newFakeGit(commit{sha: "a1", message: "release(a): pin a\n\nRelease-As: 2.0.0"})
	bGit := newFakeGit(commit{sha: "b1", message: "release(b): pin b\n\nRelease-As: 3.0.0"})
	control := &composedControlGit{fakeGit: newFakeGit()}
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixed, GroupIdentity: "central/shared"}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/a", RepoRoot: "/w/a", Repository: "source-a", Space: shared},
			{Name: "b", Dir: "/w/b/b", RepoRoot: "/w/b", Repository: "source-b", Space: shared},
		},
		Initials: map[string]ccme.Version{"a": v(1, 0, 0), "b": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "a", Git: aGit},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "b", Git: bGit},
		},
	})
	require.NoError(t, err)
	assert.True(t, pl.IsFatal())
	assert.True(t, hasCode(pl, CodeRepositoryPrecedence))
	assert.True(t, hasCode(pl, CodeFixedPinConflict), "legacy fixed-group warning remains alongside fatal causal ambiguity")
}

func TestFixedGroupAcrossSourcesWithNoPins(t *testing.T) {
	control := &composedControlGit{fakeGit: newFakeGit()}
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixed, GroupIdentity: "central/shared"}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/a", RepoRoot: "/w/a", Repository: "source-a", Space: shared},
			{Name: "b", Dir: "/w/b/b", RepoRoot: "/w/b", Repository: "source-b", Space: shared},
		},
		Initials: map[string]ccme.Version{"a": v(1, 0, 0), "b": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "a", Git: newFakeGit()},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "b", Git: newFakeGit()},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	assert.False(t, pl.Releases["a"].IsReleasing())
	assert.False(t, pl.Releases["b"].IsReleasing())
}

func TestFixedGroupCarriesRepositoryInputsWithoutADependencyEdge(t *testing.T) {
	control := &composedControlGit{fakeGit: newFakeGit()}
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixed, GroupIdentity: "central/shared"}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/w/a/a", RepoRoot: "/w/a", Repository: "source-a", Space: shared},
			{Name: "b", Dir: "/w/b/b", RepoRoot: "/w/b", Repository: "source-b", Space: shared},
		},
		Initials: map[string]ccme.Version{"a": v(1, 0, 0), "b": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":  {Name: "control", Root: "/w", Git: control, Control: true},
			"source-a": {Name: "source-a", Root: "/w/a", Path: "a", Git: newFakeGit(commit{sha: "a1", message: "fix(a): patch"})},
			"source-b": {Name: "source-b", Root: "/w/b", Path: "b", Git: newFakeGit()},
		},
	})
	require.NoError(t, err)
	require.False(t, pl.IsFatal(), "%v", pl.Diagnostics)
	require.True(t, pl.Releases["a"].IsReleasing())
	require.True(t, pl.Releases["b"].FixedRide)
	assert.Empty(t, pl.Providers["b"], "the shared group is the only relationship")
	assert.ElementsMatch(t, []string{"source-a", "source-b"}, repositoryInputNames(pl, "b"))
	assert.Equal(t, &pl.RepositoryInputs["a"][0], &pl.RepositoryInputs["b"][0],
		"equal group closures share one immutable bitset")
}

func TestRepositoryInputsUseMultipleWords(t *testing.T) {
	cp := &computation{
		order:           []string{"last"},
		byName:          map[string]*model.Package{"last": {Name: "last", Repository: "source-64"}},
		repositoryReach: map[string][]string{"last": {"source-64"}},
		controlInputs:   map[string]bool{"last": true},
		histories:       make(map[string]RepositoryHistory),
		controlRepo:     "control",
	}
	cp.histories["control"] = RepositoryHistory{Name: "control"}
	for i := range 65 {
		name := fmt.Sprintf("source-%02d", i)
		cp.histories[name] = RepositoryHistory{Name: name}
	}

	order, inputs := cp.releaseRepositoryInputs()
	pl := &Plan{RepositoryInputOrder: order, RepositoryInputs: inputs}

	require.Len(t, inputs["last"], 2)
	assert.ElementsMatch(t, []string{"control", "source-64"}, repositoryInputNames(pl, "last"))
}

func BenchmarkControlCheckpointPersistentSnapshots(b *testing.B) {
	const repositories = 32
	histories := map[string]RepositoryHistory{"control": {Name: "control", Control: true}}
	packages := make([]*model.Package, 0, repositories)
	for i := 0; i < repositories; i++ {
		name := fmt.Sprintf("source-%02d", i)
		histories[name] = RepositoryHistory{Name: name, Path: "sources/" + name, Git: newFakeGit()}
		packages = append(packages, &model.Package{Name: fmt.Sprintf("pkg-%02d", i), Repository: name})
	}
	commits := make([]gitx.ControlHistoryCommit, 2000)
	for i := range commits {
		sha := fmt.Sprintf("%040x", len(commits)-i)
		commits[i] = gitx.ControlHistoryCommit{SHA: sha, Message: "chore: control", Gitlinks: map[string]gitx.GitlinkTransition{}}
		if i+1 < len(commits) {
			commits[i].Parents = []string{fmt.Sprintf("%040x", len(commits)-i-1)}
		}
	}
	control := &composedControlGit{fakeGit: newFakeGit(), control: commits}
	histories["control"] = RepositoryHistory{Name: "control", Git: control, Control: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cp := &computation{ctx: context.Background(), controlRepo: "control", pkgs: packages,
			histories: histories, tags: map[string]gitx.Tags{}}
		if _, _, err := cp.controlCheckpoints(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkComputeComposedSharedHistory(b *testing.B) {
	const consumers = 100
	control := &composedControlGit{fakeGit: newFakeGit()}
	var commits []commit
	for i := 0; i < 500; i++ {
		commits = append(commits, commit{sha: fmt.Sprintf("a%06d", i), message: "feat(provider)^: change"})
	}
	source := counted(newFakeGit(commits...))
	space := &model.Space{Name: "workspace"}
	packages := []*model.Package{{Name: "provider", Dir: "/workspace/source/provider", RepoRoot: "/workspace/source", Repository: "source", Space: space}}
	var dependencies []model.Dependency
	for i := 0; i < consumers; i++ {
		name := fmt.Sprintf("consumer-%03d", i)
		packages = append(packages, &model.Package{Name: name, Dir: "/workspace/source/" + name,
			RepoRoot: "/workspace/source", Repository: "source", Space: space})
		dependencies = append(dependencies, model.Dependency{Consumer: name, Provider: "provider"})
	}
	options := Options{Packages: packages, Dependencies: dependencies, Repositories: map[string]RepositoryHistory{
		"control": {Name: "control", Root: "/workspace", Git: control, Control: true},
		"source":  {Name: "source", Root: "/workspace/source", Path: "source", Git: source},
	}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compute(context.Background(), control, options); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReleaseRepositoryInputs(b *testing.B) {
	const (
		packageCount    = 1024
		repositoryCount = 32
		groupSize       = 16
	)
	cp := &computation{
		order:           make([]string, 0, packageCount),
		byName:          make(map[string]*model.Package, packageCount),
		providers:       make(map[string][]string, packageCount),
		repositoryReach: make(map[string][]string, packageCount),
		controlInputs:   make(map[string]bool),
		histories:       make(map[string]RepositoryHistory, repositoryCount),
		controlRepo:     "control",
	}
	for repositoryIndex := range repositoryCount {
		name := fmt.Sprintf("source-%02d", repositoryIndex)
		if repositoryIndex == 0 {
			name = "control"
		}
		cp.histories[name] = RepositoryHistory{Name: name}
	}
	for packageIndex := range packageCount {
		name := fmt.Sprintf("package-%04d", packageIndex)
		repository := fmt.Sprintf("source-%02d", 1+packageIndex%(repositoryCount-1))
		space := &model.Space{Name: fmt.Sprintf("group-%03d", packageIndex/groupSize),
			GroupIdentity: fmt.Sprintf("central/group-%03d", packageIndex/groupSize), Versioning: model.VersioningFixed}
		cp.order = append(cp.order, name)
		cp.byName[name] = &model.Package{Name: name, Repository: repository, Space: space}
		cp.repositoryReach[name] = []string{repository}
		if packageIndex > 0 {
			cp.providers[name] = []string{cp.order[(packageIndex-1)/2]}
		}
		if packageIndex%31 == 0 {
			cp.controlInputs[name] = true
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, got := cp.releaseRepositoryInputs(); len(got) != packageCount {
			b.Fatalf("got %d package inputs", len(got))
		}
	}
}
