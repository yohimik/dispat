package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The write-back contract: only the top-level dependencies key changes, every
// other byte survives, and the previous file is copied to .backup first.

func writeConfigFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestReplaceDependenciesJSONPreservesEverythingElse(t *testing.T) {
	// Odd but valid: 4-space indent, unsorted keys, and a script named
	// "dependencies" — a nested decoy the splice must not touch.
	src := `{
    "spaces": {"libs": {"path": "pkgs"}},
    "dependencies": [
        {"consumer": "app", "provider": "old"}
    ],
    "scripts": {"dependencies": "echo decoy", "build": "make"}
}
`
	path := writeConfigFile(t, "dispat.json", src)
	deps := []DependencyConfig{
		{Consumer: "app", Provider: "core"},
		{Consumer: "app", Provider: "tools", Kind: "devDependencies", Keep: true},
	}
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, deps))

	got := readFile(t, path)
	assert.Contains(t, got, `"spaces": {"libs": {"path": "pkgs"}},`, "untouched head")
	assert.Contains(t, got, `"scripts": {"dependencies": "echo decoy", "build": "make"}`,
		"nested decoy key untouched")
	assert.Contains(t, got, `"provider": "core"`)
	assert.Contains(t, got, `"kind": "devDependencies"`)
	assert.Contains(t, got, `"keep": true`)
	assert.NotContains(t, got, `"old"`)
	assert.Equal(t, src, readFile(t, path+BackupSuffix), "backup is the previous bytes")

	// The rewritten file is still a loadable config.
	cfg, err := Load(path, nil)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 2)
	assert.True(t, cfg.Dependencies[1].Keep)
}

func TestReplaceDependenciesJSONPreservesOverrideBlocks(t *testing.T) {
	// The splice touches only the top-level dependencies key, so the
	// versionGroups map and a space's packages overrides — nested structures
	// the compute command knows nothing about — survive byte for byte.
	src := `{
    "versionGroups": {"platform": {"versioning": "fixed"}},
    "spaces": {"libs": {"path": "pkgs"}},
    "packages": {"core": {"revertOnFail": false, "dependencies": ["util"]}},
    "dependencies": []
}
`
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "app", Provider: "core"}}))
	got := readFile(t, path)
	assert.Contains(t, got, `"versionGroups": {"platform": {"versioning": "fixed"}},`)
	assert.Contains(t, got, `"packages": {"core": {"revertOnFail": false, "dependencies": ["util"]}}`)
	assert.Contains(t, got, `"provider": "core"`)
}

func TestReplaceStringListJSONNestedPath(t *testing.T) {
	// A packages entry's dependencies list is a nested key; the splice must
	// hit exactly that list and leave the root list and the entry's other
	// keys alone.
	src := `{
    "spaces": {"libs": {"path": "pkgs"}},
    "packages": {"core": {"revertOnFail": false, "dependencies": ["old", "util"]}},
    "dependencies": [{"consumer": "app", "provider": "core"}]
}
`
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceStringList(path, []string{"packages", "core", "dependencies"}, []string{"util"}))
	got := readFile(t, path)
	assert.NotContains(t, got, `"old"`)
	assert.Contains(t, got, `"revertOnFail": false`)
	assert.Contains(t, got, `"dependencies": [{"consumer": "app", "provider": "core"}]`,
		"root list untouched")
	assert.Equal(t, src, readFile(t, path+BackupSuffix))

	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &cfg), "result is valid JSON")
	entry := cfg["packages"].(map[string]any)["core"].(map[string]any)
	assert.Equal(t, []any{"util"}, entry["dependencies"])

	// Emptying the list keeps the key as [] rather than null.
	require.NoError(t, ReplaceStringList(path, []string{"packages", "core", "dependencies"}, nil))
	require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &cfg))
	entry = cfg["packages"].(map[string]any)["core"].(map[string]any)
	assert.Equal(t, []any{}, entry["dependencies"])
}

func TestReplaceStringListJSONMissingNestedKeyErrors(t *testing.T) {
	path := writeConfigFile(t, "dispat.json", `{"packages": {"core": {}}}`)
	err := ReplaceStringList(path, []string{"packages", "app", "dependencies"}, []string{"x"})
	require.Error(t, err, "nested paths are only edited, never created")
	_, statErr := os.Stat(path + BackupSuffix)
	assert.True(t, os.IsNotExist(statErr), "failure writes nothing")
}

