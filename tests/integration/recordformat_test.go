package integration

// Area 7, continued: the shape of what a record says. records_test.go is about
// the artefacts a run leaves behind — the file, the tag, the commit, the API
// call. This file is about the bytes inside them: which sections an entry has
// and in what order, what a dependency line and an entry line link to, how a
// commit body is laid out under its bullet, what an entry with nothing to
// group says, and how much blank space separates one entry from the next.
//
// Every one of these renders into two destinations from one configuration, so
// each scenario asserts on the changelog file and on the body the fake GitHub
// API was handed. A feature that reaches only one of them is a bug the
// changelog alone would never show.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// bodyFor returns the body of the release created for tag, failing when no
// release carries it. It reads the same ghBody the channel scenarios decode:
// one recorded payload, however many views of it a test file wants.
func bodyFor(t *testing.T, bodies [][]byte, tag string) string {
	t.Helper()
	var tags []string
	for _, rel := range decodeAll[ghBody](t, bodies) {
		if rel.TagName == tag {
			return rel.Body
		}
		tags = append(tags, rel.TagName)
	}
	t.Fatalf("no GitHub release for %s; created: %v", tag, tags)
	return ""
}

// recordGitHub is the GitHub recorder every scenario here uses: on for every
// published package, pointed at the fake, with the entry format the scenario
// is about.
func recordGitHub(apiURL string, format models.EntryFormatConfig) *models.GitHubConfig {
	return &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: apiURL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: format,
	}
}

// TestRecordsDependencyLinksInBothDestinations: a dependencyLink template
// turns the dependencies section's bare names into links to the provider's own
// release, in the changelog file and in the GitHub body from one configured
// value. The movement itself is unchanged — a link is added to the line, not
// substituted for what it said.
func TestRecordsDependencyLinksInBothDestinations(t *testing.T) {
	srv, bodies := githubFake(t)
	const link = "https://forge.test/${DISPAT_DEP_NAME}/tag/${DISPAT_DEP_TAG}"

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	format := models.EntryFormatConfig{DependencyLink: link}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = recordGitHub(srv.URL, format)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	// Two runs, so the consumer's line spans a movement rather than a first
	// appearance: core releases alone, then a caret reaches app.
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core)^: close a leak")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.0.1"), "tags: %v", r.TagList())

	want := "- [core](https://forge.test/core/tag/core@0.1.1): 0.1.0 -> 0.1.1"
	log := changelogOf(t, r, "app")
	assert.Contains(t, log, want, "the changelog line links the provider's release:\n%s", log)
	assert.NotContains(t, log, "- core: 0.1.0",
		"a linked line replaces the plain one rather than joining it")
	assert.Contains(t, bodyFor(t, bodies(), "app@0.0.1"), want,
		"and one configured value serves both destinations")
}

// TestRecordsAutoDependencyLinksDeriveTheForgeURL: "auto" needs no template.
// It hangs the provider's tag off the package's own github owner and repo,
// which the changelog borrows because a file has none of its own.
func TestRecordsAutoDependencyLinksDeriveTheForgeURL(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	// The coordinates are configured while the recorder itself stays off: a
	// workspace that publishes no GitHub releases still links to the
	// repository its releases are tagged in.
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(false), Owner: "acme", Repo: "mono"}
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{DependencyLink: "auto"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core)^: close a leak")
	r.ReleaseOK()
	require.True(t, r.HasTag("app@0.0.1"), "tags: %v", r.TagList())

	assert.Contains(t, changelogOf(t, r, "app"),
		"- [core](https://github.com/acme/mono/releases/tag/core@0.1.1): 0.1.0 -> 0.1.1",
		"the changelog derives the URL from the package's github owner and repo")
}

