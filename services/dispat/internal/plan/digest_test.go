// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// What the plan digest is and is not sensitive to.
//
// The digest exists so that two machines can agree they are executing one
// release (CCME §28.3), which makes both halves of it load bearing: a change
// to what is released has to change it, and everything about how the release
// is placed, observed or timed must not. The tests below are written as those
// two lists.

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// digestFixture is the workspace the digest is measured on: two libraries in
// one space, an app consuming both over two edge kinds, a declared build
// output and one pending change.
func digestFixture(root string) ([]*model.Package, []model.Dependency) {
	libs := &model.Space{Name: "libs", BuildScript: []string{"make build"},
		BuildOutputs: []string{"dist"}, Env: []string{"REGISTRY=$DISPAT_REGISTRY"}}
	apps := &model.Space{Name: "apps", PublishScript: []string{"make publish"}}
	pkgs := []*model.Package{
		{Name: "core", Dir: root + "/libs/core", Space: libs},
		{Name: "utils", Dir: root + "/libs/utils", Space: libs},
		{Name: "app", Dir: root + "/apps/app", Space: apps},
	}
	deps := []model.Dependency{
		{Consumer: "app", Provider: "core"},
		{Consumer: "app", Provider: "utils", Kind: model.KindDevDependencies},
	}
	return pkgs, deps
}

func digestHistory() *fakeGit {
	return newFakeGit(
		commit{sha: "c1", message: "feat(core): initial"},
		commit{sha: "c2", message: "fix(core): a pending fix"},
	).tag("core", "1.0.0", "c1").tag("utils", "2.0.0", "c1").tag("app", "0.5.0", "c1")
}

// digestOf computes one plan from a fresh fake repository and digests it, so
// that two calls share no state at all: every map, every slice and every unit
// pointer is built again.
func digestOf(t *testing.T, root string, adjust func(*Options), heads map[string]string) string {
	t.Helper()
	pkgs, deps := digestFixture(root)
	options := Options{Packages: pkgs, Dependencies: deps, Root: root}
	if adjust != nil {
		adjust(&options)
	}
	computed, err := Compute(context.Background(), digestHistory(), options)
	require.NoError(t, err)
	digest, err := computed.CalculateDigest(DigestInput{Heads: heads, Options: options})
	require.NoError(t, err)
	return digest
}

func digestOfFixture(t *testing.T) string {
	t.Helper()
	return digestOf(t, "/r", nil, map[string]string{"": "0000000000000000000000000000000000000000"})
}

// TestPlanDigestIsStableAcrossRecomputation: the same repository at the same
// state digests identically however many times it is planned, which is the
// property every other use of the digest rests on. Two computations share no
// state, so a map iteration order that leaked into the document would show up
// here within a few runs.
func TestPlanDigestIsStableAcrossRecomputation(t *testing.T) {
	first := digestOfFixture(t)
	assert.Len(t, first, 64, "a digest is lowercase hex sha256")
	assert.Equal(t, strings.ToLower(first), first)
	for range 8 {
		assert.Equal(t, first, digestOfFixture(t))
	}
}

// TestPlanDigestGolden pins one tiny plan's digest.
//
// It is here to catch an accidental change to the canonical document: a field
// added, removed, renamed or reordered changes this value. Changing it on
// purpose is legitimate and requires bumping DigestSchema in the same commit,
// because a node running the older code has to disagree loudly rather than
// agree by accident.
func TestPlanDigestGolden(t *testing.T) {
	const golden = "baf5a78dd954f38145e63e01ecba246656aca9aa69987aef3c2b0763ec0c4259"
	assert.Equal(t, golden, digestOfFixture(t),
		"changing the canonical document requires bumping DigestSchema in the same commit")
}

// TestPlanDigestIgnoresWhatOnlyObservesTheRun: everything §17.2 says must not
// change the semantic plan, as far as a plan can see it. The execution object
// itself never reaches this package at all, which is the strongest form of
// the same rule; what can be varied here is varied.
func TestPlanDigestIgnoresWhatOnlyObservesTheRun(t *testing.T) {
	want := digestOfFixture(t)
	heads := map[string]string{"": "0000000000000000000000000000000000000000"}
	for name, adjust := range map[string]func(*Options){
		"a logger": func(o *Options) { o.Log = zerolog.New(nil).Level(zerolog.TraceLevel) },
		"workload counters": func(o *Options) {
			o.HistoryStats = &HistoryStats{}
		},
		"the run's own masked tags": func(o *Options) {
			o.IgnoredTags = []string{"core@1.0.1"}
			o.IgnoredTagsByRepository = map[string][]string{"": {"core@1.0.1"}}
		},
		"the order the edges were declared in": func(o *Options) {
			o.Dependencies = []model.Dependency{
				{Consumer: "app", Provider: "utils", Kind: model.KindDevDependencies},
				{Consumer: "app", Provider: "core"},
			}
		},
		"the order the packages were discovered in": func(o *Options) {
			o.Packages = []*model.Package{o.Packages[2], o.Packages[0], o.Packages[1]}
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, digestOf(t, "/r", adjust, heads))
		})
	}
}

