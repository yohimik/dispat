// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// What a choreographed plan reads out of the links: where a consumer's window
// in another repository begins, and what happens when the links cannot prove
// it.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestChoreographyPlansAFirstReleaseWithoutEvidence: a fleet that has never
// released has no boundary to prove, and needs no tuple to say so.
func TestChoreographyPlansAFirstReleaseWithoutEvidence(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	res := fleet.peer("api").StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Equal(t, "minor", harness.GraphLine(res.Events, "api-pkg").Str("bump"))
}

// TestChoreographyReadsTheBoundaryFromTheReleasedLink: the pin a release
// recorded is where the consumer's window in the provider starts, so work the
// release already incorporated is not counted again and work after it is.
func TestChoreographyReadsTheBoundaryFromTheReleasedLink(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")

	// Nothing has happened since, so nothing is pending anywhere.
	settled := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, settled, "E333")
	assert.Empty(t, harness.GraphLine(settled.Events, "api-pkg").Str("bump"))

	// A fix in the provider, pushed and followed, reaches the consumer as
	// catch-up work measured from the recorded pin.
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg)^: repair the stream")
	res := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Equal(t, "patch", harness.GraphLine(res.Events, "sdk-pkg").Str("bump"))
	assert.Equal(t, "patch", harness.GraphLine(res.Events, "api-pkg").Str("bump"),
		"the consumer releases for its provider's new work and nothing older")
}

// TestChoreographyRelaysTheBoundaryAlongTheRoute: two repositories that do
// not link each other are still comparable, one hop at a time.
func TestChoreographyRelaysTheBoundaryAlongTheRoute(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "core", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "core-pkg"}}
	})
	fleet.peer("api").Commit("chore: depend on the far end of the route")
	fleet.push("api")
	// api - sdk - core: the consumer and its provider are two hops apart.
	fleet.link("api", "sdk")
	fleet.link("sdk", "core")
	fleet.follow("api", "sdk")
	api := fleet.peer("api")
	// Every hop of the route has to be a checkout: the copy of core inside
	// api's copy of sdk is the one this run reads.
	fleet.materialize(api.Repo, ".links/sdk", "core")

	api.ReleaseOK("--package", "*")
	assert.Equal(t, []string{"api-pkg@0.1.0"}, api.TagList())
	assert.Equal(t, []string{"sdk-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk"))
	assert.Equal(t, []string{"core-pkg@0.1.0"}, tagsIn(api.Repo, ".links/sdk/.links/core"))

	res := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Empty(t, harness.GraphLine(res.Events, "api-pkg").Str("bump"),
		"the relayed boundary leaves nothing pending")
}

// TestChoreographyNeedsATupleWithoutAReleaseCommit: a tag attached by hand
// proves nothing about what the release incorporated, and the remedy the
// diagnostic names is the one that works.
func TestChoreographyNeedsATupleWithoutAReleaseCommit(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	api.Git("tag", "-a", "api-pkg@0.1.0", "-m", "tagged by hand")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work after the hand-made tag")

	res := api.Status("--package", "*")
	assert.NotZero(t, res.Code)
	requireDiagnostic(t, res, "E333")
	assert.Contains(t, res.Stdout+res.Stderr, "repositoryBaselines")

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: provider}}
	})
	api.Commit("chore: state the boundary the links cannot prove")
	fixed := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, fixed, "E333")
	assert.NotEmpty(t, harness.GraphLine(fixed.Events, "sdk-pkg").Str("bump"),
		"the plan the tuple unblocked is an ordinary plan")
}

// handTaggedBoundaryFleet is the fleet the explicit-boundary scenarios read:
// api-pkg@0.1.0 tagged by hand, so no release commit proves what it
// incorporated and only a tuple can, and a propagating fix in sdk after the
// revision api's checkout pinned. With isProviderReleased, sdk-pkg@0.1.0 is
// tagged by hand at that fix and pushed, so the provider has released its work
// and the declared revision alone decides whether api-pkg is still owed it.
// It returns the fleet, the revision before the work and the work commit.
func handTaggedBoundaryFleet(t *testing.T, isProviderReleased bool) (*choreographyFleet, string, string) {
	t.Helper()
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	before := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	api.Git("tag", "-a", "api-pkg@0.1.0", "-m", "tagged by hand")
	api.Git("push", "-q", "origin", "api-pkg@0.1.0")
	work := fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg)^: work after the hand-made tag")
	if isProviderReleased {
		api.Git("-C", ".links/sdk", "tag", "-a", "sdk-pkg@0.1.0", "-m", "released by hand", work)
		api.Git("-C", ".links/sdk", "push", "-q", "origin", "sdk-pkg@0.1.0")
	}
	return fleet, before, work
}

// sdkBaseline is the one tuple these scenarios declare: api-pkg@0.1.0
// incorporated sdk through revision.
func sdkBaseline(revision string) []models.RepositoryBaselineConfig {
	return []models.RepositoryBaselineConfig{{
		Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: revision}}
}

