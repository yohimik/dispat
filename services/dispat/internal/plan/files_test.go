// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// eagerFilesGit is the real Git implementation reading every commit's changed
// files with the commit, as the history read did before paths were deferred:
// it hides gitx.ChangedFilesx and fills Files in itself. Every other
// capability the planner probes for is forwarded, so a plan over it differs
// from one over the real implementation in when the paths are read and in
// nothing else.
type eagerFilesGit struct{ inner *gitx.LocalGitx }

func (g eagerFilesGit) withFiles(ctx context.Context, commits []gitx.Commit, err error) ([]gitx.Commit, error) {
	if err != nil {
		return nil, err
	}
	shas := make([]string, len(commits))
	for i, c := range commits {
		shas[i] = c.SHA
	}
	files, err := g.inner.ChangedFiles(ctx, shas)
	if err != nil {
		return nil, err
	}
	for i := range commits {
		commits[i].Files, commits[i].AreFilesDeferred = files[commits[i].SHA], false
	}
	return commits, nil
}

func (g eagerFilesGit) Commits(ctx context.Context, since string) ([]gitx.Commit, error) {
	commits, err := g.inner.Commits(ctx, since)
	return g.withFiles(ctx, commits, err)
}
func (g eagerFilesGit) CommitsSinceAny(ctx context.Context, boundaries []string) ([]gitx.Commit, error) {
	commits, err := g.inner.CommitsSinceAny(ctx, boundaries)
	return g.withFiles(ctx, commits, err)
}
func (g eagerFilesGit) Tags(ctx context.Context, pkg string, f gitx.TagFormat) (gitx.Tags, error) {
	return g.inner.Tags(ctx, pkg, f)
}
func (g eagerFilesGit) TagsForPackages(ctx context.Context, f map[string]gitx.TagFormat) (map[string]gitx.Tags, error) {
	return g.inner.TagsForPackages(ctx, f)
}
func (g eagerFilesGit) CreateTag(ctx context.Context, name, message, target string) error {
	return g.inner.CreateTag(ctx, name, message, target)
}
func (g eagerFilesGit) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	return g.inner.IsAncestor(ctx, a, b)
}
func (g eagerFilesGit) IsShallow(ctx context.Context) (bool, error) { return g.inner.IsShallow(ctx) }
func (g eagerFilesGit) HeadSHA(ctx context.Context) (string, error) { return g.inner.HeadSHA(ctx) }
func (g eagerFilesGit) ResolveCommit(ctx context.Context, rev string) (string, error) {
	return g.inner.ResolveCommit(ctx, rev)
}

// TestDeferredFilesPlanExactlyAsFilesReadWithTheHistory is the byte-identity
// claim for reading changed files only where a scope derives from them: over
// histories with merges, scopeless units and exclusions, the plan computed
// with the paths read for the commits that need them equals the plan computed
// with every commit's paths read with the history, in every release, every
// diagnostic and their order, for a single history and a composed one. And it
// diffs fewer commits to get there.
func TestDeferredFilesPlanExactlyAsFilesReadWithTheHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real repositories")
	}
	ctx := context.Background()
	fewer := 0
	for seed := int64(1); seed <= 8; seed++ {
		git, pkgs, deps := randomRepository(t, seed)
		for _, composed := range []bool{false, true} {
			opts := Options{Packages: pkgs, Dependencies: deps, Root: git.Dir}
			plan := func(history TagInventoryGitx) (*Plan, uint64) {
				if composed {
					for _, p := range pkgs {
						p.Repository, p.RepoRoot = "source", git.Dir
					}
					opts.Repositories = map[string]RepositoryHistory{
						"source": {Name: "source", Root: git.Dir, Git: history}}
				}
				before := gitx.CommitsDiffed()
				p, err := Compute(ctx, history, opts)
				require.NoError(t, err)
				return p, gitx.CommitsDiffed() - before
			}
			want, eager := plan(eagerFilesGit{inner: git})
			got, deferred := plan(git)
			assert.Equalf(t, want.Order, got.Order, "seed %d composed %v", seed, composed)
			assert.Equalf(t, want.Diagnostics, got.Diagnostics, "seed %d composed %v", seed, composed)
			require.Equalf(t, len(want.Releases), len(got.Releases), "seed %d composed %v", seed, composed)
			for name, w := range want.Releases {
				assert.Equalf(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])),
					"seed %d composed %v: release of %s", seed, composed, name)
			}
			assert.LessOrEqualf(t, deferred, eager, "seed %d composed %v", seed, composed)
			if deferred < eager {
				fewer++
			}
			for _, p := range pkgs {
				p.Repository, p.RepoRoot = "", ""
			}
		}
	}
	assert.Positive(t, fewer, "no seed read fewer paths than the eager read")
}

// deferringGit is the fake history with its paths deferred the way the real
// implementation defers them, recording which commits the planner asked
// about.
type deferringGit struct {
	*fakeGit
	mu    sync.Mutex
	asked [][]string
}

func (g *deferringGit) Commits(ctx context.Context, since string) ([]gitx.Commit, error) {
	commits, err := g.fakeGit.Commits(ctx, since)
	for i := range commits {
		commits[i].Files, commits[i].AreFilesDeferred = nil, true
	}
	return commits, err
}

func (g *deferringGit) ChangedFiles(_ context.Context, commits []string) (map[string][]string, error) {
	g.mu.Lock()
	g.asked = append(g.asked, slices.Clone(commits))
	g.mu.Unlock()
	out := make(map[string][]string, len(commits))
	for _, sha := range commits {
		out[sha] = g.history[g.index(sha)].files
	}
	return out, nil
}

