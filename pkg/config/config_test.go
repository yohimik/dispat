package config

// The options, the defaults they stand in for, and the errors' own rendering.

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

// TestOptionsZeroValuesAreTheDefaults: Options{} is the whole default
// configuration, so a caller changing one thing writes one field.
func TestOptionsZeroValuesAreTheDefaults(t *testing.T) {
	got := NewLoader(Options{}).Options()
	if got.RefKey != DefaultRefKey || got.MaxRefDepth != DefaultMaxRefDepth ||
		got.KeyDelim != DefaultKeyDelim {
		t.Errorf("options = %#v", got)
	}
	if got.ReadFile == nil || len(got.Formats) != 4 {
		t.Errorf("options = %#v", got)
	}
	for _, ext := range []string{".json", ".yaml", ".yml", ".toml"} {
		if got.Formats[ext] == nil {
			t.Errorf("no parser for %s", ext)
		}
	}
}

// TestDefaultIsTheSameConfiguration: Default() reads the defaults back rather
// than being a second definition of them.
func TestDefaultIsTheSameConfiguration(t *testing.T) {
	zero := NewLoader(Options{}).Options()
	full := NewLoader(Default()).Options()
	if zero.RefKey != full.RefKey || zero.MaxRefDepth != full.MaxRefDepth ||
		zero.KeyDelim != full.KeyDelim || len(zero.Formats) != len(full.Formats) {
		t.Errorf("Default() = %#v, want the same as Options{}: %#v", full, zero)
	}
}

// TestDefaultFormatsIsAFreshCopy: a caller adding a format is adding it to its
// own table, not to everybody's.
func TestDefaultFormatsIsAFreshCopy(t *testing.T) {
	first := DefaultFormats()
	first[".ini"] = func([]byte) (any, error) { return nil, nil }
	if _, leaked := DefaultFormats()[".ini"]; leaked {
		t.Error("the table is shared")
	}
}

// TestNewLoaderCopiesTheOptions: later edits to the caller's struct do not
// reach the loader.
func TestNewLoaderCopiesTheOptions(t *testing.T) {
	opts := Options{RefKey: "$include"}
	l := NewLoader(opts)
	opts.RefKey = "$other"
	if got := l.Options().RefKey; got != "$include" {
		t.Errorf("refKey = %q", got)
	}
}

// TestNegativeRefDepthIsAnAnswer: the zero value takes the default, so a
// negative value is what it takes to say "follow no reference at all".
func TestNegativeRefDepthIsAnAnswer(t *testing.T) {
	if got := NewLoader(Options{MaxRefDepth: -1}).Options().MaxRefDepth; got != -1 {
		t.Errorf("maxRefDepth = %d", got)
	}
	if got := NewLoader(Options{MaxRefDepth: 0}).Options().MaxRefDepth; got != DefaultMaxRefDepth {
		t.Errorf("maxRefDepth = %d, want the default", got)
	}
}

// TestErrorsRenderTheirParts: every error is a value with the parts of its
// sentence on it, so a caller wrapping this package in wording of its own
// reaches for the fields instead of parsing the text.
func TestErrorsRenderTheirParts(t *testing.T) {
	for _, tc := range []struct {
		err     error
		want    string
		unwraps error
	}{
		{&FileError{Path: "a.json", Err: os.ErrNotExist},
			"cannot read a.json: file does not exist", os.ErrNotExist},
		{&KeyError{File: "a.json", Key: "env", Err: ErrRefTarget},
			"a.json: env: $ref must name another config file, or a list of them", ErrRefTarget},
		{&RefChainError{Err: ErrRefCycle, Chain: []string{"a (k)", "a"}},
			"$ref cycle: a (k) -> a; a file cannot reference itself, directly or through another", ErrRefCycle},
		{&RefChainError{Err: ErrRefDepth, Chain: []string{"a (k)", "b"}, Depth: 32},
			"$ref nesting is more than 32 files deep: a (k) -> b", ErrRefDepth},
		{&UnknownKeyError{Key: "a.b"}, `unknown key "a.b"`, ErrUnknownKey},
		{&FoldCollisionError{At: "env", First: "A", Second: "a"},
			`env: keys "A" and "a" collide case-insensitively`, ErrFoldCollision},
		{&NoConfigError{Dir: "/repo", Names: []string{"app.json", "app.yaml"}},
			"no config file found in /repo or any parent directory (tried app.json, app.yaml)", ErrNoConfig},
		{Wants("flow.build", "a list of names"), "flow.build: wants a list of names", nil},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
		if tc.unwraps != nil && !errors.Is(tc.err, tc.unwraps) {
			t.Errorf("%v does not unwrap to %v", tc.err, tc.unwraps)
		}
	}
}

// TestUnmarshalersReturnTheDocument: the three parsers, asked directly, so
// that what "empty" means in each of them is stated once.
func TestUnmarshalersReturnTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   Unmarshal
		in   string
		want any
	}{
		{"json object", unmarshalJSON, `{"a": "b"}`, map[string]any{"a": "b"}},
		{"json null", unmarshalJSON, `null`, nil},
		{"yaml object", unmarshalYAML, "a: b\n", map[string]any{"a": "b"}},
		{"yaml empty", unmarshalYAML, "", nil},
		{"toml table", unmarshalTOML, `a = "b"`, map[string]any{"a": "b"}},
		{"toml empty", unmarshalTOML, "", nil},
	} {
		got, err := tc.fn([]byte(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(tc.want, got) {
			t.Errorf("%s = %#v, want %#v", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		fn   Unmarshal
		in   string
	}{
		{"json", unmarshalJSON, "{"},
		{"yaml", unmarshalYAML, "a: [\n"},
		{"toml", unmarshalTOML, "a = = 1"},
	} {
		if _, err := tc.fn([]byte(tc.in)); err == nil {
			t.Errorf("%s: want an error", tc.name)
		}
	}
}
