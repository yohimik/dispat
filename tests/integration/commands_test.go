package integration

// Area 8: the init and preview commands through the compiled binary. Each is
// a small command with an observable artefact — a starter config the very
// next status can load, pending release notes on stdout — and this file pins
// exactly those artefacts plus the commands' exit codes over the process
// boundary.

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

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

// TestCommandsPreviewNotesWindowing: `dispat preview --package <name>` prints the
// package's pending release notes, and across a prerelease train the notes
// narrow to the fresh changeset — each prerelease's preview and changelog
// entry documents only its own changes, while the graduation collects the
// whole train into one entry. The version is still computed over the whole
// window: beta.1 stays 0.2.0-based even though its entry only shows a fix.
func TestCommandsPreviewNotesWindowing(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")

	res := r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "## core@0.1.0", "the header names the pending tag")
	assert.Contains(t, res.Stdout, "### Features")
	assert.Contains(t, res.Stdout, "- first release")

	// Release, then preview again: the window is empty.
	r.ReleaseOK()
	res = r.Command("preview", "--package", "core")
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "no pending changes for core")
	assert.Equal(t, 1, r.Command("preview", "-p", "ghost").Code, "an unknown package is an error")

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
	res = r.Command("preview", "--package", "core")
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

// TestCommandsPreviewAllPackages: `dispat preview` with no filter
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

	// A positional package is a usage error: the selection is a flag.
	assert.Equal(t, 2, r.Command("preview", "core").Code)
}

// TestCommandsPreviewRecordBodies: which body `dispat preview` prints.
// --github renders what the releases page would receive, under the github
// entry format rather than the changelog's, and covers every selected package
// under one header each. A record switched off says so instead of showing a
// body nothing would receive.
func TestCommandsPreviewRecordBodies(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: models.EntryFormatConfig{
		Footer: []models.EntryLine{{Line: []string{"read the changelog"}}},
	}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: "https://example.invalid", TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: models.EntryFormatConfig{
			ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
			Footer:      []models.EntryLine{{Line: []string{"read the release"}}},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): streaming api")

	gh := r.Command("preview", "--github")
	require.Equal(t, 0, gh.Code, "stderr:\n%s", gh.Stderr)
	assert.Contains(t, gh.Stdout, "### core 0.1.0", "the configured release name heads each body")
	assert.Contains(t, gh.Stdout, "### utils 0.1.0", "every package with something pending")
	assert.Contains(t, gh.Stdout, "read the release")
	assert.Contains(t, gh.Stdout, "- streaming api", "the sections are the release's own")
	assert.NotContains(t, gh.Stdout, "read the changelog", "under the github format alone")
	assert.NotContains(t, gh.Stdout, "### Release", "a preview has published nothing to report")

	both := r.Command("preview", "--changelog", "--github", "--package", "core")
	require.Equal(t, 0, both.Code, "stderr:\n%s", both.Stderr)
	assert.Equal(t, 1, strings.Count(both.Stdout, "## core@0.1.0"), "one package, one header")
	assert.Contains(t, both.Stdout, "read the changelog")
	assert.Contains(t, both.Stdout, "read the release")
	assert.Less(t, strings.Index(both.Stdout, "--- changelog ---"),
		strings.Index(both.Stdout, "--- github release ---"),
		"both are labelled, the changelog first")

	// A record switched off has no body to show, and says which record it was.
	cfg.GitHub.Enabled = models.Bool(false)
	r.WriteConfigModel(cfg)
	off := r.Command("preview", "--github", "--package", "core")
	require.Equal(t, 0, off.Code, "stderr:\n%s", off.Stderr)
	assert.Contains(t, off.Stdout, "github release withheld: disabled by config")
	assert.NotContains(t, off.Stdout, "read the release")

	// Nothing pending is still nothing pending, whichever body was asked for.
	r.ReleaseOK()
	empty := r.Command("preview", "--github")
	require.Equal(t, 0, empty.Code, "stderr:\n%s", empty.Stderr)
	assert.Contains(t, empty.Stdout, "no pending changes")
}

