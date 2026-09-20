// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A lost push response does not mean the lock was rejected. Its unique attempt
// must prove ownership before work starts and must be cleaned after the run.
func TestReleaseLockRecoversItsOwnAcceptedPushAfterAResponseFailure(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig("echo built > ../../built", 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): lock response recovery")
	remote := repo.AddBareRemote()
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*push*refs/tags/dispat-release-lock*", Nth: 1, After: true, Code: 128,
	})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "push reported a failure but landed")
	assert.FileExists(t, repo.Path("built"))
	assert.True(t, repo.IsTagged("core@0.1.0"))
	assertLockCleared(t, repo, remote)
	assert.Empty(t, repo.Git("tag", "--list", "dispat-release-lock-attempt-*"))
}

func TestReleaseLockCreationFailurePreventsPlanningAndPublication(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig("echo built > ../../built", 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): lock required")
	remote := repo.AddBareRemote()
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*tag -a dispat-release-lock-attempt-*", Code: 128})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "creating the release lock tag")
	assert.Equal(t, 1, fault.Matches())
	assert.Empty(t, plannedPackages(result))
	assert.NoFileExists(t, repo.Path("built"))
	assert.Empty(t, repo.TagList())
	assert.False(t, remoteHoldsLock(t, remote))
}

func TestReleaseLockLocalCleanupFailurePreservesThePublishedOutcome(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig(echoBuild, 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): cleanup required")
	remote := repo.AddBareRemote()
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*tag -d dispat-release-lock-attempt-*", Code: 128})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	requireDiagnostic(t, result, "E336")
	assert.Contains(t, result.Stdout, "could not remove the local release lock tag")
	assert.True(t, repo.IsTagged("core@0.1.0"))
	assert.False(t, remoteHoldsLock(t, remote), "remote cleanup succeeded before local cleanup failed")
	assert.NotEmpty(t, repo.Git("tag", "--list", "dispat-release-lock-attempt-*"))
}

// A lock written without annotation still excludes another publisher. Failure
// to clean this run's rejected attempt must never delete the foreign lock.
func TestReleaseLockPreservesALightweightForeignLockWhenAttemptCleanupFails(t *testing.T) {
	repo := harness.New(t)
	config := libsConfig("echo built > ../../built", 1)
	config.LogLevel = "debug"
	repo.WriteConfigModel(config)
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): foreign lock remains authoritative")
	remote := repo.AddBareRemote()
	repo.Git("push", "-q", "origin", "HEAD")
	bareGit(t, remote, "tag", lockTag, harness.DefaultBranch)
	foreign := lockObject(t, remote)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*tag -d dispat-release-lock-attempt-*", Code: 128})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.NotZero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout, "could not remove the local lock tag after a failed push")
	assert.Equal(t, foreign, lockObject(t, remote))
	assert.Equal(t, 1, fault.Matches())
	assert.NoFileExists(t, repo.Path("built"))
	assert.Zero(t, repo.TagCount("core@"))
	assert.NotEmpty(t, repo.Git("tag", "--list", "dispat-release-lock-attempt-*"))
}
