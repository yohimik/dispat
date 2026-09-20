// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestAutoReplacerSkipsBinaryContentWithoutLosingTextEdits(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig(echoBuild, 1))
	repo.SeedPackage("packages", "core")
	repo.WriteFile("packages/core/pin.txt", "before\n")
	const binary = "before\x00untouched\n"
	repo.WriteFile("packages/core/data.bin", binary)
	repo.Commit("feat(core): mixed package contents")
	result := repo.Command("autoreplacer", "--since", "all", "--files", "*",
		"--replace", "before=>after", "--log-level", "debug")
	require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "file skipped")
	assert.Contains(t, result.Stdout, "data.bin")
	assert.Equal(t, binary, readRepoFile(t, repo, "packages/core/data.bin"))
	assert.Equal(t, "after\n", readRepoFile(t, repo, "packages/core/pin.txt"))
	assert.Empty(t, repo.TagList())
}

func TestAutoReplacerReportsAnAtomicWriteRefusalWithoutTruncatingTheFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are required")
	}
	skipIfSuperuser(t)
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig(echoBuild, 1))
	repo.SeedPackage("packages", "core")
	repo.WriteFile("packages/core/pin.txt", "before\n")
	repo.Commit("feat(core): read-only destination")
	dir := repo.Path("packages/core")
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	result := repo.Command("autoreplacer", "--since", "all", "--files", "pin.txt", "--replace", "before=>after")
	require.NotZero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "failed")
	assert.Equal(t, "before\n", readRepoFile(t, repo, "packages/core/pin.txt"))
	assert.Empty(t, repo.TagList())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".dispat-", "no failed temporary write remains")
	}
}
