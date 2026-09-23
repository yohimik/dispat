// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The one thing a distributed run can leave behind on purpose: a release lock
// (CCME §28.6).
//
// Every other piece of state a run creates is either durable and authoritative
// (a tag, a record, a release commit) or temporary and disposable (a
// coordination branch, a fetched object, a worktree). A lock is neither. It is
// an exclusion, and giving it back is a statement: nothing this run authorized
// is still happening. For almost every run that statement is free, because
// everything the run started has reported. It is not free for exactly one
// situation, which is a publisher that was authorized and never answered, and
// there the specification is unambiguous: the run must fail and retain the
// exclusion rather than report successful cleanup and permit overlapping
// effects.
//
// So the unlock path asks the run whether it may. The question is asked of an
// interface declared here, at the consumer, because it is the lock's question
// and not the transport's: which of the locks I hold may I release. A run with
// no coordinator answers it by not being asked.

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// lockRetentionx answers which repositories' exclusion has to survive this
// run, by repository name.
//
// It is one method because the unlock path needs one fact, and it is an
// interface rather than the coordinator's own type so that a run without
// distributed execution carries nothing: the field is nil, the question is
// never asked, and the lock path is the one it has always been.
type lockRetentionx interface {
	RetainedRepositories() []string
}

// isLockRetained reports whether the release lock of one repository must be
// left on its remote.
//
// A single history has one lock and no name for itself, so any retained
// repository is that one: the empty name is what the plan calls a package's
// repository when there is only one, and comparing it against a name would
// answer no for the one case that matters.
func (a *App) isLockRetained(repository string) bool {
	if a.retention == nil {
		return false
	}
	retained := a.retention.RetainedRepositories()
	if len(retained) == 0 {
		return false
	}
	if a.workspace == nil {
		return true
	}
	for _, name := range retained {
		if name == repository {
			return true
		}
	}
	return false
}

// reportRetainedLock writes the diagnostic a left-behind lock owes an
// operator, and answers the failure the run carries.
//
// The remedy is an order of steps rather than an instruction to delete a tag,
// because §28.6 requires exactly that: the evidence for the one question that
// has to be answered lives in this run's own coordination refs, as an
// authorization with no result beside it, and a remedy that said only "delete
// the lock" would be telling an operator to hand a repository to the next run
// while a publisher may still be writing to a registry.
func (a *App) reportRetainedLock(repository, remote string) error {
	retained := execution.NewIdentifiedDiagnostic(execution.Identity{Run: a.runID},
		execution.CodePublicationUnknown, execution.CategoryPublicationUnknown,
		"the release lock of %s is left on its remote because this run authorized a publication it could not account for: list the coordination refs of run %s (dispat-worker-*), find the authorization with no result beside it, confirm on that node that the publisher has stopped, check the registry for the version, delete the run's refs, and only then delete the lock tag %s on the remote",
		formatRetainedRepository(repository), a.runID, release.LockTagName)
	event := a.logError(retained).Str("tag", release.LockTagName).
		Str("remedy", "packages/docs/docs/reference/releasing/release-lock.md")
	if remote != "" {
		event = event.Str("remote", gitx.RedactURL(remote))
	}
	event.Msg("release lock retained")
	return retained
}

// formatRetainedRepository names the repository a retained lock belongs to,
// and names a single history as itself: a run with one repository has no other
// word for it, and an empty one in the sentence would read as a bug.
func formatRetainedRepository(repository string) string {
	if repository == "" {
		return "this repository"
	}
	return repository
}

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
