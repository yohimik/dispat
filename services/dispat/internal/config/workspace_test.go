package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func requireWorkspaceDiagnostic(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic interface{ DiagnosticCode() string }
	require.True(t, errors.As(err, &diagnostic), "error has no structured diagnostic: %v", err)
	assert.Equal(t, code, diagnostic.DiagnosticCode())
	assert.Equal(t, code, DiagnosticCode(err))
}

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
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Repository)
	wantRoot, err := filepath.EvalSymlinks(filepath.Join(root, "sources", "sdk"))
	require.NoError(t, err)
	assert.Equal(t, wantRoot, pkgs[0].RepoRoot)
	repo := workspace.RepositoryForPackage(pkgs[0])
	require.NotNil(t, repo)
	assert.Equal(t, "sdk", repo.Name)
	assert.Equal(t, "sources/sdk", repo.GitlinkPath)
}

func TestComposeCentralWorkspaceRejectsUnicodeAliasSubmoduleIdentities(t *testing.T) {
	sigma := workspaceRepo(t, "sigma", nil)
	root, path := workspaceControl(t, map[string]string{"Σ": sigma}, File{
		Polyrepo: true, Packages: map[string]PackageConfig{"sigma": {Path: "sources/Σ/pkgs/sigma"}},
	})
	workspaceGit(t, root, "config", "--file", filepath.Join(root, ".gitmodules"), "submodule.ς.path", "sources/final-sigma")
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "chore: declare colliding submodule identity")
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.ErrorContains(t, err, "duplicate submodule name")
}

func TestComposeCentralWorkspaceFindsUnicodeAliasSubmodule(t *testing.T) {
	sigma := workspaceRepo(t, "sigma", nil)
	root, path := workspaceControl(t, map[string]string{"Σ": sigma}, File{
		Polyrepo: true, Packages: map[string]PackageConfig{"sigma": {Path: "sources/Σ/pkgs/sigma"}},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	repo := workspace.RepositoryByName("ς")
	require.NotNil(t, repo)
	assert.Equal(t, "Σ", repo.Name)
}

func TestComposeWorkspaceNormalizesGitlinkPathForTreeLookup(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
	})
	modulesPath := filepath.Join(root, ".gitmodules")
	modules, err := os.ReadFile(modulesPath)
	require.NoError(t, err)
	modules = []byte(strings.Replace(string(modules), "path = sources/sdk", "path = ./sources//sdk", 1))
	require.NoError(t, os.WriteFile(modulesPath, modules, 0o644))
	workspaceGit(t, root, "add", ".gitmodules")
	workspaceGit(t, root, "commit", "-m", "chore: preserve logical gitlink path")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	repo := workspace.RepositoryByName("sdk")
	require.NotNil(t, repo)
	assert.Equal(t, "sources/sdk", repo.GitlinkPath)
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
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
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

func TestComposeWorkspaceCentralSharedSpaceSpansSourceOwners(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	api := workspaceRepo(t, "api", nil)
	cfg := File{Polyrepo: true, Spaces: map[string]SpaceConfig{
		"fleet": {
			Path:       PathList{"sources/sdk/pkgs", "sources/api/pkgs"},
			Versioning: VersioningFixed,
		},
	}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "api": api}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	assert.Equal(t, "sdk", byName["sdk"].Repository)
	assert.Equal(t, "api", byName["api"].Repository)
	assert.Same(t, byName["sdk"].Space, byName["api"].Space)
	assert.Equal(t, "fleet", byName["sdk"].VersionGroupIdentity())
	assert.Equal(t, byName["sdk"].VersionGroupIdentity(), byName["api"].VersionGroupIdentity())
}

func TestComposeWorkspaceImportedGroupsWithSameNameStayRepositoryLocal(t *testing.T) {
	local := func(name string) *File {
		return &File{Spaces: map[string]SpaceConfig{
			"shared": {Path: PathList{"pkgs"}, Versioning: VersioningFixed},
		}, Packages: map[string]PackageConfig{name: {}}}
	}
	sdk := workspaceRepo(t, "sdk", local("sdk"))
	api := workspaceRepo(t, "api", local("api"))
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "api": api}, File{
		Configs: []string{"sources/sdk/dispat.json", "sources/api/dispat.json"},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	assert.Equal(t, "shared", byName["sdk"].VersionGroupName())
	assert.Equal(t, "shared", byName["api"].VersionGroupName())
	assert.NotEqual(t, byName["sdk"].VersionGroupIdentity(), byName["api"].VersionGroupIdentity())
}

func TestComposeWorkspaceRejectsControlPackageWrappingSource(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{"wrapper": {Path: "sources"}}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spans source repository")
}

func TestComposeWorkspaceRejectsCentralPackageSrcSymlinkCrossingRepository(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{
		"wrapper": {Path: "tools/wrapper", Src: "src"},
	}}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tools", "wrapper"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "sources", "sdk", "pkgs", "sdk"),
		filepath.Join(root, "tools", "wrapper", "src")))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "feat: add wrapper")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "src path")
	assert.Contains(t, err.Error(), "crosses repository ownership")
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
}

