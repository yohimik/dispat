package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The fixed-versioning group tests. Every fixture uses one shared space in a
// fixed or fixedSparse mode plus, where the interaction matters, an
// independent space next to it, and asserts on the property the mode
// promises: one shared version for fixed, aligned-only-when-changed for
// fixedSparse, and no bleed into independent spaces.

// fixedPkgs builds a workspace of one shared-versioning space ("shared":
// a, b) and one independent space ("solo": c).
func fixedPkgs(mode model.Versioning) []*model.Package {
	shared := &model.Space{Name: "shared", Versioning: mode}
	solo := &model.Space{Name: "solo"}
	return []*model.Package{
		{Name: "a", Dir: "/r/pkgs/a", Space: shared},
		{Name: "b", Dir: "/r/pkgs/b", Space: shared},
		{Name: "c", Dir: "/r/solo/c", Space: solo},
	}
}

func computeFixed(t *testing.T, mode model.Versioning, git *fakeGit) *Plan {
	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: fixedPkgs(mode), Root: "/r"})
	require.NoError(t, err)
	return p
}

func TestRideOnATrainWithHistoryIsStillNoChanges(t *testing.T) {
	// b rode the train's earlier prerelease carrying its own feat; the fresh
	// cause of the next prerelease is a's work alone. Units spans the train,
	// but the entry renders the fresh changeset, and the ride line with it:
	// an rc that adds nothing of b's own must say "no changes", not render
	// an empty body.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(b)%beta!: b's own start"},
		commit{sha: "c2", message: "feat(a): a continues the train"},
	).tag("a", "1.0.0-beta.0", "c1").tag("b", "1.0.0-beta.0", "c1")

	p := computeFixed(t, model.VersioningFixed, git)

	b := p.Releases["b"]
	require.True(t, b.Releasing(), "fixed: b rides the train")
	assert.True(t, b.FixedRide)
	assert.NotEmpty(t, b.Units, "the train history is still counted")
	assert.Empty(t, b.NotesUnits(), "nothing fresh of b's own")
	assert.True(t, b.NoChanges(), "an empty fresh changeset is a no-changes ride")
}

func TestFixedChangeReleasesWholeSpace(t *testing.T) {
	// One feat scoped to a alone releases a AND b at one shared version; the
	// independent c is untouched. b's ride is labelled (W234) and renders a
	// "no changes" entry (NoChanges), because nothing in its own history
	// explains its presence in the plan.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): only a changes"},
	)
	p := computeFixed(t, model.VersioningFixed, git)

	a, b, c := p.Releases["a"], p.Releases["b"], p.Releases["c"]
	require.True(t, a.Releasing())
	require.True(t, b.Releasing(), "fixed: b must ride along")
	assertVersion(t, v(0, 1, 0), a.Next)
	assertVersion(t, v(0, 1, 0), b.Next, "one shared version for the space")
	assert.True(t, b.FixedRide)
	assert.True(t, b.NoChanges(), "the ride carries no content of its own")
	assert.False(t, a.FixedRide, "the package with the change is an ordinary release")
	assert.False(t, a.NoChanges())
	assert.Equal(t, "fixed group versioning", b.Reason())

	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedAlign && d.Pkg == "b" {
			found = true
		}
	}
	assert.True(t, found, "the ride must be explained by W234: %v", p.Diagnostics)

	assert.False(t, c.Releasing(), "an independent space must not be dragged along")
}

func TestFixedSharedVersionIsMaxOfMembers(t *testing.T) {
	// Heterogeneous baselines (fixed adopted mid-life): the shared version is
	// computed over the space's highest baseline, so the lower member jumps
	// up to align rather than the higher one going backwards.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): patch on the lower member"},
	).tag("a", "1.0.0", "").tag("b", "2.3.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	assertVersion(t, v(2, 3, 1), p.Releases["a"].Next, "a jumps to the space version")
	assertVersion(t, v(2, 3, 1), p.Releases["b"].Next, "b rides at the same version")
	assert.Equal(t, ccme.BumpPatch, p.Releases["a"].Bump)
}

