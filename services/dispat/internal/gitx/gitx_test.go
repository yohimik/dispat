package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
)

// initRepo creates a git repo with one committed package file.
func initRepo(t *testing.T) (string, *CLI) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "main.txt"), []byte("original"), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("add", ".")
	git("commit", "-qm", "feat(core): initial")
	return root, &CLI{Dir: root}
}

// tagsOf lists a package's tags, which is the single query planning makes; the
// baselines are selections over the result.
func tagsOf(t *testing.T, cli *CLI, ctx context.Context, pkg string, format TagFormat) Tags {
	t.Helper()
	tags, err := cli.Tags(ctx, pkg, format)
	require.NoError(t, err)
	return tags
}

func TestRevertDir(t *testing.T) {
	root, cli := initRepo(t)
	pkg := filepath.Join(root, "packages", "core")

	// Dirty the folder: modify a tracked file, add untracked file and folder.
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "main.txt"), []byte("dirty"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "generated.txt"), []byte("junk"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "dist", "out.js"), []byte("junk"), 0o644))

	require.NoError(t, cli.RevertDir(context.Background(), pkg))

	data, err := os.ReadFile(filepath.Join(pkg, "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "original", string(data), "tracked file restored")
	assert.NoFileExists(t, filepath.Join(pkg, "generated.txt"), "untracked file removed")
	assert.NoDirExists(t, filepath.Join(pkg, "dist"), "untracked folder removed")
}

func TestRevertDirLeavesSiblingsAlone(t *testing.T) {
	root, cli := initRepo(t)
	other := filepath.Join(root, "packages", "other")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "keep.txt"), []byte("keep"), 0o644))

	require.NoError(t, cli.RevertDir(context.Background(), filepath.Join(root, "packages", "core")))
	assert.FileExists(t, filepath.Join(other, "keep.txt"), "revert must be scoped to the package folder")
}

// tagAt creates an annotated tag with an explicit creation date, so
// creatordate ordering in tests is deterministic.
func tagAt(t *testing.T, root, name, date string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "tag", "-a", name, "-m", name)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git tag %s: %s", name, out)
}

func TestTagRoundTrip(t *testing.T) {
	root, cli := initRepo(t)
	_ = root
	ctx := context.Background()

	_, found := tagsOf(t, cli, ctx, "core", "").Baseline()
	assert.False(t, found, "no tags yet")

	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0", ""))
	require.NoError(t, cli.CreateTag(ctx, "core@0.2.0", "release core@0.2.0", ""))
	require.NoError(t, cli.CreateTag(ctx, "core-utils@9.9.9", "other package", ""))

	tag, found := tagsOf(t, cli, ctx, "core", "").Baseline()
	require.True(t, found)
	assert.True(t, tag.Parsed)
	assert.Equal(t, "core@0.2.0", tag.Name)
	assert.Equal(t, "0.2.0", tag.Version.String())
}

func TestBaselineUnparseableNewest(t *testing.T) {
	// The newest tag by creation date is unparseable: the baseline must be
	// reported with Parsed=false instead of falling back to the older 0.0.0.
	root, cli := initRepo(t)
	ctx := context.Background()

	tagAt(t, root, "core@0.0.0", "2026-01-01T10:00:00")
	tagAt(t, root, "core@0.0.1.0", "2026-01-02T10:00:00")

	tag, found := tagsOf(t, cli, ctx, "core", "").Baseline()
	require.True(t, found)
	assert.False(t, tag.Parsed)
	assert.Equal(t, "core@0.0.1.0", tag.Name)
}

func TestBaselineMaxSemverWhenNewestParses(t *testing.T) {
	// An old unparseable tag does not disturb normal resolution, and the
	// highest parseable version wins even if created before a lower one
	// (backport scenario).
	root, cli := initRepo(t)
	ctx := context.Background()

	tagAt(t, root, "core@0.9.0-rc1", "2026-01-01T10:00:00") // old junk
	tagAt(t, root, "core@2.0.0", "2026-01-02T10:00:00")
	tagAt(t, root, "core@1.2.1", "2026-01-03T10:00:00") // backport, newest by date

	tag, found := tagsOf(t, cli, ctx, "core", "").Baseline()
	require.True(t, found)
	assert.True(t, tag.Parsed)
	assert.Equal(t, "core@2.0.0", tag.Name, "highest parseable version wins")
	assert.Equal(t, "2.0.0", tag.Version.String())
}

func TestStableBaselineSkipsPrereleases(t *testing.T) {
	// The pending window is measured from the last *stable* tag (§13.3), so a
	// package on a prerelease train must still report the stable release the
	// train started from — that is what §11.4 recomputes the target from.
	root, cli := initRepo(t)
	ctx := context.Background()

	tagAt(t, root, "core@1.2.3", "2026-01-01T10:00:00")
	tagAt(t, root, "core@1.3.0-beta.0", "2026-01-02T10:00:00")
	tagAt(t, root, "core@1.3.0-beta.1", "2026-01-03T10:00:00")

	// One listing, both baselines — which is exactly how the planner uses it.
	tags := tagsOf(t, cli, ctx, "core", "")

	latest, found := tags.Baseline()
	require.True(t, found)
	assert.Equal(t, "core@1.3.0-beta.1", latest.Name, "baseline includes prereleases")

	stable, found := tags.StableBaseline()
	require.True(t, found)
	assert.Equal(t, "core@1.2.3", stable.Name, "the stable baseline is the last release with no prerelease")
	assert.NotEmpty(t, stable.Commit, "annotated tags must be peeled to their commit")
}

func TestStableBaselineAbsent(t *testing.T) {
	// A package that has only ever shipped prereleases has no stable
	// baseline, so its window is the whole history.
	root, cli := initRepo(t)
	tagAt(t, root, "core@0.1.0-beta.0", "2026-01-01T10:00:00")

	_, found := tagsOf(t, cli, context.Background(), "core", "").StableBaseline()
	assert.False(t, found)
}

