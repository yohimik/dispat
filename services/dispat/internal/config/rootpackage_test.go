package config

// The repository root as a standalone package's folder: what a `packages`
// entry naming "." resolves to, what the root configuration file is to such a
// package, and the one setting the root folder cannot honour. The space half
// of the same question (a space whose path is ".") is covered by
// TestSpacePathValidation and by the integration suite, since what a space
// rooted at the repository does is walk folders rather than merge layers.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
)

// rootPackageConfig is the smallest repository whose one package is the
// repository itself: no space, one standalone entry, and the flow every
// package of these tests releases through.
func rootPackageConfig(path string) File {
	return File{
		Scripts: map[string]Script{"build": {"echo b"}},
		Packages: map[string]PackageConfig{
			"app": {Path: path, Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
	}
}

// TestRootPackageIsTheRepositoryFolder: a standalone entry may name the
// repository root, spelled either way, and the package it declares is the
// repository itself.
func TestRootPackageIsTheRepositoryFolder(t *testing.T) {
	for name, path := range map[string]string{
		"a bare dot":       ".",
		"a trailing slash": "./",
		"a cleaned path":   "sub/..",
	} {
		t.Run(name, func(t *testing.T) {
			root := writeModelRepo(t, rootPackageConfig(path), "sub")
			pkgs, err := discoverPackages(t, root)
			require.NoError(t, err)
			require.Len(t, pkgs, 1)
			assert.Equal(t, "app", pkgs[0].Name)
			assert.Equal(t, root, pkgs[0].Dir, "the package folder is the repository root")
			assert.Equal(t, root, pkgs[0].ScopeDir(), "so is the folder its files are counted under")
		})
	}
}

// TestRootPackageDoesNotReadTheRootConfigAsItsOwnLayer: the file in the
// package's folder is the root configuration file, so it is not merged in as
// the package's nearest layer. Two things prove it: the repository-wide keys
// that file carries do not have to be legal at package level, and the entry
// stays the nearest word about the package rather than being overruled by the
// file that holds it.
func TestRootPackageDoesNotReadTheRootConfigAsItsOwnLayer(t *testing.T) {
	cfg := rootPackageConfig(".")
	cfg.Spaces = map[string]SpaceConfig{"libs": {Path: PathList{"packages"}}}
	cfg.TagFormat = "root-{name}@{version}"
	cfg.Packages["app"] = PackageConfig{
		Path:      ".",
		Flow:      &SpaceFlowConfig{Build: []string{"build"}},
		TagFormat: "entry-{name}@{version}",
	}
	root := writeModelRepo(t, cfg, "packages/core")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err, "the root file's repository-wide keys are not held to the package level")
	byName := packagesByName(pkgs)
	require.Contains(t, byName, "app")
	require.Contains(t, byName, "core", "a space keeps its own packages beside the root one")
	assert.Equal(t, "entry-{name}@{version}", string(byName["app"].Space.TagFormat),
		"the entry is the nearest layer; the root file is not read again as the package's folder file")
}

// TestRootPackageRefusesRevertOnFail: reverting the root package's folder
// would discard the whole working tree, so the setting is refused wherever
// the true it would act on was written, and an explicit false on the entry is
// how a repository keeps the setting for everything else.
func TestRootPackageRefusesRevertOnFail(t *testing.T) {
	t.Run("written on the entry", func(t *testing.T) {
		cfg := rootPackageConfig(".")
		cfg.Packages["app"] = PackageConfig{Path: ".", RevertOnFail: models.Bool(true)}
		root := writeModelRepo(t, cfg)
		_, err := discoverPackages(t, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `package "app"`)
		assert.Contains(t, err.Error(), "revertOnFail cannot be used by a package whose path is the repository root")
	})

	t.Run("inherited from the root file", func(t *testing.T) {
		cfg := rootPackageConfig(".")
		cfg.RevertOnFail = models.Bool(true)
		root := writeModelRepo(t, cfg)
		_, err := discoverPackages(t, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "revertOnFail cannot be used by a package whose path is the repository root")
	})

	t.Run("contradicted on the entry", func(t *testing.T) {
		cfg := rootPackageConfig(".")
		cfg.RevertOnFail = models.Bool(true)
		cfg.Packages["app"] = PackageConfig{Path: ".", RevertOnFail: models.Bool(false)}
		root := writeModelRepo(t, cfg)
		pkgs, err := discoverPackages(t, root)
		require.NoError(t, err)
		require.Len(t, pkgs, 1)
		assert.False(t, pkgs[0].Space.RevertOnFail)
	})

	t.Run("a package inside a folder keeps it", func(t *testing.T) {
		cfg := rootPackageConfig("tools/cli")
		cfg.Packages["app"] = PackageConfig{Path: "tools/cli", RevertOnFail: models.Bool(true)}
		root := writeModelRepo(t, cfg, "tools/cli")
		pkgs, err := discoverPackages(t, root)
		require.NoError(t, err)
		require.Len(t, pkgs, 1)
		assert.True(t, pkgs[0].Space.RevertOnFail, "only the root folder cannot honour the setting")
	})
}

// TestRootPackageKeepsTheOtherPathRefusals: the root is legal because it is
// inside the repository, and nothing else about a standalone path changed.
func TestRootPackageKeepsTheOtherPathRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ path, want string }{
		"absolute":            {string(filepath.Separator) + "abs", "must be a repository-relative path"},
		"the parent":          {"..", "escapes the repository root"},
		"leaving through dot": {"../outside", "escapes the repository root"},
		"climbing back out":   {"sub/../..", "escapes the repository root"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadModel(t, rootPackageConfig(tc.path), "sub")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), `packages["app"]`)
		})
	}
}

