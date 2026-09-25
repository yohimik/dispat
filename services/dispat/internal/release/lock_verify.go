// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// Reading the lock back, and naming the ownership it stands for.
//
// Acquiring the lock proves who owned the repository at one instant. A run
// that spreads work over several machines needs the same answer later and
// more than once: CCME §28.6 authorizes each publication under the ownership
// the orchestrator still holds, so "do I still hold it" has to be a question
// with a remote answer rather than a local memory of having taken it.
//
// The generation is the other half. It names the ownership rather than the
// run, so that a receipt, a result or a cached authorization from an earlier
// acquisition is recognisably not this one's: the lock tag object embeds a
// fresh attempt id on every acquisition (see lockMessage), so a new
// acquisition always produces a new generation, even of the same commit by
// the same host in the same second.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// lockReader is the optional capability behind VerifyHeld: reading the object
// id a remote advertises for a tag.
//
// It is a capability interface rather than a method on LockGitx for the
// reason lockInspector is one: the fakes that stand in for git in tests
// implement the lock's own six operations, and widening the interface every
// one of them satisfies would make a new question a compilation error in code
// that has no opinion about it. *gitx.LocalGitx has it.
type lockReader interface {
	RemoteTagObject(ctx context.Context, remote, tag string) (string, error)
}

// ErrLockUnreadable is a Lock that cannot be checked because the Git behind
// it cannot read a remote tag. It is a capability statement rather than a
// verdict: answering "not held" would stop a release that is fine, and
// answering "held" would authorize a publication nobody owns, so neither is
// offered.
var ErrLockUnreadable = errors.New("release: this git cannot read the remote release lock")

// ErrLockLost is a verification that read the remote and found another lock
// object under the lock's name, or none at all: the exclusion this run took
// belongs to nobody or to somebody else now, and no answer read later can
// give it back.
var ErrLockLost = errors.New("release: the release lock this run acquired is no longer on the remote")

// ErrLockUnverified is a verification whose every read failed. It is not a
// statement that the lock is gone: the remote may well still carry it. It is
// a statement that this run cannot show that it still owns the repository,
// which is the condition a new effect needs, so a caller treats it as it
// treats a loss while saying which of the two it was.
var ErrLockUnverified = errors.New("release: the release lock could not be read back from the remote")

// The bounds of one verification. They are variables rather than constants so
// that a test can shrink them: the values are what an unreliable network
// deserves, and a test proving the retry would otherwise wait for them.
var (
	// lockVerifyReads is how many times one verification reads the remote
	// before it gives up on an answer.
	lockVerifyReads = 3
	// lockVerifyPauses are the waits before the second read and the third.
	lockVerifyPauses = []time.Duration{time.Second, 2 * time.Second}
	// lockVerifyReadTimeout bounds one read, so that a remote that accepts the
	// connection and then says nothing costs a bounded wait rather than every
	// later check of the run queued behind it.
	lockVerifyReadTimeout = 15 * time.Second
)

// VerifyHeld answers whether the remote still carries the exact lock object
// this run pushed: nil when it does, an error saying why not otherwise.
//
// It compares object ids rather than messages: the tag object is what
// acquisition contended for, so a remote carrying a different object carries
// somebody else's lock however similar it reads. That answer, and a remote
// carrying no lock at all, is ErrLockLost at once, because reading again
// cannot change what was read. A read that fails is different: it says
// nothing about the lock, and one dropped connection is not a reason to stop
// a release, so it is read again, up to lockVerifyReads times with the pauses
// between them, each read bounded on its own. Only when every read failed is
// the answer ErrLockUnverified, and every retry is logged at warn level so an
// operator sees an unreliable remote before it costs a run.
//
// A caller that cancels its context gets the context's error, which is not a
// loss: an interrupted lookup establishes nothing about the remote. A Lock
// that never acquired anything, a bypassed repository or an acquisition that
// failed, owns nothing and says so without touching the network, and a git
// that cannot read tags at all is ErrLockUnreadable at once, since no retry
// gives it the capability.
func (l *Lock) VerifyHeld(ctx context.Context) error {
	if !l.held {
		return fmt.Errorf("%w: this run holds no lock on %s", ErrLockLost, gitx.RedactURL(l.Remote))
	}
	remote, err := l.readRemoteObject(ctx)
	if err != nil {
		return err
	}
	return l.compareLockObject(remote)
}

