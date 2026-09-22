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
	"errors"
	"fmt"

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

// resolveOwnershipCheck is the question a distributed run asks before every
// new assignment: does it still hold every lock it took.
//
// It is one query per owning repository and it asks the remote, because that
// is the only place the answer can have changed. A run with no lock at all
// asks nothing: the lock bypass is already refused for a run that delegates
// work, so the only caller that reaches this without a lock is one that is not
// dispatching anything.
func (a *App) resolveOwnershipCheck(fleet *workspaceRecorder) func(context.Context) error {
	locks := a.resolveHeldLocks(fleet)
	if len(locks) == 0 {
		return nil
	}
	return func(ctx context.Context) error {
		for _, held := range locks {
			isHeld, err := held.IsHeld(ctx)
			if err != nil {
				return fmt.Errorf("reading the release lock before starting a new effect: %w", err)
			}
			if !isHeld {
				return errors.New("the release lock this run acquired is no longer on the remote")
			}
		}
		return nil
	}
}

// resolveHeldLocks are the remote release locks this run acquired: the single
// history's own, or one per participating repository of a fleet.
func (a *App) resolveHeldLocks(fleet *workspaceRecorder) []*release.Lock {
	if fleet == nil {
		if a.releaseLock == nil {
			return nil
		}
		return []*release.Lock{a.releaseLock}
	}
	locks := make([]*release.Lock, 0, len(fleet.held))
	for _, held := range fleet.held {
		locks = append(locks, held.lock)
	}
	return locks
}
