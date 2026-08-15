package integration

// Area 7: the release records — changelog files, git tags and the release
// commit — as durable artefacts. The other areas assert that records exist;
// this one asserts what they *are*: a changelog accumulates entries newest
// first above content that predates dispat, tags are annotated objects with
// the release message pointing at the released commit, and commit mode
// produces one commit carrying the changelogs, with the tags placed on it
// and everything pushable to a real remote.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

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
	cfg.Changelog = &models.ChangelogConfig{
		File:      "HISTORY.md",
		FileTitle: recordLines("# History"),
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

// TestRecordsTagFailureDoesNotUnpublishTheRelease: the whole post-publish
// failure model, end to end through the real binary.
//
// A tag sitting at a foreign commit is the one tagging failure that is easy
// to construct and the most dangerous to mishandle. The run must publish, say
// so, refuse to move the tag, keep going through the packages after the
// failing one, and still exit non-zero. What it must not do is call the
// package failed: the artefact is out, and reporting otherwise would skip
// every consumer and revert a folder for nothing.
func TestRecordsTagFailureDoesNotUnpublishTheRelease(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = models.Dependencies{{Consumer: "consumer", Provider: "core"}}
	// Force off, so the pre-existing tag below actually refuses the write.
	// With force on it would simply be rewritten, which is the point of the
	// setting and is covered by TestRecordsForceRewritesAnUnreachableTag.
	cfg.Commit = &models.CommitConfig{Force: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "consumer")
	r.Commit("feat(core,consumer): first release")

	// A tag carrying core's planned name, parked on a commit this branch does
	// not reach — a tag made on someone else's branch. dispat's baseline query
	// cannot see it, so nothing plans around it and git refuses the write.
	r.WriteFile("decoy.txt", "not the release commit\n")
	r.Commit("chore: elsewhere")
	r.Git("tag", "-a", "core@0.1.0", "-m", "someone else's tag")
	r.Git("reset", "--hard", "HEAD~1")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "a release missing its tag must not exit green")

	// Published, and the log says both halves of the truth. E220 is
	// CodeTagFailed; this module cannot import the CLI's internals, so the code
	// travels as the literal CI would match on.
	assert.True(t, harness.HasCode(res.Events, "E220"),
		"the failure is reported under its own code, events:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, `"status":"published"`)
	assert.NotContains(t, res.Stdout, `"status":"failed"`,
		"a published package is not a failed one")
	assert.Contains(t, res.Stdout, `"critical":1`, "and the totals account for it")

	// The consumer had a published provider to build against and released.
	assert.True(t, r.HasTag("consumer@0.1.0"), "tags: %v", r.TagList())

	// The foreign tag was left exactly where it was: moving it would rewrite a
	// record this run did not make.
	assert.Equal(t, "someone else's tag",
		r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/core@0.1.0"))
}

// TestRecordsForceRewritesAnUnreachableTag: the other side of the same setup.
// A tag on a commit this branch cannot reach is invisible to the planner, so
// dispat has no way to plan around it and no basis for treating it as a
// record of anything. With force on — the default — the write simply
// succeeds and the tag names this release.
//
// A tag dispat *can* see at a different commit is still left alone (E221).
// Force means "do not fail because the ref exists", not "overwrite whatever
// is there".
func TestRecordsForceRewritesAnUnreachableTag(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")

	r.WriteFile("decoy.txt", "not the release commit\n")
	r.Commit("chore: elsewhere")
	r.Git("tag", "-a", "core@0.1.0", "-m", "someone else's tag")
	r.Git("reset", "--hard", "HEAD~1")

	r.ReleaseOK()

	assert.Equal(t, "release core@0.1.0",
		r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/core@0.1.0"),
		"the tag now records this release")
	assert.Equal(t, r.Git("rev-parse", "HEAD"), r.Git("rev-list", "-n1", "core@0.1.0"))
}

// TestRecordsTagAtAnotherCommitIsLeftAlone: the sharper half of the same
// rule. Here dispat can see the existing tag and can tell it is at the wrong
// commit, which is reported under its own code (E221) — and the tag is left
// where it is rather than moved, because a tag that moved here would be
// force-pushed over the copy on the remote, turning one local mistake into
// everyone's.
//
// It goes through `dispat commit --tag-name` because that is the path that
// can actually reach the case: a release run plans its version *from* the
// tags, so it never picks a version whose tag already exists.
func TestRecordsTagAtAnotherCommitIsLeftAlone(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): first release")
	// A tag under the package's own format, so the baseline query finds it and
	// dispat can compare where it points. Its commit is this one.
	r.Git("tag", "-a", "core@9.9.9", "-m", "someone else's tag")
	at := r.Git("rev-parse", "HEAD")

	r.WriteFile("packages/core/next.txt", "more work\n")
	r.Commit("feat(core): more work")

	res := r.Command("commit", "--tag", "--tag-name", "core@9.9.9", "--package", "core")
	require.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.HasCode(res.Events, "E221"), "events:\n%s", res.Stdout)

	assert.Equal(t, "someone else's tag",
		r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/core@9.9.9"),
		"the existing tag keeps its message")
	assert.Equal(t, at, r.Git("rev-list", "-n1", "core@9.9.9"), "and its commit")
}