func TestComposeWorkspaceRejectsImportedPackageSrcSymlinkEscape(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk", Src: "src"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	external := t.TempDir()
	require.NoError(t, os.Symlink(external, filepath.Join(sdk, "pkgs", "sdk", "src")))
	workspaceGit(t, sdk, "add", ".")
	workspaceGit(t, sdk, "commit", "-m", "feat: add source alias")
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Configs: []string{"sources/sdk/dispat.json"}})

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "path or src escapes its owner root")
}

func TestComposeWorkspaceRejectsImportedPackagePathSymlinkEscape(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "alias"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	external := t.TempDir()
	require.NoError(t, os.Symlink(external, filepath.Join(sdk, "alias")))
	workspaceGit(t, sdk, "add", ".")
	workspaceGit(t, sdk, "commit", "-m", "feat: add package alias")
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Configs: []string{"sources/sdk/dispat.json"}})

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "path or src escapes its owner root")
}

func TestComposeWorkspaceRejectsCentralPackageInUnlistedNestedRepository(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"nested": {Path: "vendor/nested/pkg"}},
	})
	nested := filepath.Join(root, "vendor", "nested")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, "pkg"), 0o755))
	workspaceGit(t, nested, "init", "-b", "main")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "unlisted nested Git repository")
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
}

func TestComposeWorkspaceRejectsImportedPackageInUnlistedNestedRepository(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Configs: []string{"sources/sdk/dispat.json"}})
	packageRoot := filepath.Join(root, "sources", "sdk", "pkgs", "sdk")
	workspaceGit(t, packageRoot, "init", "-b", "main")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "unlisted nested Git repository")
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
}

func TestComposeWorkspaceRejectsSrcInUnlistedNestedRepository(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"tool": {Path: "tools/tool", Src: "src"}},
	})
	src := filepath.Join(root, "tools", "tool", "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	workspaceGit(t, src, "init", "-b", "main")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "src path")
	require.ErrorContains(t, err, "unlisted nested Git repository")
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
}

func TestComposeWorkspaceRejectsSymlinkedGitMarker(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"tool": {Path: "tools/tool"}},
	})
	tool := filepath.Join(root, "tools", "tool")
	require.NoError(t, os.MkdirAll(tool, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, ".git"), filepath.Join(tool, ".git")))

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.ErrorContains(t, err, "unsupported .git marker")
	requireWorkspaceDiagnostic(t, err, DiagnosticOwnershipInvalid)
}

func TestComposeWorkspaceRequiresExactRepositoryOverrideIdentity(t *testing.T) {
	enabled := true
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		RepositoryOverrides: map[string]RepositoryOverrideConfig{
			"SDK": {Commit: &CommitConfig{Enabled: &enabled}},
		},
	}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.ErrorContains(t, err, `unknown source repository "SDK"`)
	requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
}

func TestComposeWorkspaceRejectsRepositoryOverrideCaseAlias(t *testing.T) {
	enabled := true
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		RepositoryOverrides: map[string]RepositoryOverrideConfig{
			"sdk": {Commit: &CommitConfig{Enabled: &enabled}},
			"SDK": {Commit: &CommitConfig{Branch: "main"}},
		},
	}
	_, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	_, err := Load(path, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repositoryOverrides")
	assert.Contains(t, err.Error(), "SDK")
	assert.Contains(t, err.Error(), "sdk")
}

func TestComposeWorkspaceResolvesImportsFromAliasedControlPath(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Configs: []string{"sources/sdk/dispat.json"}})
	alias := filepath.Join(t.TempDir(), "control-alias")
	require.NoError(t, os.Symlink(root, alias))
	aliasPath := filepath.Join(alias, filepath.Base(path))

	loaded, err := Load(aliasPath, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, aliasPath, alias, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, alias, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Repository)
}

func TestComposeWorkspaceRejectsResolvedSubmoduleRootOutsideControl(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Polyrepo: true, Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}}})
	checkout := filepath.Join(root, "sources", "sdk")
	require.NoError(t, os.RemoveAll(checkout))
	require.NoError(t, os.Symlink(sdk, checkout))

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.ErrorContains(t, err, "resolves outside the control workspace")
}

func TestComposeWorkspaceRejectsAliasedSubmoduleRoots(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	api := workspaceRepo(t, "api", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk, "api": api},
		File{Polyrepo: true, Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}}})
	apiCheckout := filepath.Join(root, "sources", "api")
	require.NoError(t, os.RemoveAll(apiCheckout))
	require.NoError(t, os.Symlink(filepath.Join(root, "sources", "sdk"), apiCheckout))

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.ErrorContains(t, err, "submodule roots overlap")
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
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
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
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Name)
}

func TestComposeWorkspaceResolvesConfigsFromDeclaringRootRef(t *testing.T) {
	sdkCfg := &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}}
	sdk := workspaceRepo(t, "sdk", sdkCfg)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cfg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cfg", "imports.json"),
		[]byte(`{"configs":["../sources/sdk/dispat.json"]}`), 0o644))
	require.NoError(t, os.WriteFile(path, []byte(`{"$ref":"./cfg/imports.json"}`), 0o644))
	workspaceGit(t, root, "add", ".")
	workspaceGit(t, root, "commit", "-m", "chore: declare imports in fragment")

	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Repository)
}

