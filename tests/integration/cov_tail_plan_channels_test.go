// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the channel axis and the prerelease arithmetic behind
// it.
//
// A channel directive is the one piece of a record whose effect is invisible
// in the version it produces: "already on beta" and "moved to beta" render the
// same tag shape, and only the diagnostics say which happened. The scenarios
// here are therefore written around the diagnostics a directive raises when it
// proposes nothing, when two of them propose different things, and when a
// propagated channel reaches a dependent it must not graduate.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailChannelRepo is a workspace of independent packages, one per claim,
// so that every directive below reaches exactly the package it names and the
// diagnostics can be read per package.
func covTailChannelRepo(t *testing.T, names ...string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	for _, name := range names {
		r.SeedPackage("packages", name)
	}
	return r
}

// TestCovTailDirectChannelDirectivesReportWhatTheyProposedNothingFor: a direct
// channel directive is written by hand and reviewed, so one that does nothing
// is worth saying out loud — the author wrote it expecting a move. Each shape
// that proposes nothing gets its own package and its own code: graduating a
// package that is already stable, naming the channel a package is already on,
// and a transition whose two sides are the same. The one deliberate silence is
// a transition that does not match the package's channel: that is the
// mechanism working, since matching against the baseline is what makes one
// directive correct on the first run and on the fifth.
func TestCovTailDirectChannelDirectivesReportWhatTheyProposedNothingFor(t *testing.T) {
	r := covTailChannelRepo(t, "stayed", "train", "selfmove", "twovalues")
	r.Commit("chore: seed every package")
	tagAt(r, "stayed@1.0.0", "HEAD")
	tagAt(r, "train@1.0.0", "HEAD")
	tagAt(r, "train@1.1.0-beta.0", "HEAD")

	r.CommitEmpty("release(stayed)%stable: graduate what is already stable")
	r.CommitEmpty("release(stayed)%beta>rc: a transition for a train this package is not on")
	r.CommitEmpty("release(train)%beta: name the channel the train is already on")
	r.CommitEmpty("release(selfmove)%beta>beta: a transition to its own source")
	r.CommitEmpty("release(twovalues)%beta: the first thought")
	r.CommitEmpty("release(twovalues)%rc: the second thought")

	res := r.Status()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W185", "stayed"),
		"graduating a stable package is reported: %s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W199", "train"),
		"a channel the package is already on is reported: %s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W207", "selfmove"),
		"a transition with the same source and target is reported: %s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W186", "twovalues"),
		"two directives proposing different channels conflict: %s", res.Stdout)
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W206", "stayed"),
		"a transition that does not match is the mechanism working, not a finding")

	line := harness.GraphLine(res.Events, "twovalues")
	assert.Contains(t, line.Str("message")+line.Str("channel")+line.Str("reason"), "rc",
		"the newer of the two conflicting directives wins: %s", res.Stdout)
}

// TestCovTailAnyPrereleaseTransitionEndsWhateverTrainItFinds: the "*" from-side
// exists so one directive can end a train without the author having to know
// which channel the package landed on. It matches any prerelease and never
// matches stable, which is what keeps the same text inert once the package has
// graduated.
func TestCovTailAnyPrereleaseTransitionEndsWhateverTrainItFinds(t *testing.T) {
	r := covTailChannelRepo(t, "rider", "settled")
	r.Commit("chore: seed both packages")
	tagAt(r, "rider@1.0.0", "HEAD")
	tagAt(r, "rider@1.1.0-beta.0", "HEAD")
	tagAt(r, "settled@1.0.0", "HEAD")

	r.WriteFile("packages/rider/work.txt", "work\n")
	r.WriteFile("packages/settled/work.txt", "work\n")
	r.Commit("feat(rider,settled)%*>stable: ship the feature and end whichever train is running")

	res := r.StatusOK()
	assert.Equal(t, "beta -> stable", harness.GraphLine(res.Events, "rider").Str("channel"),
		"the package on a prerelease is graduated: %s", res.Stdout)
	assert.Equal(t, "stable", harness.GraphLine(res.Events, "settled").Str("channel"),
		"and the one already stable was never a candidate: %s", res.Stdout)
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W185", "settled"),
		"which is not the same as refusing to graduate it: %s", res.Stdout)
}

// TestCovTailPrereleaseBaselineWithoutACounterStopsTheTrain: §11.3 requires the
// prerelease counter to be a separate numeric identifier, because numeric
// identifiers compare numerically and a fused "beta10" compares as ASCII and
// misorders at ten. A baseline tag written by hand without one — or with a
// counter that is not a number — cannot be continued from, and the run says so
// instead of inventing a counter and overwriting the train's order.
func TestCovTailPrereleaseBaselineWithoutACounterStopsTheTrain(t *testing.T) {
	r := covTailChannelRepo(t, "nocount", "notanumber")
	r.Commit("chore: seed both packages")
	tagAt(r, "nocount@1.0.0", "HEAD")
	tagAt(r, "nocount@1.0.1-beta", "HEAD")
	tagAt(r, "notanumber@1.0.0", "HEAD")
	tagAt(r, "notanumber@1.0.1-beta.x", "HEAD")

	r.WriteFile("packages/nocount/work.txt", "work\n")
	r.WriteFile("packages/notanumber/work.txt", "work\n")
	r.Commit("fix(nocount,notanumber): work that would continue each train")

	res := r.Status()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "E182", "nocount"),
		"a prerelease with no counter cannot be continued: %s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "E182", "notanumber"),
		"nor can one whose counter is not a number: %s", res.Stdout)
	assert.Equal(t, 2, len(r.TagList())-2, "nothing new was tagged; tags: %v", r.TagList())
}

