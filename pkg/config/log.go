package config

// Logging, on the caller's terms.
//
// A configuration loader is a thing that goes quiet and then, one day, loads
// the wrong file. The events below are what someone reads at that point: which
// directories the ascent tried and what it made of each, which files a `$ref`
// pulled in, how many overrides landed, which key an edit was written to. They
// are the answer to "why did it do that", and they cost nothing when nobody is
// listening.
//
// The logger is an interface with two methods and no dependency, because a
// library that picks a logging package picks it for every program that imports
// it. A caller wires its own in three lines, and the ones that do not get the
// no-op.
//
// Every emit is guarded by Enabled, and the Field constructors keep their
// values in typed slots rather than boxing them into an `any`, so a Trace
// event a program never asked for costs one interface call and a comparison.

import (
	"context"
	"strconv"
)

// Level is how much an event matters. The zero value is Trace, so a Logger
// that answers Enabled for everything sees everything.
type Level int8

// The levels, quietest first.
const (
	// LevelTrace is the file-by-file detail: every read, every reference
	// followed, every directory the ascent looked in.
	LevelTrace Level = iota
	// LevelDebug is one line per operation: a tree loaded, an ascent settled,
	// a decode finished.
	LevelDebug
	// LevelInfo is what changed on disk.
	LevelInfo
	// LevelWarn is something that was not what the caller meant but did not
	// stop the load.
	LevelWarn
	// LevelError is a failure nobody is going to be told about any other way.
	// Only the watcher uses it: everywhere else an error is returned, and
	// reporting it here as well would say it twice.
	LevelError
)

// String names the level as the events themselves do.
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return "level(" + strconv.Itoa(int(l)) + ")"
}

// The events this package emits. They are constants so that a caller filtering
// or routing on one is held to the same spelling the emit uses.
const (
	// EventFileRead (trace): path, bytes, format.
	EventFileRead = "config.file.read"
	// EventRefFollow (trace): file, key, target, depth.
	EventRefFollow = "config.ref.follow"
	// EventRefMerge (trace): file, key, keys — the keys written beside a
	// reference that overrode what it brought in.
	EventRefMerge = "config.ref.merge"
	// EventResolveStep (trace): dir, candidate, class.
	EventResolveStep = "config.resolve.step"
	// EventEnvBind (trace): key, var.
	EventEnvBind = "config.env.bind"

	// EventTreeLoaded (debug): path, files, duration.
	EventTreeLoaded = "config.tree.loaded"
	// EventResolveDone (debug): path, root.
	EventResolveDone = "config.resolve.done"
	// EventOverridesApplied (debug): count.
	EventOverridesApplied = "config.overrides.applied"
	// EventDecodeDone (debug): at, keys.
	EventDecodeDone = "config.decode.done"
	// EventDecodeFailed (debug): at, error.
	EventDecodeFailed = "config.decode.failed"
	// EventEditPrepared (debug): path, edits, changed.
	EventEditPrepared = "config.edit.prepared"
	// EventWatchStopped (debug): path.
	EventWatchStopped = "config.watch.stopped"

	// EventEditCommitted (info): path, backup.
	EventEditCommitted = "config.edit.committed"
	// EventWatchStarted (info): path, files.
	EventWatchStarted = "config.watch.started"
	// EventWatchReloaded (info): path, files.
	EventWatchReloaded = "config.watch.reloaded"

	// EventEnvUnmatched (warn): var — a variable the binding's prefix claimed
	// that no declared key answers to.
	EventEnvUnmatched = "config.env.unmatched"
	// EventWatchReloadFailed (warn): path, error.
	EventWatchReloadFailed = "config.watch.reload_failed"

	// EventWatchFailed (error): error.
	EventWatchFailed = "config.watch.failed"
)

// FieldKind says which slot of a Field holds its value.
type FieldKind uint8

// The kinds a Field can carry.
const (
	KindString FieldKind = iota
	KindInt
	KindBool
	KindError
	KindAny
)

// Field is one key and value of an event. It is a value rather than an
// interface pair, and its scalars live in typed slots, so building one
// allocates nothing — which is what makes the Enabled guard worth having
// rather than a formality.
type Field struct {
	// Key names the value within the event.
	Key string

	kind FieldKind
	text string
	num  int64
	flag bool
	err  error
	val  any
}

// Str is a text field.
func Str(key, val string) Field { return Field{Key: key, kind: KindString, text: val} }

// Num is a whole-number field. It is not called Int because Int is the setter
// that fills a whole-number config field, and a package with one name for two
// things is a package whose examples cannot be copied.
func Num(key string, val int) Field { return Field{Key: key, kind: KindInt, num: int64(val)} }

// Flag is a boolean field, named for the same reason Num is.
func Flag(key string, val bool) Field { return Field{Key: key, kind: KindBool, flag: val} }

// Err is the failure a warn or error event is about. Its key is always
// "error", because a caller routing on it should not have to guess.
func Err(err error) Field { return Field{Key: "error", kind: KindError, err: err} }

// Any is anything else. It boxes, so it is for the values that have no slot
// rather than for convenience.
func Any(key string, val any) Field { return Field{Key: key, kind: KindAny, val: val} }

// Kind says which of the readers below answers for this field.
func (f Field) Kind() FieldKind { return f.kind }

// Text is the value of a KindString field.
func (f Field) Text() string { return f.text }

// Number is the value of a KindInt field.
func (f Field) Number() int64 { return f.num }

// Flag is the value of a KindBool field.
func (f Field) Flag() bool { return f.flag }

// Cause is the value of a KindError field.
func (f Field) Cause() error { return f.err }

// Value is the field whatever its kind, for an adapter with one sink to hand
// it to. It boxes the scalar kinds, which is why the typed readers exist.
func (f Field) Value() any {
	switch f.kind {
	case KindString:
		return f.text
	case KindInt:
		return f.num
	case KindBool:
		return f.flag
	case KindError:
		return f.err
	default:
		return f.val
	}
}

// Logger is what this package writes its events to. Two methods, no
// dependency: a caller wires its own logging package in around them.
//
// Enabled is asked before every event is built, so an implementation that
// answers it cheaply is what makes the trace events free in a program that
// does not want them.
type Logger interface {
	Enabled(level Level) bool
	Log(level Level, event string, fields ...Field)
}

// Nop returns the logger that is never enabled and records nothing. It is what
// a call with no logger anywhere uses, so nothing in this package has to check
// for nil.
func Nop() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Enabled(Level) bool          { return false }
func (nopLogger) Log(Level, string, ...Field) {}

// loggerKey is this package's own context key type, so nothing else can
// collide with it.
type loggerKey struct{}

// WithLogger returns a context carrying the logger, which every call taking a
// context will write its events to. It is how a request-scoped or
// command-scoped logger reaches a Loader that was built once at startup.
func WithLogger(ctx context.Context, log Logger) context.Context {
	if log == nil {
		log = Nop()
	}
	return context.WithValue(ctx, loggerKey{}, log)
}

// GetLogger returns the logger a context carries, or the no-op.
func GetLogger(ctx context.Context) Logger {
	if ctx != nil {
		if log, ok := ctx.Value(loggerKey{}).(Logger); ok && log != nil {
			return log
		}
	}
	return Nop()
}

// logger picks the logger for one call: the loader's own if it was given one,
// the context's otherwise. A Loader built with a logger is a Loader that logs
// wherever it is used, which is what a program with one logger wants; the
// context is for the programs that have several.
func (l *Loader) logger(ctx context.Context) Logger {
	if l != nil && l.opts.Logger != nil {
		return l.opts.Logger
	}
	return GetLogger(ctx)
}
