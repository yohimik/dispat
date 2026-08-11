package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The partial shared-versioning tests: the modes that hold only a prefix of
// the version in common. Each one asserts the two halves of the promise —
// that a bump reaching the shared part moves the whole group, and that a
// smaller one moves nothing but its own package.

// partialPkgs builds a workspace of one shared-versioning space ("shared":
// a, b) under an explicit mode per package, so a group's members can disagree.
func partialPkgs(modeA, modeB model.Versioning) []*model.Package {
	return []*model.Package{
		{Name: "a", Dir: "/r/pkgs/a", Space: &model.Space{Name: "shared", Versioning: modeA}},
		{Name: "b", Dir: "/r/pkgs/b", Space: &model.Space{Name: "shared", Versioning: modeB}},
	}
}

func computePartial(t *testing.T, modeA, modeB model.Versioning, git *fakeGit) *Plan {
	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: partialPkgs(modeA, modeB), Root: "/r"})
	require.NoError(t, err)
	return p
}

// ---------------------------------------------------------------------------
// The prefix helpers
// ---------------------------------------------------------------------------

func TestSamePrefix(t *testing.T) {
	cases := []struct {
		name  string
		a, b  ccme.Version
		depth int
		want  bool
	}{
		{"same major, depth 1", v(1, 2, 3), v(1, 9, 0), 1, true},
		{"different major, depth 1", v(1, 2, 3), v(2, 0, 0), 1, false},
		{"same major, different minor, depth 2", v(1, 2, 3), v(1, 9, 0), 2, false},
		{"same major and minor, depth 2", v(1, 2, 3), v(1, 2, 9), 2, true},
		{"same major and minor, depth 3", v(1, 2, 3), v(1, 2, 9), 3, false},
		{"identical, depth 3", v(1, 2, 3), v(1, 2, 3), 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, samePrefix(c.a, c.b, c.depth))
			assert.Equal(t, c.want, samePrefix(c.b, c.a, c.depth), "the comparison is symmetric")
		})
	}
}

func TestSamePrefixIgnoresPrereleases(t *testing.T) {
	// A prerelease belongs to the line its core names: 2.0.0-beta.0 is on
	// major 2, which is what decides whether the group has moved.
	pre := ccme.Version{Major: 2, Prerelease: []string{"beta", "0"}}
	assert.True(t, samePrefix(pre, v(2, 3, 0), 1))
	assert.False(t, samePrefix(pre, v(1, 9, 0), 1))
	assert.True(t, samePrefix(pre, v(2, 0, 0), 3))
}

func TestGroupTarget(t *testing.T) {
	// The version a member adopts to join the group's shared prefix.
	assert.Equal(t, "2.0.0", groupTarget(v(2, 7, 3), 1).String(), "depth 1 zeroes minor and patch")
	assert.Equal(t, "2.7.0", groupTarget(v(2, 7, 3), 2).String(), "depth 2 zeroes the patch")
	assert.Equal(t, "2.7.3", groupTarget(v(2, 7, 3), 3).String(), "depth 3 is the baseline itself")
}

func TestGroupTargetJoinsATrainRatherThanPassingIt(t *testing.T) {
	// A prerelease baseline ranks below its own core, so zeroing the tail
	// would put a joining member ahead of a stable version the group has never
	// published. The baseline wins whenever it is the lower of the two.
	pre := ccme.Version{Major: 2, Prerelease: []string{"beta", "0"}}
	for depth := 1; depth <= 3; depth++ {
		assert.Equal(t, "2.0.0-beta.0", groupTarget(pre, depth).String(),
			"depth %d must join the train", depth)
	}
}

func TestSharedPartName(t *testing.T) {
	assert.Equal(t, "one major version", SharedPartName(1))
	assert.Equal(t, "one major and minor version", SharedPartName(2))
	assert.Equal(t, "one version", SharedPartName(3))
}

// ---------------------------------------------------------------------------
// fixedMajor: the major alone is shared
// ---------------------------------------------------------------------------

func TestFixedMajorPatchStaysWithItsPackage(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): a patch of a's own"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	a, b := p.Releases["a"], p.Releases["b"]
	require.True(t, a.Releasing())
	assertVersion(t, v(1, 2, 4), a.Next, "a's own patch line, not the group's highest baseline")
	assert.False(t, b.Releasing(), "a patch below the shared major moves nobody else")
	assertVersion(t, v(1, 9, 0), b.Next)
}

