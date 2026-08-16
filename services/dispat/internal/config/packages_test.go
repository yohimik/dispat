package config

// Per-package overrides are exercised end to end: configs authored as typed
// models (raw shapes only where the marshaller cannot express the case — an
// unknown key, a scalar weak-decode, an explicit empty array that omitempty
// would drop), loaded through Load and resolved through DiscoverPackages,
// because the merge has no public seam of its own: every claim is made
// against the resolved model.Space clones and package fields.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	yaml "gopkg.in/yaml.v3"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// discoverPackages loads the repo's config and resolves its packages.
func discoverPackages(t *testing.T, root string) ([]*model.Package, error) {
	t.Helper()
	pkgs, _, err := discoverAll(t, root)
	return pkgs, err
}

// discoverAll is discoverPackages keeping the declared dependency list.
func discoverAll(t *testing.T, root string) ([]*model.Package, []DeclaredDependency, error) {
	t.Helper()
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	return DiscoverPackages(cfg, root)
}

// packagesByName indexes discovery output for assertions.
func packagesByName(pkgs []*model.Package) map[string]*model.Package {
	out := make(map[string]*model.Package, len(pkgs))
	for _, p := range pkgs {
		out[p.Name] = p
	}
	return out
}

// writePackageFile drops an in-folder override config into a package folder.
func writePackageFile(t *testing.T, root, pkgDir string, po PackageConfig) {
	t.Helper()
	data, err := json.MarshalIndent(po, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, pkgDir, "dispat.json"), data, 0o644))
}

// writePackageRaw is writePackageFile for shapes the model cannot express.
func writePackageRaw(t *testing.T, root, pkgDir string, po map[string]any) {
	t.Helper()
	writeFolderConfig(t, root, pkgDir, "dispat.json", po)
}

// writeSpaceFile drops a space's own config file into the space folder — the
// layer between the root file's space entry and anything said about one
// package.
func writeSpaceFile(t *testing.T, root, spaceDir string, sf SpaceFile) {
	t.Helper()
	writeFolderConfig(t, root, spaceDir, "dispat.json", sf)
}

// writeSpaceRaw is writeSpaceFile for shapes the model cannot express.
func writeSpaceRaw(t *testing.T, root, spaceDir string, sf map[string]any) {
	t.Helper()
	writeFolderConfig(t, root, spaceDir, "dispat.json", sf)
}

// writeFolderConfig marshals any config value into a folder's config file,
// under the name given — the seam the format and .dispatexclude tests need.
func writeFolderConfig(t *testing.T, root, dir, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, name), data, 0o644))
}

// writeExclude drops a .dispatexclude into a folder.
func writeExclude(t *testing.T, root, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, DispatexcludeName), []byte(body), 0o644))
}

// TestPackagePathForSpacePackageRejected: a packages entry whose key matches
// a space folder cannot set `path` — a space package's location is its
// folder, and redefining it could only contradict discovery.
func TestPackagePathForSpacePackageRejected(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Path: "elsewhere"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "elsewhere")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["core"]`)
	assert.Contains(t, err.Error(), `belongs to space "libs"`)
}

// TestInFolderPathRejected: an in-folder config file cannot move the folder
// it lives in, so its `path` key is refused with the file named.
func TestInFolderPathRejected(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageRaw(t, root, "packages/libs/core", map[string]any{"path": "elsewhere"})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be set in a package folder's config file")
	assert.Contains(t, err.Error(), filepath.Join("packages/libs/core", "dispat.json"))
}

// TestPackageOverrideScalarInherit: the tri-state pointers — nil inherits
// the space value, an explicit false overrides it — which plain bools could
// never express in an override layer.
func TestPackageOverrideScalarInherit(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {RevertOnFail: models.Bool(false), IsBuildWaitingPublish: models.Bool(false)},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.False(t, byName["core"].Space.RevertOnFail, "explicit false overrides the space's true")
	assert.False(t, byName["core"].Space.BuildWaitsPublish, "both scalar pointers override independently")
	assert.True(t, byName["utils"].Space.BuildWaitsPublish, "unset pointers inherit the space value")
	assert.True(t, byName["utils"].Space.RevertOnFail, "siblings keep the space config")
	assert.NotSame(t, byName["utils"].Space, byName["core"].Space, "an override gets a derived Space")
	assert.Equal(t, "libs", byName["core"].Space.Name, "the derived Space keeps the space's name")
}

// TestPackageOverrideSharedSpacePointer: packages without overrides share
// the space's one resolved Space value — the common path allocates nothing
// per package.
func TestPackageOverrideSharedSpacePointer(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Same(t, byName["core"].Space, byName["utils"].Space)
}

// TestPackageOverrideFlowEntryMerge: flow merges entry by entry — an
// overridden stage replaces its list, every other stage inherits — and an
// explicit empty array (raw: omitempty cannot marshal it) clears a stage.
func TestPackageOverrideFlowEntryMerge(t *testing.T) {
	cfg := validConfig()
	cfg.Scripts["alt"] = Script{"echo alt"}
	cfg.Packages = map[string]PackageConfig{
		"core": {Flow: &SpaceFlowConfig{Build: []string{"alt"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	writePackageRaw(t, root, "packages/libs/utils", map[string]any{
		"flow": map[string]any{"publish": []string{}},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, []string{"echo alt"}, byName["core"].Space.BuildScript, "overridden entry replaces")
	assert.Equal(t, []string{"echo publish"}, byName["core"].Space.PublishScript, "untouched entry inherits")
	assert.Empty(t, byName["utils"].Space.PublishScript, "an explicit empty array clears the stage")
	assert.Equal(t, []string{"echo build"}, byName["utils"].Space.BuildScript)
}

// TestPackageOverrideLoginForbidden: login runs once per space, in the space
// folder, gating every publish of the space — a per-package login would
// contradict all three, so the override layer rejects it.
func TestPackageOverrideLoginForbidden(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {Flow: &SpaceFlowConfig{Login: []string{"build"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.login")
	assert.Contains(t, err.Error(), `package "core"`)
}

// TestPackageOverrideScriptRefUnknown: a package's flow references resolve
// against the root scripts map, under the package's own error label.
func TestPackageOverrideScriptRefUnknown(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {Flow: &SpaceFlowConfig{Build: []string{"nope"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `space "libs": package "core"`)
	assert.Contains(t, err.Error(), `unknown script "nope"`)
}

// TestPackageOverrideAutoVersionWholeObject: autoVersion replaces wholesale
// — its empty fields carry meaning relative to their siblings, so an overlay
// could not express "all kinds" against a narrowed base — and the override's
// own syncLock references are validated like a space's.
func TestPackageOverrideAutoVersionWholeObject(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.AutoVersion = &AutoVersionConfig{Manifests: "all", SyncLock: []string{"build"}}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {AutoVersion: &AutoVersionConfig{Enabled: models.Bool(false)}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Nil(t, byName["core"].Space.AutoVersion, "the disabled override replaces the space block")
	require.NotNil(t, byName["utils"].Space.AutoVersion)
	assert.Equal(t, model.ScopeAll, byName["utils"].Space.AutoVersion.Manifests, "siblings keep the space block")

	cfg.Packages["core"] = PackageConfig{AutoVersion: &AutoVersionConfig{SyncLock: []string{"ghost"}}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "core"`)
	assert.Contains(t, err.Error(), `unknown script "ghost"`)
}

// TestPackageOverrideAutoVersionOnlyChecked: an overridden autoVersion block
// is held to the same only-names-discovered-packages rule as a space's.
func TestPackageOverrideAutoVersionOnlyChecked(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {AutoVersion: &AutoVersionConfig{Only: []string{"ghost"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "autoVersion.only")
	assert.Contains(t, err.Error(), `unknown package "ghost"`)
}

// TestPackagesEntryUnmatched: an entry naming no folder is the same class of
// typo as an unknown dependency endpoint; one naming an ignored folder says
// which file excluded it.
func TestPackagesEntryUnmatched(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"ghost": {TagFormat: "v{version}"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["ghost"] matches no package folder`)

	cfg.Packages = map[string]PackageConfig{"core": {TagFormat: "v{version}"}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages/libs", DispatexcludeName), []byte("core\n"), 0o644))
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `excluded by `+DispatexcludeName)
}

// TestInFolderConfigPrecedence: the three layers merge field by field with
// the most local winning — space config, then the packages entry, then the
// package folder's own file.
func TestInFolderConfigPrecedence(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {TagFormat: "entry-{name}@{version}", RevertOnFail: models.Bool(false)},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "file-{name}@{version}"})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	core := byName["core"].Space
	assert.Equal(t, "file-{name}@{version}", core.TagFormat, "the in-folder file is the most local layer")
	assert.False(t, core.RevertOnFail, "fields the file leaves unset keep the entry's value")
	assert.True(t, core.BuildWaitsPublish, "fields neither layer sets keep the space's")
}

// TestInFolderConfigFormats: the in-folder file resolves through the same
// names and formats as the root config — one smoke test per extension,
// bodies produced by real marshallers, never hand-written.
func TestInFolderConfigFormats(t *testing.T) {
	po := PackageConfig{TagFormat: "pkg-{name}@{version}"}
	jsonBytes, err := json.Marshal(po)
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &generic))
	yamlBytes, err := yaml.Marshal(generic)
	require.NoError(t, err)
	tomlBytes, err := toml.Marshal(generic)
	require.NoError(t, err)

	for name, body := range map[string][]byte{
		"dispat.json": jsonBytes,
		"dispat.yaml": yamlBytes,
		"dispat.yml":  yamlBytes,
		"dispat.toml": tomlBytes,
	} {
		t.Run(name, func(t *testing.T) {
			root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
			require.NoError(t, os.WriteFile(filepath.Join(root, "packages/libs/core", name), body, 0o644))
			pkgs, err := discoverPackages(t, root)
			require.NoError(t, err)
			assert.Equal(t, "pkg-{name}@{version}", packagesByName(pkgs)["core"].Space.TagFormat)
		})
	}
}

// TestInFolderConfigInvalid: an unknown key in the in-folder file fails with
// the file named, exactly like a root-config typo.
func TestInFolderConfigInvalid(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageRaw(t, root, "packages/libs/core", map[string]any{"tagFormats": "v{version}"})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join("packages/libs/core", "dispat.json"))
}

// TestInFolderNestedRoot: an in-folder file declaring spaces is a nested
// monorepo's root config, not an override; the error points at .dispatexclude
// instead of half-merging another repository's configuration.
func TestInFolderNestedRoot(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageRaw(t, root, "packages/libs/core", map[string]any{
		"spaces": map[string]any{"inner": map[string]any{"path": "pkgs"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monorepo root of its own")
	assert.Contains(t, err.Error(), DispatexcludeName)
}

// TestDispatexclude: patterns match direct sub-folder names — exact names and
// * globs — with blank lines and # comments skipped; other spaces are
// untouched.
func TestDispatexclude(t *testing.T) {
	root := writeModelRepo(t, validConfig(),
		"packages/libs/core", "packages/libs/sandbox", "packages/libs/tmp-a", "packages/libs/tmp-b",
		"packages/apps/app", "packages/apps/sandbox2")
	ignore := "# scratch folders are not packages\n\nsandbox\ntmp-*\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages/libs", DispatexcludeName), []byte(ignore), 0o644))
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Len(t, pkgs, 3)
	assert.Contains(t, byName, "core")
	assert.Contains(t, byName, "app")
	assert.Contains(t, byName, "sandbox2", "another space's folders are not affected")
	assert.NotContains(t, byName, "sandbox")
	assert.NotContains(t, byName, "tmp-a")
	assert.NotContains(t, byName, "tmp-b")
}

// TestVersionGroupDeclarations: a declared group must share (fixed or
// fixedSparse, case-insensitively normalized), must not shadow a space name,
// and an unused declaration is inert configuration.
func TestVersionGroupDeclarations(t *testing.T) {
	cfg := validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: "independent"}}
	_, err := loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a group exists to share versions")

	cfg.VersionGroups = map[string]VersionGroupConfig{"libs": {Versioning: VersioningFixed}}
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "share one namespace")

	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: "Fixed"}}
	loaded, err := loadModel(t, cfg)
	require.NoError(t, err, "an unused group is inert")
	assert.Equal(t, VersioningFixed, loaded.VersionGroups["core-group"].Versioning, "modes are normalized")

	// Every shared mode is declarable, partial ones included.
	for _, mode := range sharedVersioningNames() {
		cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: mode}}
		loaded, err = loadModel(t, cfg)
		require.NoErrorf(t, err, "versionGroups mode %q", mode)
		assert.Equal(t, mode, loaded.VersionGroups["core-group"].Versioning)
	}
}

// TestVersionGroupPartialModes: a group declared with a partial mode hands
// that mode to everyone who joins, exactly as a full one does.
func TestVersionGroupPartialModes(t *testing.T) {
	cfg := validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningFixedMajor}}
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "core-group" })
	cfg.Packages = map[string]PackageConfig{"app": {VersionGroup: "core-group"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "packages/apps/web")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, model.VersioningFixedMajor, byName["core"].Space.Versioning)
	assert.Equal(t, "core-group", byName["core"].Space.VersionGroup)
	assert.Equal(t, model.VersioningFixedMajor, byName["app"].Space.Versioning,
		"a package joins the declared group under the group's mode")
	assert.Equal(t, model.VersioningIndependent, byName["web"].Space.Versioning)

	// A space with its own partial mode is referenceable as an implicit group.
	cfg = validConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Versioning = VersioningFixedMajorMinorSparse })
	cfg.Packages = map[string]PackageConfig{"app": {VersionGroup: "libs"}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "packages/apps/web")
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	byName = packagesByName(pkgs)
	assert.Equal(t, "libs", byName["app"].Space.VersionGroup)
	assert.Equal(t, model.VersioningFixedMajorMinorSparse, byName["app"].Space.Versioning)
}

// TestDiscoverMultiPathSpace: a space listing several folders discovers the
// packages of every one, reads a space file from each (later folders
// overriding earlier), anchors every member's space behaviour at the first
// folder, and refuses one package name appearing under two of its folders.
func TestDiscoverMultiPathSpace(t *testing.T) {
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Path = PathList{"pkgs", "more"} })
	root := writeModelRepo(t, cfg, "pkgs/core", "more/extra")
	writeSpaceFile(t, root, "pkgs", SpaceFile{TagFormat: "one-{name}@v{version}"})
	writeSpaceFile(t, root, "more", SpaceFile{TagFormat: "two-{name}@v{version}"})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	require.Contains(t, byName, "core")
	require.Contains(t, byName, "extra", "the second folder's packages are the space's too")

	assert.Equal(t, filepath.Join(root, "pkgs", "core"), byName["core"].Dir)
	assert.Equal(t, filepath.Join(root, "more", "extra"), byName["extra"].Dir)
	assert.Equal(t, filepath.Join(root, "pkgs"), byName["extra"].Space.Dir,
		"the primary folder anchors the space wherever the member lives")
	assert.Equal(t, "two-{name}@v{version}", string(byName["core"].Space.TagFormat),
		"space files merge in path order, the later one over the earlier")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "dup"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "more", "dup"), 0o755))
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "dup" exists in two folders of space "libs" (pkgs and more)`)
}

// TestDiscoverMultiPathExcludePerFolder: a .dispatexclude excludes folders of
// the space folder it sits in, and only that one.
func TestDiscoverMultiPathExcludePerFolder(t *testing.T) {
	cfg := minimalConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Path = PathList{"pkgs", "more"} })
	root := writeModelRepo(t, cfg, "pkgs/core", "pkgs/skip", "more/extra", "more/tmp")
	require.NoError(t, os.WriteFile(filepath.Join(root, "more", DispatexcludeName), []byte("tmp\n"), 0o644))

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Contains(t, byName, "core")
	assert.Contains(t, byName, "skip", "the exclude of one folder does not reach another")
	assert.Contains(t, byName, "extra")
	assert.NotContains(t, byName, "tmp")
}

