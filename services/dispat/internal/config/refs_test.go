package config

// The loader every config file arrives through: which formats it reads, what
// it makes of a file that says nothing, and the copy that keeps viper's
// key-lowercasing away from the tree the env pass reads.
//
// Configs are authored as typed models and marshalled, as everywhere else in
// this package; raw bytes appear only where the point is a file no marshaller
// would produce.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes one file into dir and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// TestReadTreeFormats: the three formats dispat reads all parse into the same
// tree, with their keys spelled as the file wrote them.
func TestReadTreeFormats(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"dispat.json", `{"env": {"MiXed": "v"}}`},
		{"dispat.yaml", "env:\n  MiXed: v\n"},
		{"dispat.yml", "env:\n  MiXed: v\n"},
		{"dispat.toml", "[env]\nMiXed = \"v\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := readTree(writeFile(t, dir, tc.name, tc.body))
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"env": map[string]any{"MiXed": "v"}}, tree.root)
		})
	}
}

// TestReadTreeUnknownFormatIsRefused: the extension names the parser, so a
// file in a format dispat has none for is refused by name rather than guessed
// at. This is the one thing an explicit `--config dispat.ini` can be told.
func TestReadTreeUnknownFormatIsRefused(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.ini", "[env]\nMiXed = v\n")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read "+path)
	assert.Contains(t, err.Error(), "dispat reads json, yaml and toml config files")
}

// TestReadTreeMalformedFileIsReported: a file the parser chokes on names
// itself, because "cannot read this file" is the first thing to know and the
// parser's own message follows it.
func TestReadTreeMalformedFileIsReported(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.json", "{not json")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read "+path)
}

// TestReadTreeEmptyFileIsAnEmptyObject: an empty file is a config that says
// nothing, which validation answers ("at least one space or package") far
// better than the parser could.
func TestReadTreeEmptyFileIsAnEmptyObject(t *testing.T) {
	tree, err := readTree(writeFile(t, t.TempDir(), "dispat.yaml", ""))
	require.NoError(t, err)
	assert.Empty(t, tree.root)

	_, err = Load(writeFile(t, t.TempDir(), "dispat.yaml", ""), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one space or package")
}

// TestReadTreeTopLevelMustBeAnObject: a config file is an object of keys. A
// document that is a list has no key to read, and saying so beats an
// unknown-key error about a key nobody wrote.
func TestReadTreeTopLevelMustBeAnObject(t *testing.T) {
	path := writeFile(t, t.TempDir(), "dispat.yaml", "- one\n- two\n")

	_, err := readTree(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the top level is not an object")
}

// TestViperFromTreeLeavesTheTreeAlone: viper lowercases the keys of the map it
// is handed, in place and all the way down, and then keeps those very maps. It
// therefore gets a copy: the tree is what the env pass reads case-exactly, and
// a config whose env survived the decode is the proof it was not shared.
func TestViperFromTreeLeavesTheTreeAlone(t *testing.T) {
	tree := &tree{root: map[string]any{
		"spaces": map[string]any{"Libs": map[string]any{
			"env":       map[string]any{"MiXed": "v"},
			"aliasTags": []any{map[string]any{"Format": "latest"}},
		}},
	}}

	v, err := viperFromTree(tree, nil)
	require.NoError(t, err)
	require.NotNil(t, v.Get("spaces"))

	assert.Equal(t, map[string]any{"spaces": map[string]any{"Libs": map[string]any{
		"env":       map[string]any{"MiXed": "v"},
		"aliasTags": []any{map[string]any{"Format": "latest"}},
	}}}, tree.root, "the tree keeps every key as the file spelled it")
}

// TestCloneValueCopiesEveryContainer: the parsers in use produce string-keyed
// maps, generic maps and slices, and a typed container is copied by the
// reflect fallback rather than shared.
func TestCloneValueCopiesEveryContainer(t *testing.T) {
	original := map[string]any{
		"generic": map[any]any{1: map[string]any{"deep": "v"}},
		"list":    []any{map[string]any{"deep": "v"}},
		"typed":   map[string][]string{"a": {"one"}},
		"nils":    []any{nil},
		"scalar":  "v",
	}
	clone := cloneTree(original)
	require.Equal(t, original, clone)

	clone["generic"].(map[any]any)[1].(map[string]any)["deep"] = "changed"
	clone["list"].([]any)[0].(map[string]any)["deep"] = "changed"
	clone["typed"].(map[string][]string)["a"][0] = "changed"

	assert.Equal(t, "v", original["generic"].(map[any]any)[1].(map[string]any)["deep"])
	assert.Equal(t, "v", original["list"].([]any)[0].(map[string]any)["deep"])
	assert.Equal(t, "one", original["typed"].(map[string][]string)["a"][0])
}
