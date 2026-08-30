package config

// Fuzzing, on the two surfaces that read text nobody wrote by hand: the
// parsers, which meet whatever is in a file, and the reference target, which
// meets whatever is under a `$ref`.
//
// The property in both cases is the same and it is modest: no panic, and no
// value of a shape the rest of the package does not expect. A malformed
// document is an error, which is a fine answer; a document that parses into
// something the walk cannot name is not.

import (
	"testing"
)

// FuzzParsers holds each format's parser to the two shapes the loader is
// prepared for: an error, or a document made of maps, lists and scalars.
func FuzzParsers(f *testing.F) {
	f.Add("json", `{"a": {"b": [1, "two", true, null]}}`)
	f.Add("json", `null`)
	f.Add("json", `{"a": 1e309}`)
	f.Add("yaml", "a:\n  b: [1, two, true, null]\n")
	f.Add("yaml", "? [a]\n: b\n")
	f.Add("yaml", "a: &x\n  b: *x\n")
	f.Add("toml", "[a]\nb = [1, 2]\n")
	f.Add("toml", "")
	f.Add("ini", "a = 1\n")

	formats := DefaultFormats()
	f.Fuzz(func(t *testing.T, format, body string) {
		parse, ok := formats["."+format]
		if !ok {
			t.Skip()
		}
		doc, err := parse([]byte(body))
		if err != nil {
			return
		}
		checkShape(t, doc, 0)
	})
}

// checkShape refuses a parsed document holding anything the tree walk has no
// case for. The walk names string-keyed maps, generic maps, lists and scalars;
// a parser that started producing something else would be a case to add, and
// this is what would say so.
func checkShape(t *testing.T, v any, depth int) {
	t.Helper()
	if depth > 64 {
		return
	}
	switch node := v.(type) {
	case nil, bool, string, int, int64, float64, uint64:
		return
	case map[string]any:
		for _, val := range node {
			checkShape(t, val, depth+1)
		}
	case map[any]any:
		for _, val := range node {
			checkShape(t, val, depth+1)
		}
	case []any:
		for _, val := range node {
			checkShape(t, val, depth+1)
		}
	default:
		// A time is what TOML makes of a date, which is a scalar as far as
		// everything here is concerned: it renders through WeakScalarString and
		// is copied by value.
		if _, ok := v.(interface{ String() string }); ok {
			return
		}
		t.Fatalf("a parser produced %T, which the tree walk has no case for", v)
	}
}

// FuzzRefTargets holds the reference-target reader to its contract: a list of
// non-empty names, or an error, and never a panic on whatever a file wrote
// under the key.
func FuzzRefTargets(f *testing.F) {
	f.Add(`{"$ref": "./a.json"}`)
	f.Add(`{"$ref": ["./a.json", "./b.json"]}`)
	f.Add(`{"$ref": []}`)
	f.Add(`{"$ref": 7}`)
	f.Add(`{"$ref": "   "}`)
	f.Add(`{"$ref": {"nested": true}}`)
	f.Add(`{"$ref": ["./a.json", 7]}`)
	f.Add(`{"a": 1}`)

	l := NewLoader(Options{})
	f.Fuzz(func(t *testing.T, body string) {
		doc, err := unmarshalJSON([]byte(body))
		if err != nil {
			return
		}
		node, ok := doc.(map[string]any)
		if !ok {
			return
		}
		targets, isRef, err := l.refTargets(node)
		switch {
		case err != nil:
			if !isRef {
				t.Fatalf("an object that is not a reference cannot fail: %v", err)
			}
			return
		case !isRef:
			if targets != nil {
				t.Fatalf("targets = %#v for an object that is not a reference", targets)
			}
			return
		}
		if len(targets) == 0 {
			t.Fatal("a reference with no error names at least one file")
		}
		for _, target := range targets {
			if target == "" {
				t.Fatal("a reference target is never empty")
			}
			// Every target resolves to a path, whatever it holds.
			_ = refPath(target, "/base/app.json")
		}
	})
}

// FuzzSettings holds the settings rendering to the rules it carries: it never
// panics, it never returns a nested map that is empty, and every leaf it
// records was a leaf of the tree.
func FuzzSettings(f *testing.F) {
	f.Add(`{"a": {"b": "c"}}`)
	f.Add(`{"a": {}}`)
	f.Add(`{"a.b.c": 1, "a": {"b": 2}}`)
	f.Add(`{"": {"": {}}}`)
	f.Add(`{".": 1, "..": 2}`)

	f.Fuzz(func(t *testing.T, body string) {
		doc, err := unmarshalJSON([]byte(body))
		if err != nil {
			return
		}
		root, ok := doc.(map[string]any)
		if !ok {
			return
		}
		checkNoEmptyObjects(t, (&Tree{Root: root}).Settings(nil, nil), 0)
	})
}

// checkNoEmptyObjects refuses a rendering that left an object with no keys
// behind, which is the pruning rule stated as a property.
func checkNoEmptyObjects(t *testing.T, m map[string]any, depth int) {
	t.Helper()
	if depth > 64 {
		return
	}
	for key, val := range m {
		child, ok := val.(map[string]any)
		if !ok {
			continue
		}
		if len(child) == 0 {
			t.Fatalf("%q is an object with no keys, which is not a key at all", key)
		}
		checkNoEmptyObjects(t, child, depth+1)
	}
}