// TestCovTailComputedVersionMustExceedItsBaseline: the last guard of §13.9.
// Versions are computed from the *stable* baseline, so a repository whose
// newest tag is a prerelease of a higher core than anything the window can
// reach — an abandoned train, or a tag moved by hand — would otherwise compute
// a version SemVer ranks below what the package already published. Releasing
// that would make the tag order lie about which release came last.
func TestCovTailComputedVersionMustExceedItsBaseline(t *testing.T) {
	r := covTailChannelRepo(t, "rewound")
	r.Commit("chore: seed the package")
	tagAt(r, "rewound@1.0.0", "HEAD")
	tagAt(r, "rewound@2.0.0-beta.0", "HEAD")

	r.WriteFile("packages/rewound/work.txt", "work\n")
	r.Commit("feat(rewound): work that computes below the abandoned train")

	res := r.Status()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "E195", "rewound"),
		"the computed version does not exceed the baseline: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "2.0.0-beta.0", "and the baseline it failed against is named")
}

// covTailFanOutRepo is one provider with two dependents and one package with
// no edge at all, which is the shape every propagation-scope claim needs: a
// scope term can then name a package the traversal never reaches.
func covTailFanOutRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "near", Provider: "core"},
		{Consumer: "far", Provider: "core"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "near", "far", "aside"} {
		r.SeedPackage("packages", name)
	}
	return r
}

// TestCovTailChannelPropagationHonoursItsScope: Propagate-Channel-Scope
// restricts the channel axis the way Propagate-Scope restricts the bump axis.
// A scope naming one dependent reaches that one alone, and a scope naming a
// package the traversal never reaches excludes every dependent it did reach —
// which is a warning rather than silence, because a scope that excludes
// everything is nearly always a stale package name left behind by a rename.
func TestCovTailChannelPropagationHonoursItsScope(t *testing.T) {
	t.Run("one dependent named", func(t *testing.T) {
		r := covTailFanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream\n\n" +
			"Propagate-Channel-Scope: near")

		res := r.StatusOK()
		assert.Equal(t, "stable -> beta", harness.GraphLine(res.Events, "near").Str("channel"),
			"the named dependent takes the channel: %s", res.Stdout)
		assert.NotEqual(t, "stable -> beta", harness.GraphLine(res.Events, "far").Str("channel"),
			"and the unnamed one does not: %s", res.Stdout)
	})

	t.Run("a scope reaching nobody", func(t *testing.T) {
		r := covTailFanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream\n\n" +
			"Propagate-Channel-Scope: aside")

		res := r.StatusOK()
		assert.True(t, harness.IsCodePresent(res.Events, "W205"),
			"the scope excluded every dependent the unit reached: %s", res.Stdout)
	})
}

// TestCovTailBumpPropagationScopeThatExcludesEveryone: the same finding on the
// bump axis, which has its own code because the two scopes are written
// separately and a repository may restrict one without the other.
func TestCovTailBumpPropagationScopeThatExcludesEveryone(t *testing.T) {
	r := covTailFanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core)^: work that propagates one level\n\nPropagate-Scope: aside")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W135"),
		"the scope excluded every dependent the unit reached: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "near").Str("message"),
		"and no dependent was bumped: %s", res.Stdout)
}

// TestCovTailPropagatedStableWouldGraduateADependent: graduation ends a train
// and publishes under the version consumers resolve by default, so it must
// never happen because an unrelated package's commit propagated "stable" down
// an edge. The suppression is reported, because a suppressed graduation is a
// decision the operator may want to make deliberately with a transition.
func TestCovTailPropagatedStableWouldGraduateADependent(t *testing.T) {
	r := covTailFanOutRepo(t)
	r.Commit("chore: seed the fleet")
	tagAt(r, "near@1.0.0", "HEAD")
	tagAt(r, "near@1.1.0-beta.0", "HEAD")

	r.CommitEmpty("release(core)%stable%%stable: propose stable downstream")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W200", "near"),
		"the dependent on a train is not graduated by propagation: %s", res.Stdout)
}

// TestCovTailPropagatedTransitionThatMatchesNoDependent: a transition
// propagated down the graph is matched against each dependent's own baseline.
// When none of them is on the train it names, the directive did nothing, and
// the unit is told once rather than once per dependent.
func TestCovTailPropagatedTransitionThatMatchesNoDependent(t *testing.T) {
	r := covTailFanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.CommitEmpty("release(core)%%rc>stable: end an rc train nobody is on")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W206"),
		"the propagated transition matched no dependent it reached: %s", res.Stdout)
}

// TestCovTailInheritedChannelFromDisagreeingSources: "inherit" means the
// channel of the originating package, and a unit naming two packages on
// different channels has two answers. The run picks the first by name and says
// which one it took, because silently choosing between them would make the
// dependents' channel depend on a package ordering nothing in the message
// shows.
func TestCovTailInheritedChannelFromDisagreeingSources(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "near", Provider: "beta1"}}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"beta1", "stable1", "near"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("chore: seed the fleet")
	tagAt(r, "beta1@1.0.0", "HEAD")
	tagAt(r, "beta1@1.1.0-beta.0", "HEAD")
	tagAt(r, "stable1@1.0.0", "HEAD")

	r.CommitEmpty("release(beta1,stable1)%%inherit: inherit from two packages that disagree")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W160"),
		"the disagreement is reported rather than resolved silently: %s", res.Stdout)
}
