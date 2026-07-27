package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/monorel/internal/semver"
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

	_, found, err := cli.LatestTag(ctx, "core")
	require.NoError(t, err)
	assert.False(t, found, "no tags yet")

	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0"))
	require.NoError(t, cli.CreateTag(ctx, "core@0.2.0", "release core@0.2.0"))
	require.NoError(t, cli.CreateTag(ctx, "core-utils@9.9.9", "other package"))

	tag, found, err := cli.LatestTag(ctx, "core")
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, tag.Parsed)
	assert.Equal(t, "core@0.2.0", tag.Name)
	assert.Equal(t, semver.Version{Minor: 2}, tag.Version)
}

func TestLatestTagUnparseableNewest(t *testing.T) {
	// The newest tag by creation date is unparseable: LatestTag must report
	// it with Parsed=false instead of falling back to the older 0.0.0.
	root, cli := initRepo(t)
	ctx := context.Background()

	tagAt(t, root, "core@0.0.0", "2026-01-01T10:00:00")
	tagAt(t, root, "core@0.0.1-0.0.0", "2026-01-02T10:00:00")

	tag, found, err := cli.LatestTag(ctx, "core")
	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, tag.Parsed)
	assert.Equal(t, "core@0.0.1-0.0.0", tag.Name)
}

func TestLatestTagMaxSemverWhenNewestParses(t *testing.T) {
	// An old unparseable tag does not disturb normal resolution, and the
	// highest parseable version wins even if created before a lower one
	// (backport scenario).
	root, cli := initRepo(t)
	ctx := context.Background()

	tagAt(t, root, "core@0.9.0-rc1", "2026-01-01T10:00:00") // old junk
	tagAt(t, root, "core@2.0.0", "2026-01-02T10:00:00")
	tagAt(t, root, "core@1.2.1", "2026-01-03T10:00:00") // backport, newest by date

	tag, found, err := cli.LatestTag(ctx, "core")
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, tag.Parsed)
	assert.Equal(t, "core@2.0.0", tag.Name, "highest parseable version wins")
	assert.Equal(t, semver.Version{Major: 2}, tag.Version)
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

func TestSubjects(t *testing.T) {
	_, cli := initRepo(t)
	subjects, err := cli.Subjects(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	assert.Equal(t, "feat(core): initial", subjects[0])
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

	subjects, err := cli.Subjects(ctx, "")
	require.NoError(t, err)
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
	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0"))
	require.NoError(t, cli.Push(ctx, "origin"))

	out, err := exec.Command("git", "-C", bare, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "core@0.1.0", "tag must arrive on the remote")
	out, err = exec.Command("git", "-C", bare, "log", "--format=%s", "--all").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "chore(release): core@0.1.0", "commit must arrive on the remote")
}
