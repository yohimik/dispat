package release

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The observer is the executor's one window for external progress reporting,
// so what is tested here is the contract its consumers build on: every
// terminal outcome is announced exactly once, stage transitions bracket the
// work they name, and each event is a self-contained snapshot — the package's
// versions and channel travel with it.

// fakeObserver records every event under a mutex: the executor calls it from
// concurrent task goroutines.
type fakeObserver struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeObserver) Event(ev Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

// forPackage returns the recorded event names of one package, in order.
func (f *fakeObserver) forPackage(pkg string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, ev := range f.events {
		if ev.Package == pkg {
			names = append(names, ev.Name+":"+ev.Stage)
		}
	}
	return names
}

// find returns the first event of the given name for the package.
func (f *fakeObserver) find(pkg, name string) (Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ev := range f.events {
		if ev.Package == pkg && ev.Name == name {
			return ev, true
		}
	}
	return Event{}, false
}

func runObserved(t *testing.T, p *plan.Plan, r *fakeRunner) (*fakeObserver, map[string]*Result) {
	t.Helper()
	obs := &fakeObserver{}
	e := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4})
	e.Observer = obs
	return obs, e.Run(context.Background(), p)
}

func TestObserverPublishedSequence(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	obs, res := runObserved(t, p, &fakeRunner{})

	require.Equal(t, StatusPublished, res["a"].Status)
	// Stages bracket in order, and the terminal outcome comes last. The
	// events carry their stamp of time; ordering is the observer's record.
	assert.Equal(t, []string{
		"stage.started:build",
		"stage.succeeded:build",
		"stage.started:publish",
		"stage.succeeded:publish",
		"package.published:",
	}, obs.forPackage("a"))

	ev, ok := obs.find("a", EventPackagePublished)
	require.True(t, ok)
	assert.Equal(t, "published", ev.Status)
	assert.Equal(t, "1.0.1", ev.Version)
	assert.Equal(t, "1.0.0", ev.PreviousVersion)
	assert.NotEmpty(t, ev.Tag, "a published event names the release tag")
	assert.False(t, ev.Time.IsZero(), "every event is stamped when it happened")
}

func TestObserverFailedBuild(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	obs, res := runObserved(t, p, &fakeRunner{fail: map[string]bool{"build a": true}})

	require.Equal(t, StatusFailed, res["a"].Status)
	ev, ok := obs.find("a", EventPackageFailed)
	require.True(t, ok)
	assert.Equal(t, "failed", ev.Status)
	assert.Equal(t, "build", ev.FailedStage)
	assert.NotEmpty(t, ev.Error)
	// The build never succeeded and the publish never started: the observer
	// sees exactly what happened, not the plan's hopes.
	assert.Equal(t, []string{"stage.started:build", "package.failed:"}, obs.forPackage("a"))
}

func TestObserverBlockedSkip(t *testing.T) {
	// a's publish fails; b consumes a and has no changes of its own, so it is
	// skipped with the same W194 the log line carries.
	p := mkPlan(planSpec{WaitPublish: true, Deps: map[string][]string{"b": {"a"}}, Names: []string{"a", "b"}})
	obs, res := runObserved(t, p, &fakeRunner{fail: map[string]bool{"publish a": true}})

	require.Equal(t, StatusSkipped, res["b"].Status)
	ev, ok := obs.find("b", EventPackageSkipped)
	require.True(t, ok)
	assert.Equal(t, "skipped", ev.Status)
	assert.Equal(t, plan.CodeBlocked, ev.Code)
	assert.Equal(t, "a", ev.BlockedBy)
	// The skip is b's only event: none of its stages ever started.
	assert.Equal(t, []string{"package.skipped:"}, obs.forPackage("b"))
}

func TestObserverGatingHookFailure(t *testing.T) {
	// A beforeAll hook fails the package before its first stage frame opens,
	// so the observer sees the failure and no stage.started at all: events
	// report what happened, and no stage had started yet. The failedStage
	// still names the task the hook was gating.
	p := mkPlan(planSpec{Names: []string{"a"}})
	p.Releases["a"].Pkg.Space.BeforeAllScript = []string{"prep"}
	obs, res := runObserved(t, p, &fakeRunner{fail: map[string]bool{"prep a": true}})

	require.Equal(t, StatusFailed, res["a"].Status)
	ev, ok := obs.find("a", EventPackageFailed)
	require.True(t, ok)
	assert.Equal(t, "build", ev.FailedStage)
	assert.Equal(t, []string{"package.failed:"}, obs.forPackage("a"))
}

func TestObserverCancelled(t *testing.T) {
	// A context cancelled before the run starts leaves every package pending;
	// the drain abort flips them to cancelled and each is announced once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := mkPlan(planSpec{Names: []string{"a", "b"}})
	obs := &fakeObserver{}
	e := newExecutor(execSpec{Runner: &fakeRunner{}, Build: 4, Publish: 4})
	e.Observer = obs
	res := e.Run(ctx, p)

	for _, name := range []string{"a", "b"} {
		require.Equal(t, StatusCancelled, res[name].Status)
		ev, ok := obs.find(name, EventPackageCancelled)
		require.True(t, ok, "cancelled package %q must be announced", name)
		assert.Equal(t, "cancelled", ev.Status)
		assert.Equal(t, []string{"package.cancelled:"}, obs.forPackage(name))
	}
}
