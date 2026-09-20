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
// because the shapes these tests are about all need one member to be in a
// different position from the others: a rider that never left the train, a
// sparse member that never joined it, a leg that failed after a neighbour
// published.

// sharedGroup is a group's versioning rule as a fixture states it: the semver
// mode every member versions under, and the two sharing axes. The zero axes
// are the defaults, so a fixture that names only a mode states exactly what a
// configuration written before the axes existed states.
type sharedGroup struct {
	mode     model.Versioning
	counter  model.Sharing
	channels model.Sharing
}

// String names the rule for a subtest.
func (g sharedGroup) String() string {
	return string(g.mode) + "/counter=" + g.counter.String() + "/channels=" + g.channels.String()
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
			Name:           "shared",
			Versioning:     mode,
			CounterSharing: g.counter,
			ChannelSharing: g.channels,
		}})
	}
	return out
}

// sharingRules are the rule combinations the axis tests run over: the default
// as a control, then each valid pairing of the other two axes.
func sharingRules(mode model.Versioning) []sharedGroup {
	return []sharedGroup{
		{mode: mode},
		{mode: mode, counter: model.SharingIndependent},
		{mode: mode, counter: model.SharingIndependent, channels: model.SharingIndependent},
	}
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

// ---------------------------------------------------------------------------
// The axes
// ---------------------------------------------------------------------------

// onTheTrain is the group mid-train: all three entered an rc together at c1
// on one shared feature, and a published the next prerelease at c2 while b's
// and d's legs failed. The feature is scoped to every member so that each
// one's own window justifies the train's core under every depth, which is
// what makes the same fixture readable at all three. The later commits each
// test adds are what tell the rules apart.
func onTheTrain(history ...commit) *fakeGit {
	git := newFakeGit(append([]commit{
		{sha: "c1", message: "feat(a,b,d)%rc: start the train"},
		{sha: "c2", message: "fix(a,b,d): a shared fix"},
	}, history...)...)
	return git.tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("a", "1.11.0-rc.1", "c2")
}

func TestASharedCounterRunsTheWholeTrain(t *testing.T) {
	// The control. With one counter across the group, every run of the train
	// is the group's: the retry advances the shared counter and the member
	// that already published rides it.
	for _, history := range [][]commit{nil, {{sha: "c3", message: "fix(d): repair the release"}}} {
		p := sharedGroup{mode: model.VersioningFixedMajorMinor}.compute(t, onTheTrain(history...), nil)

		assertNext(t, p, "a", "1.11.0-rc.2", true)
		assertNext(t, p, "b", "1.11.0-rc.2", true)
		assertNext(t, p, "d", "1.11.0-rc.2", true)
		assert.True(t, p.Releases["a"].FixedRide, "a has nothing of its own and rides")
	}
}

func TestAnIndependentCounterRetriesOnlyTheFailedLegs(t *testing.T) {
	// The whole point of the axis. a published rc.1 for exactly this work, so
	// a retry at the same HEAD owes it nothing; b and d continue their own
	// counters to the version the failed run planned for them. That is
	// §13.7c G3 per member, which a shared counter cannot offer.
	for _, mode := range []model.Versioning{
		model.VersioningFixedMajor, model.VersioningFixedMajorMinor, model.VersioningFixed,
	} {
		for _, rule := range sharingRules(mode)[1:] {
			t.Run(rule.String(), func(t *testing.T) {
				for _, history := range [][]commit{nil, {{sha: "c3", message: "fix(d): repair the release"}}} {
					p := rule.compute(t, onTheTrain(history...), nil)

					assertNext(t, p, "a", "1.11.0-rc.1", false)
					assertNext(t, p, "b", "1.11.0-rc.1", true)
					assertNext(t, p, "d", "1.11.0-rc.1", true)
					assert.False(t, p.Releases["b"].FixedRide, "b releases work of its own")
				}
			})
		}
	}
}

func TestTheNextTrainTakesEveryMemberAndResetsTheCounters(t *testing.T) {
	// A movement of the shared prefix is the group's under every rule, and a
	// new core starts every member's counter again.
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor) {
		t.Run(rule.String(), func(t *testing.T) {
			p := rule.compute(t, onTheTrain(commit{sha: "c3", message: "feat(d)!: a breaking change"}), nil)

			for _, name := range []string{"a", "b", "d"} {
				assertNext(t, p, name, "2.0.0-rc.0", true)
			}
			assert.True(t, p.Releases["a"].FixedRide, "a rides the new train")
			assert.False(t, p.Releases["d"].FixedRide, "d wrote the change that opened it")
		})
	}
}

