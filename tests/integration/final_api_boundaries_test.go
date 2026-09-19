// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalManifestCommandsRejectIncompleteEditSpecificationsBeforeWriting
// proves the process boundary for each required half of --set, --link and
// --replace. A valid edit before the malformed one must not leak through: the
// whole command line is parsed before any target is opened.
func TestFinalManifestCommandsRejectIncompleteEditSpecificationsBeforeWriting(t *testing.T) {
	r := harness.New(t)
	const original = `{"name":"acme","dependencies":{"core":"^1.0.0"}}`
	r.WriteFile("package.json", original)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"set without a dependency name", []string{"writer", "package.json", "--set", "=^2.0.0"}, "no dependency name"},
		{"set without a range", []string{"writer", "package.json", "--set", "core="}, "no version range"},
		{"link without a separator", []string{"writer", "package.json", "--set", "core=^2.0.0", "--link", "core"}, "want name=path"},
		{"link without a dependency name", []string{"writer", "package.json", "--link", "=../core"}, "no dependency name"},
		{"replacement without search text", []string{"replacer", "package.json", "--replace", "=>new"}, "no text to find"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Command(tc.args...)
			assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			body, err := os.ReadFile(r.Path("package.json"))
			require.NoError(t, err)
			assert.Equal(t, original, string(body), "a usage refusal must not apply an earlier valid edit")
		})
	}
}

// TestFinalComputeDerivesDependenciesWithoutInventingGitBaselines covers an
// adopting source tree before its first git init. Manifest dependencies are
// still objective evidence and can be written; release baselines need tags and
// are explicitly omitted rather than guessed from manifest versions.
func TestFinalComputeDerivesDependenciesWithoutInventingGitBaselines(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"1.4.2"}`)
	r.WriteFile("packages/web/package.json",
		`{"name":"@acme/web","version":"2.1.0","dependencies":{"@acme/core":"workspace:*"}}`)
	require.NoError(t, os.RemoveAll(r.Path(".git")))

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     web -> core (dependencies)")
	assert.Contains(t, res.Stdout+res.Stderr, "skipping version baselines")
	assert.NotContains(t, res.Stdout, "+ initial", "manifest versions are not release history")

	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	config, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Contains(t, string(config), `"web": [`)
	assert.NotContains(t, string(config), `"initials"`)
}

// TestFinalAutoWriterLeavesANestedPackageManifestToItsOwner exercises the
// ownership boundary under --manifests all. Selecting only the outer package
// must not let its recursive scan stamp a separately configured inner package.
func TestFinalAutoWriterLeavesANestedPackageManifestToItsOwner(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 2)
	cfg.Packages = map[string]models.PackageConfig{
		"inner": {Path: "packages/outer/inner"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "outer")
	r.WriteFile("packages/outer/package.json", `{"name":"outer","version":"1.0.0"}`)
	const inner = `{"name":"inner","version":"4.5.6"}`
	r.WriteFile("packages/outer/inner/package.json", inner)
	r.Commit("feat(outer,inner): bootstrap")

	res := r.Command("autowriter", "--since", "all", "--package", "outer",
		"--manifests", "all", "--set-version", "9.9.9")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	outer, err := os.ReadFile(r.Path("packages", "outer", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(outer), `"version":"9.9.9"`)
	gotInner, err := os.ReadFile(r.Path("packages", "outer", "inner", "package.json"))
	require.NoError(t, err)
	assert.Equal(t, inner, string(gotInner), "the outer package must not write the inner package's manifest")
}

// TestFinalInstallExplainsAnUnprefixedListingWithNoVersions distinguishes an
// empty tag prefix from the default "v" prefix in the refusal. With --tag-prefix
// "", the complete tag must itself be a version and the remedy must say so.
func TestFinalInstallExplainsAnUnprefixedListingWithNoVersions(t *testing.T) {
	r := newToolRepo(t)
	res := r.install("--check", "--tag-prefix", "")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "no matching release")
	assert.Contains(t, out, "with no tag prefix")
	assert.True(t, strings.Contains(out, "--tag-prefix") || strings.Contains(out, "--prerelease"),
		"the refusal should name how to broaden the listing: %s", out)
}
