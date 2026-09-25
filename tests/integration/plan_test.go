// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Area 3: plan logic. Most tests here are flowing multi-run scenarios in
// the style of services/dispat/internal/cli's own end-to-end suite: several
// related cases packed into one narrative, because each additional run of
// an existing repository is far cheaper than a fresh fixture — and because
// the property most worth checking usually *is* the relation between runs
// (catch-up, convergence, a cancel's irreversibility). The exceptions are
// the pin guards, which get isolated repositories precisely so a rejected
// pin cannot collide with a tag an earlier step created.
//
// Every scenario reads diagnostics from --log-format json events, never
// from pretty console text, and cross-checks outcomes against real git
// tags. Where the claim is that the plan *drove execution* — a held or
// cancelled package must not run scripts, a resumed one must — the build
// script is markerBuild and buildRuns() counts its executions.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPlanCancelSemantics: cancel only reaches backwards (configuration.md,
// "Release control"). A cancelled commit's contribution is discarded for
// good — not merely deferred — so the next release reflects only what
// landed after the cancel; a cancel with nothing left pending is a no-op
// that says so (W170, the sign it probably named the wrong package); and a
// package whose only pending work was cancelled runs no scripts at all.
func TestPlanCancelSemantics(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): work about to be cancelled")
	r.CommitEmpty("cancel(core): abandon it")

	r.ReleaseOK()
	assert.Empty(t, r.TagList(), "nothing pending: no tag of any kind")
	assert.Zero(t, buildRuns(r), "a package releasing nothing must execute nothing")

	// Work landing after the cancel accumulates normally, and on its own: if
	// the cancelled feat had survived, this would be 0.1.1, not 0.0.1.
	r.WriteFile("packages/core/repair.txt", "x")
	r.Commit("fix(core): repair after the cancellation")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.0.1"), "tags: %v", r.TagList())
	assert.False(t, r.IsTagged("core@0.1.1"), "the cancelled feat must never resurface")
	assert.Equal(t, 1, buildRuns(r), "one release, one build")

	// A cancel with nothing left pending — everything already released — is
	// an empty cancel: warned about, releasing nothing, running nothing.
	r.CommitEmpty("cancel(core): too late, it already shipped")
	res := r.ReleaseOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W170"), "an empty cancel must be warned about")
	assert.Equal(t, 1, r.TagCount("core@"), "no new tag from a no-op cancel")
	assert.Equal(t, 1, buildRuns(r), "and no scripts either")

	// A hold is a record like any other, so a cancel takes it with everything
	// else it discards: work written after the barrier releases with nothing
	// holding it.
	r.WriteFile("packages/core/abandoned.txt", "x")
	r.Commit("feat(core): work that was going to be held")
	r.CommitEmpty("release(core): hold it back\n\nRelease-As: none")
	r.CommitEmpty("cancel(core): start over from here")
	r.WriteFile("packages/core/fresh.txt", "x")
	r.Commit("fix(core): work written after the barrier")
	line := harness.GraphLine(r.StatusOK().Events, "core")
	assert.Equal(t, "0.0.1 -> 0.0.2", line.Str("version"), "only the work after the barrier counts")
	assert.NotContains(t, line.Str("message"), "held", "and the discarded hold holds nothing")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.0.2"), "tags: %v", r.TagList())
	assert.Equal(t, 2, buildRuns(r), "one more release, one more build")
}

// TestPlanRequireRelease: --require-release is the CI/CD gate over the empty
// plan. Releasing nothing is an ordinary no-op that exits 0 — right for a
// person at a terminal, wrong for a pipeline stage whose whole point is that
// this run publishes something, which would otherwise pass quietly and let
// the pipeline carry on to deploy nothing. What counts is what will actually
// be published: a package the plan holds back is not it.
func TestPlanRequireRelease(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("chore(core): nothing a release cares about")

	// Nothing pending. The graph is printed either way — the refusal comes
	// after the plan that explains it, never instead of it.
	res := r.StatusOK()
	require.Equal(t, "unchanged", harness.GraphLine(res.Events, "core").Str("message"))

	res = r.Status("--require-release")
	assert.Equal(t, 3, res.Code, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "core").Str("message"),
		"the plan is still printed before the refusal")

	r.ReleaseOK() // the no-op contract, unchanged without the flag
	assert.Equal(t, 3, r.Release("--require-release").Code)
	assert.Empty(t, r.TagList(), "a refused run releases nothing")
	assert.Zero(t, buildRuns(r), "and executes nothing")

	// Something to release: the gate opens, and the release is the release it
	// always was.
	r.WriteFile("packages/core/work.txt", "x")
	r.Commit("feat(core): something to release")
	res = r.StatusOK("--require-release")
	assert.Equal(t, "● changed", harness.GraphLine(res.Events, "core").Str("message"))
	r.ReleaseOK("--require-release")
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, buildRuns(r))

	// Held is not releasing. The package has a version waiting for a later
	// run, but this one publishes nothing, so the gate stays shut.
	r.WriteFile("packages/core/held.txt", "x")
	r.Commit("feat(core): work held back\n---\nrelease(core): hold it\n\nRelease-As: none\n")
	res = r.Status("--require-release")
	assert.Equal(t, 3, res.Code, "a held package is not something this run releases")
	assert.Equal(t, "‖ held (Release-As: none)",
		harness.GraphLine(res.Events, "core").Str("message"))
	assert.Equal(t, 3, r.Release("--require-release").Code)
	assert.Equal(t, 1, r.TagCount("core@"), "still just the one release")
	assert.Equal(t, 1, buildRuns(r), "and still just the one build")
}