// TestVersioningNoneGroups: none shares nothing, so it cannot declare a
// group, cannot be joined as one, and cannot sit next to a versionGroup
// reference on the same layer.
func TestVersioningNoneGroups(t *testing.T) {
	cfg := validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningNone}}
	_, err := loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a group exists to share versions")

	cfg = validConfig()
	apps := cfg.Spaces["apps"]
	apps.Versioning = VersioningNone
	cfg.Spaces["apps"] = apps
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "apps" })
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `does not version as a group (its versioning is "none")`)

	cfg = validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningFixed}}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Versioning = VersioningNone
		s.VersionGroup = "core-group"
	})
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "versioning and versionGroup are mutually exclusive")
}

// TestVersionGroupSpaceReference: versionGroup on a space joins a declared
// group (adopting its mode) or another shared space's implicit group; the
// unknown and the independent references fail at load.
func TestVersionGroupSpaceReference(t *testing.T) {
	cfg := validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningFixedSparse}}
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "core-group" })
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, "core-group", byName["core"].Space.VersionGroup)
	assert.Equal(t, model.VersioningFixedSparse, byName["core"].Space.Versioning, "the group's mode is the members'")
	assert.Equal(t, "apps", byName["app"].Space.VersionGroup, "a space without a reference is its own group")

	cfg = validConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "nope" })
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches no versionGroups entry and no space")

	cfg = validConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "apps" })
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not version as a group")

	cfg = validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningFixed}}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.VersionGroup = "core-group"
		s.Versioning = VersioningFixed
	})
	_, err = loadModel(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

// TestVersionGroupPackageReference: a package joins a group on its own —
// including another space's implicit group — or opts out of its space's; the
// same-layer versioning conflict and the member-of-a-group space reference
// are rejected.
func TestVersionGroupPackageReference(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Versioning = VersioningFixed })
	cfg.Packages = map[string]PackageConfig{"app": {VersionGroup: "libs"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "packages/apps/web")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, "libs", byName["app"].Space.VersionGroup, "a package can join another space's group")
	assert.Equal(t, model.VersioningFixed, byName["app"].Space.Versioning)
	assert.Equal(t, model.VersioningIndependent, byName["web"].Space.Versioning, "siblings stay independent")

	// A package of a fixed space opts out entirely.
	cfg.Packages = map[string]PackageConfig{"utils": {Versioning: VersioningIndependent}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app", "packages/apps/web")
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	byName = packagesByName(pkgs)
	assert.Equal(t, model.VersioningIndependent, byName["utils"].Space.Versioning)
	assert.Equal(t, model.VersioningFixed, byName["core"].Space.Versioning)

	// Both axes in one layer contradict each other.
	cfg.Packages = map[string]PackageConfig{"core": {Versioning: VersioningFixed, VersionGroup: "libs"}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "packages/apps/web")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	// Referencing a space that is itself a member of a group names the wrong
	// thing; the error redirects to the group.
	cfg = validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"core-group": {Versioning: VersioningFixed}}
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "core-group" })
	cfg.Packages = map[string]PackageConfig{"app": {VersionGroup: "libs"}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name that group directly")
}

// TestPackageConcurrencyValidation: the override is a weight — scalar or
// [build, publish] pair, 0 and absence meaning 1 — validated for shape like
// the top-level key but never defaulted to the CPU count.
func TestPackageConcurrencyValidation(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core":  {Concurrency: []int{2, 1}},
		"utils": {Concurrency: []int{0}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, 2, byName["core"].BuildWeight)
	assert.Equal(t, 1, byName["core"].PublishWeight)
	assert.Equal(t, 1, byName["utils"].BuildWeight, "0 means the ordinary cost, not the CPU count")
	assert.Equal(t, 1, byName["app"].BuildWeight, "no override, ordinary cost")

	// The weak-typed scalar (concurrency: 3) the typed slice cannot produce.
	root = writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{
				"path": "pkgs", "flow": map[string]any{"build": "build"},
			},
		},
		"packages": map[string]any{"core": map[string]any{"concurrency": 3}},
	}, "pkgs/core")
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, 3, packagesByName(pkgs)["core"].BuildWeight, "a scalar weight applies to both stages")
	assert.Equal(t, 3, packagesByName(pkgs)["core"].PublishWeight)

	for _, bad := range [][]int{{1, 2, 3}, {-1}} {
		cfg.Packages = map[string]PackageConfig{"core": {Concurrency: bad}}
		root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
		_, err := discoverPackages(t, root)
		require.Errorf(t, err, "concurrency %v must be rejected", bad)
		assert.Contains(t, err.Error(), "concurrency")
	}
}

// TestPackageRecordSpecsOverlay: changelog and github overlay the top-level
// objects field by field — a package flips enabled and keeps the global
// file, or retargets the repository and keeps the global token env.
func TestPackageRecordSpecsOverlay(t *testing.T) {
	cfg := validConfig()
	cfg.Changelog = &ChangelogConfig{File: "HISTORY.md"}
	cfg.GitHub = &GitHubConfig{Repo: "mono", TokenEnv: "MY_TOKEN"}
	cfg.Packages = map[string]PackageConfig{
		"core": {
			Changelog: &ChangelogConfig{Enabled: models.Bool(false)},
			GitHub:    &GitHubConfig{Owner: "acme"},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.False(t, byName["core"].Changelog.Enabled)
	assert.Equal(t, "HISTORY.md", byName["core"].Changelog.File, "unset fields keep the global value")
	assert.Equal(t, "acme", byName["core"].GitHub.Owner)
	assert.Equal(t, "mono", byName["core"].GitHub.Repo)
	assert.Equal(t, "MY_TOKEN", byName["core"].GitHub.TokenEnv)

	assert.True(t, byName["utils"].Changelog.Enabled, "no override: the global policy")
	assert.Equal(t, "HISTORY.md", byName["utils"].Changelog.File)
	assert.Empty(t, byName["utils"].GitHub.Owner)
}

// TestPackageRecordSpecsOverlayBothLayers: records overlay layer by layer
// like everything else. The entry starts from the repository's objects, and
// the in-folder file then starts from the entry's result rather than from the
// repository's, so each layer only has to state what it changes.
func TestPackageRecordSpecsOverlayBothLayers(t *testing.T) {
	cfg := validConfig()
	cfg.Changelog = &ChangelogConfig{File: "GLOBAL.md", FileTitle: titleLine("# Global")}
	cfg.GitHub = &GitHubConfig{Owner: "acme", Repo: "mono", TokenEnv: "GLOBAL_TOKEN"}
	cfg.Packages = map[string]PackageConfig{
		"core": {
			Changelog: &ChangelogConfig{File: "ENTRY.md"},
			GitHub:    &GitHubConfig{Repo: "entry-repo"},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		Changelog: &ChangelogConfig{FileTitle: titleLine("# Local")},
		GitHub:    &GitHubConfig{APIURL: "https://ghe"},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, "ENTRY.md", core.Changelog.File, "the entry's value survives the second layer")
	assert.Equal(t, resolvedTitleLine("# Local"), core.Changelog.FileTitle, "which sets only what it names")
	assert.Equal(t, "entry-repo", core.GitHub.Repo)
	assert.Equal(t, "https://ghe", core.GitHub.APIURL)
	assert.Equal(t, "acme", core.GitHub.Owner, "and the repository's values are still underneath both")
	assert.Equal(t, "GLOBAL_TOKEN", core.GitHub.TokenEnv)
}

// TestPackageRecordSpecsWithoutGlobals: a package may configure records the
// repository never configured at all. With no top-level object to overlay,
// the package's own object is the whole policy and the defaults fill the rest.
func TestPackageRecordSpecsWithoutGlobals(t *testing.T) {
	cfg := validConfig() // no changelog, no github
	cfg.Packages = map[string]PackageConfig{
		"core": {
			Changelog: &ChangelogConfig{File: "NOTES.md"},
			GitHub:    &GitHubConfig{Enabled: models.Bool(true), Owner: "acme", Repo: "solo"},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, "NOTES.md", byName["core"].Changelog.File)
	assert.True(t, byName["core"].Changelog.Enabled, "the default survives an overlay onto nothing")
	assert.Equal(t, "acme", byName["core"].GitHub.Owner)
	assert.Equal(t, "solo", byName["core"].GitHub.Repo)
	assert.True(t, byName["core"].GitHub.Enabled)
	assert.NotEqual(t, "NOTES.md", byName["utils"].Changelog.File, "the sibling is untouched")
}

// TestPackageGitHubAllPackagesOverride: `github.allPackages` is a tri-state
// like the other booleans, so a package can opt itself into (or out of) the
// repository's blanket-release policy while every other field inherits.
func TestPackageGitHubAllPackagesOverride(t *testing.T) {
	cfg := validConfig()
	cfg.GitHub = &GitHubConfig{Owner: "acme", Repo: "mono", AllPackages: models.Bool(true)}
	cfg.Packages = map[string]PackageConfig{
		"core": {GitHub: &GitHubConfig{AllPackages: models.Bool(false)}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.False(t, byName["core"].GitHub.AllPackages, "the package opts out")
	assert.Equal(t, "acme", byName["core"].GitHub.Owner, "everything else still inherits")
	assert.True(t, byName["utils"].GitHub.AllPackages, "the sibling keeps the repository policy")
}

// TestPackagesEntryEmptyKey: a nameless `packages` key configures nothing and
// can match no folder, so it is rejected at load rather than silently ignored.
func TestPackagesEntryEmptyKey(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"": {TagFormat: "v{version}"}}
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package name must not be empty")
}

// TestResolveFileSkipsPackageConfig: the parent ascent must not stop at a
// package's in-folder override file — only a file declaring spaces ends it —
// while a repository whose only config is spaces-less still resolves to that
// file, so its "at least one space" error names the real mistake.
func TestResolveFileSkipsPackageConfig(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "v{version}"})

	path, resolvedRoot, err := ResolveFile(filepath.Join(root, "packages/libs/core"), "dispat.json", false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "dispat.json"), path, "the override file is not the root config")
	assert.Equal(t, root, resolvedRoot)

	broken := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(broken, "dispat.json"), []byte("{}"), 0o644))
	path, resolvedRoot, err = ResolveFile(broken, "dispat.json", false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(broken, "dispat.json"), path, "a spaces-less config still resolves, to fail in Load")
	assert.Equal(t, broken, resolvedRoot)
}