func TestIndependentChannelsGraduateOnlyTheNamedMembers(t *testing.T) {
	// A graduation ends a train, which is a deliberate act (§11.5). Where the
	// channel is each member's own, ending d's train says nothing about a's
	// or b's, and neither of them is dragged off the line it is on.
	rule := sharedGroup{
		mode:     model.VersioningFixedMajorMinor,
		counter:  model.SharingIndependent,
		channels: model.SharingIndependent,
	}
	p := rule.compute(t, onTheTrain(commit{sha: "c3", message: "release(d)%rc>stable: graduate d"}), nil)

	assertNext(t, p, "d", "1.11.0", true)
	assertNext(t, p, "b", "1.11.0-rc.1", true) // its own failed leg, not the graduation
	assertNext(t, p, "a", "1.11.0-rc.1", false)
	assert.False(t, hasCode(p, CodeFixedChannelConflict),
		"no channel is forced on anybody, so there is no conflict to report")
}

func TestSharedChannelsGraduateTheWholeGroup(t *testing.T) {
	// The other side of the same rule: while the channel is shared, ending
	// the train is a movement of a part the group holds in common, so it
	// takes every member whatever the counter axis says.
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor)[:2] {
		t.Run(rule.String(), func(t *testing.T) {
			p := rule.compute(t, onTheTrain(commit{sha: "c3", message: "release(d)%rc>stable: graduate"}), nil)

			for _, name := range []string{"a", "b", "d"} {
				assertNext(t, p, name, "1.11.0", true)
			}
			assert.True(t, p.Releases["a"].FixedRide)
		})
	}
}

func TestIndependentChannelsKeepEachMemberOnItsOwnLine(t *testing.T) {
	// Once d has graduated and the others have not, a later movement of the
	// shared prefix takes everybody: d on stable, a and b onto the new core's
	// rc line, each at the start of its own counter. A ride never graduates a
	// member, because nothing wrote that intent for it.
	rule := sharedGroup{
		mode:     model.VersioningFixedMajorMinor,
		counter:  model.SharingIndependent,
		channels: model.SharingIndependent,
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
		commit{sha: "c2", message: "release(d)%rc>stable: graduate d"},
		commit{sha: "c3", message: "feat(d): the next feature"},
	).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
		tag("d", "1.11.0", "c2")

	p := rule.compute(t, git, nil)

	assertNext(t, p, "d", "1.12.0", true)
	assertNext(t, p, "a", "1.12.0-rc.0", true)
	assertNext(t, p, "b", "1.12.0-rc.0", true)
	assert.True(t, p.Releases["a"].FixedRide)
	assert.Equal(t, "rc", p.Releases["a"].Channel, "a rides on the line it is on")
}

func TestAStableMemberFollowsAPrereleaseGroupVersion(t *testing.T) {
	// The one place the group's channel still reaches a member under the
	// independent axis: a ride must never be the first stable publication of
	// a core the group has only reached as a prerelease.
	rule := sharedGroup{
		mode:     model.VersioningFixedMajorMinor,
		counter:  model.SharingIndependent,
		channels: model.SharingIndependent,
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
		commit{sha: "c2", message: "feat(d)!: a breaking change"},
	).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
		tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

	p := rule.compute(t, git, nil)

	assertNext(t, p, "b", "2.0.0-rc.0", true)
	assert.Equal(t, "rc", p.Releases["b"].Channel,
		"a member resting on stable follows the group onto its prerelease line")
	assert.True(t, p.Releases["b"].FixedRide)
}

func TestAPrereleaseMemberIsNotGraduatedByARide(t *testing.T) {
	// The converse. The group moves on the stable line, and b, left behind on
	// an rc, joins the new core on the line it is already on.
	rule := sharedGroup{
		mode:     model.VersioningFixedMajorMinor,
		counter:  model.SharingIndependent,
		channels: model.SharingIndependent,
	}
	git := newFakeGit(
		commit{sha: "c1", message: "feat(d): a feature on the stable line"},
	).tag("a", "1.11.0", "").tag("b", "1.11.0-rc.5", "").tag("d", "1.11.0", "")

	p := rule.compute(t, git, nil)

	assertNext(t, p, "d", "1.12.0", true)
	assertNext(t, p, "a", "1.12.0", true)
	assertNext(t, p, "b", "1.12.0-rc.0", true)
	assert.Equal(t, "rc", p.Releases["b"].Channel)
}