func TestTagCommitIsPeeled(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release", ""))

	tag, found := tagsOf(t, cli, ctx, "core", "").Baseline()
	require.True(t, found)

	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	assert.Equal(t, head, tag.Commit,
		"an annotated tag's Commit must be the commit, not the tag object")
	_ = root
}

func TestTagFormatRenderParseGlob(t *testing.T) {
	for _, tc := range []struct {
		format TagFormat
		pkg    string
		tag    string
		glob   string
	}{
		{"", "core", "core@1.2.3", "core@*"},
		{"{name}@{version}", "core", "core@1.2.3", "core@*"},
		{"{name}@v{version}", "core", "core@v1.2.3", "core@v*"},
		{"services/{name}@v{version}", "core", "services/core@v1.2.3", "services/core@v*"},
		{"v{version}", "core", "v1.2.3", "v*"},
		{"{name}@{version}", "@acme/ui", "@acme/ui@1.2.3", "@acme/ui@*"},
	} {
		v := ccme.Version{Major: 1, Minor: 2, Patch: 3}
		assert.Equal(t, tc.tag, tc.format.Render(tc.pkg, v), "render %q", tc.format)
		assert.Equal(t, tc.glob, tc.format.Glob(tc.pkg), "glob %q", tc.format)

		got, ok := tc.format.ParseVersion(tc.pkg, tc.tag)
		require.True(t, ok, "parse %q from %q", tc.tag, tc.format)
		assert.Equal(t, v.String(), got.String(), "round trip %q", tc.format)
	}
}

func TestTagFormatRejectsForeignTags(t *testing.T) {
	f := TagFormat("{name}@v{version}")

	// A plain "core@1.2.3" does not carry the "v", so it belongs to a
	// different convention and must not be read as this package's baseline.
	_, ok := f.ParseVersion("core", "core@1.2.3")
	assert.False(t, ok, "a tag missing the format's literal text is not ours")

	// A tag for a different package never matches the shape at all.
	assert.False(t, f.Matches("core", "other@v1.2.3"))

	// Matching the shape and carrying a readable version are separate
	// questions: this is the tag that puts a package on the initials fallback
	// rather than out of the listing entirely.
	assert.True(t, f.Matches("core", "core@v0.0.1.0"), "the shape matches")
	_, ok = f.ParseVersion("core", "core@v0.0.1.0")
	assert.False(t, ok, "but the version does not parse")
}

func TestTagFormatValidate(t *testing.T) {
	for _, ok := range []TagFormat{
		"{name}@{version}",
		"{name}@v{version}",
		"services/{name}@v{version}",
		"v{version}",
	} {
		assert.NoError(t, ok.Validate(), "%q", ok)
	}

	for _, bad := range []struct {
		format TagFormat
		why    string
	}{
		{"{name}", "no version placeholder"},
		{"{version}-{version}", "ambiguous"},
		// Everything below renders a name git refuses. Catching these at load
		// time is the point: they would otherwise fail after publishing.
		{"/services/{name}@v{version}", "leading slash"},
		{"{name}/{version}/", "trailing slash"},
		{"-{name}@{version}", "leading dash"},
		{"{name}//{version}", "double slash"},
		{"{name}..{version}", "double dot"},
		{"{name} {version}", "whitespace"},
		{"{name}:{version}", "colon"},
		{"{name}@{version}.lock", "reserved suffix"},
	} {
		assert.Error(t, bad.format.Validate(), "%q (%s)", bad.format, bad.why)
	}
}

func TestTagFormatValidateAgreesWithGit(t *testing.T) {
	// The rules above are a subset of git's, so anything Validate accepts must
	// actually be creatable. Checking against the real binary is what keeps
	// the subset honest.
	_, cli := initRepo(t)
	ctx := context.Background()

	for _, f := range []TagFormat{
		"{name}@{version}",
		"{name}@v{version}",
		"services/{name}@v{version}",
		"v{version}",
	} {
		require.NoError(t, f.Validate(), "%q", f)
		name := f.Render("core", ccme.Version{Major: 1, Minor: 2, Patch: 3})
		assert.NoError(t, cli.CreateTag(ctx, name, "release "+name, ""),
			"git must accept %q, which Validate allowed", name)
	}
}

func TestBaselineUnderCustomFormat(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	f := TagFormat("services/{name}@v{version}")

	tagAt(t, root, "services/core@v1.2.3", "2026-01-01T10:00:00")
	tagAt(t, root, "services/core@v1.3.0", "2026-01-02T10:00:00")
	tagAt(t, root, "core@9.9.9", "2026-01-03T10:00:00") // another convention

	tag, found := tagsOf(t, cli, ctx, "core", f).Baseline()
	require.True(t, found)
	assert.Equal(t, "services/core@v1.3.0", tag.Name)
	assert.Equal(t, "1.3.0", tag.Version.String(),
		"the literal 'v' is format, not part of the version")
}

func TestUnparseableTagUnderCustomFormat(t *testing.T) {
	// The initials fallback works the same whatever the format: the tag
	// matches the shape, its version does not parse, so the baseline comes
	// from elsewhere while the window is still measured from the tag.
	root, cli := initRepo(t)
	f := TagFormat("{name}@v{version}")

	tagAt(t, root, "core@v1.0.0", "2026-01-01T10:00:00")
	tagAt(t, root, "core@v0.0.1.0", "2026-01-02T10:00:00")

	tag, found := tagsOf(t, cli, context.Background(), "core", f).Baseline()
	require.True(t, found)
	assert.False(t, tag.Parsed, "an unparseable newest tag poisons the older ones too")
	assert.Equal(t, "core@v0.0.1.0", tag.Name)
}

