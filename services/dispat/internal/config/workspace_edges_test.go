package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComposeWorkspaceKeepsImportedConfigRepositoryContext proves that an
// explicitly imported file establishes its source repository context even
// when another config exists at that repository's conventional root. Package
// paths remain repository-relative and later consumers can recover the exact
// config that declared the package.
func TestComposeWorkspaceKeepsImportedConfigRepositoryContext(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{
		"implicit": {Path: "pkgs/implicit"},
	}})
	nestedConfig := filepath.Join(sdk, "config", "release.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(nestedConfig), 0o755))
	require.NoError(t, os.WriteFile(nestedConfig, []byte(`{
  "packages": {"sdk": {"path": "pkgs/sdk"}}
}`), 0o644))
	workspaceGit(t, sdk, "add", ".")
	workspaceGit(t, sdk, "commit", "-m", "chore: add explicit release config")

	root, path := workspaceControl(t, map[string]string{"sdk-source": sdk}, File{
		Configs: []string{"sources/sdk-source/config/release.json"},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspace(loaded, path, root, nil)
	require.NoError(t, err)

	repo := workspace.RepositoryByName("SDK-SOURCE")
	require.NotNil(t, repo)
	wantRoot, err := filepath.EvalSymlinks(filepath.Join(root, "sources", "sdk-source"))
	require.NoError(t, err)
	wantConfig, err := filepath.EvalSymlinks(filepath.Join(wantRoot, "config", "release.json"))
	require.NoError(t, err)
	assert.Equal(t, wantRoot, repo.Root)
	assert.Equal(t, wantConfig, repo.ConfigPath)
	assert.True(t, repo.Imported)

	packages, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, packages, 1, "the unimported root config must stay implicit and ignored")
	assert.Equal(t, "sdk", packages[0].Name)
	assert.Equal(t, "sdk-source", packages[0].Repository)
	assert.Same(t, repo, workspace.ConfigurationForPackage(packages[0]))
}

// TestComposeWorkspaceRejectsInconsistentImportedConfiguration exercises the
// boundaries that keep one control file authoritative for fleet composition.
// An import cannot recursively add a fleet, receive a central override, or
// come from an unregistered Git repository under the control checkout.
func TestComposeWorkspaceRejectsInconsistentImportedConfiguration(t *testing.T) {
	t.Run("nested fleet", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", &File{
			Configs:  []string{"another/dispat.json"},
			Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}},
		})
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Configs: []string{"sources/sdk/dispat.json"},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "nested workspace imports are not allowed")
		requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
	})

	t.Run("central override of imported owner", func(t *testing.T) {
		enabled := true
		sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{
			"sdk": {Path: "pkgs/sdk"},
		}})
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Configs: []string{"sources/sdk/dispat.json"},
			RepositoryOverrides: map[string]RepositoryOverrideConfig{
				"sdk": {Commit: &CommitConfig{Enabled: &enabled}},
			},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `repositoryOverrides["sdk"] cannot override imported repository config`)
		requireWorkspaceDiagnostic(t, err, DiagnosticComposition)
	})

	t.Run("unregistered nested repository", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		rogue := workspaceRepo(t, "rogue", &File{Packages: map[string]PackageConfig{
			"rogue": {Path: "pkgs/rogue"},
		}})
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		workspaceGit(t, root, "clone", "-q", rogue, "vendor/rogue")
		loaded := &File{Configs: []string{"vendor/rogue/dispat.json"}}
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "which is not an initialized .gitmodules repository")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("invalid imported document", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		require.NoError(t, os.WriteFile(filepath.Join(sdk, "dispat.json"), []byte(`{"packages":`), 0o644))
		workspaceGit(t, sdk, "add", ".")
		workspaceGit(t, sdk, "commit", "-m", "chore: add malformed config")
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Configs: []string{"sources/sdk/dispat.json"},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imported config")
		assert.Contains(t, err.Error(), filepath.Join("sources", "sdk", "dispat.json"))
	})
}

