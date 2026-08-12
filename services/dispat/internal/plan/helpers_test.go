package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/ignore"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Unit tests of the plan's small reporting helpers and ancestry fallbacks —
// the accessors scripts, changelogs and diagnostics read, whose branches the
// scenario tests exercise only partially.

func TestDiagnosticString(t *testing.T) {
	assert.Equal(t, "W131: something inert",
		Diagnostic{Code: "W131", Message: "something inert"}.String())
	assert.Equal(t, "E130: core: unknown include",
		Diagnostic{Code: "E130", Pkg: "core", Message: "unknown include"}.String())
}

func TestReleaseOutputAccessor(t *testing.T) {
	rel := &Release{Outputs: []Output{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}}}
	v, ok := rel.Output("B")
	require.True(t, ok)
	assert.Equal(t, "2", v)
	_, ok = rel.Output("C")
	assert.False(t, ok)
}

func TestReleaseCounterAccessors(t *testing.T) {
	pkg := &model.Package{Name: "core", Space: &model.Space{Name: "libs"}}

	stable := &Release{Pkg: pkg, Next: v(1, 2, 3)}
	assert.False(t, stable.IsPrerelease())
	assert.Equal(t, "", stable.Counter())
	assert.Equal(t, "core@1.2.3", stable.SemverTagName())

	train := &Release{Pkg: pkg,
		Next:        ccme.Version{Major: 1, Minor: 3, Prerelease: []string{"beta", "4"}},
		Baseline:    ccme.Version{Major: 1, Minor: 3, Prerelease: []string{"beta", "3"}},
		HasBaseline: true,
	}
	assert.True(t, train.IsPrerelease())
	assert.Equal(t, "4", train.Counter())
	assert.Equal(t, "3", train.PreviousCounter())
	assert.Equal(t, "core@1.3.0-beta.4", train.SemverTagName())

	// An exact Release-As may carry more than the bare number; everything
	// after the channel belongs to the counter.
	pinned := &Release{Pkg: pkg,
		Next: ccme.Version{Major: 2, Prerelease: []string{"rc", "1", "hotfix"}}}
	assert.Equal(t, "1.hotfix", pinned.Counter())
}

func TestReleaseReason(t *testing.T) {
	pkg := &model.Package{Name: "core", Space: &model.Space{Name: "libs"}}
	for name, tc := range map[string]struct {
		rel  Release
		want string
	}{
		"fixed_ride":   {Release{FixedRide: true}, "fixed group versioning"},
		"catch_up":     {Release{CatchUp: true, Sources: []StaleSource{{Provider: "a"}, {Provider: "a"}, {Provider: "b"}}}, "catch-up from a, b"},
		"channel_only": {Release{ChannelOnly: true, Channel: "stable", BaselineChannel: "beta"}, "channel beta -> stable"},
		"channel_from": {Release{ChannelOnly: true, ChannelFrom: "core"}, "channel from core"},
		"direct":       {Release{OwnBump: ccme.BumpMinor}, "direct"},
		"propagated":   {Release{DueTo: []string{"core", "utils"}}, "propagated from core, utils"},
		"pinned":       {Release{Pinned: true}, "pinned"},
		"unchanged":    {Release{}, "unchanged"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.rel.Pkg = pkg
			assert.Equal(t, tc.want, tc.rel.Reason())
		})
	}
}

func TestPossiblyBehind(t *testing.T) {
	p := &Plan{
		Releases: map[string]*Release{
			"consumer":  {StableCommit: "c2"},
			"provider":  {StableCommit: "c9"},
			"untagged":  {},
			"unrelated": {StableCommit: "c2"},
		},
		ancestor: func(a, b string) bool { return a == b || (a == "c2" && b == "c9") },
	}

	assert.False(t, p.PossiblyBehind("ghost", "provider"), "unknown consumer")
	assert.False(t, p.PossiblyBehind("consumer", "untagged"), "a never-released provider owes nothing")
	assert.True(t, p.PossiblyBehind("untagged", "provider"), "never released while the provider has been")
	assert.True(t, p.PossiblyBehind("consumer", "provider"), "provider's tag is not an ancestor of the consumer's")
	assert.False(t, p.PossiblyBehind("provider", "unrelated"), "the ancestor relation clears it")

	p.ancestor = nil
	assert.False(t, p.PossiblyBehind("consumer", "provider"), "no ancestry available: no claim")
}

