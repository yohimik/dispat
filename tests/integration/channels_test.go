// Goal 33: choosing the channels a record reaches. A changelog file or a
// releases page can be a record of the stable line alone, of one named
// prerelease channel, or of everything; and a single line inside an entry can
// be written for the betas alone while the sections around it stay whatever
// the release carries.
//
// The scenarios here compare two packages inside one run wherever they can,
// because "this one records and that one does not" is the claim, and a single
// run makes it without depending on anything between runs.

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// ghBody is the part of a created release the channel scenarios read: which
// tag it is for, and the body the entry format produced.
type ghBody struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

// TestChannelsNamedChannelGate: a policy naming one prerelease channel records
// on that channel and nowhere else, while a policy naming every prerelease
// channel makes the complementary cut. The two are configured side by side in
// one repository, so a beta, an rc and the graduation to stable each show both
// answers at once.
func TestChannelsNamedChannelGate(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// The releases page keeps the betas alone; the changelog keeps every
	// prerelease and no stable release at all.
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true), Channels: []string{"beta"},
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	cfg.Changelog = &models.ChangelogConfig{Channels: []string{"*"}}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")

	// A beta: on both policies' channels.
	r.Commit("feat(core)%beta: first work")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	changelog := func() string {
		data, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
		require.NoError(t, err)
		return string(data)
	}
	assert.Contains(t, changelog(), "## core@0.1.0-beta.0 (", "every prerelease is recorded")
	require.Len(t, bodies(), 1, "and the named channel is created")

	// An rc: the changelog records it, the releases page does not.
	r.CommitEmpty("release(core)%beta>rc: promote")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0-rc.0"), "tags: %v", r.TagList())
	assert.Contains(t, changelog(), "## core@0.1.0-rc.0 (", "an rc is a prerelease too")
	assert.Len(t, bodies(), 1, "but it is not the channel the releases page names")

	// The graduation: neither records it.
	r.CommitEmpty("release(core)%rc>stable: graduate")
	res := r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.NotContains(t, changelog(), "## core@0.1.0 (",
		"a changelog of the prereleases holds the stable release back")
	assert.Len(t, bodies(), 1, "and the releases page still carries the one beta")
	assert.Contains(t, res.Stdout, "the release's channel is not in changelog.channels",
		"the skip says which restriction held it back")
	assert.Contains(t, res.Stdout, `"channel":"stable"`, "and which channel it was")
}

// TestChannelsFilterRecordLines: a line inside an entry carries its own
// channels, so one configured footer can say one thing on the betas and
// another on the stable release. The sections between the lines are whatever
// the release carries and are not filtered with them.
func TestChannelsFilterRecordLines(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	format := models.EntryFormatConfig{
		Header: []models.EntryLine{
			{Line: []string{"This is a test build. Do not depend on it."}, Channels: []string{"*"}},
		},
		Footer: []models.EntryLine{
			{Line: []string{"Supported until the next major."}, Channels: []string{"stable"}},
			{Line: []string{"Every release, whatever its channel."}},
		},
	}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: format,
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")

	r.Commit("feat(core)%beta: streaming support")
	r.ReleaseOK()
	r.CommitEmpty("release(core)%beta>stable: graduate")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	entries := string(data)
	beta := entryFor(t, entries, "core@0.1.0-beta.0")
	stable := entryFor(t, entries, "core@0.1.0")

	assert.Contains(t, beta, "This is a test build.", "the prerelease line reaches the beta")
	assert.NotContains(t, beta, "Supported until the next major.", "and the stable line does not")
	assert.NotContains(t, stable, "This is a test build.", "nor the prerelease line the stable entry")
	assert.Contains(t, stable, "Supported until the next major.")
	for _, entry := range []string{beta, stable} {
		assert.Contains(t, entry, "Every release, whatever its channel.",
			"a line naming no channels reaches both")
		assert.Contains(t, entry, "streaming support",
			"and the sections are whatever the release carries, unfiltered")
	}

	// The GitHub bodies agree: one entry format, one answer, wherever it is
	// rendered.
	created := decodeAll[ghBody](t, bodies())
	require.Len(t, created, 2)
	assert.Contains(t, created[0].Body, "This is a test build.")
	assert.NotContains(t, created[0].Body, "Supported until the next major.")
	assert.Contains(t, created[1].Body, "Supported until the next major.")
	assert.NotContains(t, created[1].Body, "This is a test build.")
}

