// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A partial-version alias spells a prerelease section that disappears on
// graduation. Neither shape can become the package's immutable baseline.
func TestAliasPrereleaseShapesStayOutsideReleaseHistory(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	cfg.AliasTags = []models.AliasTagConfig{{Format: "{name}@{major}-{channel}.{counter}", Moving: true}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)%rc: initial candidate")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0-rc.0"), "tags: %v", r.TagList())
	assert.Equal(t, r.Git("rev-list", "-n1", "core@0.1.0-rc.0"), r.Git("rev-list", "-n1", "core@0-rc.0"))
	status := r.StatusOK()
	assert.Equal(t, "0.1.0-rc.0", harness.GraphLine(status.Events, "core").Str("version"))

	r.CommitEmpty("fix(core)%rc: repair candidate")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0-rc.1"), "tags: %v", r.TagList())
	status = r.StatusOK()
	assert.Equal(t, "0.1.0-rc.1", harness.GraphLine(status.Events, "core").Str("version"))

	r.CommitEmpty("feat(core)%rc>stable: graduate candidate")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0"), "tags: %v", r.TagList())
	assert.Equal(t, r.Git("rev-list", "-n1", "core@0.1.0"), r.Git("rev-list", "-n1", "core@0"))
	status = r.StatusOK()
	assert.Equal(t, "0.1.0", harness.GraphLine(status.Events, "core").Str("version"))
	assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "core").Str("message"))
}