func TestNewerCommitOrdering(t *testing.T) {
	cp := &computation{byKey: map[string]*commitRec{
		"new": {rank: 0},
		"old": {rank: 5},
	}}
	assert.True(t, cp.newerCommit("new", "old"))
	assert.False(t, cp.newerCommit("old", "new"))
	assert.True(t, cp.newerCommit("new", ""), `"" is infinitely old`)
	assert.False(t, cp.newerCommit("", "old"))
	assert.True(t, cp.newerCommit("new", "unknown"), "a known commit beats an unknown one")
	assert.False(t, cp.newerCommit("unknown", "old"))
}

func TestMatchesFrom(t *testing.T) {
	assert.True(t, matchesFrom("beta", "beta"))
	assert.False(t, matchesFrom("rc", "beta"))
	assert.True(t, matchesFrom("beta", ccme.ChannelAnyPrerelease), `"*" matches any prerelease`)
	assert.False(t, matchesFrom(ccme.ChannelStable, ccme.ChannelAnyPrerelease), `"*" never matches stable`)
}

// ---------------------------------------------------------------------------
// ancestry fallbacks: parent-pointer BFS and history-rank order
// ---------------------------------------------------------------------------

// plainGit answers ancestry with gitx.ErrNoAncestry (via the embedded stub)
// so the planner has to fall back to the commits' parent pointers
// (stripParents=false) or to history order alone (stripParents=true).
type plainGit struct {
	gitx.NoAncestry
	inner        *fakeGit
	stripParents bool
}

func (g *plainGit) Tags(ctx context.Context, pkg string, f gitx.TagFormat) (gitx.Tags, error) {
	return g.inner.Tags(ctx, pkg, f)
}

func (g *plainGit) Commits(ctx context.Context, sinceTag string) ([]gitx.Commit, error) {
	cs, err := g.inner.Commits(ctx, sinceTag)
	if g.stripParents {
		for i := range cs {
			cs[i].Parents = nil
		}
	}
	return cs, err
}

func (g *plainGit) CreateTag(ctx context.Context, name, msg, target string) error {
	return g.inner.CreateTag(ctx, name, msg, target)
}

func (g *plainGit) IsShallow(ctx context.Context) (bool, error) { return g.inner.IsShallow(ctx) }

func TestCancellationWithoutAncestryChecker(t *testing.T) {
	// The cancel semantics — reaching backwards only — must hold under all
	// three ancestry sources: with the git-native check (every other test),
	// with parent pointers alone, and with nothing but history order.
	history := []commit{
		{sha: "c1", message: "feat(core): cancelled work"},
		{sha: "c2", message: "cancel(core): drop it"},
		{sha: "c3", message: "fix(core): lands after the cancel"},
	}
	for name, strip := range map[string]bool{"parent_pointers": false, "history_rank": true} {
		t.Run(name, func(t *testing.T) {
			git := &plainGit{inner: newFakeGit(history...).tag("core", "1.0.0", ""), stripParents: strip}
			p := compute(t, git, nil)

			core := p.Releases["core"]
			assert.Equal(t, ccme.BumpPatch, core.Bump, "only the post-cancel fix survives")
			assertVersion(t, v(1, 0, 1), core.Next)
		})
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"@acme/*", "@acme/ui", true},
		{"@acme/*", "@acme/deep/nested", true}, // "*" crosses "/" on purpose
		{"@acme/*", "@other/ui", false},
		{"*", "anything", true},
		{"*-utils", "core-utils", true},
		{"*-utils", "core-utilz", false},
		{"core*utils", "coreutils", true},
		{"core*utils", "core-more-utils", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXcYb", false},
		{"literal", "literal", true},
		{"literal", "literally", false}, // pattern exhausted before the input
		{"lit*", "lit", true},           // trailing star matches the empty run
		{"", "", true},
		{"", "x", false},
	} {
		assert.Equalf(t, tc.want, GlobMatch(tc.pattern, tc.s), "GlobMatch(%q, %q)", tc.pattern, tc.s)
	}
}