// TestPackagesKeyAmbiguous: one lowercased packages key matching two folders
// differing only by case has no single package to configure.
func TestPackagesKeyAmbiguous(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {TagFormat: "v{version}"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	// A second folder differing only by case; skipped on case-insensitive
	// filesystems, where the two folders cannot coexist.
	if err := os.Mkdir(filepath.Join(root, "packages/libs/Core"), 0o755); err != nil {
		t.Skip("case-insensitive filesystem")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "packages/libs")); err == nil && len(entries) < 2 {
		t.Skip("case-insensitive filesystem")
	}
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguously")
}

// TestPackageOverrideCaseInsensitiveKey: viper lowercases the packages map's
// keys, so a folder with uppercase letters still matches its entry.
func TestPackageOverrideCaseInsensitiveKey(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"corelib": {TagFormat: "v{version}"}}
	root := writeModelRepo(t, cfg, "packages/libs/CoreLib", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "v{version}", packagesByName(pkgs)["CoreLib"].Space.TagFormat)
}

// TestVersionGroupEmptyName: an empty group name can only be a mistake — it
// could never be referenced.
func TestVersionGroupEmptyName(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts":       map[string]any{"build": "echo b"},
		"versionGroups": map[string]any{"": map[string]any{"versioning": "fixed"}},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group name must not be empty")
}

// TestDiscoverUnvalidatedGroupRef: DiscoverPackages resolves the group even
// on a config that skipped Load's validation (compute-style direct use), so
// a dangling reference still fails there instead of producing a group of
// nothing.
func TestDiscoverUnvalidatedGroupRef(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs/core"), 0o755))
	cfg := &File{Spaces: map[string]SpaceConfig{
		"libs": {Path: PathList{"pkgs"}, Flow: &SpaceFlowConfig{}, VersionGroup: "nope"},
	}}
	_, _, err := DiscoverPackages(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches no versionGroups entry and no space")
}

// TestDispatexcludeUnreadable: a .dispatexclude that cannot be read (here: a
// directory of that name) fails the discovery with the file named, rather
// than silently treating every folder as a package.
func TestDispatexcludeUnreadable(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/libs", DispatexcludeName, "x"), 0o755))
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), DispatexcludeName)
}

// TestInFolderConfigUnreadable: an in-folder config that exists but cannot
// be read as one (a directory of that name) is an error, not a silent skip.
func TestInFolderConfigUnreadable(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/libs/core/dispat.json"), 0o755))
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "core"`)
}

// TestInFolderLoginForbidden: the login exclusion holds for the in-folder
// layer too, with the file named in the error.
func TestInFolderLoginForbidden(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		Flow: &SpaceFlowConfig{Login: []string{"build"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.login")
	assert.Contains(t, err.Error(), "dispat.json")
}

// TestOverlayRecordFields: the changelog/github overlay is field by field —
// every entry-format field, the changelog title, and each github target
// field override independently while unset ones inherit.
func TestOverlayRecordFields(t *testing.T) {
	baseFormat := EntryFormatConfig{DateFormat: "2006", BreakingTitle: "B", FeaturesTitle: "F",
		FixesTitle: "X", DependenciesTitle: "D"}
	over := EntryFormatConfig{DateFormat: "01/2006", BreakingTitle: "B2", FeaturesTitle: "F2",
		FixesTitle: "X2", DependenciesTitle: "D2"}
	assert.Equal(t, over, overlayFormat(baseFormat, over), "every set field overrides")
	assert.Equal(t, baseFormat, overlayFormat(baseFormat, EntryFormatConfig{}), "every unset field inherits")

	cl := overlayChangelog(
		&ChangelogConfig{File: "HISTORY.md", FileTitle: titleLine("# H"), EntryFormatConfig: baseFormat},
		&ChangelogConfig{FileTitle: titleLine("# Other")})
	assert.Equal(t, titleLine("# Other"), cl.FileTitle)
	assert.Equal(t, "HISTORY.md", cl.File)
	assert.Equal(t, baseFormat, cl.EntryFormatConfig)

	gh := overlayGitHub(
		&GitHubConfig{Owner: "acme", Repo: "mono", APIURL: "https://ghe", TokenEnv: "T1"},
		&GitHubConfig{Repo: "other", APIURL: "https://ghe2", TokenEnv: "T2", Enabled: models.Bool(false)})
	assert.Equal(t, "acme", gh.Owner)
	assert.Equal(t, "other", gh.Repo)
	assert.Equal(t, "https://ghe2", gh.APIURL)
	assert.Equal(t, "T2", gh.TokenEnv)
	assert.False(t, gh.IsEnabled())
}

// TestPackageOverrideScriptsUnion: scripts merge name by name across all
// three levels — the package adds and shadows, the space's other names
// survive, and the file's names stay underneath both. A name several levels
// define resolves to the most local command.
func TestPackageOverrideScriptsUnion(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Scripts = map[string]Script{"lint": {"space lint"}, "fmt": {"space fmt"}, "build": {"space build"}}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Scripts: map[string]Script{"lint": {"core lint"}, "extra": {"core extra"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, map[string]Script{"lint": {"core lint"}, "fmt": {"space fmt"}, "extra": {"core extra"},
		"build": {"space build"}, "publish": {"echo publish"}}, byName["core"].Space.Scripts)
	assert.Equal(t, map[string]Script{"lint": {"space lint"}, "fmt": {"space fmt"},
		"build": {"space build"}, "publish": {"echo publish"}},
		byName["utils"].Space.Scripts, "the space's own map is not mutated by the merge")
	assert.Equal(t, map[string]Script{"build": {"echo build"}, "publish": {"echo publish"}},
		byName["app"].Space.Scripts, "another space sees the file's scripts alone")

	// The precedence is the one the flow resolution uses, not a separate rule.
	assert.Equal(t, []string{"space build"}, byName["core"].Space.BuildScript)
	assert.Equal(t, []string{"echo build"}, byName["app"].Space.BuildScript)
}

// TestPackageOverrideReplacesTheWholeScript: a name is one entry however many
// commands it binds, so a level restating it replaces the sequence rather than
// adding to it. Merging the two would make a package unable to say "not that,
// this" about a name it inherited.
func TestPackageOverrideReplacesTheWholeScript(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Scripts = map[string]Script{"lint": {"space lint"}, "fmt": {"space fmt a", "space fmt b"}}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Scripts: map[string]Script{"lint": {"core lint a", "core lint b"}, "fmt": {"core fmt"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, Script{"core lint a", "core lint b"}, byName["core"].Space.Scripts["lint"],
		"an array replaces the string it shadows")
	assert.Equal(t, Script{"core fmt"}, byName["core"].Space.Scripts["fmt"],
		"and a string replaces the array, whole")
	assert.Equal(t, Script{"space fmt a", "space fmt b"}, byName["utils"].Space.Scripts["fmt"],
		"the package next door keeps what the space said")
}

// TestStandalonePackage: a packages entry with a path is a package outside
// every space — its own single-package space named after the entry, located
// at the root-relative path, with the entry as its whole configuration.
func TestStandalonePackage(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"cli": {
			Path:      "tools/cli",
			Flow:      &SpaceFlowConfig{Build: []string{"build"}},
			TagFormat: "cli-v{version}",
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	cli := byName["cli"]
	require.NotNil(t, cli, "the standalone package is discovered")
	assert.Equal(t, filepath.Join(root, "tools/cli"), cli.Dir)
	assert.Equal(t, "cli", cli.Space.Name, "its space is itself")
	assert.Equal(t, "tools/cli", cli.Space.Path)
	assert.Equal(t, []string{"echo build"}, cli.Space.BuildScript)
	assert.Empty(t, cli.Space.PublishScript, "stages the entry leaves unset stay empty")
	assert.Equal(t, "cli-v{version}", cli.Space.TagFormat)
	assert.Equal(t, model.VersioningIndependent, cli.Space.Versioning)
	assert.Equal(t, "cli", cli.Space.VersionGroup, "its own implicit group")
	assert.Equal(t, 1, cli.BuildWeight)
	assert.True(t, cli.Changelog.Enabled, "global record policy applies")
	assert.Len(t, pkgs, 3, "space packages are unaffected")
}

// TestStandalonePackageInFolderLayer: the in-folder config file overrides the
// entry field by field, exactly like a space package's most local layer.
func TestStandalonePackageInFolderLayer(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"cli": {Path: "tools/cli", TagFormat: "entry-v{version}", Concurrency: []int{2}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
	writePackageFile(t, root, "tools/cli", PackageConfig{TagFormat: "file-v{version}"})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	cli := packagesByName(pkgs)["cli"]
	assert.Equal(t, "file-v{version}", cli.Space.TagFormat, "the in-folder file wins")
	assert.Equal(t, 2, cli.BuildWeight, "fields the file leaves unset keep the entry's value")
}

// TestStandalonePackageMissingFolder: the entry's path must name an existing
// folder — discovery cannot conjure the package's location.
func TestStandalonePackageMissingFolder(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"cli": {Path: "tools/cli"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "cli"`)

	// A file at the path is no better than nothing.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tools"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tools/cli"), []byte("x"), 0o644))
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a folder")
}

