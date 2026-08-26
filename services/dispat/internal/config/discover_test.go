package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolvedSpaceConfigs proves the config-only space resolution the run
// command's typo guard leans on: the space folder's own file merges over the
// root entry, a space without a file keeps the entry as written, and no
// package needs to exist in the folder for the space's scripts to be seen.
func TestResolvedSpaceConfigs(t *testing.T) {
	cfg := minimalConfig()
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Scripts = map[string]Script{"entryonly": {"echo entry"}}
	})
	cfg.Spaces["empty"] = SpaceConfig{Path: PathList{"emptyspace"},
		Scripts: map[string]Script{"fromentry": {"echo entry"}}}
	root := writeModelRepo(t, cfg, "pkgs/core", "emptyspace")
	writeJSON(t, filepath.Join(root, "emptyspace", "dispat.json"),
		SpaceFile{Scripts: map[string]Script{"fromfile": {"echo file"}}})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	spaces, err := ResolvedSpaceConfigs(loaded, root)
	require.NoError(t, err)
	require.Len(t, spaces, 2)

	_, ok := spaces["empty"].Script("fromfile")
	assert.True(t, ok, "the empty folder's own file speaks without a package to carry it")
	_, ok = spaces["empty"].Script("fromentry")
	assert.True(t, ok, "the root entry's script survives the file merge")
	_, ok = spaces["libs"].Script("entryonly")
	assert.True(t, ok, "a space without a folder file keeps its entry as written")
	_, ok = spaces["libs"].Script("fromfile")
	assert.False(t, ok, "one space's file reaches no other space")
}

// TestResolvedSpaceConfigsRefusesABadSpaceFile: the config-only resolution
// reports a space file it cannot accept the same way discovery does, so the
// two readers cannot disagree about what a folder's file may say.
func TestResolvedSpaceConfigsRefusesABadSpaceFile(t *testing.T) {
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writeJSON(t, filepath.Join(root, "pkgs", "dispat.json"), map[string]any{"path": "elsewhere"})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, err = ResolvedSpaceConfigs(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be set")
}