// entryFor slices one changelog entry out of a file: everything from its
// header line up to the next one.
func entryFor(t *testing.T, contents, tag string) string {
	t.Helper()
	start := strings.Index(contents, "## "+tag+" (")
	require.GreaterOrEqual(t, start, 0, "the file carries an entry for %s:\n%s", tag, contents)
	rest := contents[start+3:]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		return rest[:next]
	}
	return rest
}

// TestChannelsCombineWithPackageFilters: channels is one filter among the
// others, so a line carrying a package filter and a channel filter is written
// only where both hold.
func TestChannelsCombineWithPackageFilters(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: models.EntryFormatConfig{
		Footer: []models.EntryLine{
			{Line: []string{"core, on the betas"}, Package: []string{"core"}, Channels: []string{"*"}},
		},
	}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")

	r.Commit("feat(core,utils)%beta: first work")
	r.ReleaseOK()
	r.CommitEmpty("release(core,utils)%beta>stable: graduate")
	r.ReleaseOK()

	core, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	utils, err := os.ReadFile(r.Path("packages/utils/CHANGELOG.md"))
	require.NoError(t, err)

	assert.Contains(t, entryFor(t, string(core), "core@0.1.0-beta.0"), "core, on the betas",
		"the package and the channel both match")
	assert.NotContains(t, entryFor(t, string(core), "core@0.1.0"), "core, on the betas",
		"the package matches and the channel does not")
	assert.NotContains(t, string(utils), "core, on the betas",
		"the channel matches and the package does not")
}

// TestChannelsKeepReleasersApart: two packages whose GitHub policies differ
// only in a line's channels must not share one releaser, or one package's
// entry format would render the other's body.
func TestChannelsKeepReleasersApart(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: models.EntryFormatConfig{
			Footer: []models.EntryLine{{Line: []string{"the shared footer"}, Channels: []string{"stable"}}},
		},
	}
	cfg.Packages = map[string]models.PackageConfig{"utils": {GitHub: &models.GitHubConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			Footer: []models.EntryLine{{Line: []string{"the shared footer"}, Channels: []string{"*"}}},
		},
	}}}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")

	r.Commit("feat(core,utils)%beta: first work")
	r.ReleaseOK()

	created := decodeAll[ghBody](t, bodies())
	require.Len(t, created, 2)
	byTag := map[string]ghBody{}
	for _, c := range created {
		byTag[c.TagName] = c
	}
	assert.NotContains(t, byTag["core@0.1.0-beta.0"].Body, "the shared footer",
		"core records the line on the stable line alone")
	assert.Contains(t, byTag["utils@0.1.0-beta.0"].Body, "the shared footer",
		"and utils, whose only difference is the line's channels, records it on the beta")
}