func TestReleaseTagFormatFallsBackToTheDefault(t *testing.T) {
	// A Release without a package (or space) still renders under the
	// normative default format rather than panicking — the shape hand-built
	// Releases in recorders and tests take.
	assert.Equal(t, gitx.DefaultTagFormat, (&Release{}).TagFormat())
	rel := &Release{Pkg: &model.Package{Name: "core", Space: &model.Space{TagFormat: "{name}@v{version}"}}}
	assert.Equal(t, gitx.TagFormat("{name}@v{version}"), rel.TagFormat())
}

func TestPlanAccessorsOnUnknownPackages(t *testing.T) {
	p := &Plan{
		Order: []string{"core", "ghost"},
		Releases: map[string]*Release{
			"core": {Pkg: &model.Package{Name: "core", Space: &model.Space{}},
				OwnBump: ccme.BumpMinor, Bump: ccme.BumpMinor, NewWork: true,
				Sources: []StaleSource{{Provider: "utils"}}},
		},
	}
	assert.Nil(t, p.StaleSources("ghost"), "an unknown package has no sources")
	assert.Equal(t, []StaleSource{{Provider: "utils"}}, p.StaleSources("core"))
	assert.Len(t, p.Releasing(), 1, "a nil release entry is skipped")
}

func TestComputeUsesTheConfiguredParser(t *testing.T) {
	// The parser options travel from Options.ParserConfig into unit parsing:
	// a custom type table gives `docs` a real bump, a custom separator splits
	// units where "---" no longer does, and a configured propagation depth
	// makes a plain feat reach consumers with no caret written.
	t.Run("custom_types_and_separator", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: "c1", message: "docs(core): now release-worthy\n%%%\nfeat(utils): standard type still works"},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "")
		types := ccme.DefaultTypes()
		types["docs"] = ccme.BumpPatch
		pkgs, deps := testPackages()
		p, err := Compute(context.Background(), git, Options{
			Packages: pkgs, Dependencies: deps, Root: "/r",
			ParserConfig: ccme.Config{Separator: "%%%", Types: types},
		})
		require.NoError(t, err)
		assertVersion(t, v(1, 0, 1), p.Releases["core"].Next, "docs bumps patch under the custom table")
		assertVersion(t, v(1, 1, 0), p.Releases["utils"].Next, "the second unit parsed via the custom separator")
	})

	t.Run("default_propagation_depth", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: "c1", message: "feat(core): no caret written"},
		).tag("core", "1.0.0", "").tag("app", "1.0.0", "")
		pkgs, deps := testPackages()
		p, err := Compute(context.Background(), git, Options{
			Packages: pkgs, Dependencies: deps, Root: "/r",
			ParserConfig: ccme.Config{Propagation: ccme.PropagationConfig{Depth: 1}},
		})
		require.NoError(t, err)
		assertVersion(t, v(1, 0, 1), p.Releases["app"].Next,
			"the configured default depth reaches the direct consumer without a caret")
		assert.Equal(t, []string{"core"}, p.Releases["app"].DueTo)
	})
}

func TestCommitKey(t *testing.T) {
	assert.Equal(t, "abc123", commitKey(gitx.Commit{SHA: "abc123", Message: "fix: x"}),
		"the SHA is the identity when present")
	assert.Equal(t, "msg:fix: x", commitKey(gitx.Commit{Message: "fix: x"}),
		"SHA-less implementations fall back to the message")
}

func TestKindSet(t *testing.T) {
	assert.Nil(t, kindSet(nil), "no list means every kind")
	assert.Nil(t, kindSet([]ccme.DependencyKind{ccme.KindDependencies, ccme.KindAll}),
		"'all' anywhere in the list widens to every kind")
	assert.Equal(t, map[model.DepKind]bool{
		model.KindDependencies:         true,
		model.KindDevDependencies:      true,
		model.KindPeerDependencies:     true,
		model.KindOptionalDependencies: true,
	}, kindSet([]ccme.DependencyKind{
		ccme.KindDependencies, ccme.KindDevDependencies,
		ccme.KindPeerDependencies, ccme.KindOptionalDependencies,
	}))
	assert.Equal(t, map[model.DepKind]bool{model.KindPeerDependencies: true},
		kindSet([]ccme.DependencyKind{ccme.KindPeerDependencies}))
}

