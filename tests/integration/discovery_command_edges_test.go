// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestIfAnEmptyMatchedBranchIsADeliberateNoop distinguishes a matched empty
// action from no match. The selected branch wins and the else must not run.
func TestIfAnEmptyMatchedBranchIsADeliberateNoop(t *testing.T) {
	r := harness.New(t)
	res := r.CommandEnv([]string{"READY=1"}, "if", "READY", "--then", "", "--else", "echo wrong",
		"--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "the selected branch is empty, nothing to run")
	assert.NotContains(t, out, "wrong", "a matched empty branch must not fall through")
}

// TestIfReportsWhenItsInvocationFolderDisappears covers the execution failure
// after a condition has selected a real script. This is an operational error,
// rather than the script's own exit status.
func TestIfReportsWhenItsInvocationFolderDisappears(t *testing.T) {
	r := harness.New(t)
	missing := r.Path("removed-working-directory")
	marker := r.Path("unexpected-if-output")
	require.NoError(t, os.Mkdir(missing, 0o755))
	require.NoError(t, os.Remove(missing))

	res := r.CommandAt("removed-working-directory", "if", "!ABSENT", "--then",
		"touch "+harness.ShQuote(marker))
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "could not run the script")
	assert.NoFileExists(t, marker, "a shell that could not enter its directory runs nothing")
}

// TestDiscoveryNamesOneIdentityRepeatedAcrossSpacePaths verifies the
// diagnostic for the exact same package spelling found under two configured
// roots. It must name one duplicated identity rather than imply two names.
func TestDiscoveryNamesOneIdentityRepeatedAcrossSpacePaths(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	libs := cfg.Spaces["libs"]
	libs.Path = models.PathList{"packages", "vendor"}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("vendor", "core")
	r.Commit("feat(core): duplicated checkout")

	res := r.Status()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := diagnosticText(res)
	assert.Contains(t, out, `package "core" exists in two folders of space`)
	assert.NotContains(t, out, `packages "core" and "core"`, "identical spellings are named once")
	assert.Empty(t, plannedPackages(res), "ambiguous ownership cannot produce a partial plan")
}

// TestCommandValidationExplainsSharedFlagsAndRepositoryFreeRollback covers two
// command-line decisions that happen before configuration is read: a shared
// flag names its command owners, and rollback may identify a local tool by
// --as without inventing a repository it will never contact.
func TestCommandValidationExplainsSharedFlagsAndRepositoryFreeRollback(t *testing.T) {
	r := harness.New(t)

	foreign := r.Command("status", "--release-name", "candidate")
	require.Equal(t, 2, foreign.Code, "stdout:\n%s\nstderr:\n%s", foreign.Stdout, foreign.Stderr)
	out := foreign.Stdout + foreign.Stderr
	assert.Contains(t, out, "--release-name is not a status flag")
	assert.Contains(t, out, "dispat changelog")
	assert.Contains(t, out, "dispat github")

	rollback := r.Command("install", "--rollback", "--check", "--as", "local-tool",
		"--bin-dir", r.Path("bin"))
	require.Equal(t, 0, rollback.Code, "stdout:\n%s\nstderr:\n%s", rollback.Stdout, rollback.Stderr)
	assert.Contains(t, rollback.Stdout+rollback.Stderr, "there is no backup")
}
