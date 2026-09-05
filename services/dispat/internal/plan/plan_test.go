// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// ---------------------------------------------------------------------------
// a repository, described as history rather than as timestamps
// ---------------------------------------------------------------------------
//
// Catch-up is a property of *windows* (§13.7a), so a fake that cannot express
// "which commits has this package not released?" cannot exercise it. The fake
// below is therefore built from a linear commit list plus per-package tags
// pointing into it; every window is derived from that, exactly as git would.

// commit is one entry of the fake repository's history, oldest first.
type commit struct {
	sha     string
	message string
	files   []string
	// author and email are the commit's git identity. They are blank on most
	// fixtures, which is the same thing a repository with one committer says
	// about attribution, and set where the attribution is the claim.
	author string
	email  string
}

// fakeGit serves a linear history and a set of tags pointing into it.
type fakeGit struct {
	// history is oldest-first, which is how a repository grows; the Git
	// interface hands commits back newest-first, as `git log` does.
	history []commit
	// tags maps a tag name to the SHA it points at.
	tags map[string]string
	// tagsFor maps a package to its tag names, newest release first.
	tagsFor map[string][]string
	// shallow makes IsShallow answer true (the E196 scenario).
	shallow bool
}

func newFakeGit(history ...commit) *fakeGit {
	return &fakeGit{
		history: history,
		tags:    map[string]string{},
		tagsFor: map[string][]string{},
	}
}

// tag records that pkg was released at version v, pointing at commit sha.
// Passing an empty sha means the tag points before all recorded history, which
// is how a package that has released everything is described.
func (f *fakeGit) tag(pkg, version, sha string) *fakeGit {
	name := pkg + "@" + version
	f.tags[name] = sha
	f.tagsFor[pkg] = append(f.tagsFor[pkg], name)
	return f
}

func (f *fakeGit) index(sha string) int {
	for i, c := range f.history {
		if c.sha == sha {
			return i
		}
	}
	return -1
}

// Tags returns the package's tags in the order gitx would: the first entry is
// the newest by creation date, which is what the unparseable-baseline rule
// keys off. Selecting a baseline from them is gitx.Tags' job, not the fake's.
func (f *fakeGit) Tags(_ context.Context, pkg string, _ gitx.TagFormat) (gitx.Tags, error) {
	names := f.tagsFor[pkg]
	out := make(gitx.Tags, 0, len(names))
	// tagsFor records tags oldest-first, as they were declared; gitx sorts by
	// creation date descending.
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		t := gitx.Tag{Name: name, Commit: f.tags[name]}
		raw := name[strings.LastIndexByte(name, '@')+1:]
		if v, err := ccme.ParseVersion(raw); err == nil {
			t.Version, t.Parsed = v, true
		}
		out = append(out, t)
	}
	return out, nil
}

// Commits returns the commits after sinceTag, newest first — the pending
// window of §13.3, computed from history rather than asserted.
func (f *fakeGit) Commits(_ context.Context, sinceTag string) ([]gitx.Commit, error) {
	from := 0
	if sinceTag != "" {
		if sha, ok := f.tags[sinceTag]; ok && sha != "" {
			from = f.index(sha) + 1
		}
	}
	var out []gitx.Commit
	for i := len(f.history) - 1; i >= from; i-- {
		c := f.history[i]
		gc := gitx.Commit{SHA: c.sha, Message: c.message, Files: c.files,
			AuthorName: c.author, AuthorEmail: c.email}
		if i > 0 {
			gc.Parents = []string{f.history[i-1].sha}
		}
		out = append(out, gc)
	}
	return out, nil
}

func (f *fakeGit) IsAncestor(_ context.Context, a, b string) (bool, error) {
	ia, ib := f.index(a), f.index(b)
	if ia < 0 || ib < 0 {
		return false, nil
	}
	return ia <= ib, nil
}

// ResolveCommit answers the commitResolver capability the corrections pass
// looks for, the way `git rev-parse` does: an abbreviation resolves to the one
// commit carrying it, and an ambiguous one resolves to nothing.
func (f *fakeGit) ResolveCommit(_ context.Context, rev string) (string, error) {
	var found string
	for _, c := range f.history {
		if !strings.HasPrefix(c.sha, rev) {
			continue
		}
		if found != "" {
			return "", errors.New("ambiguous argument: " + rev)
		}
		found = c.sha
	}
	if found == "" {
		return "", errors.New("unknown revision: " + rev)
	}
	return found, nil
}

func (f *fakeGit) CreateTag(context.Context, string, string, string) error { return nil }

func (f *fakeGit) IsShallow(context.Context) (bool, error) { return f.shallow, nil }

// countingGit records how many git queries planning makes per package. The
// planner fetches tags concurrently, so the counters take a lock.
type countingGit struct {
	*fakeGit
	mu         sync.Mutex
	tagQueries map[string]int
	logQueries map[string]int
}

type bulkCountingGit struct {
	*countingGit
	bulkQueries int
	bulkErr     error
}

func (b *bulkCountingGit) TagsForPackages(ctx context.Context, formats map[string]gitx.TagFormat) (map[string]gitx.Tags, error) {
	b.bulkQueries++
	if b.bulkErr != nil {
		return nil, b.bulkErr
	}
	out := make(map[string]gitx.Tags, len(formats))
	for name, format := range formats {
		out[name], _ = b.fakeGit.Tags(ctx, name, format)
	}
	return out, nil
}

func counted(f *fakeGit) *countingGit {
	return &countingGit{fakeGit: f, tagQueries: map[string]int{}, logQueries: map[string]int{}}
}

func (c *countingGit) Tags(ctx context.Context, pkg string, format gitx.TagFormat) (gitx.Tags, error) {
	c.mu.Lock()
	c.tagQueries[pkg]++
	c.mu.Unlock()
	return c.fakeGit.Tags(ctx, pkg, format)
}

func (c *countingGit) Commits(ctx context.Context, sinceTag string) ([]gitx.Commit, error) {
	c.mu.Lock()
	c.logQueries[sinceTag]++
	c.mu.Unlock()
	return c.fakeGit.Commits(ctx, sinceTag)
}

func TestPlanningQueriesGitOncePerPackage(t *testing.T) {
	// The cost bound dispat claims: one bounded tag query and one bounded log
	// query per package, never a full-history walk. A package needs *two*
	// baselines — the newest tag and the newest stable one — and asking for
	// them separately runs the same `git tag` twice, which is the easy way to
	// double the tag work for an answer that comes from identical output.
	git := counted(newFakeGit(
		commit{sha: "c1", message: "feat(core)^^%beta++*: streaming"},
		commit{sha: "c2", message: "fix(utils)^: helpers"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c1").
		tag("utils", "1.0.0", "").tag("app", "1.0.0", ""))

	pkgs, deps := testPackages()
	_, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r",
	})
	require.NoError(t, err)

	for _, name := range []string{"core", "utils", "app"} {
		assert.Equal(t, 1, git.tagQueries[name], "%s: one tag query", name)
	}
	// Both propagation phases and both baselines read the same history walk:
	// one log range per distinct window origin.
	for since, n := range git.logQueries {
		assert.Equal(t, 1, n, "log range %q must be walked once", since)
	}
}

func TestPlanningUsesBulkTagInventoryWhenAvailable(t *testing.T) {
	base := counted(newFakeGit(
		commit{sha: "c1", message: "feat(core): streaming"},
	).tag("core", "1.2.3", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", ""))
	git := &bulkCountingGit{countingGit: base}
	pkgs, deps := testPackages()
	_, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)
	assert.Equal(t, 1, git.bulkQueries)
	assert.Empty(t, git.tagQueries, "bulk inventory must replace per-package tag calls")
}

func TestPlanningSharesHistoryForDifferentTagsAtTheSameCommit(t *testing.T) {
	git := counted(newFakeGit(
		commit{sha: "c1", message: "chore: shared release"},
		commit{sha: "c2", message: "fix(a,b): pending"},
	).tag("a", "1.0.0", "c1").tag("b", "2.0.0", "c1"))
	pkgs := []*model.Package{{Name: "a", Dir: "/r/a"}, {Name: "b", Dir: "/r/b"}}

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	assertVersion(t, v(1, 0, 1), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 1), p.Releases["b"].Next)
	assert.Equal(t, 1, git.logQueries["a@1.0.0"]+git.logQueries["b@2.0.0"],
		"different tag names at one peeled commit share a history query")

	cache := make(map[string]map[string]bool)
	first := sharedCommitWindow(cache, commitWindowCacheKey("c1", "a@1.0.0"), []gitx.Commit{{SHA: "c2"}})
	second := sharedCommitWindow(cache, commitWindowCacheKey("c1", "b@2.0.0"), []gitx.Commit{{SHA: "ignored"}})
	assert.Equal(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(second).Pointer(),
		"the same peeled boundary shares immutable membership storage")
	assert.False(t, second["ignored"])
}

func TestPlanningDoesNotShareHistoryForDistinctStableCommits(t *testing.T) {
	git := counted(newFakeGit(
		commit{sha: "c1", message: "chore: a release"},
		commit{sha: "c2", message: "chore: b release"},
		commit{sha: "c3", message: "fix(a,b): pending"},
	).tag("a", "1.0.0", "c1").tag("b", "1.0.0", "c2"))
	pkgs := []*model.Package{{Name: "a", Dir: "/r/a"}, {Name: "b", Dir: "/r/b"}}

	_, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	assert.Equal(t, 1, git.logQueries["a@1.0.0"])
	assert.Equal(t, 1, git.logQueries["b@1.0.0"])
}

func TestPlanningDoesNotConflateUnknownStableCommitIDs(t *testing.T) {
	git := counted(newFakeGit(
		commit{sha: "c1", message: "fix(a,b): pending"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", ""))
	pkgs := []*model.Package{{Name: "a", Dir: "/r/a"}, {Name: "b", Dir: "/r/b"}}

	_, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	assert.Equal(t, 1, git.logQueries["a@1.0.0"])
	assert.Equal(t, 1, git.logQueries["b@1.0.0"],
		"unknown peeled IDs fall back to distinct tag identities")
}

func TestPlanningReturnsBulkTagInventoryError(t *testing.T) {
	want := context.Canceled
	git := &bulkCountingGit{countingGit: counted(newFakeGit()), bulkErr: want}
	pkgs, deps := testPackages()
	_, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.ErrorIs(t, err, want)
	assert.Contains(t, err.Error(), "loading tags")
}

func TestCommitWindowsShareStorageOnlyForTheSameBaseline(t *testing.T) {
	commits := []gitx.Commit{{SHA: "one"}, {SHA: "two"}}
	cache := make(map[string]map[string]bool)

	first := sharedCommitWindow(cache, "v1", commits)
	same := sharedCommitWindow(cache, "v1", append(commits, gitx.Commit{SHA: "ignored"}))
	other := sharedCommitWindow(cache, "v2", commits)

	require.Equal(t, first, same)
	assert.Equal(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(same).Pointer(),
		"packages sharing a baseline must share one immutable commit set")
	assert.NotEqual(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(other).Pointer(),
		"distinct baselines must retain distinct membership sets")
	assert.False(t, same["ignored"], "a cached baseline cannot be rebuilt from another package's input")
}

func TestSharedCommitWindowsKeepDistinctBaselineMembership(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a,b,c,d): initial release"},
		commit{sha: "c2", message: "feat(a,b,c,d): newer baseline"},
		commit{sha: "c3", message: "fix(a,b,c,d): pending work"},
	).tag("a", "1.0.0", "c1").tag("b", "1.0.0", "c1").
		tag("c", "1.0.0", "c2").tag("d", "1.0.0", "c2")
	pkgs := make([]*model.Package, 0, 4)
	for _, name := range []string{"a", "b", "c", "d"} {
		pkgs = append(pkgs, &model.Package{Name: name, Dir: "/r/" + name})
	}

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	assertVersion(t, v(1, 1, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["b"].Next)
	assertVersion(t, v(1, 0, 1), p.Releases["c"].Next)
	assertVersion(t, v(1, 0, 1), p.Releases["d"].Next)
}

// testPackages is the standard three-package workspace: two libraries and an
// app consuming both.
func testPackages() ([]*model.Package, []model.Dependency) {
	libs := &model.Space{Name: "libs", BuildWaitsPublish: true}
	apps := &model.Space{Name: "apps"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/libs/core", Space: libs},
		{Name: "utils", Dir: "/r/libs/utils", Space: libs},
		{Name: "app", Dir: "/r/apps/app", Space: apps},
	}
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core"},
		{Consumer: "app", Provider: "utils"},
	}
	return pkgs, deps
}

func compute(t *testing.T, git gitx.Git, initials map[string]ccme.Version) *Plan {
	t.Helper()
	pkgs, deps := testPackages()
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Initials: initials, Root: "/r"})
	require.NoError(t, err)
	return p
}

