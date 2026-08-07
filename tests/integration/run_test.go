package integration

// Area 6: the `dispat run <script>` command. A space's runScripts entries
// are raw shell commands (not references into `scripts`); `dispat run x`
// (or the shorthand `dispat x`, when x is not a command name) executes x
// inside each *changed* package with the package's full DISPAT_* environment,
// honouring the dependency graph — providers before consumers, independent
// packages in parallel within the build concurrency budget. A changed
// package whose space does not define x completes as a no-op; a name no
// space defines is an error. What a failure does to the failed package's
// dependents is the --on-error flag: "skip" (default) or "continue"; any
// failure makes the command exit 1.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// runScriptsRepo builds the shared fixture of this file: a "libs" space
// (core <- app, by a dependency edge) defining the "lint", "record" and
// "fail" run scripts, and a "tools" space (tool) defining none of them. The
// commit changes all three packages, so every one of them is in the plan.
func runScriptsRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]string{"build": "echo building", "publish": "echo publishing"}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish(), RunScripts: map[string]string{
			"lint":   "echo $DISPAT_PACKAGE >> ../../run.log",
			"record": `env | grep '^DISPAT_' | sort > run-env.txt`,
			"fail":   `[ "$DISPAT_PACKAGE" != "core" ] && echo $DISPAT_PACKAGE >> ../../run.log`,
		}},
		"tools": {Path: "tools", Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
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
	r := runScriptsRepo(t)

	r.RunScriptOK("lint")
	assert.Equal(t, []string{"core", "app"}, runLog(r),
		"providers run before consumers; the tools space defines no lint and is skipped")
	assert.Empty(t, r.TagList(), "dispat run must not release anything")
}

// TestRunShorthandCommand: `dispat lint` is `dispat run lint` when "lint" is
// not a command name.
func TestRunShorthandCommand(t *testing.T) {
	r := runScriptsRepo(t)

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
	r := runScriptsRepo(t)

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

// TestRunUnknownScriptFails: a name no space defines is an error — running
// nothing silently is how a typo hides.
func TestRunUnknownScriptFails(t *testing.T) {
	r := runScriptsRepo(t)
	res := r.RunScript("format")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
}

// TestRunOnErrorPolicies: the "fail" script exits non-zero for core, the
// provider. Under the default --on-error=skip its dependent app is skipped;
// under --on-error=continue app still runs. Both exit 1 — the failure is
// never swallowed.
func TestRunOnErrorPolicies(t *testing.T) {
	t.Run("skip_dependents_by_default", func(t *testing.T) {
		r := runScriptsRepo(t)
		res := r.RunScript("fail")
		assert.Equal(t, 1, res.Code)
		assert.Empty(t, runLog(r), "app is a dependent of the failed core and must be skipped")
	})
	t.Run("continue_still_runs_dependents", func(t *testing.T) {
		r := runScriptsRepo(t)
		res := r.RunScript("fail", "--on-error", "continue")
		assert.Equal(t, 1, res.Code, "the failure still fails the command")
		assert.Equal(t, []string{"app"}, runLog(r), "app runs despite core's failure")
	})
	t.Run("unknown_policy_is_a_usage_error", func(t *testing.T) {
		r := runScriptsRepo(t)
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
			"libs": {Path: "packages", Flow: buildPublish(), RunScripts: map[string]string{
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
// run script executes zero times — `dispat run` targets the current plan's
// changed packages, not the whole workspace.
func TestRunSkipsUnchangedPackages(t *testing.T) {
	r := runScriptsRepo(t)
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
			RunScripts: map[string]string{"lint": "echo $DISPAT_PACKAGE >> ../../run.log"}},
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
		"libs": {Path: "packages", Flow: buildPublish(), RunScripts: map[string]string{
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
		"outer":  {Path: "packages", Flow: buildPublish(), RunScripts: map[string]string{"carry": carry}},
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
		"libs": {Path: "packages", Flow: buildPublish(), RunScripts: map[string]string{"failcarry": failCarry}},
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
