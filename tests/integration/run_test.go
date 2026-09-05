// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Area 6: the `dispat run <script>` command. `dispat run x` (or the shorthand
// `dispat x`, when x is not a command name) executes the script named x inside
// each *changed* package with the package's full DISPAT_* environment,
// honouring the dependency graph — providers before consumers, independent
// packages in parallel within the build concurrency budget.
//
// A package resolves x through its own `scripts`, then its space's, then the
// file's, so the level x is defined at is what a run covers: a top-level
// script reaches every changed package, a space's reaches that space's, a
// package's reaches that package alone. A changed package that resolves
// nothing completes as a no-op. A name nothing defines is an error, and so is
// an explicit selection — a --package, --space or --group term, or the
// invocation folder — in which no package resolves it; a selection only the
// window assembled that resolves nothing is a reported no-op instead. What a
// failure does to the failed package's dependents is the --on-error flag:
// "skip" (default) or "continue"; any failure makes the command exit 1.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// runConfig is the configuration of this file's shared fixture: a
// "libs" space (core <- app, by a dependency edge) defining the "lint",
// "record" and "fail" scripts, and a "tools" space (tool) defining none of
// them. The level tests rewrite it to move a name between the three levels.
func runConfig() models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{
			"lint":   {"echo $DISPAT_PACKAGE >> ../../run.log"},
			"record": {`env | grep '^DISPAT_' | sort > run-env.txt`},
			"fail":   {`[ "$DISPAT_PACKAGE" != "core" ] && echo $DISPAT_PACKAGE >> ../../run.log`},
		}},
		"tools": {Path: models.PathList{"tools"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	return cfg
}

// runRepo lays runConfig out as a repository whose one commit
// changes all three packages, so every one of them is in the plan.
func runRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(runConfig())
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.SeedPackage("tools", "tool")
	r.Commit("feat(core,app,tool): everything changes")
	return r
}

// runLog returns the packages the lint script recorded, in execution order.
func runLog(r *harness.Repo) []string {
	data, err := os.ReadFile(r.Path("run.log"))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// TestRunExecutesChangedPackagesInTopologicalOrder: the script runs once per
// changed package of the defining space, providers before consumers, and the
// space without the script is skipped rather than failed.
func TestRunExecutesChangedPackagesInTopologicalOrder(t *testing.T) {
	r := runRepo(t)

	r.RunScriptOK("lint")
	assert.Equal(t, []string{"core", "app"}, runLog(r),
		"providers run before consumers; the tools space defines no lint and is skipped")
	assert.Empty(t, r.TagList(), "dispat run must not release anything")
}

// TestRunShorthandCommand: `dispat lint` is `dispat run lint` when "lint" is
// not a command name.
func TestRunShorthandCommand(t *testing.T) {
	r := runRepo(t)

	res := r.Release("lint") // Release passes raw args: this runs `dispat lint`
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core", "app"}, runLog(r))
	assert.Empty(t, r.TagList(), "the shorthand must run the script, not release")
}

// TestRunReceivesTheFullPackageEnvironment: the run script sees the same
// DISPAT_* environment a stage script would — package, planned version, tag,
// stage ("run:<name>") and the workspace listing — so scripts are movable
// between stages and run scripts.
func TestRunReceivesTheFullPackageEnvironment(t *testing.T) {
	r := runRepo(t)

	r.RunScriptOK("record")
	data, err := os.ReadFile(r.Path("packages", "core", "run-env.txt"))
	require.NoError(t, err)
	env := string(data)
	assert.Contains(t, env, "DISPAT_PACKAGE=core")
	assert.Contains(t, env, "DISPAT_NEW_VERSION=0.1.0")
	assert.Contains(t, env, "DISPAT_TAG=core@0.1.0")
	assert.Contains(t, env, "DISPAT_STAGE=run:record")
	assert.Contains(t, env, "DISPAT_WORKSPACE_APP_VERSION=0.1.0", "the workspace listing travels too")
	// The core version split into its three numbers, zeros included: this is
	// what a script writes a moving series tag from.
	assert.Contains(t, env, "DISPAT_VERSION=0.1.0")
	assert.Contains(t, env, "DISPAT_MAJOR=0")
	assert.Contains(t, env, "DISPAT_MINOR=1")
	assert.Contains(t, env, "DISPAT_PATCH=0")
}

// TestRunVersionComponentsOnAPrereleaseTrain: the three numbers split
// DISPAT_VERSION, not DISPAT_NEW_VERSION, so a package mid-train reports the
// stable release it is heading for. A build tagging an image "1" off a release
// candidate is exactly what this keeps deliberate rather than accidental.
func TestRunVersionComponentsOnAPrereleaseTrain(t *testing.T) {
	r := runRepo(t)
	r.CommitEmpty("feat(core)%beta: start a train")

	r.RunScriptOK("record")
	data, err := os.ReadFile(r.Path("packages", "core", "run-env.txt"))
	require.NoError(t, err)
	env := string(data)
	assert.Contains(t, env, "DISPAT_NEW_VERSION=0.1.0-beta.0")
	assert.Contains(t, env, "DISPAT_VERSION=0.1.0")
	assert.Contains(t, env, "DISPAT_MAJOR=0")
	assert.Contains(t, env, "DISPAT_MINOR=1")
	assert.Contains(t, env, "DISPAT_PATCH=0")
	assert.Contains(t, env, "DISPAT_COUNTER=0", "the prerelease is reported by the counter, not by the split")
}

// TestRunUnknownScriptFails: a name nothing defines is an error — running
// nothing silently is how a typo hides.
func TestRunUnknownScriptFails(t *testing.T) {
	r := runRepo(t)
	res := r.RunScript("format")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
}

// TestRunWhenDiscoveryItselfFails: a space pointing at a folder that is not
// there loads as config but cannot be discovered. Both halves of the run
// command have to survive that: the guard that decides whether a name exists
// anywhere (it consults discovery for scripts only a package folder declares)
// and the plan the run is selected from. Neither may panic, and both report a
// plain failure.
func TestRunWhenDiscoveryItselfFails(t *testing.T) {
	r := harness.New(t)
	cfg := runConfig()
	cfg.Spaces["ghosts"] = models.SpaceConfig{Path: models.PathList{"nowhere"}, Flow: buildPublish()}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.SeedPackage("tools", "tool")
	r.Commit("feat(core,app,tool): a space points at a missing folder")

	unknown := r.RunScript("format")
	assert.Equal(t, 1, unknown.Code, "an unknown name is still a clean error, not a crash")

	known := r.RunScript("lint")
	assert.Equal(t, 1, known.Code, "and a known name cannot be planned either")
	assert.Empty(t, runLog(r), "nothing runs when the workspace cannot be read")
}

// TestRunTopLevelScriptReachesEveryPackage: a name defined at the top level
// resolves in every package, so the run covers every changed package of every
// space — including the "tools" space, which defines no scripts of its own.
func TestRunTopLevelScriptReachesEveryPackage(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	cfg.Scripts["stamp"] = models.Script{"echo $DISPAT_PACKAGE >> ../../run.log"}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): add a top-level script")

	r.RunScriptOK("stamp")
	assert.ElementsMatch(t, []string{"core", "app", "tool"}, runLog(r))
}

