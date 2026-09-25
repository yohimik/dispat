// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// Reading the lock back and naming the ownership it stands for.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readingLockGit answers the remote tag object from a script of reads and
// records each one. Each read takes the next answer in reads, and the last one
// repeats: a remote that fails twice and then answers is a list of three.
type readingLockGit struct {
	fakeLockGit
	reads []lockRead
	// hang makes every read wait for its context instead of answering, which
	// is what a remote that accepted the connection and then went silent does.
	hang bool
}

// lockRead is one answer of the fake remote: the object it advertises for the
// lock, or the failure reading it.
type lockRead struct {
	object string
	err    error
}

func (f *readingLockGit) RemoteTagObject(ctx context.Context, _, _ string) (string, error) {
	f.record("readObject")
	if f.hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if len(f.reads) == 0 {
		return "", nil
	}
	answer := f.reads[0]
	if len(f.reads) > 1 {
		f.reads = f.reads[1:]
	}
	return answer.object, answer.err
}

// readsOf counts the remote reads a fake answered.
func readsOf(git *readingLockGit) int {
	return strings.Count(strings.Join(git.calls, " "), "readObject")
}

func acquiredLock(t *testing.T, git LockGitx) *Lock {
	t.Helper()
	return acquiredLockLogging(t, git, &bytes.Buffer{})
}

func acquiredLockLogging(t *testing.T, git LockGitx, out *bytes.Buffer) *Lock {
	t.Helper()
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(out)}
	require.NoError(t, lock.Acquire(context.Background()))
	return lock
}

// shrinkLockVerify makes one verification's pauses and per-read deadline short
// enough to test, and puts the real values back afterwards.
func shrinkLockVerify(t *testing.T, readTimeout time.Duration) {
	t.Helper()
	reads, pauses, timeout := lockVerifyReads, lockVerifyPauses, lockVerifyReadTimeout
	lockVerifyPauses = []time.Duration{time.Millisecond, 2 * time.Millisecond}
	lockVerifyReadTimeout = readTimeout
	t.Cleanup(func() { lockVerifyReads, lockVerifyPauses, lockVerifyReadTimeout = reads, pauses, timeout })
}

// TestLockVerifyHeldComparesTheObjectOnTheRemote: ownership is a question about
// the remote, answered by the object id this run pushed and not by the local
// memory of having pushed it. Another object, or none, is a loss decided on
// the first read: reading again cannot change what was read.
func TestLockVerifyHeldComparesTheObjectOnTheRemote(t *testing.T) {
	shrinkLockVerify(t, time.Second)
	for name, tc := range map[string]struct {
		remote   string
		isLost   bool
		mentions string
	}{
		"the object this run pushed":       {remote: "object-id"},
		"a replaced lock, somebody else's": {remote: "another-object", isLost: true, mentions: "another-object"},
		"a remote carrying no lock":        {remote: "", isLost: true, mentions: "carries no"},
	} {
		t.Run(name, func(t *testing.T) {
			git := &readingLockGit{reads: []lockRead{{object: tc.remote}}}
			lock := acquiredLock(t, git)

			err := lock.VerifyHeld(context.Background())

			assert.Equal(t, 1, readsOf(git), "one remote read answers it")
			if !tc.isLost {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrLockLost)
			assert.NotErrorIs(t, err, ErrLockUnverified)
			assert.Contains(t, err.Error(), tc.mentions)
		})
	}
}

// TestLockVerifyHeldRetriesAFailedRead: a failed read says nothing about the
// lock, so one dropped connection does not stop a release. The remote is read
// again after a pause, each retry is said out loud at warn level, and a lock
// that is still there is still held.
func TestLockVerifyHeldRetriesAFailedRead(t *testing.T) {
	shrinkLockVerify(t, time.Second)
	unreachable := errors.New("the remote hung up")
	git := &readingLockGit{reads: []lockRead{{err: unreachable}, {err: unreachable}, {object: "object-id"}}}
	var out bytes.Buffer
	lock := acquiredLockLogging(t, git, &out)
	out.Reset()

	require.NoError(t, lock.VerifyHeld(context.Background()))

	assert.Equal(t, 3, readsOf(git), "error, error, held: three reads")
	assert.Equal(t, 2, strings.Count(out.String(), `"level":"warn"`), "each retry is a warn line: %s", out.String())
	assert.Contains(t, out.String(), "the remote hung up")
	assert.Contains(t, out.String(), "reading it again")
}

