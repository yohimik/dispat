package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	public "github.com/yohimik/dispat/pkg/models"
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

	snippet, err := RenderKeyTOML([]string{"packages", "core", "dependencies"}, []string{"util"})
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

	snippet, err := RenderDependenciesTOML(Dependencies{
		{Consumer: "a", Provider: "b", Kind: "devDependencies", Keep: true},
	})
	require.NoError(t, err)
	assert.Contains(t, snippet, "[[dependencies.a]]", "the table is keyed by consumer")
	assert.Contains(t, snippet, "provider = 'b'")
	assert.Contains(t, snippet, "kind = 'devDependencies'")
	assert.Contains(t, snippet, "keep = true")
	assert.NotContains(t, snippet, "consumer = ", "the consumer is the key, not a field")
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
	want := Dependencies{
		{Consumer: "api", Provider: "core", Kind: "devDependencies", Keep: true},
		{Consumer: "web", Provider: "core"},
	}
	block, err := RenderDependenciesTOML(want)
	require.NoError(t, err)
	assert.Contains(t, block, "[[dependencies.web]]")
	assert.NotContains(t, block, "keep = false", "a false keep is left out, as the model reads it")

	// Pasted back, the block has to load as exactly what was rendered — and
	// through the real decoder, not a struct tag that happens to line up:
	// string matching would pass on a block dispat cannot read.
	var doc map[string]any
	require.NoError(t, toml.Unmarshal([]byte(block), &doc))
	back, err := public.NormalizeDependencies(doc["dependencies"])
	require.NoError(t, err)
	assert.Equal(t, want, back, "consumers come back sorted, each provider in declared order")

	nested, err := RenderKeyTOML([]string{"packages", "web", "dependencies"}, []string{"core", "utils"})
	require.NoError(t, err)
	var nestedBack map[string]map[string]map[string][]string
	require.NoError(t, toml.Unmarshal([]byte(nested), &nestedBack))
	assert.Equal(t, []string{"core", "utils"}, nestedBack["packages"]["web"]["dependencies"])
}

// TestReplaceKeysOneWritePerFile: two keys of one file go through a single
// call, so the backup is the file as it stood before either edit. Two
// separate calls would save the first edit's output as the "previous" copy.
func TestReplaceKeysOneWritePerFile(t *testing.T) {
	src := `{
  "spaces": {"libs": {"path": "pkgs"}},
  "dependencies": []
}
`
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceKeys(path, []Edit{
		{KeyPath: []string{"dependencies"}, Value: []DependencyConfig{{Consumer: "web", Provider: "core"}}},
		{KeyPath: []string{"initials"}, Value: map[string]string{"core": "1.4.2"}},
	}))

	got := readFile(t, path)
	assert.Contains(t, got, `"provider": "core"`)
	assert.Contains(t, got, `"initials": {`)
	assert.Contains(t, got, `"core": "1.4.2"`)
	assert.Equal(t, src, readFile(t, path+BackupSuffix), "one backup, from before both edits")

	cfg, err := Load(path, nil)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 1)
	assert.Equal(t, "1.4.2", cfg.InitialVersions["core"].String())
}

// TestReplaceKeysNoOps: an empty edit set and an edit whose value re-renders
// to the bytes already there both leave the file, and the backup, alone.
func TestReplaceKeysNoOps(t *testing.T) {
	src := `{
  "initials": {
    "core": "1.0.0"
  }
}
`
	path := writeConfigFile(t, "dispat.json", src)

	require.NoError(t, ReplaceKeys(path, nil))
	require.NoError(t, ReplaceKeys(path, []Edit{
		{KeyPath: []string{"initials"}, Value: map[string]string{"core": "1.0.0"}},
	}))
	assert.Equal(t, src, readFile(t, path))
	_, statErr := os.Stat(path + BackupSuffix)
	assert.True(t, os.IsNotExist(statErr), "an edit that changes nothing writes nothing")
}

