// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// Reading the lock back and naming the ownership it stands for.

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readingLockGit answers the remote tag object, which is the one capability
// IsHeld needs and the plain fake deliberately does not have.
type readingLockGit struct {
	fakeLockGit
	remoteObject string
	readErr      error
}

func (f *readingLockGit) RemoteTagObject(context.Context, string, string) (string, error) {
	f.record("readObject")
	return f.remoteObject, f.readErr
}

func acquiredLock(t *testing.T, git LockGitx) *Lock {
	t.Helper()
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}
	require.NoError(t, lock.Acquire(context.Background()))
	return lock
}

// TestLockIsHeldComparesTheObjectOnTheRemote: ownership is a question about
// the remote, answered by the object id this run pushed and not by the local
// memory of having pushed it.
func TestLockIsHeldComparesTheObjectOnTheRemote(t *testing.T) {
	for name, tc := range map[string]struct {
		remote string
		want   bool
	}{
		"the object this run pushed": {"object-id", true},
		"somebody else's lock":       {"another-object", false},
		"a remote carrying no lock":  {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			git := &readingLockGit{remoteObject: tc.remote}
			lock := acquiredLock(t, git)
			isHeld, err := lock.IsHeld(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.want, isHeld)
			assert.Contains(t, git.calls, "readObject", "one remote read answers it")
		})
	}
}

// TestLockIsHeldWithoutAnAcquisition: a Lock nobody took — a bypassed
// repository, or an acquisition that failed — is not held, and answering that
// touches no remote.
func TestLockIsHeldWithoutAnAcquisition(t *testing.T) {
	git := &readingLockGit{remoteObject: "object-id"}
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}

	isHeld, err := lock.IsHeld(context.Background())
	require.NoError(t, err)
	assert.False(t, isHeld)
	assert.Empty(t, git.calls, "an unheld lock asks the remote nothing")
	assert.Empty(t, lock.LockObject())
}

// TestLockIsHeldReportsWhatItCannotRead: neither a failed read nor a git that
// cannot read tags at all is turned into a verdict, because both verdicts are
// dangerous — one stops a healthy release and the other authorizes a
// publication nobody owns.
func TestLockIsHeldReportsWhatItCannotRead(t *testing.T) {
	failing := errors.New("the remote is unreachable")
	unreachable := acquiredLock(t, &readingLockGit{readErr: failing})
	isHeld, err := unreachable.IsHeld(context.Background())
	assert.False(t, isHeld)
	assert.ErrorIs(t, err, failing)

	narrow := acquiredLock(t, &fakeLockGit{})
	isHeld, err = narrow.IsHeld(context.Background())
	assert.False(t, isHeld)
	assert.ErrorIs(t, err, ErrLockUnreadable)
}

// TestResolveGenerationNamesTheOwnership: the generation is a function of
// which repositories are locked and by which objects, and of nothing else —
// not of the order they are read in, and not of the run that holds them.
func TestResolveGenerationNamesTheOwnership(t *testing.T) {
	sdk := acquiredLock(t, &readingLockGit{})
	app := acquiredLock(t, &readingLockGit{})
	app.oid = "another-object"

	generation := ResolveGeneration(map[string]*Lock{"sdk": sdk, "app": app})
	assert.Len(t, generation, 64)
	assert.Equal(t, generation, ResolveGeneration(map[string]*Lock{"app": app, "sdk": sdk}),
		"a map is read in sorted order, so two readings of one fleet agree")
	assert.NotEqual(t, generation, ResolveGeneration(map[string]*Lock{"sdk": sdk}),
		"a fleet of one is not the same ownership as a fleet of two")
	assert.NotEqual(t, ResolveGeneration(map[string]*Lock{"": sdk}), generation,
		"the repository a lock belongs to is part of what it names")
}

// TestResolveGenerationChangesWithEveryAcquisition: the lock tag carries a
// fresh attempt id, so two acquisitions of one repository are two
// generations. That is what makes a receipt from the previous acquisition
// recognisable rather than plausible.
func TestResolveGenerationChangesWithEveryAcquisition(t *testing.T) {
	git := &attemptedLockGit{}
	first := ResolveGeneration(map[string]*Lock{"": acquiredLock(t, git)})
	second := ResolveGeneration(map[string]*Lock{"": acquiredLock(t, git)})
	assert.NotEqual(t, first, second)

	bypassed := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}
	assert.NotEqual(t, first, ResolveGeneration(map[string]*Lock{"": bypassed}),
		"a repository released without a lock is its own generation")
}

// attemptedLockGit is the fake with the property the real tag object has:
// every acquisition resolves to a different object, because the message the
// tag carries holds an attempt id nothing else ever repeats.
type attemptedLockGit struct {
	readingLockGit
	acquisitions int
}

func (f *attemptedLockGit) TagObject(context.Context, string) (string, error) {
	f.acquisitions++
	return "object-" + string(rune('a'+f.acquisitions)), nil
}
