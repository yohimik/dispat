package integration

// Area 1: concurrency. architecture.md's "Task graph" section promises
// bounded, independent parallelism: at most BuildConcurrency build/version
// tasks and PublishConcurrency publish tasks in flight at once, the two
// budgets independent of each other, and a task entering a stage's ready
// queue only once its dependency edges are satisfied. Timing precision
// matters here — a one-second-resolution log cannot tell "ran concurrently"
// from "ran back to back within the same second" — so every test measures
// real nanosecond intervals via the tsmark probe, and every concurrency
// claim is checked three independent ways (two overlap counts, one
// start-order argument) inside harness.AssertConcurrencyBudget.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// budgets describes one budgetRepo fixture: the per-stage concurrency limits
// and the per-stage sleeps. Sleeps exist to make overlap observable: a
// zero-sleep script's start and end sit microseconds apart, too narrow for
// even genuinely concurrent processes to reliably straddle.
type budgets struct {
	Build        int
	Publish      int
	BuildSleep   time.Duration
	PublishSleep time.Duration
}

// budgetRepo writes a one-space config whose build and publish stages are
// wired to tsmark (per-package labels, the given sleeps) under the given
// stage budgets.
func budgetRepo(t *testing.T, b budgets) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(b.Build, b.Publish)
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", b.BuildSleep)},
		"publish": {r.TsmarkScript("publish.log", "$DISPAT_PACKAGE", b.PublishSleep)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	return r
}

// TestConcurrencyBuildBudgetEnforced: concurrency 4, five independent
// packages. The fifth package's build must start only once one of the first
// four frees a slot — proven by asserting the peak overlap is exactly 4,
// not 5 (a scheduler ignoring the budget would peak at 5; one serialising
// everything would peak at 1 and still satisfy "at most 4", which is why
// AssertConcurrencyBudget requires the peak be *reached*, not merely
// respected).
func TestConcurrencyBuildBudgetEnforced(t *testing.T) {
	const budget = 4
	names := packageNames(5, "pkg")
	r := budgetRepo(t, budgets{Build: budget, Publish: len(names), BuildSleep: 200 * time.Millisecond})
	seedIndependentPackages(r, names)

	r.ReleaseOK()

	tl := r.Timeline("build.log")
	require.Len(t, tl, len(names))
	harness.AssertConcurrencyBudget(t, tl, budget)
}

// TestConcurrencyPublishBudgetIsIndependentOfBuild pins the build budget
// high enough that it can never be the bottleneck and constrains only
// publish, proving the two stages carry separate budgets rather than one
// shared pool. In the same run the unconstrained builds must reach full
// overlap while the publishes stay capped — one timeline per stage, both
// from the same release.
func TestConcurrencyPublishBudgetIsIndependentOfBuild(t *testing.T) {
	const publishBudget = 2
	names := packageNames(5, "pkg")
	// The build sleep must dwarf process-launch jitter (single-digit
	// milliseconds on a loaded runner) because the assertion demands *full*
	// overlap of all five builds, not merely "some".
	r := budgetRepo(t, budgets{Build: len(names), Publish: publishBudget,
		BuildSleep: 150 * time.Millisecond, PublishSleep: 200 * time.Millisecond})
	seedIndependentPackages(r, names)

	r.ReleaseOK()

	build := r.Timeline("build.log")
	require.Len(t, build, len(names))
	harness.AssertConcurrencyBudget(t, build, len(names))

	publish := r.Timeline("publish.log")
	require.Len(t, publish, len(names))
	harness.AssertConcurrencyBudget(t, publish, publishBudget)
}

// TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited pins the
// two halves of the scheduling promise against each other in one graph:
// independent packages must be picked up together, and a package that
// depends on all of them must not start before every one of them has
// finished building — not "probably after", but never before. The ordering
// half is structural: downstream's build simply is not in the scheduler's
// ready queue until every provider's build has completed, whatever any
// script's duration; the sleep exists only to make the providers' own
// overlap observable.
func TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(3, 3)
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 150*time.Millisecond)},
		"publish": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "downstream", Provider: "a"},
		{Consumer: "downstream", Provider: "b"},
		{Consumer: "downstream", Provider: "c"},
	}
	r.WriteConfigModel(cfg)

	for _, name := range []string{"a", "b", "c", "downstream"} {
		r.SeedPackage("packages", name)
	}
	// The caret reaches downstream via propagation, so it releases in the
	// same run as its three providers without a commit of its own.
	r.Commit("feat(a,b,c)^: three independent providers with one shared consumer")

	r.ReleaseOK()

	tl := r.Timeline("build.log")
	require.Len(t, tl, 4)
	a, b, c := harness.Find(t, tl, "a"), harness.Find(t, tl, "b"), harness.Find(t, tl, "c")
	downstream := harness.Find(t, tl, "downstream")

	// Picked up concurrently: every pair of independent providers overlapped.
	harness.AssertOverlaps(t, a, b)
	harness.AssertOverlaps(t, b, c)
	harness.AssertOverlaps(t, a, c)
	harness.AssertConcurrencyBudget(t, []harness.Interval{a, b, c}, 3)

	// Dependant awaited: downstream's build cannot have started before every
	// provider's build finished.
	harness.AssertSequential(t, a, downstream)
	harness.AssertSequential(t, b, downstream)
	harness.AssertSequential(t, c, downstream)
}