// TestChangedFilesAreReadOnceForTheCommitsThatDeriveAScope: of a window whose
// units name their packages, derive them, exclude from the derived set and
// restrict propagation to it, the paths of exactly the deriving commits are
// read, in one question, and the plan is the one the eager read gives.
func TestChangedFilesAreReadOnceForTheCommitsThatDeriveAScope(t *testing.T) {
	history := []commit{
		{sha: "c1", message: "fix(core): named outright", files: []string{"libs/core/a.go"}},
		{sha: "c2", message: "fix: derived from the files", files: []string{"libs/utils/b.go"}},
		{sha: "c3", message: "fix(-utils): everything the files say but utils", files: []string{"apps/app/c.go", "libs/utils/c.go"}},
		{sha: "c4", message: "feat(core,.): named and derived", files: []string{"apps/app/d.go"}},
		{sha: "c5", message: "feat(core)^: restricted to the derived set\n\nPropagate-Scope: .", files: []string{"apps/app/e.go"}},
		{sha: "c6", message: "not a record at all", files: []string{"libs/core/f.go"}},
		{sha: "c7", message: "chore(release): a non-package scope", files: []string{"libs/core/g.go"}},
	}
	opts := func() Options {
		pkgs, deps := testPackages()
		return Options{Packages: pkgs, Dependencies: deps, Root: "/r", NonPackageScopes: []string{"release"}}
	}
	eager := newFakeGit(history...).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	want, err := Compute(context.Background(), eager, opts())
	require.NoError(t, err)

	deferred := &deferringGit{fakeGit: newFakeGit(history...).tag("core", "1.0.0", "").
		tag("utils", "1.0.0", "").tag("app", "1.0.0", "")}
	got, err := Compute(context.Background(), deferred, opts())
	require.NoError(t, err)

	require.Len(t, deferred.asked, 1, "one question for the whole window")
	assert.ElementsMatch(t, []string{"c2", "c3", "c4", "c5"}, deferred.asked[0])
	assert.Equal(t, want.Diagnostics, got.Diagnostics)
	for name, w := range want.Releases {
		assert.Equal(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])), name)
	}
	assert.True(t, got.Releases["utils"].IsReleasing(), "the derived scope still finds utils")
}

// TestDerivedReadsTheFilesTheForecastMissed: the pre-pass decides which
// commits need their paths, and the resolution must not depend on it being
// right. A record still deferred when its derived set is asked for is read
// there and then.
func TestDerivedReadsTheFilesTheForecastMissed(t *testing.T) {
	git := &deferringGit{fakeGit: newFakeGit(commit{sha: "c1", message: "fix: x", files: []string{"libs/core/a.go"}})}
	pkgs, _ := testPackages()
	cp := &computation{ctx: context.Background(), git: git, pkgs: pkgs, root: "/r"}
	rec := &commitRec{commit: gitx.Commit{SHA: "c1", AreFilesDeferred: true}, key: "c1"}

	assert.Equal(t, map[string]bool{"core": true}, cp.derived(rec))
	assert.Equal(t, [][]string{{"c1"}}, git.asked)
	assert.False(t, rec.commit.AreFilesDeferred)
	assert.NoError(t, cp.filesErr)
}

// TestAreFilesNeededByForecastsEveryDerivingScope pins the forecast against the
// scope forms of §6.1: only a unit that names its packages outright, in its
// header and in every propagation footer it writes, leaves the files unread.
func TestAreFilesNeededByForecastsEveryDerivingScope(t *testing.T) {
	parser, err := ccme.NewParser(ccme.Config{})
	require.NoError(t, err)
	for message, isNeeded := range map[string]bool{
		"fix(core): named":                                    false,
		"fix(core,utils)^: two named":                         false,
		"fix(core-*): a glob":                                 false,
		"fix(*): everything":                                  false,
		"fix: no scope-set":                                   true,
		"fix(-core): exclusions alone":                        true,
		"fix(core,.): named and derived":                      true,
		"fix(core,-.): named minus derived":                   true,
		"fix(core)^: named\n\nPropagate-Scope: app":           false,
		"fix(core)^: named\n\nPropagate-Scope: .":             true,
		"fix(core)^: named\n\nPropagate-Scope: -app":          true,
		"fix(core)%beta: named\n\nPropagate-Channel-Scope: .": true,
	} {
		data, _ := parser.Parse(message)
		require.NotNil(t, data, message)
		require.NotEmpty(t, data.ValidUnits(), message)
		assert.Equal(t, isNeeded, areFilesNeededBy(data.ValidUnits()), message)
	}
}

// TestPackagesChangedSinceReadsDeferredFiles: the `--since` selection resolves
// scopes exactly as planning does, so it reads the deferred paths of the
// commits whose scope derives from them.
func TestPackagesChangedSinceReadsDeferredFiles(t *testing.T) {
	git := &deferringGit{fakeGit: newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "fix: derived", files: []string{"libs/utils/a.go"}},
		commit{sha: "c3", message: "fix(core): named", files: []string{"apps/app/b.go"}},
	)}
	pkgs, deps := testPackages()
	got, err := PackagesChangedSince(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"}, "c1")
	require.NoError(t, err)
	assert.Equal(t, []string{"core", "utils"}, sortedStrings(got))
	assert.Equal(t, [][]string{{"c2"}}, git.asked)
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
