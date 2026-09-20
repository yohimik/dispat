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

// Versioning groups on a shared prerelease train: the resting-channel rule,
// the member target floor, and the three sharing axes.
//
// Every fixture here builds a group of three members over a linear history,
// because the shapes these tests are about — a rider that never left the
// train, a sparse member that never joined it, a leg that failed after a
// neighbour published — all need one member to be in a different position
// from the others.

// sharedGroup is a group's versioning rule as a fixture states it: the semver
// mode every member versions under.
type sharedGroup struct {
	mode model.Versioning
}

// pkgs builds a three-member group ("shared": a, b, d) under the rule, with a
// member optionally overridden to a mode of its own so a fixture can make one
// member sparse.
func (g sharedGroup) pkgs(overrides map[string]model.Versioning) []*model.Package {
	out := make([]*model.Package, 0, 3)
	for _, name := range []string{"a", "b", "d"} {
		mode := g.mode
		if override, ok := overrides[name]; ok {
			mode = override
		}
		out = append(out, &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: &model.Space{
			Name:       "shared",
			Versioning: mode,
		}})
	}
	return out
}

func (g sharedGroup) compute(t *testing.T, git *fakeGit, overrides map[string]model.Versioning) *Plan {
	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: g.pkgs(overrides), Root: "/r"})
	require.NoError(t, err)
	return p
}

// assertNext asserts one member's planned version and whether it releases.
func assertNext(t *testing.T, p *Plan, name, want string, releasing bool) {
	t.Helper()
	rel := p.Releases[name]
	require.NotNil(t, rel, "package %s", name)
	assert.Equal(t, want, rel.Next.String(), "%s: version", name)
	assert.Equal(t, releasing, rel.IsReleasing(), "%s: releasing", name)
}

// assertQuiet asserts that no member of the group releases anything.
func assertQuiet(t *testing.T, p *Plan) {
	t.Helper()
	for _, name := range []string{"a", "b", "d"} {
		assert.False(t, p.Releases[name].IsReleasing(), "%s must not release", name)
	}
}

// findDiagnostic returns the first diagnostic with the code, raised against
// the package.
func findDiagnostic(p *Plan, code, pkg string) (Diagnostic, bool) {
	for _, d := range p.Diagnostics {
		if d.Code == code && d.Pkg == pkg {
			return d, true
		}
	}
	return Diagnostic{}, false
}

// ---------------------------------------------------------------------------
// X1: a resting channel is not a proposal
// ---------------------------------------------------------------------------

func TestRestingStableMemberDoesNotGraduateItsGroup(t *testing.T) {
	// a and d ride an rc train; b never joined it and rests on stable. b's
	// channel is where its own tags put it, not a request to end the train,
	// so a fix in d continues the train rather than graduating the group.
	for _, mode := range []model.Versioning{model.VersioningFixedMajorMinor, model.VersioningFixed} {
		t.Run(string(mode), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
				commit{sha: "c2", message: "fix(d): more train work"},
			).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

			p := sharedGroup{mode: mode}.compute(t, git, map[string]model.Versioning{
				"b": model.VersioningFixedMajorMinorSparse,
			})

			assertNext(t, p, "d", "1.11.0-rc.1", true)
			assertNext(t, p, "a", "1.11.0-rc.1", true)
			assert.True(t, p.Releases["a"].FixedRide, "a has no changes of its own and rides")
			assert.False(t, p.Releases["b"].IsReleasing(), "the sparse member stays where it is")
			assert.Equal(t, "1.10.0", p.Releases["b"].Next.String())
		})
	}
}

func TestRestingPrereleaseMemberDoesNotUngraduateItsGroup(t *testing.T) {
	// The mirror image: a and d graduated, b's leg failed and left it on the
	// train. b is caught up to the group's published version rather than
	// dragging a and d back onto a channel neither of them asked for.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(a,d)%rc>stable: graduate"},
	).tag("a", "1.10.0", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0", "c2").tag("d", "1.11.0", "c2")

	p := sharedGroup{mode: model.VersioningFixed}.compute(t, git, nil)

	assertNext(t, p, "b", "1.11.0", true)
	assert.True(t, p.Releases["b"].FixedRide, "b catches up to the group's published version")
	assert.False(t, p.Releases["a"].IsReleasing(), "a published already and has nothing to add")
	assert.False(t, p.Releases["d"].IsReleasing())
	assert.False(t, hasCode(p, CodeChannelEntryPatch),
		"the group is not entering a channel, so no channel-entry patch applies")
}

