package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaterializedRootsResolvesRelativeEntryConfiguration(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	private := filepath.Join(base, "private")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(private, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte("{}"), 0o600))
	t.Chdir(root)

	workspace := &Workspace{ControlRoot: root, Repositories: []Repository{{
		Name: "entry", Root: root, ConfigPath: "dispat.json",
	}}}
	materialized, err := workspace.MaterializedRoots(map[string]string{"entry": private})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(private, "dispat.json"), materialized.Repositories[0].ConfigPath)

	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "dispat.json"), []byte("{}"), 0o600))
	workspace.Repositories[0].ConfigPath = filepath.Join("..", "outside", "dispat.json")
	_, err = workspace.MaterializedRoots(map[string]string{"entry": private})
	require.ErrorContains(t, err, "lies outside repository")
}