func TestFixedSparseUnchangedMemberKeepsItsVersion(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): only a changes"},
	).tag("a", "1.0.0", "").tag("b", "2.3.0", "")
	p := computeFixed(t, model.VersioningFixedSparse, git)

	a, b := p.Releases["a"], p.Releases["b"]
	require.True(t, a.Releasing())
	assertVersion(t, v(2, 3, 1), a.Next, "the changed member releases at the shared version")
	assert.False(t, b.Releasing(), "fixedSparse: an unchanged member stays put")
	assertVersion(t, v(2, 3, 0), b.Next, "at its previous version")
	assert.False(t, b.FixedRide)
}

func TestFixedSparseBothChangedShareOneVersion(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a moves"},
		commit{sha: "c2", message: "fix(b): b moves too"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixedSparse, git)

	// The group bump is the max over members: a's minor beats b's patch, and
	// both land on the same next version.
	assertVersion(t, v(1, 1, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["b"].Next)
}

func TestFixedSingleSharedPrereleaseTrain(t *testing.T) {
	// A channel directive on one member moves the whole fixed space onto one
	// train: the space is one version, so it is one train too.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a)%beta: start the train"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	a, b := p.Releases["a"], p.Releases["b"]
	assert.Equal(t, "1.1.0-beta.0", a.Next.String())
	assert.Equal(t, "1.1.0-beta.0", b.Next.String(), "the ride joins the shared train")
	assert.Equal(t, "beta", b.Channel)
}

func TestFixedExactPinPinsTheSpace(t *testing.T) {
	// An exact Release-As naming one member pins the space's single version:
	// there is nothing narrower for it to name.
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): align\n\nRelease-As: 2.0.0\n"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next)
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next)
	assert.True(t, p.Releases["a"].Pinned)
	assert.True(t, p.Releases["b"].Pinned)
}

func TestFixedHeldMemberStaysBehind(t *testing.T) {
	// A hold on one member keeps that member (and only it) out of the
	// release; the rest of the space still moves. The held member's reported
	// would-be version is the group version it will catch up to.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): work\n---\nrelease(b): hold b\n\nRelease-As: none\n"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	a, b := p.Releases["a"], p.Releases["b"]
	require.True(t, a.Releasing())
	assertVersion(t, v(1, 1, 0), a.Next)
	assert.True(t, b.Held)
	assert.False(t, b.Releasing(), "a held member must not be released")
	assertVersion(t, v(1, 1, 0), b.Next, "the hold withholds the group version")
}

func TestFixedConvergesWhenNothingPending(t *testing.T) {
	// Both members tagged at the shared version with no commits after the
	// tags: nothing releases, so a fixed space converges exactly like an
	// independent one (G6 still holds under the group computation).
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): released work"},
	).tag("a", "0.1.0", "c1").tag("b", "0.1.0", "c1")
	p := computeFixed(t, model.VersioningFixed, git)

	assert.False(t, p.Releases["a"].Releasing())
	assert.False(t, p.Releases["b"].Releasing())
	assert.Empty(t, p.Releasing())
}

func TestFixedRideIsNotACatchUp(t *testing.T) {
	// A ride must be reported as W234, never as W193: it has no provider it
	// is behind, and labelling it a catch-up would tell the operator to look
	// for one.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): only a changes"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	require.True(t, p.Releases["b"].Releasing())
	assert.False(t, p.Releases["b"].CatchUp)
	for _, d := range p.Diagnostics {
		assert.NotEqual(t, CodeCatchUp, d.Code, "no catch-up in a pure fixed ride: %v", d)
	}
}