func v(major, minor, patch uint64) ccme.Version {
	return ccme.Version{Major: major, Minor: minor, Patch: patch}
}

func pre(major, minor, patch uint64, channel string, counter string) ccme.Version {
	return ccme.Version{Major: major, Minor: minor, Patch: patch, Prerelease: []string{channel, counter}}
}

// assertVersion compares versions by precedence rather than by struct
// equality. ccme.Version carries a Raw field recording the text it was parsed
// from, so a version read out of a tag and the identical version computed by a
// bump are not deeply equal — and Raw takes no part in ordering or rendering.
func assertVersion(t *testing.T, want, got ccme.Version, msgAndArgs ...any) {
	t.Helper()
	assert.Equal(t, want.String(), got.String(), msgAndArgs...)
}

// codes lists the diagnostic codes a plan raised, for assertions that care
// which rule fired rather than what it said.
func codes(p *Plan) []string {
	out := make([]string, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

func hasCode(p *Plan, code string) bool {
	for _, d := range p.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// §8.3 — propagation is opt-in
// ---------------------------------------------------------------------------

func TestPropagationRequiresADepth(t *testing.T) {
	// propagation.depth defaults to 0 (§14), so a plain feat reaches nobody.
	// This is the single most consequential default in the spec and the one an
	// implementation is most likely to get wrong by inheriting older habits.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): add streaming"},
	).tag("core", "1.2.3", "").tag("app", "0.1.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump)
	assert.False(t, p.Releases["app"].Changed(), "a caret-less feat must not reach consumers")
}

func TestPropagationWithCaret(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: add streaming"},
	).tag("core", "1.2.3", "").tag("app", "0.1.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, ccme.BumpMinor, core.Bump)
	assertVersion(t, v(1, 3, 0), core.Next, "core -> 1.3.0")

	app := p.Releases["app"]
	assert.Equal(t, ccme.BumpPatch, app.Bump, "the default propagated bump is patch")
	assert.Equal(t, ccme.BumpNone, app.OwnBump)
	assertVersion(t, v(0, 1, 1), app.Next, "app -> 0.1.1")
	assert.Equal(t, []string{"core"}, app.DueTo)

	// Providers come before consumers: this is also the publish order (§19.2).
	pos := map[string]int{}
	for i, n := range p.Order {
		pos[n] = i
	}
	assert.Less(t, pos["core"], pos["app"])
	assert.Less(t, pos["utils"], pos["app"])
}

func TestPropagationDepthIsShortestPath(t *testing.T) {
	// "+1" reaches direct consumers only; "^^" reaches the closure.
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/a", Space: &model.Space{Name: "s"}},
		{Name: "b", Dir: "/r/b", Space: &model.Space{Name: "s"}},
		{Name: "c", Dir: "/r/c", Space: &model.Space{Name: "s"}},
	}
	deps := []model.Dependency{
		{Consumer: "b", Provider: "a"},
		{Consumer: "c", Provider: "b"},
	}
	for _, tc := range []struct {
		header string
		reachC bool
	}{
		{"feat(a)^: x", false},
		{"feat(a)+2: x", true},
		{"feat(a)^^: x", true},
	} {
		git := newFakeGit(commit{sha: "c1", message: tc.header}).
			tag("a", "1.0.0", "").tag("b", "1.0.0", "").tag("c", "1.0.0", "")
		p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Initials: nil, Root: "/r"})
		require.NoError(t, err, tc.header)

		assert.True(t, p.Releases["b"].Changed(), "%s: b is a direct consumer", tc.header)
		assert.Equal(t, tc.reachC, p.Releases["c"].Changed(), "%s: c at depth 2", tc.header)
	}
}

func TestPropagateNoneReachesNobody(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^none: internal only"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, p.Releases["core"].Changed())
	assert.False(t, p.Releases["app"].Changed(), "^none propagates nothing")
}

func TestPropagateScopeRestrictsTargets(t *testing.T) {
	git := newFakeGit(commit{
		sha:     "c1",
		message: "feat(core)^^: new plugin API\n\nPropagate-Scope: utils\n",
	}).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	pkgs, _ := testPackages()
	// core -> utils -> app, so both are reachable at "^^".
	deps := []model.Dependency{
		{Consumer: "utils", Provider: "core"},
		{Consumer: "app", Provider: "utils"},
	}
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Initials: nil, Root: "/r"})
	require.NoError(t, err)

	assert.True(t, p.Releases["utils"].Changed(), "utils is inside Propagate-Scope")
	assert.False(t, p.Releases["app"].Changed(), "app is outside Propagate-Scope")
}

func TestDevDependencyEdgesDoNotPropagate(t *testing.T) {
	// §8.4: devDependencies is not in the default kind set. A package that
	// uses another only for its test suite does not need republishing when
	// that other changes, and including such edges would make almost every
	// workspace one strongly-connected blob.
	pkgs, _ := testPackages()
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core", Kind: model.KindDevDependencies},
		{Consumer: "app", Provider: "utils", Kind: model.KindPeerDependencies},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming"},
		commit{sha: "c2", message: "feat(utils)^: helpers"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r",
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"utils"}, p.Releases["app"].DueTo,
		"peerDependencies propagate, devDependencies do not")
}

func TestPropagateScopeExcludingEverythingWarns(t *testing.T) {
	git := newFakeGit(commit{
		sha:     "c1",
		message: "feat(core)^: new API\n\nPropagate-Scope: utils\n",
	}).tag("core", "1.0.0", "").tag("app", "1.0.0", "").tag("utils", "1.0.0", "")

	// core -> app only, so restricting to utils reaches nobody.
	pkgs, _ := testPackages()
	deps := []model.Dependency{{Consumer: "app", Provider: "core"}}
	p, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r",
	})
	require.NoError(t, err)

	assert.True(t, hasCode(p, CodeScopeExcludedAll), "W135, got %v", codes(p))
	assert.False(t, p.Releases["app"].Changed())
}

func TestGlobMatchingNothingWarns(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(nothing-*): typo in a glob"},
	).tag("core", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodeEmptyGlob), "W134, got %v", codes(p))
	assert.False(t, p.HasErrors(), "a glob that matches nothing is not the E130 typo")
}

// TestScopesMatchAPackageWhicheverCase: a scope is typed into a commit message
// and a package name comes from a folder, so the two are matched
// case-insensitively, the way every selector in dispat matches a name. Both a
// plain name and a glob fold, and the package is addressed under its own
// spelling, so nothing downstream sees the commit's.
func TestScopesMatchAPackageWhicheverCase(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(Core): the header spells it another way"},
		commit{sha: "c2", message: "fix(UTIL*): and so does a glob"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.False(t, p.HasErrors(), "a folded match is a match, not the E130 typo: %v", codes(p))
	assert.True(t, p.Releases["core"].Changed(), "the exact term addressed core")
	assert.True(t, p.Releases["utils"].Changed(), "and the glob addressed utils")
}

func TestGlobalIsAnOrdinaryScopeName(t *testing.T) {
	// "global" is an ordinary scope name like any other, not an alias of "*",
	// so writing it where no package carries it is exactly the E130 typo the
	// unknown-include error exists to catch — and "*" stays the one way to
	// address the whole workspace.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(global): no longer workspace-wide"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodeUnknownInclude), "E130, got %v", codes(p))
	for _, name := range []string{"core", "utils", "app"} {
		assert.False(t, p.Releases[name].Changed(), "%s must not be addressed by 'global'", name)
	}

	git = newFakeGit(
		commit{sha: "c1", message: "feat(*): workspace-wide"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	p = compute(t, git, nil)
	for _, name := range []string{"core", "utils", "app"} {
		assert.True(t, p.Releases[name].Changed(), "%s must be addressed by '*'", name)
	}
}

func TestConflictingDirectChannelsPickTheNewest(t *testing.T) {
	// §11.6: the newest commit wins, and W186 reports both. Determinism is
	// chosen over rejecting the commit, because a channel conflict is usually
	// the result of a merge.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta: one line"},
		commit{sha: "c2", message: "release(core)%rc: another"},
	).tag("core", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, "rc", p.Releases["core"].Channel, "the newer directive wins")
	assert.True(t, hasCode(p, CodeChannelConflict), "W186, got %v", codes(p))
}

func TestOwnBumpWins(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core)^: edge case"},
		commit{sha: "c2", message: "feat(app): new screen"},
	).tag("core", "1.0.0", "").tag("app", "2.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.Equal(t, ccme.BumpMinor, app.Bump, "own feat wins over propagated patch")
	assertVersion(t, v(2, 1, 0), app.Next, "app -> 2.1.0")
}

func TestSinglePatchForMultipleProviders(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core)^: a"},
		commit{sha: "c2", message: "fix(utils)^: b"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	// Bumps merge by max(), so two changed providers still produce one patch.
	assertVersion(t, v(1, 0, 1), app.Next, "app -> 1.0.1")
	assert.ElementsMatch(t, []string{"core", "utils"}, app.DueTo)
}

func TestMultiScopeUnitAttributesEveryProvider(t *testing.T) {
	// One unit, several source packages: §9.2 attributes the whole source set
	// to every dependent it reaches (prov[d] |= sources), not the package the
	// traversal happened to arrive from. The walk visits app once, and before
	// the fix the arrival's origin was the only provider recorded, so a
	// consumer of both packages was told it releases because of one of them.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core, utils)^: one change across both"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assertVersion(t, v(1, 0, 1), app.Next, "app -> 1.0.1")
	assert.ElementsMatch(t, []string{"core", "utils"}, app.DueTo,
		"every source package of the unit forced the bump")
	assert.Equal(t, "propagated from core, utils", app.Reason())

	providers := map[string]bool{}
	for _, s := range app.Sources {
		providers[s.Provider] = true
		assert.Equal(t, "c1", s.Commit)
		assert.Equal(t, 1, s.Level)
	}
	assert.Len(t, providers, 2, "one contribution per source package: %v", app.Sources)
}

func TestMultiScopeSourceOutsideTheManifestsStaysOutOfUpdates(t *testing.T) {
	// §9.2 attributes the unit's whole source set, whether or not the target
	// consumes each member: DueTo answers why the package releases, and one
	// change written across two packages is one cause with two names. The
	// dependencies record keeps speaking the target's own manifest language,
	// so the source it never consumes stays out of Updates.
	libs := &model.Space{Name: "libs"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/core", Space: libs},
		{Name: "side", Dir: "/r/side", Space: libs},
		{Name: "app", Dir: "/r/app", Space: libs},
	}
	deps := []model.Dependency{{Consumer: "app", Provider: "core"}}
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core, side)^: one change across both"},
	).tag("core", "1.0.0", "").tag("side", "1.0.0", "").tag("app", "1.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)

	app := p.Releases["app"]
	assert.ElementsMatch(t, []string{"core", "side"}, app.DueTo)
	names := make([]string, 0, len(app.Updates))
	for _, u := range app.Updates {
		names = append(names, u.Name)
	}
	assert.Equal(t, []string{"core"}, names,
		"the record names only the provider the manifests mention")
}