func TestIsAncestor(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()

	first, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "second.txt"), []byte("x"), 0o644))
	_, err = cli.CommitDirs(ctx, []string{pkg}, "fix(core): second")
	require.NoError(t, err)
	second, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	for _, tc := range []struct {
		a, b string
		want bool
		why  string
	}{
		{first, second, true, "an earlier commit is an ancestor"},
		{second, first, false, "and the relation is not symmetric"},
		{first, first, true, "ancestor-or-self includes self"},
	} {
		got, aerr := cli.IsAncestor(ctx, tc.a, tc.b)
		require.NoError(t, aerr, tc.why)
		assert.Equal(t, tc.want, got, tc.why)
	}

	// A commit created after the ancestry DAG was first loaded is answered
	// through the per-question git fallback, not the stale cache.
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "third.txt"), []byte("y"), 0o644))
	_, err = cli.CommitDirs(ctx, []string{pkg}, "fix(core): third")
	require.NoError(t, err)
	third, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	ok, err := cli.IsAncestor(ctx, second, third)
	require.NoError(t, err)
	assert.True(t, ok, "the fallback answers for commits the cached DAG has never seen")
}

func TestCommitsCarrySHAsParentsAndFullMessages(t *testing.T) {
	// A CCME message is *expected* to contain blank lines — one after the
	// header is required, and the footer block is preceded by another — so a
	// log parser that stops at the first blank line truncates almost every
	// well-formed message and reads its body as file paths.
	root, cli := initRepo(t)
	ctx := context.Background()

	first, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "second.txt"), []byte("x"), 0o644))
	body := "feat(core)^: streaming\n\nA body paragraph explaining the change.\n\nPropagate-Depth: 2\nBREAKING CHANGE: the old API is gone\n"
	_, err = cli.CommitDirs(ctx, []string{pkg}, body)
	require.NoError(t, err)

	commits, err := cli.Commits(ctx, "")
	require.NoError(t, err)
	require.Len(t, commits, 2, "newest first")

	newest := commits[0]
	assert.NotEmpty(t, newest.SHA)
	assert.Equal(t, []string{first}, newest.Parents)
	assert.Contains(t, newest.Message, "A body paragraph")
	assert.Contains(t, newest.Message, "Propagate-Depth: 2")
	assert.Contains(t, newest.Message, "BREAKING CHANGE: the old API is gone")
	assert.Equal(t, []string{"packages/core/second.txt"}, newest.Files,
		"the body must not leak into the changed-file list")

	assert.Empty(t, commits[1].Parents, "the root commit has no parent")
	assert.Equal(t, "Test", newest.AuthorName, "the git author travels with the commit")
	assert.Equal(t, "test@example.com", newest.AuthorEmail)
}

// commitAs commits every change in root under a named identity. The repository
// identity is fixed by initRepo, and the author fields are only interesting
// when more than one person has written something.
func commitAs(t *testing.T, root, name, email, msg string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("add", "-A")
	run("-c", "user.name="+name, "-c", "user.email="+email, "commit", "-qm", msg)
}

func TestCommitsCarryTheAuthorIdentity(t *testing.T) {
	// The author fields feed release-record attribution, so the parse has to
	// survive the shapes a real history contains: a name that is not ASCII, a
	// merge (whose author is the person who merged, not either side), and a
	// message whose text could be mistaken for another field.
	root, cli := initRepo(t)
	ctx := context.Background()
	pkg := filepath.Join(root, "packages", "core")

	require.NoError(t, os.WriteFile(filepath.Join(pkg, "unicode.txt"), []byte("x"), 0o644))
	commitAs(t, root, "Ada Lovelace", "ada@example.com", "feat(core): analytic engine")

	require.NoError(t, os.WriteFile(filepath.Join(pkg, "accented.txt"), []byte("y"), 0o644))
	commitAs(t, root, "Zoé Müller-O'Brien", "zoe@example.com", "fix(core): accented name")

	commits, err := cli.Commits(ctx, "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(commits), 3)

	assert.Equal(t, "Zoé Müller-O'Brien", commits[0].AuthorName,
		"a non-ASCII name survives the log format")
	assert.Equal(t, "zoe@example.com", commits[0].AuthorEmail)
	assert.Equal(t, "Ada Lovelace", commits[1].AuthorName)
	assert.Equal(t, "ada@example.com", commits[1].AuthorEmail)
	assert.Equal(t, []string{"packages/core/accented.txt"}, commits[0].Files,
		"the author fields must not shift the file list")
}

func TestCommitsAuthorSurvivesSeparatorLikeMessages(t *testing.T) {
	// The record is split on a fixed number of fields, so a message that looks
	// like it contains more of them is the shape that would break the parse.
	// The separators are control characters git will not emit from a message,
	// but the text around them is exactly what a careless width would trip on.
	root, cli := initRepo(t)
	ctx := context.Background()
	pkg := filepath.Join(root, "packages", "core")

	require.NoError(t, os.WriteFile(filepath.Join(pkg, "tricky.txt"), []byte("x"), 0o644))
	msg := "feat(core): a message with an email <nobody@example.com> and paths\n\n" +
		"packages/core/not-a-file.txt\nAnother Person <other@example.com>\n"
	commitAs(t, root, "Real Author", "real@example.com", msg)

	commits, err := cli.Commits(ctx, "")
	require.NoError(t, err)
	require.NotEmpty(t, commits)

	newest := commits[0]
	assert.Equal(t, "Real Author", newest.AuthorName,
		"the message text must not be read as the author")
	assert.Equal(t, "real@example.com", newest.AuthorEmail)
	assert.Contains(t, newest.Message, "Another Person <other@example.com>")
	assert.Equal(t, []string{"packages/core/tricky.txt"}, newest.Files,
		"a path-shaped line inside the message is not a changed file")
}

func TestCommitsAuthorOfAMergeCommit(t *testing.T) {
	// A merge commit's author is whoever made the merge. It has two parents
	// and, with --diff-merges=first-parent, a file list of its own, so it is
	// the record whose field layout differs most from an ordinary one.
	root, cli := initRepo(t)
	ctx := context.Background()
	pkg := filepath.Join(root, "packages", "core")

	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	base, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	git("checkout", "-q", "-b", "side")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "side.txt"), []byte("s"), 0o644))
	commitAs(t, root, "Side Worker", "side@example.com", "feat(core): side work")

	git("checkout", "-q", "-")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "main.txt"), []byte("m"), 0o644))
	commitAs(t, root, "Main Worker", "main@example.com", "fix(core): main work")

	git("-c", "user.name=Merge Bot", "-c", "user.email=merge@example.com",
		"merge", "-q", "--no-ff", "-m", "chore(core): merge side", "side")

	commits, err := cli.Commits(ctx, base)
	require.NoError(t, err)
	require.NotEmpty(t, commits)

	merge := commits[0]
	require.Len(t, merge.Parents, 2, "the newest commit is the merge")
	assert.Equal(t, "Merge Bot", merge.AuthorName,
		"a merge is authored by whoever merged, not by either side")
	assert.Equal(t, "merge@example.com", merge.AuthorEmail)

	byEmail := map[string]string{}
	for _, c := range commits {
		byEmail[c.AuthorEmail] = c.AuthorName
	}
	assert.Equal(t, "Side Worker", byEmail["side@example.com"])
	assert.Equal(t, "Main Worker", byEmail["main@example.com"])
}