// readRemoteObject reads the object the remote advertises for the lock tag,
// empty when it carries none. A read that fails is read again, up to
// lockVerifyReads times with the pauses between them, each read bounded on its
// own and every retry logged at warn level. It answers ErrLockUnreadable at
// once for a git that cannot read a remote tag, the caller's context error
// when the caller stops asking, and ErrLockUnverified when every read failed.
//
// It asks nothing about ownership, which is why an acquisition whose push
// answer was lost can use it before it holds anything.
func (l *Lock) readRemoteObject(ctx context.Context) (string, error) {
	reader, isReadable := l.Git.(lockReader)
	if !isReadable {
		return "", ErrLockUnreadable
	}
	var failure error
	for read := 1; read <= lockVerifyReads; read++ {
		if read > 1 {
			if err := waitLockVerifyPause(ctx, read-2); err != nil {
				return "", err
			}
		}
		readCtx, cancelRead := context.WithTimeout(ctx, lockVerifyReadTimeout)
		remote, err := reader.RemoteTagObject(readCtx, l.Remote, LockTagName)
		cancelRead()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if err == nil {
			return remote, nil
		}
		failure = err
		if read < lockVerifyReads {
			l.Log.Warn().Err(err).Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
				Int("read", read).Int("reads", lockVerifyReads).
				Msg("the release lock could not be read; reading it again")
		}
	}
	return "", fmt.Errorf("%w: %d reads of %s failed, the last with: %w",
		ErrLockUnverified, lockVerifyReads, gitx.RedactURL(l.Remote), failure)
}

// compareLockObject turns one successful read of the remote into the answer:
// nil for the object this run pushed, ErrLockLost for anything else.
func (l *Lock) compareLockObject(remote string) error {
	if remote == l.oid {
		return nil
	}
	if remote == "" {
		return fmt.Errorf("%w: %s carries no %s", ErrLockLost, gitx.RedactURL(l.Remote), LockTagName)
	}
	return fmt.Errorf("%w: %s carries another %s object, %s", ErrLockLost, gitx.RedactURL(l.Remote),
		LockTagName, remote)
}

// waitLockVerifyPause waits out the pause before one retry, or answers the
// caller's cancellation if it comes first.
func waitLockVerifyPause(ctx context.Context, index int) error {
	if len(lockVerifyPauses) == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(lockVerifyPauses[min(index, len(lockVerifyPauses)-1)])
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ResolveGeneration names the ownership a set of held locks stands for: the
// lowercase hex SHA-256 over one sorted `<repository>=<lock tag object>` line
// per lock.
//
// The repository identity is the key of the map, empty for the single history
// that has no other name for itself, so a fleet's generation changes when any
// participant's lock changes and a single repository's changes when its own
// does. A lock that is not held contributes its empty object id, which is
// what makes "released under a bypass" a generation of its own rather than an
// accidental match with a locked run.
func ResolveGeneration(locks map[string]*Lock) string {
	lines := make([]string, 0, len(locks))
	for repository, lock := range locks {
		lines = append(lines, repository+"="+lock.LockObject()+"\n")
	}
	sort.Strings(lines)
	sum := sha256.New()
	for _, line := range lines {
		sum.Write([]byte(line))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// LockObject is the tag object this run offered the remote, empty for a Lock
// that holds nothing and for no Lock at all. It is exported so that the
// generation and the checks built on it can be computed outside this package
// without any of them being able to change what the lock believes it holds.
func (l *Lock) LockObject() string {
	if l == nil || !l.held {
		return ""
	}
	return l.oid
}
