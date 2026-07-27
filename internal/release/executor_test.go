package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/monorel/internal/conventional"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/semver"
)

// fakeRunner records "command pkgDir" events, tracks per-command concurrency
// and can fail selected events.
type fakeRunner struct {
	mu      sync.Mutex
	events  []string
	fail    map[string]bool
	delay   time.Duration
	current map[string]int
	maxCur  map[string]int
}

func (r *fakeRunner) Run(_ context.Context, dir, command string, _, _ io.Writer) error {
	key := command + " " + dir
	r.mu.Lock()
	if r.current == nil {
		r.current = map[string]int{}
		r.maxCur = map[string]int{}
	}
	r.events = append(r.events, key)
	r.current[command]++
	if r.current[command] > r.maxCur[command] {
		r.maxCur[command] = r.current[command]
	}
	shouldFail := r.fail[key]
	r.mu.Unlock()

	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	r.mu.Lock()
	r.current[command]--
	r.mu.Unlock()
	if shouldFail {
		return errors.New("boom")
	}
	return nil
}

func (r *fakeRunner) indexOf(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.events {
		if e == key {
			return i
		}
	}
	return -1
}

type fakeTagger struct {
	mu   sync.Mutex
	tags []string
}

func (f *fakeTagger) CreateTag(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = append(f.tags, name)
	return nil
}

type fakeChangelog struct {
	mu      sync.Mutex
	entries []string
}

func (f *fakeChangelog) Append(rel *plan.Release) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, rel.Pkg.Name)
	return nil
}

// mkPlan builds a plan of changed packages. waitPublish applies to the "libs"
// space that every package lives in. ownBump marks packages with own commits.
func mkPlan(waitPublish bool, ownBump map[string]semver.Bump, deps map[string][]string, names ...string) *plan.Plan {
	libs := &model.Space{Name: "libs", BuildWaitsPublish: waitPublish, BuildScript: "build", PublishScript: "publish"}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{},
		Consumers: map[string][]string{},
	}
	for _, n := range names {
		own := ownBump[n]
		bump := semver.Max(own, semver.BumpPatch) // every listed package is changed
		rel := &plan.Release{
			Pkg:     &model.Package{Name: n, Dir: n, Space: libs},
			Current: semver.Version{Major: 1},
			OwnBump: own,
			Bump:    bump,
		}
		if own != semver.BumpNone {
			rel.Commits = []conventional.Commit{{Kind: conventional.KindFix, Scope: n, Description: "own change"}}
		}
		rel.Next = rel.Current.Bumped(bump)
		p.Releases[n] = rel
		p.Order = append(p.Order, n)
	}
	for consumer, provs := range deps {
		p.Providers[consumer] = provs
		for _, prov := range provs {
			p.Consumers[prov] = append(p.Consumers[prov], consumer)
		}
	}
	return p
}

func newExecutor(r *fakeRunner, tg *fakeTagger, cl *fakeChangelog, buildConc, publishConc int) *Executor {
	return &Executor{
		BuildConcurrency:   buildConc,
		PublishConcurrency: publishConc,
		Runner:             r,
		Tagger:             tg,
		Changelog:          cl,
		Log:                zerolog.Nop(),
	}
}

func TestRunSuccessOrder(t *testing.T) {
	// b consumes a.
	p := mkPlan(false, nil, map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{}
	tg := &fakeTagger{}
	cl := &fakeChangelog{}
	res := newExecutor(r, tg, cl, 4, 4).Run(context.Background(), p)

	for _, n := range []string{"a", "b"} {
		require.Equal(t, StatusPublished, res[n].Status, "%s: %v", n, res[n].Err)
	}
	assert.Less(t, r.indexOf("build a"), r.indexOf("publish a"), "a builds before publishing")
	assert.Less(t, r.indexOf("build a"), r.indexOf("build b"), "provider builds before consumer")
	assert.Less(t, r.indexOf("publish a"), r.indexOf("publish b"), "provider publishes before consumer")
	assert.ElementsMatch(t, []string{"a@1.0.1", "b@1.0.1"}, tg.tags)
	assert.ElementsMatch(t, []string{"a", "b"}, cl.entries)
	assert.Equal(t, semver.Version{Major: 1, Patch: 1}, res["a"].To)
}

func TestRunBuildWaitsPublish(t *testing.T) {
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusPublished, res["b"].Status)
	assert.Less(t, r.indexOf("publish a"), r.indexOf("build b"),
		"with isBuildWaitingPublish, b may only build after a is published")
}