func TestHeadSHA(t *testing.T) {
	root, cli := initRepo(t)
	sha, err := cli.HeadSHA(context.Background())
	require.NoError(t, err)
	require.Len(t, sha, 40, "full commit SHA")

	out, gerr := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, gerr)
	assert.Equal(t, string(bytes.TrimSpace(out)), sha)
}

// addBareRemote creates a bare repository and registers it as "origin".
func addBareRemote(t *testing.T, root string) string {
	t.Helper()
	bare := t.TempDir()
	out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)
	out, err = exec.Command("git", "-C", root, "remote", "add", "origin", bare).CombinedOutput()
	require.NoError(t, err, "git remote add: %s", out)
	return bare
}

func TestCommitDirs(t *testing.T) {
	root, cli := initRepo(t)
	pkg := filepath.Join(root, "packages", "core")
	ctx := context.Background()

	// Nothing staged: no commit is created.
	committed, err := cli.CommitDirs(ctx, []string{pkg}, "chore(release): noop")
	require.NoError(t, err)
	assert.False(t, committed)

	// Changelog-like change inside the package.
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644))
	committed, err = cli.CommitDirs(ctx, []string{pkg}, "chore(release): core@0.2.0")
	require.NoError(t, err)
	assert.True(t, committed)

	out, gerr := exec.Command("git", "-C", root, "log", "--format=%s").Output()
	require.NoError(t, gerr)
	subjects := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Len(t, subjects, 2)
	assert.Equal(t, "chore(release): core@0.2.0", subjects[0], "newest commit first")
}

func TestVerifyRemoteAndPush(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()

	require.Error(t, cli.VerifyRemote(ctx, "origin"), "no remote configured yet")

	bare := addBareRemote(t, root)
	require.NoError(t, cli.VerifyRemote(ctx, "origin"))

	// Commit a change, tag it, push branch + tags.
	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644))
	committed, err := cli.CommitDirs(ctx, []string{pkg}, "chore(release): core@0.1.0")
	require.NoError(t, err)
	require.True(t, committed)
	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0", ""))
	report, err := cli.Push(ctx, "origin", []string{"core@0.1.0"}, false)
	require.NoError(t, err)
	assert.Empty(t, report.Skipped, "a fresh tag is pushed, not skipped")

	out, err := exec.Command("git", "-C", bare, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "core@0.1.0", "tag must arrive on the remote")
	out, err = exec.Command("git", "-C", bare, "log", "--format=%s", "--all").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "chore(release): core@0.1.0", "commit must arrive on the remote")
}

func TestPushSkipsTagsAlreadyOnTheRemote(t *testing.T) {
	// A partially pushed release means some tags already exist on the remote;
	// a later push must skip exactly those and still deliver the rest, rather
	// than dying on "tag already exists".
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0", ""))
	report, err := cli.Push(ctx, "origin", []string{"core@0.1.0"}, false)
	require.NoError(t, err)
	require.Empty(t, report.Skipped)

	// RemoteTags sees the pushed tag under its plain name (annotated tags
	// list twice on the wire; the peeled duplicate must not leak through).
	remoteTags, err := cli.RemoteTags(ctx, "origin")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"core@0.1.0": true}, remoteTags)

	// A second run's push carries one existing and one new tag.
	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644))
	committed, err := cli.CommitDirs(ctx, []string{pkg}, "chore(release): core@0.2.0")
	require.NoError(t, err)
	require.True(t, committed)
	require.NoError(t, cli.CreateTag(ctx, "core@0.2.0", "release core@0.2.0", ""))

	report, err = cli.Push(ctx, "origin", []string{"core@0.1.0", "core@0.2.0"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"core@0.1.0"}, report.Skipped, "the existing tag is skipped, not an error")
	assert.Empty(t, report.Replaced, "nothing is overwritten without force")

	out, err := exec.Command("git", "-C", bare, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "core@0.2.0", "the new tag still arrives")
}

