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
// nothing completes as a no-op; a name nothing defines, and a selection in
// which no package resolves it, are both errors. What a failure does to the
// failed package's dependents is the --on-error flag: "skip" (default) or
// "continue"; any failure makes the command exit 1.

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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{
			"lint":   "echo $DISPAT_PACKAGE >> ../../run.log",
			"record": `env | grep '^DISPAT_' | sort > run-env.txt`,
			"fail":   `[ "$DISPAT_PACKAGE" != "core" ] && echo $DISPAT_PACKAGE >> ../../run.log`,
		}},
		"tools": {Path: "tools", Flow: buildPublish()},
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
	cfg.Spaces["ghosts"] = models.SpaceConfig{Path: "nowhere", Flow: buildPublish()}
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
	cfg.Scripts["stamp"] = "echo $DISPAT_PACKAGE >> ../../run.log"
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
	tools.Scripts = map[string]string{"stamp": "echo $DISPAT_PACKAGE >> ../../run.log"}
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
		"app": {Scripts: map[string]string{"stamp": "echo $DISPAT_PACKAGE >> ../../run.log"}},
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
	cfg.Scripts["stamp"] = stamp("top")
	libs := cfg.Spaces["libs"]
	libs.Scripts["stamp"] = stamp("space")
	cfg.Spaces["libs"] = libs
	cfg.Packages = map[string]models.PackageConfig{
		"app": {Scripts: map[string]string{"stamp": stamp("package")}},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): define stamp at every level")

	r.RunScriptOK("stamp")
	assert.ElementsMatch(t, []string{"package-app", "space-core", "top-tool"}, runLog(r),
		"app takes its own, core takes its space's, tool takes the file's")
}

// TestRunNoSelectedPackageDefinesIt: the name exists — so this is not the
// typo guard — but nowhere in the selection, which would otherwise run
// nothing and report success. The mismatch between the name's level and the
// selection is an error.
func TestRunNoSelectedPackageDefinesIt(t *testing.T) {
	r := runRepo(t)
	cfg := runConfig()
	cfg.Packages = map[string]models.PackageConfig{
		"tool": {Scripts: map[string]string{"stamp": "echo $DISPAT_PACKAGE >> ../../run.log"}},
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore(core,app,tool): give tool a script of its own")
	r.ReleaseOK() // closes the window, so the next commit alone selects
	r.CommitEmpty("fix(core): only core changes now")

	res := r.RunScript("stamp")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Empty(t, runLog(r))

	// The same name over a selection that does contain tool is fine.
	r.RunScriptOK("stamp", "--since", "all")
	assert.Equal(t, []string{"tool"}, runLog(r))
}

// TestRunFilterRunsATopLevelScriptInOnePackage: a filtered run executes one
// top-level script inside one package's folder under the release environment,
// releasing nothing — reaching an unchanged package through `--since all`.
func TestRunFilterRunsATopLevelScriptInOnePackage(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["probe"] = `echo "$DISPAT_PACKAGE@$DISPAT_NEW_VERSION $DISPAT_STAGE" > probe.txt`
	cfg.Scripts["boom"] = "exit 7"
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
		cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
		cfg.Spaces = map[string]models.SpaceConfig{
			"libs": {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{
				"mark": r.TsmarkScript("run.log", "$DISPAT_PACKAGE", 200*time.Millisecond),
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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Versioning: models.VersioningFixed, Flow: buildPublish(),
			Scripts: map[string]string{"lint": "echo $DISPAT_PACKAGE >> ../../run.log"}},
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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{
			"mark": r.TsmarkScript("run.log", "$DISPAT_PACKAGE", 150*time.Millisecond),
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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	carry := `if [ "$DISPAT_PACKAGE" = "base" ]; then` +
		` echo "DISPAT_OUTPUT_FROM_BASE=hello-from-base" >> "$DISPAT_OUTPUT";` +
		` else echo "$DISPAT_PACKAGE sees $DISPAT_OUTPUT_FROM_BASE from $DISPAT_OUTPUT_SOURCE_FROM_BASE" >> ../../carry.txt; fi`
	cfg.Spaces = map[string]models.SpaceConfig{
		"outer":  {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{"carry": carry}},
		"middle": {Path: "middle", Flow: buildPublish()}, // no run scripts: a silent carrier
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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	failCarry := `if [ "$DISPAT_PACKAGE" = "core" ]; then` +
		` echo "MARK=exported-before-failing" >> "$DISPAT_OUTPUT"; exit 1;` +
		` else echo "app sees $DISPAT_OUTPUT_MARK" > ../../carry.txt; fi`
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), Scripts: map[string]string{"failcarry": failCarry}},
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
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(),
			Scripts: map[string]string{"lint": `echo "$DISPAT_PACKAGE" >> ../../lint.log`}},
		"apps": {Path: "apps", Flow: buildPublish()}, // defines no lint
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
	withRunScript.Scripts = map[string]string{"lint": `echo "$DISPAT_PACKAGE" >> ../../lint.log`}
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
	spc.Scripts = map[string]string{
		"lint": `echo "$DISPAT_PACKAGE" >> ../../lint.log`,
		"fail": `[ "$DISPAT_PACKAGE" != "core" ] && echo "$DISPAT_PACKAGE" >> ../../lint.log`,
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
	withRunScript.Scripts = map[string]string{"lint": `echo "$DISPAT_PACKAGE" >> ../../lint.log`}
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