func TestRunFailureSkipsConsumer(t *testing.T) {
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{fail: map[string]bool{"publish a": true}}
	tg := &fakeTagger{}
	res := newExecutor(r, tg, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	assert.Error(t, res["a"].Err)
	require.Equal(t, StatusSkipped, res["b"].Status, "b has no changes of its own")
	assert.Equal(t, -1, r.indexOf("build b"), "skipped consumer must not run any script")
	assert.Empty(t, tg.tags)
}

func TestRunFailureConsumerWithOwnChangesProceeds(t *testing.T) {
	p := mkPlan(true, map[string]semver.Bump{"b": semver.BumpPatch},
		map[string][]string{"b": {"a"}}, "a", "b")
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["a"].Status)
	require.Equal(t, StatusPublished, res["b"].Status,
		"b must proceed despite provider failure: %v", res["b"].Err)
}

func TestRunSkipCascades(t *testing.T) {
	// chain a -> b -> c: a fails, b skipped, c skipped too.
	p := mkPlan(true, nil, map[string][]string{"b": {"a"}, "c": {"b"}}, "a", "b", "c")
	r := &fakeRunner{fail: map[string]bool{"build a": true}}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 4, 4).Run(context.Background(), p)

	assert.Equal(t, StatusSkipped, res["b"].Status)
	assert.Equal(t, StatusSkipped, res["c"].Status)
}

func TestRunConcurrencyLimits(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	p := mkPlan(false, nil, nil, names...)
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 2, 2).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 2, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 2, "publish budget")
}

func TestRunSeparateStageBudgets(t *testing.T) {
	names := []string{"p1", "p2", "p3", "p4"}
	p := mkPlan(false, nil, nil, names...)
	r := &fakeRunner{delay: 10 * time.Millisecond}
	res := newExecutor(r, &fakeTagger{}, &fakeChangelog{}, 3, 1).Run(context.Background(), p)

	for _, n := range names {
		require.Equal(t, StatusPublished, res[n].Status, n)
	}
	assert.LessOrEqual(t, r.maxCur["build"], 3, "build budget")
	assert.LessOrEqual(t, r.maxCur["publish"], 1, "publishes must be serialized")
	// With independent budgets, builds are allowed to overlap while a
	// publish is running, so builds should actually reach parallelism > 1.
	assert.Greater(t, r.maxCur["build"], 1, "builds should overlap")
}

func TestRunUnchangedExcluded(t *testing.T) {
	p := mkPlan(false, nil, nil, "a")
	// add an unchanged package to the plan
	p.Releases["quiet"] = &plan.Release{
		Pkg:     &model.Package{Name: "quiet", Dir: "quiet", Space: p.Releases["a"].Pkg.Space},
		Current: semver.Version{Major: 3},
		Next:    semver.Version{Major: 3},
	}
	p.Order = append(p.Order, "quiet")
	res := newExecutor(&fakeRunner{}, &fakeTagger{}, &fakeChangelog{}, 1, 1).Run(context.Background(), p)

	assert.NotContains(t, res, "quiet", "unchanged package must not appear in results")
	assert.Len(t, res, 1)
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusPending: "pending", StatusPublished: "published",
		StatusFailed: "failed", StatusSkipped: "skipped",
	}
	for st, want := range cases {
		assert.Equal(t, want, st.String())
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	log := zerolog.New(writerFunc(func(p []byte) (int, error) {
		got = append(got, string(p))
		return len(p), nil
	}))
	w := newLineWriter(log, zerolog.InfoLevel)
	fmt.Fprint(w, "hello\nwor")
	fmt.Fprint(w, "ld\ntail")
	w.Flush()
	require.Len(t, got, 3, "hello, world and the flushed tail")
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
