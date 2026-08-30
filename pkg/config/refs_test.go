package config

// The loader every config file arrives through: which formats it reads, what
// it makes of a file that says nothing, and everything a `$ref` can do or get
// wrong.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestReadTreeFormats: the three formats all parse into the same tree, with
// their keys spelled as the file wrote them.
func TestReadTreeFormats(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"app.json", `{"env": {"MiXed": "v"}}`},
		{"app.yaml", "env:\n  MiXed: v\n"},
		{"app.yml", "env:\n  MiXed: v\n"},
		{"app.toml", "[env]\nMiXed = \"v\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := readTree(t, writeFile(t, dir, tc.name, tc.body))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			want := map[string]any{"env": map[string]any{"MiXed": "v"}}
			if !reflect.DeepEqual(want, tree.Root) {
				t.Errorf("root = %#v, want %#v", tree.Root, want)
			}
		})
	}
}

// TestReadTreeExtensionIsMatchedCaseInsensitively: the extension names the
// parser, and a file shouting its name is the same file.
func TestReadTreeExtensionIsMatchedCaseInsensitively(t *testing.T) {
	tree, err := readTree(t, writeFile(t, t.TempDir(), "APP.JSON", `{"name": "v"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if tree.Root["name"] != "v" {
		t.Errorf("root = %#v", tree.Root)
	}
}

// TestReadTreeUnknownFormatIsRefused: the extension names the parser, so a
// file in a format the table has none for is refused by name rather than
// guessed at.
func TestReadTreeUnknownFormatIsRefused(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.ini", "[env]\nMiXed = v\n")

	_, err := readTree(t, path)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	var fe *FileError
	if !errors.As(err, &fe) || fe.Path != path {
		t.Fatalf("err = %v, want a *FileError naming %s", err, path)
	}
	if want := "cannot read " + path; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
}

// TestReadTreeFallbackFormatClaimsTheRest: an entry under no extension at all
// takes every file the others leave, which is where a program puts its own
// wording for a file it has no parser for — or a format it reads by default.
func TestReadTreeFallbackFormatClaimsTheRest(t *testing.T) {
	dir := t.TempDir()
	mine := errors.New("this program reads json and yaml config files")
	formats := DefaultFormats()
	formats[""] = func([]byte) (any, error) { return nil, mine }
	l := NewLoader(Options{Formats: formats})

	_, err := l.ReadTree(t.Context(), writeFile(t, dir, "app.ini", "x = 1\n"))
	if !errors.Is(err, mine) {
		t.Fatalf("err = %v, want the caller's own error", err)
	}
	if errors.Is(err, ErrUnsupportedFormat) {
		t.Error("the fallback took the file, so the library's own sentinel has no business here")
	}
	// The claimed extensions still go to their own parsers.
	tree, err := l.ReadTree(t.Context(), writeFile(t, dir, "app.json", `{"name": "v"}`))
	if err != nil || tree.Root["name"] != "v" {
		t.Fatalf("tree = %#v, err = %v", tree, err)
	}
}

// TestReadTreeReadFileIsTheOneDoor: every path the package opens goes through
// Options.ReadFile, so a configuration served from somewhere other than the
// filesystem is served whole, references included.
func TestReadTreeReadFileIsTheOneDoor(t *testing.T) {
	files := map[string]string{
		"/virtual/app.json":  `{"flow": {"$ref": "./flow.json"}}`,
		"/virtual/flow.json": `{"build": ["one"]}`,
	}
	var read []string
	l := NewLoader(Options{ReadFile: func(path string) ([]byte, error) {
		read = append(read, path)
		body, ok := files[filepath.ToSlash(path)]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(body), nil
	}})

	tree, err := l.ReadTree(t.Context(), "/virtual/app.json")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := map[string]any{"flow": map[string]any{"build": []any{"one"}}}
	if !reflect.DeepEqual(want, tree.Root) {
		t.Errorf("root = %#v, want %#v", tree.Root, want)
	}
	if len(read) != 2 {
		t.Errorf("read %v, want both files through the hook", read)
	}
}

// TestReadTreeMalformedFileIsReported: a file the parser chokes on names
// itself, because "cannot read this file" is the first thing to know and the
// parser's own message follows it.
func TestReadTreeMalformedFileIsReported(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"app.json", "{not json"},
		{"app.yaml", "a:\n b: [\n"},
		{"app.toml", "a = = 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.name, tc.body)
			_, err := readTree(t, path)
			if err == nil {
				t.Fatal("want an error")
			}
			if want := "cannot read " + path; !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to contain %q", err, want)
			}
		})
	}
}

// TestReadTreeMissingFileIsReported: a file that is not there is the same
// answer as one that cannot be parsed, and it carries the reason.
func TestReadTreeMissingFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, err := readTree(t, path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want it to unwrap to os.ErrNotExist", err)
	}
}

// TestReadTreeEmptyFileIsAnEmptyObject: an empty file is a config that says
// nothing, which the caller's own validation answers far better than the
// parser could.
func TestReadTreeEmptyFileIsAnEmptyObject(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"app.yaml", ""},
		{"app.json", "null"},
		{"app.toml", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := readTree(t, writeFile(t, t.TempDir(), tc.name, tc.body))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if len(tree.Root) != 0 {
				t.Errorf("root = %#v, want empty", tree.Root)
			}
		})
	}
}

// TestReadTreeTopLevelMustBeAnObject: a config file is an object of keys. A
// document that is a list has no key to read, and saying so beats an
// unknown-key error about a key nobody wrote.
func TestReadTreeTopLevelMustBeAnObject(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.yaml", "- one\n- two\n")

	_, err := readTree(t, path)
	if err == nil || !strings.Contains(err.Error(), "the top level is not an object") {
		t.Fatalf("err = %v", err)
	}
}

// TestReadTreeRenamesNothing: the tree carries every key exactly as the file
// wrote it, all the way down and inside lists too.
func TestReadTreeRenamesNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.json", `{
		"Areas": {"Libs": {
			"Env": {"MiXed": "v"},
			"Areas": {"Inner": {"Path": "p"}}
		}},
		"Hooks": [{"URL": "u"}]
	}`)

	tree, err := readTree(t, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := map[string]any{
		"Areas": map[string]any{"Libs": map[string]any{
			"Env":   map[string]any{"MiXed": "v"},
			"Areas": map[string]any{"Inner": map[string]any{"Path": "p"}},
		}},
		"Hooks": []any{map[string]any{"URL": "u"}},
	}
	if !reflect.DeepEqual(want, tree.Root) {
		t.Errorf("root = %#v, want %#v", tree.Root, want)
	}
}

// TestReadTreeConvertsGenericMaps: the decode meets one kind of map, so a yaml
// mapping with a non-string key becomes a string-keyed one on the way, its
// keys rendered the way a value would be. The conversion happens in the walk
// that resolves the references, which is why a reference inside such a mapping
// is followed like any other.
func TestReadTreeConvertsGenericMaps(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "inner.json", `{"deep": "v"}`)
	path := writeFile(t, dir, "app.yaml", strings.Join([]string{
		"generic:",
		"  MiXed: v",
		"  1: one",
		"  true: yes",
		"  3.5: fraction",
		"nestedref:",
		"  2:",
		"    $ref: ./inner.json",
		"",
	}, "\n"))

	tree, err := readTree(t, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	generic, _ := tree.Root["generic"].(map[string]any)
	want := map[string]any{"MiXed": "v", "1": "one", "true": "yes", "3.5": "fraction"}
	if !reflect.DeepEqual(want, generic) {
		t.Errorf("generic = %#v, want %#v", generic, want)
	}
	wantRef := map[string]any{"2": map[string]any{"deep": "v"}}
	if !reflect.DeepEqual(wantRef, tree.Root["nestedref"]) {
		t.Errorf("nestedref = %#v, want %#v", tree.Root["nestedref"], wantRef)
	}
}

// TestStringKeyedSettlesCollisionsTheSameWay: two keys of a generic mapping
// that render alike are one key afterwards, and which of them survives is
// fixed rather than left to the map iteration that happened to run. No parser
// produces the pair — yaml refuses the document — so the conversion is asked
// directly.
func TestStringKeyedSettlesCollisionsTheSameWay(t *testing.T) {
	in := map[any]any{1: "number", "1": "text", "keep": "kept"}
	first := stringKeyed(in)
	if first["keep"] != "kept" {
		t.Errorf("converted = %#v", first)
	}
	for i := 0; i < 50; i++ {
		if got := stringKeyed(in)["1"]; got != first["1"] {
			t.Fatalf("run %d gave %#v where the first gave %#v", i, got, first["1"])
		}
	}
}

// TestReadTreeGenericRootIsAnObject: a yaml document whose top-level mapping
// has a non-string key is an object like any other, its keys rendered the way
// a value would be. The conversion happens in the walk, before the top level
// is asked what it is, so there is one answer to "is this an object" rather
// than one for the root and another for everything below it.
func TestReadTreeGenericRootIsAnObject(t *testing.T) {
	tree, err := readTree(t, writeFile(t, t.TempDir(), "app.yaml", "1: one\ntwo: 2\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := map[string]any{"1": "one", "two": 2}
	if !reflect.DeepEqual(want, tree.Root) {
		t.Errorf("root = %#v, want %#v", tree.Root, want)
	}
}

// TestReadTreeDoesNotShareWithTheParser: the tree is built by the walk rather
// than handed over by the parser, so writing into it reaches nothing else, and
// the settings it renders leave it alone in turn.
func TestReadTreeDoesNotShareWithTheParser(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.json", `{"env": {"MiXed": "v"}, "flow": {"build": ["one"]}}`)

	tree, err := readTree(t, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := tree.Settings(nil, Overrides{"logLevel": "warn"})
	out["env"].(map[string]any)["MiXed"] = "changed"
	if got := tree.Root["env"].(map[string]any)["MiXed"]; got != "v" {
		t.Errorf("the tree was written through: %q", got)
	}
	if _, added := tree.Root["logLevel"]; added {
		t.Error("the overrides reached the tree")
	}
}

// TestRefReplacesTheValue: the whole point, at three levels of nesting and in
// every position a value can be. A referenced document becomes the value
// wholesale, whether it is an object, a list or a single value.
func TestRefReplacesTheValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "areas.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "flow.yaml", "build:\n  - build\n")
	writeFile(t, dir, "shell.json", `["/bin/bash", "-c"]`)
	writeFile(t, dir, "name.yaml", "'the-name'\n")
	path := writeFile(t, dir, "app.json", `{
		"areas": {"$ref": "./areas.json"},
		"shell": {"$ref": "./shell.json"},
		"name": {"$ref": "./name.yaml"}
	}`)

	cfg, tree, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Areas["libs"].Path; !reflect.DeepEqual([]string{"pkgs"}, got) {
		t.Errorf("path = %#v", got)
	}
	if got := cfg.Areas["libs"].Flow.Build; !reflect.DeepEqual([]string{"build"}, got) {
		t.Errorf("an object through two files: %#v", got)
	}
	if !reflect.DeepEqual([]string{"/bin/bash", "-c"}, cfg.Shell) {
		t.Errorf("a referenced list: %#v", cfg.Shell)
	}
	if cfg.Name != "the-name" {
		t.Errorf("a referenced single value: %q", cfg.Name)
	}
	want := []string{
		path,
		filepath.Join(dir, "areas.json"),
		filepath.Join(dir, "flow.yaml"),
		filepath.Join(dir, "name.yaml"),
		filepath.Join(dir, "shell.json"),
	}
	if !reflect.DeepEqual(want, tree.Files) {
		t.Errorf("files = %#v, want %#v", tree.Files, want)
	}
}

// TestRefLoadsTheSameConfigurationAsInline: a configuration split across files
// is the configuration it was split out of, which is the claim that matters
// more than any single key.
func TestRefLoadsTheSameConfigurationAsInline(t *testing.T) {
	whole := appConfig{
		Name:  "app",
		Env:   map[string]string{"MiXed": "v"},
		Areas: map[string]areaConfig{"libs": {Path: []string{"pkgs"}}},
	}
	inlineDir := t.TempDir()
	inline, _, err := loadApp(t, writeJSON(t, inlineDir, "app.json", whole), nil)
	if err != nil {
		t.Fatalf("inline: %v", err)
	}

	dir := t.TempDir()
	writeJSON(t, dir, "areas.json", whole.Areas)
	writeFile(t, dir, "env.yaml", "MiXed: v\n")
	split := writeFile(t, dir, "app.json", `{
		"name": "app",
		"areas": {"$ref": "./areas.json"},
		"env": {"$ref": "./env.yaml"}
	}`)

	loaded, _, err := loadApp(t, split, nil)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if !reflect.DeepEqual(inline, loaded) {
		t.Errorf("split = %#v, want the inline %#v", loaded, inline)
	}
}

// TestRefResolvesAgainstTheFileThatWroteIt: a relative reference is relative
// to its own file, not to the root, which is what lets a folder of fragments
// be moved as a folder. Proven with the same file name sitting in both places,
// holding different things.
func TestRefResolvesAgainstTheFileThatWroteIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.yaml", "build:\n  - wrong\n")
	writeFile(t, dir, "cfg/flow.yaml", "build:\n  - right\n")
	writeFile(t, dir, "cfg/areas.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./cfg/areas.json"}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Areas["libs"].Flow.Build; !reflect.DeepEqual([]string{"right"}, got) {
		t.Errorf("build = %#v", got)
	}
}

// TestRefAbsolutePath: an absolute path is used as written, for the fragment
// that lives outside the repository.
func TestRefAbsolutePath(t *testing.T) {
	shared := writeFile(t, t.TempDir(), "flow.json", `{"build": ["shared"]}`)
	path := writeFile(t, t.TempDir(), "app.json",
		`{"flow": {"$ref": "`+filepath.ToSlash(shared)+`"}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Flow.Build; !reflect.DeepEqual([]string{"shared"}, got) {
		t.Errorf("build = %#v", got)
	}
}

// TestRefSiblingKeysOverride: a reference brings in a base and the keys beside
// it settle the rest, so one shared fragment serves the places that agree with
// it and the places that agree with most of it.
func TestRefSiblingKeysOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "area.yaml", "path: pkgs\nversioning: fixed\n")
	writeFile(t, dir, "independent.json", `"independent"`)
	path := writeFile(t, dir, "app.json", `{
		"areas": {
			"libs": {"$ref": "./area.yaml"},
			"apps": {"$ref": "./area.yaml", "Versioning": {"$ref": "./independent.json"}}
		}
	}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Areas["libs"].Versioning; got != "fixed" {
		t.Errorf("the fragment as it is: %q", got)
	}
	if got := cfg.Areas["apps"].Versioning; got != "independent" {
		t.Errorf("the sibling wins, however the two files spell the key: %q", got)
	}
	if got := cfg.Areas["apps"].Path; !reflect.DeepEqual([]string{"pkgs"}, got) {
		t.Errorf("everything it did not override survives: %#v", got)
	}
}

// TestRefSiblingDecidesTheEntrySpelling: a key beside a reference replaces the
// fragment's, spelling included, and so does a later file of a multi-file
// reference. Leaving both in would hand the decode two keys that fold together
// — refused as a collision — over a config that says one sensible thing.
func TestRefSiblingDecidesTheEntrySpelling(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "env.json", `{"build": "fragment", "test": "kept"}`)
	writeFile(t, dir, "more.json", `{"Test": "later"}`)
	path := writeFile(t, dir, "app.json",
		`{"env": {"$ref": ["./env.json", "./more.json"], "Build": "sibling"}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]string{"Build": "sibling", "Test": "later"}
	if !reflect.DeepEqual(want, cfg.Env) {
		t.Errorf("env = %#v, want the nearer spelling of each name and no other: %#v", cfg.Env, want)
	}
}

// TestRefSiblingsNeedAnObject: keys beside a reference override the object it
// brought in, so a fragment that is not an object leaves them nothing to
// override and the file is refused rather than half-applied.
func TestRefSiblingsNeedAnObject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell.json", `["/bin/bash", "-c"]`)
	writeFile(t, dir, "name.json", `"a-name"`)

	for _, tc := range []struct{ name, body, want string }{
		{"a list", `{"shell": {"$ref": "./shell.json", "extra": 1}}`,
			`$ref "./shell.json": the file is a list, so the keys beside the $ref have nothing to override`},
		{"a single value", `{"name": {"$ref": "./name.json", "extra": 1}}`,
			`$ref "./name.json": the file is a single value, so the keys beside the $ref have nothing to override`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readTree(t, writeFile(t, dir, "app.json", tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestRefTheDocumentItself: a config file may be nothing but a reference,
// which is how a repository keeps its real configuration somewhere other than
// the name the loader looks for.
func TestRefTheDocumentItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/real.yaml", "areas:\n  libs:\n    path: pkgs\n")
	path := writeFile(t, dir, "app.json", `{"$ref": "./cfg/real.yaml"}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Areas["libs"].Path; !reflect.DeepEqual([]string{"pkgs"}, got) {
		t.Errorf("path = %#v", got)
	}
}

// TestRefInsideAFreeFormBlock: a free-form block is data the program never
// reads, and a reference means there what it means everywhere else. One rule,
// no exception to remember.
func TestRefInsideAFreeFormBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "team.json", `{"team": "platform"}`)
	path := writeFile(t, dir, "app.json", `{"custom": {"owners": {"$ref": "./team.json"}}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]any{"owners": map[string]any{"team": "platform"}}
	if !reflect.DeepEqual(want, cfg.Custom) {
		t.Errorf("custom = %#v, want %#v", cfg.Custom, want)
	}
}

// TestRefTheSameFileTwiceIsNotACycle: a fragment used in two places is read
// twice and lands in both, because only a file already being read is a cycle.
func TestRefTheSameFileTwiceIsNotACycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.yaml", "build:\n  - build\n")
	path := writeFile(t, dir, "app.json", `{
		"areas": {
			"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}},
			"apps": {"path": "apps", "flow": {"$ref": "./flow.yaml"}}
		}
	}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"libs", "apps"} {
		if got := cfg.Areas[name].Flow.Build; !reflect.DeepEqual([]string{"build"}, got) {
			t.Errorf("%s build = %#v", name, got)
		}
	}
}

// TestRefCycles: a file that reaches itself is refused with the way it got
// there, however many files the loop runs through.
func TestRefCycles(t *testing.T) {
	t.Run("a file referencing itself", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./app.json"}}`)

		_, err := readTree(t, path)
		if !errors.Is(err, ErrRefCycle) {
			t.Fatalf("err = %v, want ErrRefCycle", err)
		}
		want := "$ref cycle: " + path + " (areas) -> " + path +
			"; a file cannot reference itself, directly or through another"
		if err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
	})

	t.Run("through two more files", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "a.json", `{"libs": {"$ref": "./b.json"}}`)
		writeFile(t, dir, "b.json", `{"path": "pkgs", "flow": {"$ref": "./app.json"}}`)
		path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./a.json"}}`)

		_, err := readTree(t, path)
		want := "$ref cycle: " + path + " (areas) -> " + filepath.Join(dir, "a.json") +
			" (libs) -> " + filepath.Join(dir, "b.json") + " (flow) -> " + path
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to contain %q", err, want)
		}
	})

	t.Run("two names for one file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./cfg/../app.json"}}`)

		_, err := readTree(t, path)
		if !errors.Is(err, ErrRefCycle) {
			t.Fatalf("err = %v, want ErrRefCycle", err)
		}
	})

	t.Run("through a folder symlink", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./self/app.json"}}`)
		if err := os.Symlink(dir, filepath.Join(dir, "self")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		// Every hop names the same file by one more "self/", so no two cleaned
		// paths on the chain are equal and the chain check can never fire. The
		// depth cap is what this case is for; it is lowered here because the
		// operating system refuses a path of many symlinks first.
		_, err := NewLoader(Options{MaxRefDepth: 4}).ReadTree(t.Context(), path)
		if !errors.Is(err, ErrRefDepth) {
			t.Fatalf("err = %v, want ErrRefDepth", err)
		}
	})
}