// TestPushForceReplacesTagsOnTheRemote: with force on, a tag the remote
// already carries is overwritten and reported, rather than skipped forever —
// which is the only way a moving tag can ever move. The branch is still
// pushed without force under the same setting: a rejected branch push means
// someone else pushed, and the answer to that is never to overwrite them.
func TestPushForceReplacesTagsOnTheRemote(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "v1", "the 1.x line", ""))
	report, err := cli.Push(ctx, "origin", []string{"v1"}, true)
	require.NoError(t, err)
	require.Empty(t, report.Replaced, "nothing to replace the first time")
	first := remoteTagCommit(t, bare, "v1")

	// A later release moves the tag locally, then pushes it again.
	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644))
	committed, err := cli.CommitDirs(ctx, []string{pkg}, "chore(release): more")
	require.NoError(t, err)
	require.True(t, committed)
	require.NoError(t, cli.CreateTagForce(ctx, "v1", "the 1.x line", ""))

	report, err = cli.Push(ctx, "origin", []string{"v1"}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1"}, report.Replaced, "the overwrite is reported, not silent")
	assert.Empty(t, report.Skipped, "force skips nothing")

	moved := remoteTagCommit(t, bare, "v1")
	assert.NotEqual(t, first, moved, "the remote tag now points at the new commit")
}

// TestPushNeverForcesTheBranch: the tag refs are dispat's own namespace and
// may be replaced; the branch carries other people's commits and may not.
func TestPushNeverForcesTheBranch(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	_, err := cli.Push(ctx, "origin", nil, true)
	require.NoError(t, err)

	// A commit lands on the remote that this clone does not have, so a
	// fast-forward is impossible. Even with force asked for, the branch push
	// must be refused rather than overwrite it.
	other := t.TempDir()
	runGit(t, other, "clone", bare, ".")
	runGit(t, other, "config", "user.email", "other@example.com")
	runGit(t, other, "config", "user.name", "Other")
	require.NoError(t, os.WriteFile(filepath.Join(other, "theirs.txt"), []byte("theirs\n"), 0o644))
	runGit(t, other, "add", ".")
	runGit(t, other, "commit", "-m", "someone else's work")
	runGit(t, other, "push", "origin", "HEAD")
	theirs := strings.TrimSpace(runGit(t, other, "rev-parse", "HEAD"))

	require.NoError(t, os.WriteFile(filepath.Join(root, "mine.txt"), []byte("mine\n"), 0o644))
	_, err = cli.CommitDirs(ctx, []string{root}, "chore: mine")
	require.NoError(t, err)

	_, err = cli.Push(ctx, "origin", nil, true)
	require.Error(t, err, "a diverged branch push is refused even with force on")

	out, err := exec.Command("git", "-C", bare, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	assert.Equal(t, theirs, strings.TrimSpace(string(out)), "their commit is still the remote tip")
}

// TestPushTagCarriesOneRefAndNeverForces: the ref-level primitive the release
// lock is built on. It delivers exactly one tag, moves no branch, and a name
// the remote already holds at another object is a rejection — which is what
// makes it usable as a mutex.
func TestPushTagCarriesOneRefAndNeverForces(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "dispat-release-lock", "held by us", ""))
	require.NoError(t, cli.PushTag(ctx, "origin", "dispat-release-lock"))

	assert.Contains(t, runGit(t, bare, "tag"), "dispat-release-lock")
	assert.Empty(t, strings.TrimSpace(runGit(t, bare, "branch", "--list")),
		"the tag travels on its own: no branch moves with it")

	// Somebody else's tag of the same name, at a different object. A second
	// push of ours has to bounce off it rather than replace it.
	theirs := t.TempDir()
	runGit(t, theirs, "clone", "-q", bare, ".")
	runGit(t, theirs, "config", "user.email", "other@example.com")
	runGit(t, theirs, "config", "user.name", "Other")
	runGit(t, theirs, "tag", "-f", "-a", "dispat-release-lock", "-m", "held by them",
		"dispat-release-lock^{commit}") // the clone has the tag but no branch: name the commit

	runGit(t, theirs, "push", "--force", "origin", "refs/tags/dispat-release-lock")
	held := remoteTagObject(t, bare, "dispat-release-lock")

	require.NoError(t, cli.CreateTagForce(ctx, "dispat-release-lock", "held by us, again", ""))
	require.Error(t, cli.PushTag(ctx, "origin", "dispat-release-lock"),
		"a name the remote already holds is a rejection, not an overwrite")
	assert.Equal(t, held, remoteTagObject(t, bare, "dispat-release-lock"),
		"their tag is untouched")
}

// TestDeleteTagLocalAndRemote: both halves of the cleanup, including what
// happens when there is nothing to delete — the case a caller tidying up after
// a half-finished run walks into.
func TestDeleteTagLocalAndRemote(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "dispat-release-lock", "held", ""))
	require.NoError(t, cli.PushTag(ctx, "origin", "dispat-release-lock"))

	require.NoError(t, cli.DeleteRemoteTag(ctx, "origin", "dispat-release-lock"))
	assert.NotContains(t, runGit(t, bare, "tag"), "dispat-release-lock")
	require.NoError(t, cli.DeleteTag(ctx, "dispat-release-lock"))
	assert.NotContains(t, runGit(t, root, "tag"), "dispat-release-lock")

	// The two halves differ on the second attempt, and callers cleaning up
	// after a half-finished run depend on knowing which: the local delete
	// fails on a tag that is not there, the remote one does not.
	assert.Error(t, cli.DeleteTag(ctx, "dispat-release-lock"))
	assert.NoError(t, cli.DeleteRemoteTag(ctx, "origin", "dispat-release-lock"),
		"a fully qualified refspec makes the remote delete idempotent")
}