// TestStandaloneEntryCarriesThePackageOnlyKeys: a standalone entry is the
// package's whole configuration, so the keys that are not space-shaped —
// records, weights, autoVersion — take effect from it exactly as they would
// from an override on a space package.
func TestStandaloneEntryCarriesThePackageOnlyKeys(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"cli": {
			Path:        "tools/cli",
			Scripts:     map[string]Script{"tidy": {"go mod tidy"}},
			AutoVersion: &AutoVersionConfig{Enabled: models.Bool(true), SyncLock: []string{"tidy"}},
			Changelog:   &ChangelogConfig{File: "CLI.md"},
			GitHub:      &GitHubConfig{Enabled: models.Bool(true), Owner: "acme", Repo: "cli"},
			Concurrency: []int{3, 2},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	cli := packagesByName(pkgs)["cli"]
	require.NotNil(t, cli)

	require.NotNil(t, cli.Space.AutoVersion, "the entry's autoVersion block is the package's")
	assert.Equal(t, []string{"go mod tidy"}, cli.Space.AutoVersion.SyncLock,
		"and its syncLock resolves against the entry's own scripts")
	assert.Equal(t, "CLI.md", cli.Changelog.File)
	assert.Equal(t, "cli", cli.GitHub.Repo)
	assert.True(t, cli.GitHub.Enabled)
	assert.Equal(t, 3, cli.BuildWeight)
	assert.Equal(t, 2, cli.PublishWeight)
}

// TestStandaloneEntryIsHeldToSpaceRules: a standalone entry is the whole
// configuration of its package, so every rule a space-shaped config obeys
// applies to it — the override-layer rules, the value rules, and the script
// references, which resolve in the package's own scope like anywhere else.
func TestStandaloneEntryIsHeldToSpaceRules(t *testing.T) {
	cases := []struct {
		name    string
		entry   PackageConfig
		wantErr string
	}{
		{"login cannot be per package",
			PackageConfig{Path: "tools/cli", Flow: &SpaceFlowConfig{Login: []string{"build"}}},
			"flow.login cannot be overridden per package"},
		{"an empty script command is rejected",
			PackageConfig{Path: "tools/cli", Scripts: map[string]Script{"lint": {"  "}}},
			`scripts["lint"] is empty`},
		{"a flow reference must resolve in the package's own scope",
			PackageConfig{Path: "tools/cli", Flow: &SpaceFlowConfig{Build: []string{"ghost"}}},
			`flow.build references unknown script "ghost"`},
		{"and so must a syncLock reference",
			PackageConfig{Path: "tools/cli",
				AutoVersion: &AutoVersionConfig{Enabled: models.Bool(true), SyncLock: []string{"ghost"}}},
			`autoVersion.syncLock references unknown script "ghost"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Packages = map[string]PackageConfig{"cli": c.entry}
			root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
			_, err := discoverPackages(t, root)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
			assert.Contains(t, err.Error(), `package "cli"`, "the error names the standalone package")
		})
	}

	// The same reference resolves once the package itself supplies the script,
	// which is the point of checking in the package's scope rather than the
	// file's.
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"cli": {
		Path:    "tools/cli",
		Flow:    &SpaceFlowConfig{Build: []string{"ghost"}},
		Scripts: map[string]Script{"ghost": {"echo boo"}},
	}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"echo boo"}, packagesByName(pkgs)["cli"].Space.BuildScript)
}

// TestStandaloneInFolderLayerIsValidatedToo: the in-folder file is a layer
// like any other, so its own mistakes are caught under a label naming the
// file that holds them.
func TestStandaloneInFolderLayerIsValidatedToo(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"cli": {Path: "tools/cli"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/cli")
	writePackageFile(t, root, "tools/cli", PackageConfig{Flow: &SpaceFlowConfig{Login: []string{"build"}}})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.login cannot be overridden per package")
	assert.Contains(t, err.Error(), "dispat.json", "the label names the file the layer came from")
}

// TestStandalonePathValidation: a standalone path must stay inside the
// repository — absolute paths and .. escapes are load errors, before any
// folder is consulted.
func TestStandalonePathValidation(t *testing.T) {
	abs := "/abs/cli"
	if os.PathSeparator == '\\' {
		abs = `C:\abs\cli`
	}
	for _, bad := range []string{abs, "../outside", "sub/../..", "."} {
		cfg := validConfig()
		cfg.Packages = map[string]PackageConfig{"cli": {Path: bad}}
		_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
		require.Errorf(t, err, "path %q must be rejected", bad)
		assert.Contains(t, err.Error(), `packages["cli"]`)
	}
}

// TestStandaloneOnlyConfig: a repository may consist of standalone packages
// alone — no spaces at all.
func TestStandaloneOnlyConfig(t *testing.T) {
	cfg := File{
		Scripts: map[string]Script{"build": {"echo b"}},
		Packages: map[string]PackageConfig{
			"cli": {Path: "tools/cli", Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
	}
	root := writeModelRepo(t, cfg, "tools/cli")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "cli", pkgs[0].Name)

	empty := File{Scripts: map[string]Script{"build": {"echo b"}}}
	_, err = loadModel(t, empty)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one space or package")
}

// TestPackageDependenciesCollected: provider lists merge from every layer —
// the root list, the packages entries (space and standalone packages alike)
// and the in-folder files — each edge carrying the declaring package as its
// consumer and the default kind.
func TestPackageDependenciesCollected(t *testing.T) {
	cfg := validConfig()
	cfg.Dependencies = []DependencyConfig{{Consumer: "app", Provider: "core"}}
	cfg.Packages = map[string]PackageConfig{
		"utils": {Dependencies: models.Providers("core")},
		"cli":   {Path: "tools/cli", Dependencies: models.Providers("core")},
	}
	root := writeModelRepo(t, cfg,
		"packages/libs/core", "packages/libs/utils", "packages/apps/app", "tools/cli")
	writePackageRaw(t, root, "packages/apps/app", map[string]any{"dependencies": "utils"})

	pkgs, declared, err := discoverAll(t, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 4)
	require.Len(t, declared, 4)

	assert.Equal(t, "app", declared[0].Consumer, "the root list comes first")
	assert.Equal(t, []string{"dependencies"}, declared[0].Source.KeyPath)
	assert.True(t, declared[0].Source.IsRootList())

	byConsumer := make(map[string]DeclaredDependency)
	for _, d := range declared[1:] {
		byConsumer[d.Consumer] = d
	}
	utils := byConsumer["utils"]
	assert.Equal(t, "core", utils.Provider)
	assert.Empty(t, utils.Kind, "package edges carry the default kind")
	assert.Equal(t, []string{"packages", "utils", "dependencies"}, utils.Source.KeyPath)
	assert.Empty(t, utils.Source.File, "an entry lives in the root config")

	app := byConsumer["app"]
	assert.Equal(t, "utils", app.Provider, "a scalar in-folder value lifts into the list")
	assert.Equal(t, []string{"dependencies"}, app.Source.KeyPath)
	assert.Contains(t, app.Source.File, filepath.Join("packages/apps/app", "dispat.json"))
	assert.False(t, app.Source.IsRootList())

	cli := byConsumer["cli"]
	assert.Equal(t, []string{"packages", "cli", "dependencies"}, cli.Source.KeyPath)

	// Discover validates the merged list like the root list.
	cfgLoaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, deps, err := Discover(cfgLoaded, root)
	require.NoError(t, err)
	assert.Len(t, deps, 4)
}

// TestPackageDependenciesInvalid: an empty provider name, a self-dependency
// and an unknown provider are rejected with the declaring source labeled.
func TestPackageDependenciesInvalid(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: models.Providers("")}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["utils"]: dependencies[0]`)
	assert.Contains(t, err.Error(), "must not be empty")

	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: models.Providers("Utils")}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot depend on itself")

	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: models.Providers("ghost")}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["utils"]: dependencies[0]: unknown provider package "ghost"`)
}

// TestDependencyArrayFormRefused: `dependencies` has one shape, an object
// keyed by consumer. An array — of full edges, of consumer-keyed groups, or
// empty — is refused at load, so a config written the other way is a mistake
// the author is told about rather than a second syntax to maintain.
func TestDependencyArrayFormRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps any
	}{
		{"edge objects", []any{map[string]any{"consumer": "app", "provider": "core"}}},
		{"consumer-keyed groups", []any{map[string]any{"web": []any{"core", "utils"}}}},
		{"empty", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeRawRepo(t, map[string]any{
				"scripts": map[string]any{"build": "echo b"},
				"spaces": map[string]any{
					"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
				},
				"dependencies": tc.deps,
			}, "pkgs/core")
			_, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "dependencies wants an object keyed by consumer")
		})
	}
}

// TestDependencyConsumerKeyRefused: the key an entry sits under is its
// consumer, so an entry naming a second one is refused like any other key
// that does not belong on a provider object.
func TestDependencyConsumerKeyRefused(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": map[string]any{
			"web": []any{map[string]any{"consumer": "app", "provider": "core"}},
		},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `dependencies["web"][0]: unknown key "consumer", want provider, kind or keep`)
}

// TestPackageDependenciesCarryKindAndKeep: a package's own list holds exactly
// what a consumer's list in the top-level object holds, so an edge needing a
// kind or a keep can live next to the rest of that package's configuration
// instead of being exiled to the root.
func TestPackageDependenciesCarryKindAndKeep(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{
				"path": "pkgs",
				"flow": map[string]any{"build": "build"},
				"packages": map[string]any{
					"web": map[string]any{"dependencies": []any{
						"core",
						map[string]any{"provider": "utils", "keep": true},
						map[string]any{"provider": "tooling", "kind": "devDependencies"},
					}},
				},
			},
		},
	}, "pkgs/core", "pkgs/utils", "pkgs/tooling", "pkgs/web")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)

	_, declared, err := DiscoverPackages(cfg, root)
	require.NoError(t, err)
	require.Len(t, declared, 3)
	for _, d := range declared {
		assert.Equal(t, "web", d.Consumer, "the consumer is the package the list belongs to")
	}
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "core"}, declared[0].DependencyConfig)
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "utils", Keep: true}, declared[1].DependencyConfig)
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "tooling", Kind: "devDependencies"},
		declared[2].DependencyConfig)

	// And the kind reaches the graph, which is the whole point of carrying it.
	_, deps, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, deps, 3)
	assert.Equal(t, model.KindDevDependencies, deps[2].Kind)
}

// TestPackageDependenciesScalarAndSelfReference: the one-name shorthand still
// works, and a package naming itself is refused wherever it is written.
func TestPackageDependenciesScalarAndSelfReference(t *testing.T) {
	base := func(deps any) map[string]any {
		return map[string]any{
			"scripts": map[string]any{"build": "echo b"},
			"spaces": map[string]any{
				"libs": map[string]any{
					"path":     "pkgs",
					"flow":     map[string]any{"build": "build"},
					"packages": map[string]any{"web": map[string]any{"dependencies": deps}},
				},
			},
		}
	}

	root := writeRawRepo(t, base("core"), "pkgs/core", "pkgs/web")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, deps, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "core", deps[0].Provider)

	root = writeRawRepo(t, base([]any{map[string]any{"provider": "web"}}), "pkgs/core", "pkgs/web")
	cfg, err = Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = DiscoverPackages(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "web" cannot depend on itself`)
}

// TestDependencyMapForm: the canonical shape — one object keyed by consumer,
// each value a provider name or an object saying more about the edge —
// through the real loader, viper's key folding and all.
func TestDependencyMapForm(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": map[string]any{
			"web": []any{"core", map[string]any{"provider": "utils", "keep": true}},
			"app": map[string]any{"provider": "core", "kind": "devDependencies"},
			"cli": "core",
		},
	}, "pkgs/core", "pkgs/utils", "pkgs/web", "pkgs/app", "pkgs/cli")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, Dependencies{
		{Consumer: "app", Provider: "core", Kind: "devDependencies"},
		{Consumer: "cli", Provider: "core"},
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
	}, cfg.Dependencies, "consumers sorted, each consumer's providers in file order")

	// And the whole graph is discoverable, which is what the form exists for.
	_, deps, err := Discover(cfg, root)
	require.NoError(t, err)
	assert.Len(t, deps, 4)
}

// TestDependencyMapFormMatchesPackageNamesCaseInsensitively: viper lowercases
// every map key, so a consumer keyed by a package whose folder carries capital
// letters arrives folded. It has to resolve back onto the package it names —
// the same rule `packages` and `spaces` entry keys already follow — or the
// map form would silently exclude every package that is not all lowercase.
func TestDependencyMapFormMatchesPackageNamesCaseInsensitively(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": map[string]any{"Web": []any{"Core"}},
	}, "pkgs/Core", "pkgs/Web")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)

	_, deps, err := Discover(cfg, root)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "Web", deps[0].Consumer, "the package's own spelling, not the folded key")
	assert.Equal(t, "Core", deps[0].Provider)
}