func TestMultiScopeReleasingSourceExplainsTheRelease(t *testing.T) {
	// One source of the unit already shipped the commit, the other releases
	// beside the consumer in this run. The releasing source is what explains
	// the consumer's presence in the plan, so this is an ordinary propagated
	// release. Before the whole set was attributed, the traversal credited
	// only the source it arrived from; when that happened to be the shipped
	// one, the plan called an explained release a catch-up.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core, utils)^: one change across both"},
	).tag("core", "1.0.1", "c1").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.ElementsMatch(t, []string{"core", "utils"}, app.DueTo)
	assert.False(t, app.CatchUp, "a releasing source explains the release")
	assert.NotContains(t, codes(p), CodeCatchUp, "got %v", codes(p))
	names := make([]string, 0, len(app.Updates))
	for _, u := range app.Updates {
		names = append(names, u.Name)
	}
	assert.ElementsMatch(t, []string{"core", "utils"}, names,
		"the shipped source's movement reaches the record through the attribution")
}

func TestBreakingChange(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)!: drop old API"},
		commit{sha: "c2", message: "feat(app): support new core API"},
	).tag("core", "1.5.2", "c1")

	p := compute(t, git, nil)

	// core released c1 already, so only app's window still holds it.
	assertVersion(t, v(1, 5, 2), p.Releases["core"].Next, "core has nothing pending")

	app := p.Releases["app"]
	assert.Equal(t, ccme.BumpMinor, app.Bump)
	assertVersion(t, v(0, 1, 0), app.Next, "first release -> 0.1.0")
}

// ---------------------------------------------------------------------------
// §6 — scope resolution
// ---------------------------------------------------------------------------

func TestDerivedScopeFromChangedFiles(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix: repair the loader", files: []string{"libs/core/loader.go"}},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, ccme.BumpPatch, p.Releases["core"].OwnBump, "the file names core")
	assert.False(t, p.Releases["utils"].Changed(), "utils owns no changed file")
}

func TestDerivedScopeLongestPrefixWins(t *testing.T) {
	// A package nested inside another owns its own files (§6.2).
	outer := &model.Space{Name: "s"}
	pkgs := []*model.Package{
		{Name: "ui", Dir: "/r/packages/ui", Space: outer},
		{Name: "theme", Dir: "/r/packages/ui/theme", Space: outer},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "fix: dark mode", files: []string{"packages/ui/theme/dark.ts"}},
	).tag("ui", "1.0.0", "").tag("theme", "1.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: nil, Initials: nil, Root: "/r"})
	require.NoError(t, err)

	assert.True(t, p.Releases["theme"].Changed(), "the inner package owns the file")
	assert.False(t, p.Releases["ui"].Changed(), "the outer package does not")
}

func TestScopeGlobAndExclusion(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(*,-app): workspace-wide"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, p.Releases["core"].Changed())
	assert.True(t, p.Releases["utils"].Changed())
	assert.False(t, p.Releases["app"].Changed(), "an exclusion always wins")
}

func TestUnknownIncludeIsAnError(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(nosuch): typo"},
	).tag("core", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeUnknownInclude), "E130 for an unknown include, got %v", codes(p))
	assert.True(t, p.HasErrors(), "a typo must not silently drop a release")
}

func TestNonPackageScopeIsExempt(t *testing.T) {
	// dispat's own release commit is scoped "release", which is not a package.
	// Without the exemption every run would leave an E130 behind for the next
	// run to trip over — a tool that poisons its own repository.
	pkgs, deps := testPackages()
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): streaming"},
		commit{sha: "c2", message: "chore(release): core@1.3.0"},
	).tag("core", "1.2.3", "")

	p, err := Compute(context.Background(), git, Options{
		Packages:         pkgs,
		Dependencies:     deps,
		Root:             "/r",
		NonPackageScopes: []string{"release"},
	})
	require.NoError(t, err)

	assert.False(t, hasCode(p, CodeUnknownInclude), "no E130, got %v", codes(p))
	assert.False(t, hasCode(p, CodeInertUnit), "nor W131: resolving to nothing is the point")
	assert.False(t, p.HasErrors())
	assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump, "the real commit still counts")
}

func TestNonPackageScopeMustBeDeclared(t *testing.T) {
	// Undeclared, the same commit is an ordinary typo.
	pkgs, deps := testPackages()
	git := newFakeGit(
		commit{sha: "c1", message: "chore(release): core@1.3.0"},
	).tag("core", "1.2.3", "")

	p, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r",
	})
	require.NoError(t, err)
	assert.True(t, hasCode(p, CodeUnknownInclude), "E130, got %v", codes(p))
}

func TestUnknownExcludeIsAWarning(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core,-deleted): fine"},
	).tag("core", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeUnknownScope), "W130, got %v", codes(p))
	assert.False(t, p.HasErrors(), "excluding something already deleted is harmless")
	assert.True(t, p.Releases["core"].Changed())
}

// ---------------------------------------------------------------------------
// §12 — baselines and initials
// ---------------------------------------------------------------------------

func TestInitialsForUntaggedPackage(t *testing.T) {
	git := newFakeGit(commit{sha: "c1", message: "feat(core): first feature"})
	p := compute(t, git, map[string]ccme.Version{"core": v(1, 2, 3)})

	core := p.Releases["core"]
	assert.True(t, core.FromInitials)
	assert.False(t, core.Tagged)
	assertVersion(t, v(1, 2, 3), core.Current, "baseline from initials")
	assertVersion(t, v(1, 3, 0), core.Next, "feat bumps on top of the baseline")
}

func TestInitialsForUnparseableTag(t *testing.T) {
	// The newest tag core@0.0.1.0 is unparseable; the pre-last tag must
	// NOT be used — the baseline comes from initials while commits are still
	// scanned from the unparseable tag, so released work is not counted twice.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): older, must not count"},
		commit{sha: "c2", message: "fix(core): repair"},
	)
	git.tags["core@0.0.1.0"] = "c1"
	git.tagsFor["core"] = []string{"core@0.0.1.0"}

	p := compute(t, git, map[string]ccme.Version{"core": v(1, 0, 0)})

	core := p.Releases["core"]
	assert.True(t, core.FromInitials)
	assertVersion(t, v(1, 0, 0), core.Current)
	assert.Equal(t, ccme.BumpPatch, core.OwnBump, "only commits since the unparseable tag count")
	assertVersion(t, v(1, 0, 1), core.Next, "1.0.0 + fix -> 1.0.1")
}

func TestUnparseableTagWithoutInitials(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): older"},
		commit{sha: "c2", message: "fix(core): repair"},
	)
	git.tags["core@0.0.1.0"] = "c1"
	git.tagsFor["core"] = []string{"core@0.0.1.0"}

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.False(t, core.FromInitials)
	assertVersion(t, ccme.Version{}, core.Current, "no initials entry: default 0.0.0")
	assertVersion(t, v(0, 0, 1), core.Next)
}

func TestInitialsIgnoredWhenTagged(t *testing.T) {
	git := newFakeGit(commit{sha: "c1", message: "fix(core): x"}).tag("core", "2.0.0", "")
	p := compute(t, git, map[string]ccme.Version{"core": v(9, 0, 0)})

	core := p.Releases["core"]
	assert.False(t, core.FromInitials, "a parseable tag beats initials")
	assertVersion(t, v(2, 0, 1), core.Next)
}

// ---------------------------------------------------------------------------
// §13.7a — catch-up (Appendix B.7)
// ---------------------------------------------------------------------------

func TestCatchUpAfterConsumerFailure(t *testing.T) {
	// The orphaned-consumer scenario. In a previous run core published 2.0.0
	// at c1 and was tagged; app's publish failed, so app is still tagged at
	// its older commit. This run has no new commits at all — and app must
	// still be scheduled for the release it missed.
	//
	// This is the exact failure §13.7a exists to prevent: under the "keep a
	// unit only while its own package has it pending" rule, c1 has left
	// core's window, the unit would have no source packages, and app would be
	// orphaned silently and for ever.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: setup"},
		commit{sha: "c1", message: "feat(core)^!: drop old API"},
	).tag("core", "2.0.0", "c1").tag("app", "1.0.0", "c0")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["core"].Changed(), "core has already released c1")

	app := p.Releases["app"]
	assert.True(t, app.Changed(), "app must catch up: c1 is still in its window")
	assert.Equal(t, ccme.BumpPatch, app.Bump)
	assertVersion(t, v(1, 0, 1), app.Next)
	assert.Equal(t, []string{"core"}, app.DueTo)

	// W193 is non-suppressible and MUST carry the origin's *published*
	// version, so a reviewer can see the plan is discharging earlier work.
	assert.True(t, app.CatchUp, "the release must be labelled a catch-up")
	require.True(t, hasCode(p, CodeCatchUp), "W193, got %v", codes(p))
	var msg string
	for _, d := range p.Diagnostics {
		if d.Code == CodeCatchUp {
			msg = d.Message
		}
	}
	assert.Contains(t, msg, "core@2.0.0", "W193 must name the origin's published version")
}

func TestNoCatchUpWhenConsumerIsFresh(t *testing.T) {
	// Both packages released at c1, the normal outcome of a successful run.
	// Nothing is pending for either, whatever order the tags were created in.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: setup"},
		commit{sha: "c1", message: "feat(core)^: streaming"},
	).tag("core", "2.0.0", "c1").tag("app", "1.0.1", "c1")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["core"].Changed())
	assert.False(t, p.Releases["app"].Changed(), "a fresh consumer must not release")
}

func TestCatchUpForNeverReleasedConsumer(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming"},
	).tag("core", "0.1.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Changed(), "a never-released consumer still has the commit pending")
	assertVersion(t, v(0, 0, 1), app.Next, "0.0.0 + patch")
	assert.Equal(t, []string{"core"}, app.DueTo)

	assert.False(t, p.Releases["utils"].Changed(), "utils has no providers and stays untouched")
}

// G4 / G6: once discharged, a contribution is not re-admitted, so repeated
// running at a fixed HEAD reaches an empty plan.
func TestCatchUpDischargesExactlyOnce(t *testing.T) {
	history := []commit{
		{sha: "c0", message: "chore: setup"},
		{sha: "c1", message: "feat(core)^: streaming"},
	}
	before := newFakeGit(history...).tag("core", "1.1.0", "c1").tag("app", "1.0.0", "c0")
	assert.True(t, compute(t, before, nil).Releases["app"].Changed(), "owed before")

	// The catch-up run publishes app at c1; the tag advances and the window
	// no longer contains the commit.
	after := newFakeGit(history...).tag("core", "1.1.0", "c1").tag("app", "1.0.1", "c1")
	assert.False(t, compute(t, after, nil).Releases["app"].Changed(),
		"the contribution must not be re-admitted after discharge (G4)")
}