// TestRecordsAutoLinksDeclineOutsideGitHubCom: the documented degradation, and
// the reason it is not a bug. The derivation is github.com's, and a GitHub
// Enterprise installation serves its web UI on a host its API URL does not
// state; rather than invent one, every "auto" line renders plain. A published
// record is permanent, and a link that leads nowhere is worse than no link —
// there is no later run in which it comes out right.
//
// The changelog declines with the GitHub body, because the coordinates it
// borrows are the whole of the package's github policy, API URL included.
func TestRecordsAutoLinksDeclineOutsideGitHubCom(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	format := models.EntryFormatConfig{
		DependencyLink: "auto",
		CommitRefs:     &models.CommitRefsConfig{Placement: "suffix", Link: "auto"},
	}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = recordGitHub(srv.URL, format)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core)^: close a leak")
	sha := r.Git("rev-parse", "HEAD")
	res := r.ReleaseOK("--log-level", "debug")
	require.True(t, r.HasTag("app@0.0.1"), "tags: %v", r.TagList())

	for name, text := range map[string]string{
		"changelog": changelogOf(t, r, "app"),
		"github":    bodyFor(t, bodies(), "app@0.0.1"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, text, "- core: 0.1.0 -> 0.1.1", "the plain movement line")
			assert.NotContains(t, text, "](http", "and no link anywhere in it")
		})
	}
	// The reference itself is still rendered, just not as a link: the commit
	// is known, only the URL is not.
	assert.Contains(t, changelogOf(t, r, "core"), "- close a leak ("+sha[:7]+")")

	// The decision is said out loud rather than left as a silently plain line.
	assert.Contains(t, res.Stdout, "record links fall back to plain text",
		"the declined derivation is logged, events:\n%s", res.Stdout)
}

// TestRecordsAutoLinksReadTheRepositoryFromTheEnvironment: the ordinary CI
// setup states the repository in $GITHUB_REPOSITORY and nowhere else. "auto"
// resolves through it, so a workspace that never writes owner and repo into
// its config still gets links.
func TestRecordsAutoLinksReadTheRepositoryFromTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/from-env")

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	// GitHub stays off — this is the changelog destination alone, resolving
	// coordinates nobody configured.
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{DependencyLink: "auto"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")

	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core)^: close a leak")
	r.ReleaseOK()

	assert.Contains(t, changelogOf(t, r, "app"),
		"- [core](https://github.com/acme/from-env/releases/tag/core@0.1.1): 0.1.0 -> 0.1.1")
}

// TestRecordsCommitRefsLinkTheForgeCommit: with references on, every entry
// line carries the commit behind it — abbreviated to the seven characters git
// itself abbreviates to, linked through the configured template in one
// destination and left plain in the other. The reference sits after the
// description and before the attribution, because who did it comes after what
// was done.
func TestRecordsCommitRefsLinkTheForgeCommit(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			CommitRefs: &models.CommitRefsConfig{
				Placement: "suffix",
				Link:      "https://forge.test/commit/${DISPAT_COMMIT}",
			},
		},
	}
	// The same policy without a link: the reference is still there, plain.
	cfg.GitHub = recordGitHub(srv.URL, models.EntryFormatConfig{
		CommitRefs: &models.CommitRefsConfig{Placement: "suffix"},
	})
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	sha := r.Git("rev-parse", "HEAD")
	require.Len(t, sha, 40, "a full object name is what the template interpolates")
	short := sha[:7]

	r.ReleaseOK()

	assert.Contains(t, changelogOf(t, r, "core"),
		"- add streaming (["+short+"](https://forge.test/commit/"+sha+"))",
		"the changelog links the reference through the template")
	assert.Contains(t, bodyFor(t, bodies(), "core@0.1.0"),
		"- add streaming ("+short+")",
		"and an unlinked policy still says which commit it was")
}

