package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestComposeWorkspaceRejectsCorruptSourceCheckouts distinguishes failures in
// the registered source itself from ordinary pin drift. A source must remain
// a complete Git worktree with a HEAD at the path recorded by .gitmodules,
// and that path must still be a gitlink in the accepted control snapshot.
func TestComposeWorkspaceRejectsCorruptSourceCheckouts(t *testing.T) {
	t.Run("registered path is not an initialized repository", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		})
		checkout := filepath.Join(root, "sources", "sdk")
		require.NoError(t, os.RemoveAll(checkout))
		require.NoError(t, os.MkdirAll(checkout, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(checkout, ".git"), []byte("invalid gitfile\n"), 0o644))

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `submodule "sdk" is not initialized`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("registered path is not the committed gitlink", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		})
		workspaceGit(t, root, "clone", "-q", sdk, "sources/relocated-sdk")
		modulesPath := filepath.Join(root, ".gitmodules")
		contents, err := os.ReadFile(modulesPath)
		require.NoError(t, err)
		contents = []byte(strings.Replace(string(contents), "path = sources/sdk", "path = sources/relocated-sdk", 1))
		require.NoError(t, os.WriteFile(modulesPath, contents, 0o644))

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `source repository "sdk" is not pinned by control HEAD`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("checkout has no head", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		})
		checkout := filepath.Join(root, "sources", "sdk")
		require.NoError(t, os.RemoveAll(checkout))
		require.NoError(t, os.MkdirAll(checkout, 0o755))
		workspaceGit(t, checkout, "init", "-b", "main")

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `source repository "sdk" has no HEAD`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("imported repository is shallow", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{
			"sdk": {Path: "pkgs/sdk"},
		}})
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Configs:  []string{"sources/sdk/dispat.json"},
			Packages: map[string]PackageConfig{"control-tool": {Path: "tools/control-tool"}},
		})
		require.NoError(t, os.MkdirAll(filepath.Join(root, "tools", "control-tool"), 0o755))
		checkout := filepath.Join(root, "sources", "sdk")
		gitDir := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "--git-dir"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(checkout, gitDir)
		}
		head := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "shallow"), []byte(head+"\n"), 0o644))

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `repository "sdk"`)
		require.ErrorContains(t, err, "is shallow; complete history is required")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})
}

// TestDiscoverWorkspaceRejectsCanonicalOwnershipFailures exercises package
// declarations that exist at discovery time but resolve outside their owner.
// Symlinks cannot turn a control declaration into an external package or
// move an imported package's source scope into another repository.
func TestDiscoverWorkspaceRejectsCanonicalOwnershipFailures(t *testing.T) {
	t.Run("central package path escapes through symlink", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{
				"escaped": {Path: "tools/escaped"},
			},
		})
		external := filepath.Join(t.TempDir(), "escaped")
		require.NoError(t, os.MkdirAll(external, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "tools"), 0o755))
		require.NoError(t, os.Symlink(external, filepath.Join(root, "tools", "escaped")))

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
		require.ErrorContains(t, err, `package "escaped" path`)
		require.ErrorContains(t, err, "escapes the control workspace")
		requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
	})

	t.Run("imported source scope crosses into another source", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{
			"sdk": {Path: "pkgs/sdk", Src: "src"},
		}})
		api := workspaceRepo(t, "api", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk, "api": api}, File{
			Configs:  []string{"sources/sdk/dispat.json"},
			Packages: map[string]PackageConfig{"api": {Path: "sources/api/pkgs/api"}},
		})
		sdkCheckout := filepath.Join(root, "sources", "sdk")
		require.NoError(t, os.Symlink(filepath.Join(root, "sources", "api", "pkgs", "api"),
			filepath.Join(sdkCheckout, "pkgs", "sdk", "src")))

		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
		require.ErrorContains(t, err, `imported repository "sdk" package "sdk" path or src escapes its owner root`)
		requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
	})

	t.Run("missing central package path fails discovery", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{
				"missing": {Path: "sources/sdk/pkgs/missing"},
			},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})

	t.Run("missing imported source scope fails discovery", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{
			"sdk": {Path: "pkgs/sdk", Src: "missing-src"},
		}})
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Configs:  []string{"sources/sdk/dispat.json"},
			Packages: map[string]PackageConfig{"control-tool": {Path: "tools/control-tool"}},
		})
		require.NoError(t, os.MkdirAll(filepath.Join(root, "tools", "control-tool"), 0o755))
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing-src")
	})
}

// TestWorkspaceNilLookupMethods documents the nil-safe query contract used by
// callers while optional composition is disabled or package ownership cannot
// be resolved.
func TestWorkspaceNilLookupMethods(t *testing.T) {
	var workspace *Workspace
	assert.Nil(t, workspace.RepositoryForPackage(nil))
	assert.Nil(t, workspace.RepositoryByName("sdk"))
	assert.Nil(t, workspace.RepositoryForDir(t.TempDir()))

	workspace = newWorkspace(t.TempDir(), []Repository{{Name: ControlRepository, Control: true}}, nil)
	assert.Nil(t, workspace.RepositoryForPackage(nil))
	assert.Nil(t, workspace.RepositoryForPackage(&model.Package{Repository: "missing"}))
	assert.Nil(t, workspace.RepositoryByName("missing"))
}