// G5: a later run's target set is a subset of the first run's. A failed
// publish must never enlarge what a commit releases.
func TestCatchUpNeverWidensBlastRadius(t *testing.T) {
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/a", Space: &model.Space{Name: "s"}},
		{Name: "b", Dir: "/r/b", Space: &model.Space{Name: "s"}},
		{Name: "c", Dir: "/r/c", Space: &model.Space{Name: "s"}},
	}
	deps := []model.Dependency{
		{Consumer: "b", Provider: "a"},
		{Consumer: "c", Provider: "b"},
	}
	history := []commit{{sha: "c1", message: "feat(a)^: only direct consumers"}}

	first := newFakeGit(history...).tag("a", "1.0.0", "").tag("b", "1.0.0", "").tag("c", "1.0.0", "")
	p1, err := Compute(context.Background(), first, Options{Packages: pkgs, Dependencies: deps, Initials: nil, Root: "/r"})
	require.NoError(t, err)
	assert.True(t, p1.Releases["b"].Changed())
	assert.False(t, p1.Releases["c"].Changed())

	// a published, b failed. The catch-up run must reach b and still not c:
	// depth is measured from the originating source set, never re-based on
	// the package that happens to be republishing.
	second := newFakeGit(history...).tag("a", "1.1.0", "c1").tag("b", "1.0.0", "").tag("c", "1.0.0", "")
	p2, err := Compute(context.Background(), second, Options{Packages: pkgs, Dependencies: deps, Initials: nil, Root: "/r"})
	require.NoError(t, err)
	assert.True(t, p2.Releases["b"].Changed(), "b still owed")
	assert.False(t, p2.Releases["c"].Changed(), "c must not be dragged in by the catch-up")
}

// §13.7b: the upward walk and the downward one are duals and MUST agree.
func TestStaleSourcesAgreesWithPropagation(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c0", message: "chore: setup"},
		commit{sha: "c1", message: "feat(core)^: streaming"},
	).tag("core", "1.1.0", "c1").tag("app", "1.0.0", "c0")

	p := compute(t, git, nil)

	sources := p.StaleSources("app")
	require.NotEmpty(t, sources, "staleSources is non-empty exactly when a bump was propagated")
	best := ccme.BumpNone
	for _, s := range sources {
		best = ccme.MaxBump(best, s.Bump)
	}
	assert.Equal(t, p.Releases["app"].PropagatedBump, best, "max() over the rows equals the propagated bump")

	// The cheap tag-level screen agrees here, but is only ever a screen.
	assert.True(t, p.PossiblyBehind("app", "core"))
	assert.Empty(t, p.StaleSources("utils"))
}

// ---------------------------------------------------------------------------
// §13.4a / §13.5a — suppression applies only to undischarged work
// ---------------------------------------------------------------------------

func TestCancelDiscardsPendingWork(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)!: half-finished"},
		commit{sha: "c2", message: "cancel(app): drop it"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["app"].Changed(), "the cancel discarded the feat")
}

func TestCancelOnConsumerStopsCatchUp(t *testing.T) {
	// §13.7d: a catch-up is stopped by acting on the *consumer*.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: setup"},
		commit{sha: "c1", message: "feat(core)^: streaming"},
		commit{sha: "c2", message: "cancel(app): we are not shipping this"},
	).tag("core", "1.1.0", "c1").tag("app", "1.0.0", "c0")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["app"].Changed(), "the pending propagated contribution is discarded")
}

func TestCancelOnPublishedProviderIsANoOp(t *testing.T) {
	// §13.4a: once the provider has published, neither cancel nor a hold on it
	// retracts what its consumers are owed. W170 is the signal that the
	// directive addressed the wrong package.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: setup"},
		commit{sha: "c1", message: "feat(core)^: streaming"},
		commit{sha: "c2", message: "cancel(core): too late"},
	).tag("core", "1.1.0", "c1").tag("app", "1.0.0", "c0")

	p := compute(t, git, nil)

	assert.True(t, p.Releases["app"].Changed(), "the consumer is still owed its release")
	assert.True(t, hasCode(p, CodeEmptyCancel), "W170, got %v", codes(p))
}

// ---------------------------------------------------------------------------
// §8.6 — holds and pins
// ---------------------------------------------------------------------------

func TestHoldIsPlannedButNotReleased(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): a screen"},
		commit{sha: "c2", message: "release(app): defer\n\nRelease-As: none\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Held)
	assert.True(t, app.Changed(), "the bump is retained, not discarded")
	assert.False(t, app.Releasing(), "a held package is excluded from the plan")
	assertVersion(t, v(1, 1, 0), app.Next, "W154 must carry the withheld version")
	assert.True(t, hasCode(p, CodeHeldVersion), "W154, got %v", codes(p))
	assert.Equal(t, []string{"app"}, p.Held())
}

func TestHeldProviderDoesNotBumpDependents(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming"},
		commit{sha: "c2", message: "release(core): wait\n\nRelease-As: none\n"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, p.Releases["core"].Held)
	assert.False(t, p.Releases["app"].Changed(),
		"a held package must not bump dependents with work it has not released")
}

func TestReleaseAsHoldThenResume(t *testing.T) {
	// The full ladder of §8.6: work accumulates, a hold withholds it without
	// discarding it, and `auto` releases at the max() of everything that
	// accumulated — catch-up included.
	history := []commit{
		{sha: "c1", message: "fix(app): one"},
		{sha: "c2", message: "release(app): not yet\n\nRelease-As: none\n"},
		{sha: "c3", message: "feat(app): two"},
	}

	held := compute(t, newFakeGit(history...).tag("app", "1.0.0", ""), nil).Releases["app"]
	require.True(t, held.Held)
	assertVersion(t, v(1, 1, 0), held.Next,
		"the withheld version accounts for work that landed after the hold")

	resumed := compute(t, newFakeGit(append(history,
		commit{sha: "c4", message: "release(app): ship it\n\nRelease-As: auto\n"},
	)...).tag("app", "1.0.0", ""), nil).Releases["app"]
	assert.False(t, resumed.Held, "auto clears the hold")
	assert.True(t, resumed.Releasing())
	assertVersion(t, v(1, 1, 0), resumed.Next, "released at the max() of everything accumulated")
}

func TestReleaseAsAutoWithNoHoldWarns(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(app): one"},
		commit{sha: "c2", message: "release(app): resume\n\nRelease-As: auto\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeAutoNoHold), "W158, got %v", codes(p))
	assert.True(t, p.Releases["app"].Releasing())
}

func TestReleaseAsExactPin(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(app): one"},
		commit{sha: "c2", message: "release(app): cut 2.0\n\nRelease-As: 2.0.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Pinned)
	assertVersion(t, v(2, 0, 0), app.Next)
	assert.Equal(t, ccme.BumpPatch, app.Bump, "the pin sets the version, never the bump")
	assert.False(t, p.HasErrors(), "got %v", codes(p))
}

func TestReleaseAsExactPinOntoAPrereleaseLine(t *testing.T) {
	// A pin states the version, and the version states the channel (§11.1).
	git := newFakeGit(
		commit{sha: "c1", message: "release(app): start the rc\n\nRelease-As: 2.0.0-rc.0\n"},
	).tag("app", "1.0.0", "")

	app := compute(t, git, nil).Releases["app"]
	assert.True(t, app.Pinned)
	assertVersion(t, pre(2, 0, 0, "rc", "0"), app.Next)
	assert.Equal(t, "rc", app.Channel)
}

func TestReleaseAsPinNotGreater(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "release(app): backwards\n\nRelease-As: 0.9.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinNotGreater), "E153, got %v", codes(p))
}

func TestReleaseAsPinMajorJump(t *testing.T) {
	// §14.1: enforced with no configuration involved.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(app): one"},
		commit{sha: "c2", message: "release(app): typo\n\nRelease-As: 5.0.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinMajorJump), "E157, got %v", codes(p))
	assert.False(t, p.Fatal(), "a bad footer is an authoring mistake, not a repository failure")
}

func TestReleaseAsPinOnMultiplePackages(t *testing.T) {
	// ccme catches the forms decidable from the message alone — two explicit
	// includes, or a term addressing the whole workspace. This is the case
	// that needs the workspace: a single "." term whose breadth depends
	// entirely on which files the commit touched.
	git := newFakeGit(commit{
		sha:     "c1",
		message: "release(.): cut them\n\nRelease-As: 2.0.0\n",
		files:   []string{"libs/core/a.txt", "libs/utils/b.txt"},
	}).tag("core", "1.0.0", "").tag("utils", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinMultiPackage), "E154, got %v", codes(p))
}

func TestReleaseAsPinOnAWholeWorkspaceScopeIsCaughtByTheParser(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "release(*): cut them all\n\nRelease-As: 2.0.0\n"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinMultiPackage), "E154, got %v", codes(p))
	assert.False(t, p.Releases["core"].Pinned, "the invalid unit contributes nothing")
}

func TestExactPinIsGuarded(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): a screen"},
		commit{sha: "c2", message: "release(app): ship it\n\nRelease-As: 1.0.1\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	// The pin is below what the accumulated units require (1.1.0).
	assert.True(t, hasCode(p, CodePinBelowBump), "E156, got %v", codes(p))
	assert.True(t, p.HasErrors())
}