// TestPlanDigestIgnoresTheCheckoutPath: two clones of one repository at
// different absolute paths digest equally. A worker checks a plan it was
// handed against the one it computes in its own checkout, and no two
// checkouts are at the same path.
func TestPlanDigestIgnoresTheCheckoutPath(t *testing.T) {
	heads := map[string]string{"": "0000000000000000000000000000000000000000"}
	assert.Equal(t, digestOf(t, "/r", nil, heads),
		digestOf(t, "/somewhere/else/checkout", nil, heads))
}

// TestPlanDigestIgnoresRunTimeOutputs: what a script exported is produced by
// the run rather than by planning, so it is not part of the plan the run was
// authorized to execute.
func TestPlanDigestIgnoresRunTimeOutputs(t *testing.T) {
	pkgs, deps := digestFixture("/r")
	options := Options{Packages: pkgs, Dependencies: deps, Root: "/r"}
	computed, err := Compute(context.Background(), digestHistory(), options)
	require.NoError(t, err)
	input := DigestInput{Options: options, Heads: map[string]string{"": "abc"}}

	before, err := computed.CalculateDigest(input)
	require.NoError(t, err)
	computed.Releases["core"].Outputs = []Output{{Name: "URL", Value: "https://registry.test/core", Source: "core:publish"}}
	after, err := computed.CalculateDigest(input)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// TestPlanDigestFollowsWhatIsReleased: every change that makes this a
// different release changes the digest. Each row states one such change, and
// they are asserted to be distinct from each other as well, because a digest
// that collapsed two different plans onto one value would be worse than one
// that changed too often.
func TestPlanDigestFollowsWhatIsReleased(t *testing.T) {
	base := digestOfFixture(t)
	heads := map[string]string{"": "0000000000000000000000000000000000000000"}
	seen := map[string]string{base: "the fixture"}
	for name, row := range map[string]struct {
		adjust func(*Options)
		heads  map[string]string
	}{
		"a new head": {nil, map[string]string{"": "1111111111111111111111111111111111111111"}},
		"a changed build command": {func(o *Options) {
			o.Packages[0].Space.BuildScript = []string{"make build --release"}
		}, heads},
		"a changed build output": {func(o *Options) {
			o.Packages[0].Space.BuildOutputs = []string{"build"}
		}, heads},
		"a changed static environment": {func(o *Options) {
			o.Packages[0].Space.Env = []string{"REGISTRY=$DISPAT_OTHER"}
		}, heads},
		"a changed edge kind": {func(o *Options) {
			o.Dependencies[1].Kind = model.KindPeerDependencies
		}, heads},
		"a changed tag format": {func(o *Options) {
			o.Packages[0].Space.TagFormat = "{name}@v{version}"
		}, heads},
		"a changed parser configuration": {func(o *Options) {
			o.ParserConfig = ccme.Config{Propagation: ccme.PropagationConfig{Depth: 1}}
		}, heads},
		"a package folder that moved": {func(o *Options) {
			o.Packages[0].Dir = "/r/libs/core-renamed"
		}, heads},
	} {
		t.Run(name, func(t *testing.T) {
			digest := digestOf(t, "/r", row.adjust, row.heads)
			assert.NotEqual(t, base, digest, "this change must move the digest")
			if previous, isSeen := seen[digest]; isSeen {
				t.Fatalf("two different plans digest alike: %s and %s", previous, name)
			}
			seen[digest] = name
		})
	}
}

// TestPlanDigestFollowsTheComputedVersions: the versions are the plan, so a
// plan that releases a different version is a different plan. The change is
// made in the repository rather than in the options, which is how a real one
// arrives.
func TestPlanDigestFollowsTheComputedVersions(t *testing.T) {
	heads := map[string]string{"": "0000000000000000000000000000000000000000"}
	pkgs, deps := digestFixture("/r")
	options := Options{Packages: pkgs, Dependencies: deps, Root: "/r"}
	breaking := newFakeGit(
		commit{sha: "c1", message: "feat(core): initial"},
		commit{sha: "c2", message: "feat(core)!: a pending break"},
	).tag("core", "1.0.0", "c1").tag("utils", "2.0.0", "c1").tag("app", "0.5.0", "c1")
	computed, err := Compute(context.Background(), breaking, options)
	require.NoError(t, err)
	assertVersion(t, v(2, 0, 0), computed.Releases["core"].Next)

	digest, err := computed.CalculateDigest(DigestInput{Options: options, Heads: heads})
	require.NoError(t, err)
	assert.NotEqual(t, digestOfFixture(t), digest)
}

// TestPlanDigestReadsTheHeadsPlanningRecorded: a composed plan carries its own
// repository-qualified head snapshot, and the caller's fallback is for the
// single history where planning leaves that map empty. The fallback must not
// be able to override what planning read.
func TestPlanDigestReadsTheHeadsPlanningRecorded(t *testing.T) {
	computed := &Plan{RepositoryHeads: map[string]string{"sdk": "aaaa", "app": "bbbb"}}
	planned, err := computed.CalculateDigest(DigestInput{Heads: map[string]string{"": "cccc"}})
	require.NoError(t, err)
	ignored, err := computed.CalculateDigest(DigestInput{Heads: map[string]string{"": "dddd"}})
	require.NoError(t, err)
	assert.Equal(t, planned, ignored, "a plan with heads of its own ignores the fallback")

	empty := &Plan{}
	supplied, err := empty.CalculateDigest(DigestInput{Heads: map[string]string{"": "cccc"}})
	require.NoError(t, err)
	assert.NotEqual(t, planned, supplied)
}
