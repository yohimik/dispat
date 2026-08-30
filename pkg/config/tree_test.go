package config

// The two rules the settings map carries, the overrides written over it, and
// the clone a caller that means to write takes first.

import (
	"reflect"
	"testing"
)

// TestSettingsPrunesEmptyObjectsAndSplitsPaths pins the two rules the decode
// input carries that the tree does not: an object with no keys is not a key at
// all, which is why an opt-in block written as a bare {} says nothing rather
// than enabling itself at its defaults; and a key spelled with the delimiter
// is the levels it names.
func TestSettingsPrunesEmptyObjectsAndSplitsPaths(t *testing.T) {
	out := settingsOf(map[string]any{
		"kept":    map[string]any{"leaf": "v"},
		"empty":   map[string]any{},
		"hollow":  map[string]any{"inner": map[string]any{}},
		"deeper":  map[string]any{"a": map[string]any{"b": map[string]any{}}},
		"a.b":     "dotted",
		"list":    []any{},
		"nothing": nil,
	}, nil)

	if want := map[string]any{"leaf": "v"}; !reflect.DeepEqual(want, out["kept"]) {
		t.Errorf("kept = %#v", out["kept"])
	}
	for _, key := range []string{"empty", "hollow", "deeper"} {
		if _, present := out[key]; present {
			t.Errorf("%q survived: an object with no keys, at any depth, is not a key", key)
		}
	}
	if want := map[string]any{"b": "dotted"}; !reflect.DeepEqual(want, out["a"]) {
		t.Errorf("the delimiter is a level: %#v", out["a"])
	}
	if want := []any{}; !reflect.DeepEqual(want, out["list"]) {
		t.Errorf("an empty list is a value, not an absence: %#v", out["list"])
	}
	if _, present := out["nothing"]; !present {
		t.Error("a key written with no value is still a key")
	}
}

// TestSettingsMergesTheLevelsADottedKeyNames: a dotted key and a written
// object name the same level, so what they hold ends up in one place rather
// than one replacing the other.
func TestSettingsMergesTheLevelsADottedKeyNames(t *testing.T) {
	out := settingsOf(map[string]any{
		"flow":         map[string]any{"build": "b"},
		"flow.publish": "p",
	}, nil)

	want := map[string]any{"build": "b", "publish": "p"}
	if !reflect.DeepEqual(want, out["flow"]) {
		t.Errorf("flow = %#v, want %#v", out["flow"], want)
	}
}

// TestSettingsIsTheSameEveryRun: a key that is both a leaf and a level is a
// configuration nobody meant to write, and the point is only that it resolves
// the same way every time — a load that fails differently from one run to the
// next is a load nobody can fix.
func TestSettingsIsTheSameEveryRun(t *testing.T) {
	root := map[string]any{
		"a":     "leaf",
		"a.b":   "level",
		"a.b.c": "deeper",
		"z":     map[string]any{"y": "v"},
		"z.y":   "other",
	}
	first := settingsOf(root, nil)
	for i := 0; i < 50; i++ {
		if got := settingsOf(root, nil); !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d gave %#v where the first gave %#v", i, got, first)
		}
	}
}

// TestSettingsKeyDelimIsAnOption: the separator is the caller's, and a key
// carrying the default one is a plain key to a loader spelled differently.
func TestSettingsKeyDelimIsAnOption(t *testing.T) {
	l := NewLoader(Options{KeyDelim: "/"})
	out := (&Tree{Root: map[string]any{"a/b": "split", "c.d": "kept"}}).Settings(l, nil)

	if want := map[string]any{"b": "split"}; !reflect.DeepEqual(want, out["a"]) {
		t.Errorf("a = %#v", out["a"])
	}
	if out["c.d"] != "kept" {
		t.Errorf("out = %#v, want the dot to be an ordinary character here", out)
	}
}

// TestSettingsLeavesTheTreeAlone: the tree is the file as it was read, and
// rendering the settings — overrides included — does not write into it.
func TestSettingsLeavesTheTreeAlone(t *testing.T) {
	root := map[string]any{
		"flow":  map[string]any{"build": []any{"one"}},
		"areas": map[string]any{"Libs": map[string]any{"path": "pkgs"}},
	}
	tree := &Tree{Root: root}
	before := cloneMap(root)

	out := tree.Settings(nil, Overrides{"logLevel": "warn", "flow.build": []string{"two"}})
	out["areas"].(map[string]any)["Libs"].(map[string]any)["path"] = "changed"

	if !reflect.DeepEqual(before, tree.Root) {
		t.Errorf("the tree changed:\n got %#v\nwant %#v", tree.Root, before)
	}
}

