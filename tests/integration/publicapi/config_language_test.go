package publicapi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/dispat/pkg/config"
)

// recordedEvent is one event a test logger was handed.
type recordedEvent struct {
	level  config.Level
	event  string
	fields []config.Field
}

// recordingLogger is the Logger a caller of pkg/config wires its own logging
// package in through, reduced to what a test needs: a floor, and a transcript.
type recordingLogger struct {
	mu     sync.Mutex
	floor  config.Level
	events []recordedEvent
}

func (l *recordingLogger) Enabled(level config.Level) bool { return level >= l.floor }

func (l *recordingLogger) Log(level config.Level, event string, fields ...config.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, recordedEvent{level: level, event: event, fields: fields})
}

func (l *recordingLogger) saw(event string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		if e.event == event {
			return true
		}
	}
	return false
}

func (l *recordingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// writeConfigFile writes one config file under dir and returns its path.
func writeConfigFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readConfigFile reads a file written by an edit.
func readConfigFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// demoConfig is the model the decode tests fill: one struct per shape the
// setters cover, so the fields table below reads as the config surface.
type demoConfig struct {
	Name        string
	Count       int
	Quiet       bool
	Verbose     *bool
	Paths       []string
	Concurrency []int
	Env         map[string]string
	Custom      map[string]any
	Sizes       map[string]int
	Nested      *demoNested
	Entries     map[string]demoNested
	List        []demoNested
	Squashed    string
}

type demoNested struct {
	Title string
}

func demoNestedFields(n *demoNested) config.Fields {
	return config.Fields{"title": config.String(&n.Title)}
}

func demoFields(c *demoConfig) config.Fields {
	f := config.Fields{
		"name":        config.String(&c.Name),
		"count":       config.Int(&c.Count),
		"quiet":       config.Bool(&c.Quiet),
		"verbose":     config.BoolPtr(&c.Verbose),
		"paths":       config.Strings(&c.Paths),
		"concurrency": config.Ints(&c.Concurrency),
		"env":         config.StringMap(&c.Env),
		"custom":      config.RawMap(&c.Custom),
		"sizes":       config.MapOf(&c.Sizes, config.WeakInt),
		"nested":      config.Object(&c.Nested, demoNestedFields),
		"entries":     config.ObjectMap(&c.Entries, demoNestedFields),
		"list":        config.ObjectList(&c.List, demoNestedFields),
	}
	return config.Merge(f, config.Fields{"squashed": config.String(&c.Squashed)})
}

// TestPublicAPIConfigFoldingAndKeyPaths drives the one place the configuration
// language decides what "the same name" means, and the paths its errors name.
func TestPublicAPIConfigFoldingAndKeyPaths(t *testing.T) {
	t.Run("a name folds to the spelling the tables are keyed by", func(t *testing.T) {
		for _, c := range []struct{ in, want string }{
			{"loglevel", "loglevel"},
			{"logLevel", "loglevel"},
			{"LOGLEVEL", "loglevel"},
			{"", ""},
			{"Grüße", "grüße"},
			{"straße", "straße"},
			{"Σ", "σ"},
			{"ς", "σ"},
			{"μ", "μ"},
			{"µ", "μ"},
			{"ι", "ι"},
			{"ͅ", "ι"},
			{"İ", "İ"},
			{"i", "i"},
		} {
			if got := config.Fold(c.in); got != c.want {
				t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("a map answers to either spelling of a name", func(t *testing.T) {
		m := map[string]int{"LogLevel": 1}
		if key, value, ok := config.LookupFold(m, "LogLevel"); !ok || key != "LogLevel" || value != 1 {
			t.Errorf("exact lookup = %q, %d, %v", key, value, ok)
		}
		if key, value, ok := config.LookupFold(m, "loglevel"); !ok || key != "LogLevel" || value != 1 {
			t.Errorf("folded lookup = %q, %d, %v", key, value, ok)
		}
		if key, _, ok := config.LookupFold(m, "absent"); ok || key != "" {
			t.Errorf("missing lookup = %q, %v", key, ok)
		}
		if key, ok := config.FoldKey(m, "LOGLEVEL"); !ok || key != "LogLevel" {
			t.Errorf("FoldKey = %q, %v", key, ok)
		}
		if _, ok := config.FoldKey(m, "absent"); ok {
			t.Error("FoldKey found a key that is not there")
		}
		greek := map[string]int{"Σ": 2}
		if key, value, ok := config.LookupFold(greek, "ς"); !ok || key != "Σ" || value != 2 {
			t.Errorf("Unicode folded lookup = %q, %d, %v", key, value, ok)
		}
		if key, ok := config.FoldKey(greek, "σ"); !ok || key != "Σ" {
			t.Errorf("Unicode FoldKey = %q, %v", key, ok)
		}
		// The dotted capital I is its own SimpleFold class. Lowercasing it
		// to ASCII i would make a lookup read the wrong user-authored key.
		dotted := map[string]int{"İ": 3}
		if key, _, ok := config.LookupFold(dotted, "i"); ok || key != "" {
			t.Errorf("distinct Unicode lookup = %q, %v", key, ok)
		}
	})

	t.Run("keys come back in one order however the map iterates", func(t *testing.T) {
		got := config.SortedKeys(map[string]any{"b": 1, "a": 2, "C": 3})
		if want := []string{"C", "a", "b"}; !slices.Equal(got, want) {
			t.Errorf("SortedKeys = %v, want %v", got, want)
		}
		if got := config.SortedKeys(map[string]any{}); len(got) != 0 {
			t.Errorf("SortedKeys of an empty map = %v", got)
		}
	})

	t.Run("a key names itself at the root and its parent below it", func(t *testing.T) {
		if got := config.KeyPath("", "name"); got != "name" {
			t.Errorf("KeyPath at the root = %q", got)
		}
		if got := config.KeyPath("spaces.apps", "path"); got != "spaces.apps.path" {
			t.Errorf("KeyPath = %q", got)
		}
		if got := config.IndexPath("paths", 2); got != "paths[2]" {
			t.Errorf("IndexPath = %q", got)
		}
	})
}

// TestPublicAPIConfigErrorVocabulary drives every error the package returns,
// both as a value a caller builds and as one a load produces, so the sentinels
// a caller matches on stay matchable.
func TestPublicAPIConfigErrorVocabulary(t *testing.T) {
	t.Run("a wrong-shape value names the key and what belongs there", func(t *testing.T) {
		err := config.Wants("spaces.apps.path", "a string")
		if got := err.Error(); got != "spaces.apps.path: wants a string" {
			t.Errorf("Wants = %q", got)
		}
	})

	t.Run("a read failure names the file", func(t *testing.T) {
		inner := errors.New("permission denied")
		err := &config.FileError{Path: "/etc/app.json", Err: inner}
		if !strings.Contains(err.Error(), "/etc/app.json") {
			t.Errorf("FileError = %q", err.Error())
		}
		if !errors.Is(err, inner) {
			t.Error("FileError does not unwrap to its cause")
		}
	})

	t.Run("a key failure places itself inside a file", func(t *testing.T) {
		err := &config.KeyError{File: "app.json", Key: "spaces", Err: config.ErrRefTarget}
		if !strings.Contains(err.Error(), "app.json: spaces:") {
			t.Errorf("KeyError = %q", err.Error())
		}
		if !errors.Is(err, config.ErrRefTarget) {
			t.Error("KeyError does not unwrap to its cause")
		}
	})

	t.Run("a chain failure names every file involved", func(t *testing.T) {
		cycle := &config.RefChainError{
			Err:   config.ErrRefCycle,
			Chain: []string{"a.json (spaces)", "b.json"},
		}
		if !strings.Contains(cycle.Error(), "a.json (spaces) -> b.json") {
			t.Errorf("cycle = %q", cycle.Error())
		}
		if !errors.Is(cycle, config.ErrRefCycle) {
			t.Error("a cycle does not unwrap to ErrRefCycle")
		}
		deep := &config.RefChainError{Err: config.ErrRefDepth, Chain: []string{"a.json"}, Depth: 4}
		if !strings.Contains(deep.Error(), "more than 4 files deep") {
			t.Errorf("depth = %q", deep.Error())
		}
		if !errors.Is(deep, config.ErrRefDepth) {
			t.Error("a depth failure does not unwrap to ErrRefDepth")
		}
	})

	t.Run("an unknown key names its full path", func(t *testing.T) {
		err := &config.UnknownKeyError{Key: "spaces.apps.pahs"}
		if !strings.Contains(err.Error(), `"spaces.apps.pahs"`) {
			t.Errorf("UnknownKeyError = %q", err.Error())
		}
		if !errors.Is(err, config.ErrUnknownKey) {
			t.Error("UnknownKeyError does not unwrap to ErrUnknownKey")
		}
	})

	t.Run("a fold collision names both spellings", func(t *testing.T) {
		err := &config.FoldCollisionError{At: "the document", First: "Build", Second: "build"}
		if !strings.Contains(err.Error(), `"Build"`) || !strings.Contains(err.Error(), `"build"`) {
			t.Errorf("FoldCollisionError = %q", err.Error())
		}
		if !errors.Is(err, config.ErrFoldCollision) {
			t.Error("FoldCollisionError does not unwrap to ErrFoldCollision")
		}
	})

	t.Run("a failed ascent lists the names it looked for", func(t *testing.T) {
		err := &config.NoConfigError{Dir: "/tmp/x", Names: []string{"app.json", "app.yaml"}}
		if !strings.Contains(err.Error(), "app.json, app.yaml") {
			t.Errorf("NoConfigError = %q", err.Error())
		}
		if !errors.Is(err, config.ErrNoConfig) {
			t.Error("NoConfigError does not unwrap to ErrNoConfig")
		}
	})
}

// TestPublicAPIConfigEventSurface drives the logging surface a caller wires its
// own logging package in through: the levels, the typed fields, and the two
// ways a logger reaches a call.
func TestPublicAPIConfigEventSurface(t *testing.T) {
	t.Run("levels name themselves", func(t *testing.T) {
		for _, c := range []struct {
			level config.Level
			want  string
		}{
			{config.LevelTrace, "trace"},
			{config.LevelDebug, "debug"},
			{config.LevelInfo, "info"},
			{config.LevelWarn, "warn"},
			{config.LevelError, "error"},
			{config.Level(9), "level(9)"},
		} {
			if got := c.level.String(); got != c.want {
				t.Errorf("Level(%d).String() = %q, want %q", c.level, got, c.want)
			}
		}
	})

	t.Run("a field carries its value in a typed slot", func(t *testing.T) {
		cause := errors.New("broken")
		for _, c := range []struct {
			field config.Field
			kind  config.FieldKind
			value any
		}{
			{config.Str("path", "app.json"), config.KindString, "app.json"},
			{config.Num("files", 3), config.KindInt, int64(3)},
			{config.Flag("changed", true), config.KindBool, true},
			{config.Err(cause), config.KindError, cause},
			{config.Any("tree", []string{"a"}), config.KindAny, []string{"a"}},
		} {
			if c.field.Kind() != c.kind {
				t.Errorf("field %q kind = %v, want %v", c.field.Key, c.field.Kind(), c.kind)
			}
			switch c.kind {
			case config.KindString:
				if c.field.Text() != c.value {
					t.Errorf("Text() = %q", c.field.Text())
				}
			case config.KindInt:
				if c.field.Number() != c.value {
					t.Errorf("Number() = %d", c.field.Number())
				}
			case config.KindBool:
				if c.field.Flag() != c.value {
					t.Errorf("Flag() = %v", c.field.Flag())
				}
			case config.KindError:
				if c.field.Cause() != c.value {
					t.Errorf("Cause() = %v", c.field.Cause())
				}
			}
		}
		if got := config.Str("k", "v").Value(); got != "v" {
			t.Errorf("Value() of a string field = %v", got)
		}
		if got := config.Num("k", 2).Value(); got != int64(2) {
			t.Errorf("Value() of a number field = %v", got)
		}
		if got := config.Flag("k", true).Value(); got != true {
			t.Errorf("Value() of a flag field = %v", got)
		}
		if got := config.Err(cause).Value(); got != cause {
			t.Errorf("Value() of an error field = %v", got)
		}
		if got, ok := config.Any("k", 42).Value().(int); !ok || got != 42 {
			t.Errorf("Value() of a boxed field = %v", got)
		}
		if config.Err(cause).Key != "error" {
			t.Error("Err does not always use the error key")
		}
	})

	t.Run("a call with no logger anywhere uses the no-op", func(t *testing.T) {
		nop := config.Nop()
		if nop.Enabled(config.LevelError) {
			t.Error("the no-op logger is enabled")
		}
		nop.Log(config.LevelError, "config.test", config.Str("k", "v"))
		var noContext context.Context
		if config.GetLogger(noContext) == nil {
			t.Error("GetLogger(nil) returned nil")
		}
		if config.GetLogger(context.Background()).Enabled(config.LevelTrace) {
			t.Error("a bare context carries an enabled logger")
		}
	})

	t.Run("a logger reaches a call on the context", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		ctx := config.WithLogger(context.Background(), log)
		if config.GetLogger(ctx) != log {
			t.Error("the context did not carry the logger back")
		}
		nilled := config.WithLogger(context.Background(), nil)
		if config.GetLogger(nilled).Enabled(config.LevelTrace) {
			t.Error("WithLogger(nil) carried an enabled logger")
		}
	})
}

// TestPublicAPIConfigWeakTyping drives the readers that turn a value written in
// one format's types into the value a Go field holds.
func TestPublicAPIConfigWeakTyping(t *testing.T) {
	t.Run("a scalar renders the way the whole decode renders it", func(t *testing.T) {
		for _, c := range []struct {
			in   any
			want string
		}{
			{"text", "text"},
			{true, "true"},
			{false, "false"},
			{int(7), "7"},
			{int64(8), "8"},
			{float64(9), "9"},
			{float64(1e21), "1000000000000000000000"},
			{nil, ""},
			{[]int{1, 2}, "[1 2]"},
		} {
			if got := config.WeakScalarString(c.in); got != c.want {
				t.Errorf("WeakScalarString(%#v) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("a string field takes any scalar and refuses a container", func(t *testing.T) {
		for _, in := range []any{nil, "x", true, int(1), int64(2), float64(3)} {
			if _, err := config.WeakString(in, "name"); err != nil {
				t.Errorf("WeakString(%#v) = %v", in, err)
			}
		}
		for _, in := range []any{[]any{"x"}, map[string]any{"a": 1}} {
			if _, err := config.WeakString(in, "name"); err == nil {
				t.Errorf("WeakString(%#v) accepted a container", in)
			}
		}
	})

	t.Run("a number field reads every spelling that reaches it", func(t *testing.T) {
		for _, c := range []struct {
			in   any
			want int
		}{
			{nil, 0}, {int(3), 3}, {int64(4), 4}, {float64(5), 5},
			{true, 1}, {false, 0}, {"", 0}, {"12", 12}, {"0x10", 16},
		} {
			got, err := config.WeakInt(c.in, "count")
			if err != nil || got != c.want {
				t.Errorf("WeakInt(%#v) = %d, %v, want %d", c.in, got, err, c.want)
			}
		}
		for _, in := range []any{float64(2.5), "two", []any{1}} {
			if _, err := config.WeakInt(in, "count"); err == nil {
				t.Errorf("WeakInt(%#v) accepted a value the field cannot hold", in)
			}
		}
	})

	t.Run("a flag reads every spelling a format offers", func(t *testing.T) {
		for _, c := range []struct {
			in   any
			want bool
		}{
			{true, true}, {false, false}, {int(1), true}, {int(0), false},
			{int64(2), true}, {float64(0), false}, {float64(1), true},
			{"", false}, {"true", true}, {"0", false},
		} {
			got, err := config.WeakBool(c.in, "quiet")
			if err != nil || got != c.want {
				t.Errorf("WeakBool(%#v) = %v, %v, want %v", c.in, got, err, c.want)
			}
		}
		for _, in := range []any{"maybe", []any{true}, nil} {
			if _, err := config.WeakBool(in, "quiet"); err == nil {
				t.Errorf("WeakBool(%#v) accepted a value that is not a flag", in)
			}
		}
	})

	t.Run("the two list shapes reach the decoder", func(t *testing.T) {
		if got, ok := config.WeakList([]any{1, "a"}); !ok || len(got) != 2 {
			t.Errorf("WeakList of a parsed list = %v, %v", got, ok)
		}
		got, ok := config.WeakList([]string{"a", "b"})
		if !ok || len(got) != 2 || got[0] != "a" {
			t.Errorf("WeakList of an override list = %v, %v", got, ok)
		}
		if _, ok := config.WeakList("a,b"); ok {
			t.Error("WeakList accepted a string")
		}
	})

	t.Run("the comma shorthand reads a typed-in list", func(t *testing.T) {
		if got := config.SplitList(""); len(got) != 0 {
			t.Errorf("SplitList(\"\") = %v, want an empty list", got)
		}
		if got := config.SplitList("lint,build"); !slices.Equal(got, []string{"lint", "build"}) {
			t.Errorf("SplitList = %v", got)
		}
	})
}

// TestPublicAPIConfigDecodeRules drives the object rules of the language: the
// shape an object has to be, the keys that fold together, the key no table
// holds, and the key that said nothing.
func TestPublicAPIConfigDecodeRules(t *testing.T) {
	t.Run("a whole configuration decodes through its fields table", func(t *testing.T) {
		var cfg demoConfig
		val := map[string]any{
			"name":        "app",
			"count":       int64(3),
			"quiet":       "true",
			"verbose":     false,
			"paths":       []any{"apps", "tools"},
			"concurrency": []any{4, 2},
			"env":         map[string]any{"CI": true, "PATH": "/bin"},
			"custom":      map[string]any{"ours": map[string]any{"Deep": 1}, "OURS": 2},
			"sizes":       map[string]any{"a": "2"},
			"nested":      map[string]any{"title": "n"},
			"entries":     map[string]any{"one": map[string]any{"title": "1"}, "two": nil},
			"list":        []any{map[string]any{"title": "a"}, nil},
			"squashed":    "yes",
		}
		if err := config.DecodeObject(val, "", demoFields(&cfg)); err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if cfg.Name != "app" || cfg.Count != 3 || !cfg.Quiet || cfg.Squashed != "yes" {
			t.Errorf("scalars = %#v", cfg)
		}
		if cfg.Verbose == nil || *cfg.Verbose {
			t.Errorf("verbose = %v, want a pointer to false", cfg.Verbose)
		}
		if !slices.Equal(cfg.Paths, []string{"apps", "tools"}) {
			t.Errorf("paths = %v", cfg.Paths)
		}
		if !slices.Equal(cfg.Concurrency, []int{4, 2}) {
			t.Errorf("concurrency = %v", cfg.Concurrency)
		}
		if cfg.Env["CI"] != "true" || cfg.Env["PATH"] != "/bin" {
			t.Errorf("env = %v", cfg.Env)
		}
		if len(cfg.Custom) != 2 {
			t.Errorf("custom = %v, want the fold-duplicate refusal to stop at its edge", cfg.Custom)
		}
		if cfg.Sizes["a"] != 2 {
			t.Errorf("sizes = %v", cfg.Sizes)
		}
		if cfg.Nested == nil || cfg.Nested.Title != "n" {
			t.Errorf("nested = %#v", cfg.Nested)
		}
		if len(cfg.Entries) != 2 || cfg.Entries["one"].Title != "1" || cfg.Entries["two"].Title != "" {
			t.Errorf("entries = %#v", cfg.Entries)
		}
		if len(cfg.List) != 2 || cfg.List[0].Title != "a" || cfg.List[1].Title != "" {
			t.Errorf("list = %#v", cfg.List)
		}
	})

	t.Run("a key holding nothing leaves its field alone", func(t *testing.T) {
		cfg := demoConfig{Name: "kept"}
		err := config.DecodeObject(map[string]any{"name": nil, "count": nil}, "", demoFields(&cfg))
		if err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if cfg.Name != "kept" || cfg.Count != 0 {
			t.Errorf("a key that said nothing wrote something: %#v", cfg)
		}
	})

	t.Run("a single object stands for the one-element list", func(t *testing.T) {
		var cfg demoConfig
		err := config.DecodeObject(map[string]any{"list": map[string]any{"title": "only"}},
			"", demoFields(&cfg))
		if err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if len(cfg.List) != 1 || cfg.List[0].Title != "only" {
			t.Errorf("list = %#v", cfg.List)
		}
	})

	t.Run("the comma shorthand reads a list somebody typed", func(t *testing.T) {
		var cfg demoConfig
		err := config.DecodeObject(map[string]any{"paths": "apps,tools"}, "", demoFields(&cfg))
		if err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if !slices.Equal(cfg.Paths, []string{"apps", "tools"}) {
			t.Errorf("paths = %v", cfg.Paths)
		}
	})

	t.Run("a scalar stands for the one-element list it belongs in", func(t *testing.T) {
		var cfg demoConfig
		err := config.DecodeObject(map[string]any{"paths": 42, "concurrency": 4},
			"", demoFields(&cfg))
		if err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if !slices.Equal(cfg.Paths, []string{"42"}) || !slices.Equal(cfg.Concurrency, []int{4}) {
			t.Errorf("paths = %v, concurrency = %v", cfg.Paths, cfg.Concurrency)
		}
	})

	t.Run("a comma-separated number list is the one somebody typed", func(t *testing.T) {
		var cfg demoConfig
		err := config.DecodeObject(map[string]any{"concurrency": "4,2"}, "", demoFields(&cfg))
		if err != nil {
			t.Fatalf("DecodeObject: %v", err)
		}
		if !slices.Equal(cfg.Concurrency, []int{4, 2}) {
			t.Errorf("concurrency = %v", cfg.Concurrency)
		}
	})

	t.Run("the object rules are refused one by one", func(t *testing.T) {
		cases := []struct {
			name string
			val  any
			at   string
			want string
		}{
			{"a value that is not an object", []any{1}, "spaces", "spaces: wants an object"},
			{"an unknown key", map[string]any{"pahs": "x"}, "", `unknown key "pahs"`},
			{"an unknown nested key", map[string]any{"nested": map[string]any{"ttl": "x"}}, "",
				`unknown key "nested.ttl"`},
			{"two keys that fold together", map[string]any{"Name": "a", "name": "b"}, "",
				"the document: keys"},
			{"a string field holding a list", map[string]any{"name": []any{"a"}}, "", "name: wants a string"},
			{"a number field holding prose", map[string]any{"count": "many"}, "", "count: wants a number"},
			{"a flag holding prose", map[string]any{"quiet": "maybe"}, "", "quiet: wants true or false"},
			{"a tri-state flag holding prose", map[string]any{"verbose": "maybe"}, "",
				"verbose: wants true or false"},
			{"a list element of the wrong shape", map[string]any{"paths": []any{[]any{"a"}}}, "",
				"paths[0]: wants a string"},
			{"a list written as an object", map[string]any{"paths": map[string]any{"a": 1}}, "",
				"paths: wants a string"},
			{"a number list element of the wrong shape", map[string]any{"concurrency": []any{"x"}}, "",
				"concurrency[0]: wants a number"},
			{"a number list written as prose", map[string]any{"concurrency": map[string]any{}}, "",
				"concurrency: wants a number"},
			{"a string map that is not an object", map[string]any{"env": "CI=true"}, "",
				"env: wants an object"},
			{"a string map value of the wrong shape", map[string]any{"env": map[string]any{"a": []any{}}}, "",
				"env.a: wants a string"},
			{"a free-form object that is not an object", map[string]any{"custom": 1}, "",
				"custom: wants an object"},
			{"a sub-object that is not an object", map[string]any{"nested": 1}, "",
				"nested: wants an object"},
			{"a map of objects that is not an object", map[string]any{"entries": 1}, "",
				"entries: wants an object"},
			{"an entry of the wrong shape", map[string]any{"entries": map[string]any{"a": 1}}, "",
				"entries.a: wants an object"},
			{"a list of objects that is neither", map[string]any{"list": "a"}, "",
				"list: wants an object or a list of objects"},
			{"a list element that is not an object", map[string]any{"list": []any{1}}, "",
				"list[0]: wants an object"},
			{"a map of named values that is not an object", map[string]any{"sizes": 1}, "",
				"sizes: wants an object"},
			{"a named value of the wrong shape", map[string]any{"sizes": map[string]any{"a": "x"}}, "",
				"sizes.a: wants a number"},
			{"two entry keys that fold together",
				map[string]any{"entries": map[string]any{"One": nil, "one": nil}}, "", "entries: keys"},
			{"two named values that fold together",
				map[string]any{"sizes": map[string]any{"A": 1, "a": 2}}, "", "sizes: keys"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				var cfg demoConfig
				err := config.DecodeObject(c.val, c.at, demoFields(&cfg))
				if err == nil {
					t.Fatalf("DecodeObject accepted %#v", c.val)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
			})
		}
	})

	t.Run("a large object still refuses two spellings of one name", func(t *testing.T) {
		val := map[string]any{}
		for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
			val[k] = nil
		}
		val["A"] = nil
		table := config.Fields{}
		for k := range val {
			table[config.Fold(k)] = func(any, string) error { return nil }
		}
		err := config.DecodeObject(val, "big", table)
		if err == nil || !errors.Is(err, config.ErrFoldCollision) {
			t.Fatalf("DecodeObject = %v, want a fold collision", err)
		}
		var collision *config.FoldCollisionError
		if !errors.As(err, &collision) || collision.At != "big" {
			t.Errorf("collision = %#v", collision)
		}
	})

	t.Run("a decode reports its outcome to the loader's log", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		l := config.NewLoader(config.Options{Logger: log})
		var cfg demoConfig
		if err := l.Decode(context.Background(), map[string]any{"name": "app"}, "", demoFields(&cfg)); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !log.saw(config.EventDecodeDone) {
			t.Error("a successful decode wrote no event")
		}
		if err := l.Decode(context.Background(), map[string]any{"pahs": "x"}, "", demoFields(&cfg)); err == nil {
			t.Fatal("Decode accepted an unknown key")
		}
		if !log.saw(config.EventDecodeFailed) {
			t.Error("a failed decode wrote no event")
		}
		quiet := config.NewLoader(config.Options{Logger: &recordingLogger{floor: config.LevelError}})
		if err := quiet.Decode(context.Background(), map[string]any{"name": "a"}, "", demoFields(&cfg)); err != nil {
			t.Fatalf("Decode with a quiet logger: %v", err)
		}
	})
}

// TestPublicAPIConfigSettingsRendering drives the shape a parsed file is handed
// the decode in: the pruning of empty objects, the delimiter that names levels,
// and the overrides written over the result.
func TestPublicAPIConfigSettingsRendering(t *testing.T) {
	t.Run("an object holding no keys is pruned away", func(t *testing.T) {
		tree := &config.Tree{Root: map[string]any{
			"kept":   map[string]any{"a": 1},
			"empty":  map[string]any{},
			"hollow": map[string]any{"inner": map[string]any{}},
			"list":   []any{1, 2},
			"scalar": "x",
		}}
		got := tree.Settings(nil, nil)
		if _, present := got["empty"]; present {
			t.Error("an empty object survived the rendering")
		}
		if _, present := got["hollow"]; present {
			t.Error("a branch holding only empty objects survived the rendering")
		}
		if kept, ok := got["kept"].(map[string]any); !ok || kept["a"] != 1 {
			t.Errorf("kept = %#v", got["kept"])
		}
		if got["scalar"] != "x" {
			t.Errorf("scalar = %#v", got["scalar"])
		}
		if list, ok := got["list"].([]any); !ok || len(list) != 2 {
			t.Errorf("a list is a leaf, got %#v", got["list"])
		}
	})

	t.Run("a key carrying the delimiter names the levels it spells", func(t *testing.T) {
		tree := &config.Tree{Root: map[string]any{
			"a":       1,
			"a.b":     2,
			"log.lvl": "debug",
		}}
		got := tree.Settings(nil, nil)
		nested, ok := got["a"].(map[string]any)
		if !ok || nested["b"] != 2 {
			t.Errorf("a = %#v, want the scalar replaced by the level the leaf names", got["a"])
		}
		logs, ok := got["log"].(map[string]any)
		if !ok || logs["lvl"] != "debug" {
			t.Errorf("log = %#v", got["log"])
		}
	})

	t.Run("a configured delimiter is the one the rendering splits on", func(t *testing.T) {
		tree := &config.Tree{Root: map[string]any{"log/level": "debug", "a.b": 1}}
		l := config.NewLoader(config.Options{KeyDelim: "/"})
		got := tree.Settings(l, nil)
		logs, ok := got["log"].(map[string]any)
		if !ok || logs["level"] != "debug" {
			t.Errorf("log = %#v", got["log"])
		}
		if got["a.b"] != 1 {
			t.Errorf("a key carrying another delimiter was split: %#v", got)
		}
	})

	t.Run("an override is written over the rendering and the tree is untouched", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		l := config.NewLoader(config.Options{Logger: log})
		tree := &config.Tree{Root: map[string]any{
			"LogLevel": "info",
			"spaces":   map[string]any{"apps": map[string]any{"path": "apps"}},
			"scalar":   1,
		}}
		ov := config.MergeOverrides(
			config.Overrides{"loglevel": "debug", "spaces.apps.path": "packages"},
			config.Overrides{"scalar.deep": "made", "fresh.level.here": 7},
		)
		got := tree.Settings(l, ov)
		if got["loglevel"] != "debug" {
			t.Errorf("the override did not land: %#v", got)
		}
		if _, stale := got["LogLevel"]; stale {
			t.Error("the file's spelling survived beside the override's")
		}
		spaces := got["spaces"].(map[string]any)["apps"].(map[string]any)
		if spaces["path"] != "packages" {
			t.Errorf("nested override = %#v", spaces)
		}
		if got["scalar"].(map[string]any)["deep"] != "made" {
			t.Errorf("a scalar level was not replaced: %#v", got["scalar"])
		}
		fresh := got["fresh"].(map[string]any)["level"].(map[string]any)
		if fresh["here"] != 7 {
			t.Errorf("a missing level was not created: %#v", got["fresh"])
		}
		if tree.Root["LogLevel"] != "info" {
			t.Error("the tree was written into")
		}
		if !log.saw(config.EventOverridesApplied) {
			t.Error("the overrides event was not written")
		}
	})

	t.Run("an empty overlay leaves the base alone", func(t *testing.T) {
		base := config.Overrides{"a": 1}
		if got := config.MergeOverrides(base, nil); len(got) != 1 {
			t.Errorf("MergeOverrides with a nil overlay = %v", got)
		}
	})

	t.Run("a tree clones deeply", func(t *testing.T) {
		var absent *config.Tree
		if absent.Clone() != nil {
			t.Error("cloning a nil tree returned a tree")
		}
		tree := &config.Tree{
			Root: map[string]any{
				"object":  map[string]any{"a": 1},
				"generic": map[any]any{1: map[string]any{"b": 2}},
				"list":    []any{map[string]any{"c": 3}},
				"strings": []string{"x"},
				"scalar":  "s",
			},
			Files: []string{"app.json"},
		}
		clone := tree.Clone()
		clone.Root["object"].(map[string]any)["a"] = 99
		clone.Root["generic"].(map[any]any)[1].(map[string]any)["b"] = 99
		clone.Root["list"].([]any)[0].(map[string]any)["c"] = 99
		clone.Root["strings"].([]string)[0] = "mutated"
		clone.Files[0] = "other.json"
		if tree.Root["object"].(map[string]any)["a"] != 1 {
			t.Error("the clone shared an object")
		}
		if tree.Root["generic"].(map[any]any)[1].(map[string]any)["b"] != 2 {
			t.Error("the clone shared a generic map")
		}
		if tree.Root["list"].([]any)[0].(map[string]any)["c"] != 3 {
			t.Error("the clone shared a list")
		}
		if tree.Root["strings"].([]string)[0] != "x" {
			t.Error("the clone shared a string list")
		}
		if tree.Files[0] != "app.json" {
			t.Error("the clone shared the file list")
		}
		if clone.Root["scalar"] != "s" {
			t.Errorf("the clone lost a scalar: %#v", clone.Root["scalar"])
		}
	})

	t.Run("a top-level key is set only when it holds something", func(t *testing.T) {
		root := map[string]any{"Spaces": map[string]any{"apps": nil}, "empty": nil}
		if !config.IsSet(root, "spaces") {
			t.Error("IsSet did not match a key case-insensitively")
		}
		if config.IsSet(root, "empty") {
			t.Error("a key written with no value reports as set")
		}
		if config.IsSet(root, "absent") {
			t.Error("a key that is not there reports as set")
		}
	})
}

// TestPublicAPIConfigEnvLayers drives the env-layer helpers a program merges its
// configured environment through.
func TestPublicAPIConfigEnvLayers(t *testing.T) {
	t.Run("an env map flattens to sorted pairs", func(t *testing.T) {
		if got := config.EnvPairs(nil); got != nil {
			t.Errorf("EnvPairs(nil) = %v", got)
		}
		got := config.EnvPairs(map[string]string{"B": "2", "A": "1"})
		if !slices.Equal(got, []string{"A=1", "B=2"}) {
			t.Errorf("EnvPairs = %v", got)
		}
	})

	t.Run("a layer overlays the one under it", func(t *testing.T) {
		base := map[string]string{"A": "1", "B": "2"}
		if got := config.MergeEnv(base, nil); len(got) != 2 {
			t.Errorf("MergeEnv with a nil overlay = %v", got)
		}
		got := config.MergeEnv(base, map[string]string{"B": "over", "C": "3"})
		if got["A"] != "1" || got["B"] != "over" || got["C"] != "3" {
			t.Errorf("MergeEnv = %v", got)
		}
		if base["B"] != "2" {
			t.Error("MergeEnv wrote into the base layer")
		}
	})

	t.Run("a key that could never reach a process is refused", func(t *testing.T) {
		if err := config.ValidateEnv("env", map[string]string{"PATH": "/bin"}); err != nil {
			t.Errorf("ValidateEnv refused a valid layer: %v", err)
		}
		for _, c := range []struct {
			name     string
			env      map[string]string
			reserved []string
			want     string
		}{
			{"an empty key", map[string]string{"": "x"}, nil, "empty key"},
			{"a key carrying an equals sign", map[string]string{"A=B": "x"}, nil, "must not contain"},
			{"a key claiming a reserved prefix", map[string]string{"DISPAT_X": "x"},
				[]string{"dispat_"}, "reserved"},
			{"two keys that fold together", map[string]string{"Path": "a", "path": "b"}, nil,
				"collide case-insensitively"},
		} {
			t.Run(c.name, func(t *testing.T) {
				err := config.ValidateEnv("env", c.env, c.reserved...)
				if err == nil {
					t.Fatalf("ValidateEnv accepted %v", c.env)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
			})
		}
	})
}

// TestPublicAPIConfigEnvBinding drives the opt-in environment binding: the name
// a key derives, the variables that answer to it, and the strictness that turns
// a typo in a deployment manifest into a failure at startup.
func TestPublicAPIConfigEnvBinding(t *testing.T) {
	t.Run("a key derives its variable name", func(t *testing.T) {
		if got := config.EnvVarName("APP_", "log.level", "."); got != "APP_LOG_LEVEL" {
			t.Errorf("EnvVarName = %q", got)
		}
		if got := config.EnvVarName("APP_", "logLevel", "."); got != "APP_LOGLEVEL" {
			t.Errorf("EnvVarName = %q", got)
		}
		if got := config.EnvVarName("", "a-b", ""); got != "A_B" {
			t.Errorf("EnvVarName with no delimiter = %q, want the dash still folded", got)
		}
	})

	t.Run("the declared keys are set from the environment", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		ctx := config.WithLogger(context.Background(), log)
		b := config.EnvBinding{
			Prefix:  "APP_",
			Keys:    []string{"log.level", "count", "absent"},
			Environ: []string{"APP_LOG_LEVEL=debug", "APP_COUNT=", "PATH=/bin", "malformed"},
		}
		ov, err := b.Overrides(ctx)
		if err != nil {
			t.Fatalf("Overrides: %v", err)
		}
		if ov["log.level"] != "debug" {
			t.Errorf("overrides = %v", ov)
		}
		if value, set := ov["count"]; !set || value != "" {
			t.Errorf("a variable set to nothing must be the empty string, got %#v", ov["count"])
		}
		if _, set := ov["absent"]; set {
			t.Error("a variable that is not set claimed its key")
		}
		if !log.saw(config.EventEnvBind) {
			t.Error("no bind event was written")
		}
	})

	t.Run("a variable the prefix claims and no key answers is reported", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		ctx := config.WithLogger(context.Background(), log)
		b := config.EnvBinding{
			Prefix:  "APP_",
			Keys:    []string{"log.level"},
			Environ: []string{"APP_LOG_LEVEL=debug", "APP_TPYO=1", "APP_OTHER=2"},
		}
		if _, err := b.Overrides(ctx); err != nil {
			t.Fatalf("Overrides: %v", err)
		}
		if !log.saw(config.EventEnvUnmatched) {
			t.Error("no unmatched event was written")
		}
		b.Strict = true
		_, err := b.Overrides(ctx)
		if err == nil || !strings.Contains(err.Error(), "APP_OTHER") {
			t.Errorf("strict error = %v, want the first unmatched variable named", err)
		}
	})

	t.Run("two keys binding one variable are refused", func(t *testing.T) {
		b := config.EnvBinding{
			Prefix:  "APP_",
			Keys:    []string{"log.level", "log-level"},
			Environ: []string{},
		}
		if _, err := b.Overrides(context.Background()); err == nil {
			t.Fatal("Overrides accepted two keys binding one variable")
		}
	})

	t.Run("a caller's own derivation replaces the default one", func(t *testing.T) {
		b := config.EnvBinding{
			Prefix:   "APP_",
			Keys:     []string{"logLevel"},
			KeyDelim: ":",
			Bind:     func(prefix, key string) string { return prefix + "LOG_LEVEL" },
			Environ:  []string{"APP_LOG_LEVEL=trace"},
		}
		ov, err := b.Overrides(context.Background())
		if err != nil || ov["logLevel"] != "trace" {
			t.Errorf("Overrides = %v, %v", ov, err)
		}
	})

	t.Run("an environment nothing answers to sets nothing", func(t *testing.T) {
		b := config.EnvBinding{Prefix: "APP_", Keys: []string{"log.level"}, Environ: []string{}}
		ov, err := b.Overrides(context.Background())
		if err != nil || ov != nil {
			t.Errorf("Overrides = %v, %v, want no overrides at all", ov, err)
		}
	})

	t.Run("an unset Environ reads the process environment", func(t *testing.T) {
		t.Setenv("DISPAT_PUBLICAPI_LEVEL", "trace")
		b := config.EnvBinding{Prefix: "DISPAT_PUBLICAPI_", Keys: []string{"level"}}
		ov, err := b.Overrides(context.Background())
		if err != nil {
			t.Fatalf("Overrides: %v", err)
		}
		if ov["level"] != "trace" {
			t.Errorf("overrides = %v, want the process environment read", ov)
		}
	})
}
