// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review cases at history boundaries the real CLI reaches
// through LocalGitx. These deliberately avoid the planner's compatibility
// fallbacks for lightweight test implementations.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalHistoryRejectsDuplicateVersionsInsideAComposedSource proves that
// the composed-history tag path enforces the same unique SemVer boundary as a
// monorepo. Distinct refs with equal precedence cannot silently choose which
// source window the fleet reads.
func TestFinalHistoryRejectsDuplicateVersionsInsideAComposedSource(t *testing.T) {
	f := finalPolyrepo(t)
	f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0+first", "-m", "first record")
	f.control.WriteFile("sources/lib/packages/core/fix.txt", "later\n")
	commitPolyrepoSource(t, f.control, "sources/lib", "fix(core): later commit")
	checkpointPolyrepoSource(t, f.control, "sources/lib")
	f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0+second", "-m", "second record")

	res := f.control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "parse to the same version")
	assert.True(t, harness.IsCodePresent(res.Events, "E191"), "events: %#v", res.Events)
	assert.Empty(t, plannedPackages(res), "a fatal boundary emits no package rows")
	assert.ElementsMatch(t, []string{"core@0.1.0+first", "core@0.1.0+second"},
		polyrepoTags(f.control, "sources/lib"))
}

// TestFinalHistoryRefusesAnUnreadableFreshPrereleaseWindow: the latest
// prerelease boundary differs from the stable boundary, so planning needs both
// windows. They are one read, the union of the two in a single walk from which
// each window is recovered by ancestry, and losing it aborts the whole plan
// rather than treating everything after the prerelease as already published.
func TestFinalHistoryRefusesAnUnreadableFreshPrereleaseWindow(t *testing.T) {
	f := finalPolyrepo(t)
	f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0", "-m", "stable record")
	f.control.WriteFile("sources/lib/packages/core/beta.txt", "beta\n")
	commitPolyrepoSource(t, f.control, "sources/lib", "feat(core)%beta: start beta train")
	checkpointPolyrepoSource(t, f.control, "sources/lib")
	f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.2.0-beta.0", "-m", "beta record")
	f.control.WriteFile("sources/lib/packages/core/beta.txt", "fresh beta fix\n")
	commitPolyrepoSource(t, f.control, "sources/lib", "fix(core)%beta++1: continue beta train")
	checkpointPolyrepoSource(t, f.control, "sources/lib")

	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib log --format=*--diff-merges=first-parent HEAD --not *",
	})
	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "lib-source history for core")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "stable and fresh source windows are one union read")
	assert.ElementsMatch(t, []string{"core@0.1.0", "core@0.2.0-beta.0"},
		polyrepoTags(f.control, "sources/lib"))

	healed := f.control.StatusOK()
	assert.Equal(t, "0.2.0-beta.0 -> 0.2.0-beta.1", harness.GraphLine(healed.Events, "core").Str("version"))
}

// TestFinalHistoryRequiresTheLatestPrereleaseBoundarySeparately gives a
// consumer a proven stable provider position but no position for its newer
// prerelease. The stable tuple cannot be reused: doing so would change the
// fresh catch-up window of the active train.
func TestFinalHistoryRequiresTheLatestPrereleaseBoundarySeparately(t *testing.T) {
	lib := harness.New(t)
	lib.SeedPackage("packages", "lib")
	lib.Commit("feat(lib): stable provider")
	lib.Git("tag", "-a", "lib@1.0.0", "-m", "stable provider")
	lib.WriteFile("packages/lib/fix.txt", "later\n")
	lib.Commit("fix(lib)^: later provider")

	app := harness.New(t)
	app.SeedPackage("packages", "app")
	app.Commit("feat(app): stable consumer")
	app.Git("tag", "-a", "app@1.0.0", "-m", "stable consumer")
	app.WriteFile("packages/app/beta.txt", "beta\n")
	app.Commit("feat(app)%beta: beta consumer")
	app.Git("tag", "-a", "app@1.1.0-beta.0", "-m", "beta consumer")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", lib)
	addPolyrepoSource(t, control, "app-source", "sources/app", app)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages", "apps": "sources/app/packages",
	})
	cfg["dependencies"] = map[string]any{"app": []any{"lib"}}
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer": "app", "releaseTag": "app@1.0.0",
		"repository": "lib-source", "revision": "lib@1.0.0",
	}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose the stable boundary only")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.True(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.Contains(t, combined, "app@1.1.0-beta.0")
	assert.Contains(t, combined, "repositoryBaselines")
	assert.Empty(t, plannedPackages(res), "a fatal boundary emits no package rows")
	assert.Empty(t, harness.GraphLine(res.Events, "app").Str("version"))
}

