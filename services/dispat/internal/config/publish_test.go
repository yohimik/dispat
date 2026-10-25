package config

// `publish.adoptPublished` from the two sides a configuration decides: what
// the ladder resolves it to, and what a level may not write.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func verifyWith(names ...string) *PublishConfig {
	return &PublishConfig{AdoptPublished: &AdoptPublishedConfig{Verify: names}}
}

// TestAdoptPublishedResolvesThroughTheLadder: the nearest level that states
// adoptPublished wins whole, its verify names resolve through the package's
// script scope, a level that states nothing inherits, and a repository that
// never writes the key adopts with the built-in check alone.
func TestAdoptPublishedResolvesThroughTheLadder(t *testing.T) {
	cfg := File{
		Scripts: map[string]Script{"build": {"echo b"}, "verify-npm": {"npm view", "true"}, "verify-image": {"crane digest"}},
		Publish: verifyWith("verify-npm"),
		Spaces: map[string]SpaceConfig{
			"libs": {Path: PathList{"packages/libs"}, Flow: &SpaceFlowConfig{Build: []string{"build"}}},
			"images": {Path: PathList{"packages/images"}, Flow: &SpaceFlowConfig{Build: []string{"build"}},
				Publish: verifyWith("verify-image")},
		},
	}
	root := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/legacy", "packages/images/app")
	writePackageFile(t, root, "packages/libs/legacy", PackageConfig{
		Publish: &PublishConfig{AdoptPublished: public.AdoptPublishedOf(false)},
	})

	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := DiscoverPackages(loaded, root)
	require.NoError(t, err)
	by := packagesByName(pkgs)

	assert.Equal(t, model.AdoptPublished{VerifyScript: []string{"npm view", "true"}}, by["core"].Space.AdoptPublished)
	assert.Equal(t, model.AdoptPublished{IsDisabled: true}, by["legacy"].Space.AdoptPublished)
	assert.Equal(t, model.AdoptPublished{VerifyScript: []string{"crane digest"}}, by["app"].Space.AdoptPublished)

	plain := writeModelRepo(t, minimalConfig(), "pkgs/core")
	loaded, err = Load(filepath.Join(plain, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err = DiscoverPackages(loaded, plain)
	require.NoError(t, err)
	assert.Equal(t, model.AdoptPublished{}, packagesByName(pkgs)["core"].Space.AdoptPublished,
		"unstated, adoption is on with the built-in check")
}

// TestAdoptPublishedShapes: the boolean, its weak spellings, the object, and
// the shapes refused by name.
func TestAdoptPublishedShapes(t *testing.T) {
	for name, row := range map[string]struct {
		stated any
		want   model.AdoptPublished
		err    string
	}{
		"false":          {stated: false, want: model.AdoptPublished{IsDisabled: true}},
		"a string false": {stated: "false", want: model.AdoptPublished{IsDisabled: true}},
		"true":           {stated: true, want: model.AdoptPublished{}},
		"an object":      {stated: map[string]any{"verify": "build"}, want: model.AdoptPublished{VerifyScript: []string{"echo b"}}},
		"an object turned off": {stated: map[string]any{"enabled": false, "verify": []any{"build"}},
			want: model.AdoptPublished{IsDisabled: true, VerifyScript: []string{"echo b"}}},
		"an unknown key":    {stated: map[string]any{"verfy": "build"}, err: "verfy"},
		"an unknown script": {stated: map[string]any{"verify": "nothing"}, err: "nothing"},
		"a list":            {stated: []any{"build"}, err: "adoptPublished"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Scripts = map[string]Script{"build": {"echo b"}}
			root := writeModelRepo(t, cfg, "pkgs/core")
			writePackageRaw(t, root, "pkgs/core", map[string]any{"publish": map[string]any{"adoptPublished": row.stated}})
			loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
			if err == nil {
				var pkgs []*model.Package
				pkgs, _, _, err = DiscoverPackages(loaded, root)
				if err == nil {
					require.Empty(t, row.err)
					assert.Equal(t, row.want, packagesByName(pkgs)["core"].Space.AdoptPublished)
					return
				}
			}
			require.NotEmpty(t, row.err, "%v", err)
			assert.ErrorContains(t, err, row.err)
		})
	}
}
