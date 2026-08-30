// Package watch reloads a configuration when the files it was read from
// change.
//
// It is a subpackage rather than part of config because it is the only thing
// here that needs fsnotify, and a program that never reloads should not link a
// filesystem-notification library to read a file once at startup. A caller
// that wants reloading imports this; everybody else pays nothing, including
// the builds where fsnotify is not available at all.
//
// What it watches is directories, not files. A config file is usually replaced
// rather than written in place — a temp file and a rename, which is how any
// careful writer, this repository's own editor included, avoids leaving a
// half-written config behind — and a watch on the file itself follows the old
// inode into the void. The directory sees the rename.
//
// The watch set is derived from the files each load reports, so a
// configuration composed through `$ref` watches every fragment, and a reload
// that changes which fragments are involved changes what is watched.
package watch

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/yohimik/dispat/pkg/config"
)

// DefaultDebounce is how long a change waits for the changes after it. One
// save of one file arrives as several events, and a multi-file edit arrives as
// several more; reloading once at the end of the flurry is both cheaper and
// more correct than reloading against a half-written set.
const DefaultDebounce = 100 * time.Millisecond

// Options configures a Watcher. Only Load is required.
type Options[T any] struct {
	// Load reads the configuration and reports the files it was read from, in
	// any order. It is called once by Start, and again after every change; the
	// files it reports become the watch set, so a load that follows references
	// must report them all.
	//
	// It is called from the watcher's own goroutine, one call at a time, so it
	// needs no locking of its own.
	Load func(ctx context.Context) (T, []string, error)

	// Debounce is how long a change waits for the ones after it. Zero value:
	// DefaultDebounce. A negative value reloads on the first event, which is
	// for tests rather than for programs.
	Debounce time.Duration

	// OnUpdate is called with each new value, from the watcher's goroutine,
	// after Value already answers with it. A slow OnUpdate delays the next
	// reload and nothing else.
	OnUpdate func(value T)

	// OnError is called with a reload that failed, or with a failure of the
	// underlying watch. The previous value stays: a configuration that stops
	// parsing is a reason to keep running on the last one that did.
	OnError func(err error)

	// Logger receives the watch events. Zero value: the logger the context
	// passed to Start carries.
	Logger config.Logger
}

// Watcher holds the current value and the goroutine that keeps it current.
type Watcher[T any] struct {
	opts     Options[T]
	log      config.Logger
	debounce time.Duration

	fs   *fsnotify.Watcher
	done chan struct{}
	stop chan struct{}
	once sync.Once

	mu    sync.RWMutex
	value T
	files []string
	dirs  map[string]bool
	watch map[string]bool
}

// Start loads the configuration once and then keeps it current until the
// context is cancelled or Close is called.
//
// The first load is synchronous, and its failure is Start's: a program that
// cannot read its configuration at startup should say so and exit, not run on
// a zero value while it waits for someone to fix the file. Every later failure
// goes to OnError and leaves the last good value in place.
func Start[T any](ctx context.Context, opts Options[T]) (*Watcher[T], error) {
	log := opts.Logger
	if log == nil {
		log = config.GetLogger(ctx)
	}
	debounce := opts.Debounce
	if debounce == 0 {
		debounce = DefaultDebounce
	}

	value, files, err := opts.Load(ctx)
	if err != nil {
		return nil, err
	}
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher[T]{
		opts:     opts,
		log:      log,
		debounce: debounce,
		fs:       fs,
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
		value:    value,
		watch:    map[string]bool{},
	}
	w.retarget(files)
	if log.Enabled(config.LevelInfo) {
		log.Log(config.LevelInfo, config.EventWatchStarted,
			config.Str("path", first(files)), config.Num("files", len(files)))
	}
	go w.run(ctx)
	return w, nil
}

// Value is the configuration as of the last successful load. It is safe to
// call from any goroutine, and it never blocks on a reload in progress.
func (w *Watcher[T]) Value() T {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.value
}

