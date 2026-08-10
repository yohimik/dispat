package integration

// Area 8: the init and preview commands through the compiled binary. Each is
// a small command with an observable artefact — a starter config the very
// next status can load, pending release notes on stdout — and this file pins
// exactly those artefacts plus the commands' exit codes over the process
// boundary.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCommandsInitThenStatusCompose: `dispat init --format toml` followed by
// a plain `dispat status` — the config file fallback finds dispat.toml with
// no --config anywhere, closing the loop between the two commands (and
// exercising the TOML starter end to end; the JSON and YAML starters load
// through the same fallback names).
func TestCommandsInitThenStatusCompose(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Command("init", "--format", "toml")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "created dispat.toml")

	status := r.StatusOK()
	assert.Contains(t, status.Stdout, "core", "the starter config must load and discover the package")

	res = r.Command("init", "--format", "toml")
	assert.Equal(t, 1, res.Code, "an existing config must never be overwritten")
}

// TestCommandsPreviewNotesWindowing: `dispat preview <package>` prints the
// package's pending release notes, and across a prerelease train the notes
// narrow to the fresh changeset — each prerelease's preview and changelog
// entry documents only its own changes, while the graduation collects the
// whole train into one entry. The version is still computed over the whole
// window: beta.1 stays 0.2.0-based even though its entry only shows a fix.
func TestCommandsPreviewNotesWindowing(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")

	res := r.Command("preview", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0", "the header names the pending tag")
	assert.Contains(t, res.Stdout, "### Features")
	assert.Contains(t, res.Stdout, "- first release")

	// Release, then preview again: the window is empty.
	r.ReleaseOK()
	res = r.Command("preview", "core")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "no pending changes for core")
	assert.Equal(t, 1, r.Command("preview", "ghost").Code, "an unknown package is an error")

	// entry returns the changelog entry for the given tag: the text between
	// its "## <tag> " header and the next entry header.
	entry := func(tag string) string {
		t.Helper()
		data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
		require.NoError(t, err)
		text := string(data)
		start := strings.Index(text, "## "+tag+" ")
		require.GreaterOrEqual(t, start, 0, "no changelog entry for %s:\n%s", tag, text)
		rest := text[start+3:]
		if next := strings.Index(rest, "\n## "); next >= 0 {
			rest = rest[:next]
		}
		return rest
	}

	r.CommitEmpty("feat(core)%beta: feature A")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.2.0-beta.0"), "tags: %v", r.TagList())
	assert.Contains(t, entry("core@0.2.0-beta.0"), "feature A")

	r.CommitEmpty("fix(core): fix B")
	// The preview of beta.1 already narrows to the fresh changeset.
	res = r.Command("preview", "core")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "fix B")
	assert.NotContains(t, res.Stdout, "feature A",
		"the preview of a prerelease must not repeat the train's published notes")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.2.0-beta.1"), "tags: %v", r.TagList())
	beta1 := entry("core@0.2.0-beta.1")
	assert.Contains(t, beta1, "fix B")
	assert.NotContains(t, beta1, "feature A", "a prerelease entry contains only its own changeset")

	r.CommitEmpty("release(core)%stable: graduate")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
	graduated := entry("core@0.2.0")
	assert.Contains(t, graduated, "feature A", "the graduation collects the whole train")
	assert.Contains(t, graduated, "fix B")
}

// TestCommandsPreviewAllPackages: `dispat preview` with no package name
// renders every package that has something pending, in publish order, and
// says "no pending changes" once nothing does. Packages with an empty window
// stay out of the combined preview instead of adding noise.
func TestCommandsPreviewAllPackages(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.SeedPackage("packages", "quiet")
	r.Commit("feat(core)^: streaming api\n---\nfix(web): close a leak")

	res := r.Command("preview")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0", "the provider's pending notes")
	assert.Contains(t, res.Stdout, "## web@0.0.1", "the consumer's pending notes")
	assert.Contains(t, res.Stdout, "- streaming api")
	assert.Contains(t, res.Stdout, "- close a leak")
	assert.NotContains(t, res.Stdout, "quiet", "a package with nothing pending stays out")
	assert.Less(t, strings.Index(res.Stdout, "## core@"), strings.Index(res.Stdout, "## web@"),
		"packages render in publish order")

	// Released, the combined preview is empty too.
	r.ReleaseOK()
	res = r.Command("preview")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "no pending changes")
	assert.NotContains(t, res.Stdout, "##", "no headers when nothing is pending")

	// Too many arguments stays a usage error.
	assert.Equal(t, 2, r.Command("preview", "core", "web").Code)
}