// TestRootPackageNarrowedBySrcAndIgnore: `src` and `ignore` narrow a root
// package exactly as they narrow any package, and a pattern naming a folder a
// deeper package already owns is redundant rather than wrong.
func TestRootPackageNarrowedBySrcAndIgnore(t *testing.T) {
	cfg := rootPackageConfig(".")
	cfg.Spaces = map[string]SpaceConfig{"libs": {Path: PathList{"packages"}}}
	cfg.Packages["app"] = PackageConfig{
		Path:   ".",
		Flow:   &SpaceFlowConfig{Build: []string{"build"}},
		Src:    "src",
		Ignore: []string{"packages/"},
	}
	root := writeModelRepo(t, cfg, "packages/core", "src")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	require.Contains(t, byName, "app")
	assert.Equal(t, filepath.Join(root, "src"), byName["app"].ScopeDir(),
		"src narrows the root package to a folder inside the repository")
	assert.True(t, byName["app"].Ignore.IsIgnored(filepath.ToSlash(filepath.Join(root, "packages", "core", "main.txt"))),
		"an ignore pattern covering a deeper package's folder is redundant, not an error")
}

// TestRootPackageBesideARootSpace: a space rooted at the repository and a
// package rooted there are the pair a folder holding both would be. Nothing
// refuses it, for the reason nothing refuses a standalone package whose
// folder holds a space's packages: the folder makes the space's packages
// exist, and the longest path prefix decides every file.
func TestRootPackageBesideARootSpace(t *testing.T) {
	cfg := rootPackageConfig(".")
	cfg.Spaces = map[string]SpaceConfig{"all": {Path: PathList{"."}}}
	root := writeModelRepo(t, cfg, "core")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	require.Contains(t, byName, "app")
	require.Contains(t, byName, "core")
	assert.Equal(t, root, byName["app"].Dir)
	assert.Equal(t, filepath.Join(root, "core"), byName["core"].Dir)
}