// Files are the files the current value was read from, in the order they were
// read. The slice is a copy: the watcher goes on using its own.
func (w *Watcher[T]) Files() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]string(nil), w.files...)
}

// Close stops watching and waits for the goroutine to finish. It is idempotent
// and safe to call after the context has already stopped the watcher.
func (w *Watcher[T]) Close() error {
	w.once.Do(func() { close(w.stop) })
	<-w.done
	return nil
}

// Done is closed when the watcher's goroutine has finished, whether because
// the context was cancelled, because Close was called, or because the
// underlying watch ended. A test waits on it; a program usually calls Close.
func (w *Watcher[T]) Done() <-chan struct{} { return w.done }

// run is the whole watcher: one goroutine, one timer, and the two ways out.
func (w *Watcher[T]) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer func() {
		timer.Stop()
		w.fs.Close()
		if w.log.Enabled(config.LevelDebug) {
			w.log.Log(config.LevelDebug, config.EventWatchStopped, config.Str("path", first(w.Files())))
		}
		close(w.done)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case event, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if !w.interested(event.Name) {
				continue
			}
			if w.debounce < 0 {
				w.reload(ctx)
				continue
			}
			timer.Reset(w.debounce)
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			if w.log.Enabled(config.LevelError) {
				w.log.Log(config.LevelError, config.EventWatchFailed, config.Err(err))
			}
			if w.opts.OnError != nil {
				w.opts.OnError(err)
			}
		case <-timer.C:
			w.reload(ctx)
		}
	}
}

// reload reads the configuration again. A failure keeps the last good value:
// a configuration that stops parsing is a reason to go on running with the one
// that did, and the caller hears about it through OnError.
func (w *Watcher[T]) reload(ctx context.Context) {
	value, files, err := w.opts.Load(ctx)
	if err != nil {
		if w.log.Enabled(config.LevelWarn) {
			w.log.Log(config.LevelWarn, config.EventWatchReloadFailed,
				config.Str("path", first(w.Files())), config.Err(err))
		}
		if w.opts.OnError != nil {
			w.opts.OnError(err)
		}
		return
	}
	w.mu.Lock()
	w.value = value
	w.mu.Unlock()
	w.retarget(files)
	if w.log.Enabled(config.LevelInfo) {
		w.log.Log(config.LevelInfo, config.EventWatchReloaded,
			config.Str("path", first(files)), config.Num("files", len(files)))
	}
	if w.opts.OnUpdate != nil {
		w.opts.OnUpdate(value)
	}
}

// retarget records the files a load reported and moves the directory watches
// onto the folders holding them. Directories rather than files, because a
// config replaced by a rename leaves a watch on the file itself pointing at an
// inode nobody will write again.
func (w *Watcher[T]) retarget(files []string) {
	dirs := make(map[string]bool, len(files))
	tracked := make(map[string]bool, len(files))
	for _, f := range files {
		abs := clean(f)
		tracked[abs] = true
		dirs[filepath.Dir(abs)] = true
	}

	w.mu.Lock()
	w.files = append([]string(nil), files...)
	w.dirs = tracked
	w.mu.Unlock()

	for dir := range dirs {
		if w.watch[dir] {
			continue
		}
		if err := w.fs.Add(dir); err != nil {
			if w.log.Enabled(config.LevelWarn) {
				w.log.Log(config.LevelWarn, config.EventWatchReloadFailed,
					config.Str("path", dir), config.Err(err))
			}
			continue
		}
		w.watch[dir] = true
	}
	for dir := range w.watch {
		if !dirs[dir] {
			w.fs.Remove(dir)
			delete(w.watch, dir)
		}
	}
}

// interested reports whether a changed path is one of the files the current
// value was read from. A directory watch reports everything in the folder, and
// most of it is not configuration.
func (w *Watcher[T]) interested(path string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.dirs[clean(path)]
}

// clean is the name two paths to one file agree on.
func clean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// first names the configuration as a whole by the file it was entered
// through, which is the first one every load reports.
func first(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return files[0]
}
