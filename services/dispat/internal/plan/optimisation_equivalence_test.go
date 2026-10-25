// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Appendix B.7a: each vector is a plausible faster route through §9.2 or
// §13.8 that changes the answer, so these pin the literal reading every
// optimisation of the planner has to keep.

// equivalenceWorkspace is one space of packages named by their consumers'
// edges, consumer first.
func equivalenceWorkspace(names []string, edges ...[2]string) ([]*model.Package, []model.Dependency) {
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, 0, len(names))
	for _, name := range names {
		pkgs = append(pkgs, &model.Package{Name: name, Dir: "/r/" + name, Space: space})
	}
	deps := make([]model.Dependency, 0, len(edges))
	for _, edge := range edges {
		deps = append(deps, model.Dependency{Consumer: edge[0], Provider: edge[1]})
	}
	return pkgs, deps
}

func computeEquivalence(t *testing.T, git TagInventoryGitx, pkgs []*model.Package, deps []model.Dependency) *Plan {
	t.Helper()
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
	require.NoError(t, err)
	return p
}

// onBeta puts a package on the beta train at the given commit.
func onBeta(git *fakeGit, name, sha string) *fakeGit {
	return git.tag(name, "1.0.0", "").tag(name, "1.0.1-beta.0", sha)
}

// TestHoistedResolvabilityKeepsAMixedSourceSet (spec vector 82c): the hoisted
// §9.3a set of source channels is {stable, beta}, and each dependent is
// admitted by its own member of it.
func TestHoistedResolvabilityKeepsAMixedSourceSet(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"core", "legacy", "cli", "old"},
		[2]string{"cli", "core"}, [2]string{"old", "legacy"})
	git := newFakeGit(
		commit{sha: "c0", message: "release(legacy,old)%beta: start the train"},
		commit{sha: "c1", message: "feat(core,legacy)^: x"},
	).tag("core", "1.0.0", "").tag("cli", "1.0.0", "")
	git = onBeta(onBeta(git, "legacy", "c0"), "old", "c0")

	p := computeEquivalence(t, git, pkgs, deps)
	cli, old := p.Releases["cli"], p.Releases["old"]
	require.True(t, cli.IsChanged(), "cli is admitted by the stable member: %v", codes(p))
	assert.Equal(t, []string{"core"}, cli.DueTo)
	require.True(t, old.IsChanged(), "old is admitted by the beta member: %v", codes(p))
	assert.Equal(t, []string{"legacy"}, old.DueTo)
	assert.False(t, hasCode(p, CodeBumpSuppressed), "neither bump is suppressed: %v", codes(p))
}

// TestHoistedResolvabilityIsPerUnit (spec vector 82d): two units of one commit
// are admitted against their own sources, so the stable source of one unit
// never admits the beta-only source of the other.
func TestHoistedResolvabilityIsPerUnit(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"core", "legacy", "cli", "old", "app"},
		[2]string{"cli", "core"}, [2]string{"old", "legacy"}, [2]string{"app", "legacy"})
	git := newFakeGit(
		commit{sha: "c0", message: "release(legacy,old)%beta: start the train"},
		commit{sha: "c1", message: "feat(core)^: x\n---\nfeat(legacy)^: y"},
	).tag("core", "1.0.0", "").tag("cli", "1.0.0", "").tag("app", "1.0.0", "")
	git = onBeta(onBeta(git, "legacy", "c0"), "old", "c0")

	p := computeEquivalence(t, git, pkgs, deps)
	require.True(t, p.Releases["cli"].IsChanged(), "cli from the first unit: %v", codes(p))
	assert.Equal(t, []string{"core"}, p.Releases["cli"].DueTo)
	require.True(t, p.Releases["old"].IsChanged(), "old from the second unit: %v", codes(p))
	assert.Equal(t, []string{"legacy"}, p.Releases["old"].DueTo)
	app := p.Releases["app"]
	assert.False(t, app.IsChanged(), "stable app cannot resolve the beta-only legacy source")
	assert.Equal(t, ccme.BumpNone, app.PropagatedBump)
	assert.Empty(t, app.Sources)
	assert.True(t, hasCode(p, CodeBumpSuppressed), "W208, got %v", codes(p))
}

