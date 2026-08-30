package config

// The write-back contract: only the named key changes, every other byte
// survives where the format allows it, the previous file is copied to .backup
// first, and a key that lives behind a `$ref` is written where it lives.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func applyEdits(t *testing.T, path string, edits ...Edit) error {
	t.Helper()
	return ApplyEdits(t.Context(), path, edits)
}

func resolveEdit(t *testing.T, path string, keyPath []string) (string, []string, error) {
	t.Helper()
	return NewLoader(Options{}).ResolveEdit(t.Context(), path, keyPath)
}

// TestApplyEditsJSONPreservesEverythingElse: the splice is byte-precise
// outside the replaced span, decoys at other levels included.
func TestApplyEditsJSONPreservesEverythingElse(t *testing.T) {
	// Odd but valid: 4-space indent, unsorted keys, and a nested key of the
	// same name the splice must not touch.
	src := `{
    "areas": {"libs": {"path": "pkgs"}},
    "tags": [
        "old"
    ],
    "custom": {"tags": "decoy", "team": "platform"}
}
`
	path := writeFile(t, t.TempDir(), "app.json", src)
	if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"one", "two"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := readBack(t, path)
	for _, want := range []string{
		`"areas": {"libs": {"path": "pkgs"}},`,
		`"custom": {"tags": "decoy", "team": "platform"}`,
		`"one"`, `"two"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"old"`) {
		t.Errorf("the old value survived:\n%s", got)
	}
	if backup := readBack(t, path+BackupSuffix); backup != src {
		t.Errorf("backup =\n%s\nwant the previous bytes", backup)
	}
	// The rewritten file is still a loadable config.
	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg.Tags) != 2 {
		t.Errorf("tags = %#v", cfg.Tags)
	}
}

// TestApplyEditsJSONNestedPath: a nested key is spliced exactly, and the key
// of the same name at the root is left alone.
func TestApplyEditsJSONNestedPath(t *testing.T) {
	src := `{
    "areas": {"libs": {"versioning": "fixed", "path": ["old", "kept"]}},
    "tags": ["root"]
}
`
	path := writeFile(t, t.TempDir(), "app.json", src)
	if err := applyEdits(t, path,
		Edit{KeyPath: []string{"areas", "libs", "path"}, Value: []string{"kept"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := readBack(t, path)
	if strings.Contains(got, `"old"`) || !strings.Contains(got, `"versioning": "fixed"`) {
		t.Errorf("result:\n%s", got)
	}
	if !strings.Contains(got, `"tags": ["root"]`) {
		t.Errorf("the root key was touched:\n%s", got)
	}

	// Emptying a list keeps the key as [] rather than null: a key holding null
	// no longer says "there is nothing here", it says nothing at all.
	if err := applyEdits(t, path,
		Edit{KeyPath: []string{"areas", "libs", "path"}, Value: []string(nil)}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readBack(t, path)), &doc); err != nil {
		t.Fatalf("the result is not valid JSON: %v", err)
	}
	entry := doc["areas"].(map[string]any)["libs"].(map[string]any)
	if got, ok := entry["path"].([]any); !ok || len(got) != 0 {
		t.Errorf("path = %#v, want an empty list", entry["path"])
	}
}

// TestApplyEditsCreatesOnlyATopLevelKey: a nested path is only ever edited,
// and its absence is a caller bug rather than a key to invent.
func TestApplyEditsCreatesOnlyATopLevelKey(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"app.json", `{"areas": {"libs": {}}}`},
		{"app.yaml", "areas:\n  libs: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.name, tc.body)
			err := applyEdits(t, path,
				Edit{KeyPath: []string{"areas", "apps", "path"}, Value: []string{"x"}})
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("err = %v", err)
			}
			if _, statErr := os.Stat(path + BackupSuffix); !os.IsNotExist(statErr) {
				t.Error("a failed edit wrote something")
			}
		})
	}
}