// TestPlanHoldResumeAndReleaseAsAuto walks release control end to end:
// hold, held-version reporting (W154), resume at the accumulated max(), a
// redundant resume with nothing left to lift (W158) — and, throughout, that
// a held package is excluded from *execution*, not merely from tagging.
func TestPlanHoldResumeAndReleaseAsAuto(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)

	// Hold: the same commit both earns a minor bump and withholds it.
	r.Commit("feat(core): first work\n---\nrelease(core): hold immediately\n\nRelease-As: none\n")
	res := r.ReleaseOK()
	assert.Empty(t, r.TagList(), "a held package must not be tagged")
	assert.Zero(t, buildRuns(r), "a held package must not run any stage script")
	assert.True(t, harness.IsCodePresent(res.Events, "W154"), "the version it would have released must be reported")

	// More work while held: still held, still nothing tagged or executed.
	r.WriteFile("packages/core/more.txt", "x")
	r.Commit("fix(core): more work while held")
	r.ReleaseOK()
	assert.Empty(t, r.TagList(), "still held")
	assert.Zero(t, buildRuns(r), "still excluded from execution")

	// Resume at the accumulated max(): the feat's minor wins over the fix.
	r.CommitEmpty("release(core): resume\n\nRelease-As: auto\n")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, buildRuns(r), "the resumed release executes exactly once")

	// A redundant auto with nothing held: W158, no-op.
	r.CommitEmpty("release(core): redundant auto\n\nRelease-As: auto\n")
	res = r.ReleaseOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W158"))
	assert.Equal(t, 1, r.TagCount("core@"))
}

// TestPlanExactPinGuards checks the three exact-`Release-As` guards on a
// bare `release(pkg)` directive with nothing else pending — the shape where
// a rejected pin's fallback has nothing to compute, so the package is simply
// left unreleased. (A rejected pin paired with a sibling bump falls back to
// the bump's computed version instead; that half is
// TestPlanRejectedPinFallsBackToTheComputedBump.) Each guard gets a fresh
// repository so a rejected pin can never collide with a tag a previous case
// created.
func TestPlanExactPinGuards(t *testing.T) {
	t.Run("E153_not_greater_than_baseline", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): establish a baseline")
		r.ReleaseOK()
		require.True(t, r.IsTagged("core@0.1.0"))

		r.CommitEmpty("release(core): pin backwards, alone\n\nRelease-As: 0.1.0\n")
		res := r.ReleaseOK() // under commitErrors: warn a rejected pin does not fail the run
		assert.True(t, harness.IsCodePresent(res.Events, "E153"))
		assert.Equal(t, 1, r.TagCount("core@"), "nothing to fall back to: no new tag")
	})

	t.Run("E157_major_jump_too_large", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.CommitEmpty("release(core): pin far ahead of an untouched package\n\nRelease-As: 5.0.0\n")
		res := r.ReleaseOK()
		assert.True(t, harness.IsCodePresent(res.Events, "E157"))
		assert.Empty(t, r.TagList())
	})

	t.Run("E154_via_glob", func(t *testing.T) {
		// Two written includes are refused by the parser; a glob's breadth
		// is invisible in the text and needs the workspace. The rejected pin
		// has a unit's blast radius: the work still releases as computed.
		r := singlePackageRepo(t, echoBuild)
		r.SeedPackage("packages", "coreutils")
		r.Commit("feat(core,coreutils): bootstrap both packages")
		r.ReleaseOK()
		r.Commit("chore(release): record the changelog")
		r.WriteFile("packages/core/work.txt", "work\n")
		r.WriteFile("packages/coreutils/work.txt", "work\n")
		r.Commit("feat(core*): work in both, pinned as if it were one\n\nRelease-As: 1.2.3")

		res := r.Status()
		assert.True(t, harness.IsCodePresent(res.Events, "E154"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "applies to 2 packages", "the run says how many it reached")
		assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(res.Events, "core").Str("version"))
	})

	t.Run("E154_multi_package_pin", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.SeedPackage("packages", "utils")
		r.Commit("chore(release): bootstrap two packages")
		r.CommitEmpty("release(core,utils): pin both at once\n\nRelease-As: 3.0.0\n")
		res := r.ReleaseOK()
		assert.True(t, harness.IsCodePresent(res.Events, "E154"))
		assert.Empty(t, r.TagList())
	})
}