func TestTwoFixedSpacesVersionSeparately(t *testing.T) {
	// Two fixed spaces are two groups: a change in one moves every package of
	// that space and none of the other.
	left := &model.Space{Name: "left", Versioning: model.VersioningFixed}
	right := &model.Space{Name: "right", Versioning: model.VersioningFixed}
	pkgs := []*model.Package{
		{Name: "l1", Dir: "/r/l/l1", Space: left},
		{Name: "l2", Dir: "/r/l/l2", Space: left},
		{Name: "r1", Dir: "/r/r/r1", Space: right},
		{Name: "r2", Dir: "/r/r/r2", Space: right},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(l1): left moves"},
	).tag("l1", "1.0.0", "").tag("l2", "1.0.0", "").tag("r1", "3.0.0", "").tag("r2", "3.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(1, 1, 0), p.Releases["l1"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["l2"].Next)
	assert.False(t, p.Releases["r1"].Releasing())
	assert.False(t, p.Releases["r2"].Releasing())
}

func TestFixedGroupSpansSpaces(t *testing.T) {
	// Two spaces joined to one declared group version as one: a change in
	// either space moves every member of the group, across both spaces, while
	// an independent bystander stays put.
	left := &model.Space{Name: "left", Versioning: model.VersioningFixed, VersionGroup: "core"}
	right := &model.Space{Name: "right", Versioning: model.VersioningFixed, VersionGroup: "core"}
	solo := &model.Space{Name: "solo"}
	pkgs := []*model.Package{
		{Name: "l1", Dir: "/r/l/l1", Space: left},
		{Name: "r1", Dir: "/r/r/r1", Space: right},
		{Name: "c", Dir: "/r/s/c", Space: solo},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(l1): moves the whole group"},
	).tag("l1", "1.0.0", "").tag("r1", "1.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(1, 1, 0), p.Releases["l1"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["r1"].Next, "one version across both spaces")
	assert.True(t, p.Releases["r1"].FixedRide)
	assert.False(t, p.Releases["c"].Releasing())
}

func TestFixedGroupMixedModes(t *testing.T) {
	// A group can mix assignment modes when a member's own configuration says
	// so: the fixed member rides to the shared version, the fixedSparse member
	// with no changes of its own stays at its previous version — the shared
	// computation is one, the assignment is per member.
	fixed := &model.Space{Name: "left", Versioning: model.VersioningFixed, VersionGroup: "core"}
	sparse := &model.Space{Name: "right", Versioning: model.VersioningFixedSparse, VersionGroup: "core"}
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/l/a", Space: fixed},
		{Name: "b", Dir: "/r/l/b", Space: fixed},
		{Name: "s", Dir: "/r/r/s", Space: sparse},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): only a changes"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "").tag("s", "1.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(1, 1, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["b"].Next, "the fixed sibling rides")
	assert.True(t, p.Releases["b"].FixedRide)
	assert.False(t, p.Releases["s"].Releasing(), "the sparse member stays behind")
	assertVersion(t, v(1, 0, 0), p.Releases["s"].Next)
}

func TestFixedGroupMixedModeLaggards(t *testing.T) {
	// The laggard alignment is per member too: with nothing pending, a fixed
	// member below the group's published baseline catches up while a sparse
	// member in the same group is left exactly where fixedSparse promises to
	// leave it.
	fixed := &model.Space{Name: "left", Versioning: model.VersioningFixed, VersionGroup: "core"}
	sparse := &model.Space{Name: "right", Versioning: model.VersioningFixedSparse, VersionGroup: "core"}
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/l/a", Space: fixed},
		{Name: "b", Dir: "/r/l/b", Space: fixed},
		{Name: "s", Dir: "/r/r/s", Space: sparse},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): released work"},
	).tag("a", "0.1.0", "c1")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assert.False(t, p.Releases["a"].Releasing(), "a already published")
	require.True(t, p.Releases["b"].Releasing(), "the fixed laggard catches up")
	assertVersion(t, v(0, 1, 0), p.Releases["b"].Next)
	assert.False(t, p.Releases["s"].Releasing(), "the sparse member never aligns")
}

func TestFixedGroupNeverPublishedHasNothingToAlign(t *testing.T) {
	// A group with no published baseline and nothing pending: there is no
	// version to align laggards to, and nothing releases.
	git := newFakeGit(
		commit{sha: "c1", message: "docs: no package addressed"},
	)
	p := computeFixed(t, model.VersioningFixed, git)
	assert.Empty(t, p.Releasing())
}

func TestFixedGroupHeldLaggardStaysHeld(t *testing.T) {
	// The alignment catch-up must not override a hold: the held laggard
	// stays behind even though the group's published baseline is ahead.
	git := newFakeGit(
		commit{sha: "c2", message: "release(b): keep b back\n\nRelease-As: none\n"},
		commit{sha: "c1", message: "feat(a): released work"},
	).tag("a", "0.1.0", "c1")
	p := computeFixed(t, model.VersioningFixed, git)

	assert.False(t, p.Releases["a"].Releasing(), "a already published")
	assert.True(t, p.Releases["b"].Held)
	assert.False(t, p.Releases["b"].Releasing(), "a hold beats the alignment")
}

