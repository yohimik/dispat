// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoreRepo is initRepo with a root lock file and a second package, both
// committed, so a restore has shared and excluded files to tell apart.
func restoreRepo(t *testing.T) (string, *LocalGitx) {
	t.Helper()
	root, cli := initRepo(t)
	writeRestoreFile(t, root, "lock.txt", "core 0.0.0\n")
	writeRestoreFile(t, root, "packages/app/main.txt", "app\n")
	runRestoreGit(t, root, "add", ".")
	runRestoreGit(t, root, "commit", "-qm", "chore: seed a lock file and a second package")
	return root, cli
}

func writeRestoreFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func readRestoreFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func runRestoreGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// TestRestoreToHeadKeepsExclusionsAndUntrackedFiles: an include of the whole
// repository restores every tracked change and every staged addition to HEAD,
// except beneath an exclusion, and leaves untracked files where they are.
func TestRestoreToHeadKeepsExclusionsAndUntrackedFiles(t *testing.T) {
	root, cli := restoreRepo(t)
	writeRestoreFile(t, root, "lock.txt", "core 0.1.0\n")
	writeRestoreFile(t, root, "packages/core/main.txt", "planned")
	writeRestoreFile(t, root, "packages/app/main.txt", "published\n")
	writeRestoreFile(t, root, "staged.txt", "staged\n")
	runRestoreGit(t, root, "add", "staged.txt")
	writeRestoreFile(t, root, "packages/core/dist/out.txt", "build output\n")

	require.NoError(t, cli.RestoreToHead(context.Background(), []string{root},
		[]string{filepath.Join(root, "packages", "app")}))

	assert.Equal(t, "core 0.0.0\n", readRestoreFile(t, root, "lock.txt"), "a shared file goes back to HEAD")
	assert.Equal(t, "original", readRestoreFile(t, root, "packages/core/main.txt"))
	assert.Equal(t, "published\n", readRestoreFile(t, root, "packages/app/main.txt"), "the exclusion is untouched")
	assert.NoFileExists(t, filepath.Join(root, "staged.txt"), "a file HEAD lacks leaves the index and the tree")
	assert.FileExists(t, filepath.Join(root, "packages", "core", "dist", "out.txt"), "untracked files stay")
	assert.Equal(t, " M packages/app/main.txt\n?? packages/core/dist/\n",
		runRestoreGit(t, root, "status", "--porcelain"))
}

// TestRestoreToHeadAcceptsPathsGitDoesNotKnow: a configured path that does not
// exist, or exists only untracked, is not an error, and a restore with nothing
// to do changes nothing.
func TestRestoreToHeadAcceptsPathsGitDoesNotKnow(t *testing.T) {
	root, cli := restoreRepo(t)
	writeRestoreFile(t, root, "generated/new.lock", "fresh\n")
	paths := []string{filepath.Join(root, "missing.lock"), filepath.Join(root, "generated", "new.lock")}

	require.NoError(t, cli.RestoreToHead(context.Background(), paths, nil))
	assert.Equal(t, "fresh\n", readRestoreFile(t, root, "generated/new.lock"))
	require.NoError(t, cli.RestoreToHead(context.Background(), nil, nil), "no paths is no work")
}

// TestRestoreToHeadMatchesExclusionsLiterally: an exclusion is a path, not a
// pattern, so a glob character in it excludes only a folder spelled that way.
func TestRestoreToHeadMatchesExclusionsLiterally(t *testing.T) {
	root, cli := restoreRepo(t)
	writeRestoreFile(t, root, "packages/app/main.txt", "planned\n")

	require.NoError(t, cli.RestoreToHead(context.Background(), []string{filepath.Join(root, "packages")},
		[]string{filepath.Join(root, "packages", "a*")}))
	assert.Equal(t, "app\n", readRestoreFile(t, root, "packages/app/main.txt"))
}