func TestFixedMajorMinorStaysWithItsPackage(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a minor of a's own"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assertVersion(t, v(1, 3, 0), p.Releases["a"].Next)
	assert.False(t, p.Releases["b"].Releasing(), "a minor is below the shared major")
}

func TestFixedMajorBreakingMovesTheWholeGroup(t *testing.T) {
	// The shared part moves, so the group versions as one: both members land
	// on the same next major, computed over the group's highest baseline.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)!: a breaking change"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	a, b := p.Releases["a"], p.Releases["b"]
	assertVersion(t, v(2, 0, 0), a.Next)
	assertVersion(t, v(2, 0, 0), b.Next, "the group shares one major")
	assert.True(t, b.FixedRide)
	assert.True(t, b.NoChanges())
	assert.False(t, a.FixedRide)

	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedAlign && d.Pkg == "b" {
			found = true
			assert.Contains(t, d.Message, "one major version",
				"the diagnostic must name what is actually shared")
		}
	}
	assert.True(t, found, "the ride must be explained by W210: %v", p.Diagnostics)
}

func TestFixedMajorSparseLeavesTheUnchangedMemberBehind(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)!: a breaking change"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajorSparse, git)

	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assert.False(t, p.Releases["b"].Releasing(), "sparse: b waits for a change of its own")
	assertVersion(t, v(1, 9, 0), p.Releases["b"].Next)
}

func TestFixedMajorSparseMemberJoinsTheSharedMajorWhenItChanges(t *testing.T) {
	// The other half of the sparse promise: b's next change does not continue
	// its old 1.x line, it starts b's 2.x line at the group's major.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(b): b's first change since the group moved"},
	).tag("a", "2.4.0", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajorSparse, git)

	b := p.Releases["b"]
	require.True(t, b.Releasing())
	assertVersion(t, v(2, 0, 0), b.Next, "b joins the shared major at the start of its own line")
	assert.False(t, b.FixedRide, "b has changes of its own; this is not a ride")
	assert.False(t, p.Releases["a"].Releasing())
}

func TestFixedMajorLaggardCatchesUp(t *testing.T) {
	// A non-sparse laggard with nothing pending — its ride failed in an
	// earlier run — is released at the group's shared major, and the aligned
	// group then converges.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)!: a released, b's ride missed"},
	).tag("a", "2.0.0", "c1").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	b := p.Releases["b"]
	require.True(t, b.Releasing(), "the laggard must catch up")
	assertVersion(t, v(2, 0, 0), b.Next)
	assert.True(t, b.FixedRide)

	git = newFakeGit(
		commit{sha: "c1", message: "feat(a)!: both released"},
	).tag("a", "2.0.0", "c1").tag("b", "2.0.0", "c1")
	p = computeFixed(t, model.VersioningFixedMajor, git)
	assert.Empty(t, p.Releasing(), "nothing left once every member shares the major")
}

func TestFixedMajorLaggardIsMeasuredOnTheMajorAlone(t *testing.T) {
	// The alignment must read the shared part and nothing more: b sits far
	// below a's version but on the same major, which is all fixedMajor asks
	// for, so b is left exactly where it is.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a released alone"},
	).tag("a", "1.9.0", "c1").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assert.Empty(t, p.Releasing(), "a shared major is already satisfied")
}

func TestFixedMajorSparseNeverAlignsLaggards(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)!: a released alone"},
	).tag("a", "2.0.0", "c1").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajorSparse, git)
	assert.Empty(t, p.Releasing())
}

// ---------------------------------------------------------------------------
// fixedMajorMinor: the major and minor are shared
// ---------------------------------------------------------------------------

func TestFixedMajorMinorPatchStaysWithItsPackage(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): a patch of a's own"},
	).tag("a", "1.2.3", "").tag("b", "1.2.0", "")
	p := computeFixed(t, model.VersioningFixedMajorMinor, git)

	assertVersion(t, v(1, 2, 4), p.Releases["a"].Next)
	assert.False(t, p.Releases["b"].Releasing(), "a patch is below the shared minor")
	assertVersion(t, v(1, 2, 0), p.Releases["b"].Next)
}

