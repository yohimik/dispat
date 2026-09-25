// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: what a propagation walks past.
//
// Propagation is the part of a plan that is hardest to read back out of the
// result, because a dependent that was reached and rejected and a dependent
// that was never reached look identical in the graph: both say "unchanged".
// The scenarios here separate the two, by naming the reason each target was
// passed over — a scope term, a window that no longer holds the commit, a
// cancel barrier — and by following one that was reached through the trace
// log, which is where an operator has to go when a release did not bump what
// they expected.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailPropagationIsTraceableEndToEnd: a run at trace level accounts for
// every step a propagation took — which edge it crossed, which package it
// reached at which level, and where each package's channel came from — plus
// the scope each unit resolved to when the commit's own files were what
// decided it. One history exercises all of it, because the claim is that the
// trace is complete rather than that any one line exists.
func TestCovTailPropagationIsTraceableEndToEnd(t *testing.T) {
	r := covTailFanOutRepo(t)
	r.Commit("chore: seed the fleet")

	// No scope-set on either unit: both resolve from the files the commit
	// touched, which is the same question asked twice of one commit.
	r.WriteFile("packages/core/derived.txt", "work\n")
	r.Commit("feat^: work the files decide the scope of\n\n---\n\n" +
		"fix: a second record with no scope-set either")
	r.CommitEmpty("release(core)%beta%%beta: propose beta downstream")

	res := r.StatusOK("--log-level", "trace")
	assert.Contains(t, res.Stdout, "plan: package derived from the commit's files",
		"the derived scope is traceable: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "plan: propagation edge",
		"and so is each edge the walk crossed: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "plan: channel resolved",
		"and where the channel each package ends on came from: %s", res.Stdout)

	plain := r.StatusOK()
	assert.Equal(t, "stable -> beta", harness.GraphLine(plain.Events, "near").Str("channel"),
		"the dependents took the propagated channel: %s", plain.Stdout)
	assert.Equal(t, "patch", harness.GraphLine(plain.Events, "far").Str("bump"),
		"and the propagated bump: %s", plain.Stdout)
}

// TestCovTailChannelPropagationSkipsWhatItCannotAdmit: a propagated channel is
// admitted against the *target's* window, exactly as a propagated bump is. A
// unit whose own scope resolves to no package proposes nothing at all; a
// target whose window no longer holds the commit is past the proposal; and a
// target behind a cancel barrier has had the commit discarded. None of the
// three is an error, and none of them may move the package.
func TestCovTailChannelPropagationSkipsWhatItCannotAdmit(t *testing.T) {
	t.Run("a unit whose scope answers to nothing", func(t *testing.T) {
		r := covTailFanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(nothing*)%beta%%beta: a glob no package answers to")

		res := r.StatusOK()
		assert.True(t, harness.IsCodePresent(res.Events, "W134"),
			"the glob that matched nothing is reported: %s", res.Stdout)
		assert.Equal(t, "stable", harness.GraphLine(res.Events, "near").Str("channel"),
			"and nothing downstream moved: %s", res.Stdout)
	})

	t.Run("a target whose window is past the proposal", func(t *testing.T) {
		r := covTailFanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream")
		r.WriteFile("packages/near/later.txt", "work\n")
		r.Commit("fix(near): work released after the proposal was written")
		tagAt(r, "near@1.0.0", "HEAD")

		res := r.StatusOK()
		assert.Equal(t, "stable", harness.GraphLine(res.Events, "near").Str("channel"),
			"the proposal is behind this package's baseline: %s", res.Stdout)
		assert.Equal(t, "stable -> beta", harness.GraphLine(res.Events, "far").Str("channel"),
			"while the dependent whose window still holds it takes the channel: %s", res.Stdout)
	})

	t.Run("a target behind a cancel barrier", func(t *testing.T) {
		r := covTailFanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream")
		r.CommitEmpty("release(far)%rc: a direct directive of its own")
		r.CommitEmpty("cancel(near,far): discard everything written so far")

		res := r.StatusOK()
		assert.Equal(t, "stable", harness.GraphLine(res.Events, "near").Str("channel"),
			"the cancelled proposal does not reach the dependent: %s", res.Stdout)
		assert.Equal(t, "stable", harness.GraphLine(res.Events, "far").Str("channel"),
			"nor does the cancelled direct directive: %s", res.Stdout)
	})
}

// TestCovTailTwoProvidersProposeDifferentChannels: a dependent of two
// providers can be handed two channels in one run. The newer commit wins, and
// the conflict is reported against the dependent, because taking one silently
// would make the outcome depend on a traversal order the message does not
// show.
func TestCovTailTwoProvidersProposeDifferentChannels(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "near", Provider: "core"},
		{Consumer: "near", Provider: "lib"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "lib", "near"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("chore: seed the fleet")
	r.CommitEmpty("release(core)%beta%%beta: the first provider proposes beta")
	r.CommitEmpty("release(lib)%rc%%rc: the second provider proposes rc")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W160", "near"),
		"the two proposals conflict: %s", res.Stdout)
	assert.Equal(t, "stable -> rc", harness.GraphLine(res.Events, "near").Str("channel"),
		"and the newer commit's proposal is the one taken: %s", res.Stdout)
}

// TestCovTailPropagationScopeExcludesByName: a scope-set may be written as the
// workspace less a package, which is how a repository keeps one dependent off
// a propagation without listing every other one. The exclusion is applied
// after the inclusion and wins.
func TestCovTailPropagationScopeExcludesByName(t *testing.T) {
	r := covTailFanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core)^: work that propagates one level\n\nPropagate-Scope: *,-far")

	res := r.StatusOK()
	assert.Equal(t, "patch", harness.GraphLine(res.Events, "near").Str("bump"),
		"the dependent the scope admits is bumped: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "far").Str("message"),
		"and the one it excludes is not: %s", res.Stdout)
}

// TestCovTailFixedGroupWithAHeldMember: a fixed group moves its members
// together, and a member held by `Release-As: none` stays behind. Holding one
// member must not stop the group, and the group's own target must not lift the
// hold — the two decisions are independent, which is exactly what makes the
// combination worth stating.
func TestCovTailFixedGroupWithAHeldMember(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.VersionGroups = map[string]models.VersionGroupConfig{
		"platform": {Versioning: models.VersioningFixed},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), VersionGroup: "platform"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "alpha")
	r.SeedPackage("packages", "beta")
	r.Commit("feat(alpha,beta): bootstrap the group")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/alpha/work.txt", "work\n")
	r.Commit("feat(alpha): work that moves the group")
	r.WriteFile("packages/beta/work.txt", "work\n")
	r.Commit("feat(beta): work of its own, held back\n\nRelease-As: none")

	res := r.StatusOK("--log-level", "trace")
	assert.Contains(t, res.Stdout, "plan: fixed group unified",
		"the group alignment is traceable: %s", res.Stdout)

	plain := r.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(plain.Events, "alpha").Str("version"),
		"the group still moves: %s", plain.Stdout)
	assert.Contains(t, harness.GraphLine(plain.Events, "beta").Str("message"), "held",
		"while the held member stays where it is: %s", plain.Stdout)
}
