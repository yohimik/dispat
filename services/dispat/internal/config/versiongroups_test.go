package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// A versioning group's `versioning` key carries three axes and two shapes.
// What is tested here is that both shapes reach the same rule through every
// format the CLI reads, that each way of writing an impossible rule is
// refused by name, that the object belongs to a group and nowhere else, and
// that the rule reaches every member's resolved space.

// groupConfig is validConfig with one declared group that libs joins.
func groupConfig(rule VersionGroupConfig) File {
	cfg := validConfig()
	cfg.VersionGroups = map[string]VersionGroupConfig{"platform": rule}
	withLibs(&cfg, func(s *SpaceConfig) { s.VersionGroup = "platform" })
	return cfg
}

func TestVersionGroupAxesLoadFromEveryFormat(t *testing.T) {
	// The object form written by the model's own marshaller, read back
	// through each format, exactly as TestLoadFormats does for the rest of
	// the language.
	cfg := groupConfig(VersionGroupConfig{
		Versioning: VersioningFixedMajorMinor,
		Counter:    SharingIndependent,
		Channels:   SharingIndependent,
	})
	base, err := json.Marshal(cfg)
	require.NoError(t, err)
	var tree map[string]any
	require.NoError(t, json.Unmarshal(base, &tree))

	marshallers := map[string]func() ([]byte, error){
		"json": func() ([]byte, error) { return json.MarshalIndent(cfg, "", "  ") },
		"yaml": func() ([]byte, error) { return yaml.Marshal(tree) },
		"toml": func() ([]byte, error) { return toml.Marshal(tree) },
	}
	for format, marshal := range marshallers {
		t.Run(format, func(t *testing.T) {
			data, err := marshal()
			require.NoError(t, err)
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "libs", "core"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "apps", "app"), 0o755))
			path := filepath.Join(root, "dispat."+format)
			require.NoError(t, os.WriteFile(path, data, 0o644))

			loaded, err := Load(path, nil)
			require.NoError(t, err)
			assert.Equal(t, VersionGroupConfig{
				Versioning: VersioningFixedMajorMinor,
				Counter:    SharingIndependent,
				Channels:   SharingIndependent,
			}, loaded.VersionGroups["platform"])
		})
	}
}

func TestVersionGroupScalarFormStillMeansTheDefaults(t *testing.T) {
	loaded, err := loadModel(t, groupConfig(VersionGroupConfig{Versioning: "FixedMajorMinor"}),
		"packages/libs/core", "packages/apps/app")
	require.NoError(t, err)
	group := loaded.VersionGroups["platform"]
	assert.Equal(t, VersioningFixedMajorMinor, group.Versioning, "modes are normalized")
	assert.Empty(t, group.Counter, "an axis nobody wrote stays unwritten")
	assert.Empty(t, group.Channels)
}

func TestVersionGroupAxesAreNormalized(t *testing.T) {
	loaded, err := loadModel(t, groupConfig(VersionGroupConfig{
		Versioning: VersioningFixedMajor,
		Counter:    "Independent",
		Channels:   "INDEPENDENT",
	}), "packages/libs/core", "packages/apps/app")
	require.NoError(t, err)
	assert.Equal(t, SharingIndependent, loaded.VersionGroups["platform"].Counter)
	assert.Equal(t, SharingIndependent, loaded.VersionGroups["platform"].Channels)
}

