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

// TestComputeRefusesUnusableDiscoveryAndSelectionBeforeWriting exercises two
// inputs an operator can repair without losing the pending manifest edge.
// Neither a broken ignore rule nor an unknown package selection may turn a
// partial scan into an edit; once corrected, the same edge is written once.
func TestComputeRefusesUnusableDiscoveryAndSelectionBeforeWriting(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core"}`)
	r.WriteFile("packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/core":"workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")
	before := readRepoFile(t, r, "dispat.json")
	r.WriteFile("packages/.dispatignore", "/\n")

	broken := r.Command("compute", "--write")
	require.NotZero(t, broken.Code, "stdout:\n%s\nstderr:\n%s", broken.Stdout, broken.Stderr)
	assert.Contains(t, broken.Stdout+broken.Stderr, "package discovery failed")
	assert.Contains(t, broken.Stdout+broken.Stderr, "names nothing")
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json"))
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
	require.NoError(t, os.Remove(r.Path("packages", ".dispatignore")))

	unknown := r.Command("compute", "--write", "--package", "ghost")
	require.NotZero(t, unknown.Code, "stdout:\n%s\nstderr:\n%s", unknown.Stdout, unknown.Stderr)
	assert.Contains(t, strings.ToLower(unknown.Stdout+unknown.Stderr), "ghost")
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json"))
	assert.NoFileExists(t, r.Path("dispat.json.backup"))

	retried := r.Command("compute", "--write")
	require.Zero(t, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.Contains(t, retried.Stdout, "+ add     web -> core")
	assert.Contains(t, readRepoFile(t, r, "dispat.json"), `"web": [`) // the missing edge was recorded
	assert.Zero(t, r.Command("compute", "--check").Code)
}

// TestComputeRefusesASymlinkedConfigWithoutSplittingItsTwoNames preserves an
// author's config alias: compute must not replace the symlink with a new file
// while leaving the file it named at the old configuration.
func TestComputeRefusesASymlinkedConfigWithoutSplittingItsTwoNames(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core"}`)
	r.WriteFile("packages/web/package.json", `{"name":"@acme/web","dependencies":{"@acme/core":"workspace:*"}}`)
	config := r.Path("dispat.json")
	target := r.Path("cfg", "source.json")
	require.NoError(t, os.MkdirAll(r.Path("cfg"), 0o755))
	require.NoError(t, os.Rename(config, target))
	require.NoError(t, os.Symlink("cfg/source.json", config))
	r.Commit("feat(core,web): bootstrap")
	before := readRepoFile(t, r, "cfg/source.json")

	res := r.Command("compute", "--write")
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "refusing to rewrite a symbolic link")
	info, err := os.Lstat(config)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the alias was not replaced")
	assert.Equal(t, before, readRepoFile(t, r, "cfg/source.json"))
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
}