// TestRefNestingIsCapped: a chain nobody wrote on purpose stops at a depth
// rather than at a stack overflow, and one file short of the cap still loads.
func TestRefNestingIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= DefaultMaxRefDepth+1; i++ {
		writeFile(t, dir, fmt.Sprintf("f%d.json", i),
			fmt.Sprintf(`{"next": {"$ref": "./f%d.json"}}`, i+1))
	}
	writeFile(t, dir, fmt.Sprintf("f%d.json", DefaultMaxRefDepth+2), `{"deep": "bottom"}`)
	path := writeFile(t, dir, "app.json", `{"custom": {"$ref": "./f0.json"}}`)

	_, err := readTree(t, path)
	if !errors.Is(err, ErrRefDepth) {
		t.Fatalf("err = %v, want ErrRefDepth", err)
	}
	want := fmt.Sprintf("$ref nesting is more than %d files deep", DefaultMaxRefDepth)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
	var chain *RefChainError
	if !errors.As(err, &chain) || len(chain.Chain) != DefaultMaxRefDepth+2 {
		t.Fatalf("chain = %#v, want every hop named", chain)
	}

	// A chain of exactly the cap loads, which is what makes the cap a cap: the
	// document's own reference is the first of the references counted.
	deep := t.TempDir()
	for i := 0; i < DefaultMaxRefDepth-1; i++ {
		writeFile(t, deep, fmt.Sprintf("g%d.json", i),
			fmt.Sprintf(`{"next": {"$ref": "./g%d.json"}}`, i+1))
	}
	writeFile(t, deep, fmt.Sprintf("g%d.json", DefaultMaxRefDepth-1), `{"deep": "bottom"}`)
	if _, err := readTree(t, writeFile(t, deep, "app.json", `{"custom": {"$ref": "./g0.json"}}`)); err != nil {
		t.Fatalf("a chain of exactly the cap: %v", err)
	}
}