// TestCanonicaliseEndpoints covers the resolver on its own: two packages
// differing only in case cannot both exist on a case-insensitive filesystem,
// so the ambiguity it has to refuse is not something a temp directory can be
// made to reproduce.
func TestCanonicaliseEndpoints(t *testing.T) {
	pkgs := []*model.Package{{Name: "Core"}, {Name: "Web"}}
	src := DepSource{KeyPath: []string{"dependencies"}, Key: "web"}

	t.Run("a folded key resolves to the package it names", func(t *testing.T) {
		declared := []DeclaredDependency{
			{DependencyConfig: DependencyConfig{Consumer: "web", Provider: "core"}, Source: src},
		}
		require.NoError(t, canonicaliseEndpoints(declared, pkgs))
		assert.Equal(t, "Web", declared[0].Consumer)
		assert.Equal(t, "Core", declared[0].Provider)
	})

	t.Run("an endpoint naming nothing keeps the author's spelling", func(t *testing.T) {
		// compute loads configs Discover would refuse, and suggests removing
		// exactly these. Rewriting them would report a name nobody wrote.
		declared := []DeclaredDependency{
			{DependencyConfig: DependencyConfig{Consumer: "web", Provider: "Ghost"}, Source: src},
		}
		require.NoError(t, canonicaliseEndpoints(declared, pkgs))
		assert.Equal(t, "Ghost", declared[0].Provider)
	})

	t.Run("two packages differing only in case are ambiguous", func(t *testing.T) {
		declared := []DeclaredDependency{
			{DependencyConfig: DependencyConfig{Consumer: "web", Provider: "core"}, Source: src},
		}
		err := canonicaliseEndpoints(declared, []*model.Package{{Name: "Core"}, {Name: "core"}, {Name: "Web"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"core" matches packages Core, core ambiguously`)
		assert.Contains(t, err.Error(), `dependencies["web"][0]`, "the entry is located by consumer")
	})
}

// TestDependencyInvalidValue: a consumer's value that is neither a name nor
// an array of names fails the load with the consumer located.
func TestDependencyInvalidValue(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": map[string]any{"web": 7},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `dependencies["web"]`, "the consumer locates the mistake")
	assert.Contains(t, err.Error(), "wants a provider name, or an array of provider names and objects")
}

// TestPackageManifestNames: a package can be told what its manifests are
// called, through the entry or through its own in-folder file, with the more
// local layer replacing rather than extending the list.
func TestPackageManifestNames(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {ManifestNames: []string{"com.acme:core", "acme-core"}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, []string{"com.acme:core", "acme-core"}, byName["core"].ManifestNames)
	assert.Nil(t, byName["utils"].ManifestNames, "the key is the package's own, not the space's")

	// The in-folder file is the most local layer, and a list replaces: adding
	// to an inherited one could never take a name away again.
	writePackageFile(t, root, "packages/libs/core", PackageConfig{ManifestNames: []string{"only-this"}})
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"only-this"}, packagesByName(pkgs)["core"].ManifestNames)
}

// TestPackageManifestNamesMustBeUnique: a name two packages were told to
// answer to identifies neither, and unlike a name two manifests happen to
// declare (W220) it is a typo in the configuration rather than a fact about
// the repository, so it fails to load.
func TestPackageManifestNamesMustBeUnique(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core":  {ManifestNames: []string{"shared"}},
		"utils": {ManifestNames: []string{"shared"}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifestNames")
	assert.Contains(t, err.Error(), `"shared"`)
	assert.Contains(t, err.Error(), `package "core"`)
	assert.Contains(t, err.Error(), `package "utils"`)
}

// TestPackageManifestNamesRejectsAnEmptyName: an empty entry names nothing
// and would bind every unnamed manifest to the package.
func TestPackageManifestNamesRejectsAnEmptyName(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {ManifestNames: []string{""}}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifestNames: empty name")
}

// TestStandaloneManifestNames: a standalone package's entry is its whole
// configuration, so the key works there too.
func TestStandaloneManifestNames(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"tools": {Path: "tools", ManifestNames: []string{"com.acme:tools"}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"com.acme:tools"}, packagesByName(pkgs)["tools"].ManifestNames)
}

// --- The space's own packages map and the space folder's config file ---
//
// Two layers join the ladder between a space and one of its packages: the
// space's `packages` map in the root file, and a dispat config file in the
// space folder (whose root object is the space, and whose `packages` map is a
// layer of its own). Every claim below is made against the resolved
// model.Space, the same seam the older override tests use.

// TestSpacePackagesEntry: a space configures one of its own packages without
// the root file's global map, and reaches no other package.
func TestSpacePackagesEntry(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"core": {TagFormat: "core-{name}@{version}"}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, "core-{name}@{version}", byName["core"].Space.TagFormat)
	assert.Equal(t, "{name}@{version}", byName["utils"].Space.TagFormat, "a sibling keeps the space's format")
	assert.Equal(t, "{name}@{version}", byName["app"].Space.TagFormat, "another space is untouched")
}

// TestSpacePackagesEntryBeatsRootEntry: both maps name the same package, and
// the space's entry wins — it is the nearer statement about the package.
// Fields only one layer sets survive from both.
func TestSpacePackagesEntryBeatsRootEntry(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {TagFormat: "root-{name}@{version}", RevertOnFail: models.Bool(false)},
	}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{
			"core": {TagFormat: "space-{name}@{version}", IsBuildWaitingPublish: models.Bool(false)},
		}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, "space-{name}@{version}", core.Space.TagFormat)
	assert.False(t, core.Space.RevertOnFail, "the root entry still applies where the space says nothing")
	assert.False(t, core.Space.BuildWaitsPublish)
}

// TestSpaceFileOverridesRootSpace: the space folder's own config file is the
// space, said again and nearer: it replaces what it names and inherits the
// rest, for every package of the space.
func TestSpaceFileOverridesRootSpace(t *testing.T) {
	cfg := validConfig()
	cfg.Scripts["space-build"] = Script{"echo space-file build"}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		TagFormat:    "file-{name}@{version}",
		RevertOnFail: models.Bool(false),
		Flow:         &SpaceFlowConfig{Build: []string{"space-build"}},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	for _, name := range []string{"core", "utils"} {
		assert.Equal(t, "file-{name}@{version}", byName[name].Space.TagFormat, name)
		assert.False(t, byName[name].Space.RevertOnFail, "an explicit false overrides the root's true")
		assert.True(t, byName[name].Space.BuildWaitsPublish, "an unset key inherits")
		assert.Equal(t, []string{"echo space-file build"}, byName[name].Space.BuildScript)
		assert.Equal(t, []string{"echo publish"}, byName[name].Space.PublishScript, "an unset stage inherits")
	}
	assert.Equal(t, "{name}@{version}", byName["app"].Space.TagFormat, "another space is untouched")
}

// TestSpaceFileScriptsAndAutoVersion: a space file carries the space's whole
// surface, scripts and autoVersion included, and its packages resolve flow
// names through the merged map.
func TestSpaceFileScriptsAndAutoVersion(t *testing.T) {
	cfg := validConfig()
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		Scripts:     map[string]Script{"build": {"echo file build"}, "sync": {"echo sync"}},
		AutoVersion: &AutoVersionConfig{Enabled: models.Bool(true), SyncLock: []string{"sync"}},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, []string{"echo file build"}, core.Space.BuildScript, "the file's script wins by name")
	require.NotNil(t, core.Space.AutoVersion)
	assert.Equal(t, []string{"echo sync"}, core.Space.AutoVersion.SyncLock)
	assert.Nil(t, packagesByName(pkgs)["app"].Space.AutoVersion, "another space is untouched")
}

// TestOverrideLadder: all six layers name the same package and the same key,
// and the nearest to the package wins. Each layer also sets a key only it
// sets, proving every one of them was applied rather than skipped.
func TestOverrideLadder(t *testing.T) {
	cfg := validConfig()
	cfg.Scripts["s1"] = Script{"echo s1"}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.TagFormat = "s1-{name}@{version}"
		s.Scripts = map[string]Script{"space": {"echo space"}}
		s.Packages = map[string]PackageConfig{
			"core": {TagFormat: "p2-{name}@{version}", Scripts: map[string]Script{"p2": {"echo p2"}}},
		}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {TagFormat: "p1-{name}@{version}", Scripts: map[string]Script{"p1": {"echo p1"}}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		TagFormat: "s2-{name}@{version}",
		Scripts:   map[string]Script{"s2": {"echo s2"}},
		Packages: map[string]PackageConfig{
			"core": {TagFormat: "p3-{name}@{version}", Scripts: map[string]Script{"p3": {"echo p3"}}},
		},
	})
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		TagFormat: "p4-{name}@{version}",
		Scripts:   map[string]Script{"p4": {"echo p4"}},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, "p4-{name}@{version}", core.Space.TagFormat, "the package's own file is nearest")
	for _, name := range []string{"space", "s2", "p1", "p2", "p3", "p4"} {
		assert.Contains(t, core.Space.Scripts, name, "layer %q was applied", name)
	}
}

// TestOverrideLadderWithoutTheNearestLayers: the same ladder with the last
// layers removed, so each one in turn is the winner. One repository per
// prefix, because a layer cannot be unwritten.
func TestOverrideLadderWithoutTheNearestLayers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layers int // how many layers of the ladder are written
		want   string
	}{
		{"space alone", 1, "s1-{name}@{version}"},
		{"space file", 2, "s2-{name}@{version}"},
		{"root entry", 3, "p1-{name}@{version}"},
		{"space entry", 4, "p2-{name}@{version}"},
		{"space file entry", 5, "p3-{name}@{version}"},
		{"package file", 6, "p4-{name}@{version}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			withLibs(&cfg, func(s *SpaceConfig) {
				s.TagFormat = "s1-{name}@{version}"
				if tc.layers >= 4 {
					s.Packages = map[string]PackageConfig{"core": {TagFormat: "p2-{name}@{version}"}}
				}
			})
			if tc.layers >= 3 {
				cfg.Packages = map[string]PackageConfig{"core": {TagFormat: "p1-{name}@{version}"}}
			}
			root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
			if tc.layers >= 2 {
				sf := SpaceFile{TagFormat: "s2-{name}@{version}"}
				if tc.layers >= 5 {
					sf.Packages = map[string]PackageConfig{"core": {TagFormat: "p3-{name}@{version}"}}
				}
				writeSpaceFile(t, root, "packages/libs", sf)
			}
			if tc.layers >= 6 {
				writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "p4-{name}@{version}"})
			}
			pkgs, err := discoverPackages(t, root)
			require.NoError(t, err)
			assert.Equal(t, tc.want, packagesByName(pkgs)["core"].Space.TagFormat)
		})
	}
}

// TestSpaceLayersRecordPolicies: the package-only keys travel the new layers
// too, and in the same order as the space-shaped ones — a package's records
// and its flow must never disagree about which layer won.
func TestSpaceLayersRecordPolicies(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{
		"core": {Changelog: &ChangelogConfig{File: "ROOT.md", FileTitle: titleLine("# Root")}, Concurrency: []int{2}},
	}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{
			"core": {Changelog: &ChangelogConfig{File: "SPACE.md"}, ManifestNames: []string{"acme:core"}},
		}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		Packages: map[string]PackageConfig{"core": {Concurrency: []int{3, 1}}},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, "SPACE.md", core.Changelog.File, "the nearer layer names the file")
	assert.Equal(t, resolvedTitleLine("# Root"), core.Changelog.FileTitle, "and the farther one still fills what it left unset")
	assert.Equal(t, 3, core.BuildWeight)
	assert.Equal(t, 1, core.PublishWeight)
	assert.Equal(t, []string{"acme:core"}, core.ManifestNames)
}

// TestSpaceLayerDependencies: providers declared at the two new layers merge
// into the one list, each carrying the source that would be edited.
func TestSpaceLayerDependencies(t *testing.T) {
	cfg := validConfig()
	cfg.Dependencies = nil
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"utils": {Dependencies: models.Providers("core")}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/libs/web",
		"packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		Packages: map[string]PackageConfig{"web": {Dependencies: models.Providers("utils")}},
	})
	_, declared, err := discoverAll(t, root)
	require.NoError(t, err)

	labels := make(map[string]string, len(declared))
	for _, d := range declared {
		labels[d.Consumer+"->"+d.Provider] = d.Source.Label()
	}
	assert.Equal(t, `spaces["libs"]: packages["utils"]: dependencies[0]`, labels["utils->core"])
	assert.Equal(t, filepath.Join(root, "packages/libs", "dispat.json")+`: packages["web"]: dependencies[0]`,
		labels["web->utils"])
}

// TestSpaceFileAtTheRepositoryRoot: a space rooted at the repository itself
// has no space file — the file in that folder is the root config, and reading
// one statement twice would refuse itself as a nested root.
func TestSpaceFileAtTheRepositoryRoot(t *testing.T) {
	cfg := minimalConfig()
	cfg.Spaces = map[string]SpaceConfig{
		"all": {Path: PathList{"."}, Flow: &SpaceFlowConfig{Build: []string{"build"}}},
	}
	root := writeModelRepo(t, cfg, "core")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"core"}, packageNames(pkgs))
}

// packageNames lists discovered package names, for the cases where only the
// membership is the claim.
func packageNames(pkgs []*model.Package) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Name)
	}
	return out
}

// --- What the new layers may not say ---

// TestSpacePackagesPathRejected: a space configures the packages inside its
// folder and cannot move one elsewhere, so `path` is refused by name.
func TestSpacePackagesPathRejected(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"core": {Path: "elsewhere"}}
	})
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app", "elsewhere")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spaces["libs"]: packages["core"]`)
	assert.Contains(t, err.Error(), "path cannot be set")
}

// TestSpaceFilePackagesPathRejected: the same rule one level down, in the
// space folder's own file.
func TestSpaceFilePackagesPathRejected(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app", "elsewhere")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		Packages: map[string]PackageConfig{"core": {Path: "elsewhere"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be set")
	assert.Contains(t, err.Error(), filepath.Join("packages/libs", "dispat.json"))
}

// TestSpaceFilePathRejected: the file sits in the space folder, so the folder
// it lives in already is the path; a file able to redefine it could point the
// space somewhere it is not.
func TestSpaceFilePathRejected(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceRaw(t, root, "packages/libs", map[string]any{"path": "elsewhere"})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be set in a space folder's config file")
}

// TestSpaceFileNestedRoot: a space file declaring spaces is another
// repository's root config, not a layer of this one.
func TestSpaceFileNestedRoot(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceRaw(t, root, "packages/libs", map[string]any{
		"spaces": map[string]any{"inner": map[string]any{"path": "pkgs"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monorepo root of its own")
	assert.Contains(t, err.Error(), "drop the space from the root config")
}

// TestSpaceFileUnknownKey: the file is decoded with the root config's stance,
// so a typo is refused with the file named.
func TestSpaceFileUnknownKey(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceRaw(t, root, "packages/libs", map[string]any{"tagFormats": "v{version}"})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join("packages/libs", "dispat.json"))
	assert.Contains(t, err.Error(), "tagformats", "viper lowercases the key it reports")
}

// TestSpaceFileInvalidValue: the merged space is held to the space rules, so
// a bad value in the file fails the load with the file named.
func TestSpaceFileInvalidValue(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{Versioning: "sometimes"})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown versioning")
	assert.Contains(t, err.Error(), filepath.Join("packages/libs", "dispat.json"))
}

// TestPackageEntryHoldsNoSpaces: a package entry configures one package, so
// spaces and packages are refused by name at every map that holds entries —
// mapstructure's unknown-key error could not say why.
func TestPackageEntryHoldsNoSpaces(t *testing.T) {
	nested := map[string]any{
		"core": map[string]any{"packages": map[string]any{"inner": map[string]any{}}},
	}
	spaced := map[string]any{
		"core": map[string]any{"spaces": map[string]any{"inner": map[string]any{"path": "p"}}},
	}
	// The root file's own map first.
	for _, tc := range []struct {
		name    string
		entries map[string]any
		refused string
		label   string
	}{
		{"root packages", nested, "packages", `packages["core"]`},
		{"root packages, spaces", spaced, "spaces", `packages["core"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := rawValidConfig()
			raw["packages"] = tc.entries
			root := writeRawRepo(t, raw, "packages/libs/core", "packages/apps/app")
			_, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.label)
			assert.Contains(t, err.Error(), tc.refused+" cannot be set on a package entry")
		})
	}

	t.Run("space packages", func(t *testing.T) {
		raw := rawValidConfig()
		raw["spaces"].(map[string]any)["libs"].(map[string]any)["packages"] = nested
		root := writeRawRepo(t, raw, "packages/libs/core", "packages/apps/app")
		_, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `spaces["libs"]: packages["core"]`)
		assert.Contains(t, err.Error(), "packages cannot be set on a package entry")
	})

	t.Run("space file packages", func(t *testing.T) {
		root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
		writeSpaceRaw(t, root, "packages/libs", map[string]any{"packages": nested})
		_, err := discoverPackages(t, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "packages cannot be set on a package entry")
	})

	t.Run("package folder file", func(t *testing.T) {
		root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
		writePackageRaw(t, root, "packages/libs/core", map[string]any{
			"packages": map[string]any{"inner": map[string]any{}},
		})
		_, err := discoverPackages(t, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "monorepo root of its own")
	})
}

// rawValidConfig is validConfig as a raw map, for the shapes the typed model
// deliberately cannot express.
func rawValidConfig() map[string]any {
	return map[string]any{
		"scripts": map[string]any{"build": "echo build", "publish": "echo publish"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages/libs",
				"flow": map[string]any{"build": "build", "publish": "publish"}},
			"apps": map[string]any{"path": "packages/apps",
				"flow": map[string]any{"build": "build", "publish": "publish"}},
		},
	}
}

// TestSpacePackagesKeyMustMatchAFolder: a key naming nothing in the space is
// the same class of typo as an unknown dependency endpoint, and the error
// says where the package would have to be configured instead.
func TestSpacePackagesKeyMustMatchAFolder(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"app": {TagFormat: "v{version}"}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spaces["libs"]: packages["app"]`)
	assert.Contains(t, err.Error(), `matches no folder of space "libs"`)
}

// TestSpaceFilePackagesKeyMustMatchAFolder: the same accounting for the space
// file's map, named after the file that holds it.
func TestSpaceFilePackagesKeyMustMatchAFolder(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		Packages: map[string]PackageConfig{"nope": {TagFormat: "v{version}"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join("packages/libs", "dispat.json"))
	assert.Contains(t, err.Error(), `matches no folder of space "libs"`)
}

// TestSpacePackagesKeyExcluded: a key naming a folder the space's
// .dispatexclude hid gets the exclusion spelled out rather than the typo
// message.
func TestSpacePackagesKeyExcluded(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"sandbox": {TagFormat: "v{version}"}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/sandbox", "packages/apps/app")
	writeExclude(t, root, "packages/libs", "sandbox\n")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "excluded by "+DispatexcludeName)
}

// TestSpacePackagesKeyAmbiguous: one lowercased key matching two folders of
// the space has no single package to configure.
func TestSpacePackagesKeyAmbiguous(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"core": {TagFormat: "v{version}"}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	if err := os.Mkdir(filepath.Join(root, "packages/libs/Core"), 0o755); err != nil {
		t.Skip("case-insensitive filesystem")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "packages/libs")); err == nil && len(entries) < 2 {
		t.Skip("case-insensitive filesystem")
	}
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguously")
}

// TestSpacePackagesEmptyKey: a nameless entry configures nothing.
func TestSpacePackagesEmptyKey(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"": {TagFormat: "v{version}"}}
	})
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package name must not be empty")
}

// TestSpacePackagesLoginRejected: login runs once per space, in the space
// folder, so the override surface refuses it at the new layers too.
func TestSpacePackagesLoginRejected(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{
			"core": {Flow: &SpaceFlowConfig{Login: []string{"build"}}},
		}
	})
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.login cannot be overridden per package")
	assert.Contains(t, err.Error(), `spaces["libs"]: packages["core"]`)
}