func TestFixedGroupRepositoryScopedPinErrorAbortsTheGroup(t *testing.T) {
	// A group pin that violates a repository-scoped guard (E157: the major
	// raised past the limit) leaves every member untouched — no correct plan
	// exists for the group, so no partial version assignment may leak out.
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): way too high\n\nRelease-As: 9.0.0\n"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	assert.True(t, p.HasErrors())
	assert.False(t, p.Releases["a"].Releasing())
	assert.False(t, p.Releases["b"].Releasing())
	assertVersion(t, v(1, 0, 0), p.Releases["a"].Next, "reporting stays at the baseline")
}

func TestFixedGroupDiagnosticsNameGroup(t *testing.T) {
	// Competing pins inside one group warn as W235 against the group's
	// synthetic package name, so the diagnostic names what actually holds the
	// single version.
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): pin low\n\nRelease-As: 1.5.0\n"},
		commit{sha: "c2", message: "release(b): pin high\n\nRelease-As: 2.0.0\n"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedPinConflict {
			found = true
			assert.Equal(t, "group:shared", d.Pkg, "the conflict is the group's, not a member's")
		}
	}
	require.True(t, found, "competing pins must warn: %v", p.Diagnostics)
	assertVersion(t, v(2, 0, 0), p.Releases["a"].Next, "the newest pin wins")
	assertVersion(t, v(2, 0, 0), p.Releases["b"].Next)
}

func TestPackageOptsOutOfFixedSpace(t *testing.T) {
	// A package override resolves to a derived Space with independent
	// versioning under the same space name — discovery's clone — and the
	// planner keeps it out of the group: the space's other members still
	// version together without it.
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixed}
	optOut := &model.Space{Name: "shared", Versioning: model.VersioningIndependent}
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/pkgs/a", Space: shared},
		{Name: "b", Dir: "/r/pkgs/b", Space: shared},
		{Name: "o", Dir: "/r/pkgs/o", Space: optOut},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): moves the group"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "").tag("o", "5.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(1, 1, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["b"].Next)
	assert.False(t, p.Releases["o"].Releasing(), "the opted-out package is independent")
	assertVersion(t, v(5, 0, 0), p.Releases["o"].Next)
}

func TestFixedRideFromPropagatedBump(t *testing.T) {
	// A dependency edge from an independent provider into one member of a
	// fixed space: the caret bumps the consumer, and the consumer's space
	// mates ride along at the same shared version.
	shared := &model.Space{Name: "shared", Versioning: model.VersioningFixed}
	solo := &model.Space{Name: "solo"}
	pkgs := []*model.Package{
		{Name: "core", Dir: "/r/solo/core", Space: solo},
		{Name: "a", Dir: "/r/pkgs/a", Space: shared},
		{Name: "b", Dir: "/r/pkgs/b", Space: shared},
	}
	deps := []model.Dependency{{Consumer: "a", Provider: "core"}}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: reaches a"},
	).tag("core", "1.0.0", "").tag("a", "0.2.0", "").tag("b", "0.2.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)

	assertVersion(t, v(1, 1, 0), p.Releases["core"].Next)
	assertVersion(t, v(0, 2, 1), p.Releases["a"].Next, "a picks up the propagated patch")
	assert.Equal(t, []string{"core"}, p.Releases["a"].DueTo)
	assertVersion(t, v(0, 2, 1), p.Releases["b"].Next, "b rides to the shared version")
	assert.True(t, p.Releases["b"].FixedRide)
	assert.Empty(t, p.Releases["b"].DueTo, "the ride owes nothing to core")
}

