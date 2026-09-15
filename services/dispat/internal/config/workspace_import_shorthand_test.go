package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Config imports keep the ordinary list shorthands without losing the path
// provenance of a referenced document, including an absolute reference and
// a scalar list value.
func TestWorkspaceImportShorthandsRetainReferencedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		paths []string
	}{
		{"comma separated string", "../sources/a/dispat.json,../sources/b/dispat.json", []string{"../sources/a/dispat.json", "../sources/b/dispat.json"}},
		{"numeric filename", 42, []string{"42"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			declaringDir := filepath.Join(root, "configuration")
			require.NoError(t, os.Mkdir(declaringDir, 0o755))
			list := filepath.Join(declaringDir, "imports.json")
			body, err := json.Marshal(tc.value)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(list, body, 0o644))
			body, err = json.Marshal(map[string]any{"configs": map[string]string{"$ref": list}})
			require.NoError(t, err)
			path := filepath.Join(root, "dispat.json")
			require.NoError(t, os.WriteFile(path, body, 0o644))

			loaded, err := Load(path, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.paths, loaded.Configs)
			imports, err := workspaceImports(loaded, path, root, nil)
			require.NoError(t, err)
			require.Len(t, imports, len(tc.paths))
			for i, item := range imports {
				assert.Equal(t, declaringDir, item.Base)
				resolved, err := resolveImportPath(root, item.Base, item.Path)
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(declaringDir, tc.paths[i]), resolved)
			}
		})
	}
}

func TestWorkspaceImportReferenceCyclesStopAtDepthLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dispat.json")
	list := filepath.Join(root, "imports.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"configs":{"$ref":"imports.json"}}`), 0o644))
	require.NoError(t, os.WriteFile(list, []byte(`{"$ref":"imports.json"}`), 0o644))

	imports, err := workspaceImports(&File{}, path, root, nil)
	require.Nil(t, imports)
	require.ErrorContains(t, err, "$ref nesting is more than")
	assert.ErrorContains(t, err, "imports.json")
}

func TestWorkspaceImportsRejectUnsupportedDocumentFormat(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dispat.custom")
	require.NoError(t, os.WriteFile(path, []byte(`{"configs":[]}`), 0o644))
	imports, err := workspaceImports(&File{}, path, root, nil)
	require.Nil(t, imports)
	require.ErrorContains(t, err, "dispat reads json, yaml and toml config files")
}