// TestRunSpaceScriptStaysInItsSpace: the same run, with the name defined on
// one space instead, reaches that space's changed packages alone. Together
// with the top-level case above, this is the whole selection rule.
func TestRunSpaceScriptStaysInItsSpace(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	tools := cfg.Spaces["tools"]
	tools.Scripts = map[string]models.Script{"stamp": {"echo $DISPAT_PACKAGE >> ../../run.log"}}
	cfg.Spaces["tools"] = tools
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): add a space script")

	r.RunScriptOK("stamp")
	assert.Equal(t, []string{"tool"}, runLog(r),
		"only the defining space's packages run; the others are no-ops")
}

// TestRunPackageScriptRunsInThatPackageAlone: a name only one package defines
// reaches that package and no other, even though its sibling is changed and
// in the same space.
func TestRunPackageScriptRunsInThatPackageAlone(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	cfg.Packages = map[string]models.PackageConfig{
		"app": {Scripts: map[string]models.Script{"stamp": {"echo $DISPAT_PACKAGE >> ../../run.log"}}},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): add a package script")

	r.RunScriptOK("stamp")
	assert.Equal(t, []string{"app"}, runLog(r))
}

// TestRunResolvesTheMostLocalScript: one name defined at all three levels
// resolves per package — the package's own first, then its space's, then the
// file's — so each changed package records which level answered for it.
func TestRunResolvesTheMostLocalScript(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	stamp := func(level string) string {
		return "echo " + level + "-$DISPAT_PACKAGE >> ../../run.log"
	}
	cfg.Scripts["stamp"] = models.Script{stamp("top")}
	libs := cfg.Spaces["libs"]
	libs.Scripts["stamp"] = models.Script{stamp("space")}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{
		"app": {Scripts: map[string]models.Script{"stamp": {stamp("package")}}},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): define stamp at every level")

	r.RunScriptOK("stamp")
	assert.ElementsMatch(t, []string{"package-app", "space-core", "top-tool"}, runLog(r),
		"app takes its own, core takes its space's, tool takes the file's")
}