// TestRefDepthIsConfigurable: the cap is an option, and a caller that wants no
// references at all sets it below zero.
func TestRefDepthIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$ref": "./one.json"}}`)

	l := NewLoader(Options{MaxRefDepth: -1})
	if _, err := l.ReadTree(t.Context(), path); !errors.Is(err, ErrRefDepth) {
		t.Fatalf("err = %v, want ErrRefDepth", err)
	}
	if _, err := NewLoader(Options{MaxRefDepth: 1}).ReadTree(t.Context(), path); err != nil {
		t.Fatalf("one hop within a cap of one: %v", err)
	}
}

// TestRefKeyIsConfigurable: the key that makes an object a reference is the
// caller's to name, and it is matched exactly so that one spelling is the
// reference and every other is a key.
func TestRefKeyIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$include": "./one.json"}}`)
	l := NewLoader(Options{RefKey: "$include"})

	tree, err := l.ReadTree(t.Context(), path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := map[string]any{"flow": map[string]any{"build": []any{"one"}}}
	if !reflect.DeepEqual(want, tree.Root) {
		t.Errorf("root = %#v, want %#v", tree.Root, want)
	}
	// The default key is nothing but a key to this loader.
	other := writeFile(t, dir, "other.json", `{"flow": {"$ref": "./one.json"}}`)
	tree, err = l.ReadTree(t.Context(), other)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := tree.Root["flow"].(map[string]any)["$ref"]; got != "./one.json" {
		t.Errorf("$ref = %#v, want the string the file wrote", got)
	}
}