// TestChoreographyReadsABaselineDeclaredByThePeerThatKnowsIt: a boundary is a
// statement about two repositories, and with no control file the run reads it
// wherever the fleet wrote it down. The declared revision is what the
// consumer's window in the provider starts from, so a propagating commit after
// it reaches the consumer.
func TestChoreographyReadsABaselineDeclaredByThePeerThatKnowsIt(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	api.Git("tag", "-a", "api-pkg@0.1.0", "-m", "tagged by hand")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg)^: work after the hand-made tag")

	// The tuple lives in the provider's configuration, not the entry's.
	fleet.configureIn(api.Repo, "sdk", "chore: state the boundary here", func(cfg *models.File) {
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: provider}}
	})

	res := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Equal(t, "propagated from sdk-pkg", harness.GraphLine(res.Events, "api-pkg").Str("reason"),
		"the work after the declared revision is in the consumer's window: %s", res.Stdout)
}

// TestChoreographyDoesNotCountALinkMoveAsAChange: a settlement writes a
// pointer to another repository, and counting it would release the package
// enclosing the link every time a neighbour published.
func TestChoreographyDoesNotCountALinkMoveAsAChange(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")

	// The provider moves and the fleet releases again, which records a new
	// pin in the consumer.
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): more work")
	api.ReleaseOK("--package", "*")
	pin := gitlinkAt(api.Repo, "HEAD", ".links/sdk")
	assert.NotEmpty(t, pin)
	api.Git("-C", ".links/sdk", "merge-base", "--is-ancestor", pin, "HEAD")

	// Nothing is pending afterwards: the link move is not the package's own
	// work, however many commits it left behind.
	res := api.StatusOK("--package", "*")
	assert.Empty(t, harness.GraphLine(res.Events, "api-pkg").Str("bump"))
	assert.Empty(t, harness.GraphLine(res.Events, "sdk-pkg").Str("bump"))
}

// TestChoreographySinceProjectsThroughTheFleet: `--since` is one range per
// repository, projected through the links from the entry's revision.
func TestChoreographySinceProjectsThroughTheFleet(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["tests"] = models.Script{"printf '%s\\n' \"$DISPAT_PACKAGE\""}
	})
	api.Commit("chore: add a test script")
	fleet.configureIn(api.Repo, "sdk", "chore: add a test script", func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{"printf '%s\\n' \"$DISPAT_PACKAGE\""}
	})
	api.Git("add", ".links/sdk")
	api.Commit("chore: follow the provider's test script")
	base := api.Git("rev-parse", "HEAD")

	// Work in the provider after that revision, recorded in the consumer.
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work after the base revision")
	api.Git("add", ".links/sdk")
	api.Commit("chore: follow the provider")

	res := api.RunScriptOK("tests", "--since", base)
	assert.Contains(t, res.Stdout, "sdk-pkg", "the provider's range is what the base revision pinned")

	all := api.RunScriptOK("tests", "--since", "all")
	assert.Contains(t, all.Stdout, "api-pkg")
	assert.Contains(t, all.Stdout, "sdk-pkg")
}

// TestChoreographyRefusesAnUnprovableBoundaryRatherThanGuessing: a pin the
// provider's checkout does not hold is an answer, not a Git failure.
func TestChoreographyRefusesAnUnprovableBoundaryRatherThanGuessing(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")

	// Rewrite the recorded link to a revision nothing in the fleet holds.
	absent := api.Git("rev-parse", "HEAD")
	api.Git("update-index", "--add", "--cacheinfo", "160000", absent, ".links/sdk")
	api.Git("commit", "-q", "-m", "chore: point the link somewhere impossible")
	api.Git("tag", "-d", "api-pkg@0.1.0")
	api.Git("tag", "-a", "api-pkg@0.1.0", "-m", "chore(release): api-pkg@0.1.0")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work the consumer never saw")

	res := api.Status("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E333")
	require.NotContains(t, res.Stdout+res.Stderr, "exit status 128", "a missing object is not a Git failure")
}

// TestChoreographySinceARevisionThatPinsNothing: a revision from before a
// link existed pins nothing for that repository, which projects to the empty
// revision — every commit it has — exactly as an absent gitlink always has.
func TestChoreographySinceARevisionThatPinsNothing(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	api := fleet.peer("api")
	base := api.Git("rev-parse", "HEAD")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{"printf '%s\n' \"$DISPAT_PACKAGE\""}
	})
	api.Commit("chore: add a test script")
	fleet.configureIn(api.Repo, "sdk", "chore: add a test script", func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{"printf '%s\n' \"$DISPAT_PACKAGE\""}
	})

	res := api.RunScriptOK("tests", "--since", base)
	assert.Contains(t, res.Stdout, "api-pkg")
	assert.Contains(t, res.Stdout, "sdk-pkg",
		"a repository that revision pins nothing for contributes its whole history")
}