// TestRunWindowSelectionWithoutTheScriptIsANoOp: the name exists — so this is
// not the typo guard — but nowhere in the window's selection. Nothing was
// named, so the mismatch accuses nobody: the run exits 0 and reports at info
// level that the script reached no covered package. The explicit-selection
// counterpart, which does error, is pinned in filter_test.go.
func TestRunWindowSelectionWithoutTheScriptIsANoOp(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	cfg.Packages = map[string]models.PackageConfig{
		"tool": {Scripts: map[string]models.Script{"stamp": {"echo $DISPAT_PACKAGE >> ../../run.log"}}},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): give tool a script of its own")
	r.ReleaseOK() // closes the window, so the next commit alone selects
	r.CommitEmpty("fix(core): only core changes now")

	res := r.RunScript("stamp")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, runLog(r), "nothing ran")
	e := findEvent(t, res.Events, "script resolves in no covered package, nothing to do")
	assert.Equal(t, "stamp", e.Str("script"))

	// The same name over a window that does contain tool runs it.
	r.RunScriptOK("stamp", "--since", "all")
	assert.Equal(t, []string{"tool"}, runLog(r))
}

// TestRunSinceWindowWithoutTheScriptIsANoOp is the sweep-in-CI shape of the
// no-op rule: a space defines the script, a standalone package does not, and
// a commit touching only the standalone package makes `--since HEAD~1` cover
// it alone. The sweep stays green rather than demanding a dummy script.
func TestRunSinceWindowWithoutTheScriptIsANoOp(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	libs := cfg.Spaces["libs"]
	libs.Scripts = map[string]models.Script{"lint": {`echo "$DISPAT_PACKAGE" >> ../../lint.log`}}
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{"solo": {Path: "tools/solo", Flow: buildPublish()}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("tools/solo/main.txt", "x")
	r.Commit("feat(core,solo): bootstrap both")
	r.WriteFile("tools/solo/touch.txt", "x")
	r.Commit("chore(solo): touch solo alone")

	res := r.RunScript("lint", "--since", "HEAD~1")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NoFileExists(t, r.Path("lint.log"), "the space's packages are outside the window")
	e := findEvent(t, res.Events, "script resolves in no covered package, nothing to do")
	assert.Equal(t, "lint", e.Str("script"))
	assert.Equal(t, []any{"solo"}, e["covered"], "the report names what the window covered")

	// A window that does reach the defining space runs it.
	r.RunScriptOK("lint", "--since", "all")
	data, err := os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, []string{"core"}, strings.Fields(string(data)))
}

// TestRunSpaceFileScriptInAnEmptySpace: a script written only in a space
// folder's own config file, in a space that holds no package, must count as
// defined — the typo guard reads the space files, not just the packages. The
// run itself is then the window no-op, because no covered package resolves it.
func TestRunSpaceFileScriptInAnEmptySpace(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	cfg.Spaces["archive"] = models.SpaceConfig{Path: models.PathList{"archive"}, Flow: buildPublish()}
	r.WriteConfigModel(cfg)
	r.WriteFile("archive/dispat.json", `{"scripts": {"special": ["echo special ran"]}}`)
	r.Commit("chore(core): add an empty space with its own script")

	res := r.RunScript("special")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	findEvent(t, res.Events, "script resolves in no covered package, nothing to do")

	res = r.RunScript("ghost")
	assert.Equal(t, 1, res.Code, "a name no level defines is still the typo guard's error")
}

// TestRunConsumersWindowWithoutTheScriptIsANoOp: --consumers expands a
// window-only selection without making it explicit, so a script the expanded
// selection still cannot resolve stays a reported no-op rather than an error.
func TestRunConsumersWindowWithoutTheScriptIsANoOp(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		"extra": {Scripts: map[string]models.Script{"special": {`echo "$DISPAT_PACKAGE" >> ../../special.log`}}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "mid", Provider: "core"},
		{Consumer: "app", Provider: "mid"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "mid")
	r.SeedPackage("packages", "app")
	r.SeedPackage("packages", "extra")
	r.Commit("feat(core,mid,app,extra): bootstrap the chain")
	r.WriteFile("packages/core/touch.txt", "x")
	r.Commit("chore(core): touch core alone")

	res := r.RunScript("special", "--since", "HEAD~1", "--consumers")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NoFileExists(t, r.Path("special.log"), "extra is not among core's consumers")
	e := findEvent(t, res.Events, "script resolves in no covered package, nothing to do")
	assert.Equal(t, []any{"core", "mid", "app"}, e["covered"],
		"the expansion happened; it just reached no package with the script")
}

// TestRunFilterRunsATopLevelScriptInOnePackage: a filtered run executes one
// top-level script inside one package's folder under the release environment,
// releasing nothing — reaching an unchanged package through `--since all`.
func TestRunFilterRunsATopLevelScriptInOnePackage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["probe"] = models.Script{`echo "$DISPAT_PACKAGE@$DISPAT_NEW_VERSION $DISPAT_STAGE" > probe.txt`}
	cfg.Scripts["boom"] = models.Script{"exit 7"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.ReleaseOK() // core is now unchanged: --since all reaches it anyway

	r.RunScriptOK("probe", "--since", "all", "-p", "core")
	data, err := os.ReadFile(r.Path("packages", "core", "probe.txt"))
	require.NoError(t, err)
	assert.Equal(t, "core@0.1.0 run:probe\n", string(data),
		"an unchanged package carries its baseline as the new version")
	assert.Equal(t, []string{"core@0.1.0"}, r.TagList(), "a filtered run releases nothing new")

	assert.Equal(t, 1, r.RunScript("ghost", "-p", "core").Code, "unknown script")
	assert.Equal(t, 1, r.RunScript("probe", "-p", "ghost").Code, "unknown package")
	assert.Equal(t, 1, r.RunScript("boom", "--since", "all", "-p", "core").Code,
		"a failing script fails the command")
}

