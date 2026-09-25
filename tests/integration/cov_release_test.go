// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for release execution: what a stage's output stream does
// with a line no terminal was ever going to print, how a declared dependency
// finds its provider when no manifest names it, which files a replace rule may
// not rewrite, and what a malformed export does on either side of the point of
// no return.

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covMegabyteOfX writes a megabyte and a bit of one character to standard
// output with no newline anywhere in it: a progress bar rewriting itself, a
// minified bundle, a base64 blob. The buffer behind a stage's output stream
// would otherwise grow without bound for the whole life of the command.
func covMegabyteOfX(char string) string {
	return "head -c 1200000 /dev/zero | tr '\\0' " + char
}

// TestReleaseTruncatesAnOverlongOutputLine: the head of the line is logged
// with a marker saying it was cut, everything up to the next newline is
// dropped rather than logged as a line of its own, and the stage's later
// output still arrives. A line that never ends at all is dropped on the flush.
func TestReleaseTruncatesAnOverlongOutputLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script is a unix pipeline")
	}
	const marker = "[line truncated: longer than 1 MiB]"

	t.Run("the rest of the line is dropped and the stage carries on", func(t *testing.T) {
		r := singlePackageRepo(t, covMegabyteOfX("x")+
			"; echo ' TAIL-OF-THE-LONG-LINE'; echo 'an ordinary line after it'")
		r.Commit("feat(core): bootstrap")

		res := r.ReleaseOK()
		assert.Contains(t, res.Stdout, marker)
		assert.Contains(t, res.Stdout, "an ordinary line after it",
			"the stream recovers at the next newline")
		assert.NotContains(t, res.Stdout, "TAIL-OF-THE-LONG-LINE",
			"the tail belongs to the line that was already cut")
		assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	})

	t.Run("a line that never ends is dropped on the flush", func(t *testing.T) {
		r := singlePackageRepo(t, covMegabyteOfX("y"))
		r.Commit("feat(core): bootstrap")

		res := r.ReleaseOK()
		assert.Contains(t, res.Stdout, marker)
		assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	})
}

// TestAutoVersionSubstringNameMatchReachesAPackageWithNoManifest: the
// substring fallback exists for the workspaces where a package has no manifest
// to declare a name in — a Gradle module, a folder of shell scripts — while
// its consumers still name it in theirs. The declared name's last segment is
// the package's folder name, and "exact" leaves the same declaration alone.
func TestAutoVersionSubstringNameMatchReachesAPackageWithNoManifest(t *testing.T) {
	setup := func(t *testing.T, nameMatch string) *harness.Repo {
		t.Helper()
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path:        models.PathList{"packages"},
			Flow:        buildPublish(),
			AutoVersion: &models.AutoVersionConfig{NameMatch: nameMatch},
		}
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "app"}}
		r.WriteConfigModel(cfg)
		// app is a folder with no manifest at all, so nothing declares the
		// name its consumer writes.
		r.SeedPackage("packages", "app")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "0.0.0",
  "dependencies": {"@acme/app": "0.0.0"}
}`)
		r.Commit("feat(app,web): bootstrap")
		return r
	}

	t.Run("substring", func(t *testing.T) {
		r := setup(t, "substring")
		r.ReleaseOK()
		require.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
		data, err := os.ReadFile(r.Path("packages", "web", "package.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"@acme/app": "^0.1.0"`,
			"the declared name's last segment is the package's folder name")
	})

	t.Run("exact", func(t *testing.T) {
		r := setup(t, "exact")
		r.ReleaseOK()
		require.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
		data, err := os.ReadFile(r.Path("packages", "web", "package.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"@acme/app": "0.0.0"`,
			"without the fallback nothing connects the two, which is the default")
	})
}

// TestAutoVersionReplaceRewritesOnlyWhatItMay: a replace rule walks the
// package folder, and the folders a workspace walk never enters are skipped
// here too — the version text inside node_modules belongs to somebody else's
// code. A link is not a file to rewrite either: rewriting it would write
// through it, twice.
func TestAutoVersionReplaceRewritesOnlyWhatItMay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a link needs a privilege the test runner may not have")
	}
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"},
		Flow: buildPublish(),
		AutoVersion: &models.AutoVersionConfig{
			Manifests: "none",
			Replace: []models.AutoVersionReplaceConfig{
				{Files: []string{"*.marker"}, Find: "core {previous}", Write: "core {version}"},
				// A rule about a provider, in a package that has none: it
				// expands to nothing, so it selects no files at all. The
				// selector reads an empty glob list as "nothing", which is the
				// opposite of what an empty list means for a range policy.
				{Files: []string{"*.marker"}, Find: "{provider} {providerPrevious}",
					Write: "{provider} {providerVersion}"},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/version.marker", "core 0.0.0\n")
	r.WriteFile("packages/core/node_modules/vendored/version.marker", "core 0.0.0\n")
	require.NoError(t, os.Symlink(r.Path("packages", "core", "version.marker"),
		r.Path("packages", "core", "link.marker")))
	r.Commit("feat(core): bootstrap")

	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())

	own, err := os.ReadFile(r.Path("packages", "core", "version.marker"))
	require.NoError(t, err)
	assert.Equal(t, "core 0.1.0\n", string(own))

	vendored, err := os.ReadFile(r.Path("packages", "core", "node_modules", "vendored", "version.marker"))
	require.NoError(t, err)
	assert.Equal(t, "core 0.0.0\n", string(vendored), "a rule must not reach into node_modules")

	info, err := os.Lstat(r.Path("packages", "core", "link.marker"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link is still a link, not a rewritten copy")
}