// TestDirectChannelCandidatesKeepPrecedenceOrder (spec vector 82e): the newer
// of two direct directives wins, and W186 reports both proposals.
func TestDirectChannelCandidatesKeepPrecedenceOrder(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"cli"})
	git := newFakeGit(
		commit{sha: "c1", message: "release(cli)%rc: older"},
		commit{sha: "c2", message: "release(cli)%beta: newer"},
	).tag("cli", "1.0.0", "")

	p := computeEquivalence(t, git, pkgs, deps)
	assert.Equal(t, "beta", p.Releases["cli"].Channel)
	assert.True(t, hasCode(p, CodeChannelConflict), "W186, got %v", codes(p))
}

// TestChannelConflictCountsProposalsNotCandidates (spec vector 82f): the older
// transition does not match ui's beta baseline, so it is a candidate and not
// a competitor, and no W186 is raised.
func TestChannelConflictCountsProposalsNotCandidates(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"ui"})
	git := onBeta(newFakeGit(
		commit{sha: "c0", message: "release(ui)%beta: start the train"},
		commit{sha: "c1", message: "release(ui)%rc>stable: older"},
		commit{sha: "c2", message: "release(ui)%beta>stable: newer"},
	), "ui", "c0")

	p := computeEquivalence(t, git, pkgs, deps)
	assert.Equal(t, "stable", p.Releases["ui"].Channel, "ui graduates from the newer directive")
	assert.False(t, hasCode(p, CodeChannelConflict), "no W186, got %v", codes(p))
}

// TestSkippedChannelPassStillResolvesEveryChannel (spec vector 82g): with
// channel depth 0 everywhere the channel pass proposes nothing, and channel(P)
// still comes from the baseline, so a propagated bump between two packages on
// the same train is admitted.
func TestSkippedChannelPassStillResolvesEveryChannel(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"core", "app"}, [2]string{"app", "core"})
	git := newFakeGit(
		commit{sha: "c0", message: "release(core,app)%beta: start the train"},
		commit{sha: "c1", message: "feat(core)^: x"},
	)
	git = onBeta(onBeta(git, "core", "c0"), "app", "c0")

	p := computeEquivalence(t, git, pkgs, deps)
	app := p.Releases["app"]
	assert.Equal(t, "beta", app.Channel, "channel(app) is its baseline")
	assert.Empty(t, app.ChannelFrom, "nothing was propagated on the channel axis")
	require.True(t, app.IsChanged(), "the bump is admitted on the shared train: %v", codes(p))
	assert.Equal(t, []string{"core"}, app.DueTo)
	assert.False(t, hasCode(p, CodeBumpSuppressed), "no W208, got %v", codes(p))
}

// TestSkippedChannelPropagationKeepsDirectChannels (spec vector 82h): channel
// depth 0 skips propagation, not §13.8, so core moves to beta and the bump to
// stable cli is suppressed.
func TestSkippedChannelPropagationKeepsDirectChannels(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"core", "cli"}, [2]string{"cli", "core"})
	git := newFakeGit(commit{sha: "c1", message: "feat(core)^%beta: x"}).
		tag("core", "1.0.0", "").tag("cli", "1.0.0", "")

	p := computeEquivalence(t, git, pkgs, deps)
	assert.Equal(t, "beta", p.Releases["core"].Channel)
	cli := p.Releases["cli"]
	assert.Equal(t, "stable", cli.Channel)
	assert.False(t, cli.IsChanged(), "the propagated bump is suppressed")
	assert.True(t, hasCode(p, CodeBumpSuppressed), "W208, got %v", codes(p))
}