// TestRunOnErrorPolicies: the "fail" script exits non-zero for core, the
// provider. Under the default --on-error=skip its dependent app is skipped;
// under --on-error=continue app still runs. Both exit 1 — the failure is
// never swallowed.
func TestRunOnErrorPolicies(t *testing.T) {
	t.Run("skip_dependents_by_default", func(t *testing.T) {
		r := runRepo(t)
		res := r.RunScript("fail")
		assert.Equal(t, 1, res.Code)
		assert.Empty(t, runLog(r), "app is a dependent of the failed core and must be skipped")
	})
	t.Run("continue_still_runs_dependents", func(t *testing.T) {
		r := runRepo(t)
		res := r.RunScript("fail", "--on-error", "continue")
		assert.Equal(t, 1, res.Code, "the failure still fails the command")
		assert.Equal(t, []string{"app"}, runLog(r), "app runs despite core's failure")
	})
	t.Run("unknown_policy_is_a_usage_error", func(t *testing.T) {
		r := runRepo(t)
		res := r.RunScript("lint", "--on-error", "explode")
		assert.Equal(t, 2, res.Code)
	})
}

// TestRunConcurrencyBudget: independent packages' run scripts execute in
// parallel within the build concurrency budget (--concurrency's first
// value), proven by measured overlap, while a serial budget forbids it.
func TestRunConcurrencyBudget(t *testing.T) {
	repo := func(t *testing.T) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := harness.BaseFile(1)
		cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{
				"mark": {r.TsmarkScript("run.log", "$DISPAT_PACKAGE", 200*time.Millisecond)},
			}},
		}
		r.WriteConfigModel(cfg)
		seedIndependentPackages(r, []string{"a", "b", "c"})
		return r
	}

	t.Run("parallel_within_the_budget", func(t *testing.T) {
		r := repo(t)
		r.RunScriptOK("mark", "--concurrency", "3")
		tl := r.Timeline("run.log")
		require.Len(t, tl, 3)
		harness.AssertConcurrencyBudget(t, tl, 3)
	})
	t.Run("serial_under_budget_one", func(t *testing.T) {
		r := repo(t)
		r.RunScriptOK("mark") // config concurrency 1
		tl := r.Timeline("run.log")
		require.Len(t, tl, 3)
		harness.AssertConcurrencyBudget(t, tl, 1)
	})
}

// TestRunSkipsUnchangedPackages: after a release nothing is changed, so the
// script executes zero times — `dispat run` targets the current plan's
// changed packages, not the whole workspace. An empty selection succeeds:
// unlike a selection none of whose packages define the script, there was
// nowhere to look in the first place.
func TestRunSkipsUnchangedPackages(t *testing.T) {
	r := runRepo(t)
	r.ReleaseOK()

	r.RunScriptOK("lint")
	assert.Empty(t, runLog(r), "no changed packages, no executions")

	// A new change to one package narrows the run to exactly it.
	r.CommitEmpty("fix(core): one package moves again")
	r.RunScriptOK("lint")
	assert.Equal(t, []string{"core"}, runLog(r))
}