// TestTagsIgnoreTheReleaseLock: the lock tag sits on HEAD for the whole of
// the run that is doing the planning, so a format broad enough to match it
// would read it as the package's newest release and see no pending commits at
// all. The name is reserved instead of relying on the format to be narrow.
func TestTagsIgnoreTheReleaseLock(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()

	require.NoError(t, cli.CreateTag(ctx, "0.1.0", "release 0.1.0", ""))
	require.NoError(t, cli.CreateTag(ctx, LockTagName, "held", ""))

	// "{version}" is the broadest format there is: its glob is "*" and its
	// shape check accepts any name at all.
	tags := tagsOf(t, cli, ctx, "core", "{version}")
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	assert.Equal(t, []string{"0.1.0"}, names, "in %s", root)
}

// remoteTagObject reads the tag object a name resolves to in a bare
// repository, which is what distinguishes two annotated tags of the same name
// at the same commit.
func remoteTagObject(t *testing.T, bare, tag string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", "refs/tags/"+tag).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// remoteTagCommit reads what a tag points at in a bare repository.
func remoteTagCommit(t *testing.T, bare, tag string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-list", "-n1", tag).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// runGit runs one git command in dir and returns its output, for the tests
// that need a second working copy to act as somebody else.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func TestTagNameNormativeForm(t *testing.T) {
	v := ccme.Version{Major: 1, Minor: 2, Patch: 3}
	assert.Equal(t, "core@1.2.3", TagName("core", v))
	v.Prerelease = []string{"beta", "4"}
	assert.Equal(t, "core@1.2.3-beta.4", TagName("core", v))
}

// bareRepo creates an empty repository with nothing committed.
func bareRepo(t *testing.T) *CLI {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git init: %s", out)
	return &CLI{Dir: root}
}

func TestHeadSHANoCommits(t *testing.T) {
	cli := bareRepo(t)
	_, err := cli.HeadSHA(context.Background())
	require.Error(t, err, "an unborn HEAD has no SHA")
	assert.Contains(t, err.Error(), "rev-parse")
}

func TestPushNoRemote(t *testing.T) {
	_, cli := initRepo(t)
	_, err := cli.Push(context.Background(), "origin", nil, false)
	require.Error(t, err, "pushing without a remote fails loudly")
}

func TestTagsOutsideRepo(t *testing.T) {
	cli := &CLI{Dir: t.TempDir()} // not a repository at all
	_, err := cli.Tags(context.Background(), "core", DefaultTagFormat)
	require.Error(t, err)
}

func TestCommitDirsNothingStaged(t *testing.T) {
	root, cli := initRepo(t)
	committed, err := cli.CommitDirs(context.Background(),
		[]string{filepath.Join(root, "packages", "core")}, "chore(release): nothing")
	require.NoError(t, err)
	assert.False(t, committed, "a clean folder stages nothing and creates no commit")
}

func TestRevertDirNoChanges(t *testing.T) {
	root, cli := initRepo(t)
	require.NoError(t, cli.RevertDir(context.Background(), filepath.Join(root, "packages", "core")),
		"reverting a clean folder is a no-op, not an error")
}

func TestIsShallowStates(t *testing.T) {
	root, cli := initRepo(t)
	shallow, err := cli.IsShallow(context.Background())
	require.NoError(t, err)
	assert.False(t, shallow, "a full clone is not shallow")

	// A --depth 1 clone of the same repository is.
	cloneDir := t.TempDir()
	cmd := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+root, cloneDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git clone: %s", out)
	shallowClone := &CLI{Dir: cloneDir}
	shallow, err = shallowClone.IsShallow(context.Background())
	require.NoError(t, err)
	assert.True(t, shallow)
}

func TestPathspecInsideAndOutside(t *testing.T) {
	root, cli := initRepo(t)
	assert.Equal(t, filepath.Join("packages", "core"),
		cli.pathspec(filepath.Join(root, "packages", "core")), "inside the repo: relative")
	assert.Equal(t, ".", cli.pathspec(root), "the root itself")
	outside := filepath.Dir(root)
	assert.Equal(t, "..", cli.pathspec(outside), "an expressible outside path stays relative")
}

func TestGlobAndMatchesUnsplittableFormat(t *testing.T) {
	f := TagFormat("no placeholders")
	assert.Equal(t, "core*", f.Glob("core"), "an uncompilable format degrades to a name prefix")
	assert.False(t, f.Matches("core", "core@1.0.0"))
	if _, ok := f.ParseVersion("core", "core@1.0.0"); ok {
		t.Error("an uncompilable format parses nothing")
	}
}

func TestConfiguredCommitterIdentity(t *testing.T) {
	// The configured identity covers every commit and annotated tag the CLI
	// creates, without any `git config` in the repository.
	root, cli := initRepo(t)
	cli.Name, cli.Email = "release bot", "bot@dispat.test"
	pkg := filepath.Join(root, "packages", "core")
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "new.txt"), []byte("x"), 0o644))
	committed, err := cli.CommitDirs(context.Background(), []string{pkg}, "chore(release): core 1.0.0")
	require.NoError(t, err)
	require.True(t, committed)
	out, err := cli.run(context.Background(), "log", "-1", "--format=%cn <%ce>")
	require.NoError(t, err)
	assert.Equal(t, "release bot <bot@dispat.test>", strings.TrimSpace(out))

	require.NoError(t, cli.CreateTag(context.Background(), "core@1.0.0", "release core@1.0.0", ""))
	out, err = cli.run(context.Background(), "for-each-ref", "--format=%(taggername) %(taggeremail)", "refs/tags/core@1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "release bot <bot@dispat.test>", strings.TrimSpace(out))
}