// TestRefFailures: everything a reference can get wrong, each named where it
// was written and with what it pointed at.
func TestRefFailures(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body, want string
		is               error
	}{
		{name: "missing file", body: `{"env": {"$ref": "./absent.json"}}`,
			want: `env: $ref "./absent.json": cannot read`, is: os.ErrNotExist},
		{name: "unreadable format", body: `{"env": {"$ref": "./env.ini"}}`,
			want: "unsupported config file format", is: ErrUnsupportedFormat},
		{name: "empty file", body: `{"env": {"$ref": "./empty.yaml"}}`,
			want: `env: $ref "./empty.yaml": the file is empty, so the key would have no value`},
		{name: "not a string", body: `{"env": {"$ref": 7}}`,
			want: "env: $ref must name another config file", is: ErrRefTarget},
		{name: "empty string", body: `{"env": {"$ref": "  "}}`,
			want: "env: $ref must name another config file", is: ErrRefTarget},
		{name: "deep inside a list", body: `{"hooks": [{"$ref": "./absent.json"}]}`,
			want: `hooks[0]: $ref "./absent.json": cannot read`},
		{name: "the document itself", body: `{"$ref": "./absent.json"}`,
			want: `the document: $ref "./absent.json": cannot read`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, dir, "env.ini", "A = 1\n")
			writeFile(t, dir, "empty.yaml", "")
			path := writeFile(t, dir, "app.json", tc.body)

			_, err := readTree(t, path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("err = %v, want it to unwrap to %v", err, tc.is)
			}
		})
	}
}

