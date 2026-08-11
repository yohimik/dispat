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
	data, err := json.MarshalIndent(po, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, pkgDir, "dispat.json"), data, 0o644))
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
	cfg.Scripts["alt"] = "echo alt"
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
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages/libs", DispatignoreName), []byte("core\n"), 0o644))
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `excluded by `+DispatignoreName)
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
// monorepo's root config, not an override; the error points at .dispatignore
// instead of half-merging another repository's configuration.
func TestInFolderNestedRoot(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	writePackageRaw(t, root, "packages/libs/core", map[string]any{
		"spaces": map[string]any{"inner": map[string]any{"path": "pkgs"}},
	})
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monorepo root of its own")
	assert.Contains(t, err.Error(), DispatignoreName)
}

// TestDispatignore: patterns match direct sub-folder names — exact names and
// * globs — with blank lines and # comments skipped; other spaces are
// untouched.
func TestDispatignore(t *testing.T) {
	root := writeModelRepo(t, validConfig(),
		"packages/libs/core", "packages/libs/sandbox", "packages/libs/tmp-a", "packages/libs/tmp-b",
		"packages/apps/app", "packages/apps/sandbox2")
	ignore := "# scratch folders are not packages\n\nsandbox\ntmp-*\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages/libs", DispatignoreName), []byte(ignore), 0o644))
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
	cfg.Changelog = &ChangelogConfig{File: "GLOBAL.md", Title: "# Global"}
	cfg.GitHub = &GitHubConfig{Owner: "acme", Repo: "mono", TokenEnv: "GLOBAL_TOKEN"}
	cfg.Packages = map[string]PackageConfig{
		"core": {
			Changelog: &ChangelogConfig{File: "ENTRY.md"},
			GitHub:    &GitHubConfig{Repo: "entry-repo"},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	writePackageFile(t, root, "packages/libs/core", PackageConfig{
		Changelog: &ChangelogConfig{Title: "# Local"},
		GitHub:    &GitHubConfig{APIURL: "https://ghe"},
	})
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	core := packagesByName(pkgs)["core"]

	assert.Equal(t, "ENTRY.md", core.Changelog.File, "the entry's value survives the second layer")
	assert.Equal(t, "# Local", core.Changelog.Title, "which sets only what it names")
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
		"libs": {Path: "pkgs", Flow: &SpaceFlowConfig{}, VersionGroup: "nope"},
	}}
	_, _, err := DiscoverPackages(cfg, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches no versionGroups entry and no space")
}

// TestDispatignoreUnreadable: a .dispatignore that cannot be read (here: a
// directory of that name) fails the discovery with the file named, rather
// than silently treating every folder as a package.
func TestDispatignoreUnreadable(t *testing.T) {
	root := writeModelRepo(t, validConfig(), "packages/libs/core", "packages/apps/app")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/libs", DispatignoreName, "x"), 0o755))
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), DispatignoreName)
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
		&ChangelogConfig{File: "HISTORY.md", Title: "# H", EntryFormatConfig: baseFormat},
		&ChangelogConfig{Title: "# Other"})
	assert.Equal(t, "# Other", cl.Title)
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
		s.Scripts = map[string]string{"lint": "space lint", "fmt": "space fmt", "build": "space build"}
	})
	cfg.Packages = map[string]PackageConfig{
		"core": {Scripts: map[string]string{"lint": "core lint", "extra": "core extra"}},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, map[string]string{"lint": "core lint", "fmt": "space fmt", "extra": "core extra",
		"build": "space build", "publish": "echo publish"}, byName["core"].Space.Scripts)
	assert.Equal(t, map[string]string{"lint": "space lint", "fmt": "space fmt",
		"build": "space build", "publish": "echo publish"},
		byName["utils"].Space.Scripts, "the space's own map is not mutated by the merge")
	assert.Equal(t, map[string]string{"build": "echo build", "publish": "echo publish"},
		byName["app"].Space.Scripts, "another space sees the file's scripts alone")

	// The precedence is the one the flow resolution uses, not a separate rule.
	assert.Equal(t, []string{"space build"}, byName["core"].Space.BuildScript)
	assert.Equal(t, []string{"echo build"}, byName["app"].Space.BuildScript)
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
			Scripts:     map[string]string{"tidy": "go mod tidy"},
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
			PackageConfig{Path: "tools/cli", Scripts: map[string]string{"lint": "  "}},
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
		Scripts: map[string]string{"ghost": "echo boo"},
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
		Scripts: map[string]string{"build": "echo b"},
		Packages: map[string]PackageConfig{
			"cli": {Path: "tools/cli", Flow: &SpaceFlowConfig{Build: []string{"build"}}},
		},
	}
	root := writeModelRepo(t, cfg, "tools/cli")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "cli", pkgs[0].Name)

	empty := File{Scripts: map[string]string{"build": "echo b"}}
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
		"utils": {Dependencies: []string{"core"}},
		"cli":   {Path: "tools/cli", Dependencies: []string{"core"}},
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
	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: []string{""}}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	_, err := discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["utils"]: dependencies[0]`)
	assert.Contains(t, err.Error(), "must not be empty")

	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: []string{"Utils"}}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	_, err = discoverPackages(t, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot depend on itself")

	cfg.Packages = map[string]PackageConfig{"utils": {Dependencies: []string{"ghost"}}}
	root = writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/utils", "packages/apps/app")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	_, _, err = Discover(loaded, root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages["utils"]: dependencies[0]: unknown provider package "ghost"`)
}

// TestDependencyShorthand: a dependencies item keyed by consumer name — its
// value one provider or an array — expands into full entries at load, next
// to the canonical objects.
func TestDependencyShorthand(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": []any{
			map[string]any{"consumer": "app", "provider": "core", "kind": "devDependencies"},
			map[string]any{"web": []any{"core", "utils"}},
			map[string]any{"app": "utils"},
		},
	}, "pkgs/core")
	cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 4)
	assert.Equal(t, DependencyConfig{Consumer: "app", Provider: "core", Kind: "devDependencies"},
		cfg.Dependencies[0], "the full object decodes as before")
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "core"}, cfg.Dependencies[1])
	assert.Equal(t, DependencyConfig{Consumer: "web", Provider: "utils"}, cfg.Dependencies[2])
	assert.Equal(t, DependencyConfig{Consumer: "app", Provider: "utils"}, cfg.Dependencies[3])
}

// TestDependencyShorthandInvalidValue: a shorthand value that is neither a
// name nor an array of names fails the load with the item located.
func TestDependencyShorthandInvalidValue(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}},
		},
		"dependencies": []any{map[string]any{"web": 7}},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider name or an array of names")
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