// TestApplyEditsAppendsAMissingTopLevelKey: a top-level key that is not there
// becomes the object's last member, indented like the rest of the file.
func TestApplyEditsAppendsAMissingTopLevelKey(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "app.json", "{\n  \"name\": \"app\"\n}\n")
		if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"one"}}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		got := readBack(t, path)
		if !strings.Contains(got, `"name": "app",`) || !strings.Contains(got, `"tags": [`) {
			t.Errorf("result:\n%s", got)
		}
	})

	t.Run("json into an empty object", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "app.json", "{}\n")
		if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"one"}}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(readBack(t, path)), &doc); err != nil {
			t.Fatalf("the result is not valid JSON: %v\n%s", err, readBack(t, path))
		}
	})

	t.Run("yaml", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "app.yaml", "name: app\n")
		if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"one"}}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := readBack(t, path); !strings.Contains(got, "tags:") {
			t.Errorf("result:\n%s", got)
		}
	})
}

// TestApplyEditsYAMLKeepsComments: yaml.v3 nodes carry their comments, so the
// rest of the file survives the round trip even though it is re-encoded.
func TestApplyEditsYAMLKeepsComments(t *testing.T) {
	src := `# the whole file
areas:
  # the one area
  libs:
    path: pkgs
tags:
  - old
`
	path := writeFile(t, t.TempDir(), "app.yaml", src)
	if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"new"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := readBack(t, path)
	for _, want := range []string{"# the whole file", "# the one area", "new"} {
		if !strings.Contains(got, want) {
			t.Errorf("result does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Errorf("the old value survived:\n%s", got)
	}
}

// TestApplyEditsYAMLNestedKeyMustBeAMapping: a path that runs through a scalar
// is a path into nothing, and saying so beats writing somewhere unexpected.
func TestApplyEditsYAMLNestedKeyMustBeAMapping(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.yaml", "areas: text\n")
	err := applyEdits(t, path, Edit{KeyPath: []string{"areas", "libs"}, Value: "x"})
	if err == nil || !strings.Contains(err.Error(), `key "areas" is not a mapping`) {
		t.Fatalf("err = %v", err)
	}
}

// TestApplyEditsJSONNestedKeyMustBeAnObject: the same rule on the splicing
// side of the house.
func TestApplyEditsJSONNestedKeyMustBeAnObject(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json", `{"areas": "text"}`)
	err := applyEdits(t, path, Edit{KeyPath: []string{"areas", "libs"}, Value: "x"})
	if err == nil || !strings.Contains(err.Error(), `key "areas" is not an object`) {
		t.Fatalf("err = %v", err)
	}
}

// TestApplyEditsTopLevelMustBeAnObject: there is no key to splice in a
// document that is a list.
func TestApplyEditsTopLevelMustBeAnObject(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"app.json", `["a"]`, "top level is not an object"},
		{"app.yaml", "- a\n", "top level is not a mapping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.name, tc.body)
			err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"x"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// TestApplyEditsTOMLIsRefused: TOML has no format-preserving round trip here,
// so the caller renders a paste-ready snippet instead.
func TestApplyEditsTOMLIsRefused(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.toml", "tags = [\"old\"]\n")
	err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"new"}})
	if !errors.Is(err, ErrTOMLEdit) {
		t.Fatalf("err = %v, want ErrTOMLEdit", err)
	}
	if got := readBack(t, path); !strings.Contains(got, "old") {
		t.Errorf("the file changed:\n%s", got)
	}
}

// TestApplyEditsUnknownFormatIsRefused: the extension names the writer, as it
// names the parser.
func TestApplyEditsUnknownFormatIsRefused(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.ini", "tags = old\n")
	err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"new"}})
	if err == nil || !strings.Contains(err.Error(), "unknown config format") {
		t.Fatalf("err = %v", err)
	}
	err = applyEdits(t, path, Edit{Value: []string{"new"}})
	if err == nil || !strings.Contains(err.Error(), "unknown config format") {
		t.Fatalf("whole-document: err = %v", err)
	}
}