func TestFixedLaggardMemberCatchesUpToSpaceBaseline(t *testing.T) {
	// A member left behind — its ride failed in an earlier run, or the space
	// adopted fixed mid-life — has nothing pending of its own, yet the space
	// promises one version for all. It must release at the space's published
	// baseline, and converge once aligned.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): work a released but b's ride missed"},
	).tag("a", "0.1.0", "c1")
	p := computeFixed(t, model.VersioningFixed, git)

	a, b := p.Releases["a"], p.Releases["b"]
	assert.False(t, a.Releasing(), "a already published the work")
	require.True(t, b.Releasing(), "the laggard must catch up")
	assertVersion(t, v(0, 1, 0), b.Next, "at exactly the space's published version")
	assert.True(t, b.FixedRide)

	// Aligned: the same history with b's tag in place converges.
	git = newFakeGit(
		commit{sha: "c1", message: "feat(a): work both released"},
	).tag("a", "0.1.0", "c1").tag("b", "0.1.0", "c1")
	p = computeFixed(t, model.VersioningFixed, git)
	assert.Empty(t, p.Releasing(), "nothing left once every member is aligned")
}

func TestFixedSparseNeverAlignsLaggards(t *testing.T) {
	// The same laggard shape under fixedSparse: staying behind is the mode's
	// whole point, so nothing releases.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): a released alone"},
	).tag("a", "0.1.0", "c1")
	p := computeFixed(t, model.VersioningFixedSparse, git)
	assert.Empty(t, p.Releasing())
}

// ---------------------------------------------------------------------------
// Joining a group: with no version at all, and with the wrong one (W233)
// ---------------------------------------------------------------------------

func TestFixedNewcomerWithNoVersionJoinsAtTheGroupVersion(t *testing.T) {
	// The ordinary way a package joins an established group: a folder with no
	// tag of its own. It has no baseline to disagree with anybody about, so it
	// simply rides to whatever the group computes, and W233 stays quiet —
	// there is no spread here, only a newcomer.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): tweak"},
	).tag("a", "1.2.0", "")

	p := computeFixed(t, model.VersioningFixed, git)

	a, b := p.Releases["a"], p.Releases["b"]
	require.True(t, a.Releasing())
	require.True(t, b.Releasing(), "the newcomer joins the group's release")
	assertVersion(t, v(1, 2, 1), a.Next)
	assertVersion(t, v(1, 2, 1), b.Next, "at the group's version, not at 0.0.1")
	assert.False(t, b.HasBaseline, "it really has never published")
	assert.True(t, b.FixedRide, "W234 explains the ride")
	assert.False(t, hasCode(p, CodeFixedMajorSpread), "no W233 for a newcomer, got %v", codes(p))
}

func TestFixedMemberOnAnotherMajorIsReported(t *testing.T) {
	// The expensive mistake: one member tagged on a major of its own. The
	// group versions from its newest member, so b's stray 9.0.0 takes a from
	// 1.2.0 to 9.0.1 in one run, and §19.1 forbids moving the tags back. The
	// plan is still the only correct one — both versions are published — so
	// the group releases and W233 names who decided it.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): tweak"},
	).tag("a", "1.2.0", "").tag("b", "9.0.0", "")

	p := computeFixed(t, model.VersioningFixed, git)

	a, b := p.Releases["a"], p.Releases["b"]
	assertVersion(t, v(9, 0, 1), a.Next, "the group versions from its newest member")
	assertVersion(t, v(9, 0, 1), b.Next)
	require.True(t, hasCode(p, CodeFixedMajorSpread), "W233, got %v", codes(p))

	var msg string
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedMajorSpread {
			msg = d.Message
		}
	}
	assert.Contains(t, msg, "1.2.0", "the message names where the outlier's mates are")
	assert.Contains(t, msg, "9.0.0", "and the version that decided the group's")
}

func TestFixedMinorSpreadIsNotReported(t *testing.T) {
	// The negative that keeps W233 worth reading: members apart by a minor or
	// a patch are the ordinary mid-catch-up state a failed ride leaves behind,
	// and W234 already accounts for it. Only a major spread is warned about.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): tweak"},
	).tag("a", "1.2.0", "").tag("b", "1.0.0", "")

	p := computeFixed(t, model.VersioningFixed, git)
	assert.False(t, hasCode(p, CodeFixedMajorSpread), "no W233 below the major, got %v", codes(p))
}

func TestFixedSparseMemberOnAnotherMajorIsNotReported(t *testing.T) {
	// A sparse member is behind on purpose: it stays on its own line until it
	// changes, so its major disagreeing with the group's is the mode working.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): tweak"},
	).tag("a", "9.0.0", "").tag("b", "1.0.0", "")

	p := computeFixed(t, model.VersioningFixedSparse, git)
	assert.False(t, hasCode(p, CodeFixedMajorSpread), "no W233 under a sparse mode, got %v", codes(p))
}