func TestFixedMajorMinorMinorMovesTheWholeGroup(t *testing.T) {
	// The case that separates the two depths: a plain feat moves nothing but
	// its own package under fixedMajor, and the whole group under
	// fixedMajorMinor.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a new minor"},
	).tag("a", "1.2.3", "").tag("b", "1.2.0", "")
	p := computeFixed(t, model.VersioningFixedMajorMinor, git)

	a, b := p.Releases["a"], p.Releases["b"]
	assertVersion(t, v(1, 3, 0), a.Next)
	assertVersion(t, v(1, 3, 0), b.Next, "the group shares the minor")
	assert.True(t, b.FixedRide)

	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedAlign && d.Pkg == "b" {
			assert.Contains(t, d.Message, "one major and minor version")
		}
	}
}

func TestFixedMajorMinorBreakingMovesTheWholeGroup(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)!: a breaking change"},
	).tag("a", "1.2.3", "").tag("b", "1.2.0", "")
	p := computeFixed(t, model.VersioningFixedMajorMinor, git)

	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next)
}

func TestFixedMajorMinorSparseMemberJoinsTheSharedPrefix(t *testing.T) {
	// b stayed behind at 1.2.0 while the group moved to 1.3.x; its own patch
	// starts b's 1.3 line rather than continuing its old one.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(b): b's first change since the group moved"},
	).tag("a", "1.3.4", "").tag("b", "1.2.0", "")
	p := computeFixed(t, model.VersioningFixedMajorMinorSparse, git)

	b := p.Releases["b"]
	require.True(t, b.Releasing())
	assertVersion(t, v(1, 3, 0), b.Next)
	assert.False(t, b.FixedRide)
}

// ---------------------------------------------------------------------------
// Trains and pins: shared above the prefix, the package's own below it
// ---------------------------------------------------------------------------

func TestFixedMajorSharedTrainAndGraduation(t *testing.T) {
	// A breaking change on a channel takes the whole group onto one train,
	// further work continues that one train for both, and one member's
	// graduation ends it for both — the train belongs to the shared major.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)%beta!: start the shared train"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)
	assert.Equal(t, "2.0.0-beta.0", p.Releases["a"].Next.String())
	assert.Equal(t, "2.0.0-beta.0", p.Releases["b"].Next.String(), "the ride joins the shared train")

	// The train once published: the fake's history is oldest first, so c1 is
	// the breaking change beta.0 carries and c2 the work arriving on top.
	onTheTrain := func(second string) *fakeGit {
		return newFakeGit(
			commit{sha: "c1", message: "feat(a)!: the breaking change"},
			commit{sha: "c2", message: second},
		).tag("a", "1.2.3", "").tag("a", "2.0.0-beta.0", "c1").
			tag("b", "1.9.0", "").tag("b", "2.0.0-beta.0", "c1")
	}

	p = computeFixed(t, model.VersioningFixedMajor, onTheTrain("fix(a): more work on the train"))
	assert.Equal(t, "2.0.0-beta.1", p.Releases["a"].Next.String())
	assert.Equal(t, "2.0.0-beta.1", p.Releases["b"].Next.String(), "one train for the group")

	p = computeFixed(t, model.VersioningFixedMajor, onTheTrain("release(a)%beta>stable: graduate"))
	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next, "the graduation ends the train for the group")
}

func TestFixedMajorPatchTrainStaysLocal(t *testing.T) {
	// The mirror image: a train below the shared major is one package's own,
	// exactly as its versions are.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a)%beta: a's own train"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assert.Equal(t, "1.2.4-beta.0", p.Releases["a"].Next.String())
	assert.False(t, p.Releases["b"].Releasing(), "b is not on a's train")
	assert.Equal(t, ccme.ChannelStable, p.Releases["b"].Channel)
}

func TestFixedMajorPinCrossingTheMajorMovesTheGroup(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): to the next major\n\nRelease-As: 2.0.0\n"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next, "the pin names the group's shared major")
	assert.True(t, p.Releases["b"].FixedRide)
}

func TestFixedMajorPinBelowTheMajorMovesOnlyItsPackage(t *testing.T) {
	// A pin that stays on the group's major asks for nothing shared. It must
	// apply to its own package alone, and it must not be answered against the
	// group aggregate — a pin below the group's highest baseline would fail
	// E153 there while being perfectly valid for the package that wrote it.
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): a's own line\n\nRelease-As: 1.5.0\n"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assert.False(t, p.HasErrors(), "the group's guards must not answer a member's pin: %v", p.Diagnostics)
	assertVersion(t, v(1, 5, 0), p.Releases["a"].Next)
	assert.True(t, p.Releases["a"].Pinned)
	assert.False(t, p.Releases["b"].Releasing())
}

