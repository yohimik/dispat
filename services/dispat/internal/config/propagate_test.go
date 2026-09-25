package config

// The propagate stage's names. `propagate` is the version stage under a second
// name, and each key naming it has a twin: autoPropagate for autoVersion, and
// flow.propagate, flow.beforePropagate and flow.postPropagate for the flow
// entries. A pair is one setting: either spelling loads to the same resolved
// package, a layer stating either replaces both inherited values, and one
// object stating both is refused under a label naming that object.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// propagateConfig is validConfig with the version stage's scripts declared, so
// a flow entry under either name has something to resolve to.
func propagateConfig() File {
	cfg := validConfig()
	cfg.Scripts["sync"] = Script{"echo sync"}
	cfg.Scripts["pre"] = Script{"echo pre"}
	cfg.Scripts["post"] = Script{"echo post"}
	cfg.Scripts["lock"] = Script{"npm install"}
	return cfg
}

var propagateDirs = []string{"packages/libs/core", "packages/libs/utils", "packages/apps/app"}

// TestPropagateSynonymsResolveLikeTheirCanonicalKeys: a space written with the
// propagate names resolves to exactly the package the version names give.
func TestPropagateSynonymsResolveLikeTheirCanonicalKeys(t *testing.T) {
	resolve := func(t *testing.T, mutate func(*SpaceConfig)) *model.Space {
		t.Helper()
		cfg := propagateConfig()
		withLibs(&cfg, mutate)
		pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.NoError(t, err)
		return packagesByName(pkgs)["core"].Space
	}
	policy := &AutoVersionConfig{Range: "exact", WriteVersion: models.Bool(false), SyncLock: []string{"lock"}}
	canonical := resolve(t, func(s *SpaceConfig) {
		s.AutoVersion = policy
		s.Flow.Version = []string{"sync"}
		s.Flow.BeforeVersion = []string{"pre"}
		s.Flow.PostVersion = []string{"post"}
	})
	synonym := resolve(t, func(s *SpaceConfig) {
		s.AutoPropagate = policy
		s.Flow.Propagate = []string{"sync"}
		s.Flow.BeforePropagate = []string{"pre"}
		s.Flow.PostPropagate = []string{"post"}
	})

	require.NotNil(t, synonym.AutoVersion, "autoPropagate enables the native reconciliation")
	assert.Equal(t, canonical.AutoVersion, synonym.AutoVersion)
	assert.Equal(t, []string{"npm install"}, synonym.AutoVersion.SyncLock)
	assert.False(t, synonym.AutoVersion.WriteVersion)
	assert.Equal(t, []string{"echo sync"}, synonym.VersionScript)
	assert.Equal(t, []string{"echo pre"}, synonym.BeforeVersionScript)
	assert.Equal(t, []string{"echo post"}, synonym.PostVersionScript)
	assert.Equal(t, canonical.VersionScript, synonym.VersionScript)
	assert.Equal(t, canonical.BeforeVersionScript, synonym.BeforeVersionScript)
	assert.Equal(t, canonical.PostVersionScript, synonym.PostVersionScript)
}

// TestPropagateSynonymsAreOneAxisAcrossLayers: a nearer layer stating either
// spelling replaces what the other spelling said further up, in both
// directions, for the block and for each flow pair.
func TestPropagateSynonymsAreOneAxisAcrossLayers(t *testing.T) {
	t.Run("autoPropagate replaces an inherited autoVersion", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.AutoVersion = &AutoVersionConfig{Range: "tilde"} })
		cfg.Packages = map[string]PackageConfig{"core": {AutoPropagate: &AutoVersionConfig{Range: "exact"}}}
		pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.NoError(t, err)
		byName := packagesByName(pkgs)
		assert.Equal(t, "exact", byName["core"].Space.AutoVersion.Range)
		assert.Equal(t, "tilde", byName["utils"].Space.AutoVersion.Range, "the sibling keeps the space's block")
	})
	t.Run("autoVersion replaces an inherited autoPropagate", func(t *testing.T) {
		cfg := propagateConfig()
		cfg.AutoPropagate = &AutoVersionConfig{Range: "tilde"}
		cfg.Packages = map[string]PackageConfig{"core": {AutoVersion: &AutoVersionConfig{Enabled: models.Bool(false)}}}
		pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.NoError(t, err)
		byName := packagesByName(pkgs)
		assert.Nil(t, byName["core"].Space.AutoVersion, "the package's own block, disabled, is the one that counts")
		require.NotNil(t, byName["app"].Space.AutoVersion, "the root's autoPropagate reaches every space")
		assert.Equal(t, "tilde", byName["app"].Space.AutoVersion.Range)
	})
	t.Run("flow pairs", func(t *testing.T) {
		cfg := propagateConfig()
		cfg.Scripts["other"] = Script{"echo other"}
		withLibs(&cfg, func(s *SpaceConfig) {
			s.Flow.Version = []string{"sync"}
			s.Flow.BeforePropagate = []string{"pre"}
			s.Flow.PostVersion = []string{"post"}
		})
		cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{
			Propagate:     []string{"other"},
			BeforeVersion: []string{"other"},
		}}}
		pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.NoError(t, err)
		core, utils := packagesByName(pkgs)["core"].Space, packagesByName(pkgs)["utils"].Space
		assert.Equal(t, []string{"echo other"}, core.VersionScript, "propagate replaces the inherited version")
		assert.Equal(t, []string{"echo other"}, core.BeforeVersionScript, "beforeVersion replaces the inherited beforePropagate")
		assert.Equal(t, []string{"echo post"}, core.PostVersionScript, "an entry the layer does not name is inherited")
		assert.Equal(t, []string{"echo sync"}, utils.VersionScript)
		assert.Equal(t, []string{"echo pre"}, utils.BeforeVersionScript)
	})
}

