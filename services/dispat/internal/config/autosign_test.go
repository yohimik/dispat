package config

// The sign stage's configuration: autoSign and the three sign flow entries.
// autoSign replaces whole through the ladder like autoVersion, owns the
// package's own version (so the autoVersion block beside it resolves
// writeVersion to false and refuses an explicit true), and the flow entries
// merge entry by entry like every other.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// resolveLibs loads cfg with the propagate fixtures' folders and answers the
// resolved space of every package, by name.
func resolveLibs(t *testing.T, cfg File) map[string]*model.Space {
	t.Helper()
	pkgs, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.NoError(t, err)
	spaces := make(map[string]*model.Space, len(pkgs))
	for _, p := range pkgs {
		spaces[p.Name] = p.Space
	}
	return spaces
}

// TestAutoSignResolves: the block is on by its presence, its scope defaults
// to the root manifests, and a package switches an inherited block off with
// `enabled: false` or restates it whole.
func TestAutoSignResolves(t *testing.T) {
	cfg := propagateConfig()
	cfg.AutoSign = &AutoSignConfig{Enabled: models.Bool(true)}
	withLibs(&cfg, func(s *SpaceConfig) { s.AutoSign = &AutoSignConfig{Manifests: "all"} })
	cfg.Packages = map[string]PackageConfig{"utils": {AutoSign: &AutoSignConfig{Enabled: models.Bool(false)}}}
	spaces := resolveLibs(t, cfg)

	assert.Equal(t, &model.AutoSign{Manifests: model.ScopeAll}, spaces["core"].AutoSign, "the space's block, whole")
	assert.Nil(t, spaces["utils"].AutoSign, "enabled: false switches the inherited block off")
	assert.Equal(t, &model.AutoSign{Manifests: model.ScopeRoot}, spaces["app"].AutoSign,
		"the root's block reaches a space that says nothing, and its scope defaults to root")

	none := resolveLibs(t, propagateConfig())
	assert.Nil(t, none["core"].AutoSign, "absent means off")
}

// TestAutoSignManifestsValues: root and all are the scopes; none would sign
// nothing and is refused in favour of `enabled: false`, and anything else is a
// typo.
func TestAutoSignManifestsValues(t *testing.T) {
	for value, want := range map[string]string{
		"none":     `space "libs": autoSign: manifests: "none" would write nothing; use "enabled": false`,
		"sideways": `space "libs": autoSign: manifests: unknown value "sideways" (want "root" or "all")`,
	} {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) { s.AutoSign = &AutoSignConfig{Manifests: value} })
		_, err := loadModel(t, cfg, propagateDirs...)
		require.Error(t, err, value)
		assert.Contains(t, err.Error(), want)
	}
}

// TestAutoSignOwnsTheOwnVersion: beside an enabled autoSign the autoVersion
// block writes ranges alone. An explicit `writeVersion: true` there asks for a
// second writer of the same field and is refused under the key the block was
// written with; without autoSign the block writes both, as it always has.
func TestAutoSignOwnsTheOwnVersion(t *testing.T) {
	t.Run("the resolved block writes no own version", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.AutoVersion = &AutoVersionConfig{Range: "exact"}
			s.AutoSign = &AutoSignConfig{Enabled: models.Bool(true)}
		})
		cfg.Packages = map[string]PackageConfig{"utils": {AutoSign: &AutoSignConfig{Enabled: models.Bool(false)}}}
		spaces := resolveLibs(t, cfg)
		assert.False(t, spaces["core"].AutoVersion.WriteVersion, "autoSign owns the own version")
		assert.True(t, spaces["utils"].AutoVersion.WriteVersion, "without autoSign the block writes it")
	})
	t.Run("writeVersion false is compatible", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.AutoPropagate = &AutoVersionConfig{WriteVersion: models.Bool(false)}
			s.AutoSign = &AutoSignConfig{Enabled: models.Bool(true)}
		})
		assert.False(t, resolveLibs(t, cfg)["core"].AutoVersion.WriteVersion)
	})
	t.Run("writeVersion true is refused", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.AutoPropagate = &AutoVersionConfig{WriteVersion: models.Bool(true)}
			s.AutoSign = &AutoSignConfig{Enabled: models.Bool(true)}
		})
		_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`space "libs": autoPropagate.writeVersion and autoSign both write the package's own version`)
	})
	t.Run("a package enabling autoSign under an inherited writeVersion true", func(t *testing.T) {
		cfg := propagateConfig()
		cfg.AutoVersion = &AutoVersionConfig{WriteVersion: models.Bool(true)}
		cfg.Packages = map[string]PackageConfig{"core": {AutoSign: &AutoSignConfig{Enabled: models.Bool(true)}}}
		_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`space "libs": package "core": autoVersion.writeVersion and autoSign both write the package's own version`)
	})
	t.Run("a disabled autoVersion block does not conflict", func(t *testing.T) {
		cfg := propagateConfig()
		withLibs(&cfg, func(s *SpaceConfig) {
			s.AutoVersion = &AutoVersionConfig{Enabled: models.Bool(false), WriteVersion: models.Bool(true)}
			s.AutoSign = &AutoSignConfig{Enabled: models.Bool(true)}
		})
		spaces := resolveLibs(t, cfg)
		assert.Nil(t, spaces["core"].AutoVersion)
		assert.NotNil(t, spaces["core"].AutoSign)
	})
}

// TestSignFlowEntries: the sign stage's entries resolve per package like every
// other flow entry, merge entry by entry, and a reference to nothing is refused
// under its own key.
func TestSignFlowEntries(t *testing.T) {
	cfg := propagateConfig()
	cfg.Scripts["sign"] = Script{"echo sign"}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Flow.Sign = []string{"sign"}
		s.Flow.BeforeSign = []string{"pre"}
		s.Flow.PostSign = []string{"post"}
	})
	cfg.Packages = map[string]PackageConfig{"core": {Flow: &SpaceFlowConfig{Sign: []string{"post"}}}}
	spaces := resolveLibs(t, cfg)
	assert.Equal(t, []string{"echo sign"}, spaces["utils"].SignScript)
	assert.Equal(t, []string{"echo pre"}, spaces["utils"].BeforeSignScript)
	assert.Equal(t, []string{"echo post"}, spaces["utils"].PostSignScript)
	assert.Equal(t, []string{"echo post"}, spaces["core"].SignScript, "a package's entry replaces the inherited one")
	assert.Equal(t, []string{"echo pre"}, spaces["core"].BeforeSignScript, "the entries it does not name are inherited")
	assert.Empty(t, spaces["app"].SignScript, "another space has no sign stage")

	cfg = propagateConfig()
	withLibs(&cfg, func(s *SpaceConfig) { s.Flow.BeforeSign = []string{"nope"} })
	_, err := discoverPackages(t, writeModelRepo(t, cfg, propagateDirs...))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `flow.beforeSign references unknown script "nope"`)
}
