// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalSharedMovingAliasRefusesBothOwners prevents two independently
// released packages from moving the same consumer-facing tag between them.
func TestFinalSharedMovingAliasRefusesBothOwners(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("touch built.txt", 1)
	cfg.AliasTags = []models.AliasTagConfig{{Format: "v{major}", Moving: true}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "sdk")
	r.Commit("feat(core,sdk): bootstrap independent packages")
	res := r.Release()
	require.NotZero(t, res.Code)
	assert.Contains(t, diagnosticText(res), `packages "core" and "sdk" both write the alias tag "v1"`)
	assert.Empty(t, r.TagList())
	assert.NoFileExists(t, r.Path("packages/core/built.txt"))
	assert.NoFileExists(t, r.Path("packages/sdk/built.txt"))

	// Giving each owner its own namespace resolves the ambiguity without
	// changing package identity or the release intent.
	cfg.AliasTags[0].Format = "{name}-v{major}"
	r.WriteConfigModel(cfg)
	r.Commit("chore: separate moving alias owners")
	r.ReleaseOK()
	for _, name := range []string{"core", "sdk"} {
		assert.True(t, r.IsTagged(name+"@0.1.0"))
		assert.Equal(t, r.Git("rev-list", "-n1", name+"@0.1.0"), r.Git("rev-list", "-n1", name+"-v0"))
	}
	assert.False(t, r.IsTagged("v0"))
}

// TestFinalFolderRecordPresentationKeepsRootCommitPolicy allows package-local
// headings and commit links without introducing a second commit parser.
func TestFinalFolderRecordPresentationKeepsRootCommitPolicy(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: models.EntryFormatConfig{
		Sections:   []models.SectionConfig{{Title: "Root changes", Types: []string{"perf"}, Bump: "minor"}},
		CommitRefs: &models.CommitRefsConfig{Placement: "suffix", Format: "[$DISPAT_COMMIT_SHORT]", Link: "auto"},
	}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	writeJSON(t, r, "packages/core/dispat.json", models.PackageConfig{
		Changelog: &models.ChangelogConfig{EntryFormatConfig: models.EntryFormatConfig{
			Sections:   []models.SectionConfig{{Title: "Faster builds", Types: []string{"perf"}}},
			CommitRefs: &models.CommitRefsConfig{Link: "https://forge.test/commit/${DISPAT_COMMIT}"},
		}},
	})
	r.Commit("perf(core): reuse the compiler cache")
	sha := r.Git("rev-parse", "HEAD")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "the repository's minor policy governs the local heading")
	data, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "### Faster builds")
	assert.NotContains(t, string(data), "### Root changes")
	assert.Contains(t, string(data), "reuse the compiler cache")
	assert.Contains(t, string(data), "https://forge.test/commit/"+sha)
}