// TestChoreographyReadsEveryReleaseSubjectOfARepositoryAtOnce: a repository
// holding two released packages is asked for both subjects in one read, and
// both boundaries resolve from them.
func TestChoreographyReadsEveryReleaseSubjectOfARepositoryAtOnce(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	sdk := fleet.peer("sdk")
	sdk.SeedPackage("packages", "sdk-extra")
	sdk.Commit("feat(sdk-extra): a second package in one repository")
	fleet.push("sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{
			{Consumer: "api-pkg", Provider: "sdk-pkg"},
			{Consumer: "api-pkg", Provider: "sdk-extra"},
		}
	})
	fleet.peer("api").Commit("chore: depend on both packages of one repository")
	fleet.push("api")
	fleet.link("api", "sdk")
	api := fleet.peer("api")

	api.ReleaseOK("--package", "*")
	assert.ElementsMatch(t, []string{"sdk-pkg@0.1.0", "sdk-extra@0.1.0"}, tagsIn(api.Repo, ".links/sdk"))

	fleet.workIn(api.Repo, "sdk", "sdk-extra", "fix(sdk-extra)^: repair the second package")
	res := api.StatusOK("--package", "*")
	requireNoDiagnostic(t, res, "E333")
	assert.Equal(t, "patch", harness.GraphLine(res.Events, "api-pkg").Str("bump"),
		"both boundaries resolved from the subjects read in one pass")
}

// TestChoreographyIncomparableDirectivesStayE334: two repositories can write
// intent no history orders, and a fleet with no repository above the others
// cannot resolve that for them. The refusal stays E334 and names a remedy
// that exists here: withdraw the conflicting intent, or restate it where the
// packages live.
func TestChoreographyIncomparableDirectivesStayE334(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "core", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{
			{Consumer: "api-pkg", Provider: "sdk-pkg"},
			{Consumer: "api-pkg", Provider: "core-pkg"},
		}
	})
	fleet.peer("api").Commit("chore: depend on two repositories")
	fleet.push("api")
	fleet.link("api", "sdk")
	fleet.link("api", "core")
	api := fleet.peer("api")

	// Each provider proposes a different channel for the same consumer, from
	// a revision the other's history cannot be ordered against.
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "feat(sdk-pkg)^%beta++1: bring the consumer onto beta")
	fleet.workIn(api.Repo, "core", "core-pkg", "feat(core-pkg)^%alpha++1: bring the consumer onto alpha")

	res := api.Status("--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E334")
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "withdraw the conflicting intent")
	assert.NotContains(t, combined, "control directive",
		"a fleet with no control repository cannot be told to write one")
}

// TestChoreographyNeedsATupleForALinkAddedAfterTheRelease: a release made
// before two repositories were linked recorded no hop between them, so its
// tree proves nothing about what it incorporated. The plan says so with E333
// rather than reading an absent pin as the empty revision, and the tuple the
// diagnostic names is what states the boundary that release actually had.
func TestChoreographyNeedsATupleForALinkAddedAfterTheRelease(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	api := fleet.peer("api")
	// api releases while it is a fleet of one: nothing in its tree names sdk.
	api.ReleaseOK("--package", "api-pkg")
	require.Equal(t, 1, api.TagCount("api-pkg@"))

	fleet.link("api", "sdk")
	provider := api.Git("-C", ".links/sdk", "rev-parse", "HEAD")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
	})
	api.Commit("chore: depend on the repository it has just been linked to")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg): work the old release never saw")

	res := api.Status("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E333")
	assert.Contains(t, res.Stdout+res.Stderr, "records no link to sdk")

	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.RepositoryBaselines = []models.RepositoryBaselineConfig{{
			Consumer: "api-pkg", ReleaseTag: "api-pkg@0.1.0", Repository: "sdk", Revision: provider}}
	})
	api.Commit("chore: state the boundary the old release cannot prove")
	requireNoDiagnostic(t, api.StatusOK("--package", "*"), "E333")
}

// TestChoreographyRefusesAPinTheProviderMovedBehind: a recorded pin has to be
// one the provider's active revision descends from. A branch reset behind it
// leaves the pin present but unreachable, which is an unprovable boundary —
// E333 naming the remedy — and never a Git failure.
func TestChoreographyRefusesAPinTheProviderMovedBehind(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	pin := gitlinkAt(api.Repo, "HEAD", ".links/sdk")
	require.NotEmpty(t, pin)

	// The provider's checkout is reset behind the revision the consumer's
	// release recorded, and then moves along its own line.
	api.Git("-C", ".links/sdk", "reset", "--hard", "-q", pin+"~1")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "fix(sdk-pkg)^: work on the line the reset left")

	res := api.Status("--package", "*")
	assert.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E333")
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "is not reachable from its active revision")
	require.NotContains(t, combined, "exit status 128", "an unreachable pin is not a Git failure")
}