// TestRefListMergesObjects: a reference may name several files, which are read
// in order and merged key by key. The last file to write a key wins it, in its
// own spelling, so a shared fragment can be adjusted by the ones after it.
func TestRefListMergesObjects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", "MiXed: base\nKept: base\n")
	writeFile(t, dir, "extra.json", `{"mixed": "extra", "Added": "extra"}`)
	path := writeFile(t, dir, "app.json", `{"env": {"$ref": ["./base.yaml", "./extra.json"]}}`)

	cfg, tree, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]string{"mixed": "extra", "Kept": "base", "Added": "extra"}
	if !reflect.DeepEqual(want, cfg.Env) {
		t.Errorf("env = %#v, want %#v", cfg.Env, want)
	}
	wantFiles := []string{path, filepath.Join(dir, "base.yaml"), filepath.Join(dir, "extra.json")}
	if !reflect.DeepEqual(wantFiles, tree.Files) {
		t.Errorf("files = %#v, want %#v", tree.Files, wantFiles)
	}
}

// TestRefListConcatenatesLists: files that hold lists are added one after
// another, which is what lets a common block be extended in place rather than
// copied.
func TestRefListConcatenatesLists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell.json", `["/bin/bash"]`)
	writeFile(t, dir, "flags.yaml", "- -e\n- -c\n")
	path := writeFile(t, dir, "app.json", `{"shell": {"$ref": ["./shell.json", "./flags.yaml"]}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := []string{"/bin/bash", "-e", "-c"}; !reflect.DeepEqual(want, cfg.Shell) {
		t.Errorf("shell = %#v, want %#v in the order the files are named", cfg.Shell, want)
	}
}

// TestRefListOfOneIsTheSameReference: a list holding one file is the reference
// naming that file, written the long way. Refusing it would make the list form
// a trap for anything that generates configuration.
func TestRefListOfOneIsTheSameReference(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$ref": ["./flow.json"]}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Flow.Build; !reflect.DeepEqual([]string{"one"}, got) {
		t.Errorf("build = %#v", got)
	}
}

// TestRefListMixedKindsAreRefused: merging asks the files to agree on what
// they hold. Two kinds of answer have no merge between them, and neither has a
// single value with anything, so the files are named rather than guessed at.
func TestRefListMixedKindsAreRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "object.json", `{"build": ["b"]}`)
	writeFile(t, dir, "list.json", `["build"]`)
	writeFile(t, dir, "single.json", `"one"`)

	for _, tc := range []struct{ name, body, want string }{
		{"an object then a list", `{"flow": {"$ref": ["./object.json", "./list.json"]}}`,
			`$ref: "./object.json" holds an object and "./list.json" holds a list`},
		{"a list then an object", `{"shell": {"$ref": ["./list.json", "./object.json"]}}`,
			`$ref: "./list.json" holds a list and "./object.json" holds an object`},
		{"a single value", `{"flow": {"$ref": ["./single.json", "./object.json"]}}`,
			`$ref: "./single.json" holds a single value and "./object.json" holds an object`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readTree(t, writeFile(t, dir, "app.json", tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "the files of one $ref must all hold objects, or all hold lists") {
				t.Errorf("err = %q, want the rule stated", err)
			}
		})
	}
}

// TestRefListSiblingKeysOverrideTheMerge: the keys beside a reference are the
// nearest layer of all, so they settle what the files merged to. They still
// need an object to override, and a merge that produced a list says so.
func TestRefListSiblingKeysOverrideTheMerge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", "path: pkgs\nversioning: fixed\n")
	writeFile(t, dir, "extra.json", `{"versioning": "independent"}`)
	writeFile(t, dir, "one.json", `["/bin/bash"]`)
	writeFile(t, dir, "two.json", `["-c"]`)

	path := writeFile(t, dir, "app.json",
		`{"areas": {"libs": {"$ref": ["./base.yaml", "./extra.json"], "Versioning": "fixed"}}}`)
	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Areas["libs"].Versioning; got != "fixed" {
		t.Errorf("the sibling outranks every file the $ref named: %q", got)
	}
	if got := cfg.Areas["libs"].Path; !reflect.DeepEqual([]string{"pkgs"}, got) {
		t.Errorf("everything it did not override survives the merge: %#v", got)
	}

	list := writeFile(t, dir, "list.json", `{"shell": {"$ref": ["./one.json", "./two.json"], "extra": 1}}`)
	_, err = readTree(t, list)
	want := "$ref: the files merge to a list, so the keys beside the $ref have nothing to override"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// TestRefListFailures: what a list of files can get wrong, each named where it
// was written. A file the list names is read exactly as a single file is, so
// its own refusals arrive unchanged.
func TestRefListFailures(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, want string }{
		{"no files at all", `{"env": {"$ref": []}}`,
			"env: $ref names no files: the list must hold at least one path"},
		{"a file that is not named", `{"env": {"$ref": ["./env.yaml", 7]}}`,
			"env: $ref[1] must name another config file"},
		{"a name that is blank", `{"env": {"$ref": ["./env.yaml", "  "]}}`,
			"env: $ref[1] must name another config file"},
		{"neither a file nor a list", `{"env": {"$ref": 7}}`,
			"env: $ref must name another config file, or a list of them"},
		{"a file that is missing", `{"env": {"$ref": ["./env.yaml", "./absent.json"]}}`,
			`env: $ref "./absent.json": cannot read`},
		{"a file that is empty", `{"env": {"$ref": ["./env.yaml", "./empty.yaml"]}}`,
			`env: $ref "./empty.yaml": the file is empty, so the key would have no value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, dir, "env.yaml", "MiXed: v\n")
			writeFile(t, dir, "empty.yaml", "")
			path := writeFile(t, dir, "app.json", tc.body)

			_, err := readTree(t, path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestRefListCycleNamesThePathThatClosedIt: every file a reference names is
// followed on its own, so a cycle through the second of them is the cycle it
// is, reported by the way it got there.
func TestRefListCycleNamesThePathThatClosedIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "first.yaml", "MiXed: v\n")
	writeFile(t, dir, "second.json", `{"deeper": {"$ref": "./app.json"}}`)
	path := writeFile(t, dir, "app.json", `{"env": {"$ref": ["./first.yaml", "./second.json"]}}`)

	_, err := readTree(t, path)
	want := "$ref cycle: " + path + " (env) -> " + filepath.Join(dir, "second.json") + " (deeper) -> " + path
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// TestRefListMergesFilesThatSplitFurther: a file a list names may be split
// itself. Each is resolved whole before it is merged, so a fragment's own
// references are followed against its own folder and a shared sub-fragment
// used by two of them is read for each, as it is anywhere else.
func TestRefListMergesFilesThatSplitFurther(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/base.json", `{"libs": {"path": "pkgs", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "cfg/extra.json", `{"apps": {"path": "apps", "flow": {"$ref": "./flow.yaml"}}}`)
	writeFile(t, dir, "cfg/flow.yaml", "build:\n  - build\n")
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": ["./cfg/base.json", "./cfg/extra.json"]}}`)

	cfg, tree, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"libs", "apps"} {
		if got := cfg.Areas[name].Flow.Build; !reflect.DeepEqual([]string{"build"}, got) {
			t.Errorf("%s build = %#v", name, got)
		}
	}
	want := []string{
		path,
		filepath.Join(dir, "cfg", "base.json"),
		filepath.Join(dir, "cfg", "flow.yaml"),
		filepath.Join(dir, "cfg", "extra.json"),
		filepath.Join(dir, "cfg", "flow.yaml"),
	}
	if !reflect.DeepEqual(want, tree.Files) {
		t.Errorf("files = %#v, want %#v", tree.Files, want)
	}
}