// TestPlanRejectedPinFallsBackToTheComputedBump: a rejected exact pin has
// §16's unit-scoped blast radius — the bad `release` unit contributes
// nothing, and a genuine bump in the *same* commit (a `feat` unit separated
// by `---`) still releases at its ordinarily-computed version. This was
// originally a regression fence for the opposite, observed behaviour (the
// package tagged its unchanged 0.0.0 baseline, silently discarding the
// feature); the planner now falls back correctly and the fence guards the
// fix.
func TestPlanRejectedPinFallsBackToTheComputedBump(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.CommitEmpty("feat(core): needs a minor\n---\nrelease(core): pin too low\n\nRelease-As: 0.0.5\n")

	res := r.ReleaseOK() // under commitErrors: warn the rejected pin does not fail the run
	assert.True(t, harness.IsCodePresent(res.Events, "E156"), "the below-bump guard still fires")
	assert.True(t, r.IsTagged("core@0.1.0"),
		"the feat's own minor bump releases despite the rejected pin — tags: %v", r.TagList())
	assert.False(t, r.IsTagged("core@0.0.0"), "the unchanged baseline must not be tagged")

	// Converged: the discharged feat and the spent pin release nothing more.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"))
}

// TestPlanConsumerFailureCatchesUpAfterProviderPublished is the "consumer
// failed to release" half of the partial-publish pair: the provider
// publishes and is tagged; the consumer's build fails in the same run. A
// later run with no new commits must catch the consumer up to exactly the
// version it was owed, without re-releasing the provider — and must label
// it a catch-up (W193), because nothing in the commit log alone would
// explain why it released.
func TestPlanConsumerFailureCatchesUpAfterProviderPublished(t *testing.T) {
	r := linkedRepo(t, "core", "app", failIfMarker)
	r.WriteFile("packages/app/FAIL", "x")
	r.Commit("feat(core)^: reaches app, which is about to fail its build")

	res := r.Release()
	require.Equal(t, 1, res.Code, "app's failure must fail the run\nstdout:\n%s", res.Stdout)
	assert.True(t, r.IsTagged("core@0.1.0"), "the independent provider still published")
	assert.Zero(t, r.TagCount("app@"), "app must not be tagged on a failed build")

	r.Remove("packages/app/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.IsTagged("app@0.0.1"), "app catches up to the version it was owed")
	assert.Equal(t, 1, r.TagCount("core@"), "core must not be re-released for a catch-up")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W193", "app"), "a catch-up must be labelled as one")

	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("app@"), "converged: no repeat catch-up")
}

// TestPlanProviderBuildFailureBlocksConsumerThenHeals is the "provider
// failed to release" half: the provider's build fails, so the consumer —
// whose only reason to release is propagation from that provider — must
// never attempt to build or publish against a release that never happened.
// It is reported blocked (W194), not silently dropped. Once the provider is
// fixed, both release together in the same run — and that is *not* a
// catch-up, because the provider had not published in any earlier run.
func TestPlanProviderBuildFailureBlocksConsumerThenHeals(t *testing.T) {
	r := linkedRepo(t, "core", "app", failIfMarker)
	r.WriteFile("packages/core/FAIL", "x")
	r.Commit("feat(core)^: about to fail its own build, with a dependent consumer")

	res := r.Release()
	require.Equal(t, 1, res.Code, "core's failure must fail the run\nstdout:\n%s", res.Stdout)
	assert.Zero(t, r.TagCount("core@"), "core must not be tagged")
	assert.Zero(t, r.TagCount("app@"), "app must not release against a provider that never built")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "app"), "app must be reported blocked, not silently absent")

	r.Remove("packages/core/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"))
	assert.True(t, r.IsTagged("app@0.0.1"))
	assert.False(t, harness.IsCodePresent(res.Events, "W194"), "nothing is blocked once the provider heals")
	assert.False(t, harness.IsCodePresent(res.Events, "W193"), "same-run propagation is not a catch-up")
}

