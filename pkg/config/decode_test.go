package config

// The decode's own rules, stated against the test model rather than against
// any one consumer's: what a key holding nothing means, what an unknown key
// costs, which shorthands a value may be written in, and why the first
// mistake reported is always the same one.

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// decodeApp is the decode on its own, with no file behind it.
func decodeApp(src map[string]any) (*appConfig, error) {
	var cfg appConfig
	return &cfg, DecodeObject(src, "", appFields(&cfg))
}

// TestDecodeUnknownKeyIsRefusedByItsFullPath: a key no table holds is a key
// the model has no field for, and the error names it from the root, because a
// typo the loader accepts is configuration that silently never applies.
func TestDecodeUnknownKeyIsRefusedByItsFullPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  map[string]any
		want string
	}{
		{"at the root", map[string]any{"nmae": "v"}, `unknown key "nmae"`},
		{"in a sub-object", map[string]any{"flow": map[string]any{"biuld": []any{}}},
			`unknown key "flow.biuld"`},
		{"in a named entry", map[string]any{"areas": map[string]any{"libs": map[string]any{"paht": "p"}}},
			`unknown key "areas.libs.paht"`},
		{"in a list element", map[string]any{"hooks": []any{map[string]any{"rul": "u"}}},
			`unknown key "hooks[0].rul"`},
		{"three levels down", map[string]any{"areas": map[string]any{
			"libs": map[string]any{"areas": map[string]any{"inner": map[string]any{"nope": 1}}}}},
			`unknown key "areas.libs.areas.inner.nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeApp(tc.src)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrUnknownKey) {
				t.Errorf("err = %v, want ErrUnknownKey", err)
			}
		})
	}
}

// TestDecodeUnknownKeyWithNoValueStillErrors: a typo is a typo whether or not
// the key holds anything. The key-said-nothing rule comes after the table
// lookup, not before it.
func TestDecodeUnknownKeyWithNoValueStillErrors(t *testing.T) {
	_, err := decodeApp(map[string]any{"nmae": nil})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

// TestDecodeNilValueIsAKeyThatSaidNothing: a key written with no value is
// used, so it is not a typo, and it leaves its field at the zero value the
// caller's own defaults apply to.
func TestDecodeNilValueIsAKeyThatSaidNothing(t *testing.T) {
	cfg, err := decodeApp(map[string]any{"name": nil, "flow": nil, "quiet": nil})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Name != "" || cfg.Flow != nil || cfg.Quiet != nil {
		t.Errorf("cfg = %#v, want every field left alone", cfg)
	}
}

// TestDecodeFoldsFieldKeysAndKeepsEntryKeys: the table is keyed in lower case
// and the written key is folded to find its setter, which is what lets
// `logLevel` and `loglevel` both load; the names of a map of entries are not
// table keys, and keep the case the file gave them.
func TestDecodeFoldsFieldKeysAndKeepsEntryKeys(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"LOGLEVEL": "warn",
		"Areas":    map[string]any{"MiXed": map[string]any{"Path": "p"}},
		"Env":      map[string]any{"PATH": "a", "home": "b"},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("logLevel = %q", cfg.LogLevel)
	}
	if _, ok := cfg.Areas["MiXed"]; !ok {
		t.Errorf("areas = %#v, want the name spelled as the file wrote it", cfg.Areas)
	}
	want := map[string]string{"PATH": "a", "home": "b"}
	if !reflect.DeepEqual(want, cfg.Env) {
		t.Errorf("env = %#v, want the case each name was written in: %#v", cfg.Env, want)
	}
}

// TestDecodeRefusesTwoSpellingsOfOneKey: a name written twice in one object
// has no lookup that could answer for it, so it is refused wherever objects
// are read — the root, a sub-object, a map of entries, a map of values.
func TestDecodeRefusesTwoSpellingsOfOneKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  map[string]any
		want string
	}{
		{"at the root", map[string]any{"name": "a", "Name": "b"},
			`the document: keys "Name" and "name" collide case-insensitively`},
		{"in a sub-object", map[string]any{"flow": map[string]any{"build": []any{}, "Build": []any{}}},
			`flow: keys "Build" and "build" collide case-insensitively`},
		{"in a map of entries", map[string]any{"areas": map[string]any{"libs": nil, "Libs": nil}},
			`areas: keys "Libs" and "libs" collide case-insensitively`},
		{"in a map of values", map[string]any{"env": map[string]any{"a": "1", "A": "2"}},
			`env: keys "A" and "a" collide case-insensitively`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeApp(tc.src)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrFoldCollision) {
				t.Errorf("err = %v, want ErrFoldCollision", err)
			}
		})
	}
}

// TestDecodeRefusesTwoSpellingsInAWideObject: the fold check compares keys
// against a slice for a small object and against a map for a large one, and
// the two must report the same collision in the same words.
func TestDecodeRefusesTwoSpellingsInAWideObject(t *testing.T) {
	for _, size := range []int{2, foldScan, foldScan + 1, foldScan * 4} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			env := map[string]any{"dup": "a", "DUP": "b"}
			for i := len(env); i < size; i++ {
				env[fmt.Sprintf("k%02d", i)] = "v"
			}
			_, err := decodeApp(map[string]any{"env": env})
			want := `env: keys "DUP" and "dup" collide case-insensitively`
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
}

// TestDecodeReportsTheSameMistakeEveryTime: a map has no order of its own, so
// the keys are visited sorted and a file with several mistakes in it always
// reports the same one first.
func TestDecodeReportsTheSameMistakeEveryTime(t *testing.T) {
	src := map[string]any{"aaa": 1, "zzz": 2, "mmm": 3}
	for i := 0; i < 50; i++ {
		_, err := decodeApp(src)
		if err == nil || err.Error() != `unknown key "aaa"` {
			t.Fatalf("run %d: err = %v", i, err)
		}
	}
	// And the collision check runs before any setter, so a file with both
	// mistakes reports the collision rather than whichever key sorted first.
	_, err := decodeApp(map[string]any{"Name": "a", "name": "b", "aaa": 1})
	if !errors.Is(err, ErrFoldCollision) {
		t.Fatalf("err = %v, want the collision first", err)
	}
}

// TestDecodeAllocatesAnObjectOnlyWhenTheFileWroteOne: nil is a layer saying
// nothing, and an object with no keys at all was pruned before the decoder saw
// it, so it says nothing either.
func TestDecodeAllocatesAnObjectOnlyWhenTheFileWroteOne(t *testing.T) {
	cfg, err := decodeApp(settingsOf(map[string]any{
		"flow": map[string]any{},
	}, nil))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Flow != nil {
		t.Errorf("flow = %#v, want nil: a bare {} says nothing", cfg.Flow)
	}

	cfg, err = decodeApp(settingsOf(map[string]any{
		"flow": map[string]any{"build": []any{"one"}},
	}, nil))
	if err != nil || cfg.Flow == nil {
		t.Fatalf("flow = %#v, err = %v", cfg.Flow, err)
	}
}

// TestDecodeWeakScalarsFillTypedFields: a config file's format has types and
// the config language does not, so every spelling of a value fills the field
// it was written for.
func TestDecodeWeakScalarsFillTypedFields(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"name":        7,
		"loglevel":    true,
		"concurrency": "4",
		"quiet":       "true",
		"verbose":     1,
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Name != "7" || cfg.LogLevel != "true" {
		t.Errorf("cfg = %#v", cfg)
	}
	if !reflect.DeepEqual([]int{4}, cfg.Concurrency) {
		t.Errorf("concurrency = %#v", cfg.Concurrency)
	}
	if cfg.Quiet == nil || !*cfg.Quiet || cfg.Verbose == nil || !*cfg.Verbose {
		t.Errorf("quiet = %v, verbose = %v", cfg.Quiet, cfg.Verbose)
	}
}

// TestDecodeReadsEveryFormatsSpellingOfANumber: every parser has its own Go
// type for a number — JSON a float64, YAML an int, TOML an int64 — and an
// override hands its value over as text, so all four are the same number here.
func TestDecodeReadsEveryFormatsSpellingOfANumber(t *testing.T) {
	for _, val := range []any{4, int64(4), float64(4), "4"} {
		cfg, err := decodeApp(map[string]any{"hooks": []any{map[string]any{"retries": val}}})
		if err != nil {
			t.Fatalf("%T: %v", val, err)
		}
		if cfg.Hooks[0].Retries != 4 {
			t.Errorf("%T gave %d", val, cfg.Hooks[0].Retries)
		}
	}
	// A float carrying a fraction is refused rather than truncated.
	_, err := decodeApp(map[string]any{"hooks": []any{map[string]any{"retries": 2.5}}})
	if err == nil || !strings.Contains(err.Error(), "wants a whole number") {
		t.Fatalf("err = %v", err)
	}
}

// TestDecodeReadsEveryFormatsSpellingOfABoolean: both spellings a format
// offers are accepted, and so is a number, which is how a value that travelled
// through an environment variable or a template arrives.
func TestDecodeReadsEveryFormatsSpellingOfABoolean(t *testing.T) {
	for _, tc := range []struct {
		val  any
		want bool
	}{
		{true, true}, {false, false}, {"true", true}, {"yes", false},
		{1, true}, {0, false}, {int64(2), true}, {float64(0), false}, {"", false},
	} {
		cfg, err := decodeApp(map[string]any{"quiet": tc.val})
		if tc.val == "yes" {
			// "yes" is not one of the two spellings Go's parser takes.
			if err == nil || !strings.Contains(err.Error(), "wants true or false") {
				t.Errorf("%#v: err = %v", tc.val, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%#v: %v", tc.val, err)
		}
		if cfg.Quiet == nil || *cfg.Quiet != tc.want {
			t.Errorf("%#v gave %v, want %v", tc.val, cfg.Quiet, tc.want)
		}
	}
}

// TestDecodeLiftsALoneScalarIntoItsContainer: a scalar stands in for the
// one-element container it belongs in, and a comma-separated string is the
// list somebody typed on a command line.
func TestDecodeLiftsALoneScalarIntoItsContainer(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"tags":        "one,two",
		"shell":       "sh",
		"concurrency": "4,2",
		"hooks":       map[string]any{"url": "u"},
		"areas":       map[string]any{"libs": map[string]any{"path": "a,b"}},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(want, cfg.Tags) {
		t.Errorf("tags = %#v", cfg.Tags)
	}
	if want := []string{"sh"}; !reflect.DeepEqual(want, cfg.Shell) {
		t.Errorf("shell = %#v", cfg.Shell)
	}
	if want := []int{4, 2}; !reflect.DeepEqual(want, cfg.Concurrency) {
		t.Errorf("concurrency = %#v", cfg.Concurrency)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].URL != "u" {
		t.Errorf("hooks = %#v, want a lone object as the one-element list", cfg.Hooks)
	}
	if want := []string{"a,b"}; !reflect.DeepEqual(want, cfg.Areas["libs"].Path) {
		t.Errorf("path = %#v, want the comma kept: a setter decides its own shorthand", cfg.Areas["libs"].Path)
	}
}

// TestDecodeEmptyListsKeepTheirEmptiness: an empty list is a value — "nothing
// here" — and never the absence a nil field means.
func TestDecodeEmptyListsKeepTheirEmptiness(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"tags":        "",
		"shell":       []any{},
		"concurrency": []any{},
		"hooks":       []any{},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for name, got := range map[string]int{
		"tags": len(cfg.Tags), "shell": len(cfg.Shell),
		"concurrency": len(cfg.Concurrency), "hooks": len(cfg.Hooks),
	} {
		if got != 0 {
			t.Errorf("%s = %d, want empty", name, got)
		}
	}
	if cfg.Tags == nil || cfg.Shell == nil || cfg.Concurrency == nil || cfg.Hooks == nil {
		t.Errorf("cfg = %#v, want empty lists rather than absent ones", cfg)
	}
}

// TestDecodeRefusesAValueOfTheWrongShape: the one sentence a value of the
// wrong shape earns is the key and what belongs under it. Saying what was
// written instead would repeat the file back at a reader who has it open.
func TestDecodeRefusesAValueOfTheWrongShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  map[string]any
		want string
	}{
		{"an object where a name belongs", map[string]any{"name": map[string]any{}},
			"name: wants a string"},
		{"a list where a name belongs", map[string]any{"name": []any{"a"}},
			"name: wants a string"},
		{"a name where an object belongs", map[string]any{"flow": "b"},
			"flow: wants an object"},
		{"a name where a map of entries belongs", map[string]any{"areas": "b"},
			"areas: wants an object"},
		{"a name where a map of values belongs", map[string]any{"env": "b"},
			"env: wants an object"},
		{"a name where a free-form block belongs", map[string]any{"custom": "b"},
			"custom: wants an object"},
		{"a name where a list of objects belongs", map[string]any{"hooks": "b"},
			"hooks: wants an object or a list of objects"},
		{"an object inside a list of names", map[string]any{"tags": []any{map[string]any{}}},
			"tags[0]: wants a string"},
		{"a name where a number belongs", map[string]any{"concurrency": []any{"x"}},
			"concurrency[0]: wants a number"},
		{"an object where a number belongs", map[string]any{"hooks": []any{map[string]any{"retries": []any{}}}},
			"hooks[0].retries: wants a number"},
		{"an object where a flag belongs", map[string]any{"quiet": []any{}},
			"quiet: wants true or false"},
		{"a list where a value belongs", map[string]any{"env": map[string]any{"A": []any{}}},
			"env.A: wants a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeApp(tc.src)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestDecodeAFreeFormBlockStopsTheRefusals: a free-form block's contents are
// the file author's own, so nothing inside it is a key the model has to know
// and nothing inside it can collide. The refusals stop at its edge by
// construction rather than by an exemption someone has to maintain.
func TestDecodeAFreeFormBlockStopsTheRefusals(t *testing.T) {
	inner := map[string]any{"anything": 1, "A": 2, "a": 3}
	cfg, err := decodeApp(map[string]any{"custom": map[string]any{"owners": inner}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(map[string]any{"owners": inner}, cfg.Custom) {
		t.Errorf("custom = %#v", cfg.Custom)
	}
	// It is a copy at its own level, so the caller's map is not the tree's.
	cfg.Custom["added"] = true
}

// TestDecodeAnEntryWithNoBodyIsStillAnEntry: the file named it, and naming it
// is what puts it in the map.
func TestDecodeAnEntryWithNoBodyIsStillAnEntry(t *testing.T) {
	cfg, err := decodeApp(map[string]any{
		"areas": map[string]any{"libs": nil},
		"hooks": []any{nil},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := cfg.Areas["libs"]; !ok {
		t.Errorf("areas = %#v", cfg.Areas)
	}
	if len(cfg.Hooks) != 1 {
		t.Errorf("hooks = %#v", cfg.Hooks)
	}
}

// TestMergeFoldsASquashedTable: squashing means the embedded struct's keys are
// written at the enclosing object's level, with no key of their own.
func TestMergeFoldsASquashedTable(t *testing.T) {
	type outer struct {
		Name  string
		Inner flowConfig
	}
	var dst outer
	table := Merge(Fields{"name": String(&dst.Name)}, flowFields(&dst.Inner))
	if err := DecodeObject(map[string]any{"name": "n", "build": []any{"b"}}, "", table); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dst.Name != "n" || !reflect.DeepEqual([]string{"b"}, dst.Inner.Build) {
		t.Errorf("dst = %#v", dst)
	}
}

// TestKeyPathAndIndexPath: the root object has no path of its own, so its keys
// are named by themselves.
func TestKeyPathAndIndexPath(t *testing.T) {
	if got := KeyPath("", "a"); got != "a" {
		t.Errorf("KeyPath = %q", got)
	}
	if got := KeyPath("a", "b"); got != "a.b" {
		t.Errorf("KeyPath = %q", got)
	}
	if got := IndexPath("a", 2); got != "a[2]" {
		t.Errorf("IndexPath = %q", got)
	}
}

// TestDecodeObjectNeedsAnObject: the value has to be an object before any of
// the object rules can apply, and the root is named for what it is.
func TestDecodeObjectNeedsAnObject(t *testing.T) {
	err := DecodeObject([]any{"a"}, "", Fields{})
	if err == nil || err.Error() != ": wants an object" {
		t.Fatalf("err = %v", err)
	}
	err = DecodeObject("x", "flow", Fields{})
	if err == nil || err.Error() != "flow: wants an object" {
		t.Fatalf("err = %v", err)
	}
}