// TestRecordsCommitRefsAutoLinksTheRepository: "auto" builds the commit URL
// from the same coordinates the dependency links use, so a workspace states
// the repository once.
func TestRecordsCommitRefsAutoLinksTheRepository(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(false), Owner: "acme", Repo: "mono"}
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			CommitRefs: &models.CommitRefsConfig{Placement: "suffix", Link: "auto"},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	sha := r.Git("rev-parse", "HEAD")

	r.ReleaseOK()

	assert.Contains(t, changelogOf(t, r, "core"),
		"- add streaming (["+sha[:7]+"](https://github.com/acme/mono/commit/"+sha+"))")
}

// TestRecordsCustomNoChangesText: an entry with nothing to group carries the
// configured sentence instead of the built-in one, in both destinations — and
// a sentence that expands to nothing falls back to the built-in rather than
// standing, because an empty expansion is a mistake in the template and an
// entry must never render empty.
func TestRecordsCustomNoChangesText(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	format := models.EntryFormatConfig{
		NoChangesText: "Nothing of its own: see the ${DISPAT_PACKAGE} release notes.",
	}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = recordGitHub(srv.URL, format)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	// An exact pin with no pending work: the release exists because somebody
	// asked for the version, and there is nothing to list under it.
	r.CommitEmpty("release(core): cut it exactly here\n\nRelease-As: 1.0.0")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())

	want := "Nothing of its own: see the core release notes."
	entry := entryOf(t, changelogOf(t, r, "core"), "core@1.0.0")
	assert.Contains(t, entry, want)
	assert.NotContains(t, entry, "No changes:", "the configured sentence replaces the whole line")
	assert.Contains(t, bodyFor(t, bodies(), "core@1.0.0"), want,
		"the GitHub body carries the same sentence")
}

// TestRecordsNoChangesTextThatExpandsToNothingFallsBack: the other half. A
// template naming a variable nothing defines expands to nothing, and an entry
// with no sections and no sentence would publish as a header alone.
func TestRecordsNoChangesTextThatExpandsToNothingFallsBack(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{NoChangesText: "${NOTHING_DEFINES_THIS}"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	r.CommitEmpty("release(core): cut it exactly here\n\nRelease-As: 1.0.0")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())

	assert.Contains(t, entryOf(t, changelogOf(t, r, "core"), "core@1.0.0"),
		"No changes: a version set by Release-As.",
		"an empty expansion falls back to the built-in that names the cause")
}

// TestRecordsCustomTypeSectionsOrdered: the whole sections feature in one run.
// A custom section claims a commit type dispat has never heard of and declares
// the bump that makes it releasable at all; the list reorders two built-ins;
// and the built-ins the list never mentions are appended after it rather than
// dropped, because a section silently removed would take released work out of
// the record with it.
func TestRecordsCustomTypeSectionsOrdered(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// Added, then fixes, then breaking. Features and dependencies are never
	// named, so they follow in their default relative order.
	format := models.EntryFormatConfig{
		Sections: []models.SectionConfig{
			{Title: "Added", Types: []string{"add"}, Bump: "minor"},
			{Title: "fixes"},
			{Title: "breaking"},
		},
	}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = recordGitHub(srv.URL, format)
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")

	r.Commit("add(core): a brand new thing\n---\nfix(core): close a leak\n" +
		"---\nfeat(core): add streaming\n---\nfeat(core)!: drop the old API")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "the breaking change majors it; tags: %v", r.TagList())

	for name, text := range map[string]string{
		"changelog": entryOf(t, changelogOf(t, r, "core"), "core@1.0.0"),
		"github":    bodyFor(t, bodies(), "core@1.0.0"),
	} {
		t.Run(name, func(t *testing.T) {
			assertOrderedIn(t, text,
				"### Added", "- a brand new thing",
				"### Fixes", "- close a leak",
				"### Breaking Changes", "- drop the old API",
				"### Features", "- add streaming")
			assert.NotContains(t, text, "### Dependencies",
				"an appended built-in with nothing to show still renders nothing")
		})
	}

	// The bump the section declared reached the parser: a type nothing else in
	// the configuration mentions releases a minor on its own.
	r.CommitEmpty("add(core): another new thing")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@1.1.0"),
		"the section's bump made the type releasable; tags: %v", r.TagList())
}

