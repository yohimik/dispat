package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// fakeWork is a packageWork that records what it was asked to do and answers
// from a script: which packages fail, which resolve to nothing, which fail to
// resolve at all.
type fakeWork struct {
	mu       sync.Mutex
	started  []string // in start order
	finished []string
	fail     map[string]bool
	nothing  map[string]bool
	unable   map[string]bool // resolve itself fails
	block    chan struct{}   // when set, every task waits on it before finishing
}

func (w *fakeWork) stage() string { return "fake" }

func (w *fakeWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	pkg := rel.Pkg.Name
	if w.unable[pkg] {
		return nil, errors.New("cannot resolve " + pkg)
	}
	if w.nothing[pkg] {
		return nil, nil
	}
	return func(context.Context) error {
		w.mu.Lock()
		w.started = append(w.started, pkg)
		w.mu.Unlock()
		if w.block != nil {
			<-w.block
		}
		w.mu.Lock()
		w.finished = append(w.finished, pkg)
		w.mu.Unlock()
		if w.fail[pkg] {
			return errors.New(pkg + " failed")
		}
		return nil
	}, nil
}

func (w *fakeWork) order() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.finished...)
}

// sweepApp is an App whose build budget is one, so a sweep's launch order is
// its execution order and the assertions can be exact.
func sweepApp(t *testing.T, budget int) (*App, *plan.Plan) {
	t.Helper()
	root := t.TempDir()
	// b consumes a, c consumes b, d is independent.
	pl := runPlan(root, []string{"a", "b", "c", "d"}, map[string][]string{"b": {"a"}, "c": {"b"}})
	cfg := &config.File{Spaces: map[string]config.SpaceConfig{"libs": {Path: config.PathList{"libs"}}},
		BuildConcurrency: budget}
	return New(root, cfg, zerolog.Nop()), pl
}

func TestSweepRunsProvidersFirst(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b", "c", "d"}, w, sweepOptions{})
	require.NoError(t, err)
	// d is ready from the start and b only becomes ready when a finishes, so a
	// serial sweep takes the independent package first. What the edges promise
	// is the relative order a -> b -> c, and that holds.
	assert.Equal(t, []string{"a", "d", "b", "c"}, w.order(), "a package waits for its providers")
	assert.Equal(t, sweepReport{Ran: 4, Resolved: 4}, rep)
}

func TestSweepCoversOnlyWhatItWasGiven(t *testing.T) {
	// An edge to a package outside the sweep orders nothing: c is handed out
	// straight away even though b, its provider, was left out.
	a, pl := sweepApp(t, 1)
	w := &fakeWork{}
	rep, err := a.runSweep(context.Background(), pl, []string{"c", "a"}, w, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "a"}, w.order(), "the covered order is the sweep's own")
	assert.Equal(t, 2, rep.Ran)
}

// TestSweepSkipCascade: under the default policy a failed package's dependents
// are skipped transitively, and an independent package still runs.
func TestSweepSkipCascade(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{fail: map[string]bool{"a": true}}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b", "c", "d"}, w, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "d"}, w.order(), "b and c never ran")
	assert.Equal(t, sweepReport{Ran: 1, Failed: 1, Skipped: 2, Resolved: 4}, rep)
}

func TestSweepOnErrorContinueRunsTheDependents(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{fail: map[string]bool{"a": true}}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b", "c", "d"}, w,
		sweepOptions{OnError: OnErrorContinue})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "d", "b", "c"}, w.order(), "the failure stopped nothing")
	assert.Equal(t, sweepReport{Ran: 3, Failed: 1, Resolved: 4}, rep)
}

// TestSweepCountsAPackageWithNothingToDo: a nil task is a no-op that still
// happened, and it is not counted as resolved — which is what tells a sweep
// that covered only packages with nothing to do apart from one that ran.
func TestSweepCountsAPackageWithNothingToDo(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{nothing: map[string]bool{"a": true, "b": true}}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b", "c"}, w, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, w.order())
	assert.Equal(t, sweepReport{Ran: 1, Resolved: 1}, rep)
}

// TestSweepResolveFailureIsAPackageFailure: a work that cannot even work out
// what to do fails that package, and cascades exactly as a failed task does.
func TestSweepResolveFailureIsAPackageFailure(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{unable: map[string]bool{"a": true}}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b", "d"}, w, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"d"}, w.order())
	// a never resolved, so it had nothing to do that anyone could see; b and d
	// both did.
	assert.Equal(t, sweepReport{Ran: 1, Failed: 1, Skipped: 1, Resolved: 2}, rep)
}