func TestVersionGroupRefusesAnImpossibleRule(t *testing.T) {
	for _, c := range []struct {
		name string
		rule VersionGroupConfig
		want string
	}{
		{"unknown counter", VersionGroupConfig{Versioning: VersioningFixed, Counter: "sometimes"},
			`versioning.counter "sometimes" is invalid`},
		{"unknown channels", VersionGroupConfig{Versioning: VersioningFixed, Channels: "maybe"},
			`versioning.channels "maybe" is invalid`},
		{"semver missing", VersionGroupConfig{Counter: SharingIndependent},
			"semver is required beside counter and channels"},
		{"one counter, two channels",
			VersionGroupConfig{Versioning: VersioningFixed, Channels: SharingIndependent},
			"one shared counter cannot span two channels"},
		{"axes on a mode that shares nothing",
			VersionGroupConfig{Versioning: VersioningIndependent, Counter: SharingIndependent,
				Channels: SharingIndependent},
			"a group exists to share versions"},
		{"axes on none",
			VersionGroupConfig{Versioning: VersioningNone, Counter: SharingIndependent},
			"a group exists to share versions"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadModel(t, groupConfig(c.rule), "packages/libs/core", "packages/apps/app")
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestVersioningObjectBelongsToAGroupAlone(t *testing.T) {
	// The four levels that state a mode state no sharing rule, because the
	// rule belongs to what the members share. Each of them names the fix
	// rather than reporting a bare type mismatch.
	object := map[string]any{"semver": "fixedMajor", "counter": "independent"}
	for _, c := range []struct {
		name string
		cfg  map[string]any
	}{
		{"root", map[string]any{
			"scripts":    map[string]any{"b": "echo b"},
			"spaces":     map[string]any{"libs": map[string]any{"path": "pkgs"}},
			"versioning": object,
		}},
		{"space", map[string]any{
			"scripts": map[string]any{"b": "echo b"},
			"spaces": map[string]any{
				"libs": map[string]any{"path": "pkgs", "versioning": object},
			},
		}},
		{"package", map[string]any{
			"scripts":  map[string]any{"b": "echo b"},
			"spaces":   map[string]any{"libs": map[string]any{"path": "pkgs"}},
			"packages": map[string]any{"core": map[string]any{"versioning": object}},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := writeRawRepo(t, c.cfg, "pkgs/core")
			_, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "declared on a versionGroups entry")
		})
	}
}

func TestVersionGroupUnknownAxisIsRefused(t *testing.T) {
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"b": "echo b"},
		"spaces":  map[string]any{"libs": map[string]any{"path": "pkgs", "versionGroup": "platform"}},
		"versionGroups": map[string]any{
			"platform": map[string]any{
				"versioning": map[string]any{"semver": "fixed", "counters": "independent"},
			},
		},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown key "counters"`)
}

func TestVersionGroupAxesReachEveryMember(t *testing.T) {
	// A space member and a standalone package member of one group both carry
	// the whole rule, which is what the planner reads off each package.
	cfg := groupConfig(VersionGroupConfig{
		Versioning: VersioningFixedMajorMinor,
		Counter:    SharingIndependent,
		Channels:   SharingIndependent,
	})
	cfg.Packages = map[string]PackageConfig{"shell": {Path: "tools/shell", VersionGroup: "platform"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/shell")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	for _, name := range []string{"core", "shell"} {
		space := byName[name].Space
		require.NotNil(t, space, "package %s", name)
		assert.Equal(t, model.VersioningFixedMajorMinor, space.Versioning, name)
		assert.Equal(t, "platform", space.VersionGroup, name)
		assert.Equal(t, model.SharingIndependent, space.CounterSharing, name)
		assert.Equal(t, model.SharingIndependent, space.ChannelSharing, name)
	}
	assert.Equal(t, model.Sharing(""), byName["app"].Space.CounterSharing,
		"a package outside the group shares nothing and says so with the default")
}

func TestImplicitSpaceGroupCarriesTheDefaultAxes(t *testing.T) {
	// A space that versions as a group of its own has no declaration to hold
	// axes, so its members carry the defaults; F2 would give it the object.
	cfg := validConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Versioning = VersioningFixedMajor })
	cfg.Packages = map[string]PackageConfig{"app": {VersionGroup: "libs"}}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	assert.Equal(t, "libs", byName["app"].Space.VersionGroup)
	assert.True(t, byName["app"].Space.CounterSharing.IsShared())
	assert.True(t, byName["app"].Space.ChannelSharing.IsShared())
}
