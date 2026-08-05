package integration

// Area 3: plan logic. Most tests here are flowing multi-run scenarios in
// the style of services/cli/internal/cli's own end-to-end suite: several
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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
// a rejected pin simply leaves the package unreleased. (Pairing a rejected
// pin with a sibling bump in the same commit behaves surprisingly; that
// case is pinned separately in
// TestPlanRejectedPinSwallowsAnAccompanyingBump.) Each guard gets a fresh
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

// TestPlanRejectedPinSwallowsAnAccompanyingBump documents a sharp edge this
// suite turned up rather than assumed: a rejected exact pin paired with a
// genuine bump in the *same* commit (a `feat` unit and a `release` unit
// separated by `---`) does not fall back to releasing the bump at its
// ordinarily-computed version. §16's unit-scoped blast radius suggests the
// `feat` should still ship as 0.1.0 — instead the package publishes and
// tags its unchanged baseline, silently discarding the feature release.
// Pinned as a regression fence and flagged for a maintainer's second look;
// nothing else in this suite treats it as the intended contract.
func TestPlanRejectedPinSwallowsAnAccompanyingBump(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.CommitEmpty("feat(core): needs a minor\n---\nrelease(core): pin too low\n\nRelease-As: 0.0.5\n")

	res := r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "E156"), "the below-bump guard does fire")
	assert.False(t, r.HasTag("core@0.1.0"), "the feat's own minor bump was not released")
	assert.True(t, r.HasTag("core@0.0.0"),
		"instead the package published at its unchanged baseline — tags: %v", r.TagList())
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
	configFor := func(deps string) string {
		return fmt.Sprintf(`{
  "scripts": {"build": "echo building", "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},%s
  %s
}`, deps, harness.Base("1"))
	}
	r := harness.New(t)
	r.WriteConfig(configFor(""))
	r.SeedPackage("packages", "core")
	// A caret with no matching consumer yet: harmless, core just releases.
	r.Commit("feat(core)^: v1, propagating to a consumer that does not exist yet")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"))

	// consumer arrives later, wired into the graph in the very commit that
	// creates it. "release" is exempt from scope resolution by default, so
	// the bootstrap commit itself contributes nothing.
	r.SeedPackage("packages", "consumer")
	r.WriteConfig(configFor(`
  "dependencies": [{"consumer": "consumer", "provider": "core"}],`))
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
	r.CommitEmpty("feat(core)^@beta: start the train, but only core moves")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	assert.Zero(t, r.TagCount("consumer@"), "a stable consumer cannot be dragged onto a prerelease")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W208", "consumer"), "the suppression must be reported")

	// This time the channel explicitly propagates: both packages join the
	// train together.
	r.CommitEmpty("fix(core)^@beta++1: bring the consumer along this time")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0-beta.1"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("consumer@0.0.1-beta.0"), "tags: %v", r.TagList())

	// One graduation directive naming both packages directly. (A propagated
	// "@@beta>stable" transition reaching consumer from a directive on core
	// alone turns out not to graduate it — see
	// TestPlanPropagatedGraduationTransitionIsSuppressed. Naming both in
	// the release() scope-set sidesteps that path, and is itself a
	// legitimate way to end a train.)
	beforeGraduation := len(r.TagList())
	r.CommitEmpty("release(core,consumer)@beta>stable: graduate the whole train")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("consumer@0.0.1"), "tags: %v", r.TagList())

	// Converged: a re-run with no new commits changes nothing, proving the
	// multi-package graduation discharged cleanly.
	r.ReleaseOK()
	assert.Equal(t, beforeGraduation+2, len(r.TagList()), "no repeat release for either package")
}

// TestPlanPropagatedGraduationTransitionIsSuppressed documents the second
// sharp edge this suite turned up: configuration.md's own worked example —
// `release(core)@beta>stable@@beta>stable++*: graduate core and everything
// still on beta behind it` — describes a propagated transition as the
// deliberate exception to "a propagated stable never graduates a
// dependant" (channel.go: "A transition is the deliberate exception,
// because its author had to name the train being ended in order to write
// it"). The propagation call site does not carry the exception through: it
// resolves every propagated value with graduates=false, so the transition
// is refused by the very check the comment says it should skip (W200) and
// the unit is then reported as matching no one it reached (W206). core's
// own direct graduation is unaffected. A regression fence, not an
// endorsement.
func TestPlanPropagatedGraduationTransitionIsSuppressed(t *testing.T) {
	r := linkedRepo(t, "core", "consumer", echoBuild)
	r.CommitEmpty("feat(core)^@beta++1: start the train, bringing the consumer too")
	r.ReleaseOK()
	require.True(t, r.HasTag("consumer@0.0.1-beta.0"))

	r.CommitEmpty("release(core)@beta>stable@@beta>stable++1: graduate the whole train")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "core's own direct transition still graduates it; tags: %v", r.TagList())
	assert.False(t, r.HasTag("consumer@0.0.1"), "the propagated transition does not graduate consumer")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W200", "consumer"))
	assert.True(t, harness.HasCode(res.Events, "W206"))
}