// TestRunInFixedSpaceIncludesRides: in a fixed-versioning space a change to
// one member puts every member in the plan, so the run script executes in
// all of them — the ride is part of the release the script inspects.
func TestRunInFixedSpaceIncludesRides(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Versioning: models.VersioningFixed, Flow: buildPublish(),
			Scripts: map[string]models.Script{"lint": {"echo $DISPAT_PACKAGE >> ../../run.log"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a): only a changes")

	r.RunScriptOK("lint")
	got := runLog(r)
	assert.ElementsMatch(t, []string{"a", "b"}, got,
		"the fixed ride is a changed package and runs the script too: %v", got)
}

// TestRunGraphOrderingUnderConcurrency pins the run command's two scheduling
// promises against each other in one graph, the way the release executor's
// concurrency tests do: three independent providers' scripts overlap up to
// the budget (measured, three independent checks), while their shared
// consumer's script never starts before every provider's ended.
func TestRunGraphOrderingUnderConcurrency(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{
			"mark": {r.TsmarkScript("run.log", "$DISPAT_PACKAGE", 150*time.Millisecond)},
		}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "d", Provider: "a"},
		{Consumer: "d", Provider: "b"},
		{Consumer: "d", Provider: "c"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"a", "b", "c", "d"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("feat(a,b,c,d): the whole graph changes")

	r.RunScriptOK("mark")

	tl := r.Timeline("run.log")
	require.Len(t, tl, 4)
	a, b, c := harness.Find(t, tl, "a"), harness.Find(t, tl, "b"), harness.Find(t, tl, "c")
	d := harness.Find(t, tl, "d")
	harness.AssertOverlaps(t, a, b)
	harness.AssertOverlaps(t, b, c)
	harness.AssertOverlaps(t, a, c)
	harness.AssertConcurrencyBudget(t, []harness.Interval{a, b, c}, 3)
	harness.AssertSequential(t, a, d)
	harness.AssertSequential(t, b, d)
	harness.AssertSequential(t, c, d)
}

// TestRunCarriesOutputsAcrossPackages: a provider's run script exports
// through $DISPAT_OUTPUT — here with the DISPAT_OUTPUT_-prefixed spelling —
// and its consumers read the export as DISPAT_OUTPUT_<NAME>, with
// DISPAT_OUTPUT_SOURCE_<NAME> naming the exporting script — transitively,
// and through a middle package whose space does not even define the script
// (its no-op still carries).
func TestRunCarriesOutputsAcrossPackages(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	carry := `if [ "$DISPAT_PACKAGE" = "base" ]; then` +
		` echo "DISPAT_OUTPUT_FROM_BASE=hello-from-base" >> "$DISPAT_OUTPUT";` +
		` else echo "$DISPAT_PACKAGE sees $DISPAT_OUTPUT_FROM_BASE from $DISPAT_OUTPUT_SOURCE_FROM_BASE" >> ../../carry.txt; fi`
	cfg.Spaces = map[string]models.SpaceConfig{
		"outer":  {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{"carry": {carry}}},
		"middle": {Path: models.PathList{"middle"}, Flow: buildPublish()}, // no run scripts: a silent carrier
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "mid", Provider: "base"},
		{Consumer: "top", Provider: "mid"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "base")
	r.SeedPackage("middle", "mid")
	r.SeedPackage("packages", "top")
	r.Commit("feat(base,mid,top): the whole chain changes")

	r.RunScriptOK("carry")
	data, err := os.ReadFile(r.Path("carry.txt"))
	require.NoError(t, err)
	assert.Equal(t, "top sees hello-from-base from base:run:carry\n", string(data),
		"the export travels base -> mid (script-less) -> top, provenance included")
}

// TestRunCarriesOutputsFromAFailedProvider: under --on-error=continue a
// failed provider's dependents still run — and they still receive whatever
// the failed script exported before failing, mirroring what the pipeline's
// onFail hooks get.
func TestRunCarriesOutputsFromAFailedProvider(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	failCarry := `if [ "$DISPAT_PACKAGE" = "core" ]; then` +
		` echo "MARK=exported-before-failing" >> "$DISPAT_OUTPUT"; exit 1;` +
		` else echo "app sees $DISPAT_OUTPUT_MARK" > ../../carry.txt; fi`
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Scripts: map[string]models.Script{"failcarry": {failCarry}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): both change")

	res := r.RunScript("failcarry", "--on-error", "continue")
	assert.Equal(t, 1, res.Code, "the provider's failure still fails the command")
	data, err := os.ReadFile(r.Path("carry.txt"))
	require.NoError(t, err)
	assert.Equal(t, "app sees exported-before-failing\n", string(data))
}

// TestRunFilterNarrowsToANamedPackage: --package narrows the run to that
// package within the window, while naming an unknown package or one whose
// space does not define the script fails instead of silently running nothing.
// The filter narrows, never widens: reaching an unchanged package takes a
// window that covers it.
func TestRunFilterNarrowsToANamedPackage(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(),
			Scripts: map[string]models.Script{"lint": {`echo "$DISPAT_PACKAGE" >> ../../lint.log`}}},
		"apps": {Path: models.PathList{"apps"}, Flow: buildPublish()}, // defines no lint
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.SeedPackage("apps", "web")
	r.Commit("feat(a,b,web): all three change")

	// Both a and b changed; the filter narrows the run to b alone.
	res := r.Command("run", "lint", "--package", "b")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "b\n", string(data), "only the named package runs")

	assert.Equal(t, 1, r.Command("run", "lint", "-p", "ghost").Code, "an unknown package is an error")
	assert.Equal(t, 1, r.Command("run", "lint", "-p", "web").Code,
		"a selection whose space does not define the script is an error, not a silent no-op")

	// The filter narrows the window rather than replacing it.
	r.ReleaseOK() // consume every pending change
	res = r.Command("run", "lint", "-p", "a")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "b\n", string(data), "an unchanged package is out of the window")

	res = r.Command("run", "lint", "--since", "all", "-p", "a")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "b\na\n", string(data), "a window covering every package reaches it")
}