// TestPlanCatchUpWholeHistoryForNeverReleasedConsumer: the window rule
// taken to its least intuitive conclusion (architecture.md: "a consumer
// that was never released while a provider has been is the same case, with
// the whole history as its window"). A package added *after* a provider's
// propagating commit still catches up to it on its very first run — an
// untagged package's window is not "since it started existing", it is
// everything.
func TestPlanCatchUpWholeHistoryForNeverReleasedConsumer(t *testing.T) {
	configFor := func(deps []models.DependencyConfig) models.File {
		cfg := libsConfig(echoBuild, 1)
		cfg.Dependencies = deps
		return cfg
	}
	r := harness.New(t)
	r.WriteConfigModel(configFor(nil))
	r.SeedPackage("packages", "core")
	// A caret with no matching consumer yet: harmless, core just releases.
	r.Commit("feat(core)^: v1, propagating to a consumer that does not exist yet")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"))

	// consumer arrives later, wired into the graph in the very commit that
	// creates it. "release" is exempt from scope resolution by default, so
	// the bootstrap commit itself contributes nothing.
	r.SeedPackage("packages", "consumer")
	r.WriteConfigModel(configFor([]models.DependencyConfig{{Consumer: "consumer", Provider: "core"}}))
	r.Commit("chore(release): wire up the new consumer package")

	res := r.ReleaseOK()
	assert.True(t, r.IsTagged("consumer@0.0.1"),
		"consumer's window has no tag to bound it, so it still contains core's old propagating commit; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@"), "core itself has nothing new pending")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W193", "consumer"))
}

// TestPlanPrereleaseTrainWeirdCases covers a two-package train: a caret
// that cannot oblige a stable consumer is suppressed and reported (W208),
// an explicit channel-propagating caret brings the consumer onto the train
// instead, and one graduation directive naming both packages ends the train
// — after which a no-op run changes nothing for either package.
func TestPlanPrereleaseTrainWeirdCases(t *testing.T) {
	r := linkedRepo(t, "core", "consumer", echoBuild)
	r.Commit("chore: bootstrap both packages")

	// A caret reaches consumer, but with no channel propagation a stable
	// consumer cannot resolve a beta provider: suppressed, core alone
	// enters beta.
	r.CommitEmpty("feat(core)^%beta: start the train, but only core moves")
	res := r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("consumer@"), "a stable consumer cannot be dragged onto a prerelease")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W208", "consumer"), "the suppression must be reported")

	// This time the channel explicitly propagates: both packages join the
	// train together.
	r.CommitEmpty("fix(core)^%beta++1: bring the consumer along this time")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("consumer@0.0.1-beta.0"), "tags: %v", r.TagList())

	// One graduation directive naming both packages directly — one legitimate
	// way to end a train; the propagated "%%beta>stable" transition from a
	// directive on core alone is the other, covered by
	// TestPlanPropagatedGraduationTransitionGraduatesTheTrain.
	beforeGraduation := len(r.TagList())
	r.CommitEmpty("release(core,consumer)%beta>stable: graduate the whole train")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("consumer@0.0.1"), "tags: %v", r.TagList())

	// Converged: a re-run with no new commits changes nothing, proving the
	// multi-package graduation discharged cleanly.
	r.ReleaseOK()
	assert.Equal(t, beforeGraduation+2, len(r.TagList()), "no repeat release for either package")
}

// TestPlanPropagatedGraduationTransitionGraduatesTheTrain: configuration.md's
// worked example — `release(core)%beta>stable%%beta>stable++N: graduate core
// and everything still on beta behind it` — works as documented: a
// propagated *transition* is the deliberate exception to "a propagated
// stable never graduates a dependant" (its author had to name the train
// being ended in order to write it), so the consumer graduates together with
// core, and the graduated train converges. This was originally a regression
// fence for the opposite, observed behaviour (the propagated transition was
// refused with W200 and reported unmatched, W206); the planner now carries
// the exception through and the fence guards the fix. A propagated *bare*
// `stable` is still suppressed — that half lives in
// TestPlanPrereleaseTrainWeirdCases' W208 case and the unit suites.
func TestPlanPropagatedGraduationTransitionGraduatesTheTrain(t *testing.T) {
	r := linkedRepo(t, "core", "consumer", echoBuild)
	r.CommitEmpty("feat(core)^%beta++1: start the train, bringing the consumer too")
	r.ReleaseOK()
	require.True(t, r.IsTagged("consumer@0.0.1-beta.0"))

	r.CommitEmpty("release(core)%beta>stable%%beta>stable++1: graduate the whole train")
	res := r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "core's own direct transition graduates it; tags: %v", r.TagList())
	assert.True(t, r.IsTagged("consumer@0.0.1"),
		"the propagated transition graduates the consumer too; tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W200", "consumer"),
		"a transition is not a suppressed graduation")
	assert.False(t, harness.IsCodePresent(res.Events, "W206"), "the transition matched the consumer it reached")

	// Converged: the graduated train has nothing left to do.
	before := len(r.TagList())
	r.ReleaseOK()
	assert.Equal(t, before, len(r.TagList()))
}

