// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A manifest may name a script-only provider, but a releasable consumer
// cannot follow it: no provider release exists to satisfy that edge.
// Script-only consumers may still declare the relation for their own order.
func TestComputeSkipsVersioningNoneProviderForReleasableConsumer(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(noneConfig(echoBuild))
	r.SeedPackage("packages", "web")
	r.SeedPackage("tools", "smoke")
	r.SeedPackage("tools", "probe")
	r.WriteFile("tools/smoke/package.json", `{"name":"@acme/smoke"}`)
	r.WriteFile("packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/smoke":"workspace:*"}}`)
	r.WriteFile("tools/probe/package.json", `{"name":"@acme/probe","dependencies":{"@acme/smoke":"workspace:*"}}`)
	r.Commit("feat(web,smoke,probe): bootstrap mixed versioning")

	preview := r.Command("compute")
	require.Zero(t, preview.Code, "stdout:\n%s\nstderr:\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "probe -> smoke")
	assert.NotContains(t, preview.Stdout, "web -> smoke")

	write := r.Command("compute", "--write")
	require.Zero(t, write.Code, "stdout:\n%s\nstderr:\n%s", write.Stdout, write.Stderr)
	assert.Contains(t, write.Stdout, "probe -> smoke")
	assert.NotContains(t, write.Stdout, "web -> smoke")
	r.StatusOK()
	assert.Zero(t, r.Command("compute", "--check").Code, "the allowed edge converges")
}

// A typo in an edge's kind makes ordinary planning unsafe, but compute is the
// command that can repair it from a manifest instead of requiring a hand edit.
func TestComputeRepairsInvalidDeclaredDependencyKind(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core", Kind: "devDepndencies"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core"}`)
	r.WriteFile("packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/core":"workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap with a misspelled edge kind")

	bad := r.Status()
	require.NotZero(t, bad.Code)
	assert.Contains(t, bad.Stdout+bad.Stderr, "devDepndencies")
	preview := r.Command("compute")
	require.Zero(t, preview.Code, "stdout:\n%s\nstderr:\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "invalid kind")
	assert.Contains(t, preview.Stdout, "web -> core")
	write := r.Command("compute", "--write")
	require.Zero(t, write.Code, "stdout:\n%s\nstderr:\n%s", write.Stdout, write.Stderr)
	r.StatusOK()
	assert.Zero(t, r.Command("compute", "--check").Code)
}
