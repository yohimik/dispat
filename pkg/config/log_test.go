package config

// The events, against a recording logger: which of them fire, what they carry,
// and that nothing is built for a level nobody asked for.

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recorder is the fake a caller's own adapter stands in the place of.
type recorder struct {
	mu    sync.Mutex
	from  Level
	lines []line
}

type line struct {
	level  Level
	event  string
	fields map[string]Field
}

func newRecorder(from Level) *recorder { return &recorder{from: from} }

func (r *recorder) Enabled(l Level) bool { return l >= r.from }

func (r *recorder) Log(l Level, event string, fields ...Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Key] = f
	}
	r.lines = append(r.lines, line{level: l, event: event, fields: m})
}

func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.lines))
	for _, l := range r.lines {
		out = append(out, l.event)
	}
	return out
}

func (r *recorder) first(event string) (line, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lines {
		if l.event == event {
			return l, true
		}
	}
	return line{}, false
}

func (r *recorder) has(event string) bool {
	_, ok := r.first(event)
	return ok
}

// TestEventsOfALoad: a load at trace names every file it read, every reference
// it followed, and the tree it ended with.
func TestEventsOfALoad(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json",
		`{"flow": {"$ref": "./flow.json", "publish": ["p"]}}`)

	rec := newRecorder(LevelTrace)
	l := NewLoader(Options{Logger: rec})
	if _, err := l.ReadTree(t.Context(), path); err != nil {
		t.Fatalf("read: %v", err)
	}

	for _, event := range []string{EventFileRead, EventRefFollow, EventRefMerge, EventTreeLoaded} {
		if !rec.has(event) {
			t.Errorf("no %s in %v", event, rec.events())
		}
	}
	read, _ := rec.first(EventFileRead)
	if got := read.fields["path"].Text(); got != path {
		t.Errorf("path = %q", got)
	}
	if got := read.fields["format"].Text(); got != ".json" {
		t.Errorf("format = %q", got)
	}
	if got := read.fields["bytes"].Number(); got <= 0 {
		t.Errorf("bytes = %d", got)
	}
	loaded, _ := rec.first(EventTreeLoaded)
	if got := loaded.fields["files"].Number(); got != 2 {
		t.Errorf("files = %d, want 2", got)
	}
	if loaded.level != LevelDebug {
		t.Errorf("tree.loaded is %v", loaded.level)
	}
	follow, _ := rec.first(EventRefFollow)
	if got := follow.fields["target"].Text(); got != filepath.Join(dir, "flow.json") {
		t.Errorf("target = %q", got)
	}
	if got := follow.fields["depth"].Number(); got != 1 {
		t.Errorf("depth = %d", got)
	}
}

// TestQuietLevelsBuildNothing: a logger that only wants Info hears nothing of
// the trace events, which is the whole point of asking Enabled first.
func TestQuietLevelsBuildNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$ref": "./flow.json"}}`)

	rec := newRecorder(LevelInfo)
	if _, err := NewLoader(Options{Logger: rec}).ReadTree(t.Context(), path); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := rec.events(); len(got) != 0 {
		t.Errorf("events = %v, want none below info", got)
	}
}