// TestRefListResolvesEachAgainstItsOwnFolder: the files of one reference may
// live in different folders, and each is resolved against the file that named
// it, as every reference is.
func TestRefListResolvesEachAgainstItsOwnFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/areas.json", `{"$ref": ["./base.json", "../overlay.json"]}`)
	writeFile(t, dir, "cfg/base.json", `{"libs": {"path": "pkgs"}}`)
	writeFile(t, dir, "overlay.json", `{"apps": {"path": "apps"}}`)
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./cfg/areas.json"}}`)

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for name, want := range map[string]string{"libs": "pkgs", "apps": "apps"} {
		if got := cfg.Areas[name].Path; !reflect.DeepEqual([]string{want}, got) {
			t.Errorf("%s path = %#v, want %q", name, got, want)
		}
	}
}

// TestRefEmptyFileInEveryFormat: "this file is empty" is one answer across the
// three formats, even though TOML spells an empty document as an empty table
// and the other two as no value at all.
func TestRefEmptyFileInEveryFormat(t *testing.T) {
	for _, name := range []string{"empty.json", "empty.yaml", "empty.toml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := ""
			if name == "empty.json" {
				body = "null" // an empty .json file is not a document at all
			}
			writeFile(t, dir, name, body)
			path := writeFile(t, dir, "app.json", `{"env": {"$ref": "./`+name+`"}}`)

			_, err := readTree(t, path)
			want := "the file is empty, so the key would have no value"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
		})
	}
}