// TestPlanTrainCatchUpStaysACatchUp: the catch-up scan on a prerelease train.
// app's own feature is published train history (beta.0), core released the
// propagating fix in a run whose app leg failed, and the healing run's whole
// fresh cause is that already-published propagation. W193 and the catch-up
// verdict must survive the train-wide own bump — before 1.0.0 the published
// feature hid them and the plan read "changed / propagated" instead.
func TestPlanTrainCatchUpStaysACatchUp(t *testing.T) {
	r := linkedRepo(t, "core", "app", failIfMarker)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(app): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())

	r.CommitEmpty("feat(app)%beta: app boards a train")
	r.ReleaseOK()
	require.True(t, r.IsTagged("app@0.2.0-beta.0"), "tags: %v", r.TagList())

	r.WriteFile("packages/app/FAIL", "break app's leg")
	r.CommitEmpty("fix(core)^: repair, propagating")
	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "app's leg must fail")
	require.True(t, r.IsTagged("core@0.1.1"), "core published; tags: %v", r.TagList())
	require.Zero(t, r.TagCount("app@0.2.0-beta.1"), "app did not")

	r.Remove("packages/app/FAIL")
	res = r.ReleaseOK()
	require.True(t, r.IsTagged("app@0.2.0-beta.1"), "the train heals; tags: %v", r.TagList())
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W193", "app"),
		"discharging core's published work is a catch-up, train or not: %s", res.Stdout)
	line := harness.GraphLine(res.Events, "app")
	assert.Contains(t, line.Str("message"), "catch-up", "the verdict marker: %s", res.Stdout)
	assert.Equal(t, "catch-up from core", line.Str("reason"))
}

// TestPlanFailedProviderSkipsTrainConsumer: the skip cascade on a train. The
// consumer's own feature is published train history; its only fresh cause is
// the failing provider's propagation, so the cascade must skip it — releasing
// it would ship a prerelease whose entire content is a provider movement that
// never published, recording a version that does not exist. The healing run
// then releases both together as an ordinary propagation.
func TestPlanFailedProviderSkipsTrainConsumer(t *testing.T) {
	r := linkedRepo(t, "core", "app", failIfMarker)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(app): bootstrap")
	r.ReleaseOK()
	r.CommitEmpty("feat(app)%beta: app boards a train")
	r.ReleaseOK()
	require.True(t, r.IsTagged("app@0.2.0-beta.0"), "tags: %v", r.TagList())

	r.WriteFile("packages/core/FAIL", "break the provider")
	r.CommitEmpty("fix(core)^: repair, propagating")
	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "the provider's failure fails the run")
	assert.Zero(t, r.TagCount("core@0.1.1"), "core did not publish; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("app@0.2.0"),
		"the consumer must not release a movement that never published; tags: %v", r.TagList())
	skipped := false
	for _, e := range res.Events {
		if e.Package() == "app" && e.Str("status") == "skipped" {
			skipped = true
		}
	}
	assert.True(t, skipped, "app is reported skipped, not released: %s", res.Stdout)

	r.Remove("packages/core/FAIL")
	res = r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.1"), "tags: %v", r.TagList())
	require.True(t, r.IsTagged("app@0.2.0-beta.1"), "tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "W193"),
		"provider and consumer release together: ordinary propagation, not a catch-up")
	assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "app").Str("reason"))
}

// TestPlanChannelOnlyReleaseAndEntryPatch: a release directive that only
// moves the channel is still a release (§13.9) — W202 explains its presence
// in the plan — and entering a prerelease channel with nothing pending takes
// the §11.4 entry patch, reported as W204. Graduating that otherwise empty
// train carries the same patch to stable. Both are non-suppressible: a tag
// appearing with no bump-worthy commit is exactly what a reader of the log
// cannot otherwise account for.
func TestPlanChannelOnlyReleaseAndEntryPatch(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): stable work")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())

	r.CommitEmpty("release(core)%beta: enter beta with nothing pending")
	res := r.ReleaseOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W202", "core"),
		"a channel-only release must be explained")
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W204", "core"),
		"the entry patch must be explained")
	assert.True(t, r.IsTagged("core@0.1.1-beta.0"),
		"channel entry with nothing pending takes the entry patch: %v", r.TagList())
	assert.Equal(t, 2, buildRuns(r), "a channel-only release executes its scripts")

	// The train entered through a patch but its window still has no direct
	// bump. Graduation must carry that same patch back to the stable core
	// rather than propose 0.1.0 behind the published beta.
	r.CommitEmpty("release(core)%beta>stable: graduate the entry-patched train")
	res = r.ReleaseOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W204", "core"),
		"the graduation patch is explained too")
	assert.True(t, r.IsTagged("core@0.1.1"), "graduation preserves the train's core: %v", r.TagList())
	assert.Equal(t, 3, buildRuns(r))
	r.ReleaseOK()
	assert.Equal(t, 3, r.TagCount("core@"), "the graduated train converges")
}