// TestEventsOfAnAscent: each directory tried, what the file found there turned
// out to be, and the one the ascent settled on.
func TestEventsOfAnAscent(t *testing.T) {
	dir := t.TempDir()
	root := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	sub := filepath.Join(dir, "pkgs")
	writeFile(t, sub, "app.json", `{"hooks": [{"url": "u"}]}`)

	rec := newRecorder(LevelTrace)
	l := NewLoader(Options{Logger: rec})
	if _, _, err := l.Resolve(t.Context(), sub, appResolver()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var classes []string
	rec.mu.Lock()
	for _, ln := range rec.lines {
		if ln.event == EventResolveStep {
			classes = append(classes, ln.fields["class"].Text())
		}
	}
	rec.mu.Unlock()
	if want := []string{"candidate", "root"}; strings.Join(classes, ",") != strings.Join(want, ",") {
		t.Errorf("classes = %v, want %v", classes, want)
	}
	done, ok := rec.first(EventResolveDone)
	if !ok || done.fields["path"].Text() != root || done.fields["root"].Text() != dir {
		t.Errorf("resolve.done = %#v", done.fields)
	}
}

// TestEventsOfADecodeAndAnEdit: the outcome of a decode, and the two halves of
// a write.
func TestEventsOfADecodeAndAnEdit(t *testing.T) {
	rec := newRecorder(LevelTrace)
	l := NewLoader(Options{Logger: rec})

	var cfg appConfig
	if err := l.Decode(t.Context(), map[string]any{"name": "n"}, "", appFields(&cfg)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rec.has(EventDecodeDone) {
		t.Errorf("events = %v", rec.events())
	}
	err := l.Decode(t.Context(), map[string]any{"nmae": "n"}, "areas.libs", appFields(&cfg))
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v", err)
	}
	failed, _ := rec.first(EventDecodeFailed)
	if failed.fields["at"].Text() != "areas.libs" {
		t.Errorf("at = %q", failed.fields["at"].Text())
	}
	if !errors.Is(failed.fields["error"].Cause(), ErrUnknownKey) {
		t.Errorf("error field = %v", failed.fields["error"].Cause())
	}

	// The edit helpers take their logger from the context, being package
	// functions rather than a loader's.
	path := writeFile(t, t.TempDir(), "app.json", `{"tags": ["old"]}`)
	ctx := WithLogger(t.Context(), rec)
	if err := ApplyEdits(ctx, path, []Edit{{KeyPath: []string{"tags"}, Value: []string{"new"}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	prepared, ok := rec.first(EventEditPrepared)
	if !ok || !prepared.fields["changed"].Flag() || prepared.fields["edits"].Number() != 1 {
		t.Errorf("edit.prepared = %#v", prepared.fields)
	}
	committed, ok := rec.first(EventEditCommitted)
	if !ok || committed.level != LevelInfo ||
		committed.fields["backup"].Text() != path+BackupSuffix {
		t.Errorf("edit.committed = %#v", committed.fields)
	}
}

// TestOverridesAppliedIsTheLoadersOwnEvent: Settings takes no context, so its
// event goes to the loader's own logger and to no other.
func TestOverridesAppliedIsTheLoadersOwnEvent(t *testing.T) {
	rec := newRecorder(LevelTrace)
	tree := &Tree{Root: map[string]any{"name": "app"}}

	tree.Settings(NewLoader(Options{Logger: rec}), Overrides{"logLevel": "warn"})
	if applied, ok := rec.first(EventOverridesApplied); !ok || applied.fields["count"].Number() != 1 {
		t.Errorf("overrides.applied = %#v", applied.fields)
	}

	quiet := newRecorder(LevelTrace)
	tree.Settings(NewLoader(Options{Logger: quiet}), nil)
	if quiet.has(EventOverridesApplied) {
		t.Error("no overrides is not an event")
	}
}

// TestTheContextCarriesTheLogger: a Loader built once at startup writes to the
// logger of whichever call it is serving, unless it was given one of its own.
func TestTheContextCarriesTheLogger(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json", `{"name": "app"}`)

	ctx := newRecorder(LevelTrace)
	if _, err := NewLoader(Options{}).ReadTree(WithLogger(t.Context(), ctx), path); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ctx.has(EventTreeLoaded) {
		t.Errorf("the context's logger heard nothing: %v", ctx.events())
	}

	own := newRecorder(LevelTrace)
	other := newRecorder(LevelTrace)
	if _, err := NewLoader(Options{Logger: own}).ReadTree(WithLogger(t.Context(), other), path); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !own.has(EventTreeLoaded) || other.has(EventTreeLoaded) {
		t.Error("the loader's own logger is the one that wins")
	}
}

// TestNopAndTheContextHelpers: the defaults, and what a context carrying
// nothing answers.
func TestNopAndTheContextHelpers(t *testing.T) {
	if Nop().Enabled(LevelError) {
		t.Error("the no-op is never enabled")
	}
	Nop().Log(LevelError, "anything", Str("k", "v"))

	if GetLogger(t.Context()) != Nop() {
		t.Error("a context carrying no logger answers the no-op")
	}
	//nolint:staticcheck // the nil context is exactly the case being pinned
	if GetLogger(nil) != Nop() {
		t.Error("no context at all answers the no-op")
	}
	if GetLogger(WithLogger(t.Context(), nil)) != Nop() {
		t.Error("a nil logger stored is the no-op read back")
	}
	rec := newRecorder(LevelTrace)
	if GetLogger(WithLogger(t.Context(), rec)) != Logger(rec) {
		t.Error("what went in is what comes out")
	}
}

// TestFieldsCarryTheirValues: every constructor, its kind, its typed reader
// and the boxed form an adapter with one sink reads.
func TestFieldsCarryTheirValues(t *testing.T) {
	cause := errors.New("boom")
	for _, tc := range []struct {
		field Field
		kind  FieldKind
		key   string
		value any
	}{
		{Str("k", "v"), KindString, "k", "v"},
		{Num("k", 7), KindInt, "k", int64(7)},
		{Flag("k", true), KindBool, "k", true},
		{Err(cause), KindError, "error", cause},
		{Any("k", []int{1}), KindAny, "k", []int{1}},
	} {
		if tc.field.Kind() != tc.kind || tc.field.Key != tc.key {
			t.Errorf("field = %#v", tc.field)
		}
		if got := tc.field.Value(); !equalAny(got, tc.value) {
			t.Errorf("Value() = %#v, want %#v", got, tc.value)
		}
	}
	if got := Str("k", "v").Text(); got != "v" {
		t.Errorf("Text = %q", got)
	}
	if got := Num("k", 7).Number(); got != 7 {
		t.Errorf("Number = %d", got)
	}
	if got := Flag("k", true).Flag(); !got {
		t.Error("Flag")
	}
	if got := Err(cause).Cause(); got != cause {
		t.Errorf("Cause = %v", got)
	}
}

func equalAny(a, b any) bool {
	if s, ok := a.([]int); ok {
		o, ok := b.([]int)
		if !ok || len(s) != len(o) {
			return false
		}
		for i := range s {
			if s[i] != o[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

// TestLevelNames: the levels print as the events spell them.
func TestLevelNames(t *testing.T) {
	for _, tc := range []struct {
		level Level
		want  string
	}{
		{LevelTrace, "trace"}, {LevelDebug, "debug"}, {LevelInfo, "info"},
		{LevelWarn, "warn"}, {LevelError, "error"}, {Level(9), "level(9)"},
	} {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("Level(%d) = %q, want %q", tc.level, got, tc.want)
		}
	}
	for _, tc := range []struct {
		class Class
		want  string
	}{
		{ClassRoot, "root"}, {ClassCandidate, "candidate"}, {ClassFallback, "fallback"},
	} {
		if got := tc.class.String(); got != tc.want {
			t.Errorf("Class(%d) = %q, want %q", tc.class, got, tc.want)
		}
	}
}
