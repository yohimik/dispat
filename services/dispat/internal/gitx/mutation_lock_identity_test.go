// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepoAt creates a repository with one commit at an exact folder, which is
// what the identity tests need and initRepo's own t.TempDir() cannot give:
// the spelling of the folder is the thing under test.
func initRepoAt(t *testing.T, root string) *LocalGitx {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.txt"), []byte("original"), 0o644))
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "feat(core): initial")
	return &LocalGitx{Dir: root}
}

// isCaseInsensitive reports whether the filesystem under dir hands the same
// folder back under a different spelling. It is probed rather than assumed:
// macOS is case-insensitive by default and case-sensitive on an APFS volume
// formatted that way, and the two cannot be told apart from the platform name.
func isCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	require.NoError(t, os.Mkdir(probe, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(probe) })
	info, err := os.Stat(filepath.Join(dir, "caseprobe"))
	if err != nil {
		return false
	}
	original, err := os.Stat(probe)
	require.NoError(t, err)
	return os.SameFile(info, original)
}

// TestCanonicalMutationDirIsOneSpellingPerDirectory: the lock's identity is the
// directory, not the string that names it.
//
// Two spellings of one folder would mean two lock paths, two descriptors on
// one file and a process waiting on its own flock — a wait it could leave only
// by being cancelled, because the release it waits for is its own and comes
// afterwards. Asked of the resolver directly because the caller above it
// cannot produce the input any more: `git rev-parse --git-common-dir` returns
// the filesystem's own spelling, so on this host git already folds the case of
// a repository path. That is a property of git rather than of this package,
// and this is the rule that holds whatever git returns.
func TestCanonicalMutationDirIsOneSpellingPerDirectory(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	if !isCaseInsensitive(t, base) {
		t.Skip("the filesystem is case-sensitive, so two spellings are two folders")
	}
	dir := filepath.Join(base, "Common")
	require.NoError(t, os.Mkdir(dir, 0o755))

	first, err := canonicalMutationDir(dir)
	require.NoError(t, err)
	second, err := canonicalMutationDir(filepath.Join(base, "common"))
	require.NoError(t, err)
	assert.Equal(t, first, second, "one directory under two spellings is one lock, not a deadlock")

	// A folder that went away takes its entry with it, so a new folder at the
	// same path is answered as itself rather than as the one it replaced: an
	// inode a deleted directory leaves behind can be handed straight to the
	// next one.
	require.NoError(t, os.RemoveAll(dir))
	other := filepath.Join(base, "other")
	require.NoError(t, os.Mkdir(other, 0o755))
	answer, err := canonicalMutationDir(other)
	require.NoError(t, err)
	assert.Equal(t, other, answer)

	_, err = canonicalMutationDir(filepath.Join(base, "absent"))
	require.Error(t, err, "a directory that is not there cannot hold a lock")
}

// TestMutationLockIsOneLockHoweverTheRepositoryIsSpelled is the same rule seen
// from the caller: acquiring two spellings of one repository in one
// transaction must take one lock. The bound is deliberately short, so a
// regression is a two-second failure rather than a hung suite.
func TestMutationLockIsOneLockHoweverTheRepositoryIsSpelled(t *testing.T) {
	base := t.TempDir()
	if !isCaseInsensitive(t, base) {
		t.Skip("the filesystem is case-sensitive, so two spellings are two folders")
	}
	upper := initRepoAt(t, filepath.Join(base, "Repo"))
	lower := &LocalGitx{Dir: filepath.Join(base, "repo")}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := AcquireMutations(ctx, upper, lower)
	require.NoError(t, err, "one repository under two spellings must be one lock, not a deadlock")
	release()

	upperPath, err := upper.mutationLockPath(context.Background())
	require.NoError(t, err)
	lowerPath, err := lower.mutationLockPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, upperPath, lowerPath, "both spellings resolve to one lock path")
}

// TestMutationLockResolvesTheCommonDirectoryOncePerRepository: the `rev-parse`
// behind the lock path is asked once per repository folder and the answer is
// reused, because a release takes this lock on every commit, tag and push.
//
// The counter is the process-wide git invocation counter, so the claim is
// measured rather than read off the shape of the code.
func TestMutationLockResolvesTheCommonDirectoryOncePerRepository(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()

	first, err := cli.mutationLockPath(ctx)
	require.NoError(t, err)
	// The resolved path, because the answer is canonicalized: on macOS the
	// temporary folder reaches the repository through /var, which is a link.
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(real, ".git", mutationLockFile), first)

	before := GitInvocations()
	for range 5 {
		again, err := cli.mutationLockPath(ctx)
		require.NoError(t, err)
		assert.Equal(t, first, again)
	}
	assert.Equal(t, uint64(0), GitInvocations()-before, "the resolved common directory is remembered")

	// A repository that went away is not answered from memory: the entry is
	// dropped and the next question reaches git again, which is what makes a
	// removed temporary checkout an error rather than a stale path.
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".git")))
	_, err = cli.mutationLockPath(ctx)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "Git common directory"), err)
}
