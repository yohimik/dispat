// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Asking every configured node what it is, before anything is placed
// anywhere.
//
// §28.2 requires the orchestrator to validate the effective configuration,
// the protocol compatibility and the output-transfer capability of its pool
// before dispatch, and to fail rather than silently drop a node. This is
// where a release does that: after the plan is fixed and the working trees
// have been checked, before the first hook of the run. A node that cannot
// answer, or that could not take what this plan would put on it, fails the
// release with nothing published, nothing tagged and the locks given back by
// the cleanup the release path already has.
//
// With no worker links every function here returns before it does anything,
// which is what keeps a repository that never asked for distributed execution
// paying nothing for it.

import (
	"context"
	"os"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// preflightWorkers probes every configured worker node and answers the
// coordinator that owns this run's refs, or nil when the run delegates
// nothing.
func (a *App) preflightWorkers(ctx context.Context, pl *plan.Plan, fleet *workspaceRecorder) (*execution.Coordinator, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	coordinator, err := a.newCoordinator(fleet)
	if err != nil {
		return nil, a.reportPreflightFailure(err)
	}
	if err := coordinator.Preflight(ctx, planPlatforms(pl)); err != nil {
		// The refs this run offered are closed by the caller's own deferred
		// cleanup, so a refusal here leaves nothing behind even though it
		// returns before anything else runs.
		return coordinator, a.reportPreflightFailure(err)
	}
	return coordinator, nil
}

// newCoordinator assembles this run's coordinator: who it is, what plan it
// executes, which ownership it holds, and one mailbox per configured link.
//
// The local object store of every mailbox is the repository being released.
// Transport objects are unreachable there the moment their refs are deleted,
// so they cost a `git gc` and nothing else, and using the checkout that is
// already open is what keeps a release from needing a second store of its own.
func (a *App) newCoordinator(fleet *workspaceRecorder) (*execution.Coordinator, error) {
	settings := a.cfg.Execution
	signer, err := execution.NewSigner(os.Getenv(settings.SecretEnv))
	if err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"execution.secretEnv names %s and it is unset or empty in this environment: %w",
			settings.SecretEnv, err)
	}
	links := make([]execution.Link, 0, len(settings.Workers))
	mailboxes := make(map[string]*execution.GitMailbox, len(settings.Workers))
	for _, worker := range settings.Workers {
		links = append(links, execution.Link{Name: worker.Name, Endpoint: worker.Endpoint})
		mailboxes[worker.Name] = execution.NewGitMailbox(worker.Endpoint, a.git, signer, a.log)
	}
	timeouts := settings.ResolveTimeouts()
	return execution.NewCoordinator(a.runID, a.planDigest, a.resolveOwnershipGeneration(fleet),
		links, mailboxes, execution.Timeouts{
			Preflight: time.Duration(timeouts.Preflight) * time.Second,
			Task:      time.Duration(timeouts.Task) * time.Second,
			Cancel:    time.Duration(timeouts.Cancel) * time.Second,
		}, formatTransferLimits(settings), a.log), nil
}

// resolveOwnershipGeneration names the exclusion this run holds (§28.3): the
// lock objects of every participating repository, or of the single history
// that has no other name for itself.
//
// It reads the locks the acquisition recorded rather than the remote, because
// the generation names what this run took: asking the remote again would
// answer what is there now, which is the question IsHeld exists for.
func (a *App) resolveOwnershipGeneration(fleet *workspaceRecorder) string {
	if fleet == nil {
		return release.ResolveGeneration(map[string]*release.Lock{"": a.releaseLock})
	}
	locks := make(map[string]*release.Lock, len(fleet.held))
	for _, held := range fleet.held {
		locks[held.repository.repo.Name] = held.lock
	}
	return release.ResolveGeneration(locks)
}

// planPlatforms is what every releasing package requires of the node that
// would build it. A package that requires nothing is still listed, because
// "any node" is only satisfiable when there is a node at all.
func planPlatforms(pl *plan.Plan) []execution.PackagePlatforms {
	releasing := pl.Releasing()
	wanted := make([]execution.PackagePlatforms, 0, len(releasing))
	for _, rel := range releasing {
		wanted = append(wanted, execution.PackagePlatforms{
			Package: rel.Pkg.Name, Platforms: rel.Pkg.Space.BuildPlatforms,
		})
	}
	return wanted
}

// reportPreflightFailure writes the refusal where it is decided, with the
// code, the class and the work it is about already on it.
func (a *App) reportPreflightFailure(err error) error {
	a.logError(err).Msg("cannot dispatch this release")
	return err
}

// closeCoordinator deletes every coordination ref this run created.
//
// It runs on a context detached from cancellation, like every other
// finalization in the release path: an interrupted run has more reason to
// clean its mailboxes than a finished one. A ref that survives is a warning
// and never a failed release, because a coordination branch carries no
// release record: whatever it says about this run, the tags and the records
// are what a release is.
func (a *App) closeCoordinator(ctx context.Context, coordinator *execution.Coordinator) {
	if err := coordinator.Close(context.WithoutCancel(ctx)); err != nil {
		event := a.log.Warn().Err(err).Str("code", execution.CodeTransportRetained).
			Str("category", execution.CategoryTransportCleanup)
		execution.AttachIdentity(event, err).Msg("coordination branches were not closed")
	}
}