func TestExactPinMayBeAPrereleaseOfTheRequiredBump(t *testing.T) {
	// Shipping the next minor as an rc first is the ordinary way to use a pin
	// on a train, and E156 measures how large a release is, not how finished
	// it is. Comparing whole versions would rank 1.1.0-rc.0 below the computed
	// 1.1.0 by SemVer precedence, reject the pin, and fall back to releasing
	// the stable 1.1.0 the operator was holding back: the guard misfiring into
	// exactly the release nobody asked for.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): a screen"},
		commit{sha: "c2", message: "release(app): cut an rc first\n\nRelease-As: 1.1.0-rc.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.False(t, hasCode(p, CodePinBelowBump), "no E156, got %v", codes(p))
	assert.False(t, p.HasErrors(), "got %v", codes(p))
	assert.True(t, app.Pinned)
	assertVersion(t, pre(1, 1, 0, "rc", "0"), app.Next)
	assert.Equal(t, "rc", app.Channel, "the version states the channel (§11.1)")
}

func TestExactPinPrereleaseBelowTheRequiredBumpStillFails(t *testing.T) {
	// The other half of the same rule: measuring on cores must not let a
	// prerelease smuggle a breaking change out as an rc of a minor. 1.1.0-rc.0
	// has core 1.1.0, which is below the 2.0.0 the breaking change requires.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)!: new api"},
		commit{sha: "c2", message: "release(app): try to soften it\n\nRelease-As: 1.1.0-rc.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinBelowBump), "E156, got %v", codes(p))
	assert.False(t, p.Releases["app"].Pinned, "the rejected pin contributes nothing")
}

func TestExactPinPrereleaseStillGuardsTheBaseline(t *testing.T) {
	// E153 keeps comparing whole versions, because "does this move forward"
	// is exactly the question SemVer precedence answers: an rc of the version
	// already published ranks below it and moves nothing.
	git := newFakeGit(
		commit{sha: "c1", message: "release(app): go backwards\n\nRelease-As: 1.0.0-rc.1\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodePinNotGreater), "E153, got %v", codes(p))
}

// ---------------------------------------------------------------------------
// §11 / §9.3 — channels, prereleases and graduation (Appendix B.5, B.9)
// ---------------------------------------------------------------------------

func TestEnterAPrereleaseTrain(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
	).tag("core", "1.2.3", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, "beta", core.Channel)
	assert.Equal(t, "stable", core.BaselineChannel)
	assertVersion(t, pre(1, 3, 0, "beta", "0"), core.Next, "1.2.3 + minor on beta -> 1.3.0-beta.0")
	assert.Equal(t, "stable -> beta", core.ChannelTransition())
}

func TestPrereleaseCounterAdvances(t *testing.T) {
	// The window is measured from the last *stable* tag, so the feat that
	// started the train is still pending and the target is unchanged.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
		commit{sha: "c2", message: "fix(core): polish"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assertVersion(t, v(1, 2, 3), core.Current, "the window runs from the stable baseline")
	assert.Equal(t, "beta", core.Channel, "a train stays together with no directive")
	assertVersion(t, pre(1, 3, 0, "beta", "1"), core.Next, "counter advances, target unchanged")
}

func TestBreakingChangeMovesTheWholeTrain(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
		commit{sha: "c2", message: "feat(core)!: drop old API"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assertVersion(t, pre(2, 0, 0, "beta", "0"), core.Next,
		"the target moved, so the counter resets rather than continuing under a stale version")
}

func TestGraduation(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
		commit{sha: "c2", message: "release(core)%stable: promote"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, "stable", core.Channel)
	assertVersion(t, v(1, 3, 0), core.Next, "graduation publishes the train's target with no suffix")
	assert.Equal(t, "beta -> stable", core.ChannelTransition())
}

func TestTransitionIsIdempotent(t *testing.T) {
	// A transition matches against the *baseline* channel, so a package that
	// has already graduated is untouched — which is what makes the same
	// directive correct on the first run and on the fifth.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta>stable: finish the train"},
	).tag("core", "1.3.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, "stable", core.Channel)
	assert.False(t, core.Changed(), "an already-graduated package proposes nothing")
	assert.False(t, hasCode(p, CodeGraduateStable),
		"a stable package does not match a prerelease <from>, so not even W185 arises")
}

func TestCaretAloneDoesNotDragConsumersOntoATrain(t *testing.T) {
	// §11.7 / §9.3a: the caret reaches the consumer and the bump is suppressed,
	// because a stable consumer cannot resolve a beta release. W208 is
	// non-suppressible precisely so this is visible.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^%beta: streaming"},
	).tag("core", "1.2.3", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, "beta", p.Releases["core"].Channel)
	assert.False(t, p.Releases["app"].Changed(), "the consumer is suppressed by §9.3a")
	assert.True(t, hasCode(p, CodeBumpSuppressed), "W208, got %v", codes(p))
}

func TestChannelPropagationCarriesTheTrain(t *testing.T) {
	// "^%beta++1" is the form that has to be written: the consumers enter the
	// train too, so §9.3a admits the bump.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^%beta++1: streaming"},
	).tag("core", "1.2.3", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, "beta", core.Channel)
	assertVersion(t, pre(1, 3, 0, "beta", "0"), core.Next)

	app := p.Releases["app"]
	assert.Equal(t, "beta", app.Channel, "the consumer is carried onto the train")
	assert.Equal(t, ccme.BumpPatch, app.Bump)
	assertVersion(t, pre(1, 0, 1, "beta", "0"), app.Next, "1.0.0 + patch on beta")
	assert.False(t, hasCode(p, CodeBumpSuppressed), "nothing is suppressed, got %v", codes(p))
}

func TestPropagatedStableNeverGraduates(t *testing.T) {
	// §9.3: a non-transition propagated `stable` is suppressed with W200.
	// Graduation must never happen because an unrelated package's commit
	// propagated a channel.
	git := newFakeGit(
		commit{sha: "c0", message: "release(app)%beta: enter"},
		commit{sha: "c1", message: "feat(core)^%%stable++1: back to stable"},
	).tag("core", "1.2.3", "").tag("app", "1.0.0", "").tag("app", "1.0.1-beta.0", "c0")

	p := compute(t, git, nil)

	assert.Equal(t, "beta", p.Releases["app"].Channel, "the dependent keeps its own channel")
	assert.True(t, hasCode(p, CodeChannelNoGraduate), "W200, got %v", codes(p))
}

func TestChannelOnlyReleaseGetsTheEntryPatch(t *testing.T) {
	// §11.4: entering a train with no bump of its own would compute a version
	// SemVer ranks *below* the baseline, so one patch is applied and W204
	// reports it.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta: start the train"},
	).tag("core", "1.2.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, ccme.BumpNone, core.Bump, "a release unit carries no bump")
	assert.True(t, core.Releasing(), "a channel change is a reason to release")
	assert.True(t, core.ChannelOnly)
	assertVersion(t, pre(1, 2, 1, "beta", "0"), core.Next, "the entry patch lifts it above 1.2.0")
	assert.True(t, hasCode(p, CodeChannelEntryPatch), "W204, got %v", codes(p))
	assert.True(t, hasCode(p, CodeChannelOnly), "W202, got %v", codes(p))
}

func TestEstablishedTrainNeedsNoDirectives(t *testing.T) {
	// §11.7: a channel is derived from each package's own baseline, so a train
	// already under way stays together with nothing written.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
		commit{sha: "c2", message: "fix(core)^: polish"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c1").
		tag("app", "1.0.0", "").tag("app", "1.0.1-beta.0", "c1")

	p := compute(t, git, nil)

	assert.Equal(t, "beta", p.Releases["core"].Channel)
	app := p.Releases["app"]
	assert.Equal(t, "beta", app.Channel, "no directive needed to stay on the line")
	assert.Equal(t, ccme.BumpPatch, app.Bump, "§9.3a admits it: both are on beta")
	assertVersion(t, pre(1, 0, 1, "beta", "1"), app.Next)
}

// G7: the channel axis discharges through the baseline, even though the
// window still contains the commit. Comparing against the channel computed
// earlier in the same run loses this and re-releases for ever.
func TestChannelAxisConverges(t *testing.T) {
	history := []commit{{sha: "c1", message: "release(core)%beta: start the train"}}

	first := newFakeGit(history...).tag("core", "1.2.0", "")
	assert.True(t, compute(t, first, nil).Releases["core"].Releasing(), "moves onto beta")

	// Having arrived on beta, the same directive proposes nothing.
	second := newFakeGit(history...).tag("core", "1.2.0", "").tag("core", "1.2.1-beta.0", "c1")
	core := compute(t, second, nil).Releases["core"]
	assert.Equal(t, "beta", core.Channel)
	assert.False(t, core.ChannelChanged(), "it is already there")
	assert.False(t, core.Releasing(), "nothing further is proposed (G7)")
}

func TestDirectChannelBeatsPropagated(t *testing.T) {
	// §13.8: a direct directive beats every propagated one regardless of age.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^%beta++1: streaming"},
		commit{sha: "c2", message: "release(app)%rc: app rides its own train"},
	).tag("core", "1.2.3", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, "beta", p.Releases["core"].Channel)
	assert.Equal(t, "rc", p.Releases["app"].Channel, "the direct directive wins")
}

// ---------------------------------------------------------------------------
// §13.1 — graph constraints
// ---------------------------------------------------------------------------

func TestConflictingReleaseAsPicksTheNewest(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "release(app): hold\n\nRelease-As: none\n"},
		commit{sha: "c2", message: "release(app): pin instead\n\nRelease-As: 2.0.0\n"},
	).tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.False(t, app.Held, "the newer directive wins")
	assert.True(t, app.Pinned)
	assertVersion(t, v(2, 0, 0), app.Next)
	assert.True(t, hasCode(p, CodeReleaseAsConflict), "W153, got %v", codes(p))
}

func TestComputedVersionMustExceedTheBaseline(t *testing.T) {
	// Only reachable from a hand-edited tag: the beta line was tagged two
	// majors ahead of anything the stable baseline plus the pending window can
	// produce, so continuing the train would go backwards. The new fix after
	// the tag is what makes the train releasable at all — work contained in
	// the baseline alone converges to "unchanged" instead of erroring.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(core): repair"},
		commit{sha: "c2", message: "fix(core): repair again"},
	).tag("core", "1.0.0", "").tag("core", "2.0.0-beta.0", "c1")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeVersionNotGreater), "E195, got %v", codes(p))
	assert.True(t, p.Fatal(), "an integrity failure aborts the run")
}

func TestGraduationMustNotGoBackwards(t *testing.T) {
	// The beta line was hand-tagged above what the stable baseline plus the
	// pending window can produce, so graduating would lower the version.
	git := newFakeGit(
		commit{sha: "c0", message: "feat(core)%beta: try"},
		commit{sha: "c1", message: "release(core)%stable: promote"},
	).tag("core", "1.0.0", "").tag("core", "2.5.0-beta.0", "c0")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeGraduateNoIncrease), "E185, got %v", codes(p))
	assert.True(t, p.Fatal())
}

func TestRedundantChannelDirectiveProposesNothing(t *testing.T) {
	// A *fresh* directive pointing where the package already is warns W199:
	// the author wrote it and it did nothing.
	git := newFakeGit(
		commit{sha: "c0", message: "release(core)%beta: start"},
		commit{sha: "c1", message: "release(core)%beta: again"},
	).tag("core", "1.0.0", "").tag("core", "1.0.1-beta.0", "c0")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeChannelRedundant), "W199, got %v", codes(p))
	assert.False(t, p.Releases["core"].Releasing(), "and nothing is released")
}

func TestContainedChannelDirectiveIsSilent(t *testing.T) {
	// The directive that *put* the package on beta is contained in the
	// baseline tag it produced. It is not redundant — it worked, and the tag
	// records it — so re-warning "already on beta" (W199) on every run until
	// graduation would report the mechanism working as an anomaly.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta: start"},
	).tag("core", "1.0.0", "").tag("core", "1.0.1-beta.0", "c1")

	p := compute(t, git, nil)

	assert.False(t, hasCode(p, CodeChannelRedundant), "no W199, got %v", codes(p))
	assert.Equal(t, "beta", p.Releases["core"].Channel, "the baseline carries the channel")
	assert.False(t, p.Releases["core"].Releasing())
}

func TestInertTransitionIsReported(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta>beta: nowhere"},
	).tag("core", "1.0.0", "").tag("core", "1.1.0-beta.0", "c1")

	p := compute(t, git, nil)
	assert.True(t, hasCode(p, CodeTransitionInert), "W207, got %v", codes(p))
	assert.False(t, p.Releases["core"].ChannelChanged())
}

func TestPropagatedTransitionMatchingNothingIsReported(t *testing.T) {
	// A mistyped <from>: the transition reaches the dependent and matches no
	// package, so nothing moves and the directive is reported as inert.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%%nosuch>stable++1: graduate them"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeTransitionUnmatched), "W206, got %v", codes(p))
	assert.Equal(t, "stable", p.Releases["app"].Channel)
}

func TestChannelScopeExcludingEverythingWarns(t *testing.T) {
	git := newFakeGit(commit{
		sha:     "c1",
		message: "feat(core)%beta++1: start a train\n\nPropagate-Channel-Scope: utils\n",
	}).tag("core", "1.0.0", "").tag("app", "1.0.0", "").tag("utils", "1.0.0", "")

	pkgs, _ := testPackages()
	deps := []model.Dependency{{Consumer: "app", Provider: "core"}}
	p, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r",
	})
	require.NoError(t, err)

	assert.True(t, hasCode(p, CodeChannelScopeExcludedAll), "W205, got %v", codes(p))
	assert.Equal(t, "stable", p.Releases["app"].Channel)
}

func TestConflictingPropagatedChannelsPickTheNewest(t *testing.T) {
	// Two providers push different channels at the same dependent; the newer
	// commit wins and the conflict is reported rather than rejected.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(utils)%rc++1: utils goes rc"},
		commit{sha: "c2", message: "feat(core)%beta++1: core goes beta"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, "beta", p.Releases["app"].Channel, "the newer commit wins")
	assert.True(t, hasCode(p, CodePropagatedChannelConflict), "W160, got %v", codes(p))
}

// ---------------------------------------------------------------------------
// §16 — blast radius
// ---------------------------------------------------------------------------

