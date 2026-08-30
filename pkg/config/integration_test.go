package config

// The package end to end, against real directory trees: an ascent from a
// nested folder, a configuration split across all three formats, a fan-out of
// fragments, an edit written back through a reference, and the files a
// configuration turns out to have been read from.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// loadFrom is the whole story in one call: find the file from dir, read it,
// render the settings, decode.
func loadFrom(t *testing.T, dir string, ov Overrides) (*appConfig, *Tree, string, error) {
	t.Helper()
	l := NewLoader(Options{})
	path, _, err := l.Resolve(t.Context(), dir, appResolver())
	if err != nil {
		return nil, nil, "", err
	}
	cfg, tree, err := loadApp(t, path, ov)
	return cfg, tree, path, err
}

// TestEndToEndAcrossFormatsAndFolders: a configuration composed from all three
// formats, found by ascending out of a package folder, decoded whole.
func TestEndToEndAcrossFormatsAndFolders(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cfg/areas.yaml", strings.Join([]string{
		"libs:",
		"  path: pkgs",
		"  flow:",
		"    $ref: ./flow.toml",
		"  env:",
		"    $ref: [../base.json, ./libs.yaml]",
		"apps:",
		"  path: [apps, extra]",
		"  versioning: fixed",
		"",
	}, "\n"))
	writeFile(t, dir, "cfg/flow.toml", "build = [\"compile\", \"link\"]\n")
	writeFile(t, dir, "cfg/libs.yaml", "MiXed: local\nOnly: libs\n")
	writeFile(t, dir, "base.json", `{"MIXED": "base", "Shared": "base"}`)
	writeFile(t, dir, "app.json", `{
		"name": "monorepo",
		"areas": {"$ref": "./cfg/areas.yaml"},
		"custom": {"team": "platform"}
	}`)
	deep := filepath.Join(dir, "pkgs", "core", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, tree, path, err := loadFrom(t, deep, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if path != filepath.Join(dir, "app.json") {
		t.Errorf("resolved %q", path)
	}
	if cfg.Name != "monorepo" {
		t.Errorf("name = %q", cfg.Name)
	}
	libs := cfg.Areas["libs"]
	if want := []string{"compile", "link"}; !reflect.DeepEqual(want, libs.Flow.Build) {
		t.Errorf("build = %#v", libs.Flow.Build)
	}
	wantEnv := map[string]string{"MiXed": "local", "Shared": "base", "Only": "libs"}
	if !reflect.DeepEqual(wantEnv, libs.Env) {
		t.Errorf("env = %#v, want %#v", libs.Env, wantEnv)
	}
	if want := []string{"apps", "extra"}; !reflect.DeepEqual(want, cfg.Areas["apps"].Path) {
		t.Errorf("path = %#v", cfg.Areas["apps"].Path)
	}
	// Keys are visited in order at every level, so the files come back in the
	// order the walk read them: `env` before `flow` inside `libs`.
	wantFiles := []string{
		path,
		filepath.Join(dir, "cfg", "areas.yaml"),
		filepath.Join(dir, "base.json"),
		filepath.Join(dir, "cfg", "libs.yaml"),
		filepath.Join(dir, "cfg", "flow.toml"),
	}
	if !reflect.DeepEqual(wantFiles, tree.Files) {
		t.Errorf("files = %#v, want %#v", tree.Files, wantFiles)
	}
}

// TestEndToEndThreeLevelFanOut: a root that names fragments that name
// fragments, each resolved against its own folder, with one leaf shared by two
// branches and read once for each.
func TestEndToEndThreeLevelFanOut(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shared/flow.json", `{"build": ["b"], "publish": ["p"]}`)
	for _, name := range []string{"one", "two"} {
		writeFile(t, dir, "areas/"+name+"/area.json",
			`{"path": "`+name+`", "flow": {"$ref": "../../shared/flow.json"}}`)
		writeFile(t, dir, "areas/"+name+".json",
			`{"`+name+`": {"$ref": "./`+name+`/area.json"}}`)
	}
	path := writeFile(t, dir, "app.json",
		`{"areas": {"$ref": ["./areas/one.json", "./areas/two.json"]}}`)

	cfg, tree, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"one", "two"} {
		area := cfg.Areas[name]
		if !reflect.DeepEqual([]string{name}, area.Path) {
			t.Errorf("%s path = %#v", name, area.Path)
		}
		if area.Flow == nil || !reflect.DeepEqual([]string{"b"}, area.Flow.Build) {
			t.Errorf("%s flow = %#v", name, area.Flow)
		}
	}
	if len(tree.Files) != 7 {
		t.Errorf("files = %#v, want the shared leaf read once per branch", tree.Files)
	}
}