// TestRecordsReleaseCommitTagsAndPush: commit mode end to end through the
// real binary against a real (bare) remote — one release commit carrying
// every published package's changelog, the tags placed on that commit, the
// branch and tags pushed, and the next run converging because dispat's own
// release-commit scope is exempt from scope resolution.
func TestRecordsReleaseCommitTagsAndPush(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.AddBareRemote()

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

// TestRecordsCommitModeLeavesHistoryUntouchedWhenNothingPublished: the
// release commit is created at the end of the run, only when something
// published, so a run where every package fails leaves the history exactly
// as it was: no commit, no tags.
func TestRecordsCommitModeLeavesHistoryUntouchedWhenNothingPublished(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("exit 1", 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): will fail its build")
	base := r.Git("rev-parse", "HEAD")

	res := r.Release()
	require.Equal(t, 1, res.Code, "the failing package must fail the run\nstdout:\n%s", res.Stdout)
	assert.Equal(t, base, r.Git("rev-parse", "HEAD"),
		"no release commit may exist when nothing published")
	assert.NotContains(t, r.Git("log", "-1", "--format=%s"), "chore(release)")
	assert.Empty(t, r.TagList())
}

// TestRecordsPushSkipsExistingRemoteTags: a tag already present on the remote
// (left by a partially pushed earlier run) is skipped with the rest of the
// push going through — the branch, the release commit and every new tag —
// and the pre-existing remote tag keeping its original target, because this
// run turns force off. With force on (the default) the same tag is replaced;
// see TestRecordsPushForceReplacesExistingRemoteTags.
func TestRecordsPushSkipsExistingRemoteTags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{
		Enabled: models.Bool(true), Push: true, Force: models.Bool(false),
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.AddBareRemote()
	r.Commit("feat(a,b): first release of both")

	// Plant a's future tag on the remote only: create it at the source
	// commit, push it, delete it locally, so the planner still plans a@0.1.0.
	r.Git("tag", "-a", "a@0.1.0", "-m", "left by an earlier partial push")
	r.Git("push", "-q", "origin", "a@0.1.0")
	remoteTarget := r.Git("rev-list", "-n1", "a@0.1.0")
	r.Git("tag", "-d", "a@0.1.0")

	r.ReleaseOK()
	head := r.Git("rev-parse", "HEAD")

	remoteRefs := r.Git("ls-remote", "origin")
	assert.Contains(t, remoteRefs, "refs/tags/b@0.1.0", "the new tag arrives")
	assert.Contains(t, remoteRefs, head, "the release commit arrives")
	stillAt := strings.SplitN(r.Git("ls-remote", "origin", "refs/tags/a@0.1.0^{}"), "\t", 2)[0]
	assert.Equal(t, remoteTarget, stillAt, "the existing remote tag is skipped, not overwritten")
}

// TestRecordsPushForceReplacesExistingRemoteTags: the default. A tag the
// remote already carries is overwritten rather than skipped forever, which is
// what closes the window between the check and the push — and the only way a
// moving tag could ever move. The replacement is reported, not silent.
func TestRecordsPushForceReplacesExistingRemoteTags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.AddBareRemote()
	r.Commit("feat(a): first release")

	r.Git("tag", "-a", "a@0.1.0", "-m", "left by an earlier partial push")
	r.Git("push", "-q", "origin", "a@0.1.0")
	staleTarget := r.Git("rev-list", "-n1", "a@0.1.0")
	r.Git("tag", "-d", "a@0.1.0")

	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "overwritten", "the replacement is reported")

	nowAt := strings.SplitN(r.Git("ls-remote", "origin", "refs/tags/a@0.1.0^{}"), "\t", 2)[0]
	assert.NotEqual(t, staleTarget, nowAt, "the remote tag was moved onto this release")
	assert.Equal(t, r.Git("rev-list", "-n1", "a@0.1.0"), nowAt, "and agrees with the local tag")
}

// TestRecordsExportedPackageCommitPinsTheTag: a release script exporting
// PACKAGE_<KEY>=<commitHash> pins its package's tag to that commit, so in
// commit mode the tag lands on the exported (source) commit while the
// release commit still carries the changelog on top.
func TestRecordsExportedPackageCommitPinsTheTag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{`echo "PACKAGE_CORE=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"`}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	source := r.Git("rev-parse", "HEAD")

	r.ReleaseOK()

	assert.Equal(t, "chore(release): core@0.1.0", r.Git("log", "-1", "--format=%s"),
		"the release commit still exists")
	assert.NotEqual(t, source, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, source, r.Git("rev-list", "-n1", "core@0.1.0"),
		"the tag is pinned to the exported commit, not the release commit")
}

