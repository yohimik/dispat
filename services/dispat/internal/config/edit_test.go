package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	require.NoError(t, ReplaceDependencies(path, deps))

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

func TestReplaceDependenciesJSONAppendsMissingKey(t *testing.T) {
	src := "{\n  \"scripts\": {\"b\": \"make\"}\n}\n"
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceDependencies(path, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
	got := readFile(t, path)
	assert.Contains(t, got, `"scripts": {"b": "make"},`)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &cfg), "result is valid JSON")
	assert.Len(t, cfg["dependencies"], 1)
}

func TestReplaceDependenciesJSONEmptyObject(t *testing.T) {
	path := writeConfigFile(t, "dispat.json", "{}\n")
	require.NoError(t, ReplaceDependencies(path, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
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
	require.NoError(t, ReplaceDependencies(path, []DependencyConfig{{Consumer: "app", Provider: "core"}}))
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
	require.NoError(t, ReplaceDependencies(path, []DependencyConfig{{Consumer: "a", Provider: "b", Kind: "peerDependencies"}}))
	got := readFile(t, path)
	assert.Contains(t, got, "dependencies:")
	assert.Contains(t, got, "kind: peerDependencies")
}

func TestReplaceDependenciesNoChangeWritesNothing(t *testing.T) {
	src := "{\n  \"dependencies\": [\n    {\n      \"consumer\": \"a\",\n      \"provider\": \"b\"\n    }\n  ]\n}"
	path := writeConfigFile(t, "dispat.json", src)
	require.NoError(t, ReplaceDependencies(path, []DependencyConfig{{Consumer: "a", Provider: "b"}}))
	_, err := os.Stat(path + BackupSuffix)
	assert.True(t, os.IsNotExist(err), "no change, no backup, no write")
}

func TestReplaceDependenciesTOMLRefuses(t *testing.T) {
	path := writeConfigFile(t, "dispat.toml", "[scripts]\nb = \"make\"\n")
	err := ReplaceDependencies(path, []DependencyConfig{{Consumer: "a", Provider: "b"}})
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