func TestErrorBlastRadius(t *testing.T) {
	// A unit-scoped error invalidates what it names and nothing else; only a
	// repository-scoped one means no correct plan exists at all.
	unitScoped := newFakeGit(
		commit{sha: "c1", message: "feat(nosuch): typo"},
		commit{sha: "c2", message: "feat(core): real work"},
	).tag("core", "1.0.0", "")

	p := compute(t, unitScoped, nil)
	assert.True(t, p.HasErrors(), "E130 was raised")
	assert.False(t, p.Fatal(), "but a typo does not stop the repository from having a plan")
	assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump, "the sibling commit still applies")

	// A tag that cannot be continued from is an integrity failure with no
	// offending unit to invalidate. The fix after the bad tag is what forces
	// the train to be continued — the tagged work alone is converged.
	repoScoped := newFakeGit(
		commit{sha: "c1", message: "feat(core)%beta: streaming"},
		commit{sha: "c2", message: "fix(core): more"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta", "c1")

	p = compute(t, repoScoped, nil)
	assert.True(t, hasCode(p, CodeBadPrereleaseTag), "E182, got %v", codes(p))
	assert.True(t, p.Fatal(), "repository-scoped errors abort whatever the policy")
}

// ---------------------------------------------------------------------------
// §14 — tag formats
// ---------------------------------------------------------------------------

func TestTagNameUsesTheSpaceFormat(t *testing.T) {
	space := &model.Space{Name: "services", TagFormat: "services/{name}@v{version}"}
	rel := &Release{
		Pkg:  &model.Package{Name: "core", Space: space},
		Next: v(1, 3, 0),
	}
	assert.Equal(t, "services/core@v1.3.0", rel.TagName())

	plain := &Release{
		Pkg:  &model.Package{Name: "core", Space: &model.Space{Name: "libs"}},
		Next: v(1, 3, 0),
	}
	assert.Equal(t, "core@1.3.0", plain.TagName(), "an unset space format falls back to the default")
}

func TestCycleIsRejected(t *testing.T) {
	pkgs, _ := testPackages()
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core"},
		{Consumer: "core", Provider: "app"},
	}
	// A cycle is not a load failure but §16's E200: a fatal plan carrying the
	// repository-scoped diagnostic, so the code reaches the events stream.
	p, err := Compute(context.Background(), newFakeGit(), Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)
	require.True(t, p.Fatal(), "a cyclic graph must make the plan fatal")
	require.Len(t, p.Diagnostics, 1)
	assert.Equal(t, CodeDependencyCycle, p.Diagnostics[0].Code)
	assert.Contains(t, p.Diagnostics[0].Message, "cycle")
	assert.Contains(t, p.Diagnostics[0].Message, "app -> core (dependencies)",
		"the diagnostic names the edges and the manifest field carrying each one")
	assert.Empty(t, p.Order, "no publish order exists")
}

func TestDuplicateVersionTagsAreRejected(t *testing.T) {
	// Two reachable tags parsing to the same version on different commits:
	// "core@1.2.3" and "core@1.2.3+hotfix" collide because build metadata
	// carries no precedence (§12.1).
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): first"},
		commit{sha: "c2", message: "feat(core): second"},
	).tag("core", "1.2.3", "c1")
	git.tags["core@1.2.3+hotfix"] = "c2"
	git.tagsFor["core"] = append(git.tagsFor["core"], "core@1.2.3+hotfix")

	pkgs, _ := testPackages()
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	require.True(t, p.Fatal())
	require.Len(t, p.Diagnostics, 1)
	assert.Equal(t, CodeDuplicateVersionTag, p.Diagnostics[0].Code)
	assert.Equal(t, "core", p.Diagnostics[0].Pkg)
}

func TestShallowRepositoryIsRejected(t *testing.T) {
	git := newFakeGit(commit{sha: "c1", message: "feat(core): work"})
	git.shallow = true
	pkgs, _ := testPackages()
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	require.True(t, p.Fatal())
	require.Len(t, p.Diagnostics, 1)
	assert.Equal(t, CodeShallowRepository, p.Diagnostics[0].Code)
}

// ---------------------------------------------------------------------------
// §11.4 — train convergence: published prerelease work must not re-release
// ---------------------------------------------------------------------------

func TestPrereleaseTrainConvergesAfterRelease(t *testing.T) {
	// The reported bug. app entered beta with a breaking change and released
	// 1.0.0-beta.0. A re-run with no new commits must find nothing to do: the
	// window still spans the whole train (it is measured from the stable tag),
	// but everything in it is contained in the baseline prerelease tag, and
	// already-published work re-releasing the train would produce beta.1,
	// beta.2, ... forever.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)%beta!: new api"},
	).tag("app", "0.1.5", "").tag("app", "1.0.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.False(t, app.Changed(), "published train content must not re-release")
	assert.Empty(t, p.Releasing(), "the plan must be empty")
	assertVersion(t, pre(1, 0, 0, "beta", "0"), app.Next,
		"an unchanged package reports its baseline")
	assert.Equal(t, ccme.BumpMajor, app.Bump,
		"the bump is still computed over the whole train — graduation needs it")
	assert.False(t, app.NewWork, "everything pending is contained in the baseline")
	assert.False(t, hasCode(p, CodeChannelRedundant),
		"the directive that started the train is contained, not redundant — no W199, got %v", codes(p))
}

func TestPrereleaseTrainNewWorkContinuesTheCounter(t *testing.T) {
	// A fix landing after beta.0 is new work: the train continues at beta.1,
	// and the version is still computed over the WHOLE window — the published
	// breaking change keeps the target at 1.0.0 rather than shrinking it to a
	// patch of the stable baseline.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)%beta!: new api"},
		commit{sha: "c2", message: "fix(app): tweak"},
	).tag("app", "0.1.5", "").tag("app", "1.0.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Changed(), "new work releases the train")
	assert.True(t, app.NewWork)
	assert.Equal(t, ccme.BumpMajor, app.Bump, "max over the whole train, not just the new fix")
	assertVersion(t, pre(1, 0, 0, "beta", "1"), app.Next, "the counter continues")
}

func TestTrainGraduationCountsPublishedWork(t *testing.T) {
	// Graduation releases the whole train under one stable version, so the
	// bump of the already-published breaking change must still be in force
	// even though it alone would not have re-released the train.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)%beta!: new api"},
		commit{sha: "c2", message: "release(app)%stable: ship it"},
	).tag("app", "0.1.5", "").tag("app", "1.0.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Changed(), "a graduation is a channel change")
	assert.Equal(t, "beta -> stable", app.ChannelTransition())
	assertVersion(t, v(1, 0, 0), app.Next,
		"applyBump(stable baseline, effective) with the train's major, no suffix")
}

func TestPinDischargedByPrereleaseRelease(t *testing.T) {
	// A Release-As pin published by a prerelease is consumed exactly as a
	// stable release consumes one. Without the discharge the pin stays in
	// force — equal to the baseline it created — and raises E153 ("does not
	// move forward") on every later run of the train.
	git := newFakeGit(
		commit{sha: "c1", message: "release(app): try the rc\n\nRelease-As: 2.0.0-rc.0\n"},
	).tag("app", "1.0.0", "").tag("app", "2.0.0-rc.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.False(t, app.Pinned, "the pin was consumed by the release that shipped it")
	assert.False(t, app.Changed())
	assert.False(t, hasCode(p, CodePinNotGreater), "no E153, got %v", codes(p))
	assert.False(t, p.HasErrors())
}

func TestCancelCannotRetractPublishedPrerelease(t *testing.T) {
	// §10.3: cancellation never reaches a published tag, and a prerelease tag
	// is a published tag. Without the guard the cancel discards the train's
	// published breaking change, the effective bump shrinks to the new fix's
	// patch, and the computed 0.1.6-beta.0 goes backwards from the baseline —
	// E195 aborting the run over work that is already public.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app)%beta!: new api"},
		commit{sha: "c2", message: "cancel(app): try to unship it"},
		commit{sha: "c3", message: "fix(app): tweak"},
	).tag("app", "0.1.5", "").tag("app", "1.0.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.Equal(t, ccme.BumpMajor, app.Bump, "the published breaking change survives the cancel")
	assertVersion(t, pre(1, 0, 0, "beta", "1"), app.Next)
	assert.False(t, hasCode(p, CodeVersionNotGreater), "no E195, got %v", codes(p))
	assert.True(t, hasCode(p, CodeEmptyCancel),
		"W170: the cancel discarded nothing, and says so")
}

func TestPropagatedTrainWorkConverges(t *testing.T) {
	// The propagated flavour of the same bug: app (on beta) released
	// 1.3.0-beta.0 carrying core's propagated minor. Core has discharged the
	// commit too. A re-run must not plan beta.1 out of the same contribution.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^minor: streaming"},
	).tag("core", "1.4.0", "c1").
		tag("app", "1.2.3", "").tag("app", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.False(t, app.Changed(), "published propagated work must not re-release the train")
	assert.Equal(t, ccme.BumpMinor, app.PropagatedBump, "still computed for the window")
	assert.Empty(t, app.DueTo,
		"a delivered blast keeps counting toward the train's target, but it is not a reason to release: reported once, in the release that shipped it")
	assert.Empty(t, p.Releasing())

	// A second propagated contribution after beta.0 is new work: the train
	// continues, and the whole window keeps the target at 1.3.0.
	git2 := newFakeGit(
		commit{sha: "c1", message: "feat(core)^minor: streaming"},
		commit{sha: "c2", message: "fix(core)^: repair"},
	).tag("core", "1.4.0", "c1").
		tag("app", "1.2.3", "").tag("app", "1.3.0-beta.0", "c1")

	p2 := compute(t, git2, nil)
	app2 := p2.Releases["app"]
	assert.True(t, app2.Changed(), "fresh propagated work releases the train")
	assertVersion(t, pre(1, 3, 0, "beta", "1"), app2.Next)
}

func TestReasonSpeaksOfFreshWorkOnly(t *testing.T) {
	// app's beta.0 already published its own feat; the fresh cause of beta.1
	// is core's propagated fix alone. The train-wide OwnBump keeps deciding
	// the target, but the reason names what actually forces this release:
	// the propagation, not own work the train has already shipped.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): own work"},
		commit{sha: "c2", message: "fix(core)^: repair"},
	).tag("core", "1.4.0", "").
		tag("app", "1.2.3", "").tag("app", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Changed())
	assert.Equal(t, ccme.BumpMinor, app.OwnBump, "the published feat still decides the train's target")
	assert.Equal(t, "propagated from core", app.Reason())
}

func TestCatchUpOnATrainWithPublishedOwnWork(t *testing.T) {
	// app's beta.0 already published its own feat, and core then released the
	// propagating fix in a run whose app leg never happened. app's only fresh
	// cause is core's already-published propagation — a textbook catch-up —
	// and the own work the train already shipped must not hide it: OwnBump
	// spans the train, but the catch-up scan reads the fresh changeset.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(app): own work"},
		commit{sha: "c2", message: "fix(core)^: repair"},
	).tag("core", "1.4.0", "").tag("core", "1.4.1", "c2").
		tag("app", "1.2.3", "").tag("app", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.True(t, app.Changed(), "the published propagation still owes app a release")
	assert.Equal(t, ccme.BumpMinor, app.OwnBump, "the published feat still decides the train's target")
	assert.True(t, app.CatchUp, "the release must be labelled a catch-up")
	assert.True(t, hasCode(p, CodeCatchUp), "W193, got %v", codes(p))
	assert.Equal(t, "catch-up from core", app.Reason())
}