// TestRecordsExportedCommitExcludesTagFromReleaseCommitAndPushes: in a mixed
// run only the exporting package's tag moves to the exported commit — no tag
// for it is created on the release commit — while its space mate keeps the
// normal placement, and the push delivers both tags with their respective
// targets intact.
func TestRecordsExportedCommitExcludesTagFromReleaseCommitAndPushes(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{`if [ "$DISPAT_PACKAGE" = "a" ]; then` +
		` echo "PACKAGE_A=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"; fi`}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")

	r.AddBareRemote()

	r.Commit("feat(a,b): first release of both")
	source := r.Git("rev-parse", "HEAD")

	r.ReleaseOK()
	head := r.Git("rev-parse", "HEAD")
	require.NotEqual(t, source, head, "the release commit exists on top of the source commit")

	// The exporting package's tag is pinned to the exported commit and is
	// NOT created on the release commit; the other package's tag is.
	assert.Equal(t, source, r.Git("rev-list", "-n1", "a@0.1.0"))
	assert.Equal(t, head, r.Git("rev-list", "-n1", "b@0.1.0"))
	assert.Equal(t, "b@0.1.0", r.Git("tag", "--points-at", head),
		"only the non-exporting package's tag sits on the release commit")

	// Both tags arrive on the remote with their targets intact.
	aAt := strings.SplitN(r.Git("ls-remote", "origin", "refs/tags/a@0.1.0^{}"), "\t", 2)[0]
	bAt := strings.SplitN(r.Git("ls-remote", "origin", "refs/tags/b@0.1.0^{}"), "\t", 2)[0]
	assert.Equal(t, source, aAt, "the pinned tag points at the exported commit on the remote too")
	assert.Equal(t, head, bAt)
}

// TestRecordsExportedCommitPinsTagOutsideCommitMode: without the release
// commit, tags normally land on HEAD; the PACKAGE_<KEY> export redirects the
// tag to the exported commit and no tag is created at HEAD for that package.
func TestRecordsExportedCommitPinsTagOutsideCommitMode(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{`echo "PACKAGE_CORE=$(git rev-parse HEAD~1)" >> "$DISPAT_OUTPUT"`}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first change")
	older := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("fix(core): second change")
	head := r.Git("rev-parse", "HEAD")

	r.ReleaseOK()

	assert.Equal(t, older, r.Git("rev-list", "-n1", "core@0.1.0"),
		"the tag lands on the exported commit, not on HEAD")
	assert.Empty(t, r.Git("tag", "--points-at", head),
		"no tag is created at HEAD for the pinned package")
	assert.Equal(t, head, r.Git("rev-parse", "HEAD"), "no commit mode: HEAD does not move")
}

// TestRecordsPushVerifyDisabled: commit.verify=false switches the upfront
// ls-remote check off, so the release work happens and only the push itself
// fails — where the default (verify on) fails fast before any work.
func TestRecordsPushVerifyDisabled(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true, Verify: models.Bool(false)}
	r.WriteConfigModel(cfg) // note: no remote configured at all
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): released, push fails later")

	res := r.Release()
	require.Equal(t, 1, res.Code, "the push itself still fails\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "E224"),
		"the push failure is reported under its own code, events:\n%s", res.Stdout)
	assert.True(t, r.HasTag("core@0.1.0"), "release work happened before the failing push; tags: %v", r.TagList())
	assert.Equal(t, "chore(release): core@0.1.0", r.Git("log", "-1", "--format=%s"),
		"the release commit exists")

	// The contrast: with verify at its default the same misconfiguration
	// fails fast, before any release work at all.
	rd := harness.New(t)
	cfg = libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	rd.WriteConfigModel(cfg) // again no remote configured
	rd.SeedPackage("packages", "core")
	rd.Commit("feat(core): never released")

	res = rd.Release()
	require.Equal(t, 1, res.Code, "remote verification must fail the run\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rd.TagList(), "the default fails fast before any work")
	assert.NoFileExists(t, rd.Path("packages", "core", "CHANGELOG.md"),
		"no release work may run before verification passes")
}

// TestRecordsChangelogDisabled: changelog.enabled=false switches the file
// recorder off without touching anything else — the release still publishes
// and tags, and no changelog appears.
func TestRecordsChangelogDisabled(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): no changelog wanted")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tagging is independent of the changelog; tags: %v", r.TagList())
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"))
}

// TestRecordsCommitModeGithubFinalize: in release-commit mode the GitHub
// releases move to the finalize phase, created after the release commit
// exists so their body documents the exact commit and tag. The recorder
// stays opt-in per package (no DISPAT_EXPORT_GITHUB, no release), a
// PACKAGE_<KEY> export overrides both the documented commit and the
// target_commitish, and with push disabled no run-level SHA is sent as
// target_commitish. commit.messageFormat is exercised alongside: the release
// commit renders the {packages} and {tags} placeholders.
func TestRecordsCommitModeGithubFinalize(t *testing.T) {
	type ghRelease struct {
		TagName         string `json:"tag_name"`
		Body            string `json:"body"`
		TargetCommitish string `json:"target_commitish"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// Only a opts into a GitHub release; it also pins its record to the
	// commit its publish ran at (the source commit — the release commit does
	// not exist yet at publish time).
	cfg.Scripts["publish"] = models.Script{`if [ "$DISPAT_PACKAGE" = "a" ]; then` +
		` echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT";` +
		` echo "PACKAGE_A=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"; fi`}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true),
		MessageFormat: "chore(release): publish {packages} as {tags}"}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a,b): first release of both")
	source := r.Git("rev-parse", "HEAD")

	r.ReleaseOK()

	assert.Equal(t, "chore(release): publish a, b as a@0.1.0, b@0.1.0",
		r.Git("log", "-1", "--format=%s"), "messageFormat renders both placeholders")

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 1, "no export, no GitHub release: b must be skipped")
	assert.Equal(t, "a@0.1.0", releases[0].TagName)
	assert.Contains(t, releases[0].Body, "### Release")
	assert.Contains(t, releases[0].Body, "- commit: "+source,
		"the body documents the exported commit, not the release commit")
	assert.Contains(t, releases[0].Body, "- tag: a@0.1.0")
	assert.Equal(t, source, releases[0].TargetCommitish,
		"the exported hash is the target_commitish even without a push")
}