func TestWorkspaceImportsPreserveReferenceProvenance(t *testing.T) {
	write := func(t *testing.T, root, name, body string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}
	resolved := func(t *testing.T, root string, imports []workspaceImport) []string {
		t.Helper()
		out := make([]string, len(imports))
		for i := range imports {
			path, err := resolveImportPath(root, imports[i].Base, imports[i].Path)
			require.NoError(t, err)
			out[i] = path
		}
		return out
	}
	canonicalRoot := func(t *testing.T) string {
		t.Helper()
		root, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		return root
	}

	for _, tc := range []struct {
		name string
		file string
		body string
	}{
		{"json", "dispat.json", `{"$ref":"./cfg/imports.json"}`},
		{"yaml", "dispat.yaml", "$ref: ./cfg/imports.json\n"},
		{"toml", "dispat.toml", `"$ref" = "./cfg/imports.json"`},
	} {
		t.Run("root ref "+tc.name, func(t *testing.T) {
			root := canonicalRoot(t)
			path := write(t, root, tc.file, tc.body)
			write(t, root, "cfg/imports.json", `{"configs":["../sources/lib/dispat.json"]}`)
			imports, err := workspaceImports(&File{Configs: []string{"../sources/lib/dispat.json"}}, path, root, nil)
			require.NoError(t, err)
			assert.Equal(t, []string{filepath.Join(root, "sources", "lib", "dispat.json")}, resolved(t, root, imports))
		})
	}

	t.Run("configs value single ref", func(t *testing.T) {
		root := canonicalRoot(t)
		path := write(t, root, "dispat.json", `{"configs":{"$ref":"./a/list.json"}}`)
		write(t, root, "a/list.json", `["../sources/a/dispat.json"]`)
		imports, err := workspaceImports(&File{Configs: []string{"../sources/a/dispat.json"}}, path, root, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(root, "sources", "a", "dispat.json")}, resolved(t, root, imports))
	})

	t.Run("configs value list refs retain each base", func(t *testing.T) {
		root := canonicalRoot(t)
		path := write(t, root, "dispat.json", `{"configs":{"$ref":["./a/list.json","./b/list.yaml"]}}`)
		write(t, root, "a/list.json", `["../sources/a/dispat.json"]`)
		write(t, root, "b/list.yaml", "- ../sources/b/dispat.yaml\n")
		imports, err := workspaceImports(&File{Configs: []string{
			"../sources/a/dispat.json", "../sources/b/dispat.yaml",
		}}, path, root, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			filepath.Join(root, "sources", "a", "dispat.json"),
			filepath.Join(root, "sources", "b", "dispat.yaml"),
		}, resolved(t, root, imports))
	})

	t.Run("direct override wins root ref", func(t *testing.T) {
		root := canonicalRoot(t)
		path := write(t, root, "dispat.json", `{"$ref":"./cfg/base.json","configs":["sources/direct/dispat.json"]}`)
		write(t, root, "cfg/base.json", `{"configs":["../sources/referenced/dispat.json"]}`)
		imports, err := workspaceImports(&File{Configs: []string{"sources/direct/dispat.json"}}, path, root, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(root, "sources", "direct", "dispat.json")}, resolved(t, root, imports))
	})
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
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control HEAD pins")
	requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	advanced := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, map[string][]string{"sdk": {"older", advanced}}, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace)
	assert.False(t, workspace.IsInheritedPinsEnabled())
	var resolved []string
	workspace, err = ComposeWorkspaceWithPinResolver(t.Context(), loaded, path, root, nil, nil, func(repository string) ([]string, error) {
		resolved = append(resolved, repository)
		return []string{advanced}, nil
	})
	require.NoError(t, err)
	assert.True(t, workspace.IsInheritedPinsEnabled())
	assert.Equal(t, []string{"sdk", "sdk"}, resolved,
		"the live pin is read before and after the checkout's HEAD, and the two reads agree")
	assert.Equal(t, advanced, workspace.RepositoryByName("sdk").CompositionHead)
	assert.Equal(t, strings.TrimSpace(workspaceGit(t, root, "rev-parse", "HEAD")),
		workspace.RepositoryByName(ControlRepository).CompositionHead)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, map[string][]string{"sdk": {"wrong"}}, nil)
	require.Error(t, err)
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
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting imported configs")
	requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
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
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicates baseline")
	requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
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
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)
	for _, baseline := range loaded.RepositoryBaselines {
		assert.Len(t, baseline.Revision, 40)
	}
}

func TestComposeWorkspaceRequiresExactBaselineRepositoryIdentity(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	cfg := File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk/pkgs/sdk"}},
		RepositoryBaselines: []RepositoryBaselineConfig{
			{Consumer: "consumer", ReleaseTag: "sdk@1.0.0", Repository: "SDK", Revision: "HEAD"},
		},
	}
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, cfg)
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	_, err = ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.ErrorContains(t, err, `unknown repository "SDK"`)
	requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
}