// TestRootPackageInAFolderWithItsOwnFileStillReadsIt: the skip is the root
// folder's alone. A standalone package one folder down still gets its own
// in-folder configuration file as its nearest layer, which is what the skip
// would have taken away had it been written as "a standalone package has no
// folder file".
func TestRootPackageInAFolderWithItsOwnFileStillReadsIt(t *testing.T) {
	cfg := rootPackageConfig("tools/cli")
	cfg.Packages["app"] = PackageConfig{
		Path: "tools/cli", Flow: &SpaceFlowConfig{Build: []string{"build"}},
		TagFormat: "entry-{name}@{version}",
	}
	root := writeModelRepo(t, cfg, "tools/cli")
	writePackageFile(t, root, filepath.Join("tools", "cli"),
		PackageConfig{TagFormat: "file-{name}@{version}"})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "file-{name}@{version}", string(pkgs[0].Space.TagFormat),
		"a package folder that is not the repository root keeps its own file as the nearest layer")
}

// TestRootPackageInAnImportedSourceConfig: a source repository imported into
// a fleet may be a single-package repository. Its own configuration names its
// own root, so the package is that repository and the repository that owns it
// is the source, not the control checkout it sits inside.
func TestRootPackageInAnImportedSourceConfig(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{"sdk": {Path: "."}}})
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Configs: []string{"sources/sdk/dispat.json"},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)

	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	wantDir, err := filepath.EvalSymlinks(filepath.Join(root, "sources", "sdk"))
	require.NoError(t, err)
	assert.Equal(t, "sdk", pkgs[0].Repository)
	assert.Equal(t, wantDir, pkgs[0].Dir, `"." inside an imported config is that repository's root`)
}

// TestCentralWorkspacePackageAtASourceRoot: a control repository configuring
// its sources centrally names them by their folder under the control
// checkout, which is a source repository's root rather than ".". That path is
// unaffected by the root being a legal one, and the package still belongs to
// the repository whose checkout holds it.
func TestCentralWorkspacePackageAtASourceRoot(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Polyrepo: true,
		Packages: map[string]PackageConfig{"sdk": {Path: "sources/sdk"}},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)

	pkgs, _, _, err := DiscoverWorkspace(loaded, root, workspace)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "sdk", pkgs[0].Repository)
}

// TestRootPackageMayNotSpanASourceRepository: a repository whose root is a
// package may not also hold another repository's checkout. That is the
// existing ownership rule rather than a rule about the root: a package's
// folder belongs to one repository, and the control checkout's root holds the
// sources beneath it.
func TestRootPackageMayNotSpanASourceRepository(t *testing.T) {
	sdk := workspaceRepo(t, "sdk", &File{Packages: map[string]PackageConfig{"sdk": {Path: "pkgs/sdk"}}})
	root, path := workspaceControl(t, map[string]string{"sdk": sdk}, File{
		Configs:  []string{"sources/sdk/dispat.json"},
		Packages: map[string]PackageConfig{"control": {Path: "."}},
	})
	loaded, err := Load(path, nil)
	require.NoError(t, err)
	workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
	require.NoError(t, err)

	_, _, _, err = DiscoverWorkspace(loaded, root, workspace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "control"`)
	assert.Contains(t, err.Error(), `spans source repository "sdk"`)
}

// TestRootPackageIgnoresAConfigFileOfAnotherName: the root file is skipped by
// the folder it sits in and not by its name, so a repository loaded through
// --config still leaves whatever other dispat file lies at the top out of the
// package's layers. Anything else would make the same repository resolve
// differently depending on which file the invocation named.
func TestRootPackageIgnoresAConfigFileOfAnotherName(t *testing.T) {
	cfg := rootPackageConfig(".")
	cfg.Packages["app"] = PackageConfig{
		Path: ".", Flow: &SpaceFlowConfig{Build: []string{"build"}},
		TagFormat: "entry-{name}@{version}",
	}
	root := writeModelRepo(t, cfg)
	require.NoError(t, os.Rename(filepath.Join(root, "dispat.json"), filepath.Join(root, "release.json")))
	writePackageFile(t, root, ".", PackageConfig{TagFormat: "file-{name}@{version}"})

	loaded, err := Load(filepath.Join(root, "release.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "entry-{name}@{version}", string(pkgs[0].Space.TagFormat),
		"the folder decides the skip, so no file at the top of the repository is a package layer")
}