// TestRefRefusalOfADeeperFileNamesEveryHop: a mistake inside a referenced file
// is reported against that file, because that is where it is written.
func TestRefRefusalOfADeeperFileNamesEveryHop(t *testing.T) {
	dir := t.TempDir()
	inner := writeFile(t, dir, "inner.json", `{"deep": {"$ref": 7}}`)
	path := writeFile(t, dir, "app.json", `{"custom": {"$ref": "./inner.json"}}`)

	_, err := readTree(t, path)
	want := inner + ": deep: $ref must name another config file"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// TestReadTreeNilLoaderIsTheDefaultOne: every entry point takes a nil Loader
// as "the defaults" rather than panicking on it, which is what makes the
// package usable before anyone has decided on options.
func TestReadTreeNilLoaderIsTheDefaultOne(t *testing.T) {
	var l *Loader
	dir := t.TempDir()
	writeFile(t, dir, "flow.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$ref": "./flow.json"}}`)

	tree, err := l.ReadTree(t.Context(), path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := map[string]any{"flow": map[string]any{"build": []any{"one"}}}
	if !reflect.DeepEqual(want, tree.Root) {
		t.Errorf("root = %#v, want %#v", tree.Root, want)
	}
}

// TestConcurrentReadsAreIndependent: a Loader holds no state between calls, so
// two goroutines reading two trees never meet. Meant for -race.
func TestConcurrentReadsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flow.json", `{"build": ["one"]}`)
	path := writeFile(t, dir, "app.json", `{"flow": {"$ref": "./flow.json"}, "name": "app"}`)
	l := NewLoader(Options{})

	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			tree, err := l.ReadTree(t.Context(), path)
			if err == nil {
				tree.Settings(l, Overrides{"logLevel": "warn"})
			}
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Errorf("read: %v", err)
		}
	}
}