// TestRunNarrowsToTheInvokedPackage: invoked from inside a package folder (or
// any subdirectory of it), a run covers that package alone — riding the config
// ascent to find the root — while from the monorepo top it still covers every
// changed package. Both spellings behave the same: the folder is the filter,
// not a property of the shorthand.
func TestRunNarrowsToTheInvokedPackage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	withRunScript := cfg.Spaces["libs"]
	withRunScript.Scripts = map[string]models.Script{"lint": {`echo "$DISPAT_PACKAGE" >> ../../lint.log`}}
	cfg.Spaces["libs"] = withRunScript
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a,b): both change")

	// From inside a: only a runs, even though b changed too.
	res := r.CommandAt("packages/a", "lint")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "a\n", string(data), "the shorthand narrows to the invoked package")

	// From a nested subdirectory of the package: still a.
	r.WriteFile("packages/a/internal/keep.txt", "x")
	res = r.CommandAt("packages/a/internal", "lint")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "a\na\n", string(data))

	// The two-word spelling narrows identically.
	res = r.CommandAt("packages/a", "run", "lint")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "a\na\na\n", string(data))

	// An explicit term beats the folder it was typed in.
	res = r.CommandAt("packages/a", "lint", "-p", "b")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.Equal(t, "a\na\na\nb\n", string(data))

	// From the monorepo top the shorthand still covers every changed package.
	r.Remove("lint.log")
	r.Command("lint")
	data, err = os.ReadFile(r.Path("lint.log"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, strings.Fields(string(data)))
}

// consumersRepo builds the --consumers fixture: a three-link chain
// core <- mid <- app plus an independent extra, all in one space defining a
// "lint" script that logs the package name and a "fail" script that fails in
// core alone. Bootstrap-committed, then one more commit touching core only,
// so `-s HEAD~1` addresses exactly core.
func consumersRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	spc := cfg.Spaces["libs"]
	spc.Scripts = map[string]models.Script{
		"lint": {`echo "$DISPAT_PACKAGE" >> ../../lint.log`},
		"fail": {`[ "$DISPAT_PACKAGE" != "core" ] && echo "$DISPAT_PACKAGE" >> ../../lint.log`},
	}
	cfg.Spaces["libs"] = spc
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "mid", Provider: "core"},
		{Consumer: "app", Provider: "mid"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "mid")
	r.SeedPackage("packages", "app")
	r.SeedPackage("packages", "extra")
	r.Commit("feat(core,mid,app,extra): bootstrap the chain")
	r.WriteFile("packages/core/touch.txt", "x")
	r.Commit("chore(core): touch core alone")
	return r
}

// consumersLog returns the packages the fixture's scripts recorded, in
// execution order.
func consumersLog(t *testing.T, r *harness.Repo) []string {
	t.Helper()
	data, err := os.ReadFile(r.Path("lint.log"))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// TestRunConsumersExpandTransitively: a --since window covers only what the
// commits address; --consumers adds every transitive dependent — app is
// reached through mid, not by any commit — providers still running first,
// and packages nothing depends on staying out.
func TestRunConsumersExpandTransitively(t *testing.T) {
	r := consumersRepo(t)

	// The gap being fixed: the window alone re-runs nothing downstream.
	r.RunScriptOK("lint", "--since", "HEAD~1")
	assert.Equal(t, []string{"core"}, consumersLog(t, r), "the window covers what the commit addressed")

	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "HEAD~1", "--consumers")
	assert.Equal(t, []string{"core", "mid", "app"}, consumersLog(t, r),
		"consumers are pulled in transitively (app only depends on core through mid), in graph order")

	// Everything already selected: the expansion is a no-op.
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "all", "--consumers")
	assert.ElementsMatch(t, []string{"core", "mid", "app", "extra"}, consumersLog(t, r))
}

// TestRunConsumersOnReleaseWindow: the default window has the same gap when
// propagation does not reach the consumers (the default depth is 0) —
// --consumers closes it there too.
func TestRunConsumersOnReleaseWindow(t *testing.T) {
	r := consumersRepo(t)

	r.RunScriptOK("lint")
	assert.ElementsMatch(t, []string{"core", "mid", "app", "extra"}, consumersLog(t, r),
		"the bootstrap feat put every package in the release window")

	// Release, then change core alone: the next window holds only core.
	r.ReleaseOK()
	r.WriteFile("packages/core/more.txt", "x")
	r.Commit("feat(core): core moves on")

	r.Remove("lint.log")
	r.RunScriptOK("lint")
	assert.Equal(t, []string{"core"}, consumersLog(t, r), "depth-0 propagation reaches no consumer")

	r.Remove("lint.log")
	r.RunScriptOK("lint", "--consumers")
	assert.Equal(t, []string{"core", "mid", "app"}, consumersLog(t, r),
		"--consumers re-runs the dependents of the released change")
}

// TestRunConsumersSkipCascade: an expanded consumer is a full member of the
// run — a failing provider script skips it transitively under the default
// --on-error skip, and --on-error continue runs it anyway.
func TestRunConsumersSkipCascade(t *testing.T) {
	r := consumersRepo(t)

	res := r.RunScript("fail", "--since", "HEAD~1", "--consumers")
	assert.Equal(t, 1, res.Code, "a failing script fails the command")
	assert.Empty(t, consumersLog(t, r), "mid and app are skipped transitively behind core's failure")

	res = r.RunScript("fail", "--since", "HEAD~1", "--consumers", "--on-error", "continue")
	assert.Equal(t, 1, res.Code)
	assert.Equal(t, []string{"mid", "app"}, consumersLog(t, r), "continue still runs the dependents, in order")
}

