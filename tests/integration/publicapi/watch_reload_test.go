package publicapi_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yohimik/dispat/pkg/config"
	configwatch "github.com/yohimik/dispat/pkg/config/watch"
)

// watchProbe is the Load a watcher is driven through: it counts its calls, can
// be made to fail, and reports whichever files the test currently wants watched.
type watchProbe struct {
	mu     sync.Mutex
	loads  int
	files  []string
	fail   error
	loader *config.Loader
}

func (p *watchProbe) load(ctx context.Context) (map[string]any, []string, error) {
	p.mu.Lock()
	p.loads++
	files := append([]string(nil), p.files...)
	fail := p.fail
	p.mu.Unlock()
	if fail != nil {
		return nil, nil, fail
	}
	if len(files) == 0 {
		return map[string]any{}, nil, nil
	}
	tree, err := p.loader.ReadTree(ctx, files[0])
	if err != nil {
		return nil, nil, err
	}
	return tree.Root, files, nil
}

func (p *watchProbe) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loads
}

func (p *watchProbe) breakLoad(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail = err
}

func (p *watchProbe) watchFiles(files ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files = files
}

// waitFor polls until cond holds, which is what a filesystem notification needs
// instead of a sleep long enough to be a guess.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestPublicAPIConfigWatchLifecycle drives the optional config/watch subpackage:
// the first load a program exits on, the reloads a change produces, the failure
// that keeps the last good value, and the two ways a watcher stops.
func TestPublicAPIConfigWatchLifecycle(t *testing.T) {
	loader := config.NewLoader(config.Options{})

	t.Run("the first load is the caller's to fail on", func(t *testing.T) {
		broken := errors.New("cannot read the configuration")
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load: func(context.Context) (map[string]any, []string, error) { return nil, nil, broken },
		})
		if !errors.Is(err, broken) {
			t.Fatalf("Start = %v, want the load's failure", err)
		}
		if w != nil {
			t.Fatal("Start returned a watcher beside its failure")
		}
	})

	t.Run("an atomic edit is observed as a new value", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{\n  \"logLevel\": \"info\"\n}\n")
		probe := &watchProbe{files: []string{path}, loader: loader}
		log := &recordingLogger{floor: config.LevelTrace}

		updates := make(chan map[string]any, 8)
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: -1,
			Logger:   log,
			OnUpdate: func(value map[string]any) { updates <- value },
			OnError:  func(err error) { t.Errorf("unexpected reload failure: %v", err) },
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()

		if got := w.Value()["logLevel"]; got != "info" {
			t.Errorf("Value() = %#v", w.Value())
		}
		if files := w.Files(); len(files) != 1 || files[0] != path {
			t.Errorf("Files() = %v", files)
		}
		w.Files()[0] = "mutated"
		if w.Files()[0] != path {
			t.Error("Files() handed back the watcher's own slice")
		}
		if !log.saw(config.EventWatchStarted) {
			t.Error("no start event was written")
		}

		if err := config.ApplyEdits(context.Background(), path, []config.Edit{
			{KeyPath: []string{"logLevel"}, Value: "debug"},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}

		waitFor(t, "the reloaded value", func() bool {
			select {
			case value := <-updates:
				return value["logLevel"] == "debug"
			default:
				return false
			}
		})
		if got := w.Value()["logLevel"]; got != "debug" {
			t.Errorf("Value() after the edit = %#v", w.Value())
		}
		if !log.saw(config.EventWatchReloaded) {
			t.Error("no reload event was written")
		}
	})

	t.Run("a change to a file the configuration was not read from is ignored", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":1}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		// Every reload reports the value it read, so a reload the unrelated
		// file caused, however late it arrives, reads {"a":1} and is told
		// apart from the one app.json's change earns.
		var mu sync.Mutex
		var reloads []any
		// One save arrives as several events, and the debounce is what makes
		// it one reload; a short one keeps the scenario quick.
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: 50 * time.Millisecond,
			OnUpdate: func(value map[string]any) {
				mu.Lock()
				defer mu.Unlock()
				reloads = append(reloads, value["a"])
			},
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()
		seen := func() []any {
			mu.Lock()
			defer mu.Unlock()
			return append([]any(nil), reloads...)
		}

		writeConfigFile(t, dir, "unrelated.txt", "nothing to do with it")
		time.Sleep(200 * time.Millisecond)
		if got := seen(); len(got) != 0 {
			t.Errorf("an unrelated file triggered reloads: %v", got)
		}

		writeConfigFile(t, dir, "app.json", `{"a":2}`)
		waitFor(t, "the reload the config's own change earns", func() bool {
			return len(seen()) > 0
		})
		// A late spurious reload would arrive in this window as well.
		time.Sleep(200 * time.Millisecond)
		if got := seen(); len(got) != 1 || got[0] != float64(2) {
			t.Errorf("reloads = %v, want exactly one, reading app.json's new value", got)
		}
	})

	t.Run("a reload that fails keeps the last good value", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"logLevel":"info"}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		log := &recordingLogger{floor: config.LevelTrace}
		failures := make(chan error, 8)

		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: -1,
			Logger:   log,
			OnError:  func(err error) { failures <- err },
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()

		broken := errors.New("the file stopped parsing")
		probe.breakLoad(broken)
		writeConfigFile(t, dir, "app.json", `{"logLevel":`)

		waitFor(t, "the reload failure", func() bool {
			select {
			case err := <-failures:
				return errors.Is(err, broken)
			default:
				return false
			}
		})
		if got := w.Value()["logLevel"]; got != "info" {
			t.Errorf("Value() = %#v, want the last good value", w.Value())
		}
		if !log.saw(config.EventWatchReloadFailed) {
			t.Error("no reload-failure event was written")
		}
	})

	t.Run("the watch set follows the files each load reports", func(t *testing.T) {
		first := t.TempDir()
		second := t.TempDir()
		firstPath := writeConfigFile(t, first, "app.json", `{"a":1}`)
		secondPath := writeConfigFile(t, second, "app.json", `{"b":2}`)
		probe := &watchProbe{files: []string{firstPath}, loader: loader}
		reloads := make(chan []string, 8)

		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: -1,
			OnUpdate: func(map[string]any) { reloads <- nil },
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()

		probe.watchFiles(secondPath)
		writeConfigFile(t, first, "app.json", `{"a":2}`)
		waitFor(t, "the reload that moves the watch", func() bool {
			select {
			case <-reloads:
				return true
			default:
				return false
			}
		})
		waitFor(t, "the new watch set", func() bool {
			files := w.Files()
			return len(files) == 1 && files[0] == secondPath
		})

		writeConfigFile(t, second, "app.json", `{"b":3}`)
		waitFor(t, "a reload from the new folder", func() bool {
			select {
			case <-reloads:
				return true
			default:
				return false
			}
		})
		if got := w.Value()["b"]; got == nil {
			t.Errorf("Value() = %#v, want the second file's document", w.Value())
		}
	})

	t.Run("a folder that cannot be watched is reported and the load goes on", func(t *testing.T) {
		dir := t.TempDir()
		absent := filepath.Join(dir, "gone", "app.json")
		log := &recordingLogger{floor: config.LevelTrace}
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load: func(context.Context) (map[string]any, []string, error) {
				return map[string]any{"a": 1}, []string{absent}, nil
			},
			Debounce: -1,
			Logger:   log,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()
		if w.Value()["a"] != 1 {
			t.Errorf("Value() = %#v", w.Value())
		}
		if !log.saw(config.EventWatchReloadFailed) {
			t.Error("a folder that cannot be watched was not reported")
		}
	})

	t.Run("a load reporting no files still holds its value", func(t *testing.T) {
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load: func(context.Context) (map[string]any, []string, error) {
				return map[string]any{"a": 1}, nil, nil
			},
			Debounce: -1,
			Logger:   &recordingLogger{floor: config.LevelTrace},
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()
		if len(w.Files()) != 0 {
			t.Errorf("Files() = %v, want none", w.Files())
		}
	})

	t.Run("a cancelled context stops the watcher", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":1}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		ctx, cancel := context.WithCancel(context.Background())
		w, err := configwatch.Start(ctx, configwatch.Options[map[string]any]{Load: probe.load})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		cancel()
		select {
		case <-w.Done():
		case <-time.After(10 * time.Second):
			t.Fatal("the watcher did not stop when its context was cancelled")
		}
		if err := w.Close(); err != nil {
			t.Errorf("Close after a cancelled context = %v", err)
		}
	})

	t.Run("Close stops the watcher and is idempotent", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":1}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		log := &recordingLogger{floor: config.LevelTrace}
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:   probe.load,
			Logger: log,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("a second Close: %v", err)
		}
		select {
		case <-w.Done():
		default:
			t.Error("Done is not closed after Close returned")
		}
		if !log.saw(config.EventWatchStopped) {
			t.Error("no stop event was written")
		}
	})

	t.Run("the debounce waits for the changes after a change", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":1}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		reloads := make(chan struct{}, 16)
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: configwatch.DefaultDebounce,
			OnUpdate: func(map[string]any) { reloads <- struct{}{} },
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()
		for i := 0; i < 3; i++ {
			writeConfigFile(t, dir, "app.json", `{"a":2}`)
		}
		waitFor(t, "the debounced reload", func() bool {
			select {
			case <-reloads:
				return true
			default:
				return false
			}
		})
		if got := probe.calls(); got > 4 {
			t.Errorf("a flurry of writes produced %d loads", got)
		}
	})

	t.Run("a watcher with no logger anywhere still runs", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":1}`)
		probe := &watchProbe{files: []string{path}, loader: loader}
		w, err := configwatch.Start(context.Background(), configwatch.Options[map[string]any]{
			Load:     probe.load,
			Debounce: -1,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer w.Close()
		writeConfigFile(t, dir, "app.json", `{"a":2}`)
		waitFor(t, "a reload with no logger and no callbacks", func() bool {
			return probe.calls() > 1
		})
	})
}