func TestResolveCommit(t *testing.T) {
	_, cli := initRepo(t)
	full, err := cli.HeadSHA(context.Background())
	require.NoError(t, err)
	short, err := cli.ResolveCommit(context.Background(), full[:8])
	require.NoError(t, err)
	assert.Equal(t, full, short, "a short SHA peels to the full commit")
	_, err = cli.ResolveCommit(context.Background(), "doesnotexist")
	assert.Error(t, err)
}

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func TestCurrentBranch(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()

	// The host's init.defaultBranch differs between machines and CI runners,
	// so the tests name the branch rather than inherit it.
	gitIn(t, root, "checkout", "-q", "-B", "main")
	branch, err := cli.CurrentBranch(ctx)
	require.NoError(t, err)
	assert.Equal(t, "main", branch)

	gitIn(t, root, "checkout", "-q", "-B", "release/v2")
	branch, err = cli.CurrentBranch(ctx)
	require.NoError(t, err)
	assert.Equal(t, "release/v2", branch, "a slashed branch keeps its full name")

	// A detached HEAD has no branch, reported as "" rather than as git's own
	// spelling of it, which is the literal word HEAD.
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	gitIn(t, root, "checkout", "-q", head)
	branch, err = cli.CurrentBranch(ctx)
	require.NoError(t, err)
	assert.Empty(t, branch, "detached HEAD has no branch name")
}

func TestCurrentBranchOutsideRepo(t *testing.T) {
	cli := &CLI{Dir: t.TempDir()}
	_, err := cli.CurrentBranch(context.Background())
	assert.Error(t, err, "a folder that is not a repository is an error, not an empty branch")
}

func TestBehindRemote(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	gitIn(t, root, "checkout", "-q", "-B", "main")
	bare := addBareRemote(t, root)

	// A branch the remote does not have yet is not behind: the first push is
	// what creates it, and refusing would make the first release impossible.
	behind, err := cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.False(t, behind, "a branch absent from the remote is not behind")

	_, err = cli.Push(ctx, "origin", nil, false)
	require.NoError(t, err)
	behind, err = cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.False(t, behind, "a checkout level with the remote is not behind")

	// Ahead is not behind: local commits the remote has not seen are exactly
	// what a release is about to push.
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "extra.txt"), []byte("x"), 0o644))
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "feat(core): local work")
	behind, err = cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.False(t, behind, "a checkout ahead of the remote is not behind")

	// Another clone pushes: now the remote holds a commit this checkout has
	// never fetched, which is the case the guard exists for.
	other := t.TempDir()
	out, err := exec.Command("git", "clone", "-q", "--branch", "main", bare, other).CombinedOutput()
	require.NoError(t, err, "git clone: %s", out)
	gitIn(t, other, "config", "user.email", "other@example.com")
	gitIn(t, other, "config", "user.name", "Other")
	gitIn(t, other, "commit", "-q", "--allow-empty", "-m", "chore: pushed elsewhere")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/main")

	behind, err = cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.True(t, behind, "an unfetched remote tip means the checkout is behind")

	// Catching up clears it, even though the local commit is still ahead.
	gitIn(t, root, "pull", "-q", "--rebase", "origin", "main")
	behind, err = cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.False(t, behind, "a rebased checkout contains the remote tip again")
}

// TestBehindRemoteIgnoresTailMatchingRefs: ls-remote arguments are
// tail-matching patterns, so a branch literally named "x/refs/heads/main"
// lists alongside "refs/heads/main". Only the exact ref may be read as the
// tip, or an unrelated branch would decide whether the release proceeds.
func TestBehindRemoteIgnoresTailMatchingRefs(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	gitIn(t, root, "checkout", "-q", "-B", "main")
	addBareRemote(t, root)
	_, err := cli.Push(ctx, "origin", nil, false)
	require.NoError(t, err)

	// The decoy carries a commit main does not have. Read as the tip it would
	// make the checkout look behind; matched exactly, it is ignored.
	gitIn(t, root, "checkout", "-q", "-b", "x/refs/heads/main")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "chore: decoy")
	gitIn(t, root, "push", "-q", "origin", "x/refs/heads/main")
	gitIn(t, root, "checkout", "-q", "main")

	behind, err := cli.BehindRemote(ctx, "origin", "main")
	require.NoError(t, err)
	assert.False(t, behind, "only refs/heads/main decides, not a branch whose name ends in it")
}

func TestBehindRemoteNoRemote(t *testing.T) {
	root, cli := initRepo(t)
	gitIn(t, root, "checkout", "-q", "-B", "main")
	_, err := cli.BehindRemote(context.Background(), "origin", "main")
	assert.Error(t, err, "an unreachable remote is an error, not a verdict")
}

func TestRemoteTagMessage(t *testing.T) {
	// The reader behind the lock refusal's holder line: an annotated tag's
	// message comes back from the remote without touching this clone's refs,
	// and a missing tag is an error rather than an empty answer.
	root, cli := initRepo(t)
	ctx := context.Background()
	addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "dispat-release-lock", "dispat release lock\n\nhost ci-7\npid 42\n", ""))
	require.NoError(t, cli.PushTag(ctx, "origin", "dispat-release-lock"))
	require.NoError(t, cli.DeleteTag(ctx, "dispat-release-lock"))

	msg, err := cli.RemoteTagMessage(ctx, "origin", "dispat-release-lock")
	require.NoError(t, err)
	assert.Contains(t, msg, "host ci-7")
	assert.Contains(t, msg, "pid 42")

	out, gerr := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, gerr)
	assert.NotContains(t, string(out), "dispat-release-lock", "the read leaves no local ref behind")

	_, err = cli.RemoteTagMessage(ctx, "origin", "no-such-tag")
	assert.Error(t, err)
}