// TestPlanDirectChannelDirectivesReportWhatTheyProposedNothingFor: a direct
// channel directive is written by hand and reviewed, so one that does nothing
// is worth saying out loud — the author wrote it expecting a move. Each shape
// that proposes nothing gets its own package and its own code: graduating a
// package that is already stable, naming the channel a package is already on,
// and a transition whose two sides are the same. The one deliberate silence is
// a transition that does not match the package's channel: that is the
// mechanism working, since matching against the baseline is what makes one
// directive correct on the first run and on the fifth.
func TestPlanDirectChannelDirectivesReportWhatTheyProposedNothingFor(t *testing.T) {
	r := channelRepo(t, "stayed", "train", "selfmove", "twovalues")
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

// TestPlanAnyPrereleaseTransitionEndsWhateverTrainItFinds: the "*" from-side
// exists so one directive can end a train without the author having to know
// which channel the package landed on. It matches any prerelease and never
// matches stable, which is what keeps the same text inert once the package has
// graduated.
func TestPlanAnyPrereleaseTransitionEndsWhateverTrainItFinds(t *testing.T) {
	r := channelRepo(t, "rider", "settled")
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

// TestPlanPrereleaseBaselineWithoutACounterStopsTheTrain: §11.3 requires the
// prerelease counter to be a separate numeric identifier, because numeric
// identifiers compare numerically and a fused "beta10" compares as ASCII and
// misorders at ten. A baseline tag written by hand without one — or with a
// counter that is not a number — cannot be continued from, and the run says so
// instead of inventing a counter and overwriting the train's order.
func TestPlanPrereleaseBaselineWithoutACounterStopsTheTrain(t *testing.T) {
	r := channelRepo(t, "nocount", "notanumber")
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

// TestPlanComputedVersionMustExceedItsBaseline: the last guard of §13.9.
// Versions are computed from the *stable* baseline, so a repository whose
// newest tag is a prerelease of a higher core than anything the window can
// reach — an abandoned train, or a tag moved by hand — would otherwise compute
// a version SemVer ranks below what the package already published. Releasing
// that would make the tag order lie about which release came last.
func TestPlanComputedVersionMustExceedItsBaseline(t *testing.T) {
	r := channelRepo(t, "rewound")
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

// TestPlanChannelPropagationHonoursItsScope: Propagate-Channel-Scope
// restricts the channel axis the way Propagate-Scope restricts the bump axis.
// A scope naming one dependent reaches that one alone, and a scope naming a
// package the traversal never reaches excludes every dependent it did reach —
// which is a warning rather than silence, because a scope that excludes
// everything is nearly always a stale package name left behind by a rename.
func TestPlanChannelPropagationHonoursItsScope(t *testing.T) {
	t.Run("one dependent named", func(t *testing.T) {
		r := fanOutRepo(t)
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
		r := fanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream\n\n" +
			"Propagate-Channel-Scope: aside")

		res := r.StatusOK()
		assert.True(t, harness.IsCodePresent(res.Events, "W205"),
			"the scope excluded every dependent the unit reached: %s", res.Stdout)
	})
}

// TestPlanBumpPropagationScopeThatExcludesEveryone: the same finding on the
// bump axis, which has its own code because the two scopes are written
// separately and a repository may restrict one without the other.
func TestPlanBumpPropagationScopeThatExcludesEveryone(t *testing.T) {
	r := fanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core)^: work that propagates one level\n\nPropagate-Scope: aside")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W135"),
		"the scope excluded every dependent the unit reached: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "near").Str("message"),
		"and no dependent was bumped: %s", res.Stdout)
}

// TestPlanPropagatedStableLeavesADependentOnItsTrain: graduation ends a train
// and publishes under the version consumers resolve by default, so it must
// never happen because an unrelated package's commit propagated "stable" down
// an edge. The suppression is reported, because a suppressed graduation is a
// decision the operator may want to make deliberately with a transition.
func TestPlanPropagatedStableLeavesADependentOnItsTrain(t *testing.T) {
	r := fanOutRepo(t)
	r.Commit("chore: seed the fleet")
	tagAt(r, "near@1.0.0", "HEAD")
	tagAt(r, "near@1.1.0-beta.0", "HEAD")

	r.CommitEmpty("release(core)%stable%%stable: propose stable downstream")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W200", "near"),
		"the suppression is reported: %s", res.Stdout)
	near := harness.GraphLine(res.Events, "near")
	assert.Equal(t, "beta", near.Str("channel"), "the dependent stays on its train: %s", near)
	assert.Equal(t, "unchanged", near.Str("message"), "and nothing graduates it: %s", near)
}

