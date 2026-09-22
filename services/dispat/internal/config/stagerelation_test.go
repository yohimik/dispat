package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// `isBuildWaitingPublish` folds through the ordinary ladder, and the object
// form has to fold exactly as the boolean always did: a level that states one
// replaces the whole relation rather than overlaying a field on the level
// above, because the two fields carry meaning against each other and a
// half-inherited relation is one nobody wrote.

func TestStageRelationLadderReplacesWhole(t *testing.T) {
	cfg := validConfig()
	// The root states the relaxed deploy-order relation, so every level below
	// inherits both of its fields until one of them says otherwise.
	cfg.IsBuildWaitingPublish = &models.StageRelation{
		Build: models.StageWaitNone, IsBlocking: models.Bool(false)}
	withLibs(&cfg, func(s *SpaceConfig) { s.IsBuildWaitingPublish = nil })
	cfg.Packages = map[string]PackageConfig{"tool": {Path: "tools/tool"}}

	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/apps/app", "tools/tool")
	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	for _, name := range []string{"core", "app", "tool"} {
		resolved := byName[name].Space.ProviderRelation
		assert.Equal(t, models.StageWaitNone, resolved.Build, name)
		assert.False(t, resolved.IsBlocking, name)
		assert.False(t, resolved.IsBuildWaitingBuild(), name)
	}
}

func TestStageRelationNearestLevelWins(t *testing.T) {
	cfg := validConfig()
	cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
	withLibs(&cfg, func(s *SpaceConfig) {
		s.IsBuildWaitingPublish = &models.StageRelation{Build: models.StageWaitNone}
		s.Packages = map[string]PackageConfig{
			"utils": {IsBuildWaitingPublish: models.StageRelationOf(false)},
		}
	})
	root := writeModelRepo(t, cfg,
		"packages/libs/core", "packages/libs/utils", "packages/libs/tool", "packages/apps/app")
	// The space folder's own file sits between the space entry and the package
	// entry, and names only the package it is nearer than.
	writePackageFile(t, root, "packages/libs/tool", PackageConfig{
		IsBuildWaitingPublish: &models.StageRelation{
			Build: models.StageWaitBuild, IsBlocking: models.Bool(true)}})

	pkgs, err := discoverPackages(t, root)
	require.NoError(t, err)
	byName := packagesByName(pkgs)

	for name, want := range map[string]model.StageRelation{
		// The root's `true` reaches the space that states nothing.
		"app": {Build: models.StageWaitPublish, IsBlocking: true},
		// The space entry replaces it whole, blocking included: `none`
		// does not block unless the entry says so.
		"core": {Build: models.StageWaitNone, IsBlocking: false},
		// A package entry replaces the space's.
		"utils": {Build: models.StageWaitBuild, IsBlocking: false},
		// And the package's own folder file replaces that again.
		"tool": {Build: models.StageWaitBuild, IsBlocking: true},
	} {
		assert.Equal(t, want, byName[name].Space.ProviderRelation, name)
	}
}

func TestStageRelationRefusalsNameTheKey(t *testing.T) {
	for name, c := range map[string]struct {
		written any
		want    string
	}{
		"an object with no build":     {map[string]any{"isBlocking": true}, "build is required"},
		"an unknown value":            {map[string]any{"build": "published"}, "is not one of none, build or publish"},
		"an unknown key":              {map[string]any{"build": "none", "extra": 1}, `unknown key "extra"`},
		"a publish that cannot block": {map[string]any{"build": "publish", "isBlocking": false}, "cannot state isBlocking: false"},
		"a list":                      {[]any{"none"}, "true or false"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeRawRepo(t, map[string]any{
				"scripts": map[string]any{"build": "echo build"},
				"spaces": map[string]any{
					"libs": map[string]any{"path": "packages/libs", "isBuildWaitingPublish": c.written},
				},
			}, "packages/libs/core")
			_, err := Load(filepath.Join(root, "dispat.json"), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			assert.Contains(t, err.Error(), "isBuildWaitingPublish", "the refusal names the key")
		})
	}
}
