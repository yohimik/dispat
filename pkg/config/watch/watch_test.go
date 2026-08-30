package watch

// The watcher: what makes it reload, what makes it stop, and that stopping
// takes its goroutine with it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yohimik/dispat/pkg/config"
)

// appConfig is the smallest configuration worth reloading.
type appConfig struct {
	Name string
}

func appFields(dst *appConfig) config.Fields {
	return config.Fields{"name": config.String(&dst.Name)}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// replaceFile writes a file the way a careful writer does: a temp file beside
// it and a rename. It is the case a watch on the file itself would miss.
func replaceFile(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

// loader is the Load a caller writes: read the tree, decode it, hand back the
// value and the files it came from.
func loader(path string) func(context.Context) (appConfig, []string, error) {
	l := config.NewLoader(config.Options{})
	return func(ctx context.Context) (appConfig, []string, error) {
		tree, err := l.ReadTree(ctx, path)
		if err != nil {
			return appConfig{}, nil, err
		}
		var cfg appConfig
		if err := config.DecodeObject(tree.Settings(l, nil), "", appFields(&cfg)); err != nil {
			return appConfig{}, nil, err
		}
		return cfg, tree.Files, nil
	}
}

// updates collects what the watcher hands back, so a test can wait for a
// value rather than for a duration.
type updates struct {
	ch chan appConfig
}

func newUpdates() *updates { return &updates{ch: make(chan appConfig, 16)} }

func (u *updates) on(cfg appConfig) { u.ch <- cfg }

func (u *updates) next(t *testing.T) appConfig {
	t.Helper()
	select {
	case cfg := <-u.ch:
		return cfg
	case <-time.After(10 * time.Second):
		t.Fatal("no reload within ten seconds")
		return appConfig{}
	}
}

func start(t *testing.T, ctx context.Context, opts Options[appConfig]) *Watcher[appConfig] {
	t.Helper()
	w, err := Start(ctx, opts)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// TestStartLoadsOnceAndReportsItsFiles: the first load is synchronous, and a
// program that cannot read its configuration at startup hears about it there.
func TestStartLoadsOnceAndReportsItsFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)

	w := start(t, t.Context(), Options[appConfig]{Load: loader(path)})
	if got := w.Value().Name; got != "first" {
		t.Errorf("value = %q", got)
	}
	if files := w.Files(); len(files) != 1 || files[0] != path {
		t.Errorf("files = %#v", files)
	}

	_, err := Start(t.Context(), Options[appConfig]{Load: loader(filepath.Join(dir, "absent.json"))})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want the load's own failure", err)
	}
}

// TestReloadOnWrite: the plain case.
func TestReloadOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)
	seen := newUpdates()

	w := start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: 10 * time.Millisecond, OnUpdate: seen.on,
	})
	writeFile(t, path, `{"name": "second"}`)

	if got := seen.next(t).Name; got != "second" {
		t.Errorf("update = %q", got)
	}
	if got := w.Value().Name; got != "second" {
		t.Errorf("value = %q", got)
	}
}

// TestReloadOnAnAtomicReplace: a config replaced by a rename is the case a
// watch on the file itself would miss, and the reason the watch is on the
// directory.
func TestReloadOnAnAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)
	seen := newUpdates()

	start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: 10 * time.Millisecond, OnUpdate: seen.on,
	})
	replaceFile(t, path, `{"name": "replaced"}`)

	if got := seen.next(t).Name; got != "replaced" {
		t.Errorf("update = %q", got)
	}
}

// TestReloadOnARefTarget: the watch set is every file the load reported, so a
// configuration composed through $ref reloads when a fragment changes — and
// the fragment may live in another folder.
func TestReloadOnARefTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	frag := filepath.Join(dir, "cfg", "name.json")
	writeFile(t, frag, `"first"`)
	writeFile(t, path, `{"name": {"$ref": "./cfg/name.json"}}`)
	seen := newUpdates()

	w := start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: 10 * time.Millisecond, OnUpdate: seen.on,
	})
	if len(w.Files()) != 2 {
		t.Fatalf("files = %#v", w.Files())
	}
	writeFile(t, frag, `"second"`)

	if got := seen.next(t).Name; got != "second" {
		t.Errorf("update = %q", got)
	}
}

// TestWatchSetFollowsTheFiles: a reload that changes which fragments are
// involved changes what is watched, so the new one is live and the old one is
// not.
func TestWatchSetFollowsTheFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	old := filepath.Join(dir, "old", "name.json")
	fresh := filepath.Join(dir, "new", "name.json")
	writeFile(t, old, `"from-old"`)
	writeFile(t, fresh, `"from-new"`)
	writeFile(t, path, `{"name": {"$ref": "./old/name.json"}}`)
	seen := newUpdates()

	w := start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: 10 * time.Millisecond, OnUpdate: seen.on,
	})
	writeFile(t, path, `{"name": {"$ref": "./new/name.json"}}`)
	if got := seen.next(t).Name; got != "from-new" {
		t.Fatalf("update = %q", got)
	}

	writeFile(t, fresh, `"changed"`)
	if got := seen.next(t).Name; got != "changed" {
		t.Errorf("the new fragment is watched: %q", got)
	}
	if files := w.Files(); len(files) != 2 || files[1] != fresh {
		t.Errorf("files = %#v", files)
	}
}

