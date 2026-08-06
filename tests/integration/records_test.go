package integration

// Area 7: the release records — changelog files, git tags and the release
// commit — as durable artefacts. The other areas assert that records exist;
// this one asserts what they *are*: a changelog accumulates entries newest
// first above content that predates dispat, tags are annotated objects with
// the release message pointing at the released commit, and commit mode
// produces one commit carrying the changelogs, with the tags placed on it
// and everything pushable to a real remote.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestRecordsChangelogAccumulatesAcrossReleases: entries prepend newest
// first under the title, a changelog that existed before dispat keeps its
// content below every generated entry, sections are grouped by bump, and a
// consumer's entry carries the provider's version movement.
func TestRecordsChangelogAccumulatesAcrossReleases(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	// A changelog that predates dispat: its content must survive, below.
	r.WriteFile("packages/core/CHANGELOG.md", "# Changelog\n\nManual notes from before dispat.\n")

	// Run 1: a plain feat on core alone.
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	// Run 2: two units in one commit — a fix and a breaking change — plus a
	// caret, so app releases with a dependencies section.
	r.CommitEmpty("fix(core)^: close a leak\n---\nfeat(core)!: drop the old API")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "the breaking change majors core; tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.0.1"), "the caret reaches app; tags: %v", r.TagList())

	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	log := string(data)

	// Newest entry first, oldest generated entry next, pre-dispat content last.
	first := strings.Index(log, "## core@1.0.0")
	second := strings.Index(log, "## core@0.1.0")
	manual := strings.Index(log, "Manual notes from before dispat.")
	require.True(t, first >= 0 && second >= 0 && manual >= 0, "changelog:\n%s", log)
	assert.True(t, first < second && second < manual,
		"entries newest first, pre-existing content preserved below:\n%s", log)
	assert.True(t, strings.HasPrefix(log, "# Changelog\n"), "one title at the top")
	assert.Equal(t, 1, strings.Count(log, "# Changelog\n"), "the title is never duplicated")

	// The 1.0.0 entry groups its two units by bump.
	entry := log[first:second]
	assert.Contains(t, entry, "### Breaking Changes")
	assert.Contains(t, entry, "- drop the old API")
	assert.Contains(t, entry, "### Fixes")
	assert.Contains(t, entry, "- close a leak")
	assert.NotContains(t, entry, "add streaming", "run 1's unit belongs to the 0.1.0 entry alone")

	// The consumer's entry records the provider's movement, not a bare name.
	appLog, err := os.ReadFile(r.Path("packages", "app", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(appLog), "## app@0.0.1")
	assert.Contains(t, string(appLog), "### Dependencies")
	assert.Contains(t, string(appLog), "- core: 0.1.0 -> 1.0.0")
}

// TestRecordsChangelogCustomFileTitleAndSections: the changelog options
// change the artefact on disk — file name, title line and section headings —
// and the default CHANGELOG.md is not written at all.
func TestRecordsChangelogCustomFileTitleAndSections(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = models.ChangelogConfig{
		File:  "HISTORY.md",
		Title: "# History",
		EntryFormatConfig: models.EntryFormatConfig{
			FeaturesTitle: "What's new",
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "HISTORY.md"))
	require.NoError(t, err, "the configured file is written")
	log := string(data)
	assert.True(t, strings.HasPrefix(log, "# History\n"))
	assert.Contains(t, log, "### What's new")
	assert.Contains(t, log, "- add streaming")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"the default file must not appear next to the configured one")
}

// TestRecordsTagsAreAnnotatedWithReleaseMessages: a release tag is an
// annotated tag object (not a lightweight ref), its message is the release
// message, and it points at the commit that was released.
func TestRecordsTagsAreAnnotatedWithReleaseMessages(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"))

	assert.Equal(t, "tag", r.Git("cat-file", "-t", "core@0.1.0"),
		"a release tag is an annotated tag object, not a lightweight ref")
	assert.Equal(t, "release core@0.1.0",
		r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/core@0.1.0"))
	assert.Equal(t, r.Git("rev-parse", "HEAD"), r.Git("rev-list", "-n1", "core@0.1.0"),
		"the tag peels to the released commit")
}

// TestRecordsReleaseCommitTagsAndPush: commit mode end to end through the
// real binary against a real (bare) remote — one release commit carrying
// every published package's changelog, the tags placed on that commit, the
// branch and tags pushed, and the next run converging because dispat's own
// release-commit scope is exempt from scope resolution.
func TestRecordsReleaseCommitTagsAndPush(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = models.CommitConfig{Enabled: harness.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	bare := t.TempDir()
	r.Git("init", "-q", "--bare", bare)
	r.Git("remote", "add", "origin", bare)

	r.Commit("feat(a,b): first release of both")
	r.ReleaseOK()

	// One release commit, default message, carrying both changelogs.
	assert.Equal(t, "chore(release): a@0.1.0, b@0.1.0", r.Git("log", "-1", "--format=%s"))
	committed := r.Git("show", "--name-only", "--format=", "HEAD")
	assert.Contains(t, committed, "packages/a/CHANGELOG.md")
	assert.Contains(t, committed, "packages/b/CHANGELOG.md")

	// The tags sit on the release commit, not on the released source commit.
	head := r.Git("rev-parse", "HEAD")
	for _, tag := range []string{"a@0.1.0", "b@0.1.0"} {
		require.True(t, r.HasTag(tag), "tags: %v", r.TagList())
		assert.Equal(t, head, r.Git("rev-list", "-n1", tag), "%s must point at the release commit", tag)
	}

	// Branch and tags arrived on the remote.
	remoteRefs := r.Git("ls-remote", "origin")
	assert.Contains(t, remoteRefs, "refs/tags/a@0.1.0")
	assert.Contains(t, remoteRefs, "refs/tags/b@0.1.0")
	assert.Contains(t, remoteRefs, head, "the release commit itself is on the remote")

	// Converged: the release commit's own scope is exempt (nonPackageScopes
	// defaults to ["release"]), so a re-run releases nothing and moves nothing.
	r.ReleaseOK()
	assert.Equal(t, head, r.Git("rev-parse", "HEAD"), "no second release commit")
	assert.Equal(t, 2, len(r.TagList()), "no new tags on a quiet run")
}