// TestSweepSkipIsDecidedAfterResolving: a package the cascade skips still
// counted as one that had something to do, so a run that skipped everything is
// not mistaken for a run over packages that defined nothing.
func TestSweepSkipIsDecidedAfterResolving(t *testing.T) {
	a, pl := sweepApp(t, 1)
	w := &fakeWork{fail: map[string]bool{"a": true}}
	rep, err := a.runSweep(context.Background(), pl, []string{"a", "b"}, w, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Resolved, "the skipped package resolved before it was skipped")
	assert.Equal(t, 1, rep.Skipped)
}

// TestSweepBudgetBoundsTheParallelism: with a budget of one, a blocked package
// stops everything behind it; the independent packages are simply queued.
func TestSweepBudgetBoundsTheParallelism(t *testing.T) {
	a, pl := sweepApp(t, 2)
	w := &fakeWork{block: make(chan struct{})}
	done := make(chan sweepReport, 1)
	go func() {
		rep, err := a.runSweep(context.Background(), pl, []string{"a", "d", "b"}, w, sweepOptions{})
		assert.NoError(t, err)
		done <- rep
	}()

	assert.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.started) == 2
	}, time.Second, time.Millisecond, "two independent packages fit the budget of two")
	w.mu.Lock()
	assert.ElementsMatch(t, []string{"a", "d"}, w.started, "b waits for a, not for the budget")
	w.mu.Unlock()
	close(w.block)
	assert.Equal(t, sweepReport{Ran: 3, Resolved: 3}, <-done)
}

// TestSweepSerialBudget: a budget of one is what the commands writing a shared
// resource ask for, and it holds even for packages with no edge between them.
func TestSweepSerialBudget(t *testing.T) {
	a, pl := sweepApp(t, 8)
	var peak, live int
	var mu sync.Mutex
	w := &countingWork{onStart: func() {
		mu.Lock()
		live++
		peak = max(peak, live)
		mu.Unlock()
	}, onEnd: func() {
		mu.Lock()
		live--
		mu.Unlock()
	}}
	_, err := a.runSweep(context.Background(), pl, []string{"a", "d"}, w, sweepOptions{Budget: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, peak, "a serial sweep never has two packages in flight")
}

func TestSweepCancellationStopsLaunching(t *testing.T) {
	a, pl := sweepApp(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &fakeWork{}
	rep, err := a.runSweep(ctx, pl, []string{"a", "b"}, w, sweepOptions{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, w.order(), "a cancelled sweep launches nothing")
	assert.Equal(t, sweepReport{}, rep)
}

func TestSweepOverNoPackagesIsCleanlyEmpty(t *testing.T) {
	a, pl := sweepApp(t, 1)
	rep, err := a.runSweep(context.Background(), pl, nil, &fakeWork{}, sweepOptions{})
	require.NoError(t, err)
	assert.Equal(t, sweepReport{}, rep)
}

func TestCoveredReleasesIndexesTheCoveredPackages(t *testing.T) {
	_, pl := sweepApp(t, 1)
	rels := coveredReleases(pl, []string{"a", "c"})
	assert.Len(t, rels, 2)
	assert.Same(t, pl.Releases["a"], rels["a"])
	_, ok := rels["b"]
	assert.False(t, ok, "an uncovered package has no entry")
}

func TestBudgetForAsksTheWork(t *testing.T) {
	assert.Equal(t, 0, budgetFor(&fakeWork{}), "an ordinary work rides the build budget")
	assert.Equal(t, 1, budgetFor(&commitWork{}), "the git commands are serial")
	assert.Equal(t, 1, budgetFor(&githubWork{}))
}

// countingWork calls back on either side of every package's work, which is how
// the budget assertions watch the parallelism.
type countingWork struct{ onStart, onEnd func() }

func (w *countingWork) stage() string { return "counting" }

func (w *countingWork) resolve(context.Context, *plan.Release) (task, error) {
	return func(context.Context) error {
		w.onStart()
		time.Sleep(2 * time.Millisecond)
		w.onEnd()
		return nil
	}, nil
}
