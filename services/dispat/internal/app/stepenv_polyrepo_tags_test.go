// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestComposedStepDoesNotCreateAWorkspaceWideTagMask keeps the app boundary
// honest before planning: the invoking package's raw tag must not enter the
// legacy global mask when repository ownership is available. The plan-level
// regression proves the corresponding owner-qualified mask semantics.
func TestComposedStepDoesNotCreateAWorkspaceWideTagMask(t *testing.T) {
	clearRunEnv(t)
	wiredEnv(t, "a", "v1.1.0", "1.1.0")
	workspace := &config.Workspace{Repositories: []config.Repository{
		{Name: config.ControlRepository, Control: true, Config: &config.File{}},
		{Name: "source-a", Config: &config.File{}},
		{Name: "source-b", Config: &config.File{}},
	}}
	a := NewWorkspace(t.TempDir(), &config.File{}, workspace, zerolog.Nop())
	a.pkgs = []*model.Package{{Name: "a", Repository: "source-a"}}
	a.pkgsOnce.Do(func() {})
	var window WindowOptions

	env, err := a.wireStep(&window)
	require.NoError(t, err)
	require.NotNil(t, env)
	assert.Empty(t, a.ignoreTags, "a composed step must not install a cross-repository mask")
	assert.Equal(t, map[string][]string{"source-a": {"v1.1.0"}}, a.ignoreTagsByRepository)
}

func TestComposedStepMasksEachResolvedOwnerAndSkipsUnknowns(t *testing.T) {
	clearRunEnv(t)
	workspace := &config.Workspace{Repositories: []config.Repository{
		{Name: config.ControlRepository, Control: true, Config: &config.File{}},
		{Name: "source-a", Config: &config.File{}},
		{Name: "Source-B", Config: &config.File{}},
	}}
	newApp := func(pkgs ...*model.Package) *App {
		a := NewWorkspace(t.TempDir(), &config.File{}, workspace, zerolog.Nop())
		a.pkgs = pkgs
		a.pkgsOnce.Do(func() {})
		return a
	}

	t.Run("case folded invoking package and independent releasing owner", func(t *testing.T) {
		a := newApp(
			&model.Package{Name: "Alpha", Repository: "source-a", Space: &model.Space{Name: "a", TagFormat: "v{version}"}},
			&model.Package{Name: "Beta", Repository: "Source-B", Space: &model.Space{Name: "b", TagFormat: "{name}@{version}"}},
		)
		a.maskRunTags(&runEnv{pkg: "alpha", tag: "v1.1.0", releasing: []releasingPackage{
			{name: "BETA", version: mustVersion(t, "2.0.0")},
		}})
		assert.Equal(t, map[string][]string{
			"source-a": {"v1.1.0"},
			"Source-B": {"Beta@2.0.0"},
		}, a.ignoreTagsByRepository)
		assert.Empty(t, a.ignoreTags)
	})

	t.Run("missing package", func(t *testing.T) {
		a := newApp(&model.Package{Name: "Alpha", Repository: "source-a"})
		a.maskRunTags(&runEnv{pkg: "missing", tag: "v1.1.0"})
		assert.Empty(t, a.ignoreTagsByRepository)
		assert.Empty(t, a.ignoreTags)
	})

	t.Run("unknown repository owner", func(t *testing.T) {
		a := newApp(&model.Package{Name: "Alpha", Repository: "missing-source"})
		a.maskRunTags(&runEnv{pkg: "alpha", tag: "v1.1.0"})
		assert.Empty(t, a.ignoreTagsByRepository)
		assert.Empty(t, a.ignoreTags)
	})
}