// TestRecordsBreakingWinsOverACustomClaim: a claimed type that is also a
// breaking change renders under Breaking Changes. A reader scans an entry for
// what breaks them, and letting `add(x)!:` sit in "Added" would hide it behind
// the word its author chose for ordinary work.
func TestRecordsBreakingWinsOverACustomClaim(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			Sections: []models.SectionConfig{{Title: "Added", Types: []string{"add"}, Bump: "minor"}},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("add(core): an ordinary addition\n---\nadd(core)!: an addition that breaks you")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())

	// "Added" is the only section the list names, so it renders first and the
	// built-ins follow it. What this pins is not the order but the grouping:
	// the breaking addition is under Breaking Changes, not under the word its
	// author chose.
	entry := entryOf(t, changelogOf(t, r, "core"), "core@1.0.0")
	assertOrderedIn(t, entry,
		"### Added", "- an ordinary addition",
		"### Breaking Changes", "- an addition that breaks you")
	added := entry[strings.Index(entry, "### Added"):strings.Index(entry, "### Breaking Changes")]
	assert.NotContains(t, added, "breaks you", "a breaking change may not hide in a custom section")
}

// TestRecordsSectionBumpIsRefusedInAFolderConfig: the bump merges into the
// commit parser, which is built once for the whole repository out of the root
// file. A folder's own config is read later, during discovery, so a type
// declared there would render under its section without ever becoming
// releasable — a section nothing reaches. The load refuses it and says where
// the declaration belongs.
func TestRecordsSectionBumpIsRefusedInAFolderConfig(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/dispat.json", `{
  "changelog": {
    "sections": [{"title": "Added", "types": ["add"], "bump": "minor"}]
  }
}`)
	r.Commit("feat(core): add streaming")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	// A folder's config is read during discovery rather than at load, so the
	// refusal arrives as the run's own error event rather than on stderr.
	assert.Contains(t, res.Stdout, "package discovery failed")
	assert.Contains(t, res.Stdout, "sections[0]")
	assert.Contains(t, res.Stdout, "bump cannot be set in a folder's own config file")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
}

// TestRecordsBodyParagraphsStayInTheirBullet: a commit body is indented two
// spaces so that it is part of the bullet above it. Flush-left, the second
// paragraph leaves the list item in every markdown renderer there is, and the
// change's own explanation ends up reading as prose about the section. The
// blank line between the paragraphs stays genuinely blank — trailing spaces on
// an empty line are what a linter complains about next.
func TestRecordsBodyParagraphsStayInTheirBullet(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = recordGitHub(srv.URL, models.EntryFormatConfig{})
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming\n\n" +
		"The first paragraph says why it was done.\n\n" +
		"The second paragraph says how.")
	r.ReleaseOK()

	want := "- add streaming\n" +
		"  The first paragraph says why it was done.\n" +
		"\n" +
		"  The second paragraph says how.\n"
	for name, text := range map[string]string{
		"changelog": changelogOf(t, r, "core"),
		"github":    bodyFor(t, bodies(), "core@0.1.0"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, text, want, "the body is a continuation of its bullet:\n%s", text)
			assert.NotContains(t, text, "  \n", "a blank line inside a body carries no trailing space")
		})
	}
}

// TestRecordsEntrySpacingDefaultAndConfigured: the seam between one entry and
// what is under it is exactly the configured number of blank lines, whatever
// the entry above happened to end with. It used to vary with the shape of the
// release: an entry closing on a dependencies list left one blank line and one
// closing on a section of bodiless bullets left two, so a file recorded the
// shape of each release rather than one rule. The GitHub body has no seam at
// all — the spacing belongs to the file.
func TestRecordsEntrySpacingDefaultAndConfigured(t *testing.T) {
	srv, bodies := githubFake(t)

	// The default: two blank lines, so the bytes between two entries are
	// exactly "\n\n\n## ".
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = recordGitHub(srv.URL, models.EntryFormatConfig{})
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	// A tail written by hand before dispat, under the title dispat renders.
	r.WriteFile("packages/core/CHANGELOG.md", "# Changelog\n\nManual notes from before dispat.\n")

	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core): close a leak")
	r.ReleaseOK()

	log := changelogOf(t, r, "core")
	assert.True(t, strings.HasPrefix(log, "# Changelog\n\n## core@0.1.1 ("),
		"the title's own seam is one blank line:\n%s", log)
	assert.Contains(t, log, "\n\n\n## core@0.1.0 (", "two blank lines between entries")
	assert.True(t, strings.HasSuffix(log, "\n\n\nManual notes from before dispat.\n"),
		"and the same seam above content that predates dispat:\n%q", log)
	assert.NotContains(t, log, "\n\n\n\n", "never more than the configured seam")

	assert.False(t, strings.HasSuffix(bodyFor(t, bodies(), "core@0.1.1"), "\n\n"),
		"a GitHub body carries no entry seam: the spacing is the file's")

	// The same repository shape with the seam narrowed to one blank line.
	tight := harness.New(t)
	cfg = libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntrySpacing: models.Int(1)}
	tight.WriteConfigModel(cfg)
	tight.SeedPackage("packages", "core")
	tight.WriteFile("packages/core/CHANGELOG.md", "# Changelog\n\nManual notes from before dispat.\n")
	tight.Commit("feat(core): add streaming")
	tight.ReleaseOK()
	tight.CommitEmpty("fix(core): close a leak")
	tight.ReleaseOK()

	log = changelogOf(t, tight, "core")
	assert.Contains(t, log, "\n\n## core@0.1.0 (")
	assert.True(t, strings.HasSuffix(log, "\n\nManual notes from before dispat.\n"), "%q", log)
	assert.NotContains(t, log, "\n\n\n", "one blank line means one blank line")
}

// TestRecordsEntrySpacingOutsideItsBoundsIsAConfigError: the bounds exist so a
// mistyped value cannot write a screenful of nothing between every release.
func TestRecordsEntrySpacingOutsideItsBoundsIsAConfigError(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{EntrySpacing: models.Int(99)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stderr, "entrySpacing must be between 1 and 10")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"a config that does not load records nothing")
}

// TestRecordsDefaultsAreUnchangedByTheNewOptions: the byte-compatibility
// guarantee. A workspace that configures none of this gets the entry it always
// got — no links, no references, the built-in sentence, the built-in order —
// so the release that ships these options changes nobody's changelog.
func TestRecordsDefaultsAreUnchangedByTheNewOptions(t *testing.T) {
	// $GITHUB_REPOSITORY is what "auto" falls back to, and this suite may well
	// be running inside a workflow that sets it. Nothing here asks for auto,
	// so nothing may resolve through it either way; the variable is cleared so
	// the assertion means the same thing on a laptop and in CI.
	t.Setenv("GITHUB_REPOSITORY", "")

	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming\n\nA body under the bullet.")
	r.ReleaseOK()

	log := changelogOf(t, r, "core")
	assert.Equal(t, "# Changelog\n\n## core@0.1.0 ("+today(t, r)+")\n\n"+
		"### Features\n\n- add streaming\n  A body under the bullet.\n", log,
		"the whole file, byte for byte")
}

// today is the date the run stamped its entry with, read back off the file so
// a scenario asserting whole bytes does not race the clock at midnight.
func today(t *testing.T, r *harness.Repo) string {
	t.Helper()
	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	_, rest, ok := strings.Cut(string(data), " (")
	require.True(t, ok, "no entry header in:\n%s", data)
	date, _, ok := strings.Cut(rest, ")")
	require.True(t, ok)
	return date
}
