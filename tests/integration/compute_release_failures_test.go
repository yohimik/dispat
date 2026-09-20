// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestComputeTOMLValueFragmentRefusalPreservesItsProvenance proves a $ref may
// own the entire dependency value in a TOML fragment. Since that fragment has
// no enclosing key to paste over, compute names the actual owner and leaves
// both it and the referring root unchanged without manufacturing a backup.
func TestComputeTOMLValueFragmentRefusalPreservesItsProvenance(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"updateCheck": false,
		"github":      map[string]any{"enabled": false},
		"scripts":     map[string]any{"build": "echo building", "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages",
				"flow": map[string]any{"build": []string{"build"}, "publish": []string{"publish"}}},
		},
		"dependencies": map[string]any{"$ref": "./cfg/deps.toml"},
	})
	r.WriteFile("cfg/deps.toml", "web = ['ghost']\n")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name":"@acme/web","version":"0.0.0","dependencies":{"@acme/core":"workspace:*"}}`)
	r.Commit("feat(core,web): dependency declared by the manifests")
	rootBefore := readRepoFile(t, r, "dispat.json")
	fragmentBefore := readRepoFile(t, r, "cfg/deps.toml")

	result := r.Command("compute", "--write")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "deps.toml is TOML and holds this value alone",
		"stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Equal(t, rootBefore, readRepoFile(t, r, "dispat.json"))
	assert.Equal(t, fragmentBefore, readRepoFile(t, r, "cfg/deps.toml"))
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
	assert.NoFileExists(t, r.Path("cfg", "deps.toml.backup"))
	_, err := os.Stat(r.Path("cfg", "deps.toml"))
	require.NoError(t, err)
}

// TestComputeStarRequiresANamedFleetEntry proves star has no implicit hub in a
// monorepository. The command refuses before scanning or writing configuration,
// so selecting this topology cannot turn an ordinary repository into a fleet.
func TestComputeStarRequiresANamedFleetEntry(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig("echo building", 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): ordinary monorepository")
	before := readRepoFile(t, r, "dispat.json")

	result := r.Command("compute", "--topology", "star", "--write")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout+result.Stderr, "star topology requires a linked fleet")
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json"))
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
	assert.NoFileExists(t, r.Path(".gitmodules"))
}

// TestComputeStarKeepsStagedRepairsWhenACloneRevealsAConflictingEdge proves a
// roster-only peer can reveal topology the entry could not inspect before it
// was cloned. Star stops when that peer already links another non-entry peer,
// names the incompatibility, and retains every staged operation for review.
func TestComputeStarKeepsStagedRepairsWhenACloneRevealsAConflictingEdge(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("sdk", "web")
	entry := fleet.peer("api")

	result := entry.CommandEnv(fileProtocolEnv(), "compute", "--topology", "star", "--write")
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	combined := result.Stdout + result.Stderr
	assert.Contains(t, combined, "repaired fleet is incompatible with star topology")
	assert.Contains(t, combined, "review the staged changes before retrying")
	assert.Contains(t, combined, "sdk")
	assert.Contains(t, combined, "web")
	assert.True(t, strings.Contains(result.Stdout, "linked sdk from api") ||
		strings.Contains(result.Stdout, "linked web from api"),
		"at least one accepted repair was staged before the hidden edge became observable")
	assert.Contains(t, entry.Git("diff", "--cached", "--name-only"), ".gitmodules")
	assert.DirExists(t, entry.Path(".links", "sdk", "packages"))
	assert.Equal(t, ".links/web",
		entry.Git("-C", ".links/sdk", "config", "--file", ".gitmodules", "submodule.web.path"),
		"the peer's pre-existing edge is preserved rather than rewired")
}
