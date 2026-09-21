package config

// The two build keys, from three sides: what the ladder resolves them to,
// what shapes a single level may write, and the one question that needs every
// package to be known, whether two of them claimed one folder.
//
// Every claim is made against the resolved model, through Load and
// DiscoverPackages, because the merge has no seam of its own: the ladder is
// only observable in what a package ended up with.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// buildLadderConfig is the fixture the ladder claims are made on: a
// repository default, a space that overrides it, a space folder file that
// overrides the space, a package folder file that overrides everything, and
// two packages that inherit from different heights.
func buildLadderConfig() File {
	return File{
		Scripts:        map[string]Script{"build": {"echo b"}},
		BuildOutputs:   []string{"dist"},
		BuildPlatforms: []string{"linux/amd64"},
		Spaces: map[string]SpaceConfig{
			"libs": {
				Path:         PathList{"packages/libs"},
				Flow:         &SpaceFlowConfig{Build: []string{"build"}},
				BuildOutputs: []string{"build"},
			},
			"apps": {
				Path: PathList{"packages/apps"},
				Flow: &SpaceFlowConfig{Build: []string{"build"}},
			},
		},
		Packages: map[string]PackageConfig{"tool": {Path: "tools/tool"}},
	}
}

// TestBuildKeysResolveThroughTheLadder: the nearest level that states a list
// wins, whole, and a level that states nothing inherits. The two keys travel
// the ladder independently, so a package may narrow its platforms and keep
// the outputs it inherited.
func TestBuildKeysResolveThroughTheLadder(t *testing.T) {
	root := writeModelRepo(t, buildLadderConfig(),
		"packages/libs/core", "packages/libs/utils", "packages/apps/app", "tools/tool")
	// The space folder's file outranks the root file's space entry, and its
	// own `packages` entry is written raw because an empty list is how a
	// package opts out and the marshaller drops one.
	writeSpaceRaw(t, root, "packages/libs", map[string]any{
		"buildOutputs": []any{"out"},
		"packages":     map[string]any{"utils": map[string]any{"buildOutputs": []any{}}},
	})
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		BuildOutputs:   []string{"lib/esm"},
		BuildPlatforms: []string{"darwin/arm64"},
	})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)

	require.Contains(t, by, "core")
	assert.Equal(t, []string{"lib/esm"}, by["core"].Space.BuildOutputs,
		"the package folder's own file is the nearest statement about the package")
	assert.Equal(t, []string{"darwin/arm64"}, by["core"].Space.BuildPlatforms,
		"the two keys travel the ladder on their own")
	require.Contains(t, by, "utils")
	assert.Empty(t, by["utils"].Space.BuildOutputs,
		"an explicit empty list is how a package says its build leaves nothing to carry")
	assert.Equal(t, []string{"linux/amd64"}, by["utils"].Space.BuildPlatforms,
		"opting out of the outputs says nothing about where the build may run")
	require.Contains(t, by, "app")
	assert.Equal(t, []string{"dist"}, by["app"].Space.BuildOutputs,
		"a space that states nothing inherits the repository's list")
	require.Contains(t, by, "tool")
	assert.Equal(t, []string{"dist"}, by["tool"].Space.BuildOutputs,
		"a standalone package is its own space and starts from the same repository default")
	assert.Equal(t, []string{"linux/amd64"}, by["tool"].Space.BuildPlatforms)
}

// TestBuildOutputsSpaceFileOverridesTheSpaceEntry: the middle rung of the
// ladder, asserted on its own because the space folder's file is the one
// layer a package cannot see and the root file does not name.
func TestBuildOutputsSpaceFileOverridesTheSpaceEntry(t *testing.T) {
	root := writeModelRepo(t, buildLadderConfig(),
		"packages/libs/core", "packages/apps/app", "tools/tool")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{BuildOutputs: []string{"out"}})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)
	assert.Equal(t, []string{"out"}, by["core"].Space.BuildOutputs,
		"the space folder's file outranks the root file's entry for the same space")
	assert.Equal(t, []string{"dist"}, by["app"].Space.BuildOutputs,
		"and speaks for its own space alone")
}