func TestAlignmentReadsTheCounterAxisBothWays(t *testing.T) {
	// A member holding the line's prefix on the line's channel, one
	// prerelease behind the member that is furthest along. With one counter
	// that is a laggard and the alignment catches it up; with a counter of
	// its own it is exactly where it is entitled to be, and a quiet run stays
	// quiet.
	fixture := func() *fakeGit {
		return newFakeGit(
			commit{sha: "c1", message: "feat(a,b,d)%rc: start the train"},
		).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
			tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
			tag("d", "1.11.0-rc.1", "c1")
	}
	rules := sharingRules(model.VersioningFixedMajorMinor)

	shared := rules[0].compute(t, fixture(), nil)
	assertNext(t, shared, "a", "1.11.0-rc.1", true)
	assertNext(t, shared, "b", "1.11.0-rc.1", true)
	assert.True(t, shared.Releases["a"].FixedRide, "one counter makes a lower one a laggard")

	for _, rule := range rules[1:] {
		t.Run(rule.String(), func(t *testing.T) {
			assertQuiet(t, rule.compute(t, fixture(), nil))
		})
	}
}

func TestAMemberBelowThePrefixStillCatchesUp(t *testing.T) {
	// The other half of alignment: falling behind the shared prefix is not
	// something any axis excuses. Under one counter the laggard adopts the
	// group's published version; under its own it joins the prefix at the
	// start of its own line.
	for _, c := range []struct {
		rule sharedGroup
		want string
	}{
		{sharedGroup{mode: model.VersioningFixedMajorMinor}, "1.11.0-rc.1"},
		{sharedGroup{mode: model.VersioningFixedMajorMinor, counter: model.SharingIndependent}, "1.11.0-rc.0"},
	} {
		t.Run(c.rule.String(), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a)%rc: enter"},
				commit{sha: "c2", message: "fix(a,d): a shared fix"},
			).tag("a", "1.10.3", "").tag("b", "1.9.4", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1").
				tag("a", "1.11.0-rc.1", "c2").tag("d", "1.11.0-rc.1", "c2")

			p := c.rule.compute(t, git, nil)

			assertNext(t, p, "b", c.want, true)
			assert.True(t, p.Releases["b"].FixedRide, "b has nothing of its own")
			d, ok := findDiagnostic(p, CodeFixedAlign, "b")
			require.True(t, ok, "a catch-up is reported")
			assert.Contains(t, d.Message, c.want)
		})
	}
}

func TestASparseMemberNeverRidesWhateverTheAxes(t *testing.T) {
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor) {
		t.Run(rule.String(), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(a,d)%rc: start the train"},
				commit{sha: "c2", message: "feat(d)!: a breaking change"},
			).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

			p := rule.compute(t, git, map[string]model.Versioning{
				"b": model.VersioningFixedMajorMinorSparse,
			})

			assertNext(t, p, "d", "2.0.0-rc.0", true)
			assertNext(t, p, "a", "2.0.0-rc.0", true)
			assert.True(t, p.Releases["a"].FixedRide, "a is a plain member and rides")
			assert.False(t, p.Releases["b"].IsReleasing(), "the sparse member waits for a cause of its own")
		})
	}
}

func TestAHeldMemberReportsTheGroupVersionWhateverTheAxes(t *testing.T) {
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor) {
		t.Run(rule.String(), func(t *testing.T) {
			p := rule.compute(t, onTheTrain(
				commit{sha: "c3", message: "feat(d)!: a breaking change\n\n---\n\n" +
					"release(b): hold b back\n\nRelease-As: none\n"}), nil)

			b := p.Releases["b"]
			assert.True(t, b.Held)
			assert.False(t, b.IsReleasing())
			assertVersion(t, v(2, 0, 0), b.Next.Core(), "W154 reports the version the hold withholds")
		})
	}
}