func TestRecordsSpeakInDirectProviders(t *testing.T) {
	// A depth-all blast from lib reaches top two hops away. DueTo names the
	// origin, because that answers why top releases; the dependencies
	// record does not, because top's manifests never mention lib — the
	// movement top actually picks up is mid's, which the releasing half of
	// Updates carries.
	libs := &model.Space{Name: "libs"}
	pkgs := []*model.Package{
		{Name: "lib", Dir: "/r/lib", Space: libs},
		{Name: "mid", Dir: "/r/mid", Space: libs},
		{Name: "top", Dir: "/r/top", Space: libs},
	}
	deps := []model.Dependency{
		{Consumer: "mid", Provider: "lib"},
		{Consumer: "top", Provider: "mid"},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "fix(lib)^^: deep repair"},
	).tag("lib", "1.0.0", "").tag("mid", "1.0.0", "").tag("top", "1.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)

	top := p.Releases["top"]
	assert.True(t, top.Changed(), "the blast reaches two hops")
	assert.Equal(t, []string{"lib"}, top.DueTo, "the origin answers why")
	names := make([]string, 0, len(top.Updates))
	for _, u := range top.Updates {
		names = append(names, u.Name)
	}
	assert.Equal(t, []string{"mid"}, names,
		"the record names the direct provider whose movement top picks up")
}

func TestDeliveredBlastNamesOnlyTheFreshOrigin(t *testing.T) {
	// The mixed shape: app's beta.0 already carried core's propagated minor,
	// and utils' fresh propagated patch now continues the train. DueTo names
	// utils alone — core's contribution still counts toward the train's
	// target, but its blast was reported by the release that shipped it, and
	// naming it again would put a provider that moved nothing into every
	// later plan and record until graduation.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^minor: streaming"},
		commit{sha: "c2", message: "fix(utils)^: repair"},
	).tag("core", "1.4.0", "c1").tag("utils", "0.9.1", "").
		tag("app", "1.2.3", "").tag("app", "1.3.0-beta.0", "c1")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	assert.True(t, app.Changed(), "the fresh blast releases the train")
	assertVersion(t, pre(1, 3, 0, "beta", "1"), app.Next)
	assert.Equal(t, []string{"utils"}, app.DueTo,
		"only the origin the baseline has not answered yet")
	assert.Equal(t, ccme.BumpMinor, app.PropagatedBump,
		"the delivered minor still outweighs the fresh patch in the train's target")
}

func TestGraduationUpdatesSpanTheTrain(t *testing.T) {
	// core's fix propagated onto app's train and beta.1 shipped it, so the
	// blast is delivered and DueTo is rightly empty. But the graduation's
	// entry is the one readers of the stable line actually see, so its
	// dependencies section must still carry core's movement over the whole
	// train — reconstructed from the tags, From at app's last stable release.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: baseline"},
		commit{sha: "c1", message: "feat(app)%beta: board the train"},
		commit{sha: "c2", message: "fix(core)^: repair underneath"},
		commit{sha: "c3", message: "release(app)%stable: graduate"},
	).tag("core", "1.4.0", "c0").tag("core", "1.4.1", "c2").
		tag("app", "1.2.3", "c0").
		tag("app", "1.3.0-beta.0", "c1").tag("app", "1.3.0-beta.1", "c2")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.True(t, app.Changed(), "the direct directive graduates the train")
	assertVersion(t, v(1, 3, 0), app.Next)
	assert.Empty(t, app.DueTo, "the blast was delivered by beta.1 and is not re-reported")
	require.Len(t, app.Updates, 1, "the stable entry still documents what moved underneath")
	assert.Equal(t, "core", app.Updates[0].Name)
	assert.Equal(t, "1.4.0", app.Updates[0].From.String(), "From is the version app's last stable release shipped against")
	assert.Equal(t, "1.4.1", app.Updates[0].To.String())
}

func TestCatchUpUpdatesSpanFromTheConsumersLastRelease(t *testing.T) {
	// core published 1.4.1 in a run app's leg missed, so by the catch-up
	// core's own before-and-after have collapsed onto 1.4.1 — and a record
	// reading them says "1.4.1 -> 1.4.1", a movement line with no movement
	// (the docs leg of the 1.3.0 release wrote exactly that). From is what
	// app's previous release shipped against, reconstructed off core's tags
	// at app's own baseline, the same way a graduation spans its train.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: baseline"},
		commit{sha: "c1", message: "fix(core)^: reaches app, whose leg died"},
	).tag("core", "1.4.0", "c0").tag("core", "1.4.1", "c1").
		tag("app", "1.2.3", "c0")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.True(t, app.Changed(), "the missed blast is still owed")
	assert.True(t, app.CatchUp, "and it is a catch-up: core is not in the plan")
	require.Len(t, app.Updates, 1)
	assert.Equal(t, "core", app.Updates[0].Name)
	assert.Equal(t, "1.4.0", app.Updates[0].From.String(),
		"From is the version app's last release shipped against")
	assert.Equal(t, "1.4.1", app.Updates[0].To.String())
}

func TestSpentCancelIsNotReported(t *testing.T) {
	// The cancel did its work in an earlier run: it discarded core's pending
	// feat, and core then released past it. The discard is invisible now (the
	// discarded units left core's window together with the cancel), and the
	// cancel commit only survives in the union window because app's window
	// still spans it. It must not warn W170 — that message is for a *live*
	// cancel that discards nothing, the misaimed case of §13.7d.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): streaming"},
		commit{sha: "c2", message: "cancel(core): drop it"},
		commit{sha: "c3", message: "fix(core): something else"},
	).tag("core", "1.1.0", "c3").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.False(t, hasCode(p, CodeEmptyCancel),
		"a cancel discharged for every package it names is spent, not misaimed; got %v", codes(p))
	assert.False(t, p.Releases["core"].Changed())
}

func TestSpentCancelInsideATrainIsNotReported(t *testing.T) {
	// The train flavour: the cancel is contained in the baseline prerelease —
	// whatever it had to say was said before beta.0 shipped it.
	git := newFakeGit(
		commit{sha: "c1", message: "cancel(app): drop pending work"},
		commit{sha: "c2", message: "feat(app)%beta!: new api"},
	).tag("app", "0.1.5", "").tag("app", "1.0.0-beta.0", "c2")

	p := compute(t, git, nil)

	assert.False(t, hasCode(p, CodeEmptyCancel), "no W170, got %v", codes(p))
	assert.False(t, p.Releases["app"].Changed(), "the train is converged")
}

func TestReleaseUpdatesCarryProviderVersions(t *testing.T) {
	// The consumer's Updates carry each provider's version movement, so a
	// changelog can say "core: 1.0.0 -> 1.1.0" instead of the bare name.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming"},
	).tag("core", "1.0.0", "").tag("app", "2.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.Len(t, app.Updates, 1)
	assert.Equal(t, "core", app.Updates[0].Name)
	assertVersion(t, v(1, 0, 0), app.Updates[0].From, "what the provider last published")
	assertVersion(t, v(1, 1, 0), app.Updates[0].To, "what it releases this run")

	// On a catch-up the provider's version is already out: From equals To.
	git2 := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming"},
	).tag("core", "1.1.0", "c1").tag("app", "2.0.0", "")

	p2 := compute(t, git2, nil)

	app2 := p2.Releases["app"]
	require.True(t, app2.CatchUp)
	require.Len(t, app2.Updates, 1)
	assertVersion(t, v(1, 1, 0), app2.Updates[0].From)
	assertVersion(t, v(1, 1, 0), app2.Updates[0].To, "already published: nothing moves, the consumer catches up to it")
}

func TestPropagatedTransitionGraduatesTheDependant(t *testing.T) {
	// §9.3's deliberate exception, as channel.go documents it: a propagated
	// `beta>stable` *transition* — unlike a propagated bare `stable` (W200,
	// TestPropagatedStableNeverGraduates) — does graduate the dependants
	// still on the named train, because its author had to name the train
	// being ended in order to write it. This is the
	// `release(core)%beta>stable%%beta>stable++*` form the configuration
	// reference walks through.
	git := newFakeGit(
		commit{sha: "c0", message: "feat(core)^%beta++1: the train, both aboard"},
		commit{sha: "c1", message: "release(core)%beta>stable%%beta>stable++1: graduate the whole train"},
	).tag("core", "1.2.3", "").tag("core", "1.3.0-beta.0", "c0").
		tag("app", "1.0.0", "").tag("app", "1.0.1-beta.0", "c0")

	p := compute(t, git, nil)

	core, app := p.Releases["core"], p.Releases["app"]
	assert.Equal(t, ccme.ChannelStable, core.Channel)
	assertVersion(t, v(1, 3, 0), core.Next, "core graduates over the whole train")

	assert.Equal(t, ccme.ChannelStable, app.Channel, "the propagated transition graduates the dependant")
	assert.Equal(t, "core", app.ChannelFrom)
	require.True(t, app.Releasing())
	assertVersion(t, v(1, 0, 1), app.Next, "graduated at the train's target")
	assert.False(t, hasCode(p, CodeChannelNoGraduate), "no W200 for a transition, got %v", codes(p))
	assert.False(t, hasCode(p, CodeTransitionUnmatched), "the transition matched, got %v", codes(p))
}

func TestPropagatedTransitionStillSkipsPackagesOffTheTrain(t *testing.T) {
	// The transition's precision survives the graduation exception: a
	// dependant whose baseline is not on the named train is unmatched and
	// stays exactly where it is.
	git := newFakeGit(
		commit{sha: "c1", message: "release(core)%beta>stable%%beta>stable++1: graduate"},
	).tag("core", "1.3.0-beta.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, ccme.ChannelStable, p.Releases["core"].Channel)
	app := p.Releases["app"]
	assert.False(t, app.Releasing(), "a stable dependant is not on the train and must not move")
	assert.Equal(t, ccme.ChannelStable, app.Channel)
}

// TestDiagnosticCodesAreDocumented: every code dispat defines outside the
// specification's registry has to appear in the architecture page's inventory
// of them. That inventory is a claim about what this project emits, and a code
// added without a line there is a feature nobody can look up.
//
// The three files it reads all sit outside this module, which Go's test cache
// does not track: a stale "ok" survives a rename of any of them. Anything
// moving these paths has to re-run this test with -count=1.
func TestDiagnosticCodesAreDocumented(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	require.NoError(t, err)

	spec, err := os.ReadFile(filepath.Join(root, "specs", "ccme-spec", "SPEC.md"))
	require.NoError(t, err)
	source, err := os.ReadFile(filepath.Join(root, "services", "dispat", "internal", "plan", "plan.go"))
	require.NoError(t, err)
	page, err := os.ReadFile(filepath.Join(root, "packages", "docs", "docs", "internals", "architecture.md"))
	require.NoError(t, err)

	reserved := map[string]bool{}
	for _, c := range regexp.MustCompile(`\b[EW][0-9]{3}\b`).FindAllString(string(spec), -1) {
		reserved[c] = true
	}

	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([EW][0-9]{3})"`).FindAllStringSubmatch(string(source), -1) {
		code := m[1]
		if reserved[code] || seen[code] {
			continue
		}
		seen[code] = true
		// Either named outright or covered by a range like "`W234`-`W237`".
		if strings.Contains(string(page), code) || coveredByDocRange(string(page), code) {
			continue
		}
		t.Errorf("%s is dispat's own but the architecture page does not list it", code)
	}
	require.NotEmpty(t, seen, "the scan must find dispat's own codes at all")
}