func TestNewcomerWithoutABaselineProposesNoChannel(t *testing.T) {
	// A member with no tag at all has no baseline channel to rest on, so it
	// must not be read as proposing stable against a group mid-train.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
		commit{sha: "c2", message: "fix(d): more train work"},
	).tag("a", "1.10.3", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	assertNext(t, p, "d", "1.11.0-rc.1", true)
	assertNext(t, p, "b", "1.11.0-rc.1", true)
	assert.True(t, p.Releases["b"].FixedRide, "the newcomer joins the train it is riding")
}

// ---------------------------------------------------------------------------
// X2: the member target floor
// ---------------------------------------------------------------------------

// graduationRetry is the shape a half-finished graduation leaves behind: the
// group entered an rc train together, a graduation was written for all three,
// and only a published before the run died. b and d carry the directive and
// the train tag; neither carries the feature that set the train's core, which
// belongs to d alone.
func graduationRetry() *fakeGit {
	return newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(a,b,d)%rc>stable: graduate the train"},
	).tag("a", "1.10.0", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0", "c2")
}

func TestGraduationRetryFinishesTheTrainRatherThanGoingBackwards(t *testing.T) {
	// b's own window carries no bump at all: the feature that took the train
	// to 1.11.0 is d's, and b only ever rode. Graduating from b's own stable
	// baseline computes 1.10.0, which is behind the rc it has published. The
	// floor is what makes the retry finish the train it is on.
	for _, mode := range []model.Versioning{model.VersioningFixed, model.VersioningFixedMajorMinor} {
		t.Run(string(mode), func(t *testing.T) {
			p := sharedGroup{mode: mode}.compute(t, graduationRetry(), nil)

			assertNext(t, p, "b", "1.11.0", true)
			assertNext(t, p, "d", "1.11.0", true)
			assert.False(t, p.Releases["a"].IsReleasing(), "a published in the failed run")
			assert.False(t, hasCode(p, CodeGraduateNoIncrease))
		})
	}
}

func TestARejectedPinOnARiderFallsBackAboveItsBaseline(t *testing.T) {
	// The pin names the version b already holds, so E153 rejects it and the
	// ordinary computation runs in its place (§16). That fallback must see
	// the same floor the direct path does, or a rejected footer would turn a
	// finishable graduation into E185.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(a,d)%rc>stable: graduate the train\n\n---\n\n" +
			"release(b)%rc>stable: graduate b at a stated version\n\nRelease-As: 1.11.0-rc.0\n"},
	).tag("a", "1.10.0", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0", "c2")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	require.True(t, hasCode(p, CodePinNotGreater), "the pin is rejected")
	assertNext(t, p, "b", "1.11.0", true)
	assert.False(t, p.Releases["b"].Pinned)
	assert.False(t, hasCode(p, CodeGraduateNoIncrease), "the fallback sees the floor too")
}

func TestAHandEditedTagStillFailsAGraduation(t *testing.T) {
	// b sits a patch above the group's shared minor, which nothing in the
	// group's history explains. The floor raises b to the line and no
	// further, so E185 still reports the tag that cannot be graduated from.
	git := newFakeGit(
		commit{sha: "c1", message: "release(b)%rc: put b on its own rc line"},
		commit{sha: "c2", message: "release(b)%rc>stable: bring b back"},
	).tag("a", "1.11.5", "").tag("b", "1.10.0", "").tag("d", "1.11.5", "").
		tag("b", "1.11.3-rc.0", "c1")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	d, ok := findDiagnostic(p, CodeGraduateNoIncrease, "b")
	require.True(t, ok, "E185 still reports the tag nothing explains")
	assert.Contains(t, d.Message, "1.11.3-rc.0")
}

func TestAChannelSwitchBelowTheLineStillFails(t *testing.T) {
	// The floor raises the core, never the channel: switching b from rc onto
	// a channel whose name sorts lower produces a version below b's own
	// baseline, and E195 is exactly the guard for that.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(b)%rc>beta: move b to the beta line"},
	).tag("a", "1.10.0", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.5", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0", "c2")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	d, ok := findDiagnostic(p, CodeVersionNotGreater, "b")
	require.True(t, ok, "E195 still reports a version behind the package's own baseline")
	assert.Contains(t, d.Message, "1.11.0-beta.0")
}

func TestAStableLineLaggardStillCatchesUpAtTheSharedPrefix(t *testing.T) {
	// The floor must not disturb the case alignment already answers: a member
	// behind the group's stable line releases at the line, on the stable
	// channel, exactly as before.
	git := newFakeGit(
		commit{sha: "c1", message: "fix(b): b's own patch"},
	).tag("a", "1.10.0", "").tag("b", "1.9.6", "").tag("d", "1.10.0", "")

	p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, git, nil)

	assertNext(t, p, "b", "1.10.0", true)
	assert.False(t, p.Releases["a"].IsReleasing(), "a patch stays below the shared minor")
	assert.False(t, p.Releases["d"].IsReleasing())
}