// TestReplaceKeysFormats: YAML gains the absent key and keeps its comments,
// TOML refuses whatever the edit says, an unknown extension is an error, and
// a failing second edit leaves the file untouched because nothing is written
// until every splice succeeded.
func TestReplaceKeysFormats(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		src := "# config\nspaces:\n  libs:\n    path: pkgs # keep me\n"
		path := writeConfigFile(t, "dispat.yaml", src)
		require.NoError(t, ReplaceKeys(path, []Edit{
			{KeyPath: []string{"initials"}, Value: map[string]string{"core": "1.4.2"}},
		}))
		got := readFile(t, path)
		assert.Contains(t, got, "# config")
		assert.Contains(t, got, "# keep me")
		assert.Contains(t, got, "core: 1.4.2")

		err := ReplaceKeys(path, []Edit{{KeyPath: []string{"packages", "gone", "dependencies"}, Value: []string{"core"}}})
		assert.ErrorContains(t, err, "not found", "a nested path YAML does not carry is a caller bug")
	})

	t.Run("toml", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.toml", "[initials]\ncore = \"1.0.0\"\n")
		err := ReplaceKeys(path, []Edit{{KeyPath: []string{"initials"}, Value: map[string]string{"core": "2.0.0"}}})
		assert.ErrorIs(t, err, ErrTOMLEdit)
	})

	t.Run("unknown format", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.ini", "x=1\n")
		err := ReplaceKeys(path, []Edit{{KeyPath: []string{"initials"}, Value: map[string]string{}}})
		assert.ErrorContains(t, err, "unknown config format")
	})

	t.Run("missing file", func(t *testing.T) {
		err := ReplaceKeys(filepath.Join(t.TempDir(), "absent.json"), []Edit{
			{KeyPath: []string{"initials"}, Value: map[string]string{}},
		})
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("second edit fails", func(t *testing.T) {
		src := `{"dependencies": []}`
		path := writeConfigFile(t, "dispat.json", src)
		err := ReplaceKeys(path, []Edit{
			{KeyPath: []string{"dependencies"}, Value: []DependencyConfig{{Consumer: "web", Provider: "core"}}},
			{KeyPath: []string{"packages", "gone", "dependencies"}, Value: []string{"core"}},
		})
		require.Error(t, err)
		assert.Equal(t, src, readFile(t, path), "the first splice is discarded with the second's failure")
		_, statErr := os.Stat(path + BackupSuffix)
		assert.True(t, os.IsNotExist(statErr))
	})
}

// TestStringMapAt: the initials map comes back spelled the way the file
// spells it, in every format, because the loaded config cannot be written
// back without renaming the user's keys.
func TestStringMapAt(t *testing.T) {
	t.Run("json keeps the author's case", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.json", `{"initials": {"@acme/Core": "1.4.2", "Web": "2.0.0"}}`)
		got, err := StringMapAt(path, []string{"initials"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"@acme/Core": "1.4.2", "Web": "2.0.0"}, got)

		// The key path itself is matched the way the splicing writers match
		// theirs, so a config spelling it "Initials" resolves too.
		got, err = StringMapAt(path, []string{"INITIALS"})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("yaml and toml", func(t *testing.T) {
		yamlPath := writeConfigFile(t, "dispat.yaml", "initials:\n  core: 1.4.2\n  web: \"2.0.0\"\n")
		got, err := StringMapAt(yamlPath, []string{"initials"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"core": "1.4.2", "web": "2.0.0"}, got)

		tomlPath := writeConfigFile(t, "dispat.toml", "[initials]\ncore = \"1.4.2\"\n")
		got, err = StringMapAt(tomlPath, []string{"initials"})
		require.NoError(t, err, "TOML is unwritable, but its current entries are still readable")
		assert.Equal(t, map[string]string{"core": "1.4.2"}, got)
	})

	t.Run("absent key is not an error", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.json", `{"spaces": {}}`)
		got, err := StringMapAt(path, []string{"initials"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("nested path", func(t *testing.T) {
		path := writeConfigFile(t, "dispat.json", `{"spaces": {"libs": {"tagFormats": {"core": "{name}@{version}"}}}}`)
		got, err := StringMapAt(path, []string{"spaces", "libs", "tagFormats"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"core": "{name}@{version}"}, got)
	})

	t.Run("wrong shapes", func(t *testing.T) {
		list := writeConfigFile(t, "dispat.json", `{"initials": ["core"]}`)
		_, err := StringMapAt(list, []string{"initials"})
		assert.ErrorContains(t, err, "is not an object")

		nested := writeConfigFile(t, "dispat.json", `{"initials": {"core": {"version": "1.0.0"}}}`)
		_, err = StringMapAt(nested, []string{"initials"})
		assert.ErrorContains(t, err, "is not a string")

		broken := writeConfigFile(t, "dispat.json", `{"initials":`)
		_, err = StringMapAt(broken, []string{"initials"})
		assert.Error(t, err)

		_, err = StringMapAt(writeConfigFile(t, "dispat.ini", "x=1\n"), []string{"initials"})
		assert.ErrorContains(t, err, "unknown config format")

		_, err = StringMapAt(filepath.Join(t.TempDir(), "absent.json"), []string{"initials"})
		assert.True(t, os.IsNotExist(err))
	})
}
