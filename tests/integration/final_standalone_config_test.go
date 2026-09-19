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

// TestFinalStandaloneFolderPolicyControlsItsRelease follows the standalone
// package's local overrides through a release and its converged retry.
func TestFinalStandaloneFolderPolicyControlsItsRelease(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(2)
	cfg.Scripts = map[string]models.Script{"build": {"exit 91"}, "publish": {"echo publishing"}}
	cfg.Flow = buildPublish()
	cfg.Packages = map[string]models.PackageConfig{"core": {Path: "tools/core"}}
	r.WriteConfigModel(cfg)
	r.WriteFile("tools/core/package.json", `{"name":"core","version":"0.0.0"}`)
	writeJSON(t, r, "tools/core/dispat.json", models.PackageConfig{
		Concurrency: []int{1, 2},
		Scripts:     map[string]models.Script{"Build": {"printf 'built\\n' >> built.txt"}},
		AutoVersion: &models.AutoVersionConfig{Enabled: models.Bool(true), Only: []string{"core"}},
		Changelog:   &models.ChangelogConfig{File: "HISTORY.md"},
	})
	r.Commit("feat(core): release the standalone package")
	r.ReleaseOK()
	manifest, err := os.ReadFile(r.Path("tools/core/package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), `"version":"0.1.0"`)
	assert.FileExists(t, r.Path("tools/core/HISTORY.md"))
	assert.NoFileExists(t, r.Path("tools/core/CHANGELOG.md"))
	assert.True(t, r.IsTagged("core@0.1.0"))
	r.ReleaseOK()
	built, err := os.ReadFile(r.Path("tools/core/built.txt"))
	require.NoError(t, err)
	assert.Equal(t, "built\n", string(built), "the local Build override runs once and the retry converges")
}

// TestFinalStandaloneFolderRefusalsPreventAnyRelease applies invalid local
// policy to a valid standalone root declaration. Every failure names the
// package before its build or release record can be written.
func TestFinalStandaloneFolderRefusalsPreventAnyRelease(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"syntax", `{`, "dispat.json"},
		{"wrong field type", `{"concurrency":["many"]}`, "invalid format"},
		{"nested spaces", `{"spaces":{"nested":{}}}`, "declares spaces"},
		{"invalid mode", `{"versioning":"calver"}`, "unknown versioning"},
		{"invalid package budget", `{"concurrency":[-1]}`, "concurrency"},
		{"missing script", `{"flow":{"build":["missing"]}}`, "unknown script"},
		{"invalid ignore", `{"ignore":["!"]}`, "re-includes nothing"},
		{"invalid version rule", `{"autoVersion":{"enabled":true,"only":["missing"]}}`, "only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			cfg := harness.BaseFile(1)
			cfg.Scripts = map[string]models.Script{"build": {"touch built.txt"}, "publish": {"echo publishing"}}
			cfg.Flow = buildPublish()
			cfg.Packages = map[string]models.PackageConfig{"core": {Path: "tools/core"}}
			r.WriteConfigModel(cfg)
			r.WriteFile("tools/core/dispat.json", tc.body)
			r.Commit("feat(core): bootstrap")
			refuseStatus(t, r, tc.want)
			res := r.Release()
			require.NotZero(t, res.Code)
			assert.NoFileExists(t, r.Path("tools/core/built.txt"))
			assert.Empty(t, r.TagList())
		})
	}
}
