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

// A lock push whose answer is lost on a remote that then answers no read at
// all cannot be adopted, because nothing shows it is this run's, and must not
// be stranded, because it may have landed. The push reaches the remote, every
// read of the lock fails, and the run removes the lock under a lease on its
// own object and refuses with E336, naming the attempt a stranded lock would
// carry. Nothing is planned and the remote is left without a lock.
func TestReleaseLockRemovesAPushWhoseOutcomeCannotBeRead(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig("echo built > ../../built", 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): lock outcome unknown")
	remote := repo.AddBareRemote()
	// Every git call about the remote lock ref runs and then reports a lost
	// answer: the lock push, each read and the lease delete. The attempt's own
	// local tag is named differently and is not touched.
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*[0-9a-f :]refs/tags/dispat-release-lock", After: true, Code: 128,
	})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	requireDiagnostic(t, result, "E336")
	assert.Contains(t, result.Stdout, "could not be read back")
	assert.Contains(t, result.Stdout, "dispat-release-lock-attempt-", "the refusal names the attempt")
	assert.GreaterOrEqual(t, fault.Matches(), 5, "the push, three reads and the lease delete")
	assert.False(t, remoteHoldsLock(t, remote), "the push that landed is removed again")
	assert.Empty(t, plannedPackages(result))
	assert.NoFileExists(t, repo.Path("built"))
	assert.Empty(t, repo.Git("tag", "--list", "dispat-release-lock-attempt-*"))
}

// A lock delete whose answer is lost already gave the lock back. The run reads
// the remote once, finds no lock, and ends as the successful release it was:
// exit 0, no E336, and no remedy telling anybody to delete a tag.
func TestReleaseLockLostDeleteResponseIsARelease(t *testing.T) {
	repo := harness.New(t)
	repo.WriteConfigModel(libsConfig("echo built > ../../built", 1))
	repo.SeedPackage("packages", "core")
	repo.Commit("feat(core): lock delete answer lost")
	remote := repo.AddBareRemote()
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*--force-with-lease=refs/tags/dispat-release-lock:*", After: true, Code: 128,
	})
	result := repo.CommandEnv(append(harness.LockEnabled, fault.Env()...))
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Equal(t, 1, fault.Matches(), "the delete was made and its answer lost")
	requireNoDiagnostic(t, result, "E336")
	assert.NotContains(t, result.Stdout, "delete the tag on the remote")
	assert.True(t, repo.IsTagged("core@0.1.0"))
	assertLockCleared(t, repo, remote)
}