// TestRecordsGitHubAllPackages: with github.allPackages, every published
// package gets a release without exporting DISPAT_EXPORT_GITHUB — the export
// then only adds assets — while the default keeps the export as the opt-in.
func TestRecordsGitHubAllPackages(t *testing.T) {
	type ghRelease struct {
		TagName string `json:"tag_name"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {"echo building"}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: buildPublish()}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): bootstrap both")

	r.ReleaseOK()
	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 2, "one release per published package, no export needed")
	tags := []string{releases[0].TagName, releases[1].TagName}
	assert.ElementsMatch(t, []string{"core@0.1.0", "utils@0.1.0"}, tags)
}

// TestRecordsChannelsHoldPrereleasesBack: changelog.channels and
// github.channels naming the stable line alone hold the betas back. The beta
// is still planned, tagged and published — the flow is untouched — but it
// leaves no changelog entry and no GitHub release; the graduation to stable
// writes the one entry covering the whole window and creates the one release.
// A package naming both the stable line and every prerelease (utils here)
// records its beta as a package restricting nothing would.
func TestRecordsChannelsHoldPrereleasesBack(t *testing.T) {
	type ghRelease struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Channels: []string{"stable"}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true), Channels: []string{"stable"},
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	// utils opts back in, so the two policies are compared inside one run. A
	// nearer layer states the whole restriction, so opting back in is naming
	// the stable line and every prerelease channel together.
	cfg.Packages = map[string]models.PackageConfig{"utils": {
		Changelog: &models.ChangelogConfig{Channels: []string{"stable", "*"}},
		GitHub:    &models.GitHubConfig{Channels: []string{"stable", "*"}},
	}}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")

	// Run 1: a beta of both.
	r.Commit("feat(core,utils)%beta: first work")
	r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.0-beta.0"), "the beta is still tagged and published")
	assert.NoFileExists(t, r.Path("packages/core/CHANGELOG.md"),
		"a changelog recording on the stable line alone leaves the beta unrecorded")
	assert.FileExists(t, r.Path("packages/utils/CHANGELOG.md"),
		"a package that opted back in still records its beta")

	betas := decodeAll[ghRelease](t, bodies())
	require.Len(t, betas, 1, "only the opted-in package gets a prerelease release")
	assert.Equal(t, "utils@0.1.0-beta.0", betas[0].TagName)
	assert.True(t, betas[0].Prerelease)

	// Run 2: graduate both to stable.
	r.CommitEmpty("release(core,utils)%beta>stable: graduate")
	r.ReleaseOK()

	core, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(core), "## core@0.1.0 (", "the stable release writes the entry")
	assert.NotContains(t, string(core), "beta", "no beta entry was ever written")
	assert.Contains(t, string(core), "first work",
		"the stable entry covers the work the betas carried")

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 3, "the two stable releases join the one beta")
	assert.ElementsMatch(t,
		[]string{"utils@0.1.0-beta.0", "core@0.1.0", "utils@0.1.0"},
		[]string{releases[0].TagName, releases[1].TagName, releases[2].TagName})
}

// TestRecordsGitHubReleaseExistsIsASkip: a release the repository already
// carries is skipped (W224) instead of failing the run on the API's 422, so
// a re-run after a later failure — or a flow that published from `dispat
// github` earlier — converges instead of blocking.
func TestRecordsGitHubReleaseExistsIsASkip(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	first := r.Command("github", "--package", "core")
	require.Equal(t, 0, first.Code, "stderr: %s", first.Stderr)
	require.Len(t, bodies(), 1, "the first invocation creates the release")

	// The same plan again — a re-run after a later stage failed, or a run
	// following the flow that already published.
	second := r.Command("github", "--package", "core")
	assert.Equal(t, 0, second.Code, "stderr: %s", second.Stderr)
	assert.Len(t, bodies(), 1, "the existing release is never created twice")
	assert.True(t, harness.HasCode(second.Events, "W224"), "the skip says which code it is")

	// And the release that follows converges too, instead of failing on the
	// API's 422 for a duplicate tag.
	r.ReleaseOK()
	assert.Len(t, bodies(), 1)
	assert.True(t, r.HasTag("core@0.1.0"))
}

// recordLines is the unfiltered line list a bare string in a config file
// decodes into — the typed spelling of the shorthand.
func recordLines(text ...string) []models.EntryLine {
	return []models.EntryLine{{Line: text}}
}

// TestRecordsHeaderAndFooterPerEntry: the header and footer belong to the
// entry, not to the file. Two releases leave two of each, the file title is
// written once, and the blocks bracket the sections in order.
func TestRecordsHeaderAndFooterPerEntry(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		FileTitle: recordLines("# Changelog", "", "Everything that shipped."),
		EntryFormatConfig: models.EntryFormatConfig{
			Header: recordLines("Built by CI."),
			Footer: recordLines("", "---"),
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")

	r.Commit("feat(core): add streaming")
	r.ReleaseOK()
	r.CommitEmpty("fix(core): close a leak")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	log := string(data)

	assert.True(t, strings.HasPrefix(log, "# Changelog\n\nEverything that shipped.\n"), log)
	assert.Equal(t, 1, strings.Count(log, "Everything that shipped."), "the file title is written once")
	assert.Equal(t, 2, strings.Count(log, "Built by CI."), "one header per entry")
	assert.Equal(t, 2, strings.Count(log, "\n---\n"), "one footer per entry")

	// Inside the newest entry, in order.
	entry := log[strings.Index(log, "## core@0.1.1"):strings.Index(log, "## core@0.1.0")]
	assertOrderedIn(t, entry, "Built by CI.", "### Fixes", "- close a leak", "---")
}

// TestRecordsReleaseNameSubHeader: releaseName writes a sub-header naming the
// release under the entry's date line, and the entry stays recognisable, so a
// re-run still skips it.
func TestRecordsReleaseNameSubHeader(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assertOrderedIn(t, string(data), "## core@0.1.0 (", "### core 0.1.0", "### Features")

	// The entry is still found by its tag line, so a second write skips it.
	res := r.Release()
	require.Equal(t, 0, res.Code, res.Stderr)
	after, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, string(data), string(after), "nothing pending, nothing rewritten")
}

// TestRecordsLineFiltersSelectPackages: one configured list serves the whole
// workspace — package, space and group filters each write to their own
// packages and to no others.
func TestRecordsLineFiltersSelectPackages(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}, "publish": {"echo publishing"}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages/libs", Flow: buildPublish(), Versioning: "fixed"},
		"apps": {Path: "packages/apps", Flow: buildPublish()},
	}
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{
			Footer: []models.EntryLine{
				{Line: []string{"everyone"}},
				{Line: []string{"core only"}, Package: []string{"core"}},
				{Line: []string{"apps only"}, Space: []string{"apps"}},
				{Line: []string{"grouped"}, Group: []string{"libs"}},
			},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/libs", "core")
	r.SeedPackage("packages/libs", "utils")
	r.SeedPackage("packages/apps", "web")
	r.Commit("feat(core,utils,web): bootstrap")
	r.ReleaseOK()

	read := func(space, pkg string) string {
		data, err := os.ReadFile(r.Path("packages", space, pkg, "CHANGELOG.md"))
		require.NoError(t, err)
		return string(data)
	}
	core, utils, web := read("libs", "core"), read("libs", "utils"), read("apps", "web")

	for name, log := range map[string]string{"core": core, "utils": utils, "web": web} {
		assert.Contains(t, log, "everyone", "%s: an unfiltered line reaches every package", name)
	}
	assert.Contains(t, core, "core only")
	assert.NotContains(t, utils, "core only")
	assert.NotContains(t, web, "core only")

	assert.Contains(t, web, "apps only")
	assert.NotContains(t, core, "apps only")

	assert.Contains(t, core, "grouped", "libs versions as one group")
	assert.Contains(t, utils, "grouped")
	assert.NotContains(t, web, "grouped", "an independent space belongs to no group")
}

// TestRecordsTextExpandsVariables: the release's own variables, a script
// output and the process environment all resolve in record text, in the
// changelog file and in the GitHub release body alike. A name nothing
// defines leaves nothing behind.
func TestRecordsTextExpandsVariables(t *testing.T) {
	type ghRelease struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig("echo DISPAT_OUTPUT_IMAGE=acme/core:1 >> \"$DISPAT_OUTPUT\"", 1)
	format := models.EntryFormatConfig{
		ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
		Footer: recordLines(
			"tag: ${DISPAT_TAG}",
			"image: ${DISPAT_OUTPUT_IMAGE}",
			"built-by: ${DISPAT_IT_BUILDER}",
			"missing: [${NOTHING_DEFINES_THIS}]",
		),
	}
	cfg.Changelog = &models.ChangelogConfig{EntryFormatConfig: format}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: format,
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	t.Setenv("DISPAT_IT_BUILDER", "ci-runner")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	log := string(data)
	assert.Contains(t, log, "### core 0.1.0")
	assert.Contains(t, log, "tag: core@0.1.0")
	assert.Contains(t, log, "image: acme/core:1", "a value the build script exported")
	assert.Contains(t, log, "built-by: ci-runner", "and one from the environment")
	assert.Contains(t, log, "missing: []", "an undefined name expands to nothing")

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 1)
	assert.Equal(t, "core 0.1.0", releases[0].Name, "releaseName renames the release")
	assert.Contains(t, releases[0].Body, "tag: core@0.1.0")
	assert.Contains(t, releases[0].Body, "image: acme/core:1")
	assert.NotContains(t, releases[0].Body, "### core 0.1.0",
		"on GitHub the name is the release's title, not a sub-header in its body")
}

