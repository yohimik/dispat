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

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// lockReader is the optional capability behind IsHeld: reading the object id
// a remote advertises for a tag.
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

// IsHeld reports whether the remote still carries the exact lock object this
// run pushed.
//
// One remote read, and it compares object ids rather than messages: the tag
// object is what acquisition contended for, so a remote carrying a different
// object carries somebody else's lock however similar it reads. A Lock that
// never acquired anything — a bypassed repository, or an acquisition that
// failed — is not held and says so without touching the network, which is
// what keeps the bypass a local matter.
func (l *Lock) IsHeld(ctx context.Context) (bool, error) {
	if !l.held {
		return false, nil
	}
	reader, isReadable := l.Git.(lockReader)
	if !isReadable {
		return false, ErrLockUnreadable
	}
	remote, err := reader.RemoteTagObject(ctx, l.Remote, LockTagName)
	if err != nil {
		return false, fmt.Errorf("reading the release lock tag on %s: %w", gitx.RedactURL(l.Remote), err)
	}
	return remote == l.oid, nil
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