// TestDebounceCoalesces: one save arrives as several events and a multi-file
// edit as several more, so the flurry is one reload.
func TestDebounceCoalesces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)

	var loads atomic.Int64
	inner := loader(path)
	seen := newUpdates()
	start(t, t.Context(), Options[appConfig]{
		Load: func(ctx context.Context) (appConfig, []string, error) {
			loads.Add(1)
			return inner(ctx)
		},
		Debounce: 150 * time.Millisecond,
		OnUpdate: seen.on,
	})

	for i := 0; i < 20; i++ {
		writeFile(t, path, `{"name": "burst"}`)
	}
	writeFile(t, path, `{"name": "last"}`)

	if got := seen.next(t).Name; got != "last" {
		t.Errorf("update = %q", got)
	}
	// One load at Start, and one for the whole flurry. A slow machine may
	// split the burst across two windows; three would mean no debouncing.
	if got := loads.Load(); got > 3 {
		t.Errorf("%d loads for one burst", got)
	}
}

// TestReloadFailureKeepsTheLastGoodValue: a configuration that stops parsing
// is a reason to go on running with the one that did.
func TestReloadFailureKeepsTheLastGoodValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "good"}`)

	failures := make(chan error, 4)
	w := start(t, t.Context(), Options[appConfig]{
		Load:     loader(path),
		Debounce: 10 * time.Millisecond,
		OnError:  func(err error) { failures <- err },
	})
	writeFile(t, path, "{not json")

	select {
	case err := <-failures:
		if err == nil {
			t.Fatal("want the load's error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no failure reported")
	}
	if got := w.Value().Name; got != "good" {
		t.Errorf("value = %q, want the last good one", got)
	}
}

// TestCloseIsIdempotentAndTakesTheGoroutine: the two ways out, each of them
// leaving nothing running.
func TestCloseIsIdempotentAndTakesTheGoroutine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)

	before := runtime.NumGoroutine()
	w, err := Start(t.Context(), Options[appConfig]{Load: loader(path)})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-w.Done():
	default:
		t.Fatal("Close returned before the goroutine finished")
	}
	// Closing twice is a no-op rather than a panic on a closed channel.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.Close() }()
	}
	wg.Wait()
	waitForGoroutines(t, before)
}

// TestCancellingTheContextStopsTheWatcher: the other way out, for the program
// that has a context and no reference to close.
func TestCancellingTheContextStopsTheWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(t.Context())
	w, err := Start(ctx, Options[appConfig]{Load: loader(path)})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	cancel()
	select {
	case <-w.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the goroutine outlived its context")
	}
	// And Close after the context already stopped it still returns.
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitForGoroutines(t, before)
}

// waitForGoroutines is the hand-rolled leak check: the count comes back to
// where it started, allowing for the runtime's own housekeeping to settle.
func waitForGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		got := runtime.NumGoroutine()
		if got <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%d goroutines, started from %d", got, before)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEventsOfAWatch: what a watcher tells a logger it was given.
func TestEventsOfAWatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)

	rec := &recorder{}
	seen := newUpdates()
	w := start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: 10 * time.Millisecond, OnUpdate: seen.on, Logger: rec,
	})
	if !rec.has(config.EventWatchStarted) {
		t.Errorf("events = %v", rec.events())
	}
	writeFile(t, path, `{"name": "second"}`)
	seen.next(t)
	if !rec.has(config.EventWatchReloaded) {
		t.Errorf("events = %v", rec.events())
	}
	w.Close()
	if !rec.has(config.EventWatchStopped) {
		t.Errorf("events = %v", rec.events())
	}
}

// recorder is the caller's logger, as a fake.
type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) Enabled(config.Level) bool { return true }

func (r *recorder) Log(_ config.Level, event string, _ ...config.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, event)
}

func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func (r *recorder) has(event string) bool {
	for _, e := range r.events() {
		if e == event {
			return true
		}
	}
	return false
}

// TestNegativeDebounceReloadsAtOnce: the option a test wants, where waiting
// for a window to close is the thing being avoided rather than the point.
func TestNegativeDebounceReloadsAtOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	writeFile(t, path, `{"name": "first"}`)
	seen := newUpdates()

	start(t, t.Context(), Options[appConfig]{
		Load: loader(path), Debounce: -1, OnUpdate: seen.on,
	})
	writeFile(t, path, `{"name": "second"}`)

	// Every event reloads, so the value arrives and goes on arriving.
	for i := 0; i < 20; i++ {
		if seen.next(t).Name == "second" {
			return
		}
	}
	t.Error("no reload carried the new value")
}

// TestFirstNamesNothingWhenThereIsNothing: a load that reports no files at all
// leaves the events' path empty rather than panicking on an index.
func TestFirstNamesNothingWhenThereIsNothing(t *testing.T) {
	if got := first(nil); got != "" {
		t.Errorf("first(nil) = %q", got)
	}
	w := start(t, t.Context(), Options[appConfig]{
		Load: func(context.Context) (appConfig, []string, error) { return appConfig{Name: "n"}, nil, nil },
	})
	if got := w.Value().Name; got != "n" {
		t.Errorf("value = %q", got)
	}
	if files := w.Files(); len(files) != 0 {
		t.Errorf("files = %#v", files)
	}
}
