// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Ownership checks before distributed assignments and release effects.

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// openOwnershipGate opens the gate every new effect of this release passes,
// once the locks are held and before anything is planned: each publication,
// whichever machine runs it, and each assignment of a distributed run, which
// borrows the same gate so that one loss is one decision (CCME §28.6).
//
// A run that holds no lock, a bypassed one, opens no gate and asks nothing.
func (a *App) openOwnershipGate(fleet *workspaceRecorder) {
	verify := a.resolveOwnershipCheck(fleet)
	if verify == nil {
		return
	}
	a.ownership = execution.NewOwnershipGate(a.runID, verify, a.log)
}

// resolveOwnershipCheck is the question a release asks before every new
// effect: does it still hold every lock it took.
//
// It is one verification per owning repository and it asks the remote,
// because that is the only place the answer can have changed. Each
// verification is bounded and retries a failed read before it gives up (see
// release.Lock.VerifyHeld), so an unreachable remote costs a bounded wait
// rather than every later check of the run. A run with no lock to read asks
// nothing: the lock bypass and a remote with `commit.verify` off are both
// refused for a run that delegates work, so the only caller that reaches this
// without a lock to read is one that is not dispatching anything.
func (a *App) resolveOwnershipCheck(fleet *workspaceRecorder) func(context.Context) error {
	locks := a.resolveVerifiedLocks(fleet)
	if len(locks) == 0 {
		return nil
	}
	return func(ctx context.Context) error {
		for _, held := range locks {
			if err := held.VerifyHeld(ctx); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("verifying the release lock before starting a new effect: %w", err)
			}
		}
		return nil
	}
}

// resolveVerifiedLocks are the remote release locks this run acquired and
// reads back: the single history's own, or one per participating repository
// of a fleet.
//
// A repository whose `commit.verify` is off is skipped, with one warning each.
// That setting is for a remote that rejects `ls-remote` and accepts pushes,
// and reading the lock back is another `ls-remote`: the exemption the upfront
// checks and the records comparison give that remote is the same exemption
// here, and it is said out loud for the reason theirs is, because a run that
// forgoes the read must not read as though ownership had been checked.
func (a *App) resolveVerifiedLocks(fleet *workspaceRecorder) []*release.Lock {
	if fleet == nil {
		if a.releaseLock == nil {
			return nil
		}
		if !a.cfg.Commit.IsVerifyEnabled() {
			warnOwnershipUnverified(a.log, a.releaseLock.Remote)
			return nil
		}
		return []*release.Lock{a.releaseLock}
	}
	locks := make([]*release.Lock, 0, len(fleet.held))
	for _, held := range fleet.held {
		if !held.repository.repo.Commit.IsVerifyEnabled() {
			warnOwnershipUnverified(held.repository.git.Log, held.lock.Remote)
			continue
		}
		locks = append(locks, held.lock)
	}
	return locks
}

// warnOwnershipUnverified is the one line a repository whose lock is not read
// back before its publications writes.
func warnOwnershipUnverified(log zerolog.Logger, remote string) {
	log.Warn().Str("tag", release.LockTagName).Str("remote", gitx.RedactURL(remote)).
		Msg("the release lock is not read back before each publication: commit.verify is off for this remote, " +
			"so a publication cannot be withheld when another run has taken the lock over")
}