func TestUnderDir(t *testing.T) {
	assert.True(t, underDir("r/libs/core/main.go", "r/libs/core"))
	assert.True(t, underDir("r/libs/core", "r/libs/core"), "the folder itself")
	assert.True(t, underDir("r/libs/core/x", "r/libs/core/"), "a trailing slash is cleaned")
	assert.False(t, underDir("r/libs/core-extra/x", "r/libs/core"),
		"a sibling sharing the prefix is outside")
	assert.True(t, underDir("anything/at/all", ""), "a root-dir package owns everything")
	assert.True(t, underDir("anything/at/all", "."), "same for the dot spelling")
}

func TestBaselineChannel(t *testing.T) {
	cp := &computation{rel: map[string]*Release{"core": {BaselineChannel: "beta"}}}
	assert.Equal(t, "beta", cp.baselineChannel("core"))
	assert.Equal(t, ccme.ChannelStable, cp.baselineChannel("unknown"),
		"a package outside the plan reads as stable")
}

func TestAncestryFailed(t *testing.T) {
	cp := &computation{}
	assert.NoError(t, cp.ancestryFailed())
	cp.ancErr = assert.AnError
	err := cp.ancestryFailed()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ancestry query failed")
}

func TestResolveChannelValue(t *testing.T) {
	stable, beta := ccme.ChannelStable, "beta"

	p := resolveChannelValue(ccme.ChannelValue{To: beta}, stable, "", false)
	assert.Equal(t, beta, p.target, "a plain channel proposes itself")

	p = resolveChannelValue(ccme.ChannelValue{To: beta}, beta, "", false)
	assert.Empty(t, p.target)
	assert.Equal(t, CodeChannelRedundant, p.code, "already there proposes nothing")

	p = resolveChannelValue(ccme.ChannelValue{To: stable}, beta, "", false)
	assert.Empty(t, p.target)
	assert.Equal(t, CodeChannelNoGraduate, p.code,
		"a propagated stable never graduates a train by accident")

	p = resolveChannelValue(ccme.ChannelValue{To: stable}, beta, "", true)
	assert.Equal(t, stable, p.target, "a direct directive does graduate")

	p = resolveChannelValue(ccme.ChannelValue{From: beta, To: stable}, beta, "", false)
	assert.Equal(t, stable, p.target, "a transition graduates even propagated")

	p = resolveChannelValue(ccme.ChannelValue{From: beta, To: stable}, "rc", "", false)
	assert.Equal(t, CodeTransitionUnmatched, p.code, "wrong train, no proposal")

	p = resolveChannelValue(ccme.ChannelValue{From: beta, To: beta}, beta, "", false)
	assert.Equal(t, CodeTransitionInert, p.code)

	p = resolveChannelValue(ccme.ChannelValue{Word: ccme.ChannelInherit}, stable, beta, false)
	assert.Equal(t, beta, p.target, "inherit resolves to the origin's channel")

	p = resolveChannelValue(ccme.ChannelValue{Word: ccme.ChannelNone}, stable, beta, false)
	assert.Equal(t, noProposal, p)

	p = resolveChannelValue(ccme.ChannelValue{Word: ccme.ChannelInherit}, stable, "", false)
	assert.Equal(t, noProposal, p, "inherit with no origin proposes nothing")
}

func TestOriginChannel(t *testing.T) {
	cp := &computation{rel: map[string]*Release{
		"a": {BaselineChannel: "beta"},
		"b": {BaselineChannel: ccme.ChannelStable},
	}}
	rec := &commitRec{key: "k"}
	u := &ccme.Unit{}

	assert.Equal(t, ccme.ChannelStable, cp.originChannel(u, nil, rec),
		"no sources reads as stable")
	assert.Equal(t, "beta", cp.originChannel(u, map[string]bool{"a": true}, rec),
		"one source: its baseline channel")

	got := cp.originChannel(u, map[string]bool{"a": true, "b": true}, rec)
	assert.Equal(t, "beta", got, "disagreeing sources resolve to the first by name")
	require.NotEmpty(t, cp.diags)
	assert.Equal(t, CodePropagatedChannelConflict, cp.diags[len(cp.diags)-1].Code)
}

