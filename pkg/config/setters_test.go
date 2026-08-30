package config

// Each setter's own table: the shapes it takes, the shorthands it admits, and
// what it refuses.

import (
	"reflect"
	"testing"
)

// TestBoolFillsAPlainFlag: the difference from BoolPtr is the whole point —
// a plain flag has no way to say "this layer did not speak".
func TestBoolFillsAPlainFlag(t *testing.T) {
	cfg, err := decodeApp(map[string]any{"strict": "true"})
	if err != nil || !cfg.Strict {
		t.Fatalf("strict = %v, err = %v", cfg.Strict, err)
	}
	cfg, err = decodeApp(map[string]any{"strict": 0})
	if err != nil || cfg.Strict {
		t.Fatalf("strict = %v, err = %v", cfg.Strict, err)
	}
	if _, err := decodeApp(map[string]any{"strict": []any{}}); err == nil {
		t.Error("a list is not a flag")
	}
}

// TestStringsTakesEveryShape: the comma shorthand for a scalar string, a list
// of values as written, and any other scalar as the one-element list it stands
// for.
func TestStringsTakesEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want []string
	}{
		{"a comma list", "a,b", []string{"a", "b"}},
		{"one name", "a", []string{"a"}},
		{"a written list", []any{"a", 2, true}, []string{"a", "2", "true"}},
		{"a list of strings", []string{"a", "b"}, []string{"a", "b"}},
		{"a lone number", 7, []string{"7"}},
		{"a lone flag", true, []string{"true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := decodeApp(map[string]any{"tags": tc.val})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(tc.want, cfg.Tags) {
				t.Errorf("tags = %#v, want %#v", cfg.Tags, tc.want)
			}
		})
	}
	if _, err := decodeApp(map[string]any{"tags": map[string]any{}}); err == nil {
		t.Error("an object is not a list of names")
	}
}

// TestIntsTakesEveryShape: the same two shorthands Strings takes, over the
// four spellings of a number.
func TestIntsTakesEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want []int
	}{
		{"a comma list", "4,2", []int{4, 2}},
		{"one number", 4, []int{4}},
		{"one number as text", "4", []int{4}},
		{"a written list", []any{4, "2", int64(1)}, []int{4, 2, 1}},
		{"a list of strings", []string{"4", "2"}, []int{4, 2}},
		{"an empty string", "", []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := decodeApp(map[string]any{"concurrency": tc.val})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(tc.want, cfg.Concurrency) {
				t.Errorf("concurrency = %#v, want %#v", cfg.Concurrency, tc.want)
			}
		})
	}
	for _, val := range []any{map[string]any{}, "x,2", []any{"x"}} {
		if _, err := decodeApp(map[string]any{"concurrency": val}); err == nil {
			t.Errorf("%#v: want an error", val)
		}
	}
}

// TestBoolPtrIsTheTriState: the pointer is allocated only because the file
// wrote the key, so a layer that says nothing stays nil.
func TestBoolPtrIsTheTriState(t *testing.T) {
	cfg, err := decodeApp(map[string]any{"quiet": false})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Quiet == nil || *cfg.Quiet {
		t.Errorf("quiet = %v, want a pointer to false", cfg.Quiet)
	}
	if !reflect.DeepEqual(boolPtr(false), cfg.Quiet) {
		t.Errorf("quiet = %v", cfg.Quiet)
	}
	if cfg.Verbose != nil {
		t.Errorf("verbose = %v, want nil", cfg.Verbose)
	}
}

// TestObjectMapAndListDecodeTheirEntries: a map of named objects keeps the
// names, a list of objects keeps the order, and both name their errors by
// where the entry sits.
func TestObjectMapAndListDecodeTheirEntries(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"areas": map[string]any{
			"libs": map[string]any{"path": "pkgs", "areas": map[string]any{
				"inner": map[string]any{"path": "deep"}}},
		},
		"hooks": []any{
			map[string]any{"url": "one", "events": "a,b"},
			map[string]any{"url": "two", "enabled": true},
		},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := cfg.Areas["libs"].Areas["inner"].Path; !reflect.DeepEqual([]string{"deep"}, got) {
		t.Errorf("nested path = %#v", got)
	}
	if len(cfg.Hooks) != 2 || cfg.Hooks[0].URL != "one" || cfg.Hooks[1].URL != "two" {
		t.Errorf("hooks = %#v", cfg.Hooks)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(want, cfg.Hooks[0].Events) {
		t.Errorf("events = %#v", cfg.Hooks[0].Events)
	}
	if cfg.Hooks[1].Enabled == nil || !*cfg.Hooks[1].Enabled {
		t.Errorf("enabled = %v", cfg.Hooks[1].Enabled)
	}
}

// TestRawMapCopiesItsOwnLevel: the caller's map is not the tree's, so writing
// into what was decoded cannot reach back into the file that was read.
func TestRawMapCopiesItsOwnLevel(t *testing.T) {
	src := map[string]any{"a": 1}
	cfg, err := decodeApp(map[string]any{"custom": src})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg.Custom["b"] = 2
	if _, leaked := src["b"]; leaked {
		t.Error("the source map was written into")
	}
}

// TestMapOfCarriesTheObjectRules: a caller whose values are neither a scalar
// nor an object writes the reader and gets the rules — an object, no two keys
// folding together, keys in order, names as written — for free.
func TestMapOfCarriesTheObjectRules(t *testing.T) {
	type entry []string
	read := func(val any, at string) (entry, error) {
		if val == nil {
			// The reader decides what an absent value means; here it is an
			// entry the file named and said nothing about.
			return nil, nil
		}
		if s, ok := val.(string); ok {
			return entry{s}, nil // a scalar that never splits, unlike Strings
		}
		items, ok := WeakList(val)
		if !ok {
			return nil, Wants(at, "a name or a list of names")
		}
		out := make(entry, 0, len(items))
		for i, item := range items {
			s, err := WeakString(item, IndexPath(at, i))
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}

	var dst map[string]entry
	set := MapOf(&dst, read)

	if err := set(map[string]any{
		"Build": "make all,now",
		"test":  []any{"one", "two"},
		"empty": nil,
	}, "scripts"); err != nil {
		t.Fatalf("set: %v", err)
	}
	want := map[string]entry{
		"Build": {"make all,now"},
		"test":  {"one", "two"},
		"empty": nil,
	}
	if !reflect.DeepEqual(want, dst) {
		t.Errorf("dst = %#v, want %#v", dst, want)
	}

	if err := set("text", "scripts"); err == nil || err.Error() != "scripts: wants an object" {
		t.Errorf("err = %v", err)
	}
	err := set(map[string]any{"a": "1", "A": "2"}, "scripts")
	if err == nil || err.Error() != `scripts: keys "A" and "a" collide case-insensitively` {
		t.Errorf("err = %v", err)
	}
	err = set(map[string]any{"a": map[string]any{}}, "scripts")
	if err == nil || err.Error() != "scripts.a: wants a name or a list of names" {
		t.Errorf("err = %v", err)
	}
}