// TestLockVerifyHeldGivesUpAfterThreeReads: a remote that never answers is not
// a lock this run can show it owns. After the third failed read the answer is
// ErrLockUnverified, which is not a claim that the lock is gone, and the last
// failure travels with it.
func TestLockVerifyHeldGivesUpAfterThreeReads(t *testing.T) {
	shrinkLockVerify(t, time.Second)
	unreachable := errors.New("the remote hung up")
	git := &readingLockGit{reads: []lockRead{{err: unreachable}}}
	lock := acquiredLock(t, git)

	err := lock.VerifyHeld(context.Background())

	require.ErrorIs(t, err, ErrLockUnverified)
	assert.NotErrorIs(t, err, ErrLockLost, "an unread lock is not a lock read as gone")
	assert.ErrorIs(t, err, unreachable)
	assert.Equal(t, 3, readsOf(git))
}

// TestLockVerifyHeldBoundsAHungRead: a remote that accepts the connection and
// then says nothing would otherwise hold the verification, and every check of
// the run queued behind it, for as long as the network cares to. Each read has
// a deadline of its own, so three hung reads are one bounded answer.
func TestLockVerifyHeldBoundsAHungRead(t *testing.T) {
	shrinkLockVerify(t, 20*time.Millisecond)
	git := &readingLockGit{hang: true}
	lock := acquiredLock(t, git)

	done := make(chan error, 1)
	go func() { done <- lock.VerifyHeld(context.Background()) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrLockUnverified)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "the read's own deadline is what ended it")
		assert.Equal(t, 3, readsOf(git))
	case <-time.After(10 * time.Second):
		t.Fatal("a hung read was not bounded")
	}
}

// TestLockVerifyHeldCancellationIsNotALoss: a caller that stops asking has not
// learned anything about the remote. The context's own error comes back,
// whether the cancellation arrives during a read or during the pause before a
// retry, and it is neither a loss nor an unverified lock.
func TestLockVerifyHeldCancellationIsNotALoss(t *testing.T) {
	shrinkLockVerify(t, time.Second)

	t.Run("during a read", func(t *testing.T) {
		git := &readingLockGit{hang: true}
		lock := acquiredLock(t, git)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(10*time.Millisecond, cancel)

		err := lock.VerifyHeld(ctx)

		require.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, ErrLockLost)
		assert.NotErrorIs(t, err, ErrLockUnverified)
		assert.Equal(t, 1, readsOf(git), "nothing is read after the caller left")
	})

	t.Run("during the pause before a retry", func(t *testing.T) {
		lockVerifyPauses = []time.Duration{time.Hour}
		git := &readingLockGit{reads: []lockRead{{err: errors.New("the remote hung up")}}}
		lock := acquiredLock(t, git)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(10*time.Millisecond, cancel)

		err := lock.VerifyHeld(ctx)

		require.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, ErrLockUnverified)
		assert.Equal(t, 1, readsOf(git))
	})
}

// TestLockVerifyHeldWithoutAnAcquisition: a Lock nobody took, a bypassed
// repository or an acquisition that failed, owns nothing, and answering that
// touches no remote.
func TestLockVerifyHeldWithoutAnAcquisition(t *testing.T) {
	git := &readingLockGit{reads: []lockRead{{object: "object-id"}}}
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}

	err := lock.VerifyHeld(context.Background())

	require.ErrorIs(t, err, ErrLockLost)
	assert.Empty(t, git.calls, "an unheld lock asks the remote nothing")
	assert.Empty(t, lock.LockObject())
}

// TestResolveGenerationNamesTheOwnership: the generation is a function of
// which repositories are locked and by which objects, and of nothing else:
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
