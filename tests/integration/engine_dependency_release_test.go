// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Engine package managers cannot read npm caret ranges. The release planner's
// common range policy must be rendered in each consumer's native syntax.
func TestEngineDependencyReleaseUsesNativeExactRanges(t *testing.T) {
	repo := harness.New(t)
	config := libsConfig(echoBuild, 1)
	config.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{Match: []string{"*"}, Range: "caret", Manifests: "all"},
	}
	config.Dependencies = []models.DependencyConfig{
		{Consumer: "client", Provider: "core"},
		{Consumer: "game", Provider: "core"},
	}
	repo.WriteConfigModel(config)
	repo.WriteFile("packages/core/package.json", `{"name":"com.acme.core","version":"0.0.0"}`)
	repo.WriteFile("packages/client/Packages/manifest.json", `{"dependencies":{"com.acme.core":"0.0.0","com.unity.textmeshpro":"3.0.6"}}`)
	repo.WriteFile("packages/game/gem.json", `{"gem_name":"AcmeGame","version":"0.0.0","dependencies":["com.acme.core==0.0.0","Atom_RHI==1.0.0"]}`)
	repo.Commit("feat(core,client,game): engine consumers")
	repo.ReleaseOK()
	assert.Contains(t, readRepoFile(t, repo, "packages/client/Packages/manifest.json"), `"com.acme.core":"0.1.0"`)
	assert.Contains(t, readRepoFile(t, repo, "packages/client/Packages/manifest.json"), `"com.unity.textmeshpro":"3.0.6"`)
	assert.Contains(t, readRepoFile(t, repo, "packages/game/gem.json"), `"com.acme.core==0.1.0"`)
	assert.Contains(t, readRepoFile(t, repo, "packages/game/gem.json"), `"Atom_RHI==1.0.0"`)
	assert.Contains(t, repo.TagList(), "core@0.1.0")
	assert.Contains(t, repo.TagList(), "client@0.1.0")
	assert.Contains(t, repo.TagList(), "game@0.1.0")
}