// TestFinalHistorySharesAControlCheckpointAcrossSourceReleases records two
// source tags in one canonical checkpoint. Later control intent addressed to
// both packages must resolve both control-history boundaries from that one
// immutable fleet snapshot.
func TestFinalHistorySharesAControlCheckpointAcrossSourceReleases(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.SeedPackage("packages", "util")
	source.Commit("chore: bootstrap source packages")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose source packages")

	control.WriteFile("sources/lib/packages/core/released.txt", "core\n")
	control.WriteFile("sources/lib/packages/util/released.txt", "util\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(core,util): release together")
	control.Git("-C", "sources/lib", "tag", "-a", "core@1.0.0", "-m", "core release")
	control.Git("-C", "sources/lib", "tag", "-a", "util@1.0.0", "-m", "util release")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): core@1.0.0, util@1.0.0")
	control.CommitEmpty("fix(core,util): control follow-up for both releases")

	res := control.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(res.Events, "core").Str("version"))
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(res.Events, "util").Str("version"))
	assert.False(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.ElementsMatch(t, []string{"core@1.0.0", "util@1.0.0"},
		polyrepoTags(control, "sources/lib"))
}

// TestFinalHistoryAppliesControlCancellationWithoutPropagatingIt proves the
// polyrepo-specific control-boundary pass treats cancellation as a barrier,
// not as a release proposal. The source work is newer than its release, but a
// later control cancel clears it without walking dependency edges.
func TestFinalHistoryAppliesControlCancellationWithoutPropagatingIt(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.Commit("chore: bootstrap source package")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose source package")

	control.WriteFile("sources/lib/packages/core/released.txt", "released\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(core): first release")
	control.Git("-C", "sources/lib", "tag", "-a", "core@1.0.0", "-m", "core release")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): core@1.0.0")

	control.WriteFile("sources/lib/packages/core/pending.txt", "pending\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(core): pending source work")
	checkpointPolyrepoSource(t, control, "sources/lib")
	control.CommitEmpty("cancel(core): discard pending source work")

	res := control.StatusOK()
	core := harness.GraphLine(res.Events, "core")
	assert.Equal(t, "unchanged", core.Str("message"), "the control cancellation clears the newer source fix")
	assert.Empty(t, core.Str("bump"), "a cancel unit never becomes a release proposal")
	assert.False(t, harness.IsCodePresent(res.Events, "E333"), "the release checkpoint bounds control history: %#v", res.Events)
	assert.Equal(t, []string{"core@1.0.0"}, polyrepoTags(control, "sources/lib"),
		"status does not publish the cancelled work")
}