func TestReplaceStringListYAMLNestedPath(t *testing.T) {
	src := `# config
packages:
  core:
    revertOnFail: false # keep me
    dependencies:
      - old
      - util
dependencies:
  - consumer: app
    provider: core
`
	path := writeConfigFile(t, "dispat.yaml", src)
	require.NoError(t, ReplaceStringList(path, []string{"packages", "core", "dependencies"}, []string{"util"}))
	got := readFile(t, path)
	assert.Contains(t, got, "# config")
	assert.Contains(t, got, "# keep me")
	assert.NotContains(t, got, "- old")
	assert.Contains(t, got, "provider: core", "root list untouched")
}

func TestReplaceStringListTOMLRefuses(t *testing.T) {
	path := writeConfigFile(t, "dispat.toml", "[packages.core]\ndependencies = [\"old\"]\n")
	err := ReplaceStringList(path, []string{"packages", "core", "dependencies"}, []string{"util"})
	assert.ErrorIs(t, err, ErrTOMLEdit)

	snippet, err := RenderStringListTOML([]string{"packages", "core", "dependencies"}, []string{"util"})
	require.NoError(t, err)
	assert.Contains(t, snippet, "[packages]")
	assert.Contains(t, snippet, "'util'")
}

func TestReplaceDependenciesJSONAppendsMissingKey(t *testing.T) {
	src := "{\n  \"scripts\": {\"b\": \"make\"}\n}\n"
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
	got := readFile(t, path)
	assert.Contains(t, got, `"scripts": {"b": "make"},`)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &cfg), "result is valid JSON")
	assert.Len(t, cfg["dependencies"], 1)
}

func TestReplaceDependenciesJSONEmptyObject(t *testing.T) {
	path := writeConfigFile(t, "dispat.json", "{}\n")
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &cfg))
	assert.Len(t, cfg["dependencies"], 1)
}

func TestReplaceDependenciesYAMLKeepsComments(t *testing.T) {
	src := `# the monorepo config
scripts:
  build: make # the build
dependencies:
  - consumer: app
    provider: old
spaces:
  libs:
    path: pkgs
`
	path := writeConfigFile(t, "dispat.yaml", src)
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "app", Provider: "core"}}))
	got := readFile(t, path)
	assert.Contains(t, got, "# the monorepo config")
	assert.Contains(t, got, "# the build")
	assert.Contains(t, got, "provider: core")
	assert.NotContains(t, got, "provider: old")
	assert.Contains(t, got, "path: pkgs")
	assert.Equal(t, src, readFile(t, path+BackupSuffix))
}

func TestReplaceDependenciesYAMLAppendsMissingKey(t *testing.T) {
	path := writeConfigFile(t, "dispat.yaml", "scripts:\n  b: make\n")
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "a", Provider: "b", Kind: "peerDependencies"}}))
	got := readFile(t, path)
	assert.Contains(t, got, "dependencies:")
	assert.Contains(t, got, "kind: peerDependencies")
}

func TestReplaceDependenciesNoChangeWritesNothing(t *testing.T) {
	src := "{\n  \"dependencies\": [\n    {\n      \"consumer\": \"a\",\n      \"provider\": \"b\"\n    }\n  ]\n}"
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
	_, err := os.Stat(path + BackupSuffix)
	assert.True(t, os.IsNotExist(err), "no change, no backup, no write")
}

func TestReplaceDependenciesTOMLRefuses(t *testing.T) {
	path := writeConfigFile(t, "dispat.toml", "[scripts]\nb = \"make\"\n")
	err := ReplaceDependencies(path, []string{"dependencies"}, []DependencyConfig{{Consumer: "a", Provider: "b"}})
	assert.ErrorIs(t, err, ErrTOMLEdit)
	_, statErr := os.Stat(path + BackupSuffix)
	assert.True(t, os.IsNotExist(statErr), "refusal writes nothing")

	snippet, err := RenderDependenciesTOML([]DependencyConfig{
		{Consumer: "a", Provider: "b", Kind: "devDependencies", Keep: true},
	})
	require.NoError(t, err)
	assert.Contains(t, snippet, "[[dependencies]]")
	assert.Contains(t, snippet, "consumer = 'a'")
	assert.Contains(t, snippet, "kind = 'devDependencies'")
	assert.Contains(t, snippet, "keep = true")
}

func TestEditAtomicWriteErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	// An unwritable folder fails at the temp-file stage, leaving nothing behind.
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	err := atomicWrite(filepath.Join(dir, "dispat.json"), []byte("{}"), 0o644)
	require.Error(t, err)
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "no temp file survives a failed write")

	// A target that is a folder fails at the rename, and the temp file is
	// cleaned up.
	dir2 := t.TempDir()
	target := filepath.Join(dir2, "dispat.json")
	require.NoError(t, os.Mkdir(target, 0o755))
	err = atomicWrite(target, []byte("{}"), 0o644)
	require.Error(t, err)
	entries, readErr = os.ReadDir(dir2)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1, "only the pre-existing folder remains")
}

func TestReplaceRefusesWhatItCannotEditSafely(t *testing.T) {
	// The editor rewrites the user's own config file, so every input it does
	// not fully understand has to come back as an error with the file
	// untouched. A silent no-op would leave `compute --write` claiming a
	// change it never made; a partial write would corrupt the config.
	deps := []DependencyConfig{{Consumer: "web", Provider: "core"}}

	t.Run("a format with no in-place editor", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.ini", "[deps]\n")
		require.Error(t, ReplaceDependencies(path, []string{"dependencies"}, deps))
		assert.Equal(t, "[deps]\n", readFile(t, path))
		assert.NoFileExists(t, path+BackupSuffix, "a refused edit writes no backup either")
	})

	t.Run("a file that is not there", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "dispat.json")
		require.Error(t, ReplaceDependencies(missing, []string{"dependencies"}, deps))
	})

	t.Run("a config that is not an object", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.json", `["not", "an", "object"]`)
		err := ReplaceDependencies(path, []string{"dependencies"}, deps)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "top level is not an object")
	})

	t.Run("a config that is not JSON at all", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.json", "{ this is not json")
		require.Error(t, ReplaceDependencies(path, []string{"dependencies"}, deps))
		assert.Equal(t, "{ this is not json", readFile(t, path))
	})

	t.Run("an ancestor that is not an object", func(t *testing.T) {
		// packages.web is a string here, so descending into it would mean
		// replacing a value the caller never looked at.
		path := writeConfigFile(t, "dispat.json", `{"packages": {"web": "not-an-object"}}`)
		err := ReplaceStringList(path, []string{"packages", "web", "dependencies"}, []string{"core"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not an object")
	})
}

func TestRenderTOMLFallbacks(t *testing.T) {
	// A TOML config is refused for in-place editing, so the command prints a
	// paste-ready block instead. It has to be valid TOML carrying exactly the
	// fields the config model reads back.
	block, err := RenderDependenciesTOML([]DependencyConfig{
		{Consumer: "web", Provider: "core"},
		{Consumer: "api", Provider: "core", Kind: "devDependencies", Keep: true},
	})
	require.NoError(t, err)
	assert.Contains(t, block, "[[dependencies]]")
	assert.NotContains(t, block, "keep = false", "a false keep is left out, as the model reads it")

	// Pasted back, the block has to parse into exactly what was rendered:
	// string matching would pass on a block no TOML reader accepts.
	var back struct {
		Dependencies []DependencyConfig `toml:"dependencies"`
	}
	require.NoError(t, toml.Unmarshal([]byte(block), &back))
	assert.Equal(t, []DependencyConfig{
		{Consumer: "web", Provider: "core"},
		{Consumer: "api", Provider: "core", Kind: "devDependencies", Keep: true},
	}, back.Dependencies)

	nested, err := RenderStringListTOML([]string{"packages", "web", "dependencies"}, []string{"core", "utils"})
	require.NoError(t, err)
	var nestedBack map[string]map[string]map[string][]string
	require.NoError(t, toml.Unmarshal([]byte(nested), &nestedBack))
	assert.Equal(t, []string{"core", "utils"}, nestedBack["packages"]["web"]["dependencies"])
}