// coveredByDocRange reports whether the page lists a range "`W234`-`W237`"
// that contains the code.
func coveredByDocRange(page, code string) bool {
	for _, m := range regexp.MustCompile("`([EW])([0-9]{3})`-`([EW])([0-9]{3})`").FindAllStringSubmatch(page, -1) {
		if m[1] != code[:1] || m[3] != code[:1] {
			continue
		}
		lo, _ := strconv.Atoi(m[2])
		hi, _ := strconv.Atoi(m[4])
		n, _ := strconv.Atoi(code[1:])
		if n >= lo && n <= hi {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Release.Updates: every provider whose version a release picks up
// ---------------------------------------------------------------------------

func TestUpdatesCoverAReleasingProviderThatPropagatedNothing(t *testing.T) {
	// The ordinary run on default settings. Propagation depth is 0, so a
	// `feat(core)` beside a `fix(app)` moves nobody: app releases for its own
	// reason and DueTo is empty. app still declares core as a provider and
	// still ships against core's new version, so Updates has to name it —
	// this is what the manifests, the version script and the changelog all
	// read.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): a feature"},
		commit{sha: "c2", message: "fix(app): something of its own"},
	).tag("core", "1.0.0", "").tag("app", "2.0.0", "").tag("utils", "1.0.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.True(t, app.Releasing())
	assert.Empty(t, app.DueTo, "nothing propagated: DueTo answers a different question")
	require.Len(t, app.Updates, 1, "got %+v", app.Updates)
	assert.Equal(t, "core", app.Updates[0].Name)
	assertVersion(t, v(1, 0, 0), app.Updates[0].From)
	assertVersion(t, v(1, 1, 0), app.Updates[0].To)

	assert.Empty(t, p.Releases["core"].Updates, "core declares no providers")
}

func TestUpdatesLeaveOutAProviderThatIsNotReleasing(t *testing.T) {
	// utils declares no work this run, so it publishes nothing for app to
	// pick up and must stay out — otherwise every consumer's changelog would
	// list every provider on every release, saying nothing.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): a feature"},
		commit{sha: "c2", message: "fix(app): something of its own"},
	).tag("core", "1.0.0", "").tag("app", "2.0.0", "").tag("utils", "1.0.0", "")

	p := compute(t, git, nil)

	names := make([]string, 0, 2)
	for _, u := range p.Releases["app"].Updates {
		names = append(names, u.Name)
	}
	assert.Equal(t, []string{"core"}, names, "utils is quiet this run")
}

func TestUpdatesKeepACatchUpProviderThatIsNotReleasing(t *testing.T) {
	// The case that forces a union rather than a replacement (§13.7a). core
	// published in an earlier run; app's leg failed and is only now catching
	// up. core is *not* releasing now, so the "releasing providers" half
	// misses it, and only DueTo carries it. From equals To: the version is
	// already out.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: reaches its consumers"},
	).tag("core", "1.0.0", "").tag("core", "1.1.0", "c1").tag("app", "2.0.0", "")

	p := compute(t, git, nil)

	core, app := p.Releases["core"], p.Releases["app"]
	require.False(t, core.Releasing(), "core already published this work")
	require.True(t, app.Releasing(), "app is the catch-up")
	assert.Equal(t, []string{"core"}, app.DueTo)
	require.Len(t, app.Updates, 1)
	assert.Equal(t, "core", app.Updates[0].Name)
	assert.Equal(t, app.Updates[0].From, app.Updates[0].To,
		"already-published provider: the consumer picks up a version that has not moved since")
}

func TestUpdatesNameEachProviderOnce(t *testing.T) {
	// Plan.Providers is indexed by edge, so one pair declared under two
	// dependency kinds is two entries and one provider. A duplicated update
	// would duplicate a changelog line and a DISPAT_UPDATED_* entry.
	pkgs, _ := testPackages()
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core", Kind: model.DepKind("dependencies")},
		{Consumer: "app", Provider: "core", Kind: model.DepKind("devDependencies")},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): a feature"},
		commit{sha: "c2", message: "fix(app): something of its own"},
	).tag("core", "1.0.0", "").tag("app", "2.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)

	require.Len(t, p.Releases["app"].Updates, 1, "got %+v", p.Releases["app"].Updates)
	assert.Equal(t, "core", p.Releases["app"].Updates[0].Name)
}

// TestTraceNarratesTheDerivation: at trace level the planner says how it got
// there — scope resolution, propagation edges, applied bumps, resolved
// channels — the intermediates "why did commit X (not) count for package Y"
// is answered from. Shape only: the events exist and name their subjects;
// the exact wording stays free.
func TestTraceNarratesTheDerivation(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: streaming", files: []string{"libs/core/a.go"}},
		commit{sha: "c2", message: "fix: helpers", files: []string{"libs/utils/b.go"}},
		commit{sha: "c3", message: "fix(app)%beta: flag", files: []string{"apps/app/c.go"}},
	)
	pkgs, deps := testPackages()

	var buf bytes.Buffer
	log := zerolog.New(&buf).Level(zerolog.TraceLevel)
	_, err := Compute(context.Background(), git, Options{
		Packages: pkgs, Dependencies: deps, Root: "/r", Log: log,
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "plan: scope resolved")
	assert.Contains(t, out, "plan: package derived from the commit's files",
		"the scopeless c2 resolves through §6.2, naming the file")
	assert.Contains(t, out, "plan: propagation edge")
	assert.Contains(t, out, "plan: bump propagated")
	assert.Contains(t, out, "plan: channel resolved")
	assert.Contains(t, out, "plan: package resolved")
}

// TestAliasFilterKeepsOnlyWhatCouldBeARelease: the listing a package's own
// moving alias appears in, which is every listing from the first release that
// wrote one.
//
// The alias has the release format's shape and no version in it, and it is the
// newest tag by creation date, so leaving it in makes it the baseline and the
// package looks unreleased for good. Dropping every unparseable name instead
// would take the malformed release tag with it, and that one has to stay: the
// initials fallback measures the window from it so already released commits
// are not counted a second time.
func TestAliasFilterKeepsOnlyWhatCouldBeARelease(t *testing.T) {
	pkg := &model.Package{Name: "crier", Space: &model.Space{
		Name: "root", TagFormat: "v{version}",
		AliasTags: []model.AliasTag{{Format: "v{major}", Moving: true, Force: true, Channels: []string{"stable"}}},
	}}
	// Newest first by creation date, which is the order a listing arrives in.
	tags := gitx.Tags{
		{Name: "v1"},       // the alias, rewritten by the last release
		{Name: "v1.0.0.0"}, // a release tag somebody mistyped
		{Name: "v1.1.0", Version: ccme.Version{Major: 1, Minor: 1}, Parsed: true},
		{Name: "v1.0.0", Version: ccme.Version{Major: 1}, Parsed: true},
	}

	filter := NewAliasFilter([]*model.Package{pkg})
	kept := filter.Without(tags, pkg.Name, zerolog.Nop())
	names := make([]string, 0, len(kept))
	for _, t := range kept {
		names = append(names, t.Name)
	}
	assert.Equal(t, []string{"v1.0.0.0", "v1.1.0", "v1.0.0"}, names,
		"the alias goes, the malformed release tag stays")

	baseline, ok := kept.Baseline()
	require.True(t, ok)
	assert.False(t, baseline.Parsed, "the malformed tag is still what an unreadable baseline looks like")

	// And with it out of the way, the release tags read as they always did.
	only := filter.Without(gitx.Tags{tags[0], tags[2], tags[3]}, pkg.Name, zerolog.Nop())
	baseline, ok = only.Baseline()
	require.True(t, ok)
	assert.Equal(t, "v1.1.0", baseline.Name)
	assert.Equal(t, "1.1.0", baseline.Version.String())

	// A workspace declaring no alias filters nothing, which is what keeps the
	// filter from having an opinion about anybody else's tags.
	plain := &model.Package{Name: "crier", Space: &model.Space{Name: "root", TagFormat: "v{version}"}}
	assert.Len(t, NewAliasFilter([]*model.Package{plain}).Without(tags, "crier", zerolog.Nop()), len(tags))
	assert.Len(t, AliasFilter{}.Without(tags, "crier", zerolog.Nop()), len(tags))
	assert.Len(t, NewAliasFilter(nil).Without(tags, "crier", zerolog.Nop()), len(tags))
}

// TestAliasFilterCoversEveryPackagesAliases: an alias belongs to the package
// that writes it and lands in whichever listing its shape matches, which need
// not be its owner's.
//
// One package tags "v{version}" and another writes the bare "v{major}" a
// consumer pins. Both are legal, and neither the configuration nor the tag
// itself says whose it is: "v1" simply matches the first package's format and
// parses as nothing. A filter that only knew the listing owner's aliases left
// that package's baseline collapsing to its initials from the other's first
// release onwards.
func TestAliasFilterCoversEveryPackagesAliases(t *testing.T) {
	space := &model.Space{Name: "libs", TagFormat: "v{version}"}
	action := &model.Space{Name: "actions", TagFormat: "action-v{version}",
		AliasTags: []model.AliasTag{{Format: "v{major}", Moving: true, Force: true}}}
	core := &model.Package{Name: "core", Space: space}
	act := &model.Package{Name: "act", Space: action}

	tags := gitx.Tags{
		{Name: "v1"}, // the action's alias, newest, and shaped like a release of core
		{Name: "v1.2.0", Version: ccme.Version{Major: 1, Minor: 2}, Parsed: true},
	}

	// The listing owner declares no alias at all, so its own set is empty.
	assert.Len(t, NewAliasFilter([]*model.Package{core}).Without(tags, "core", zerolog.Nop()), 2,
		"nothing but core is what used to be looked at")

	kept := NewAliasFilter([]*model.Package{core, act}).Without(tags, "core", zerolog.Nop())
	require.Len(t, kept, 1)
	baseline, ok := kept.Baseline()
	require.True(t, ok)
	assert.Equal(t, "v1.2.0", baseline.Name, "core is released, not new")
	assert.True(t, baseline.Parsed)
}

// aliasGit serves one package's tag listing verbatim, newest first, which is
// what the moving alias makes possible to describe: a name the format matches
// sitting above every release.
type aliasGit struct {
	*fakeGit
	list gitx.Tags
}

func (g *aliasGit) Tags(context.Context, string, gitx.TagFormat) (gitx.Tags, error) {
	return g.list, nil
}

// TestPlanReadsPastAMovingAlias: the single-repository convention
// through the planner. Tags are "v1.4.2" and the alias is "v1", so the alias
// matches the release format and is the newest tag by creation date the moment
// a release writes it. Read as the baseline it carries no version, and the
// package looks unreleased from its first release onwards.
func TestPlanReadsPastAMovingAlias(t *testing.T) {
	git := &aliasGit{fakeGit: newFakeGit(
		commit{sha: "c1", message: "feat(core): first"},
		commit{sha: "c2", message: "feat(core): second"},
	)}
	git.tags["v0.1.0"] = "c1"
	git.list = gitx.Tags{
		{Name: "v0", Commit: "c1"}, // the alias, newest and unparseable
		{Name: "v0.1.0", Commit: "c1", Version: v(0, 1, 0), Parsed: true},
	}

	pkg := &model.Package{Name: "core", Dir: "/r/core", Space: &model.Space{
		Name: "root", TagFormat: "v{version}",
		AliasTags: []model.AliasTag{{Format: "v{major}", Moving: true, Force: true}},
	}}
	p, err := Compute(context.Background(), git, Options{Packages: []*model.Package{pkg}, Root: "/r"})
	require.NoError(t, err)

	rel := p.Releases["core"]
	require.NotNil(t, rel)
	assert.True(t, rel.Tagged, "the release the alias points at is still the baseline")
	assertVersion(t, v(0, 1, 0), rel.Current)
	assertVersion(t, v(0, 2, 0), rel.Next, "and the window is the one commit since it")
}
