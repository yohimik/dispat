// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the atomic replace every file a user keeps goes
// through.
//
// A changelog and a config file are documents someone edits, so dispat never
// writes them in place: it writes a neighbouring temporary file and renames it
// over the target. Two things that replace cannot do are worth proving through
// the binary, because both of them happen to real repositories and both would
// otherwise destroy something: replacing a symlink would overwrite whatever it
// points at with a file, and a folder that refuses a temporary file must stop
// the step rather than leave a half-written record.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// skipIfSuperuser skips a scenario that relies on filesystem permissions
// actually stopping the process.
func skipIfSuperuser(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("a read-only folder does not stop the superuser")
	}
}

// TestCovAtomicWriteRefusesToReplaceASymlink: a record file that is a symlink
// is never replaced. The write is refused by name, the link still points where
// it did, and what it points at is untouched — a repository that publishes its
// changelogs through links keeps them.
func TestCovAtomicWriteRefusesToReplaceASymlink(t *testing.T) {
	t.Run("a changelog symlinked elsewhere", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.WriteFile("docs/core-history.md", "# kept by hand\n")
		require.NoError(t, os.Symlink(r.Path("docs", "core-history.md"),
			r.Path("packages", "core", "CHANGELOG.md")))
		r.Commit("feat(core): first feature")

		res := r.Command("changelog", "--package", "core")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "refusing to replace symlink")

		info, err := os.Lstat(r.Path("packages", "core", "CHANGELOG.md"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link is still a link")
		assert.Equal(t, "# kept by hand\n", readRepoFile(t, r, "docs/core-history.md"),
			"and what it points at was never written through")
	})

	t.Run("a config file symlinked to nowhere", func(t *testing.T) {
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		require.NoError(t, os.Symlink(r.Path("absent", "dispat.json"), r.Path("dispat.json")))

		res := r.Command("init")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "refusing to replace symlink")
		_, err := os.Stat(r.Path("dispat.json"))
		assert.True(t, os.IsNotExist(err), "the dangling link was not turned into a file")
	})
}

// TestCovAtomicWriteStopsWhenTheFolderTakesNoTemporaryFile: the temporary file
// lands beside its target so the rename never crosses a filesystem, which
// means a folder that cannot be written in stops the write there — before any
// part of the record exists.
func TestCovAtomicWriteStopsWhenTheFolderTakesNoTemporaryFile(t *testing.T) {
	skipIfSuperuser(t)

	t.Run("a package folder that cannot be written in", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")
		dir := r.Path("packages", "core")
		require.NoError(t, os.Chmod(dir, 0o555))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

		res := r.Command("changelog", "--package", "core")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "permission denied")
		_, err := os.Stat(filepath.Join(dir, "CHANGELOG.md"))
		assert.True(t, os.IsNotExist(err), "no changelog was created")
	})

	t.Run("a repository root that cannot be written in", func(t *testing.T) {
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		r.Commit("feat(core): bootstrap")
		require.NoError(t, os.Chmod(r.Root, 0o555))
		t.Cleanup(func() { _ = os.Chmod(r.Root, 0o755) })

		res := r.Command("init")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "permission denied")
	})
}