// ---------------------------------------------------------------------------
// Groups whose members declare different depths
// ---------------------------------------------------------------------------

func TestMixedDepthGroupVersionsAtTheDeepest(t *testing.T) {
	// A fixedMajor package and a fixedMajorMinor package in one group: the
	// deeper declaration wins, because holding the major and minor equal also
	// holds the major equal, and W213 explains the sharing the shallower
	// member never asked for.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a minor"},
	).tag("a", "1.2.0", "").tag("b", "1.2.0", "")
	p := computePartial(t, model.VersioningFixedMajor, model.VersioningFixedMajorMinor, git)

	assertVersion(t, v(1, 3, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 3, 0), p.Releases["b"].Next, "the minor is shared at the deepest depth")

	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedDepthConflict {
			found = true
			assert.Equal(t, "group:shared", d.Pkg, "the conflict is the group's")
			assert.Contains(t, d.Message, "one major and minor version")
		}
	}
	assert.True(t, found, "the mixed depth must warn: %v", p.Diagnostics)
}

func TestSparsenessAloneIsNotADepthConflict(t *testing.T) {
	// fixed and fixedSparse share the same depth and differ only in
	// assignment, so mixing them stays silent — as it always has.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a minor"},
	).tag("a", "1.2.0", "").tag("b", "1.2.0", "")
	p := computePartial(t, model.VersioningFixed, model.VersioningFixedSparse, git)

	for _, d := range p.Diagnostics {
		assert.NotEqual(t, CodeFixedDepthConflict, d.Code, "same depth, no conflict: %v", d)
	}
	assert.False(t, p.Releases["b"].Releasing(), "the sparse member still stays behind")
}

// ---------------------------------------------------------------------------
// Interaction with the rest of the planner
// ---------------------------------------------------------------------------

func TestFixedMajorHoldWithholdsTheGroupVersion(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c2", message: "release(b): keep b back\n\nRelease-As: none\n"},
		commit{sha: "c1", message: "feat(a)!: moves the group"},
	).tag("a", "1.2.3", "").tag("b", "1.9.0", "")
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assert.True(t, p.Releases["b"].Held)
	assert.False(t, p.Releases["b"].Releasing())
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next, "the hold withholds the group version")
	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
}

func TestFixedMajorRideFromPropagatedBreakingBump(t *testing.T) {
	// A propagated bump reaching the shared depth carries the group, and the
	// ride owes nothing to the provider: only the member with the edge picks
	// up the provider update.
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixedMajor}
	solo := &model.Space{Name: "solo"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/solo/core", Space: solo},
		{Name: "a", Dir: "/r/pkgs/a", Space: shared},
		{Name: "b", Dir: "/r/pkgs/b", Space: shared},
	}
	deps := []model.Dependency{{Consumer: "a", Provider: "core"}}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^major!: reaches a"},
	).tag("core", "1.0.0", "").tag("a", "1.2.0", "").tag("b", "1.5.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next, "b rides to the shared major")
	assert.True(t, p.Releases["b"].FixedRide)
	assert.Empty(t, p.Releases["b"].DueTo, "the ride owes nothing to core")
}

func TestFixedMajorGroupSpansSpaces(t *testing.T) {
	left := &model.Space{Name: "left", Versioning: model.VersioningFixedMajor, VersionGroup: "core"}
	right := &model.Space{Name: "right", Versioning: model.VersioningFixedMajor, VersionGroup: "core"}
	solo := &model.Space{Name: "solo"}
	pkgs := []*model.Package{
		{Name: "l1", Dir: "/r/l/l1", Space: left},
		{Name: "r1", Dir: "/r/r/r1", Space: right},
		{Name: "c", Dir: "/r/s/c", Space: solo},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(l1)!: moves the whole group"},
	).tag("l1", "1.0.0", "").tag("r1", "1.4.0", "").tag("c", "1.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(2, 0, 0), p.Releases["l1"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["r1"].Next, "one major across both spaces")
	assert.False(t, p.Releases["c"].Releasing())
}

func TestPartialGroupNeverPublishedVersionsIndependently(t *testing.T) {
	// With no baseline anywhere there is no shared prefix to hold: a minor
	// under fixedMajor is still one package's own, and nothing aligns.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): the first release"},
	)
	p := computeFixed(t, model.VersioningFixedMajor, git)

	assertVersion(t, v(0, 1, 0), p.Releases["a"].Next)
	assert.False(t, p.Releases["b"].Releasing(), "b has nothing to align to")
}