// TestSpaceFileAutoVersionOnlyUnknown: the merged space's autoVersion is held
// to the same rule as the root file's, so an `only` naming no package fails.
func TestSpaceFileAutoVersionOnlyUnknown(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{
		AutoVersion: &AutoVersionConfig{Enabled: models.Bool(true), Only: []string{"ghost"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "autoVersion.only: unknown package")
}

// --- .dispatexclude over config file names ---

// TestDispatexcludeHidesAPackageConfig: a folder holding two config files says
// which one is real by ignoring the other, and the surviving name decides the
// package's override layer.
func TestDispatexcludeHidesAPackageConfig(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "json-{version}"})
	writeFolderConfig(t, root, "packages/libs/core", "dispat.yaml", PackageConfig{TagFormat: "yaml-{version}"})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "json-{version}", packagesByName(pkgs)["core"].Space.TagFormat,
		"without an ignore file the name order decides")

	writeExclude(t, root, "packages/libs/core", "# the json file is generated\ndispat.json\n")
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "yaml-{version}", packagesByName(pkgs)["core"].Space.TagFormat)
}

// TestDispatexcludeHidesASpaceConfig: the same rule in a space folder, where
// the ignore file already decides which sub-folders are packages.
func TestDispatexcludeHidesASpaceConfig(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/libs/sandbox", "packages/apps/app")
	writeSpaceFile(t, root, "packages/libs", SpaceFile{TagFormat: "json-{version}"})
	writeFolderConfig(t, root, "packages/libs", "dispat.yaml", SpaceFile{TagFormat: "yaml-{version}"})
	writeExclude(t, root, "packages/libs", "sandbox\ndispat.json\n")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, "yaml-{version}", byName["core"].Space.TagFormat)
	assert.NotContains(t, byName, "sandbox", "the folder patterns still hold")
}

// TestDispatexcludeHidesEveryConfig: ignoring every candidate leaves the
// folder with nothing to say, which is not an error — the folder simply has
// no override layer.
func TestDispatexcludeHidesEveryConfig(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "json-{version}"})
	writeExclude(t, root, "packages/libs/core", "dispat.*\n")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "{name}@{version}", packagesByName(pkgs)["core"].Space.TagFormat)
}

// TestDispatexcludeUnreadableHidingConfigs: an ignore file that cannot be read
// leaves it unknowable which config the folder meant, so the load fails
// instead of guessing.
func TestDispatexcludeUnreadableHidingConfigs(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{TagFormat: "json-{version}"})
	path := filepath.Join(root, "packages/libs/core", DispatexcludeName)
	require.NoError(t, os.WriteFile(path, []byte("dispat.json\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("running as a user that reads unreadable files")
	}
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), DispatexcludeName)
}

// TestPackageSrcNarrowsTheScopeFolder: src rides the override layers like
// every other package-only key, and lands on the resolved package as the
// folder its changes must sit under.
func TestPackageSrcNarrowsTheScopeFolder(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Src: "lib"}}
	root := writeModelRepo(t, cfg,
		"packages/libs/core/lib", "packages/libs/utils", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, "lib", byName["core"].Src)
	assert.Equal(t, filepath.Join(root, "packages", "libs", "core", "lib"), byName["core"].ScopeDir())
	assert.Empty(t, byName["utils"].Src, "a package that says nothing keeps its whole folder")
	assert.Equal(t, filepath.Join(root, "packages", "libs", "utils"), byName["utils"].ScopeDir())
}

// TestPackageSrcInFolderLayerWins: the layer nearest the package states the
// narrowing, exactly like manifestNames.
func TestPackageSrcInFolderLayerWins(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Src: "lib"}}
	root := writeModelRepo(t, cfg,
		"packages/libs/core/lib", "packages/libs/core/source", "packages/libs/utils", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{Src: "source"})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "source", packagesByName(pkgs)["core"].Src)
}

// TestPackageSrcValidation: a src that cannot narrow anything is refused at
// load, because the alternative is a package that quietly stops releasing.
func TestPackageSrcValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		src, want string
	}{
		"absolute":              {"/abs/lib", "relative to the package folder"},
		"escaping the package":  {"../elsewhere", "leaves the package folder"},
		"the package itself":    {".", "is the package folder itself"},
		"a folder that is gone": {"nope", "names no folder inside the package"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Packages = map[string]PackageConfig{"core": {Src: tc.src}}
			root := writeModelRepo(t, cfg,
				"packages/libs/core/lib", "packages/libs/utils", "packages/apps/app")
			_, err := discoverPackages(t, root)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	// A src naming a file rather than a folder is the same mistake in
	// another spelling.
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Src: "README.md"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "libs", "core", "README.md"), []byte("x"), 0o644))
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names a file, want a folder")
}

// TestAliasTagsResolveThroughTheLadder: aliasTags follows tagFormat's override
// ladder, and a nearer list replaces the inherited one whole rather than
// adding to it, which is the only way a package can drop its space's aliases.
func TestAliasTagsResolveThroughTheLadder(t *testing.T) {
	cfg := validConfig()
	cfg.AliasTags = []AliasTagConfig{{Format: "repo-v{major}"}}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.AliasTags = []AliasTagConfig{{Format: "libs-v{major}", Moving: true, Channels: []string{"stable"}}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)

	by := map[string]*model.Package{}
	for _, p := range pkgs {
		by[p.Name] = p
	}
	require.Contains(t, by, "core")
	assert.Equal(t, []model.AliasTag{
		{Format: "libs-v{major}", Moving: true, Channels: []string{"stable"}, Force: true},
	}, by["core"].Space.AliasTags, "the space's list wins over the repository's")
	require.Contains(t, by, "app")
	assert.Equal(t, []model.AliasTag{{Format: "repo-v{major}", Force: true}},
		by["app"].Space.AliasTags, "a space that declares none inherits the repository's")
}

// TestAliasTagsEmptyListOptsOut: an empty list is how a package says "none of
// my space's aliases", and it has to be written literally — the typed model
// omits an empty slice, so this is a raw config on purpose.
func TestAliasTagsEmptyListOptsOut(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{
				"path":      "pkgs",
				"flow":      map[string]any{"build": "build"},
				"tagFormat": "pkgs/{name}/v{version}",
				"aliasTags": []any{map[string]any{"format": "v{major}", "moving": true}},
				"packages": map[string]any{
					"utils": map[string]any{"aliasTags": []any{}},
				},
			},
		},
	}, "pkgs/core", "pkgs/utils")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)

	by := map[string]*model.Package{}
	for _, p := range pkgs {
		by[p.Name] = p
	}
	assert.Len(t, by["core"].Space.AliasTags, 1, "the space's alias reaches its packages")
	assert.Empty(t, by["utils"].Space.AliasTags, "an empty list declared on the package means none")
}

// TestAliasTagsMovingCannotOptOutOfForce: moving an alias *is* overwriting it,
// so the pair would silently produce an alias that stopped moving.
func TestAliasTagsMovingCannotOptOutOfForce(t *testing.T) {
	cfg := validConfig()
	cfg.AliasTags = []AliasTagConfig{{Format: "v{major}", Moving: true, Force: models.Bool(false)}}
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a moving alias cannot set force: false")
}