// TestEndToEndOverridesLandOnTheDecodedModel: an override reaches a nested key
// of a configuration that came out of three files, and replaces whatever
// spelling the file used.
func TestEndToEndOverridesLandOnTheDecodedModel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "areas.json", `{"libs": {"Path": "pkgs", "versioning": "fixed"}}`)
	path := writeFile(t, dir, "app.json",
		`{"LogLevel": "debug", "areas": {"$ref": "./areas.json"}}`)

	cfg, _, err := loadApp(t, path, Overrides{
		"logLevel":              "warn",
		"areas.libs.path":       "elsewhere",
		"areas.libs.flow.build": []string{"one"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("logLevel = %q", cfg.LogLevel)
	}
	libs := cfg.Areas["libs"]
	if !reflect.DeepEqual([]string{"elsewhere"}, libs.Path) {
		t.Errorf("path = %#v", libs.Path)
	}
	if libs.Versioning != "fixed" {
		t.Errorf("everything the override did not name survives: %q", libs.Versioning)
	}
	if libs.Flow == nil || !reflect.DeepEqual([]string{"one"}, libs.Flow.Build) {
		t.Errorf("flow = %#v", libs.Flow)
	}
}

// TestEndToEndEditGoesWhereTheKeyIsWritten: the writer follows the same
// references the loader did, so a configuration split across files is written
// where each key is written, and the loader reads the change back.
func TestEndToEndEditGoesWhereTheKeyIsWritten(t *testing.T) {
	dir := t.TempDir()
	inner := writeFile(t, dir, "areas.json", "{\n  \"libs\": {\n    \"path\": [\"old\"]\n  }\n}\n")
	path := writeFile(t, dir, "app.json", `{"name": "app", "areas": {"$ref": "./areas.json"}}`)
	l := NewLoader(Options{})

	file, keyPath, err := l.ResolveEdit(t.Context(), path, []string{"areas", "libs", "path"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if file != inner {
		t.Fatalf("file = %q, want %q", file, inner)
	}
	if err := ApplyEdits(t.Context(), file, []Edit{{KeyPath: keyPath, Value: []string{"new"}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual([]string{"new"}, cfg.Areas["libs"].Path) {
		t.Errorf("path = %#v", cfg.Areas["libs"].Path)
	}
	if got := readBack(t, path); !strings.Contains(got, `"$ref": "./areas.json"`) {
		t.Errorf("the reference did not survive the write:\n%s", got)
	}
}

// TestEndToEndAFileNobodyCanRead: a config whose permissions forbid reading is
// the read failure it is, named by the file. Skipped for root, which does not
// honour file permissions.
func TestEndToEndAFileNobodyCanRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads what it likes")
	}
	dir := t.TempDir()
	path := writeFile(t, dir, "app.json", `{"areas": {}}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err := readTree(t, path)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want a permission failure", err)
	}
}

// TestEndToEndABackupThatCannotBeWritten: the backup is written before the
// file, so a folder that refuses the write leaves the config untouched and
// says which half failed.
func TestEndToEndABackupThatCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes where it likes")
	}
	dir := t.TempDir()
	src := `{"tags": ["old"]}`
	path := writeFile(t, dir, "app.json", src)

	p, err := PrepareEdits(t.Context(), path, []Edit{{KeyPath: []string{"tags"}, Value: []string{"new"}}})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err = p.Commit()
	if err == nil || !strings.Contains(err.Error(), "saving backup") {
		t.Fatalf("err = %v", err)
	}
	if got := readBack(t, path); got != src {
		t.Errorf("the config changed:\n%s", got)
	}
}
