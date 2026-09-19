// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: what `dispat compute --write` does with a config it
// cannot rewrite.
//
// The format-preserving writers rewrite JSON and YAML around the one key that
// changed. TOML they cannot, so instead of editing the file to something the
// author did not write, compute renders the exact block to paste and refuses.
// Both shapes it can be asked to write have their own renderer — the root
// dependency object, whose entries are objects, and a package entry's plain
// list of provider names — so both are asked for here, and each must name
// where the block goes.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// tomlWorkspace is a TOML configuration of one space at packages/, with the
// two scripts every fixture uses. extra is appended verbatim.
const tomlWorkspace = `logLevel = "info"
logFormat = "json"
updateCheck = false

[github]
enabled = false

[scripts]
build = "echo building"
publish = "echo publishing"

[spaces.libs]
path = "packages"

[spaces.libs.flow]
build = "build"
publish = "publish"
`

// TestCovComputeRendersAPasteableBlockForTOMLConfigs: a TOML config is never
// rewritten. compute prints the block to paste, names the key it replaces and
// the file it belongs in, and exits non-zero with the file byte-identical.
func TestCovComputeRendersAPasteableBlockForTOMLConfigs(t *testing.T) {
	t.Run("the root dependency object", func(t *testing.T) {
		r := harness.New(t)
		r.WriteFile("dispat.toml", tomlWorkspace)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
		r.Commit("feat(core,web): bootstrap")
		before := readRepoFile(t, r, "dispat.toml")

		res := r.Command("compute", "--write", "--config", "dispat.toml")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "paste over the [dependencies] table in dispat.toml")
		assert.Contains(t, res.Stdout, "[dependencies]")
		assert.Contains(t, res.Stdout, "[[dependencies.web]]")
		assert.Contains(t, res.Stdout, "provider = 'core'")
		assert.Equal(t, before, readRepoFile(t, r, "dispat.toml"), "the config is untouched")
		_, err := os.Stat(r.Path("dispat.toml.backup"))
		assert.True(t, os.IsNotExist(err), "a refused edit writes no backup")
	})

	t.Run("a package entry's provider list", func(t *testing.T) {
		r := harness.New(t)
		r.WriteFile("dispat.toml", tomlWorkspace+"\n[packages.web]\ndependencies = [\"extra\"]\n")
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.SeedPackage("packages", "extra")
		r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
		r.WriteFile("packages/extra/package.json", `{"name": "@acme/extra", "version": "0.0.0"}`)
		r.WriteFile("packages/web/package.json",
			`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*", "@acme/extra": "workspace:*"}}`)
		r.Commit("feat(core,web,extra): bootstrap")
		before := readRepoFile(t, r, "dispat.toml")

		res := r.Command("compute", "--write", "--config", "dispat.toml")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "paste over the dependencies in dispat.toml")
		assert.Contains(t, res.Stdout, "core")
		assert.Equal(t, before, readRepoFile(t, r, "dispat.toml"), "the config is untouched")
	})
}

// TestCovComputeTOMLRefusalStillReportsTheSuggestion: the refusal is about
// writing, not about detecting. The suggestion itself is printed exactly as
// the preview prints it, so an operator can act on it by hand.
func TestCovComputeTOMLRefusalStillReportsTheSuggestion(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("dispat.toml", tomlWorkspace)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.Commit("feat(core,web): bootstrap")

	preview := r.Command("compute", "--config", "dispat.toml")
	require.Equal(t, 0, preview.Code, "stdout:\n%s\nstderr:\n%s", preview.Stdout, preview.Stderr)
	assert.Contains(t, preview.Stdout, "+ add     web -> core (dependencies)")

	refused := r.Command("compute", "--write", "--config", "dispat.toml")
	assert.Equal(t, 1, refused.Code)
	assert.Contains(t, refused.Stdout, "+ add     web -> core (dependencies)",
		"the refusal repeats what it found before saying it cannot write it")
}
