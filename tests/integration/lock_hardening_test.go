// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Release-lock hardening. The lock's job is to be held before anything is
// decided and to be given back to whoever still owns it, so these tests assert
// on the two things a stuck pipeline is read for: when the lock exists
// relative to the plan, and what survives on the remote afterwards.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// rejectPushes installs a pre-receive hook that refuses every write, which is
// what an unauthorized remote looks like from the pushing side: reachable,
// answering, and refusing the ref.
func rejectPushes(t *testing.T, bare string) {
	t.Helper()
	hook := filepath.Join(bare, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))
}

// delayPushes installs a pre-receive hook that stalls, so a scenario can
// interrupt a run while it is still acquiring.
func delayPushes(t *testing.T, bare string) {
	t.Helper()
	hook := filepath.Join(bare, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nsleep 30\n"), 0o755))
}

// lockedFleet assembles a two-source fleet whose sources sort a-source before
// z-source, each with its own bare remote, and returns the control repository
// with the control, a and z remotes.
func lockedFleet(t *testing.T, build string) (*harness.Repo, string, string, string) {
	t.Helper()
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "a")
	aSource.Commit("feat(a): bootstrap first source")
	zSource := harness.New(t)
	zSource.SeedPackage("packages", "z")
	zSource.Commit("feat(z): bootstrap later source")

	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", aSource)
	addPolyrepoSource(t, control, "z-source", "sources/z", zSource)
	cfg := polyrepoFile()
	cfg["logLevel"] = "debug"
	cfg["spaces"] = centralSpaces(map[string]string{
		"a": "sources/a/packages",
		"z": "sources/z/packages",
	})
	cfg["scripts"] = map[string]any{
		"build":   []string{build},
		"publish": []string{"echo publishing"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure the locked fleet")

	controlRemote := control.AddBareRemote()
	sourceRemote := func(path, name string) string {
		remote := filepath.Join(t.TempDir(), name+".git")
		control.Git("init", "-q", "--bare", remote)
		control.Git("-C", path, "remote", "set-url", "origin", remote)
		control.Git("-C", path, "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		return remote
	}
	return control, controlRemote, sourceRemote("sources/a", "a-source"), sourceRemote("sources/z", "z-source")
}

// firstIndex returns the position of the first event satisfying match, or -1.
func firstIndex(events []harness.Event, match func(harness.Event) bool) int {
	for i, event := range events {
		if match(event) {
			return i
		}
	}
	return -1
}

func lockAcquired(event harness.Event) bool {
	return event.Str("message") == "release lock acquired"
}

func planningStarted(event harness.Event) bool {
	message := event.Str("message")
	return strings.HasPrefix(message, "plan:") || message == "release plan ready"
}

// TestReleaseLockPrecedesPlanning proves the ordering the lock exists for:
// every participating repository is locked before the run reads a single tag
// or computes a version, while `status` plans the same repository without
// touching the remote at all.
func TestReleaseLockPrecedesPlanning(t *testing.T) {
	control, controlRemote, aRemote, zRemote := lockedFleet(t, "echo building")

	status := control.StatusOK()
	require.NotEqual(t, -1, firstIndex(status.Events, planningStarted),
		"status must still plan: stdout:\n%s", status.Stdout)
	assert.Equal(t, -1, firstIndex(status.Events, lockAcquired),
		"status never acquires the release lock")
	assert.False(t, remoteHoldsLock(t, controlRemote))

	res := releaseLocked(control)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	var acquired []string
	lastLock := -1
	for i, event := range res.Events {
		if lockAcquired(event) {
			acquired = append(acquired, event.Str("repository"))
			lastLock = i
		}
	}
	assert.Equal(t, []string{"a-source", "control", "z-source"}, acquired,
		"every participating repository is locked, in name order")
	planned := firstIndex(res.Events, planningStarted)
	require.NotEqual(t, -1, planned, "stdout:\n%s", res.Stdout)
	assert.Less(t, lastLock, planned,
		"the last lock is taken before the first planning event")
	assertLockCleared(t, control, controlRemote)
	assert.False(t, remoteHoldsLock(t, aRemote))
	assert.False(t, remoteHoldsLock(t, zRemote))
}

// TestReleaseLockBypassWarnsWithItsScope proves the explicit unsafe bypass is
// never silent and never vague: one warning naming every repository releasing
// without a lock, and no lock tag anywhere.
func TestReleaseLockBypassWarnsWithItsScope(t *testing.T) {
	control, controlRemote, aRemote, zRemote := lockedFleet(t, "echo building")

	res := control.CommandEnv([]string{"DISPAT_UNSAFE_DISABLE_LOCK=true"})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	var bypass harness.Event
	for _, event := range res.Events {
		if event.Code() == "W331" {
			bypass = event
		}
	}
	require.NotEmpty(t, bypass, "stdout:\n%s", res.Stdout)
	assert.Equal(t, "warn", bypass.Str("level"))
	assert.Equal(t, []string{"a-source", "control", "z-source"}, eventStrings(bypass, "repositories"),
		"the warning names every repository the bypass applies to")
	assert.Equal(t, []string{"DISPAT_UNSAFE_DISABLE_LOCK"}, eventStrings(bypass, "setting"))
	for _, remote := range []string{controlRemote, aRemote, zRemote} {
		assert.False(t, remoteHoldsLock(t, remote), "a bypassed run takes no lock")
	}
}

// TestReleaseLockRefusedRemoteUnwindsEarlierLocks proves that a remote which
// answers but refuses the write stops the run exactly like contention does:
// nothing is planned or published, and every lock this run already owned is
// given back.
func TestReleaseLockRefusedRemoteUnwindsEarlierLocks(t *testing.T) {
	control, controlRemote, aRemote, zRemote := lockedFleet(t,
		"echo ran > ../../../../build-ran")
	rejectPushes(t, zRemote)

	res := releaseLocked(control)
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E336"), "stdout:\n%s", res.Stdout)
	assert.NoFileExists(t, control.Path("build-ran"),
		"a refused lock stops the run before any package script")
	assert.Empty(t, polyrepoTags(control, "sources/a"))
	assert.Empty(t, polyrepoTags(control, "sources/z"))
	assert.False(t, remoteHoldsLock(t, aRemote), "the earlier source lock must unwind")
	assert.False(t, remoteHoldsLock(t, zRemote))
	assertLockCleared(t, control, controlRemote)
}

// TestReleaseLockCancelledAcquisitionUnwinds proves that an interrupt during
// acquisition is the same refusal: the run never plans, and the locks it had
// already taken are given back even though the interrupt cancelled its
// context.
func TestReleaseLockCancelledAcquisitionUnwinds(t *testing.T) {
	control, controlRemote, aRemote, zRemote := lockedFleet(t, "echo building")
	delayPushes(t, zRemote)

	proc := control.StartReleaseEnv(harness.LockEnabled)
	require.Eventually(t, func() bool { return remoteHoldsLock(t, aRemote) },
		20*time.Second, 20*time.Millisecond, "the first source lock never appeared")
	proc.Signal(os.Interrupt)
	res := proc.Wait()

	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, -1, firstIndex(res.Events, planningStarted),
		"a cancelled acquisition never reaches planning")
	assert.False(t, remoteHoldsLock(t, aRemote), "the earlier source lock must unwind")
	assert.False(t, remoteHoldsLock(t, zRemote))
	assert.Empty(t, polyrepoTags(control, "sources/a"))
	assertLockCleared(t, control, controlRemote)
}

// TestReleaseLockCleanupPreservesAReplacedLock proves ownership is verified at
// cleanup rather than assumed: a lock replaced by another run while this one
// worked belongs to that run, and this run's cleanup leaves it alone, reports
// E336 and fails rather than claiming it returned its lock cleanly.
func TestReleaseLockCleanupPreservesAReplacedLock(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD")

	// The release holds the lock while its build runs; the replacement lands
	// in that window, exactly as a second machine's release would leave it.
	cfg.Scripts["steal"] = models.Script{
		"git -C " + bare + " tag -d " + lockTag,
		"git -C " + bare + " -c user.email=other@dispat.test -c 'user.name=other clone'" +
			" tag -a " + lockTag + " -m 'held by another release' " + harness.DefaultBranch,
	}
	cfg.Run = &models.RunConfig{PostAll: []string{"steal"}}
	r.WriteConfigModel(cfg)

	res := releaseLocked(r)
	assert.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, r.TagCount("core@"))
	assert.True(t, remoteHoldsLock(t, bare), "another run's lock must survive this run's cleanup")
	assert.Contains(t, res.Stdout, `"code":"E336"`)
	assert.Contains(t, res.Stdout, "could not remove the release lock tag from the remote",
		"the refused delete is reported rather than forced")
}