// TestRecordsGitHubBodyOrder: with commit mode on, the release section
// documenting the commit sits between the sections and the footer, and the
// tag is still the tag whatever the release is called.
func TestRecordsGitHubBodyOrder(t *testing.T) {
	type ghRelease struct {
		Name    string `json:"name"`
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Name: "dispat", Email: "dispat@example.com"}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: models.EntryFormatConfig{
			ReleaseName: "Winter release",
			Header:      recordLines("Built by CI."),
			Footer:      recordLines("Questions? open an issue."),
		},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 1)
	assert.Equal(t, "Winter release", releases[0].Name)
	assert.Equal(t, "core@0.1.0", releases[0].TagName, "the tag is never renamed")
	assertOrderedIn(t, releases[0].Body,
		"Built by CI.", "### Features", "- add streaming", "### Release", "- commit: ",
		"Questions? open an issue.")
}

// TestRecordsLineOverrideReplacesInherited: a package's own list states what
// that package writes; it does not extend the inherited one.
func TestRecordsLineOverrideReplacesInherited(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{Footer: recordLines("global footer")},
	}
	cfg.Packages = map[string]models.PackageConfig{"core": {
		Changelog: &models.ChangelogConfig{
			EntryFormatConfig: models.EntryFormatConfig{Footer: recordLines("core footer")},
		},
	}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core,utils): bootstrap")
	r.ReleaseOK()

	core, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	utils, err := os.ReadFile(r.Path("packages", "utils", "CHANGELOG.md"))
	require.NoError(t, err)

	assert.Contains(t, string(core), "core footer")
	assert.NotContains(t, string(core), "global footer", "the nearest layer states the whole list")
	assert.Contains(t, string(utils), "global footer", "and the rest still inherit it")
}