// TestComposeWorkspaceValidatesRepositoryBaselineObjects keeps explicit
// cross-repository evidence tied to one exact, reachable commit and refuses
// malformed or duplicate evidence before planning can consume it.
func TestComposeWorkspaceValidatesRepositoryBaselineObjects(t *testing.T) {
	t.Run("all tuple fields are required", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}},
			RepositoryBaselines: []RepositoryBaselineConfig{{
				Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "sdk",
			}},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "consumer, releaseTag, repository and revision are required")
		requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
	})

	t.Run("consumer identity is case insensitive for duplicate detection", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}},
			RepositoryBaselines: []RepositoryBaselineConfig{
				{Consumer: "App", ReleaseTag: "app@1.0.0", Repository: "sdk", Revision: "HEAD"},
				{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "sdk", Revision: "HEAD~0"},
			},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "duplicates baseline")
		requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
	})

	t.Run("revision must resolve in the named repository", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}},
			RepositoryBaselines: []RepositoryBaselineConfig{{
				Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "sdk", Revision: "missing-revision",
			}},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `revision "missing-revision" is not a commit in repository "sdk"`)
		requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
	})

	t.Run("revision must be reachable from the named repository head", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		workspaceGit(t, sdk, "checkout", "--orphan", "abandoned")
		workspaceGit(t, sdk, "rm", "-q", "-rf", ".")
		require.NoError(t, os.WriteFile(filepath.Join(sdk, "abandoned.txt"), []byte("unreachable"), 0o644))
		workspaceGit(t, sdk, "add", ".")
		workspaceGit(t, sdk, "commit", "-m", "chore: abandoned history")
		unreachable := strings.TrimSpace(workspaceGit(t, sdk, "rev-parse", "HEAD"))
		workspaceGit(t, sdk, "checkout", "main")

		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
			Polyrepo: true,
			Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}},
			RepositoryBaselines: []RepositoryBaselineConfig{{
				Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "sdk", Revision: unreachable,
			}},
		})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "is not reachable from repository")
		requireWorkspaceDiagnostic(t, err, DiagnosticBoundary)
	})
}

// TestComposeWorkspaceRejectsInvalidModuleIdentities covers failures in the
// .gitmodules inventory itself. Logical identities are case insensitive, and
// every registered path must stay beneath the control repository.
func TestComposeWorkspaceRejectsInvalidModuleIdentities(t *testing.T) {
	t.Run("polyrepo requires a module inventory", func(t *testing.T) {
		root, path := workspaceControl(t, nil, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "has no .gitmodules")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("case alias", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		modulesPath := filepath.Join(root, ".gitmodules")
		contents, err := os.ReadFile(modulesPath)
		require.NoError(t, err)
		contents = append(contents, []byte("\n[submodule \"SDK\"]\n\tpath = sources/sdk-alias\n\turl = "+sdk+"\n")...)
		require.NoError(t, os.WriteFile(modulesPath, contents, 0o644))
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `duplicate submodule name "SDK"`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("path escape", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		modulesPath := filepath.Join(root, ".gitmodules")
		contents, err := os.ReadFile(modulesPath)
		require.NoError(t, err)
		contents = []byte(strings.Replace(string(contents), "path = sources/sdk", "path = ../sdk", 1))
		require.NoError(t, os.WriteFile(modulesPath, contents, 0o644))
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "path escapes control root")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("empty inventory", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		require.NoError(t, os.WriteFile(filepath.Join(root, ".gitmodules"), nil, 0o644))
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "read .gitmodules")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("missing checkout", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		require.NoError(t, os.RemoveAll(filepath.Join(root, "sources", "sdk")))
		loaded, err := Load(path, nil)
		require.NoError(t, err)
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `submodule "sdk" is missing or uninitialized`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})
}

// TestComposeWorkspaceRejectsInvalidControlAndImportRoots makes composition
// fail at the boundary that is actually invalid. Missing or unborn control
// repositories cannot supply a stable snapshot, and explicit imports cannot
// escape that snapshot or name files that do not exist.
func TestComposeWorkspaceRejectsInvalidControlAndImportRoots(t *testing.T) {
	t.Run("missing control root", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		workspace, err := ComposeWorkspace(&File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}}, filepath.Join(missing, "dispat.json"), missing, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "resolve control root")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("control root is not Git", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "dispat.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"polyrepo":true}`), 0o644))
		workspace, err := ComposeWorkspace(&File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}}, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `repository "control"`)
		require.ErrorContains(t, err, "is not initialized")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("control repository has no head", func(t *testing.T) {
		root := t.TempDir()
		workspaceGit(t, root, "init", "-b", "main")
		path := filepath.Join(root, "dispat.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"polyrepo":true}`), 0o644))
		workspace, err := ComposeWorkspace(&File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}}, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "control repository has no HEAD")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("import path escapes control root", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		loaded := &File{Configs: []string{"../outside/dispat.json"}}
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, "path escapes control root")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})

	t.Run("import file is missing", func(t *testing.T) {
		sdk := workspaceRepo(t, "sdk", nil)
		root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
		loaded := &File{Configs: []string{"sources/sdk/missing.json"}}
		workspace, err := ComposeWorkspace(loaded, path, root, nil)
		require.Nil(t, workspace)
		require.ErrorContains(t, err, `config "sources/sdk/missing.json"`)
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	})
}