func TestAPinInsideThePrefixStaysWithItsMember(t *testing.T) {
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor) {
		t.Run(rule.String(), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(d)%rc: start the train\n\n---\n\nrelease(a,b)%rc: enter"},
				commit{sha: "c2", message: "release(b): pin b\n\nRelease-As: 1.11.0-rc.7\n"},
			).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

			p := rule.compute(t, git, nil)

			assertNext(t, p, "b", "1.11.0-rc.7", true)
			assert.True(t, p.Releases["b"].Pinned)
			assert.False(t, p.Releases["a"].IsReleasing(), "a pin inside the prefix asks nothing of the group")
			assert.False(t, p.Releases["d"].IsReleasing())
		})
	}
}

func TestAPinInsideThePrefixNeverBreaksAMovingGroup(t *testing.T) {
	// The pin names a version inside the prefix the group is leaving, so the
	// group never took it (fixedGroupPin left it to the member) and the
	// member cannot apply it either: honouring it would leave one member on
	// the old prefix while the rest moved. Every rule answers the same way,
	// and the silence about the dropped pin is the same silence under all of
	// them.
	for _, rule := range sharingRules(model.VersioningFixedMajorMinor) {
		t.Run(rule.String(), func(t *testing.T) {
			git := newFakeGit(
				commit{sha: "c1", message: "feat(a,b,d)%rc: start the train"},
				commit{sha: "c2", message: "feat(d)!: a breaking change\n\n---\n\n" +
					"release(b): hold b at the old line\n\nRelease-As: 1.11.0-rc.7\n"},
			).tag("a", "1.10.3", "").tag("b", "1.10.0", "").tag("d", "1.10.0", "").
				tag("a", "1.11.0-rc.0", "c1").tag("b", "1.11.0-rc.0", "c1").tag("d", "1.11.0-rc.0", "c1")

			p := rule.compute(t, git, nil)

			for _, name := range []string{"a", "b", "d"} {
				assertNext(t, p, name, "2.0.0-rc.0", true)
			}
			assert.False(t, p.Releases["b"].Pinned, "the pin the group did not take is not applied")
		})
	}
}

func TestEveryAxisReleasesAboveEachMembersOwnBaseline(t *testing.T) {
	// The invariant that outranks every rule above, extended over the axes
	// and over a train baseline, which is where a member's own window and its
	// own tag disagree the most.
	modes := []model.Versioning{
		model.VersioningFixedMajor, model.VersioningFixedMajorMinor, model.VersioningFixed,
	}
	messages := []string{
		"fix(d): a patch", "feat(d): a minor", "feat(d)!: a break",
		"fix(b): the other side", "release(d)%rc>stable: graduate d",
		"release(a,b,d)%rc>stable: graduate the train",
	}
	for _, mode := range modes {
		for _, rule := range sharingRules(mode) {
			for _, msg := range messages {
				p := rule.compute(t, onTheTrain(commit{sha: "c3", message: msg}), nil)
				for _, name := range []string{"a", "b", "d"} {
					rel := p.Releases[name]
					if !rel.IsReleasing() {
						continue
					}
					assert.Truef(t, versionLess(rel.Baseline, rel.Next),
						"%s/%s: %s released %s over baseline %s", rule, msg, name, rel.Next, rel.Baseline)
				}
			}
		}
	}
}

func TestAnIndependentCounterConvergesAcrossRuns(t *testing.T) {
	// The multi-run replay of §13.7c G6: plan, publish part of it by writing
	// the tags the run would have written, replan. Each run releases only
	// what the last one left, and the third is empty.
	rule := sharedGroup{mode: model.VersioningFixedMajorMinor, counter: model.SharingIndependent}

	first := rule.compute(t, onTheTrain(), nil)
	assertNext(t, first, "b", "1.11.0-rc.1", true)
	assertNext(t, first, "d", "1.11.0-rc.1", true)

	// b published, d's leg failed again.
	second := rule.compute(t, onTheTrain().tag("b", "1.11.0-rc.1", "c2"), nil)
	assertNext(t, second, "d", "1.11.0-rc.1", true)
	assert.False(t, second.Releases["b"].IsReleasing(), "b is done with this work")
	assert.False(t, second.Releases["a"].IsReleasing())

	// d published too.
	third := rule.compute(t, onTheTrain().tag("b", "1.11.0-rc.1", "c2").tag("d", "1.11.0-rc.1", "c2"), nil)
	assertQuiet(t, third)
}