// TestIsSet: a key holding null is not a declaration, and the question is
// asked case-insensitively like every other.
func TestIsSet(t *testing.T) {
	root := map[string]any{"Areas": map[string]any{}, "empty": nil, "list": []any{}}
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"areas", true}, {"AREAS", true}, {"empty", false}, {"list", true}, {"absent", false},
	} {
		if got := IsSet(root, tc.key); got != tc.want {
			t.Errorf("IsSet(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestCloneCopiesEveryContainer: the parsers produce string-keyed maps,
// generic maps and lists, an override adds a list of strings, and every one of
// them is copied rather than shared. A value of any other type is passed
// through, which is what the clone says instead of reflecting over containers
// nothing builds.
func TestCloneCopiesEveryContainer(t *testing.T) {
	original := &Tree{
		Root: map[string]any{
			"generic": map[any]any{1: map[string]any{"deep": "v"}},
			"list":    []any{map[string]any{"deep": "v"}},
			"strings": []string{"one"},
			"nils":    []any{nil},
			"scalar":  "v",
		},
		Files: []string{"app.json"},
	}
	clone := original.Clone()
	if !reflect.DeepEqual(original, clone) {
		t.Fatalf("clone = %#v, want %#v", clone, original)
	}

	clone.Root["generic"].(map[any]any)[1].(map[string]any)["deep"] = "changed"
	clone.Root["list"].([]any)[0].(map[string]any)["deep"] = "changed"
	clone.Root["strings"].([]string)[0] = "changed"
	clone.Files[0] = "changed"

	if got := original.Root["generic"].(map[any]any)[1].(map[string]any)["deep"]; got != "v" {
		t.Errorf("generic was shared: %q", got)
	}
	if got := original.Root["list"].([]any)[0].(map[string]any)["deep"]; got != "v" {
		t.Errorf("list was shared: %q", got)
	}
	if got := original.Root["strings"].([]string)[0]; got != "one" {
		t.Errorf("strings was shared: %q", got)
	}
	if original.Files[0] != "app.json" {
		t.Errorf("files was shared: %q", original.Files[0])
	}
	if (*Tree)(nil).Clone() != nil {
		t.Error("cloning nothing is nothing")
	}
}

// TestOverridesReplaceWhateverSpellingTheFileUsed pins the one thing an
// override binding ever meant: the value replaces what the file says rather
// than sitting beside it. A file writing `logLevel` and an override writing
// `loglevel` would otherwise be two keys the decode refuses as a collision,
// over a value the operator passed correctly.
func TestOverridesReplaceWhateverSpellingTheFileUsed(t *testing.T) {
	for _, spelling := range []string{"logLevel", "loglevel", "LOGLEVEL"} {
		out := settingsOf(map[string]any{spelling: "debug"}, Overrides{"logLevel": "warn"})
		want := map[string]any{"logLevel": "warn"}
		if !reflect.DeepEqual(want, out) {
			t.Errorf("the file wrote %q and the settings are %#v, want %#v", spelling, out, want)
		}
	}
}

// TestOverridesWalkToANestedKey: an override names a key path, so a nested key
// is as reachable as a top-level one, and the levels above it keep the
// spelling the file gave them.
func TestOverridesWalkToANestedKey(t *testing.T) {
	out := settingsOf(map[string]any{
		"Flow":  map[string]any{"Build": []any{"one"}, "publish": []any{"p"}},
		"areas": map[string]any{"libs": map[string]any{"path": "pkgs"}},
	}, Overrides{
		"Flow.build":        []string{"two"},
		"areas.libs.path":   "elsewhere",
		"areas.apps.path":   "new",
		"missing.deep.leaf": "created",
	})

	flow := out["Flow"].(map[string]any)
	if want := []string{"two"}; !reflect.DeepEqual(want, flow["build"]) {
		t.Errorf("build = %#v", flow["build"])
	}
	if _, both := flow["Build"]; both {
		t.Error("the file's spelling stayed beside the override's")
	}
	if flow["publish"] == nil {
		t.Error("everything the override did not name survives")
	}
	areas := out["areas"].(map[string]any)
	if areas["libs"].(map[string]any)["path"] != "elsewhere" {
		t.Errorf("areas = %#v", areas)
	}
	if areas["apps"].(map[string]any)["path"] != "new" {
		t.Errorf("a level the file never wrote is created: %#v", areas)
	}
	if out["missing"].(map[string]any)["deep"].(map[string]any)["leaf"] != "created" {
		t.Errorf("out = %#v", out)
	}
}

// TestOverridesReplaceALevelThatIsNotAnObject: the override's own path says
// what shape the levels above it have, so a leaf standing where a level is
// named is replaced rather than tunnelled into.
func TestOverridesReplaceALevelThatIsNotAnObject(t *testing.T) {
	out := settingsOf(map[string]any{"Flow": "not an object"},
		Overrides{"flow.build": []string{"one"}})

	want := map[string]any{"flow": map[string]any{"build": []string{"one"}}}
	if !reflect.DeepEqual(want, out) {
		t.Errorf("out = %#v, want %#v", out, want)
	}
}

// TestOverridesAreAppliedInOneOrder: two overrides that disagree about the
// shape of a level always resolve the same way, whichever order the map hands
// them over in.
func TestOverridesAreAppliedInOneOrder(t *testing.T) {
	ov := Overrides{"a": "leaf", "a.b": "level", "a.b.c": "deeper"}
	first := settingsOf(map[string]any{}, ov)
	for i := 0; i < 50; i++ {
		if got := settingsOf(map[string]any{}, ov); !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d gave %#v where the first gave %#v", i, got, first)
		}
	}
}

// TestMergeOverrides: over wins key by key, a nil overlay leaves the base
// alone, and neither input is written into.
func TestMergeOverrides(t *testing.T) {
	base := Overrides{"a": 1, "b": 2}
	if got := MergeOverrides(base, nil); !reflect.DeepEqual(Overrides{"a": 1, "b": 2}, got) {
		t.Errorf("nil overlay: %#v", got)
	}
	got := MergeOverrides(base, Overrides{"b": 3, "c": 4})
	if want := (Overrides{"a": 1, "b": 3, "c": 4}); !reflect.DeepEqual(want, got) {
		t.Errorf("merged = %#v, want %#v", got, want)
	}
	if base["b"] != 2 {
		t.Error("the base was written into")
	}
	if got := MergeOverrides(nil, Overrides{"a": 1}); !reflect.DeepEqual(Overrides{"a": 1}, got) {
		t.Errorf("nil base: %#v", got)
	}
}
