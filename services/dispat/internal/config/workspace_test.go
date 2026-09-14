package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workspaceGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
	return string(out)
}

func workspaceRepo(t *testing.T, name string, cfg *File) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", name), 0o755))
	workspaceGit(t, root, "init", "-b", "main")
	workspaceGit(t, root, "config", "user.name", "Test")
	workspaceGit(t, root, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkgs", name, "README.md"), []byte(name), 0o644))
	if cfg != nil {
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))
	}
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "feat: initial")
	return root
}

func workspaceControl(t *testing.T, modules map[string]string, cfg File) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "control")
	require.NoError(t, os.MkdirAll(root, 0o755))
	workspaceGit(t, root, "init", "-b", "main")
	workspaceGit(t, root, "config", "user.name", "Test")
	workspaceGit(t, root, "config", "user.email", "test@example.com")
	for name, source := range modules {
		workspaceGit(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "--name", name, source, filepath.Join("sources", name))
	}
	path := filepath.Join(root, "dispat.json")
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "chore: pin workspace")
	return root, path
}

func TestComposeCentralWorkspaceAssignsSourceOwner(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{
		"sdk": {Path: "sources/sdk/pkgs/sdk"},
	}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Repository)
	assert.Equal(t, filepath.Join(root, "sources", "sdk"), pkgs[0].RepoRoot)
	assert.Equal(t, "sdk", workspace.RepositoryForPackage(pkgs[0]).Name)
}

func TestComposeWorkspaceAllowsControlOwnedPackageBesideImportedSource(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	cfg := File{
		Configs:  []string{"sources/sdk/dispat.json"},
		Packages: map[string]PackageConfig{"tool": {Path: "tools/tool"}},
	}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tools", "tool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tools", "tool", "README.md"), []byte("tool"), 0o644))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "feat(tool): add control tool")
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	owners := map[string]string{}
	for _, p := range pkgs {
		owners[p.Name] = p.Repository
	}
	assert.Equal(t, ControlRepository, owners["tool"])
	assert.Equal(t, "sdk", owners["sdk"])
}

func TestComposeWorkspaceRejectsControlPackageWrappingSource(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{"wrapper": {Path: "sources"}}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spans source repository")
}

func TestComposeImportedWorkspaceMergesGraph(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	appCfg := &File{
		Packages:     map[string]PackageConfig{"app": {Path: "pkgs/app"}},
		Dependencies: Dependencies{{Consumer: "app", Provider: "sdk", External: true}},
	}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	app := workspaceRepo(t, "app", appCfg)
	control := File{Configs: []string{"sources/sdk/dispat.json", "sources/app/dispat.json", "sources/sdk/dispat.json"}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "app": app}, control)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	pkgs, deps, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	require.Len(t, deps, 1)
	assert.Equal(t, "app", deps[0].Consumer)
	assert.Equal(t, "sdk", deps[0].Provider)
	assert.Len(t, workspace.Repositories, 3, "control and two deduplicated imports")
}

func TestComposeWorkspaceResolvesConfigsFromWinningRefLayer(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Configs: []string{"sources/sdk/dispat.json"}})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "shared"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "shared", "base.json"),
		[]byte(`{"configs":["sources/sdk/dispat.json"]}`), 0o644))
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"$ref":"./shared/base.json","configs":["sources/sdk/dispat.json"]}`), 0o644))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "chore: override imported config")
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Name)
}

func TestComposeWorkspaceRejectsUnpinnedSource(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	checkout := filepath.Join(root, "sources", "sdk")
	workspaceGit(t, checkout, "config", "user.name", "Test")
	workspaceGit(t, checkout, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "later.txt"), []byte("later"), 0o644))
	workspaceGit(t, checkout, "add", ".")
	workspaceGit(t, checkout, "commit", "-m", "feat: later")
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspace(loaded, path, root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control HEAD pins")
}

func TestComposeWorkspaceRejectsConflictingConfigsForOneRepository(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	data, err := json.Marshal(sdkCfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sdk, "other.json"), data, 0o644))
	workspaceGit(t, sdk, "add", ".")
	workspaceGit(t, sdk, "commit", "-m", "chore: second config")
	control := File{Configs: []string{"sources/sdk/dispat.json", "sources/sdk/other.json"}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, control)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspace(loaded, path, root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting imported configs")
}

func TestComposeWorkspaceResolvesAndDeduplicatesBaseline(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		RepositoryBaselines: []RepositoryBaselineConfig{
			{Consumer: "consumer", ReleaseTag: "sdk@1.0.0", Repository: "sdk", Revision: "HEAD"},
			{Consumer: "consumer", ReleaseTag: "sdk@1.0.0", Repository: "sdk", Revision: "HEAD"},
		},
	}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspace(loaded, path, root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates baseline")
}

func TestComposeWorkspaceAllowsBaselinePerRepository(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	api := workspaceRepo(t, "api", nil)
	cfg := File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{
			"sdk": {Path: "sources/sdk/pkgs/sdk"},
			"api": {Path: "sources/api/pkgs/api"},
		},
		RepositoryBaselines: []RepositoryBaselineConfig{
			{Consumer: "consumer", ReleaseTag: "shared@1.0.0", Repository: "sdk", Revision: "HEAD"},
			{Consumer: "consumer", ReleaseTag: "shared@1.0.0", Repository: "api", Revision: "HEAD"},
		},
	}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "api": api}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)
	for _, baseline := range loaded.RepositoryBaselines {
		assert.Len(t, baseline.Revision, 40)
	}
}
