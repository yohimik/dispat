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
	skipped, err := cli.Push(ctx, "origin", []string{"core@0.1.0"})
	require.NoError(t, err)
	assert.Empty(t, skipped, "a fresh tag is pushed, not skipped")

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
	skipped, err := cli.Push(ctx, "origin", []string{"core@0.1.0"})
	require.NoError(t, err)
	require.Empty(t, skipped)

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

	skipped, err = cli.Push(ctx, "origin", []string{"core@0.1.0", "core@0.2.0"})
	require.NoError(t, err)
	assert.Equal(t, []string{"core@0.1.0"}, skipped, "the existing tag is skipped, not an error")

	out, err := exec.Command("git", "-C", bare, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "core@0.2.0", "the new tag still arrives")
}

func TestTagNameNormativeForm(t *testing.T) {
	v := ccme.Version{Major: 1, Minor: 2, Patch: 3}
	assert.Equal(t, "core@1.2.3", TagName("core", v))
	v.Prerelease = []string{"beta", "4"}
	assert.Equal(t, "core@1.2.3-beta.4", TagName("core", v))
}