// TestMutates: which git invocations rise to debug level. The one read-only
// spelling sharing a subcommand with a mutation is "tag --list".
func TestMutates(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"push", "origin", "HEAD"}, true},
		{[]string{"commit", "-m", "x"}, true},
		{[]string{"add", "--", "a"}, true},
		{[]string{"checkout", "--", "a"}, true},
		{[]string{"clean", "-fd"}, true},
		{[]string{"tag", "-a", "v1", "-m", "x"}, true},
		{[]string{"tag", "-d", "v1"}, true},
		{[]string{"tag", "--list", "--merged", "HEAD"}, false},
		{[]string{"rev-parse", "HEAD"}, false},
		{[]string{"ls-remote", "origin"}, false},
		{nil, false},
	} {
		assert.Equalf(t, tc.want, mutates(tc.args), "mutates(%v)", tc.args)
	}
}

// TestPushReportsARejectedBranchAsRecoverable: the two failures a push can
// meet call for entirely different answers, so they have to be told apart. A
// branch somebody moved is recoverable and comes back as ErrRejected; a remote
// nobody can reach is not, and replaying commits onto it would be nonsense.
func TestPushReportsARejectedBranchAsRecoverable(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	_, err := cli.Push(ctx, "origin", nil, false)
	require.NoError(t, err)

	// Another clone lands a commit, which is what leaves this one unable to
	// push what it built on the old tip.
	other := t.TempDir()
	otherGit := func(args ...string) {
		t.Helper()
		out, gerr := exec.Command("git", append([]string{"-C", other}, args...)...).CombinedOutput()
		require.NoError(t, gerr, "git %v: %s", args, out)
	}
	out, cerr := exec.Command("git", "clone", "-q", bare, other).CombinedOutput()
	require.NoError(t, cerr, "git clone: %s", out)
	otherGit("config", "user.email", "other@example.com")
	otherGit("config", "user.name", "Other")
	require.NoError(t, os.WriteFile(filepath.Join(other, "theirs.txt"), []byte("theirs\n"), 0o644))
	otherGit("add", ".")
	otherGit("commit", "-qm", "chore: landed elsewhere")
	otherGit("push", "-q", "origin", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "CHANGELOG.md"),
		[]byte("# Changelog\n"), 0o644))
	committed, err := cli.CommitDirs(ctx, []string{filepath.Join(root, "packages", "core")}, "chore(release): core@0.2.0")
	require.NoError(t, err)
	require.True(t, committed)

	_, err = cli.Push(ctx, "origin", nil, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRejected, "a branch that moved is something the caller can recover from")
	assert.Contains(t, err.Error(), "[rejected]", "and the wrapped error still says what git said")

	// The recovery itself: what landed is joined with the local commit, which
	// keeps the identity it had, and the push then succeeds.
	before, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	branch, err := cli.CurrentBranch(ctx)
	require.NoError(t, err)
	require.NoError(t, cli.MergeRemote(ctx, "origin", branch, "chore(release): merge origin/"+branch))
	parents, oerr := exec.Command("git", "-C", root, "rev-list", "--parents", "-n", "1", "HEAD").Output()
	require.NoError(t, oerr)
	fields := strings.Fields(strings.TrimSpace(string(parents)))
	require.Len(t, fields, 3, "the tip is a merge of two commits")
	assert.Equal(t, before, fields[1], "the release commit is the first parent and was not rewritten")
	_, err = cli.Push(ctx, "origin", nil, false)
	assert.NoError(t, err)

	// A remote nobody can reach is a different failure and must not be
	// mistaken for one worth replaying commits onto.
	_, err = exec.Command("git", "-C", root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone")).Output()
	require.NoError(t, err)
	_, err = cli.Push(ctx, "origin", nil, false)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRejected)
}

// TestMergeRemoteUndoesAMergeItCannotFinish: a conflict leaves the working
// tree as the run had it rather than half way through a merge somebody else
// then has to find and abort.
func TestMergeRemoteUndoesAMergeItCannotFinish(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	_, err := cli.Push(ctx, "origin", nil, false)
	require.NoError(t, err)

	other := t.TempDir()
	out, cerr := exec.Command("git", "clone", "-q", bare, other).CombinedOutput()
	require.NoError(t, cerr, "git clone: %s", out)
	for _, args := range [][]string{
		{"config", "user.email", "other@example.com"},
		{"config", "user.name", "Other"},
	} {
		out, gerr := exec.Command("git", append([]string{"-C", other}, args...)...).CombinedOutput()
		require.NoError(t, gerr, "git %v: %s", args, out)
	}
	require.NoError(t, os.WriteFile(filepath.Join(other, "packages", "core", "CHANGELOG.md"),
		[]byte("# Their changelog\n"), 0o644))
	for _, args := range [][]string{
		{"add", "."}, {"commit", "-qm", "docs: their changelog"}, {"push", "-q", "origin", "HEAD"},
	} {
		out, gerr := exec.Command("git", append([]string{"-C", other}, args...)...).CombinedOutput()
		require.NoError(t, gerr, "git %v: %s", args, out)
	}

	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "CHANGELOG.md"),
		[]byte("# Our changelog\n"), 0o644))
	_, err = cli.CommitDirs(ctx, []string{filepath.Join(root, "packages", "core")}, "chore(release): core@0.2.0")
	require.NoError(t, err)

	branch, err := cli.CurrentBranch(ctx)
	require.NoError(t, err)
	require.Error(t, cli.MergeRemote(ctx, "origin", branch, "chore(release): merge"),
		"the two sides wrote the same file")
	assert.NoFileExists(t, filepath.Join(root, ".git", "MERGE_HEAD"))
	assert.NoDirExists(t, filepath.Join(root, ".git", "rebase-merge"))
	head, err := exec.Command("git", "-C", root, "log", "--format=%s", "-1").Output()
	require.NoError(t, err)
	assert.Equal(t, "chore(release): core@0.2.0", strings.TrimSpace(string(head)),
		"the run's own commit is still what HEAD names")
}