// refuseBuildConfig writes one configuration, loads it and discovers its
// packages, and answers with whichever step refused it. Both steps are one
// answer here because the level decides which of them holds it: the root file
// is checked as it loads, and a space or a package as its folders resolve.
func refuseBuildConfig(t *testing.T, cfg File, pkgDirs ...string) error {
	t.Helper()
	root := writeModelRepo(t, cfg, pkgDirs...)
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	if err != nil {
		return err
	}
	_, _, _, err = DiscoverPackages(loaded, root)
	return err
}

// buildKeysAt returns the fixture with the two lists stated at the root.
func buildKeysAt(outputs, platforms []string) File {
	cfg := minimalConfig()
	cfg.BuildOutputs = outputs
	cfg.BuildPlatforms = platforms
	return cfg
}

// TestBuildKeyRefusals: every shape a single level may not write. Each is
// refused with the key path named and the execution code attached, because a
// configuration no distributed run could be started under is one code a CI
// job switches on rather than a sentence it matches.
func TestBuildKeyRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		cfg  File
		want string
	}{
		"an empty path":                  {buildKeysAt([]string{""}, nil), "names no build output"},
		"a path holding a NUL byte":      {buildKeysAt([]string{"dist\x00"}, nil), "NUL byte"},
		"a path written with backslash":  {buildKeysAt([]string{`dist\assets`}, nil), "slash-separated"},
		"a path naming a drive":          {buildKeysAt([]string{"C:/dist"}, nil), "colon"},
		"an absolute path":               {buildKeysAt([]string{"/srv/dist"}, nil), "absolute"},
		"a path leaving the package":     {buildKeysAt([]string{"../dist"}, nil), "leaves the package folder"},
		"a path reaching past a folder":  {buildKeysAt([]string{"dist/../../elsewhere"}, nil), "leaves the package folder"},
		"the package folder itself":      {buildKeysAt([]string{"."}, nil), "names the package folder itself"},
		"the package folder spelled out": {buildKeysAt([]string{"dist/.."}, nil), "leaves the package folder"},
		"a path naming repository metadata": {
			buildKeysAt([]string{".git/hooks"}, nil), "repository metadata"},
		"a path holding repository metadata deeper down": {
			buildKeysAt([]string{"dist/.GIT"}, nil), "repository metadata"},
		"the same root written twice": {
			buildKeysAt([]string{"dist", "dist/"}, nil), "one build output root"},
		"a root inside another root": {
			buildKeysAt([]string{"dist", "dist/assets"}, nil), "one build output root"},
		"two spellings of one root": {
			buildKeysAt([]string{"dist", "DIST"}, nil), "one build output root"},
		"a platform with no architecture": {
			buildKeysAt(nil, []string{"linux"}), "is not a platform"},
		"a platform with a third half": {
			buildKeysAt(nil, []string{"linux/amd64/v3"}), "is not a platform"},
		"a platform spelled with capitals": {
			buildKeysAt(nil, []string{"Linux/amd64"}), "is not a platform"},
		"a platform with an empty half": {
			buildKeysAt(nil, []string{"linux/"}), "is not a platform"},
		"a platform stated twice": {
			buildKeysAt(nil, []string{"linux/amd64", "linux/amd64"}), "already stated at index 0"},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseBuildConfig(t, row.cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err),
				"an execution-configuration refusal carries its own code")
		})
	}
}

// TestBuildKeysAreRefusedAtEveryLevel: the rules belong to the keys rather
// than to the root file, so a space, a package entry and a package folder's
// own file are each held to them.
func TestBuildKeysAreRefusedAtEveryLevel(t *testing.T) {
	t.Run("a space", func(t *testing.T) {
		cfg := minimalConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.BuildOutputs = []string{"../dist"} })
		err := refuseBuildConfig(t, cfg, "pkgs/core")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `space "libs": buildOutputs`)
		assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
	})

	t.Run("a package entry", func(t *testing.T) {
		cfg := minimalConfig()
		cfg.Packages = map[string]PackageConfig{"core": {BuildPlatforms: []string{"linux"}}}
		err := refuseBuildConfig(t, cfg, "pkgs/core")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "buildPlatforms")
		assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
	})

	t.Run("a package folder's own file", func(t *testing.T) {
		root := writeModelRepo(t, minimalConfig(), "pkgs/core")
		writePackageFile(t, root, "pkgs/core", PackageConfig{BuildOutputs: []string{"dist/.git"}})
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, _, err = DiscoverPackages(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository metadata")
		assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
	})
}