// TestRecordsLineWithoutTextIsAConfigError: a line that selects packages and
// writes nothing to them fails the load, naming where it sits.
func TestRecordsLineWithoutTextIsAConfigError(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"scripts": map[string]any{"build": echoBuild, "publish": "echo publishing"},
		"spaces": map[string]any{
			"libs": map[string]any{"path": "packages", "flow": map[string]any{
				"build": []any{"build"}, "publish": []any{"publish"}}},
		},
		"changelog": map[string]any{"footer": []any{map[string]any{"package": "core"}}},
	})
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming")

	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "a footer with nothing to write must not load")
	assert.Contains(t, res.Stderr, "footer[0]")
	assert.Contains(t, res.Stderr, "line is required")
}

// TestRecordsLineShorthandsInAPackageFolder: the shorthand shapes decode the
// same in an in-folder package config as in the root config.
func TestRecordsLineShorthandsInAPackageFolder(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/dispat.json", `{
  "changelog": {
    "fileTitle": "# Core",
    "footer": ["one", ["two", "three"], {"line": "four", "package": "core"}]
  }
}`)
	r.Commit("feat(core): add streaming")
	r.ReleaseOK()

	data, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	log := string(data)
	assert.True(t, strings.HasPrefix(log, "# Core\n"), log)
	assertOrderedIn(t, log, "### Features", "one", "two", "three", "four")
}

// TestRecordsAliasTags: the whole alias feature end to end — a package that
// publishes under its own path-prefixed tag and, beside it, the bare refs a
// consumer pins. The moving one follows the newest stable release; the exact
// one is written per release and never moves; a prerelease writes its own
// exact ref and leaves the moving one where it is.
func TestRecordsAliasTags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:      "packages",
		Flow:      buildPublish(),
		TagFormat: "packages/{name}/v{version}",
		AliasTags: []models.AliasTagConfig{
			{Format: "v{version}"},
			{Format: "v{major}", Moving: true, Channels: []string{"stable"}},
		},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.AddBareRemote()

	// Release 1: stable 0.1.0.
	r.Commit("feat(core): first release")
	r.ReleaseOK()
	require.True(t, r.HasTag("packages/core/v0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("v0.1.0"), "the exact alias, tags: %v", r.TagList())
	require.True(t, r.HasTag("v0"), "the moving alias, tags: %v", r.TagList())
	firstMajor := r.Git("rev-list", "-n1", "v0")

	// The aliases reach the remote too: a ref nobody can fetch is not a
	// pointer anyone can pin.
	remote := r.Git("ls-remote", "origin")
	assert.Contains(t, remote, "refs/tags/v0.1.0")
	assert.Contains(t, remote, "refs/tags/v0")

	// ...and they stay out of the release commit's subject. `{tags}` names
	// what this run released, and an alias is not a release: it is a moving
	// pointer at one. Listing them would make a release of a single package
	// read as three, and would change the subject's shape the day somebody
	// added an alias to the config. Asserted whole rather than by absence,
	// because the alias `v0.1.0` is a substring of the release tag.
	assert.Equal(t, "chore(release): packages/core/v0.1.0",
		r.Git("log", "-1", "--format=%s"), "the aliases are pushed, not announced")

	// Release 2: a prerelease writes its own exact ref and must not move the
	// major onto a release candidate.
	r.CommitEmpty("feat(core)%rc: a release candidate")
	r.ReleaseOK()
	require.True(t, r.HasTag("v0.2.0-rc.0"), "tags: %v", r.TagList())
	assert.Equal(t, firstMajor, r.Git("rev-list", "-n1", "v0"),
		"a prerelease leaves the moving alias where it is")

	// Release 3: the next stable moves the major and leaves every exact ref.
	r.CommitEmpty("feat(core)%rc>stable: graduate")
	r.ReleaseOK()
	require.True(t, r.HasTag("v0.2.0"), "tags: %v", r.TagList())
	assert.NotEqual(t, firstMajor, r.Git("rev-list", "-n1", "v0"), "the moving alias followed")
	assert.Equal(t, r.Git("rev-list", "-n1", "packages/core/v0.2.0"), r.Git("rev-list", "-n1", "v0"),
		"and points at the release it names")

	// And after three releases with aliases live, the baselines still read
	// correctly: an alias must never become the package's history.
	status := r.StatusOK()
	assert.NotContains(t, status.Stdout, "0.1.0 ->", "the baseline is the newest release, not an alias")
	assert.Contains(t, status.Stdout, `"version":"0.2.0"`, "status:\n%s", status.Stdout)
}

// --- The GitHub recorder driven through real channel transitions and a
// build's exported attachments. These sit with the other release records
// because they are the same artefact story: what a run leaves behind.

// githubConfig returns a config whose GitHub recorder points at the given
// fake API server, with the token read from DISPAT_IT_TOKEN. The publish
// script exports an empty DISPAT_EXPORT_GITHUB — the recorder acts only on
// packages that opted in.
func githubConfig(apiURL string) models.File {
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building"},
		"publish": {`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}}}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: apiURL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	return cfg
}