// TestFinalHistoryExcludesBothParentsOfAReleasedControlMerge gives the source
// package a control-repository input whose release checkpoint descends from a
// real merge. Both sides of the already-shipped control DAG must be excluded,
// including their shared ancestor; only later source work remains pending.
func TestFinalHistoryExcludesBothParentsOfAReleasedControlMerge(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.Commit("chore: bootstrap source package")

	control := harness.New(t)
	control.SeedPackage("packages", "policy")
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"control": "packages", "libs": "sources/lib/packages",
	})
	cfg["dependencies"] = map[string]any{"core": []any{"policy"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose policy and source")

	control.Git("checkout", "-q", "-b", "policy-side")
	control.WriteFile("packages/policy/side.txt", "side\n")
	control.Commit("fix(policy): side of policy history")
	control.Git("checkout", "-q", harness.DefaultBranch)
	control.WriteFile("packages/policy/main.txt", "main\n")
	control.Commit("fix(policy): main side of policy history")
	control.Git("merge", "-q", "--no-ff", "policy-side", "-m", "chore: merge policy history")

	control.WriteFile("sources/lib/packages/core/released.txt", "released\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(core): first release")
	control.Git("-C", "sources/lib", "tag", "-a", "core@1.0.0", "-m", "core release")
	control.Git("add", "sources/lib")
	control.Git("commit", "-q", "-m", "chore(release): core@1.0.0")

	control.WriteFile("sources/lib/packages/core/pending.txt", "pending\n")
	commitPolyrepoSource(t, control, "sources/lib", "fix(core): pending after the checkpoint")
	checkpointPolyrepoSource(t, control, "sources/lib")

	res := control.StatusOK()
	core := harness.GraphLine(res.Events, "core")
	assert.Equal(t, "1.0.0 -> 1.0.1", core.Str("version"))
	assert.Equal(t, "patch", core.Str("bump"), "the old control merge does not replay into the source window")
	assert.False(t, harness.IsCodePresent(res.Events, "E333"), "the checkpoint bounds the merged control DAG: %#v", res.Events)
}

// TestFinalHistoryUsesInitialVersionsForComposedSourceBaselines exercises both
// honest initial-version cases through the composed tag inventory: a newest
// matching tag whose version is opaque, and a package with no tag at all. The
// opaque tag still bounds its package's window while both versions start from
// the configured initials.
func TestFinalHistoryUsesInitialVersionsForComposedSourceBaselines(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.SeedPackage("packages", "util")
	source.Commit("feat(core,util): bootstrap packages")
	source.Git("tag", "-a", "core@not-a-version", "-m", "opaque release marker")
	source.WriteFile("packages/core/fix.txt", "core fix\n")
	source.WriteFile("packages/util/fix.txt", "util fix\n")
	source.Commit("fix(core,util): later fixes")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["initials"] = map[string]any{"core": "1.5.0", "util": "2.0.0"}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose source initials")

	res := control.StatusOK()
	core := harness.GraphLine(res.Events, "core")
	util := harness.GraphLine(res.Events, "util")
	assert.Equal(t, "1.5.0 -> 1.5.1", core.Str("version"), "the opaque tag excludes the earlier feature")
	assert.Equal(t, "2.0.0 -> 2.1.0", util.Str("version"), "the untagged package reads its whole history")
	assert.Equal(t, true, core["baselineFromInitials"])
	assert.Equal(t, true, util["baselineFromInitials"])
}

// TestFinalHistoryRequiresAControlBoundaryForControlIntent starts with a
// source release made before composition, then addresses that package from
// control history. The source tag is enough for its native window, but the
// control directive also needs proof of which control snapshot that release
// incorporated.
func TestFinalHistoryRequiresAControlBoundaryForControlIntent(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.Commit("feat(core): released before composition")
	source.Git("tag", "-a", "core@1.0.0", "-m", "source release")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose the released source")
	control.CommitEmpty("fix(core): control intent without a release checkpoint")

	res := control.Status()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.True(t, harness.IsCodePresent(res.Events, "E333"), "events: %#v", res.Events)
	assert.Contains(t, combined, "core@1.0.0")
	assert.Contains(t, combined, "repositoryBaselines for repository control")
	assert.Empty(t, plannedPackages(res), "unbounded control intent emits no package rows")
}

// TestFinalHistoryReportsChoreographedLinkReads extends the production debug
// workload contract to the link-evidence path. A settled consumer boundary
// reads release subjects and link trees, and the count is observable without
// changing the resulting plan.
func TestFinalHistoryReportsChoreographedLinkReads(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	api.ReleaseOK("--package", "*")
	quiet := api.StatusOK("--package", "*")
	debug := api.StatusOK("--package", "*", "--log-level", "debug")
	assert.Equal(t, plannedPackages(quiet), plannedPackages(debug))
	requireNoDiagnostic(t, debug, "E333")

	var workload harness.Event
	for _, event := range debug.Events {
		if event.Str("message") == "planning workload" {
			workload = event
		}
	}
	require.NotEmpty(t, workload, "debug planning emits one workload summary")
	reads, ok := workload["linkReads"].(float64)
	require.True(t, ok, "linkReads must be a numeric count: %#v", workload)
	assert.Positive(t, reads, "release subjects and recorded link pins are real Git reads")
	assert.Empty(t, harness.GraphLine(debug.Events, "api-pkg").Str("bump"), "the settled plan is unchanged")
}