// TestEqualDirectivesKeepTheirOwnInheritedChannel (spec vector 82i): two units
// with the same directive and target keep their own origins: alpha's inherits
// stable and proposes nothing, beta's moves app to rc.
func TestEqualDirectivesKeepTheirOwnInheritedChannel(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"alpha", "beta", "app"},
		[2]string{"app", "alpha"}, [2]string{"app", "beta"})
	git := newFakeGit(
		commit{sha: "c0", message: "release(beta)%rc: start the train"},
		commit{sha: "c1", message: "feat(alpha)^%%inherit++1: a"},
		commit{sha: "c2", message: "feat(beta)^%%inherit++1: b"},
	).tag("alpha", "1.0.0", "").tag("beta", "1.0.0", "").tag("beta", "1.0.1-rc.0", "c0").
		tag("app", "1.0.0", "")

	p := computeEquivalence(t, git, pkgs, deps)
	app := p.Releases["app"]
	assert.Equal(t, "rc", app.Channel, "the beta unit moves app to rc")
	assert.Equal(t, "beta", app.ChannelFrom, "and the move is credited to its own origin")
	require.True(t, app.IsChanged(), "%v", codes(p))
	require.Len(t, app.Sources, 2, "one contribution per unit, each with its own origin")
	assert.Equal(t, StaleSource{Provider: "alpha", Commit: "c1", commitKey: "c1", Level: 1, Bump: ccme.BumpPatch},
		app.Sources[0])
	assert.Equal(t, StaleSource{Provider: "beta", Commit: "c2", commitKey: "c2", Level: 1, Bump: ccme.BumpPatch},
		app.Sources[1])
	assert.False(t, hasCode(p, CodeBumpSuppressed), "no W208, got %v", codes(p))
	assert.False(t, hasCode(p, CodePropagatedChannelConflict), "alpha's unit proposes nothing to conflict with: %v",
		codes(p))
}

// TestEqualDirectivesAreAdmittedPerUnit (spec vector 82j): two feat(provider)^
// units, C1 on a branch that diverges from app's stable baseline and C2 behind
// it. provider released C2 and app's release carries that release, so only
// C1 contributes to app. lib, released before both, keeps C2 in the union.
func TestEqualDirectivesAreAdmittedPerUnit(t *testing.T) {
	git := &mergedGit{fakeGit: newFakeGit(), at: make(map[string]int), behind: make(map[int][]bool)}
	add := func(sha, message string, parents ...int) int {
		i := len(git.history)
		git.history = append(git.history, commit{sha: sha, message: message})
		git.parents = append(git.parents, parents)
		git.at[sha] = i
		return i
	}
	base := add("c0", "chore: base")
	c2 := add("c2", "feat(provider)^: two", base)
	baseline := add("cb", "fix(app): app's own release", c2)
	c1 := add("c1", "feat(provider)^: one", base)
	add("cm", "chore: merge", baseline, c1)
	git.tag("lib", "1.0.0", "c0").tag("provider", "1.0.0", "c0").tag("provider", "1.1.0", "c2").
		tag("app", "1.0.0", "cb")
	pkgs, deps := equivalenceWorkspace([]string{"provider", "app", "lib"}, [2]string{"app", "provider"})

	p := computeEquivalence(t, git, pkgs, deps)
	app := p.Releases["app"]
	require.True(t, app.IsChanged(), "%v", codes(p))
	assert.Equal(t, []StaleSource{{Provider: "provider", Commit: "c1", commitKey: "c1", Level: 1, Bump: ccme.BumpPatch}},
		app.Sources, "C2 is not in Wfresh(app) and was delivered")
	assertVersion(t, v(1, 2, 0), p.Releases["provider"].Next, "provider releases C1 alone")
}

// TestNoOpDirectChannelDoesNotHideAnOlderProposal (spec vector 82k): the newer
// directive proposes nothing (W199), the older one graduates ui, and with one
// proposal there is no W186.
func TestNoOpDirectChannelDoesNotHideAnOlderProposal(t *testing.T) {
	pkgs, deps := equivalenceWorkspace([]string{"ui"})
	git := onBeta(newFakeGit(
		commit{sha: "c0", message: "release(ui)%beta: start the train"},
		commit{sha: "c1", message: "release(ui)%stable: older"},
		commit{sha: "c2", message: "release(ui)%beta: newer"},
	), "ui", "c0")

	p := computeEquivalence(t, git, pkgs, deps)
	assert.Equal(t, "stable", p.Releases["ui"].Channel, "the older directive graduates ui")
	assert.True(t, hasCode(p, CodeChannelRedundant), "W199, got %v", codes(p))
	assert.False(t, hasCode(p, CodeChannelConflict), "no W186, got %v", codes(p))
}
