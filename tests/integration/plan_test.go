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
	assert.True(t, r.HasTag("core@0.0.1"), "tags: %v", r.TagList())
	assert.False(t, r.HasTag("core@0.1.1"), "the cancelled feat must never resurface")
	assert.Equal(t, 1, buildRuns(r), "one release, one build")

	// A cancel with nothing left pending — everything already released — is
	// an empty cancel: warned about, releasing nothing, running nothing.
	r.CommitEmpty("cancel(core): too late, it already shipped")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W170"), "an empty cancel must be warned about")
	assert.Equal(t, 1, r.TagCount("core@"), "no new tag from a no-op cancel")
	assert.Equal(t, 1, buildRuns(r), "and no scripts either")
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
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
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
	assert.True(t, harness.HasCode(res.Events, "W154"), "the version it would have released must be reported")

	// More work while held: still held, still nothing tagged or executed.
	r.WriteFile("packages/core/more.txt", "x")
	r.Commit("fix(core): more work while held")
	r.ReleaseOK()
	assert.Empty(t, r.TagList(), "still held")
	assert.Zero(t, buildRuns(r), "still excluded from execution")

	// Resume at the accumulated max(): the feat's minor wins over the fix.
	r.CommitEmpty("release(core): resume\n\nRelease-As: auto\n")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, 1, buildRuns(r), "the resumed release executes exactly once")

	// A redundant auto with nothing held: W158, no-op.
	r.CommitEmpty("release(core): redundant auto\n\nRelease-As: auto\n")
	res = r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W158"))
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
		require.True(t, r.HasTag("core@0.1.0"))

		r.CommitEmpty("release(core): pin backwards, alone\n\nRelease-As: 0.1.0\n")
		res := r.ReleaseOK() // under commitErrors: warn a rejected pin does not fail the run
		assert.True(t, harness.HasCode(res.Events, "E153"))
		assert.Equal(t, 1, r.TagCount("core@"), "nothing to fall back to: no new tag")
	})

	t.Run("E157_major_jump_too_large", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.CommitEmpty("release(core): pin far ahead of an untouched package\n\nRelease-As: 5.0.0\n")
		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "E157"))
		assert.Empty(t, r.TagList())
	})

	t.Run("E154_multi_package_pin", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.SeedPackage("packages", "utils")
		r.Commit("chore(release): bootstrap two packages")
		r.CommitEmpty("release(core,utils): pin both at once\n\nRelease-As: 3.0.0\n")
		res := r.ReleaseOK()
		assert.True(t, harness.HasCode(res.Events, "E154"))
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
	assert.True(t, harness.HasCode(res.Events, "E156"), "the below-bump guard still fires")
	assert.True(t, r.HasTag("core@0.1.0"),
		"the feat's own minor bump releases despite the rejected pin — tags: %v", r.TagList())
	assert.False(t, r.HasTag("core@0.0.0"), "the unchanged baseline must not be tagged")

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
	assert.True(t, r.HasTag("core@0.1.0"), "the independent provider still published")
	assert.Zero(t, r.TagCount("app@"), "app must not be tagged on a failed build")

	r.Remove("packages/app/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("app@0.0.1"), "app catches up to the version it was owed")
	assert.Equal(t, 1, r.TagCount("core@"), "core must not be re-released for a catch-up")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W193", "app"), "a catch-up must be labelled as one")

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
	assert.True(t, harness.HasCodeForPackage(res.Events, "W194", "app"), "app must be reported blocked, not silently absent")

	r.Remove("packages/core/FAIL")
	res = r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"))
	assert.True(t, r.HasTag("app@0.0.1"))
	assert.False(t, harness.HasCode(res.Events, "W194"), "nothing is blocked once the provider heals")
	assert.False(t, harness.HasCode(res.Events, "W193"), "same-run propagation is not a catch-up")
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
	require.True(t, r.HasTag("core@0.1.0"))

	// consumer arrives later, wired into the graph in the very commit that
	// creates it. "release" is exempt from scope resolution by default, so
	// the bootstrap commit itself contributes nothing.
	r.SeedPackage("packages", "consumer")
	r.WriteConfigModel(configFor([]models.DependencyConfig{{Consumer: "consumer", Provider: "core"}}))
	r.Commit("chore(release): wire up the new consumer package")

	res := r.ReleaseOK()
	assert.True(t, r.HasTag("consumer@0.0.1"),
		"consumer's window has no tag to bound it, so it still contains core's old propagating commit; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@"), "core itself has nothing new pending")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W193", "consumer"))
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
	assert.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("consumer@"), "a stable consumer cannot be dragged onto a prerelease")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W208", "consumer"), "the suppression must be reported")

	// This time the channel explicitly propagates: both packages join the
	// train together.
	r.CommitEmpty("fix(core)^%beta++1: bring the consumer along this time")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("consumer@0.0.1-beta.0"), "tags: %v", r.TagList())

	// One graduation directive naming both packages directly — one legitimate
	// way to end a train; the propagated "%%beta>stable" transition from a
	// directive on core alone is the other, covered by
	// TestPlanPropagatedGraduationTransitionGraduatesTheTrain.
	beforeGraduation := len(r.TagList())
	r.CommitEmpty("release(core,consumer)%beta>stable: graduate the whole train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("consumer@0.0.1"), "tags: %v", r.TagList())

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
	require.True(t, r.HasTag("consumer@0.0.1-beta.0"))

	r.CommitEmpty("release(core)%beta>stable%%beta>stable++1: graduate the whole train")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "core's own direct transition graduates it; tags: %v", r.TagList())
	assert.True(t, r.HasTag("consumer@0.0.1"),
		"the propagated transition graduates the consumer too; tags: %v", r.TagList())
	assert.False(t, harness.HasCodeForPackage(res.Events, "W200", "consumer"),
		"a transition is not a suppressed graduation")
	assert.False(t, harness.HasCode(res.Events, "W206"), "the transition matched the consumer it reached")

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
	require.True(t, r.HasTag("app@0.1.0"), "tags: %v", r.TagList())

	r.CommitEmpty("feat(app)%beta: app boards a train")
	r.ReleaseOK()
	require.True(t, r.HasTag("app@0.2.0-beta.0"), "tags: %v", r.TagList())

	r.WriteFile("packages/app/FAIL", "break app's leg")
	r.CommitEmpty("fix(core)^: repair, propagating")
	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "app's leg must fail")
	require.True(t, r.HasTag("core@0.1.1"), "core published; tags: %v", r.TagList())
	require.Zero(t, r.TagCount("app@0.2.0-beta.1"), "app did not")

	r.Remove("packages/app/FAIL")
	res = r.ReleaseOK()
	require.True(t, r.HasTag("app@0.2.0-beta.1"), "the train heals; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W193", "app"),
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
	require.True(t, r.HasTag("app@0.2.0-beta.0"), "tags: %v", r.TagList())

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
	require.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.2.0-beta.1"), "tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W193"),
		"provider and consumer release together: ordinary propagation, not a catch-up")
	assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "app").Str("reason"))
}

// TestPlanChannelOnlyReleaseAndEntryPatch: a release directive that only
// moves the channel is still a release (§13.9) — W202 explains its presence
// in the plan — and entering a prerelease channel with nothing pending takes
// the §11.4 entry patch, reported as W204. Both are non-suppressible: a tag
// appearing with no bump-worthy commit is exactly what a reader of the log
// cannot otherwise account for.
func TestPlanChannelOnlyReleaseAndEntryPatch(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): stable work")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	r.CommitEmpty("release(core)%beta: enter beta with nothing pending")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W202", "core"),
		"a channel-only release must be explained")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W204", "core"),
		"the entry patch must be explained")
	assert.True(t, r.HasTag("core@0.1.1-beta.0"),
		"channel entry with nothing pending takes the entry patch: %v", r.TagList())
	assert.Equal(t, 2, buildRuns(r), "a channel-only release executes its scripts")
}
