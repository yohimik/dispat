package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceFolderInputsFollowTheirRepositoryOwner exercises every local
// discovery input in one mixed workspace. Control-owned folders retain the
// ordinary monorepo layers, centrally managed source folders cannot inject
// configuration or discovery filters, and an explicitly imported source
// keeps all of its local layers.
func TestWorkspaceFolderInputsFollowTheirRepositoryOwner(t *testing.T) {
	central := workspaceRepo(t, "central", nil)
	require.NoError(t, os.MkdirAll(filepath.Join(central, "pkgs", "filtered"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(central, "pkgs", "filtered", "README.md"), []byte("filtered"), 0o644))
	writeJSON(t, filepath.Join(central, "pkgs", "dispat.json"),
		map[string]any{"tagFormat": "untrusted-space-{name}@{version}"})
	writeJSON(t, filepath.Join(central, "pkgs", "central", "dispat.json"),
		map[string]any{"tagFormat": "untrusted-package-{name}@{version}"})
	writeIgnoreFile(t, central, "pkgs/central", "ignored.txt\n")
	require.NoError(t, os.WriteFile(filepath.Join(central, "pkgs", DispatexcludeName),
		[]byte("filtered\n"), 0o644))
	workspaceGit(t, central, "add", ".")
	workspaceGit(t, central, "commit", "-m", "test: add centrally ignored folder inputs")

	importedCfg := &File{Spaces: map[string]SpaceConfig{
		"imported": {Path: PathList{"pkgs"}, TagFormat: "imported-root-{name}@{version}"},
	}}
	imported := workspaceRepo(t, "imported", importedCfg)
	require.NoError(t, os.MkdirAll(filepath.Join(imported, "pkgs", "filtered"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(imported, "pkgs", "filtered", "README.md"), []byte("filtered"), 0o644))
	writeJSON(t, filepath.Join(imported, "pkgs", "dispat.json"),
		map[string]any{"tagFormat": "imported-space-{name}@{version}"})
	writeJSON(t, filepath.Join(imported, "pkgs", "imported", "dispat.json"),
		map[string]any{"tagFormat": "imported-package-{name}@{version}"})
	writeIgnoreFile(t, imported, "pkgs/imported", "ignored.txt\n")
	require.NoError(t, os.WriteFile(filepath.Join(imported, "pkgs", DispatexcludeName),
		[]byte("filtered\n"), 0o644))
	workspaceGit(t, imported, "add", ".")
	workspaceGit(t, imported, "commit", "-m", "test: add imported folder inputs")

	root, path := workspaceControl(t, map[string]string{
		"central":  central,
		"imported": imported,
	}, File{
		Configs: []string{"sources/imported/dispat.json"},
		Spaces: map[string]SpaceConfig{
			"central": {Path: PathList{"central-alias/pkgs"}, TagFormat: "control-central-{name}@{version}"},
			"owned":   {Path: PathList{"owned"}, TagFormat: "control-owned-{name}@{version}"},
		},
	})
	require.NoError(t, os.Symlink(filepath.Join("sources", "central"), filepath.Join(root, "central-alias")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "owned", "control"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "owned", "filtered"), 0o755))
	writeJSON(t, filepath.Join(root, "owned", "dispat.json"),
		map[string]any{"tagFormat": "control-space-{name}@{version}"})
	writeJSON(t, filepath.Join(root, "owned", "control", "dispat.json"),
		map[string]any{"tagFormat": "control-package-{name}@{version}"})
	writeIgnoreFile(t, root, "owned/control", "ignored.txt\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "owned", DispatexcludeName),
		[]byte("filtered\n"), 0o644))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "test: add control-owned folder inputs")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	pkgs, _, excluded, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	require.Contains(t, byName, "central", "a source .dispatexclude cannot hide centrally managed packages")
	require.Contains(t, byName, "filtered", "the central source's filter is not control configuration")
	require.Contains(t, byName, "control")
	require.Contains(t, byName, "imported")
	assert.Equal(t, "control-central-{name}@{version}", string(byName["central"].Space.TagFormat),
		"centrally managed source space and package files are ignored through a canonical symlink")
	assert.True(t, byName["central"].Counts(filepath.ToSlash(filepath.Join(byName["central"].Dir, "ignored.txt"))),
		"a centrally managed source package cannot add its own ignore rules")

	assert.Equal(t, "control-package-{name}@{version}", string(byName["control"].Space.TagFormat),
		"a control-owned package keeps its in-folder layer")
	assert.False(t, byName["control"].Counts(filepath.ToSlash(filepath.Join(byName["control"].Dir, "ignored.txt"))),
		"a control-owned package keeps its .dispatignore")
	assert.Equal(t, "imported-package-{name}@{version}", string(byName["imported"].Space.TagFormat),
		"an imported package keeps its repository-local space and package layers")
	assert.False(t, byName["imported"].Counts(filepath.ToSlash(filepath.Join(byName["imported"].Dir, "ignored.txt"))),
		"an imported package keeps its repository-local .dispatignore")

	assert.ElementsMatch(t, []ExcludedDir{
		{Space: "owned", Name: "filtered"},
		{Space: "imported", Name: "filtered"},
	}, excluded, "only the control-owned and explicitly imported .dispatexclude files apply")
}

func TestResolvedWorkspaceSpaceConfigsNilWorkspaceKeepsLegacyFolderLayers(t *testing.T) {
	cfg := minimalConfig()
	root := writeModelRepo(t, cfg, "pkgs/core")
	writeJSON(t, filepath.Join(root, "pkgs", "dispat.json"),
		SpaceFile{Scripts: map[string]Script{"folder-only": {"echo folder"}}})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	spaces, err := ResolvedWorkspaceSpaceConfigs(loaded, root, nil)
	require.NoError(t, err)
	require.Len(t, spaces, 1)
	_, ok := spaces[0].Script("folder-only")
	assert.True(t, ok)
}

// TestWorkspaceNestedOwnerFolderConfigCannotPreemptOwnership ensures the
// ownership check remains the diagnostic boundary when a control space walks
// into an undeclared nested Git repository. Files in that other repository
// have no authority to change the control configuration first.
func TestWorkspaceNestedOwnerFolderConfigCannotPreemptOwnership(t *testing.T) {
	listed := workspaceRepo(t, "listed", nil)
	root, path := workspaceControl(t, map[string]string{"listed": listed}, File{
		Polyrepo: true,
		Spaces: map[string]SpaceConfig{
			"tools": {Path: PathList{"tools"}},
		},
	})
	nested := filepath.Join(root, "tools", "rogue")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	workspaceGit(t, nested, "init", "-b", "main")
	workspaceGit(t, nested, "config", "user.name", "Test")
	workspaceGit(t, nested, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(nested, "README.md"), []byte("rogue"), 0o644))
	writeJSON(t, filepath.Join(nested, "dispat.json"), map[string]any{"unknown": true})
	workspaceGit(t, nested, "add", ".")
	workspaceGit(t, nested, "commit", "-m", "test: nested repository")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.Error(t, err)
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
	assert.Contains(t, err.Error(), "nested Git repository")
	assert.NotContains(t, err.Error(), "unknown key")
}