// TestAliasTagsMustNotBeReadableAsReleaseTags is the refusal the whole feature
// rests on. An alias is only ever written, so the one thing keeping it out of a
// package's history is its name not matching any package's tagFormat.
func TestAliasTagsMustNotBeReadableAsReleaseTags(t *testing.T) {
	t.Run("against its own package's format", func(t *testing.T) {
		cfg := validConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.TagFormat = "v{version}"
			s.AliasTags = []AliasTagConfig{{Format: "v{major}", Moving: true}}
		})
		root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = DiscoverPackages(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "would be read back as a release tag")
	})

	t.Run("against another package's format", func(t *testing.T) {
		// libs tags as "v{version}"; the alias belongs to a package in another
		// space, and would be read back as one of libs' releases.
		cfg := validConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.TagFormat = "v{version}" })
		cfg.Packages = map[string]PackageConfig{
			"tool": {Path: "tools/tool", AliasTags: []AliasTagConfig{{Format: "v{version}"}}},
		}
		root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/tool")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = DiscoverPackages(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "would be read back as a release tag")
	})

	t.Run("path-prefixed formats leave bare aliases alone", func(t *testing.T) {
		cfg := validConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.TagFormat = "packages/{name}/v{version}"
			s.AliasTags = []AliasTagConfig{{Format: "v{version}"}, {Format: "v{major}", Moving: true}}
		})
		root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = DiscoverPackages(loaded, root)
		assert.NoError(t, err, "this is the shape the feature exists for")
	})
}

// TestAliasTagsTwoPackagesOneName: a fixed group whose members all declare
// "v{major}" would force-move one ref between their commits every release.
func TestAliasTagsTwoPackagesOneName(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) {
		s.TagFormat = "packages/{name}/v{version}"
		s.AliasTags = []AliasTagConfig{{Format: "v{major}", Moving: true}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = DiscoverPackages(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both write the alias tag")
}

// TestSpaceDependencies: a space declares the edges between its own packages
// next to the space, in the same object keyed by consumer the root file uses,
// and every declaration merges into one list.
func TestSpaceDependencies(t *testing.T) {
	cfg := validConfig()
	cfg.Dependencies = nil
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Dependencies = Dependencies{
			{Consumer: "web", Provider: "core"},
			{Consumer: "web", Provider: "utils", Keep: true},
		}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils",
		"packages/libs/web", "packages/apps/app")

	_, declared, err := discoverAll(t, root)
	require.NoError(t, err)
	require.Len(t, declared, 2)
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "core"}, declared[0].DependencyConfig)
	assert.True(t, declared[1].Keep, "an entry object carries keep here as anywhere else")
	assert.Equal(t, `spaces["libs"]: dependencies["web"][0]`, declared[0].Source.Label(),
		"the source names the space that holds the object")
	assert.Equal(t, "libs", declared[0].Source.Space)
	assert.True(t, declared[0].Source.IsObjectList(), "written back keyed by consumer")
	assert.False(t, declared[0].Source.IsRootList(), "but it is not the root object")

	cfgLoaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, deps, err := Discover(cfgLoaded, root)
	require.NoError(t, err)
	assert.Len(t, deps, 2, "the edges reach the graph")
}

// TestSpaceDependenciesMustTouchTheSpace: the rule the level rests on. An
// edge with one endpoint in the space is fine wherever the other one lives;
// an edge with neither belongs in the root object, and saying so is more
// useful than silently accepting a declaration nobody would think to look for
// here.
func TestSpaceDependenciesMustTouchTheSpace(t *testing.T) {
	dirs := []string{"packages/libs/core", "packages/libs/utils", "packages/apps/app", "packages/apps/web"}

	t.Run("consumer in the space", func(t *testing.T) {
		cfg := validConfig()
		cfg.Dependencies = nil
		withLibs(&cfg, func(sc *SpaceConfig) {
			sc.Dependencies = Dependencies{{Consumer: "core", Provider: "app"}}
		})
		_, _, err := discoverAll(t, writeModelRepo(t, cfg, dirs...))
		require.NoError(t, err, "a cross-space edge may be declared by either end")
	})

	t.Run("provider in the space", func(t *testing.T) {
		cfg := validConfig()
		cfg.Dependencies = nil
		withLibs(&cfg, func(sc *SpaceConfig) {
			sc.Dependencies = Dependencies{{Consumer: "app", Provider: "core"}}
		})
		_, _, err := discoverAll(t, writeModelRepo(t, cfg, dirs...))
		require.NoError(t, err)
	})

	t.Run("neither endpoint in the space", func(t *testing.T) {
		cfg := validConfig()
		cfg.Dependencies = nil
		withLibs(&cfg, func(sc *SpaceConfig) {
			sc.Dependencies = Dependencies{{Consumer: "web", Provider: "app"}}
		})
		_, _, err := discoverAll(t, writeModelRepo(t, cfg, dirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`spaces["libs"]: dependencies["web"][0]: neither consumer "web" nor provider "app" is a package of space "libs"`)
		assert.Contains(t, err.Error(), "belongs in the root dependencies object",
			"the refusal says where the edge does belong")
	})

	t.Run("an unknown endpoint is left to Discover", func(t *testing.T) {
		cfg := validConfig()
		cfg.Dependencies = nil
		withLibs(&cfg, func(sc *SpaceConfig) {
			sc.Dependencies = Dependencies{{Consumer: "ghost", Provider: "phantom"}}
		})
		root := writeModelRepo(t, cfg, dirs...)
		_, _, err := discoverAll(t, root)
		require.NoError(t, err, "compute loads configs Discover refuses, to suggest removing them")

		loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
		require.NoError(t, err)
		_, _, err = Discover(loaded, root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown consumer package "ghost"`)
	})
}

// TestSpaceDependenciesSelfEdge: a package depending on itself is refused in
// a space's object with the space named, like it is everywhere else.
func TestSpaceDependenciesSelfEdge(t *testing.T) {
	cfg := validConfig()
	cfg.Dependencies = nil
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Dependencies = Dependencies{{Consumer: "core", Provider: "core"}}
	})
	_, _, err := discoverAll(t, writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spaces["libs"]: dependencies[0]: package "core" cannot depend on itself`)
}

// TestSpaceFileDependencies: the space folder's own config file declares
// edges too, and they add to what the root file's space entry says rather
// than replacing it — dependency declarations never override.
func TestSpaceFileDependencies(t *testing.T) {
	cfg := validConfig()
	cfg.Dependencies = nil
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Dependencies = Dependencies{{Consumer: "web", Provider: "core"}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils",
		"packages/libs/web", "packages/apps/app")
	writeFolderConfig(t, root, "packages/libs", "dispat.json", map[string]any{
		"dependencies": map[string]any{"web": []any{"utils"}},
	})

	_, declared, err := discoverAll(t, root)
	require.NoError(t, err)
	require.Len(t, declared, 2, "both objects count")
	assert.Equal(t, "core", declared[0].Provider, "the root file's space entry comes first")
	assert.Equal(t, "utils", declared[1].Provider)
	assert.Equal(t, "libs", declared[1].Source.Space)
	assert.Contains(t, declared[1].Source.Label(), "dispat.json: dependencies[\"web\"][0]",
		"the source names the file that holds the object")
	assert.True(t, declared[1].Source.IsObjectList(),
		"a space file's object is written back keyed by consumer, unlike a package file's list")
}

// TestRootDefaultsReachEverySpace: the root file is the bottom layer of the
// same ladder every other level folds through, so a space-shaped key stated
// once at the top reaches every space and every standalone package.
func TestRootDefaultsReachEverySpace(t *testing.T) {
	cfg := validConfig()
	cfg.Flow = &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
	cfg.IsBuildWaitingPublish = models.Bool(true)
	cfg.RevertOnFail = models.Bool(true)
	cfg.Versioning = "fixed"
	cfg.AutoVersion = &AutoVersionConfig{Enabled: models.Bool(true)}
	// The spaces say nothing at all, so everything they have comes from above.
	cfg.Spaces = map[string]SpaceConfig{
		"libs": {Path: PathList{"packages/libs"}},
		"apps": {Path: PathList{"packages/apps"}},
	}
	cfg.Packages = map[string]PackageConfig{"tool": {Path: "tools/tool"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/tool")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	require.Len(t, byName, 3)
	for _, name := range []string{"core", "app", "tool"} {
		sp := byName[name].Space
		assert.Equal(t, []string{"echo build"}, sp.BuildScript, name)
		assert.True(t, sp.BuildWaitsPublish, name)
		assert.True(t, sp.RevertOnFail, name)
		assert.Equal(t, model.Versioning("fixed"), sp.Versioning, name)
		assert.NotNil(t, sp.AutoVersion, name)
	}
	assert.Equal(t, "libs", byName["core"].Space.VersionGroup,
		"a root versioning mode applies under each space's own implicit group")
	assert.Equal(t, "apps", byName["app"].Space.VersionGroup)
	assert.Equal(t, "tool", byName["tool"].Space.VersionGroup,
		"a standalone package is its own group")
}

// TestRootDefaultsAreOverriddenAtEveryLevel: each level below the root can
// still say otherwise, including saying "false" against a root "true" — which
// is why the space booleans are tri-state.
func TestRootDefaultsAreOverriddenAtEveryLevel(t *testing.T) {
	cfg := validConfig()
	cfg.Scripts["build-libs"] = Script{"echo libs"}
	cfg.Scripts["build-core"] = Script{"echo core"}
	cfg.Flow = &SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
	cfg.RevertOnFail = models.Bool(true)
	cfg.Versioning = "fixed"
	cfg.Spaces = map[string]SpaceConfig{
		// The space overrides one flow entry and keeps the rest.
		"libs": {Path: PathList{"packages/libs"}, Flow: &SpaceFlowConfig{Build: []string{"build-libs"}}},
		"apps": {Path: PathList{"packages/apps"}, Versioning: "independent", RevertOnFail: models.Bool(false)},
	}
	cfg.Packages = map[string]PackageConfig{
		"core": {Flow: &SpaceFlowConfig{Build: []string{"build-core"}}, RevertOnFail: models.Bool(false)},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, []string{"echo core"}, byName["core"].Space.BuildScript, "the package is nearest")
	assert.Equal(t, []string{"echo libs"}, byName["utils"].Space.BuildScript, "its space is next")
	assert.Equal(t, []string{"echo build"}, byName["app"].Space.BuildScript, "the root is the floor")
	assert.Equal(t, []string{"echo publish"}, byName["core"].Space.PublishScript,
		"an override replaces one entry, not the whole flow")

	assert.False(t, byName["core"].Space.RevertOnFail, "a package says false against a root true")
	assert.False(t, byName["app"].Space.RevertOnFail, "and so does a space")
	assert.True(t, byName["utils"].Space.RevertOnFail, "a sibling that says nothing keeps the root's")

	assert.Equal(t, model.Versioning("independent"), byName["app"].Space.Versioning)
	assert.Equal(t, model.Versioning("fixed"), byName["core"].Space.Versioning)
}

// TestRootFlowLoginRunsPerSpace: login is a space-level script, and declaring
// it once at the root means every space runs that one — not that a package
// may have one of its own, which is still refused.
func TestRootFlowLoginRunsPerSpace(t *testing.T) {
	cfg := validConfig()
	cfg.Scripts["login"] = Script{"npm login"}
	cfg.Flow = &SpaceFlowConfig{Login: []string{"login"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	for _, p := range packagesByName(pkgs) {
		assert.Equal(t, []string{"npm login"}, p.Space.LoginScript, p.Name)
	}

	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{Login: []string{"login"}}}}
	_, err = discoverPackages(t, writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow.login cannot be overridden per package")
}

// TestRootAliasTagsAndTagFormatFold: the two keys that used to fall back to
// the root inside buildSpace are ordinary layers now, so an empty list at any
// level still means "no aliases" rather than "inherit".
func TestRootAliasTagsAndTagFormatFold(t *testing.T) {
	cfg := validConfig()
	cfg.TagFormat = "root-{name}@{version}"
	cfg.AliasTags = []AliasTagConfig{{Format: "{name}-v{major}", Moving: true}}
	withLibs(&cfg, func(sc *SpaceConfig) { sc.TagFormat = "libs-{name}@{version}" })
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	assert.Equal(t, "libs-{name}@{version}", byName["core"].Space.TagFormat, "the space wins")
	assert.Equal(t, "root-{name}@{version}", byName["app"].Space.TagFormat, "the root is the fallback")
	require.Len(t, byName["core"].Space.AliasTags, 1, "inherited from the root")
	assert.Equal(t, "{name}-v{major}", byName["core"].Space.AliasTags[0].Format)

	// An explicit empty list opts a package out. omitempty drops one from a
	// marshalled model, so this case has to be written raw.
	raw := map[string]any{
		"scripts":   map[string]any{"build": "echo b"},
		"aliasTags": []any{map[string]any{"format": "{name}-v{major}", "moving": true}},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"packages": map[string]any{"app": map[string]any{"aliasTags": []any{}}},
	}
	pkgs, err = discoverPackages(t, writeRawRepo(t, raw, "pkgs/core", "pkgs/app"))
	require.NoError(t, err)
	byName = packagesByName(pkgs)
	assert.Len(t, byName["core"].Space.AliasTags, 1, "the sibling still inherits")
	assert.Empty(t, byName["app"].Space.AliasTags, "an explicit empty list is a decision, not a gap")
}

// TestRootVersioningInvalid: a typo in the root's own versioning is reported
// against the key that holds it, even when every space overrides it and the
// value never reaches a package.
func TestRootVersioningInvalid(t *testing.T) {
	cfg := validConfig()
	cfg.Versioning = "fixxed"
	withLibs(&cfg, func(sc *SpaceConfig) { sc.Versioning = "independent" })
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `versioning "fixxed" is invalid`)
}

// TestSpaceRecordsSrcAndConcurrency: the four keys that used to skip the
// space level. Each is stated once for a whole space, and a package can still
// depart from it.
func TestSpaceRecordsSrcAndConcurrency(t *testing.T) {
	cfg := validConfig()
	cfg.Changelog = &ChangelogConfig{Enabled: models.Bool(true), File: "ROOT.md"}
	cfg.GitHub = &GitHubConfig{Enabled: models.Bool(true), Owner: "acme"}
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Changelog = &ChangelogConfig{File: "LIBS.md"} // enabled inherited
		sc.GitHub = &GitHubConfig{Repo: "libs"}          // owner inherited
		sc.Src = "src"
		sc.Concurrency = []int{2}
	})
	cfg.Packages = map[string]PackageConfig{
		"utils": {Src: "lib", Concurrency: []int{3, 1}, Changelog: &ChangelogConfig{File: "UTILS.md"}},
	}
	root := writeModelRepo(t, cfg,
		"packages/libs/core/src", "packages/libs/utils/lib", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	core := byName["core"]
	assert.Equal(t, "src", core.Src, "the space states the layout once")
	assert.Equal(t, "LIBS.md", core.Changelog.File)
	assert.True(t, core.Changelog.Enabled, "the root's enabled shows through the space's overlay")
	assert.Equal(t, "acme", core.GitHub.Owner, "and so does the root's owner")
	assert.Equal(t, "libs", core.GitHub.Repo)
	assert.Equal(t, 2, core.BuildWeight, "the space's weight reaches its packages")
	assert.Equal(t, 2, core.PublishWeight)

	utils := byName["utils"]
	assert.Equal(t, "lib", utils.Src, "a package departs from its space")
	assert.Equal(t, "UTILS.md", utils.Changelog.File)
	assert.True(t, utils.Changelog.Enabled, "still enabled from the root")
	assert.Equal(t, 3, utils.BuildWeight)
	assert.Equal(t, 1, utils.PublishWeight)

	app := byName["app"]
	assert.Empty(t, app.Src, "another space is untouched")
	assert.Equal(t, "ROOT.md", app.Changelog.File)
	assert.Equal(t, 1, app.BuildWeight, "and keeps the ordinary weight")
}

// TestRootSrcReachesEveryPackage: a repository whose packages all keep their
// sources in the same sub-folder says so once. The strictness is the point:
// a package without the folder is a load error rather than a package that
// silently owns no files.
func TestRootSrcReachesEveryPackage(t *testing.T) {
	cfg := validConfig()
	cfg.Src = "src"
	cfg.Packages = map[string]PackageConfig{"tool": {Path: "tools/tool"}}
	root := writeModelRepo(t, cfg,
		"packages/libs/core/src", "packages/apps/app/src", "tools/tool/src")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	for _, p := range pkgs {
		assert.Equal(t, "src", p.Src, p.Name)
		assert.Equal(t, filepath.Join(p.Dir, "src"), p.ScopeDir(), p.Name)
	}

	// One package laid out differently fails the load, and overriding src for
	// that package is the fix.
	root = writeModelRepo(t, cfg, "packages/libs/core/src", "packages/apps/app", "tools/tool/src")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `package "app": src "src" names no folder inside the package`)

	cfg.Packages["app"] = PackageConfig{Src: "lib"}
	root = writeModelRepo(t, cfg, "packages/libs/core/src", "packages/apps/app/lib", "tools/tool/src")
	pkgs, err = discoverPackages(t, root)
	require.NoError(t, err)
	assert.Equal(t, "lib", packagesByName(pkgs)["app"].Src)
}

// TestSpaceFileRecordsAndSrc: the space folder's own config file states them
// too, nearer than the root file's space entry and still under the package.
func TestSpaceFileRecordsAndSrc(t *testing.T) {
	cfg := validConfig()
	cfg.Changelog = &ChangelogConfig{Enabled: models.Bool(true), File: "ROOT.md"}
	withLibs(&cfg, func(sc *SpaceConfig) { sc.Src = "src" })
	root := writeModelRepo(t, cfg, "packages/libs/core/lib", "packages/apps/app")
	writeFolderConfig(t, root, "packages/libs", "dispat.json", map[string]any{
		"src":       "lib",
		"changelog": map[string]any{"file": "LIBS.md"},
	})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]
	assert.Equal(t, "lib", core.Src, "the space file is nearer than the space entry")
	assert.Equal(t, "LIBS.md", core.Changelog.File)
	assert.True(t, core.Changelog.Enabled, "the root's enabled still shows through")
}

// TestSpaceConcurrencyInvalid: a space's weight is held to the same rule a
// package's is, with the space named.
func TestSpaceConcurrencyInvalid(t *testing.T) {
	cfg := validConfig()
	withLibs(&cfg, func(sc *SpaceConfig) { sc.Concurrency = []int{1, 2, 3} })
	_, err := loadModel(t, cfg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "concurrency accepts at most two values")
}

// writeIgnoreFile drops a .dispatignore into a folder.
func writeIgnoreFile(t *testing.T, root, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(dir), DispatignoreName),
		[]byte(body), 0o644))
}