// TestPlanPropagatedTransitionNoDependentIsOnIsReportedOnce: a transition
// propagated down the graph is matched against each dependent's own baseline.
// When none of them is on the train it names, the directive did nothing, and
// the unit is told once rather than once per dependent.
func TestPlanPropagatedTransitionNoDependentIsOnIsReportedOnce(t *testing.T) {
	r := fanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.CommitEmpty("release(core)%%rc>stable: end an rc train nobody is on")

	res := r.StatusOK()
	assert.Equal(t, 1, countCode(res.Events, "W206"),
		"the propagated transition that matched no dependent is reported once: %s", res.Stdout)
	for _, name := range []string{"near", "far"} {
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, name).Str("message"),
			"and moved no dependent: %s", res.Stdout)
	}
}

// TestPlanInheritedChannelFromDisagreeingSourcesTakesTheFirst: "inherit" means the
// channel of the originating package, and a unit naming two packages on
// different channels has two answers. The run picks the first by name and says
// which one it took, because silently choosing between them would make the
// dependents' channel depend on a package ordering nothing in the message
// shows.
func TestPlanInheritedChannelFromDisagreeingSourcesTakesTheFirst(t *testing.T) {
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
	near := harness.GraphLine(res.Events, "near")
	assert.Equal(t, "stable -> beta", near.Str("channel"),
		"the first source by name, beta1, is the one whose channel the dependent inherits: %s", near)
}

// TestPlanPropagationIsTraceableEndToEnd: a run at trace level accounts for
// every step a propagation took — which edge it crossed, which package it
// reached at which level, and where each package's channel came from — plus
// the scope each unit resolved to when the commit's own files were what
// decided it. One history exercises all of it, because the claim is that the
// trace is complete rather than that any one line exists.
func TestPlanPropagationIsTraceableEndToEnd(t *testing.T) {
	r := fanOutRepo(t)
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

// TestPlanChannelPropagationSkipsWhatItCannotAdmit: a propagated channel is
// admitted against the *target's* window, exactly as a propagated bump is. A
// unit whose own scope resolves to no package proposes nothing at all; a
// target whose window no longer holds the commit is past the proposal; and a
// target behind a cancel barrier has had the commit discarded. None of the
// three is an error, and none of them may move the package.
func TestPlanChannelPropagationSkipsWhatItCannotAdmit(t *testing.T) {
	t.Run("a unit whose scope answers to nothing", func(t *testing.T) {
		r := fanOutRepo(t)
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(nothing*)%beta%%beta: a glob no package answers to")

		res := r.StatusOK()
		assert.True(t, harness.IsCodePresent(res.Events, "W134"),
			"the glob that matched nothing is reported: %s", res.Stdout)
		assert.Equal(t, "stable", harness.GraphLine(res.Events, "near").Str("channel"),
			"and nothing downstream moved: %s", res.Stdout)
	})

	t.Run("a target whose window is past the proposal", func(t *testing.T) {
		r := fanOutRepo(t)
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
		r := fanOutRepo(t)
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

// TestPlanTwoProvidersProposeDifferentChannels: a dependent of two
// providers can be handed two channels in one run. The newer commit wins, and
// the conflict is reported against the dependent, because taking one silently
// would make the outcome depend on a traversal order the message does not
// show.
func TestPlanTwoProvidersProposeDifferentChannels(t *testing.T) {
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

// TestPlanPropagationScopeExcludesByName: a scope-set may be written as the
// workspace less a package, which is how a repository keeps one dependent off
// a propagation without listing every other one. The exclusion is applied
// after the inclusion and wins.
func TestPlanPropagationScopeExcludesByName(t *testing.T) {
	r := fanOutRepo(t)
	r.Commit("chore: seed the fleet")
	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core)^: work that propagates one level\n\nPropagate-Scope: *,-far")

	res := r.StatusOK()
	assert.Equal(t, "patch", harness.GraphLine(res.Events, "near").Str("bump"),
		"the dependent the scope admits is bumped: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "far").Str("message"),
		"and the one it excludes is not: %s", res.Stdout)
}

// TestPlanTwoReleaseAsDirectivesInOneWindow: a hold written last week and a
// resume written today are both in force until one of them is released. The
// newest wins, and the fact that there were two is reported, because a
// directive that was silently outranked is the reason a release an operator
// expected to be held went out.
func TestPlanTwoReleaseAsDirectivesInOneWindow(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core): work worth releasing")
	r.CommitEmpty("release(core): hold it back for now\n\nRelease-As: none")
	r.CommitEmpty("release(core): let it go after all\n\nRelease-As: auto")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W153", "core"),
		"two directives were pending: %s", res.Stdout)
	assert.False(t, harness.IsCodePresent(res.Events, "W158"),
		"the resume did lift a hold, so it is not the redundant kind: %s", res.Stdout)
	assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(res.Events, "core").Str("version"),
		"and the newest directive is the one in force: %s", res.Stdout)
}

// TestPlanScopeTermsReachTheirPackages: every shape a scope term may take, in
// one history. A glob reaches the packages it matches and says so when it
// matches none; "." is the packages the commit's own files are in; "*" is the
// workspace; and an exclusion naming nothing is a warning where an inclusion
// naming nothing is an error, because a typo in an include silently drops a
// release and a stale exclusion is what a refactor leaves behind.
func TestPlanScopeTermsReachTheirPackages(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "coreutils")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,coreutils,web): bootstrap every package")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/web/own.txt", "derived from the files\n")
	r.Commit("fix(.): the packages this commit's own files are in")
	r.CommitEmpty("fix(core*): a glob over two package names")
	r.CommitEmpty("fix(nothing*): a glob nothing answers to")
	r.CommitEmpty("fix(*,-ghost): the workspace, less a package that is not there")
	r.CommitEmpty("fix(ghost): an include naming no package")

	res := r.Status()
	assert.True(t, harness.IsCodePresent(res.Events, "W134"),
		"the glob that matched nothing is reported: %s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "W130"),
		"an exclusion naming no package is a warning: %s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "E130"),
		"an inclusion naming no package is an error: %s", res.Stdout)

	for _, pkg := range []string{"core", "coreutils", "web"} {
		line := harness.GraphLine(res.Events, pkg)
		assert.Equal(t, "0.1.0 -> 0.1.1", line.Str("version"),
			"%s was reached by the terms above: %s", pkg, line.Str("message"))
	}
}