// TestRunConsumersComposeWithAFilter: --consumers expands the filtered
// selection rather than refusing it, and the expansion is not filtered back
// out — asking for a package's consumers is asking for packages the filter
// itself never named. The folder spelling behaves identically.
func TestRunConsumersComposeWithAFilter(t *testing.T) {
	r := consumersRepo(t)

	res := r.Command("run", "lint", "--since", "all", "-p", "mid", "--consumers")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"mid", "app"}, consumersLog(t, r),
		"mid and its dependents, in graph order; core and extra stay out")

	r.Remove("lint.log")
	res = r.CommandAt("packages/mid", "lint", "--since", "all", "--consumers")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"mid", "app"}, consumersLog(t, r),
		"the invocation folder is the same filter, so it expands the same way")
}

// TestRunSinceSelectsByCommitScopes: --since re-scopes the run from the
// release window to what the commits since a revision address, under the
// planner's own scope semantics — a written scope is authoritative even when
// the files say otherwise, and only a scopeless unit falls back to the files
// it changed (§6.2). "all" selects every package, an unknown revision fails
// loudly, and a package filter narrows the window the flag chose.
func TestRunSinceSelectsByCommitScopes(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	withRunScript := cfg.Spaces["libs"]
	withRunScript.Scripts = map[string]models.Script{"lint": {`echo "$DISPAT_PACKAGE" >> ../../lint.log`}}
	cfg.Spaces["libs"] = withRunScript
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.SeedPackage("packages", "c")
	r.Commit("feat(a,b,c): bootstrap all three")
	// A second commit touching only b's folder.
	r.WriteFile("packages/b/extra.txt", "x")
	r.Commit("chore(b): touch b alone")

	lintLog := func() []string {
		t.Helper()
		data, err := os.ReadFile(r.Path("lint.log"))
		require.NoError(t, err)
		return strings.Fields(string(data))
	}

	// The whole release window still covers all three...
	r.RunScriptOK("lint")
	assert.ElementsMatch(t, []string{"a", "b", "c"}, lintLog())

	// ...while --since HEAD~1 narrows to what the last commit touched.
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "HEAD~1")
	assert.Equal(t, []string{"b"}, lintLog(), "only the package the commit touched")

	// The reserved "all" value.
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "all")
	assert.ElementsMatch(t, []string{"a", "b", "c"}, lintLog(), "'all' selects every package")

	res := r.RunScript("lint", "--since", "no-such-rev")
	assert.Equal(t, 1, res.Code, "an unknown revision must fail, not silently select nothing")

	// A filter narrows whichever window the flag chose.
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "all", "-p", "a,c")
	assert.Equal(t, []string{"a", "c"}, lintLog(), "the filter narrows the window")

	// Scopes are authoritative: a commit whose files sit under c but whose
	// scope names a selects a, not c.
	r.WriteFile("packages/c/scoped-elsewhere.txt", "x")
	r.Commit("fix(a): scoped to a, files under c")
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "HEAD~1")
	assert.Equal(t, []string{"a"}, lintLog(), "the written scope wins over the changed files")

	// A scopeless unit falls back to the files it changed (§6.2).
	r.WriteFile("packages/c/derived.txt", "x")
	r.Commit("fix: no scope written, files under c")
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "HEAD~1")
	assert.Equal(t, []string{"c"}, lintLog(), "a scopeless unit derives its packages from its files")

	// A filter that empties the window runs nothing, and says so by exiting 0.
	r.Remove("lint.log")
	r.RunScriptOK("lint", "--since", "HEAD~1", "-p", "a")
	assert.NoFileExists(t, r.Path("lint.log"), "a package outside the window is an honest no-op")
}

// argsScript is the fixture the forwarding scenarios share: a helper the run
// script invokes, which records one line per argument it was handed, tagged
// with the package it ran in.
//
// It is shaped the way a real consumer of this feature is. The arguments are
// appended to the *command text*, so what receives them is whatever program
// the script ends in; here that is the helper, which takes the package name
// first and treats everything after it as forwarded.
const argsScript = `pkg=$1; shift; for a in "$@"; do printf '%s|%s\n' "$pkg" "$a" >> "$LOG"; done`