// TestDocumentedWorkedExample replays the table on the "Shared versions"
// documentation page commit for commit: a group of core and ui starting at
// 1.4.2 and 1.4.0, where only core ever changes. The page is what a reader
// plans their repository against, so it is worth a fence of its own.
func TestDocumentedWorkedExample(t *testing.T) {
	history := []struct {
		sha, message string
		// want is the pair each mode ends the step at, "" meaning unchanged.
		fixed, majorMinor, major [2]string
	}{
		{"c1", "fix(core): a patch",
			[2]string{"1.4.3", "1.4.3"}, [2]string{"1.4.3", ""}, [2]string{"1.4.3", ""}},
		{"c2", "fix(core): another patch",
			[2]string{"1.4.4", "1.4.4"}, [2]string{"1.4.4", ""}, [2]string{"1.4.4", ""}},
		{"c3", "feat(core): a feature",
			[2]string{"1.5.0", "1.5.0"}, [2]string{"1.5.0", "1.5.0"}, [2]string{"1.5.0", ""}},
		{"c4", "feat(core)!: a breaking change",
			[2]string{"2.0.0", "2.0.0"}, [2]string{"2.0.0", "2.0.0"}, [2]string{"2.0.0", "2.0.0"}},
	}
	modes := []struct {
		mode model.Versioning
		col  func(int) [2]string
	}{
		{model.VersioningFixed, func(i int) [2]string { return history[i].fixed }},
		{model.VersioningFixedMajorMinor, func(i int) [2]string { return history[i].majorMinor }},
		{model.VersioningFixedMajor, func(i int) [2]string { return history[i].major }},
	}

	for _, m := range modes {
		t.Run(string(m.mode), func(t *testing.T) {
			// Each step replays the whole history with the tags the previous
			// steps produced, which is what a repository actually looks like.
			at := [2]string{"1.4.2", "1.4.0"}
			for i := range history {
				hist := make([]commit, 0, i+1)
				for _, h := range history[:i+1] {
					hist = append(hist, commit{sha: h.sha, message: h.message})
				}
				prev := "" // the first step's tags predate all recorded history
				if i > 0 {
					prev = history[i-1].sha
				}
				git := newFakeGit(hist...).tag("core", at[0], prev).tag("ui", at[1], prev)
				pkgs := []*model.Package{
					{Name: "core", Dir: "/r/pkgs/core", Space: &model.Space{Name: "g", Versioning: m.mode}},
					{Name: "ui", Dir: "/r/pkgs/ui", Space: &model.Space{Name: "g", Versioning: m.mode}},
				}
				p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
				require.NoError(t, err)

				want := m.col(i)
				for k, name := range []string{"core", "ui"} {
					rel := p.Releases[name]
					if want[k] == "" {
						assert.Falsef(t, rel.Releasing(), "step %d: %s must not release", i+1, name)
						continue
					}
					require.Truef(t, rel.Releasing(), "step %d: %s must release", i+1, name)
					assert.Equalf(t, want[k], rel.Next.String(), "step %d: %s", i+1, name)
					at[k] = want[k]
				}
			}
		})
	}
}

func TestPartialModesNeverReleaseBelowTheirOwnBaseline(t *testing.T) {
	// The invariant that outranks every rule above: whichever path a member
	// takes, its next version must exceed what it last published.
	modes := []model.Versioning{
		model.VersioningFixedMajor, model.VersioningFixedMajorSparse,
		model.VersioningFixedMajorMinor, model.VersioningFixedMajorMinorSparse,
	}
	messages := []string{
		"fix(a): a patch", "feat(a): a minor", "feat(a)!: a break",
		"fix(b): the other side", "feat(b): the other side",
	}
	for _, mode := range modes {
		for _, msg := range messages {
			git := newFakeGit(commit{sha: "c1", message: msg}).
				tag("a", "1.2.3", "").tag("b", "1.9.7", "")
			p := computeFixed(t, mode, git)
			for _, name := range []string{"a", "b"} {
				rel := p.Releases[name]
				if !rel.Releasing() {
					continue
				}
				assert.Truef(t, versionLess(rel.Baseline, rel.Next),
					"%s/%s: %s released %s over baseline %s", mode, msg, name, rel.Next, rel.Baseline)
			}
		}
	}
}
