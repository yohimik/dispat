package config

// The drift guard the test model needs and the library source may not have: a
// field added to a struct without a key in its table would be a field no
// config file could ever set, and every test here would still pass.
//
// It reflects, which the library itself never does — TinyGo compiles the
// source and not the tests, so a test may reach for what the source refuses.

import (
	"reflect"
	"testing"
)

// TestEveryFieldHasAKey: one key per exported field, and one field per key.
func TestEveryFieldHasAKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		table Fields
	}{
		{"appConfig", appConfig{}, appFields(&appConfig{})},
		{"flowConfig", flowConfig{}, flowFields(&flowConfig{})},
		{"areaConfig", areaConfig{}, areaFields(&areaConfig{})},
		{"hookConfig", hookConfig{}, hookFields(&hookConfig{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.TypeOf(tc.value)
			if typ.NumField() != len(tc.table) {
				t.Fatalf("%d fields against %d keys", typ.NumField(), len(tc.table))
			}
			for i := 0; i < typ.NumField(); i++ {
				name := Fold(typ.Field(i).Name)
				if _, ok := tc.table[name]; !ok {
					t.Errorf("field %s has no key %q", typ.Field(i).Name, name)
				}
			}
		})
	}
}

// TestEveryKeyFillsItsOwnField: every key of the root table writes somewhere,
// and no two of them write to the same place. A setter table is a list of
// closures over one struct, and a copy-paste that points two keys at one field
// is the mistake this catches.
func TestEveryKeyFillsItsOwnField(t *testing.T) {
	sample := map[string]any{
		"name":        "n",
		"shell":       []any{"sh"},
		"concurrency": []any{1},
		"loglevel":    "warn",
		"strict":      true,
		"quiet":       true,
		"verbose":     true,
		"env":         map[string]any{"A": "1"},
		"custom":      map[string]any{"a": 1},
		"flow":        map[string]any{"build": []any{"b"}},
		"areas":       map[string]any{"libs": map[string]any{"path": "p"}},
		"hooks":       []any{map[string]any{"url": "u"}},
		"tags":        []any{"t"},
	}
	table := appFields(&appConfig{})
	for key := range table {
		if _, ok := sample[key]; !ok {
			t.Fatalf("the sample has nothing for %q", key)
		}
	}

	for key := range sample {
		var cfg appConfig
		if err := DecodeObject(map[string]any{key: sample[key]}, "", appFields(&cfg)); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		v := reflect.ValueOf(cfg)
		var filled []string
		for i := 0; i < v.NumField(); i++ {
			if !v.Field(i).IsZero() {
				filled = append(filled, v.Type().Field(i).Name)
			}
		}
		if len(filled) != 1 || Fold(filled[0]) != key {
			t.Errorf("%q filled %v, want exactly its own field", key, filled)
		}
	}
}