// TestIgnoreChainLevelsConcatenate: the change-scope patterns of every level
// apply together, nearest last, so a package can re-include what the
// repository excluded.
func TestIgnoreChainLevelsConcatenate(t *testing.T) {
	cfg := validConfig()
	cfg.Ignore = []string{"*.md"}
	withLibs(&cfg, func(sc *SpaceConfig) { sc.Ignore = []string{"fixtures/"} })
	cfg.Packages = map[string]PackageConfig{
		"core": {Ignore: []string{"!README.md", "scratch/"}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	core := byName["core"]
	assert.True(t, core.Ignore.Ignores(slash(root, "packages/libs/core/docs/guide.md")),
		"the repository level reaches every package")
	assert.False(t, core.Ignore.Ignores(slash(root, "packages/libs/core/README.md")),
		"and the package lifts it for itself")
	assert.True(t, core.Ignore.Ignores(slash(root, "packages/libs/core/scratch/x.go")))
	assert.False(t, core.Counts(slash(root, "packages/libs/core/scratch/x.go")))
	assert.True(t, core.Counts(slash(root, "packages/libs/core/main.go")),
		"everything nobody excluded still counts")

	utils := byName["utils"]
	assert.True(t, utils.Ignore.Ignores(slash(root, "packages/libs/utils/README.md")),
		"a sibling does not inherit the package's re-inclusion")
	assert.True(t, utils.Ignore.Ignores(slash(root, "packages/libs/utils/fixtures/a.json")),
		"the space level reaches its own packages")

	app := byName["app"]
	assert.False(t, app.Ignore.Ignores(slash(root, "packages/apps/app/fixtures/a.json")),
		"and not another space's")
	assert.True(t, app.Ignore.Ignores(slash(root, "packages/apps/app/notes.md")))
}

// slash builds the absolute slash-separated path the planner asks about.
func slash(root, rel string) string {
	return filepath.ToSlash(filepath.Join(root, filepath.FromSlash(rel)))
}

// TestIgnoreFileAndKeyAgree: a .dispatignore file says what the `ignore` key
// says, at whichever level it sits in, and the file is read after the key of
// the same level.
func TestIgnoreFileAndKeyAgree(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Ignore: []string{"docs/"}}}
	root := writeModelRepo(t, cfg, "packages/libs/core/docs", "packages/libs/utils", "packages/apps/app")
	writeIgnoreFile(t, root, ".", "*.md\n")
	writeIgnoreFile(t, root, "packages/libs", "# not a release trigger\nfixtures/\n")
	writeIgnoreFile(t, root, "packages/libs/core", "!docs/api.md\n")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	core := byName["core"]
	assert.True(t, core.Ignore.Ignores(slash(root, "packages/libs/core/docs/guide.md")),
		"the entry's key applies")
	assert.False(t, core.Ignore.Ignores(slash(root, "packages/libs/core/docs/api.md")),
		"and the folder's file has the last word at that level")
	assert.True(t, byName["utils"].Ignore.Ignores(slash(root, "packages/libs/utils/fixtures/a.json")))
	assert.True(t, byName["app"].Ignore.Ignores(slash(root, "packages/apps/app/notes.md")),
		"the repository's own file reaches every space")
}

// TestIgnorePackageLayersAccumulate: the four package layers add patterns
// rather than replacing them, which is the one merge rule this key has.
func TestIgnorePackageLayersAccumulate(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Ignore: []string{"docs/"}}}
	withLibs(&cfg, func(sc *SpaceConfig) {
		sc.Packages = map[string]PackageConfig{"core": {Ignore: []string{"fixtures/"}}}
	})
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{Ignore: []string{"scratch/"}})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]
	for _, rel := range []string{"docs/a.md", "fixtures/a.json", "scratch/a.go"} {
		assert.True(t, core.Ignore.Ignores(slash(root, "packages/libs/core/"+rel)), rel)
	}
	assert.True(t, core.Counts(slash(root, "packages/libs/core/main.go")))
}

// TestIgnoreStandalonePackage: a package outside every space still sits under
// the repository's patterns and carries its own.
func TestIgnoreStandalonePackage(t *testing.T) {
	cfg := validConfig()
	cfg.Ignore = []string{"*.md"}
	cfg.Packages = map[string]PackageConfig{"tool": {Path: "tools/tool", Ignore: []string{"testdata/"}}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/tool")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	tool := packagesByName(pkgs)["tool"]
	assert.True(t, tool.Ignore.Ignores(slash(root, "tools/tool/README.md")))
	assert.True(t, tool.Ignore.Ignores(slash(root, "tools/tool/testdata/a.json")))
	assert.True(t, tool.Counts(slash(root, "tools/tool/main.go")))
}

// TestIgnoreNothingDeclared: the common case costs nothing — no patterns
// anywhere means no chain to walk.
func TestIgnoreNothingDeclared(t *testing.T) {
	pkgs, err := discoverPackages(t, writeModelRepo(t, validConfig(),
		"packages/libs/core", "packages/apps/app"))
	require.NoError(t, err)
	for _, p := range pkgs {
		assert.Empty(t, p.Ignore, p.Name)
		assert.True(t, p.Counts(slash(p.Dir, "anything.md")), p.Name)
	}
}

// TestIgnoreInvalidPattern: a pattern that cannot be carried out fails the
// load, with the folder that holds it named.
func TestIgnoreInvalidPattern(t *testing.T) {
	cfg := validConfig()
	cfg.Packages = map[string]PackageConfig{"core": {Ignore: []string{"docs/", "!"}}}
	_, err := discoverPackages(t, writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-includes nothing")
	assert.Contains(t, err.Error(), `package "core"`, "the refusal says whose pattern it is")
}

// TestIgnoreFileUnreadable: a .dispatignore that cannot be read (here: a
// directory of that name) fails the load with the file named, rather than
// silently counting files the author meant to exclude.
func TestIgnoreFileUnreadable(t *testing.T) {
	for _, dir := range []string{".", "packages/libs", "packages/libs/core"} {
		t.Run(dir, func(t *testing.T) {
			root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
			require.NoError(t, os.MkdirAll(
				filepath.Join(root, filepath.FromSlash(dir), DispatignoreName, "x"), 0o755))
			_, err := discoverPackages(t, root)
			require.Error(t, err)
			assert.Contains(t, err.Error(), DispatignoreName)
		})
	}
}

// TestIgnoreFileInASpaceFolder: the space level can be written as a file too,
// and it reaches every package of that space and no other.
func TestIgnoreFileInASpaceFolder(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	writeIgnoreFile(t, root, "packages/libs", "fixtures/\n")

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)
	for _, name := range []string{"core", "utils"} {
		assert.True(t, byName[name].Ignore.Ignores(
			slash(root, "packages/libs/"+name+"/fixtures/a.json")), name)
	}
	assert.True(t, byName["app"].Counts(slash(root, "packages/apps/app/fixtures/a.json")),
		"another space is untouched")
}