// buildOverlapConfig is the nested-folder fixture the ownership claims are
// made on: `core` is a package of the `libs` space, and `plugin` is a
// standalone package inside `core`'s own folder, which is the only way two
// packages can name one folder at all.
func buildOverlapConfig(coreOutputs, pluginOutputs []string) File {
	cfg := File{
		Scripts: map[string]Script{"build": {"echo b"}},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: PathList{"packages"}, Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
		Packages: map[string]PackageConfig{
			"core":   {BuildOutputs: coreOutputs},
			"plugin": {Path: "packages/core/plugin", BuildOutputs: pluginOutputs},
		},
	}
	return cfg
}

// TestBuildOutputRootsCannotBeClaimedTwice: package folders may nest, so two
// packages can end up naming one folder, and the folder's contents would then
// be attributed to whichever build finished last. Discovery refuses the pair
// and names both sides of it.
func TestBuildOutputRootsCannotBeClaimedTwice(t *testing.T) {
	for name, row := range map[string]struct {
		core, plugin []string
		want         string
	}{
		"the same folder twice": {
			core: []string{"plugin/dist"}, plugin: []string{"dist"}, want: "they name the same folder"},
		"one folder inside the other": {
			core: []string{"plugin"}, plugin: []string{"dist"}, want: "the second sits inside the first"},
		"two spellings of one folder": {
			core: []string{"plugin/DIST"}, plugin: []string{"dist"}, want: "they name the same folder"},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseBuildConfig(t, buildOverlapConfig(row.core, row.plugin),
				"packages/core", "packages/core/plugin")
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
			assert.Contains(t, err.Error(), `package "core"`)
			assert.Contains(t, err.Error(), `package "plugin"`)
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestBuildOutputRootCannotHoldAnotherPackage: a root is installed by replacing
// the folder it names, so a root that holds a nested package's folder would
// replace that package's checkout on every node the outputs reach, whether or
// not the nested package declares outputs of its own.
func TestBuildOutputRootCannotHoldAnotherPackage(t *testing.T) {
	for name, row := range map[string]struct {
		core []string
	}{
		"the nested package's own folder": {core: []string{"plugin"}},
		"another spelling of that folder": {core: []string{"PLUGIN"}},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseBuildConfig(t, buildOverlapConfig(row.core, nil),
				"packages/core", "packages/core/plugin")
			require.Error(t, err)
			assert.Contains(t, err.Error(), `which holds the folder of package "plugin"`)
			assert.Equal(t, DiagnosticExecution, DiagnosticCode(err))
		})
	}
}

// TestBuildOutputRootsAllowSiblingFolders: the rule is about one folder with
// two owners, not about one name written twice. Every package declaring
// `dist` is the ordinary workspace, nested package folders included.
func TestBuildOutputRootsAllowSiblingFolders(t *testing.T) {
	root := writeModelRepo(t, buildOverlapConfig([]string{"dist"}, []string{"dist"}),
		"packages/core", "packages/core/plugin")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)
	assert.Equal(t, []string{"dist"}, by["core"].Space.BuildOutputs)
	assert.Equal(t, []string{"dist"}, by["plugin"].Space.BuildOutputs)
}

// TestBuildOutputRootsAreRepositoryLocal: two repositories of a composed
// workspace may each hold a package declaring `dist`, and those are two
// folders. Resolving every root against its own package folder is what makes
// that true without a rule about repositories: two packages collide when they
// name one folder, never because they spell their outputs alike.
func TestBuildOutputRootsAreRepositoryLocal(t *testing.T) {
	workspace := t.TempDir()
	packageAt := func(repository, name string) *model.Package {
		return &model.Package{
			Name:  name,
			Dir:   filepath.Join(workspace, repository, "packages", name),
			Space: &model.Space{BuildOutputs: []string{"dist"}},
		}
	}
	apart := &discovery{root: workspace, pkgs: []*model.Package{
		packageAt("one", "core"), packageAt("two", "core"),
	}}
	assert.NoError(t, apart.checkBuildOutputRoots(),
		"one spelling in two checkouts is two folders")

	together := &discovery{root: workspace, pkgs: []*model.Package{
		packageAt("one", "core"), packageAt("one", "core"),
	}}
	err := together.checkBuildOutputRoots()
	require.Error(t, err, "one folder claimed twice is refused wherever the claims came from")
	assert.Contains(t, err.Error(), "they name the same folder")
}