// seedArgsFixture writes the helper and returns a reader for what it recorded.
func seedArgsFixture(r *harness.Repo, cfg *models.File, names []string) func() []string {
	r.WriteFile("args.sh", argsScript+"\n")
	cfg.Scripts["show"] = models.Script{`LOG=../../args.log sh ../../args.sh "$DISPAT_PACKAGE"`}
	r.WriteConfigModel(*cfg)
	seedIndependentPackages(r, names)
	return func() []string {
		data, err := os.ReadFile(r.Path("args.log"))
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// TestRunForwardsArgumentsAfterTheDash: `dispat run show -- --watch` hands the
// script what followed the dash, the way `npm run test -- --watch` does.
//
// Three claims no unit test can make. The arguments reach *every* covered
// package, because the invocation is one intent about the selection rather
// than about whichever package the scheduler reaches first. An argument
// carrying a space arrives as one argument rather than two, which is the whole
// reason the quoting exists. And a bare word is still a usage error, so the
// rule that the selection is a flag survives the feature intact.
func TestRunForwardsArgumentsAfterTheDash(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	lines := seedArgsFixture(r, &cfg, []string{"a", "b"})

	r.RunScriptOK("show", "--", "--watch", "--reporter=dot")
	assert.ElementsMatch(t, []string{
		"a|--watch", "a|--reporter=dot",
		"b|--watch", "b|--reporter=dot",
	}, lines(), "every covered package gets every argument")

	// One argument, not two: the space is inside it.
	r.Remove("args.log")
	r.RunScriptOK("show", "--", "my suite")
	assert.ElementsMatch(t, []string{"a|my suite", "b|my suite"}, lines(),
		"an argument carrying a space arrives whole")

	// A quote, a dollar and a semicolon are text, never syntax.
	r.Remove("args.log")
	r.RunScriptOK("show", "--", "it's", "$HOME", "a;b")
	assert.ElementsMatch(t, []string{
		"a|it's", "a|$HOME", "a|a;b",
		"b|it's", "b|$HOME", "b|a;b",
	}, lines(), "a forwarded argument is data, not shell syntax")

	// Nothing after the dash is the invocation that existed before this did.
	r.Remove("args.log")
	r.RunScriptOK("show")
	assert.Empty(t, lines(), "no arguments, nothing appended")

	// The rule that survives all of it.
	assert.Equal(t, 2, r.RunScript("show", "a").Code,
		"a bare word is still a usage error: the selection is a flag")
}

// TestRunShorthandForwardsArgumentsToo: `dispat show -- --fix` is
// `dispat run show -- --fix`, so the shorthand cannot be the spelling that
// quietly drops them.
func TestRunShorthandForwardsArgumentsToo(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	lines := seedArgsFixture(r, &cfg, []string{"a"})

	res := r.Command("show", "--", "--fix")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"a|--fix"}, lines())
}

// TestRunMultiCommandScript: a name bound to several commands runs all of
// them, in order, as separate shell invocations in the package folder.
//
// Two claims only the real binary can make. The commands are separate
// processes rather than one string dispat joined together, which the `cd`
// proves: it moves the first command's shell and nothing else, so the second
// still writes where the script started. And the order is the written one, per
// package, which is what makes a sequence worth writing as one.
func TestRunMultiCommandScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["steps"] = models.Script{
		"echo one-$DISPAT_PACKAGE >> ../../steps.log",
		"cd /",
		"echo two-$DISPAT_PACKAGE >> ../../steps.log",
	}
	r.WriteConfigModel(cfg)
	seedIndependentPackages(r, []string{"a"})

	r.RunScriptOK("steps")
	data, err := os.ReadFile(r.Path("steps.log"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one-a", "two-a"}, strings.Fields(string(data)),
		"both commands ran, in order, and the cd did not move the second")
}

// TestRunMultiCommandScriptStopsAtAFailure: the sequence gates its own
// remainder, so the command after a failing one never runs and the run fails.
func TestRunMultiCommandScriptStopsAtAFailure(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["steps"] = models.Script{
		"echo one >> ../../steps.log",
		"exit 3",
		"echo three >> ../../steps.log",
	}
	r.WriteConfigModel(cfg)
	seedIndependentPackages(r, []string{"a"})

	assert.Equal(t, 1, r.RunScript("steps").Code, "a failed script fails the run")
	data, err := os.ReadFile(r.Path("steps.log"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one"}, strings.Fields(string(data)),
		"the command after the failure never ran")
}

// TestRunMultiCommandScriptArgumentsLandOnTheLast: `--` arguments go to the
// script's work, which is its last command; the setup steps before it are left
// as the config wrote them.
func TestRunMultiCommandScriptArgumentsLandOnTheLast(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	r.WriteFile("args.sh", argsScript+"\n")
	cfg.Scripts["show"] = models.Script{
		`echo setup >> ../../args.log`,
		`LOG=../../args.log sh ../../args.sh "$DISPAT_PACKAGE"`,
	}
	r.WriteConfigModel(cfg)
	seedIndependentPackages(r, []string{"a"})

	r.RunScriptOK("show", "--", "--watch")
	data, err := os.ReadFile(r.Path("args.log"))
	require.NoError(t, err)
	assert.Equal(t, []string{"setup", "a|--watch"},
		strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"),
		"the setup step ran untouched; the last command took the argument")
}