// TestCommandsHelpIsScopedToTheCommand: `dispat <command> --help` prints
// that command's synopsis and its own flags, not the whole program's. The
// program help without a command word lists every command and the global
// flags alone, so it stays readable however the flag set grows. Both exit 0
// and need no config file or repository.
func TestCommandsHelpIsScopedToTheCommand(t *testing.T) {
	r := harness.New(t)

	program := r.Command("--help")
	require.Equal(t, 0, program.Code, "stderr:\n%s", program.Stderr)
	assert.Contains(t, program.Stderr, "usage: dispat [command] [flags]")
	for _, word := range []string{"release", "status", "run", "init", "preview", "changelog",
		"autoversion", "autowriter", "commit", "github", "compute", "scanner", "writer", "replacer"} {
		assert.Contains(t, program.Stderr, word, "the command list names every command")
	}
	assert.Contains(t, program.Stderr, "global flags:")
	assert.NotContains(t, program.Stderr, "--set-version",
		"a command's own flags stay out of the program help")

	for name, tc := range map[string]struct {
		args        []string
		usage       string
		has, hasNot []string
	}{
		"run": {
			args: []string{"run", "--help"}, usage: "usage: dispat run <script> [-- args...] [flags]",
			has:    []string{"--on-error", "--since", "--consumers"},
			hasNot: []string{"--set-version", "--tag", "--owner"},
		},
		"github": {
			args: []string{"github", "--help"}, usage: "usage: dispat github [flags]",
			// A step command sweeps packages like run does, window flags and all.
			has:    []string{"--owner", "--repo", "--token-env", "--target", "--on-error", "--since"},
			hasNot: []string{"--set-version", "--file"},
		},
		"autowriter": {
			args: []string{"autowriter", "--help"}, usage: "usage: dispat autowriter [flags]",
			has: []string{"--set-version", "--set", "--link", "--manifests",
				"--set-local", "--link-local", "--unlink-local", "--range",
				"--only-updated", "--since", "--consumers"},
			hasNot: []string{"--tag", "--owner"},
		},
		"writer": {
			args: []string{"writer", "--help"}, usage: "usage: dispat writer <manifest>... [flags]",
			has:    []string{"--set-version", "--set", "--link"},
			hasNot: []string{"--on-error", "--tag"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Command(tc.args...)
			assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Contains(t, res.Stderr, tc.usage)
			for _, want := range tc.has {
				assert.Contains(t, res.Stderr, want)
			}
			for _, unwanted := range tc.hasNot {
				assert.NotContains(t, res.Stderr, unwanted, "another command's flag leaked in")
			}
		})
	}
}

// TestCommandsVersionNamesThePlatform: --version answers without a config
// file, and says which build is running — a version alone does not
// distinguish the release's binaries from each other.
func TestCommandsVersionNamesThePlatform(t *testing.T) {
	r := harness.New(t)
	res := r.Command("--version")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Regexp(t, `dispat \S+ \(`+runtime.GOOS+`_`+runtime.GOARCH+`\)`, res.Stdout)
}

// TestCommandsReservedWordsShadowTheirScripts: a command word always wins
// over a run script of the same name, and the two-word `dispat run <word>`
// is how the script is reached instead. Both halves matter: the first is why
// adding a command word is a breaking change for anyone whose config already
// uses it, and the second is why doing so does not take their script away.
//
// The words gathered here are the ones whose bare form needs arguments, so
// the command winning shows up as the usage exit rather than as output. The
// words whose bare form does something observable prove the same rule inside
// their own areas, where the thing it does is the interesting part.
func TestCommandsReservedWordsShadowTheirScripts(t *testing.T) {
	for _, word := range []string{"autowriter", "autoreplacer"} {
		t.Run(word, func(t *testing.T) {
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			cfg.Scripts[word] = models.Script{"echo the script ran"}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")

			res := r.Command("run", word)
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Contains(t, res.Stdout, "the script ran",
				"the two-word spelling still reaches the script")

			assert.Equal(t, 2, r.Command(word).Code,
				"the bare word is the command, which needs something to write")
		})
	}
}
