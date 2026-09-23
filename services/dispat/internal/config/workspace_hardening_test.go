package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeWorkspaceExcludesDisabledRepositoryBeforeCheckoutInspection(t *testing.T) {
	disabled := false
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo:            true,
		Configs:             []string{"sources/sdk/dispat.json"},
		Packages:            map[string]PackageConfig{"control-package": {Path: "ctl"}},
		RepositoryOverrides: map[string]RepositoryOverrideConfig{"sdk": {Enabled: &disabled}},
	})
	require.NoError(t, os.RemoveAll(filepath.Join(root, "sources", "sdk")))
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, workspace.Repositories, 1)
	assert.Equal(t, ControlRepository, workspace.Repositories[0].Name)
}

// TestComposeWorkspaceRefusesUnknownOverrideWhicheverWayItReads: an override
// naming no `.gitmodules` repository is a typo whichever value it carries. A
// misspelled exclusion that were ignored would release the repository it was
// written to hold back, which is the more expensive half of the mistake.
func TestComposeWorkspaceRefusesUnknownOverrideWhicheverWayItReads(t *testing.T) {
	enabled, disabled := true, false
	for name, override := range map[string]RepositoryOverrideConfig{
		"disabled":      {Enabled: &disabled},
		"enabled":       {Enabled: &enabled},
		"commit policy": {Commit: &CommitConfig{Remote: "upstream"}},
	} {
		t.Run(name, func(t *testing.T) {
			sdk := workspaceRepo(t, "sdk", nil)
			root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
				Polyrepo:            true,
				Packages:            map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
				RepositoryOverrides: map[string]RepositoryOverrideConfig{"Sdk": override},
			})
			loaded, err := Load(path, nil)
			require.NoError(t, err)
			_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown source repository")
			assert.Equal(t, DiagnosticComposition, DiagnosticCode(err))
		})
	}
}

// TestComposeWorkspaceExcludesDeclarationsOwnedByADisabledRepository: the
// control file's own space paths belong to the repository that holds them, so
// excluding the repository excludes the declaration. The boundary itself
// stays reserved, which is what keeps a control-owned package out of it.
func TestComposeWorkspaceExcludesDeclarationsOwnedByADisabledRepository(t *testing.T) {
	disabled := false
	sdk := workspaceRepo(t, "sdk", nil)
	app := workspaceRepo(t, "app", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "app": app}, File{
		Polyrepo: true,
		Spaces: map[string]SpaceConfig{
			"libraries": {Path: PathList{"sources/sdk/pkgs"}},
			"apps":      {Path: PathList{"sources/app/pkgs"}},
		},
		RepositoryOverrides: map[string]RepositoryOverrideConfig{"app": {Enabled: &disabled}},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"app"}, workspace.DisabledRepositoryNames())
	assert.Equal(t, []string{"apps"}, workspace.ExcludedSpaces())
	assert.NotContains(t, loaded.Spaces, "apps", "an excluded space declares nothing")
	repository, ok := workspace.ExcludedPackageRepository("app")
	assert.True(t, ok)
	assert.Equal(t, "app", repository)

	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Name)
	require.Len(t, workspace.DisabledRepositories(), 1)
	// Composition answers repository roots through the filesystem, so the
	// expectation is the canonical spelling: on macOS the temporary folder
	// sits behind the /var -> /private/var link.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalRoot, "sources", "app"), workspace.DisabledRepositories()[0].Root,
		"the excluded boundary remains reserved while discovery omits its packages")
}
