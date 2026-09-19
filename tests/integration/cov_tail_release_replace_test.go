// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the replacing strategy's walk over a package folder.
// A rule reaches any file at all, which means it also reaches whatever the
// filesystem will not let it read, and the answer has to be the same one a
// scan gives: the package folder itself must be readable, anything below it is
// reported and stepped over, and the rule still rewrites everything it could
// reach.

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailReplaceRuleStepsOverAFolderItCannotEnter: failing a release over
// an unreadable folder no rule was ever going to reach would be the worse
// trade, so the folder is named in a warning and skipped whole, and the files
// the rule could reach are rewritten as if it were not there.
func TestCovTailReplaceRuleStepsOverAFolderItCannotEnter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder's mode does not gate a directory read on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root enters a folder whatever its mode says")
	}
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/pin.txt", "pinned at 0.0.1\n")
	r.WriteFile("packages/core/sealed/pin.txt", "pinned at 0.0.1\n")
	r.Commit("feat(core): bootstrap")

	sealed := r.Path("packages", "core", "sealed")
	require.NoError(t, os.Chmod(sealed, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	res := r.Command("autoreplacer", "--replace", "pinned at 0.0.1=>pinned at {version}",
		"--files", "*.txt", "--since", "all")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "folder skipped", "the folder that was stepped over is named")
	assert.Contains(t, res.Stdout, "sealed")

	assert.Equal(t, "pinned at 0.1.0\n", covTailReadFile(t, r, "packages", "core", "pin.txt"),
		"everything the rule could reach was still rewritten")
}

// TestCovTailChangelogRefusesAPathItCannotWriteAtomically: a changelog is
// rewritten whole through a temporary file and a rename, because a write
// interrupted halfway would take the package's history with it. A configured
// path whose parent is a file cannot be examined at all, which is neither "no
// changelog yet" nor "one to append to", so the write is refused and the run
// fails on the record rather than replacing the file with a guess.
func TestCovTailChangelogRefusesAPathItCannotWriteAtomically(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{File: "notes/CHANGELOG.md"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	// A file where the changelog's folder would be.
	r.WriteFile("packages/core/notes", "not a folder\n")
	r.Commit("feat(core): bootstrap")

	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "changelog")
	assert.Contains(t, res.Stdout+res.Stderr, "notes",
		"the path the write could not examine is named")
	assert.NoFileExists(t, r.Path("packages", "core", "notes", "CHANGELOG.md"),
		"and nothing was written anywhere near it")
}