func TestFixedSparseMemberDecidingTheGroupMajorIsReported(t *testing.T) {
	// A group can mix modes, and the aggregate takes the newest baseline from
	// every member, sparse ones included. So a sparse member can be the one
	// that decides the group's major while the non-sparse members sit a major
	// below it — the outlier is the sparse package, and it is exactly the one
	// W233 has to name for the warning to be actionable.
	fixed := &model.Space{Name: "left", Versioning: model.VersioningFixed, VersionGroup: "core"}
	sparse := &model.Space{Name: "right", Versioning: model.VersioningFixedSparse, VersionGroup: "core"}
	pkgs := []*model.Package{
		{Name: "a", Dir: "/r/l/a", Space: fixed},
		{Name: "s", Dir: "/r/r/s", Space: sparse},
	}
	git := newFakeGit(
		commit{sha: "c1", message: "fix(a): tweak"},
	).tag("a", "1.2.0", "").tag("s", "9.0.0", "")

	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)
	require.True(t, hasCode(p, CodeFixedMajorSpread), "W233, got %v", codes(p))

	var msg string
	for _, d := range p.Diagnostics {
		if d.Code == CodeFixedMajorSpread {
			msg = d.Message
		}
	}
	assert.Contains(t, msg, "9.0.0", "the sparse member decided the group's major")
	assert.Contains(t, msg, "1.2.0", "and the message names where its mates are")
}

// ---------------------------------------------------------------------------
// Rejected pins fall back (§16 unit-scoped blast radius) and propagated
// transitions graduate — the two planner properties the integration suite
// fences with dedicated regression tests.
// ---------------------------------------------------------------------------

func TestRejectedPinFallsBackToTheComputedVersion(t *testing.T) {
	// A commit carrying a real feat and a bad pin: the pin's guard fires
	// (E156) and the pin contributes nothing — the feat still releases at its
	// ordinarily computed version instead of being swallowed with the footer.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): needs a minor\n---\nrelease(a): pin too low\n\nRelease-As: 1.0.1\n"},
	).tag("a", "1.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: fixedPkgs(model.VersioningIndependent), Root: "/r"})
	require.NoError(t, err)

	a := p.Releases["a"]
	found := false
	for _, d := range p.Diagnostics {
		if d.Code == CodePinBelowBump {
			found = true
		}
	}
	require.True(t, found, "the below-bump guard still fires: %v", p.Diagnostics)
	assert.False(t, a.Pinned, "a rejected pin is not a pin")
	require.True(t, a.Releasing(), "the sibling feat still releases")
	assertVersion(t, v(1, 1, 0), a.Next, "at the computed version, not the baseline")
}

func TestRejectedPinAloneReleasesNothing(t *testing.T) {
	// The fallback is the ordinary computation: with no sibling bump there is
	// nothing to fall back to, so a lone rejected pin still releases nothing.
	git := newFakeGit(
		commit{sha: "c1", message: "release(a): backwards\n\nRelease-As: 0.9.0\n"},
	).tag("a", "1.0.0", "")
	p, err := Compute(context.Background(), git, Options{Packages: fixedPkgs(model.VersioningIndependent), Root: "/r"})
	require.NoError(t, err)

	assert.False(t, p.Releases["a"].Releasing())
	assertVersion(t, v(1, 0, 0), p.Releases["a"].Next, "reporting stays at the baseline")
}

func TestFixedGroupRejectedPinFallsBack(t *testing.T) {
	// The same rule under fixed versioning: the space's rejected pin reports
	// its error and the group releases at the computed shared version.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(a): work\n---\nrelease(a): too low\n\nRelease-As: 1.0.1\n"},
	).tag("a", "1.0.0", "").tag("b", "1.0.0", "")
	p := computeFixed(t, model.VersioningFixed, git)

	assertVersion(t, v(1, 1, 0), p.Releases["a"].Next)
	assertVersion(t, v(1, 1, 0), p.Releases["b"].Next, "the ride follows the fallback version")
	assert.False(t, p.Releases["a"].Pinned)
}