// TestWorkspaceImportsRejectMalformedReferenceGraphs checks the error paths
// specific to resolving configs provenance. These errors must surface before
// an import can silently fall back to the control file's directory.
func TestWorkspaceImportsRejectMalformedReferenceGraphs(t *testing.T) {
	write := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	t.Run("missing root config", func(t *testing.T) {
		root := t.TempDir()
		_, err := workspaceImports(&File{}, filepath.Join(root, "missing.json"), root, nil)
		require.ErrorContains(t, err, "resolve configs declaration")
	})

	t.Run("invalid root reference", func(t *testing.T) {
		root := t.TempDir()
		path := write(t, root, "dispat.json", `{"$ref":42}`)
		_, err := workspaceImports(&File{}, path, root, nil)
		require.ErrorContains(t, err, "$ref must name a file or list of files")
	})

	t.Run("invalid configs reference", func(t *testing.T) {
		root := t.TempDir()
		path := write(t, root, "dispat.json", `{"configs":{"$ref":["list.json",42]}}`)
		_, err := workspaceImports(&File{}, path, root, nil)
		require.ErrorContains(t, err, "$ref[1] must name a file")
	})

	t.Run("missing configs reference document", func(t *testing.T) {
		root := t.TempDir()
		path := write(t, root, "dispat.json", `{"configs":{"$ref":"missing.json"}}`)
		_, err := workspaceImports(&File{}, path, root, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing.json")
	})

	t.Run("non-string configs list member", func(t *testing.T) {
		root := t.TempDir()
		path := write(t, root, "dispat.json", `{"configs":["source.json",{}]}`)
		_, err := workspaceImports(&File{}, path, root, nil)
		require.ErrorContains(t, err, "configs[1]")
	})
}

// TestComposeWorkspacePropagatesLivePinResolverFailure proves that malformed
// transient coordination cannot be bypassed with an otherwise valid static
// run pin. The source identity remains attached to the E330 diagnostic.
func TestComposeWorkspacePropagatesLivePinResolverFailure(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
	checkout := filepath.Join(root, "sources", "sdk")
	workspaceGit(t, checkout, "config", "user.name", "Test")
	workspaceGit(t, checkout, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "later.txt"), []byte("later"), 0o644))
	workspaceGit(t, checkout, "add", ".")
	workspaceGit(t, checkout, "commit", "-m", "feat: advance source")
	advanced := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
	loaded, err := Load(path, nil)
	require.NoError(t, err)

	var resolved []string
	workspace, err := ComposeWorkspaceWithPinResolver(t.Context(), loaded, path, root, nil,
		map[string][]string{"sdk": {advanced}}, func(repository string) ([]string, error) {
			resolved = append(resolved, repository)
			return nil, errors.New("coordinator record is malformed")
		})
	require.Nil(t, workspace)
	require.ErrorContains(t, err, `source repository "sdk": reading live run pin`)
	require.ErrorContains(t, err, "coordinator record is malformed")
	requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	assert.Equal(t, []string{"sdk"}, resolved)
}