// TestPropagateSynonymsBothInOneObjectAreRefused: every object that can hold a
// pair refuses to hold both of its spellings, and the error names the object.
func TestPropagateSynonymsBothInOneObjectAreRefused(t *testing.T) {
	both := func() (*AutoVersionConfig, *AutoVersionConfig) {
		return &AutoVersionConfig{Range: "exact"}, &AutoVersionConfig{Range: "tilde"}
	}
	autoPair := "autoVersion and autoPropagate are mutually exclusive"
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T) string
		want    []string
	}{
		{"root file", func(t *testing.T) string {
			cfg := propagateConfig()
			cfg.AutoVersion, cfg.AutoPropagate = both()
			return writeModelRepo(t, cfg, propagateDirs...)
		}, []string{"config: " + autoPair}},
		{"root flow", func(t *testing.T) string {
			cfg := propagateConfig()
			cfg.Flow = &SpaceFlowConfig{PostVersion: []string{"post"}, PostPropagate: []string{"post"}}
			return writeModelRepo(t, cfg, propagateDirs...)
		}, []string{"config: flow.postVersion and flow.postPropagate are mutually exclusive"}},
		{"space entry", func(t *testing.T) string {
			cfg := propagateConfig()
			withLibs(&cfg, func(s *SpaceConfig) { s.Flow.Version, s.Flow.Propagate = []string{"sync"}, []string{"sync"} })
			return writeModelRepo(t, cfg, propagateDirs...)
		}, []string{`space "libs": flow.version and flow.propagate are mutually exclusive`}},
		{"space file", func(t *testing.T) string {
			root := writeModelRepo(t, propagateConfig(), propagateDirs...)
			av, ap := both()
			writeSpaceFile(t, root, "packages/libs", SpaceFile{AutoVersion: av, AutoPropagate: ap})
			return root
		}, []string{`space "libs"`, filepath.Join("packages", "libs", "dispat.json") + ": " + autoPair}},
		{"root packages entry", func(t *testing.T) string {
			cfg := propagateConfig()
			av, ap := both()
			cfg.Packages = map[string]PackageConfig{"core": {AutoVersion: av, AutoPropagate: ap}}
			return writeModelRepo(t, cfg, propagateDirs...)
		}, []string{`space "libs": package "core": ` + autoPair}},
		{"space packages entry", func(t *testing.T) string {
			cfg := propagateConfig()
			withLibs(&cfg, func(s *SpaceConfig) {
				s.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{
					BeforeVersion: []string{"pre"}, BeforePropagate: []string{"pre"}}}}
			})
			return writeModelRepo(t, cfg, propagateDirs...)
		}, []string{`spaces["libs"]: packages["core"]: flow.beforeVersion and flow.beforePropagate are mutually exclusive`}},
		{"space file packages entry", func(t *testing.T) string {
			root := writeModelRepo(t, propagateConfig(), propagateDirs...)
			av, ap := both()
			writeSpaceFile(t, root, "packages/libs", SpaceFile{
				Packages: map[string]PackageConfig{"core": {AutoVersion: av, AutoPropagate: ap}}})
			return root
		}, []string{`space "libs" (`, filepath.Join("packages", "libs", "dispat.json") + `): packages["core"]: ` + autoPair}},
		{"package folder file", func(t *testing.T) string {
			root := writeModelRepo(t, propagateConfig(), propagateDirs...)
			av, ap := both()
			writePackageFile(t, root, "packages/libs/core", PackageConfig{AutoVersion: av, AutoPropagate: ap})
			return root
		}, []string{`package "core" (`, filepath.Join("packages", "libs", "core", "dispat.json") + "): " + autoPair}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := c.arrange(t)
			cfg, err := Load(filepath.Join(root, "dispat.json"), nil)
			if err == nil {
				_, _, _, err = DiscoverPackages(cfg, root)
			}
			require.Error(t, err)
			for _, want := range c.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestPropagateSynonymErrorsNameTheWrittenKey: a message about the block
// names the key the file wrote, so an author reading it finds the line.
func TestPropagateSynonymErrorsNameTheWrittenKey(t *testing.T) {
	t.Run("a value", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.AutoPropagate = &AutoVersionConfig{Manifests: "some"} })
		_, err := loadModel(t, cfg, propagateDirs...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `space "libs": autoPropagate: manifests: unknown value "some"`)
	})
	t.Run("a syncLock reference", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.AutoPropagate = &AutoVersionConfig{SyncLock: []string{"nope"}} })
		_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `autoPropagate.syncLock references unknown script "nope"`)
	})
	t.Run("an only name", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.AutoPropagate = &AutoVersionConfig{Only: []string{"ghost"}} })
		_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `space "libs": autoPropagate.only: unknown package "ghost"`)

		cfg = propagateConfig()
		cfg.Packages = map[string]PackageConfig{"core": {AutoPropagate: &AutoVersionConfig{Only: []string{"ghost"}}}}
		_, err = discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `package "core": autoPropagate.only: unknown package "ghost"`)
	})
	t.Run("a flow reference", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.Flow.PostPropagate = []string{"nope"} })
		_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `flow.postPropagate references unknown script "nope"`)
	})
}