// TestApplyEditsOneWritePerFile: two keys of one file go through one call, so
// the file is read once and one backup is written — a second call would read
// the already-edited file and save that as the backup, so the pre-edit copy
// the user reaches for would be gone.
func TestApplyEditsOneWritePerFile(t *testing.T) {
	src := `{"name": "app", "tags": ["old"], "shell": ["sh"]}`
	path := writeFile(t, t.TempDir(), "app.json", src)
	if err := applyEdits(t, path,
		Edit{KeyPath: []string{"tags"}, Value: []string{"new"}},
		Edit{KeyPath: []string{"shell"}, Value: []string{"bash"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := readBack(t, path)
	if !strings.Contains(got, "new") || !strings.Contains(got, "bash") {
		t.Errorf("result:\n%s", got)
	}
	if backup := readBack(t, path+BackupSuffix); backup != src {
		t.Errorf("backup = %s, want the pre-edit bytes", backup)
	}
}

// TestApplyEditsThatChangeNothingWriteNothing: an edit set whose result equals
// the file leaves the file and its permissions alone, and writes no backup.
func TestApplyEditsThatChangeNothingWriteNothing(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json", "{\n  \"tags\": [\n    \"one\"\n  ]\n}\n")
	before := readBack(t, path)
	if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"one"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := readBack(t, path); got != before {
		t.Errorf("the file changed:\n%s", got)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Error("a no-op edit wrote a backup")
	}

	// No edits at all is the same answer, without even reading the file.
	p, err := PrepareEdits(t.Context(), filepath.Join(t.TempDir(), "absent.json"), nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := p.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestPrepareEditsRendersWithoutWriting: preparing every file before
// committing any is what keeps a multi-file edit from stopping halfway
// through.
func TestPrepareEditsRendersWithoutWriting(t *testing.T) {
	src := `{"tags": ["old"]}`
	path := writeFile(t, t.TempDir(), "app.json", src)

	p, err := PrepareEdits(t.Context(), path, []Edit{{KeyPath: []string{"tags"}, Value: []string{"new"}}})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got := readBack(t, path); got != src {
		t.Errorf("preparing wrote to the file:\n%s", got)
	}
	if p.Path != path {
		t.Errorf("p.Path = %q", p.Path)
	}
	if err := p.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := readBack(t, path); !strings.Contains(got, "new") {
		t.Errorf("result:\n%s", got)
	}
}

// TestCommitKeepsTheFilesPermissions: a 0600 config must not leak through a
// world-readable backup.
func TestCommitKeepsTheFilesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	if err := os.WriteFile(path, []byte(`{"tags": ["old"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyEdits(t, path, Edit{KeyPath: []string{"tags"}, Value: []string{"new"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, p := range []string{path, path + BackupSuffix} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != fs.FileMode(0o600) {
			t.Errorf("%s is %v, want 0600", p, got)
		}
	}
}

// TestPrepareEditsOnAnAbsentFile: there is nothing to splice into.
func TestPrepareEditsOnAnAbsentFile(t *testing.T) {
	_, err := PrepareEdits(t.Context(), filepath.Join(t.TempDir(), "absent.json"),
		[]Edit{{KeyPath: []string{"tags"}, Value: []string{"x"}}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

// TestApplyEditsWritesAWholeDocument: an edit with no key path replaces the
// file's whole content, which is what a key whose value is a `$ref` resolves
// to — the referenced file holds nothing but that value.
func TestApplyEditsWritesAWholeDocument(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"app.json", "[\n  \"old\"\n]\n"},
		{"app.yaml", "- old\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.name, tc.body)
			if err := applyEdits(t, path, Edit{Value: []string{"new"}}); err != nil {
				t.Fatalf("apply: %v", err)
			}
			got := readBack(t, path)
			if !strings.Contains(got, "new") || strings.Contains(got, "old") {
				t.Errorf("result:\n%s", got)
			}
		})
	}

	t.Run("an emptied list is not null", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "app.json", "[\"old\"]\n")
		if err := applyEdits(t, path, Edit{Value: []string(nil)}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := strings.TrimSpace(readBack(t, path)); got != "[]" {
			t.Errorf("result = %q, want []", got)
		}
	})
}

// TestRenderKeyTOML: the paste-ready fallback, nested under its key path.
func TestRenderKeyTOML(t *testing.T) {
	got, err := RenderKeyTOML([]string{"areas", "libs", "path"}, []string{"pkgs"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("the snippet is not valid TOML: %v\n%s", err, got)
	}
	entry := doc["areas"].(map[string]any)["libs"].(map[string]any)
	if want := []any{"pkgs"}; len(entry["path"].([]any)) != len(want) {
		t.Errorf("snippet:\n%s", got)
	}

	// No key path at all is the value on its own.
	if _, err := RenderKeyTOML(nil, map[string]any{"a": 1}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := RenderKeyTOML([]string{"a"}, func() {}); err == nil {
		t.Error("a value TOML cannot render should say so")
	}
}

// TestStringMapAt reads what the file holds, key for key, because a write has
// to start from that rather than from a merged, validated view.
func TestStringMapAt(t *testing.T) {
	dir := t.TempDir()
	l := NewLoader(Options{})

	t.Run("every format", func(t *testing.T) {
		for _, tc := range []struct{ name, body string }{
			{"app.json", `{"env": {"MiXed": "v"}}`},
			{"app.yaml", "env:\n  MiXed: v\n"},
			{"app.toml", "[env]\nMiXed = \"v\"\n"},
		} {
			got, err := l.StringMapAt(t.Context(), writeFile(t, dir, tc.name, tc.body), []string{"env"})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(got) != 1 || got["MiXed"] != "v" {
				t.Errorf("%s: %#v", tc.name, got)
			}
		}
	})

	t.Run("a key the file does not carry", func(t *testing.T) {
		got, err := l.StringMapAt(t.Context(), writeFile(t, dir, "none.json", `{}`), []string{"env"})
		if err != nil || got != nil {
			t.Errorf("got %#v, err %v; want no map and no error", got, err)
		}
	})

	t.Run("a nested key, matched case-insensitively", func(t *testing.T) {
		path := writeFile(t, dir, "nested.json", `{"Areas": {"libs": {"Env": {"A": "1"}}}}`)
		got, err := l.StringMapAt(t.Context(), path, []string{"areas", "libs", "env"})
		if err != nil || got["A"] != "1" {
			t.Errorf("got %#v, err %v", got, err)
		}
	})

	t.Run("through a $ref", func(t *testing.T) {
		writeFile(t, dir, "env.yaml", "MiXed: v\n")
		path := writeFile(t, dir, "ref.json", `{"env": {"$ref": "./env.yaml"}}`)
		got, err := l.StringMapAt(t.Context(), path, []string{"env"})
		if err != nil || got["MiXed"] != "v" {
			t.Errorf("got %#v, err %v", got, err)
		}
	})

	t.Run("a value that is not an object", func(t *testing.T) {
		path := writeFile(t, dir, "flat.json", `{"env": "text"}`)
		_, err := l.StringMapAt(t.Context(), path, []string{"env"})
		if err == nil || !strings.Contains(err.Error(), "env is not an object") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a value that is not a string", func(t *testing.T) {
		path := writeFile(t, dir, "num.json", `{"env": {"A": 1}}`)
		_, err := l.StringMapAt(t.Context(), path, []string{"env"})
		if err == nil || !strings.Contains(err.Error(), "env.A is not a string") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a file that cannot be read", func(t *testing.T) {
		_, err := l.StringMapAt(t.Context(), filepath.Join(dir, "absent.json"), []string{"env"})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestResolveEditFollowsARef: a `$ref` crossed on the way down moves the edit
// into the file it names, with the rest of the path, and the reference itself
// survives the write.
func TestResolveEditFollowsARef(t *testing.T) {
	dir := t.TempDir()
	inner := writeFile(t, dir, "areas.json", `{"libs": {"path": ["pkgs"]}}`)
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./areas.json"}}`)

	file, keyPath, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if file != inner || !strings.EqualFold(strings.Join(keyPath, "."), "libs.path") {
		t.Errorf("resolve = %q, %#v", file, keyPath)
	}
}

// TestResolveEditFollowsAChain: each reference moves the edit one file on.
func TestResolveEditFollowsAChain(t *testing.T) {
	dir := t.TempDir()
	last := writeFile(t, dir, "path.json", `["pkgs"]`)
	writeFile(t, dir, "libs.json", `{"path": {"$ref": "./path.json"}}`)
	writeFile(t, dir, "areas.json", `{"libs": {"$ref": "./libs.json"}}`)
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": "./areas.json"}}`)

	file, keyPath, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if file != last || len(keyPath) != 0 {
		t.Errorf("resolve = %q, %#v; want the referenced document itself", file, keyPath)
	}
}

// TestResolveEditPrefersTheKeyBesideTheRef: the keys beside a reference are
// the nearer layer, so one of them holding what comes next settles it there.
func TestResolveEditPrefersTheKeyBesideTheRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "areas.json", `{"libs": {"path": ["old"]}}`)
	path := writeFile(t, dir, "app.json",
		`{"areas": {"$ref": "./areas.json", "libs": {"path": ["beside"]}}}`)

	file, keyPath, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if file != path || len(keyPath) != 3 {
		t.Errorf("resolve = %q, %#v; want the file that wrote the sibling", file, keyPath)
	}
}

// TestResolveEditRefusals: a key composed from a reference and its siblings,
// and a key merged from several files, have no single file to be written to.
func TestResolveEditRefusals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.json", `{"libs": {"path": ["a"]}}`)
	writeFile(t, dir, "two.json", `{"apps": {"path": ["b"]}}`)

	t.Run("composed", func(t *testing.T) {
		path := writeFile(t, dir, "composed.json",
			`{"areas": {"$ref": "./one.json", "extra": {"path": ["x"]}}}`)
		_, _, err := resolveEdit(t, path, []string{"areas"})
		if !errors.Is(err, ErrRefEdit) {
			t.Fatalf("err = %v, want ErrRefEdit", err)
		}
		want := "areas: a key composed from a $ref and the keys beside it cannot be rewritten in place;" +
			" write areas beside the $ref, or leave the $ref as the whole value"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	})

	t.Run("merged", func(t *testing.T) {
		path := writeFile(t, dir, "merged.json", `{"areas": {"$ref": ["./one.json", "./two.json"]}}`)
		_, _, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
		if !errors.Is(err, ErrMultiRefEdit) {
			t.Fatalf("err = %v, want ErrMultiRefEdit", err)
		}
		want := "write path beside the $ref, or point the $ref at a single file"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	})

	t.Run("merged with the key written beside it", func(t *testing.T) {
		path := writeFile(t, dir, "beside.json",
			`{"areas": {"$ref": ["./one.json", "./two.json"], "libs": {"path": ["x"]}}}`)
		file, _, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
		if err != nil || file != path {
			t.Fatalf("file = %q, err = %v", file, err)
		}
	})

	t.Run("a reference naming nothing readable", func(t *testing.T) {
		path := writeFile(t, dir, "bad.json", `{"areas": {"$ref": 7}}`)
		_, _, err := resolveEdit(t, path, []string{"areas", "libs"})
		if !errors.Is(err, ErrRefTarget) {
			t.Fatalf("err = %v, want ErrRefTarget", err)
		}
	})
}

// TestResolveEditFollowsAListOfOne: a list holding one file is the reference
// naming that file, here as everywhere else.
func TestResolveEditFollowsAListOfOne(t *testing.T) {
	dir := t.TempDir()
	inner := writeFile(t, dir, "areas.json", `{"libs": {"path": ["pkgs"]}}`)
	path := writeFile(t, dir, "app.json", `{"areas": {"$ref": ["./areas.json"]}}`)

	file, _, err := resolveEdit(t, path, []string{"areas", "libs", "path"})
	if err != nil || file != inner {
		t.Fatalf("file = %q, err = %v", file, err)
	}
}

// TestResolveEditLeavesAPlainKeyAlone: a key no reference stands in front of,
// and a key no file holds, both come back unchanged for the writers to handle
// as they already do.
func TestResolveEditLeavesAPlainKeyAlone(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.json", `{"areas": {"libs": {"path": ["pkgs"]}}, "tags": ["a"]}`)

	for _, keyPath := range [][]string{
		{"areas", "libs", "path"},
		{"tags"},
		{"absent"},
		{"tags", "deeper"},
		{"areas", "libs", "path", "deeper"},
	} {
		file, got, err := resolveEdit(t, path, keyPath)
		if err != nil {
			t.Fatalf("%v: %v", keyPath, err)
		}
		if file != path || len(got) != len(keyPath) {
			t.Errorf("%v resolved to %q, %#v", keyPath, file, got)
		}
	}

	// A document that is not an object holds no key path at all.
	list := writeFile(t, dir, "list.json", `["a"]`)
	if file, _, err := resolveEdit(t, list, []string{"tags"}); err != nil || file != list {
		t.Errorf("file = %q, err = %v", file, err)
	}
}

// TestResolveEditNestingIsCapped: the loader refuses a cycle long before an
// edit is collected, so the writer's own bound only has to stop rather than
// explain. It counts the references followed, not the keys walked.
func TestResolveEditNestingIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= DefaultMaxRefDepth+1; i++ {
		writeFile(t, dir, "f"+itoa(i)+".json", `{"next": {"$ref": "./f`+itoa(i+1)+`.json"}}`)
	}
	path := writeFile(t, dir, "app.json", `{"custom": {"$ref": "./f0.json"}}`)

	keyPath := []string{"custom"}
	for i := 0; i <= DefaultMaxRefDepth+1; i++ {
		keyPath = append(keyPath, "next")
	}
	_, _, err := resolveEdit(t, path, keyPath)
	if err == nil || !strings.Contains(err.Error(), "$ref nesting is more than 32 files deep") {
		t.Fatalf("err = %v", err)
	}

	// A path of many keys through one file is not nesting, and is not capped.
	deep := map[string]any{}
	node := deep
	keys := make([]string, 0, DefaultMaxRefDepth+2)
	for i := 0; i <= DefaultMaxRefDepth+1; i++ {
		key := "k" + itoa(i)
		child := map[string]any{}
		node[key] = child
		node = child
		keys = append(keys, key)
	}
	flat := writeJSON(t, dir, "deep.json", deep)
	file, inner, err := resolveEdit(t, flat, keys)
	if err != nil || file != flat || len(inner) != len(keys) {
		t.Fatalf("file = %q, keys = %d, err = %v", file, len(inner), err)
	}
}

// TestResolveEditReadsAFileItCannotParse: the writer stops where the loader
// would, and says which file it was.
func TestResolveEditReadsAFileItCannotParse(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json", "{not json")
	_, _, err := resolveEdit(t, path, []string{"tags"})
	if err == nil || !strings.Contains(err.Error(), "cannot read "+path) {
		t.Fatalf("err = %v", err)
	}
}

// itoa keeps the fixture builders free of a format import.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