// TestRecordsGithubReleasePrereleaseFlagFollowsChannel exercises the GitHub
// release recorder through a real train and its graduation, end to end
// rather than in isolation: the same package's releases must flip
// `prerelease` true then false as its channel actually changes.
func TestRecordsGithubReleasePrereleaseFlagFollowsChannel(t *testing.T) {
	type ghRelease struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	srv, bodies := githubFake(t)

	r := harness.New(t)
	r.WriteConfigModel(githubConfig(srv.URL))
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core)%beta: start the train")

	r.ReleaseOK()
	r.CommitEmpty("release(core)%stable: graduate")
	r.ReleaseOK()

	releases := decodeAll[ghRelease](t, bodies())
	require.Len(t, releases, 2)
	assert.Equal(t, "core@0.1.0-beta.0", releases[0].TagName)
	assert.True(t, releases[0].Prerelease, "the beta release must be marked a prerelease")
	assert.Equal(t, "core@0.1.0", releases[1].TagName)
	assert.False(t, releases[1].Prerelease, "the graduated release must not be")
}

// TestRecordsGithubReleaseAttachments exercises the whole script-output and
// attachment path through the real binary: the build script exports
// DISPAT_EXPORT_GITHUB (two files) — opting the package into a GitHub
// release — plus an ordinary output into $DISPAT_OUTPUT, the publish and
// announce scripts must see them again (the export under its full name, the
// output as DISPAT_OUTPUT_*), and the created GitHub release must receive
// both files as assets at the endpoint the release itself advertised
// (upload_url).
func TestRecordsGithubReleaseAttachments(t *testing.T) {
	type upload struct {
		name, body string
	}
	var mu sync.Mutex
	var uploads []upload
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		switch {
		case req.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case req.URL.Path == "/uploads":
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			mu.Lock()
			uploads = append(uploads, upload{name: req.URL.Query().Get("name"), body: string(body)})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default: // release creation: advertise this server as the asset endpoint
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
		}
	}))
	defer srv.Close()

	r := harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.Scripts = map[string]models.Script{
		"build": {`echo binary-bytes > app.bin && echo docs-bytes > docs.txt` +
			` && echo "DISPAT_EXPORT_GITHUB=$PWD/app.bin $PWD/docs.txt" >> "$DISPAT_OUTPUT"` +
			` && echo "BUILD_FLAVOUR=release" >> "$DISPAT_OUTPUT"`},
		"publish":  {`echo "publish: $DISPAT_OUTPUTS / $DISPAT_EXPORT_GITHUB" > ../../publish-env.txt`},
		"announce": {`echo "announce: $DISPAT_OUTPUT_BUILD_FLAVOUR" > ../../announce-env.txt`},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"}, Announce: []string{"announce"}}}}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release with artefacts")

	r.ReleaseOK()

	// The ordinary output reached the later stages as DISPAT_OUTPUT_*; the
	// GitHub export travelled under its full name and stayed out of the
	// DISPAT_OUTPUTS listing.
	pubEnv, err := os.ReadFile(r.Path("publish-env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(pubEnv), "publish: BUILD_FLAVOUR / ")
	assert.Contains(t, string(pubEnv), "/app.bin")
	assert.Contains(t, string(pubEnv), "/docs.txt")
	annEnv, err := os.ReadFile(r.Path("announce-env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(annEnv), "announce: release")

	// Both files landed on the release as assets, named after their files.
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, uploads, 2, "uploads: %v", uploads)
	byName := map[string]string{}
	for _, u := range uploads {
		byName[u.name] = u.body
	}
	assert.Equal(t, "binary-bytes\n", byName["app.bin"])
	assert.Equal(t, "docs-bytes\n", byName["docs.txt"])
}

// The failures after the point of no return.
//
// Each of these happens when a package is already published to its registry:
// the artefact is out and nothing dispat does afterwards can take it back. So
// none of them fails a package or stops the run. Each is recorded under its
// own code, the run finishes what else it owed, and the exit code says
// something went wrong. E220 and E221, the two tagging failures, are covered
// above; these are the remaining three, plus the alias-tag warning that
// deliberately is *not* one of them.

// TestRecordsCommitFailureStillTags: the release commit fails after every
// package published. Tagging still follows, because the tags then point where
// they would have pointed had there been no release commit to make, and a
// released package with no tag is the one outcome the next run cannot
// recover from — it would read the package as never released and publish the
// same version again.
//
// The publish script takes git's index lock, which is exactly what a
// concurrent git process in the same checkout would do.
func TestRecordsCommitFailureStillTags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{"touch ../../.git/index.lock"}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): published, then the commit cannot be made")

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "a release missing its commit must not exit green\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "E223"),
		"the commit failure is reported under its own code, events:\n%s", res.Stdout)

	// Published, not failed: the artefact is out.
	assert.Contains(t, res.Stdout, `"status":"published"`)
	assert.NotContains(t, res.Stdout, `"status":"failed"`)

	// And the tag was still written, which is the whole reason tagging follows
	// a failed commit instead of being abandoned with it.
	assert.True(t, r.HasTag("core@0.1.0"),
		"the tag must survive the commit failure; tags: %v", r.TagList())
}