// TestPlanDerivedScopeReadsEveryPathTheCommitChanged: a unit with no
// scope-set is the packages owning the paths its commit changed (CCME §6.2),
// read exactly as the repository records them. A file moved from one package
// to another changed both, however similar git finds the two versions (vector
// 29), so the package it left releases beside the package it reached; a file
// whose name git would quote, with a letter outside ASCII, still belongs to
// the package whose folder holds it.
func TestPlanDerivedScopeReadsEveryPathTheCommitChanged(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "util")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/helper.txt", strings.Repeat("a helper worth moving\n", 40))
	r.Commit("feat(core,util,web): bootstrap every package")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.Git("mv", "packages/core/helper.txt", "packages/util/helper.txt")
	r.Commit("fix: move the helper where it is used")
	r.WriteFile("packages/web/café.txt", "a page\n")
	r.Commit("fix: add a page whose name git quotes")

	res := r.StatusOK()
	for _, pkg := range []string{"core", "util", "web"} {
		line := harness.GraphLine(res.Events, pkg)
		assert.Equal(t, "0.1.0 -> 0.1.1", line.Str("version"),
			"%s owns a path its commit changed: %s", pkg, line.Str("message"))
	}
	assert.False(t, harness.IsCodePresent(res.Events, "W131"), "no scopeless unit was left inert: %s", res.Stdout)
}

// TestPlanBaselineIgnoresARefShorterThanTheTagPrefix: the tag listing is
// dispatched to package matchers through a trie over their literal prefixes,
// and a ref shorter than the prefix it shares characters with runs the walk
// off the end of the name rather than off the end of the trie. Such a ref
// belongs to no package and must reach none, while the real release tag beside
// it is still the package's baseline.
func TestPlanBaselineIgnoresARefShorterThanTheTagPrefix(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	tagAt(r, "core@1.0.0", "HEAD")
	// Shorter than "core@", and sharing its first characters.
	r.Git("tag", "cor")

	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("fix(core): work on top of the baseline")

	res := r.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the release tag is the baseline and the short ref is nobody's: %s", res.Stdout)
}

// channelRepo is a workspace of independent packages, one per claim,
// so that every directive below reaches exactly the package it names and the
// diagnostics can be read per package.
func channelRepo(t *testing.T, names ...string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	for _, name := range names {
		r.SeedPackage("packages", name)
	}
	return r
}

// fanOutRepo is one provider with two dependents and one package with
// no edge at all, which is the shape every propagation-scope claim needs: a
// scope term can then name a package the traversal never reaches.
func fanOutRepo(t *testing.T) *harness.Repo {
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