// TestDerivedOwnershipHonoursSrc: §6.2 ownership is by longest matching path
// prefix over each package's scope folder, so a package declaring `src`
// stops owning what lies outside it — and a package nested inside another's
// src still wins its own files, because it is the longer prefix.
func TestDerivedOwnershipHonoursSrc(t *testing.T) {
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/packages/core", Src: "lib"},
		{Name: "inner", Dir: "/r/packages/core/lib/inner"},
		{Name: "plain", Dir: "/r/packages/plain"},
	}
	cp := &computation{root: "/r", pkgs: pkgs}

	for name, tc := range map[string]struct {
		files []string
		want  []string
	}{
		"a file inside src belongs to the package": {
			[]string{"packages/core/lib/parser.go"}, []string{"core"}},
		"a file outside src belongs to nobody": {
			[]string{"packages/core/docs/guide.md", "packages/core/README.md"}, nil},
		"the nested package still wins its own files": {
			[]string{"packages/core/lib/inner/main.go"}, []string{"inner"}},
		"a package without src owns its whole folder": {
			[]string{"packages/plain/docs/guide.md"}, []string{"plain"}},
		"a commit spanning both": {
			[]string{"packages/core/lib/parser.go", "packages/core/docs/guide.md",
				"packages/plain/x.go"}, []string{"core", "plain"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &commitRec{commit: gitx.Commit{Files: tc.files}}
			got := cp.derived(rec)
			assert.Len(t, got, len(tc.want))
			for _, want := range tc.want {
				assert.True(t, got[want], "%s should own one of %v", want, tc.files)
			}
			assert.NotNil(t, rec.derivedSet,
				"the memo is filled even when nothing is owned, so a second ask costs nothing")
		})
	}
}

// TestDerivedOwnershipHonoursIgnore: the owning package has the last word on
// its own files. A file its patterns exclude counts for nobody — it does not
// fall through to the package that encloses it, because the file belongs to
// the package that said it does not deserve a release.
func TestDerivedOwnershipHonoursIgnore(t *testing.T) {
	compile := func(patterns ...string) *ignore.Rules {
		t.Helper()
		r, err := ignore.Compile(patterns)
		require.NoError(t, err)
		return r
	}
	outer := ignore.Layer{Dir: "/r", Rules: compile("*.md")}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/packages/core", Ignore: ignore.Chain{outer,
			{Dir: "/r/packages/core", Rules: compile("testdata/", "!README.md")}}},
		{Name: "inner", Dir: "/r/packages/core/inner", Ignore: ignore.Chain{outer}},
		{Name: "plain", Dir: "/r/packages/plain"},
	}
	cp := &computation{root: "/r", pkgs: pkgs}

	for name, tc := range map[string]struct {
		files []string
		want  []string
	}{
		"an ignored file counts for nobody": {
			[]string{"packages/core/testdata/a.json"}, nil},
		"including one the enclosing package would otherwise own": {
			[]string{"packages/core/inner/notes.md"}, nil},
		"a package can re-include what the repository excluded": {
			[]string{"packages/core/README.md"}, []string{"core"}},
		"but only for itself": {
			[]string{"packages/core/inner/README.md"}, nil},
		"a package with no patterns is untouched": {
			[]string{"packages/plain/notes.md"}, []string{"plain"}},
		"and one ordinary file in the commit is enough": {
			[]string{"packages/core/testdata/a.json", "packages/core/main.go"}, []string{"core"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &commitRec{commit: gitx.Commit{Files: tc.files}}
			got := cp.derived(rec)
			assert.Len(t, got, len(tc.want))
			for _, want := range tc.want {
				assert.True(t, got[want], "%s should own one of %v", want, tc.files)
			}
		})
	}
}