// TestRecordsChangelogFailureIsCriticalNotFailure: a release record that
// cannot be written after the package published. The changelog path is a
// directory, so the write fails for a reason no retry inside the run could
// fix.
//
// What this pins is the split: the package stays published, the run carries
// on, the failure is reported as E222, and the exit code is non-zero. The
// release tag — the record that actually decides what the next run sees —
// is written regardless, because a missing changelog entry is a thing to go
// and add, not a reason to re-publish.
func TestRecordsChangelogFailureIsCriticalNotFailure(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): both release, core cannot record")
	// A directory where the changelog file belongs: the recorder's read and
	// write both fail on it, and neither is a missing file.
	require.NoError(t, os.Mkdir(r.Path("packages", "core", "CHANGELOG.md"), 0o755))

	res := r.Release()
	require.NotEqual(t, 0, res.Code, "a release missing a record must not exit green\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.HasCode(res.Events, "E222"),
		"the record failure is reported under its own code, events:\n%s", res.Stdout)

	assert.Contains(t, res.Stdout, `"status":"published"`)
	assert.NotContains(t, res.Stdout, `"status":"failed"`,
		"a package whose changelog failed is still published")
	assert.True(t, r.HasTag("core@0.1.0"),
		"the tag is written whatever the changelog did; tags: %v", r.TagList())

	// The consumer is untouched by its provider's recording failure: the
	// provider published, so there was nothing to hold app back.
	assert.True(t, r.HasTag("app@0.1.0"), "tags: %v", r.TagList())
	assert.FileExists(t, r.Path("packages", "app", "CHANGELOG.md"),
		"one package's failed record does not skip the next one's")
}

// TestRecordsAliasTagFailureIsOnlyAWarning: an alias that cannot be written
// is a warning (W232), not a critical. The distinction is the point: an alias
// is a convenience ref rather than the record of a release, so losing one is
// something to re-point by hand or on the next release, and the run exits
// green.
//
// Constructed with force off — so the alias may not overwrite anything — and
// a ref already sitting on the alias name.
func TestRecordsAliasTagFailureIsOnlyAWarning(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Force: models.Bool(false)}
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path:      "packages",
		Flow:      buildPublish(),
		AliasTags: []models.AliasTagConfig{{Format: "v{version}"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): the alias name is already taken")
	// Somebody's tag already holds the name the alias wants. With force off
	// the write is refused rather than overwriting it.
	r.Git("tag", "-a", "v0.1.0", "-m", "not dispat's")

	res := r.ReleaseOK()

	assert.True(t, harness.HasCode(res.Events, "W232"),
		"the alias failure is reported under its own code, events:\n%s", res.Stdout)
	// Green: the release itself is complete.
	assert.True(t, r.HasTag("core@0.1.0"),
		"the release tag is unaffected by the alias; tags: %v", r.TagList())
	assert.Equal(t, "not dispat's",
		r.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/v0.1.0"),
		"the existing ref is left exactly where it was")
	assert.NotContains(t, res.Stdout, `"critical":1`,
		"an alias is not a record, so its loss is not a critical")
}