// TestChannelsValidationRefusals: a restriction naming nothing is a mistake,
// and a file title that varied by channel would be written again every time
// the channel moved. Both are refused where they are written, before any work.
func TestChannelsValidationRefusals(t *testing.T) {
	cases := []struct {
		name, want string
		changelog  map[string]any
	}{
		{"an empty channel name in a line", "changelog: footer[0]: channels must not contain an empty name",
			map[string]any{"footer": []any{map[string]any{"line": "x", "channels": []any{""}}}}},
		{"an empty channel name on the object", "changelog: channels must not contain an empty name",
			map[string]any{"channels": []any{" "}}},
		{"channels on a file title", "changelog: fileTitle[0]: channels is not allowed here",
			map[string]any{"fileTitle": []any{map[string]any{"line": "# Changelog", "channels": "stable"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := harness.New(t)
			cfg := rawSplitConfig()
			cfg["scripts"] = map[string]any{"build": "echo building > built.txt", "publish": "echo publishing"}
			cfg["changelog"] = tc.changelog
			r.WriteConfigRaw(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first release")

			res := r.Release()
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stderr, tc.want)
			assert.Empty(t, r.TagList(), "nothing released")
			assert.NoFileExists(t, r.Path("built.txt"), "no script ran")
		})
	}
}

// TestChannelsPreviewShowsBothBodies: preview renders the body each record
// would carry, under that record's own entry format, so the lines a channel
// admits can be read before anything is released.
func TestChannelsPreviewShowsBothBodies(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: models.EntryFormatConfig{
		Footer: []models.EntryLine{{Line: []string{"from the changelog"}}},
	}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true), Channels: []string{"stable"},
		Owner: "acme", Repo: "mono", APIURL: "https://example.invalid", TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: models.EntryFormatConfig{
			ReleaseName: "core ${DISPAT_VERSION}",
			Footer: []models.EntryLine{
				{Line: []string{"from the release, on the betas"}, Channels: []string{"*"}},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)%beta: streaming support")

	both := r.Command("preview", "--changelog", "--github", "--package", "core")
	require.Equal(t, 0, both.Code, "stderr:\n%s", both.Stderr)
	assert.Equal(t, 1, strings.Count(both.Stdout, "## core@0.1.0-beta.0"), "one package, one header")
	assert.Contains(t, both.Stdout, "--- changelog ---")
	assert.Contains(t, both.Stdout, "--- github release ---")
	assert.Contains(t, both.Stdout, "from the changelog")
	assert.Contains(t, both.Stdout, "github release withheld: the channels do not admit beta",
		"a record the channels hold back says so instead of showing a body nothing would receive")

	// The default is the changelog entry, and it is what --changelog prints.
	bare := r.Command("preview", "--package", "core")
	require.Equal(t, 0, bare.Code, "stderr:\n%s", bare.Stderr)
	only := r.Command("preview", "--changelog", "--package", "core")
	require.Equal(t, 0, only.Code, "stderr:\n%s", only.Stderr)
	assert.Equal(t, bare.Stdout, only.Stdout, "naming the changelog prints what naming nothing prints")
	assert.NotContains(t, bare.Stdout, "---", "one body needs no label")
	assert.NotContains(t, bare.Stdout, "from the release", "and carries the changelog format alone")

	// Once the release graduates, the github body is the one the releases
	// page would receive, under the github format.
	r.ReleaseOK()
	r.CommitEmpty("release(core)%beta>stable: graduate")
	gh := r.Command("preview", "--github", "--package", "core")
	require.Equal(t, 0, gh.Code, "stderr:\n%s", gh.Stderr)
	assert.Contains(t, gh.Stdout, "### core 0.1.0", "the release name heads the body")
	assert.NotContains(t, gh.Stdout, "from the changelog", "under the github format, not the changelog's")
	assert.NotContains(t, gh.Stdout, "from the release, on the betas",
		"and the line the stable channel does not admit is not in it")
	assert.NotContains(t, gh.Stdout, "### Release", "a preview has published nothing to report")
}

// TestChannelsAreReportedInTheSkipEvent: the skip is an info-level event
// carrying the package, the tag and the channel, so a flow can tell "held
// back by configuration" from "failed" without reading prose.
func TestChannelsAreReportedInTheSkipEvent(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Channels: []string{"stable"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)%beta: first work")

	res := r.ReleaseOK()
	var found bool
	for _, ev := range res.Events {
		if !strings.Contains(ev.Str("message"), "changelog entry skipped") {
			continue
		}
		found = true
		assert.Equal(t, "info", ev.Str("level"), "a release-shaped decision the operator should see")
		assert.Equal(t, "core", ev.Package())
		assert.Equal(t, "core@0.1.0-beta.0", ev.Str("tag"))
		assert.Equal(t, "beta", ev.Str("channel"), "named by the channel that was held back")
	}
	assert.True(t, found, "the skip is on the record")
	assert.NoFileExists(t, r.Path("packages/core/CHANGELOG.md"))
	assert.True(t, r.HasTag("core@0.1.0-beta.0"), "the release itself is untouched")
}
