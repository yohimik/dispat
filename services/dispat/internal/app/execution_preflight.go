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
	"errors"
	"os"
	"time"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type releasePreflightRequest struct {
	plan          *plan.Plan
	mode          string
	fleet         *workspaceRecorder
	compiled      *release.CompiledTaskGraph
	returnChannel *runReturnChannel
	pinnedSources *execution.PinnedRunSources
	pinnedInputs  *execution.PinnedRunInputs
}

const invalidExecutionAdmissionPolicyErrorFormat = "execution admission policy is invalid: %w"
const invalidExecutionCoordinationEndpointErrorFormat = "execution coordination endpoint is invalid: %w"

// preflightWorkers probes every configured worker and answers the
// coordinator that owns this run's refs, or nil when the run delegates
// nothing.
func (a *App) preflightWorkers(ctx context.Context, request releasePreflightRequest) (*execution.Coordinator, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	pl, fleet, compiled, returnChannel := request.plan, request.fleet, request.compiled, request.returnChannel
	if compiled != nil && len(compiled.TaskShapes()) == choreographyNoEntriesCount {
		return nil, nil
	}
	// The links reach the release remote at the destination this run's own
	// lock was taken on, the destination its cleanup reaches too.
	remote := a.coordination
	if locked := a.resolveLockedEndpoint(fleet); locked != "" {
		remote.url = locked
	}
	coordinator, err := a.newCoordinator(a.resolveOwnershipGeneration(fleet), a.ownership, remote)
	if err != nil {
		return nil, a.reportPreflightFailure(err)
	}
	a.coordinator = coordinator
	if err := setChoreographyCoordinatorMode(coordinator, request.mode, a.cfg.Execution); err != nil {
		return coordinator, a.reportPreflightFailure(err)
	}
	coordinator.PinnedInputs = request.pinnedInputs
	if compiled == nil {
		return coordinator, a.reportPreflightFailure(execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			preflightMissingGraphErrorText))
	}
	_, _, _, graphDigest, err := a.releaseChoreographyInventory(coordinator, pl, compiled)
	if err != nil {
		return coordinator, a.reportPreflightFailure(err)
	}
	coordinator.GraphDigest = graphDigest
	if returnChannel == nil {
		return coordinator, a.reportPreflightFailure(execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			preflightMissingReturnChannelErrorText))
	}
	coordinator.ReturnEndpoint = returnChannel.endpoint
	coordinator.ReturnCertSHA256 = returnChannel.certPin
	coordinator.PinnedSources = request.pinnedSources
	if err := a.prepareRunChoreographyBootstrap(ctx, coordinator); err != nil {
		return coordinator, a.reportPreflightFailure(err)
	}
	if err := a.bindProbeReturnHandler(coordinator, returnChannel); err != nil {
		return coordinator, a.reportPreflightFailure(err)
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
// executes, which ownership it holds and the gate that ownership is verified
// through again, and one mailbox per configured link, each reached at the
// link's own endpoint or, for a link that states none, at the release remote
// handed in, whose push URL is held to the rules of an endpoint once more.
//
// The ownership is the caller's, because the two runs that dispatch hold
// different things: a release holds the locks it acquired, and the coordinator
// borrows the release's own gate, the one every publication of the run passes,
// so that a loss decided on either path is the loss both read. A sweep holds no
// lock, hands in no gate, and binds its messages to a generation drawn from its
// own run identity (§28.10).
//
// The local object store of every mailbox is the repository being released.
// Transport objects are unreachable there the moment their refs are deleted,
// so they cost a `git gc` and nothing else, and using the checkout that is
// already open is what keeps a release from needing a second store of its own.
func (a *App) newCoordinator(generation string, ownership *execution.OwnershipGate,
	remote coordinationRemote) (*execution.Coordinator, error) {
	settings := a.cfg.Execution
	signer, err := execution.NewSigner(os.Getenv(settings.SecretEnv))
	if err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"execution.secretEnv names %s and it is unset or empty in this environment: %w",
			settings.SecretEnv, err)
	}
	links, err := a.formatWorkerLinks(remote)
	if err != nil {
		return nil, err
	}
	mailboxes := make(map[string]*execution.GitMailbox, len(links))
	for _, link := range links {
		mailboxes[link.Name] = execution.NewGitMailbox(link.Endpoint, a.git, signer, a.log)
	}
	timeouts := settings.ResolveTimeouts()
	localSlots, err := openSharedNodeSlots(a.sender.Node, settings.ResolveConcurrency())
	if err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			firstNodeCapacityUnavailableErrorFormat, err)
	}
	a.localNodeSlots = localSlots
	// This machine joins its own pool: it is a node under the same rules
	// (§28.1), and the name it joins under is the one it already writes on
	// every line of this run.
	local := execution.LocalNode{Name: a.sender.Node, Capacity: settings.ResolveConcurrency(),
		AuthorityID: localSlots.AuthorityID()}
	coordinator := execution.NewCoordinator(a.runID, a.planDigest, generation,
		local, links, mailboxes, signer, execution.Timeouts{
			Preflight: time.Duration(timeouts.Preflight) * time.Second,
			Task:      time.Duration(timeouts.Task) * time.Second,
			Cancel:    time.Duration(timeouts.Cancel) * time.Second,
		}, formatTransferLimits(settings), a.log)
	var canonicalEndpoint string
	var canonicalMailbox *execution.GitMailbox
	authorized := make([]execution.ChoreographyAuthorizedNode, choreographyNoEntriesCount, len(links))
	for _, link := range links {
		if canonicalEndpoint == choreographyAbsentIdentityText {
			canonicalEndpoint = link.Endpoint
			canonicalMailbox = mailboxes[link.Name]
		}
		authorized = append(authorized, execution.ChoreographyAuthorizedNode{
			Name: link.Name, CoordinationEndpoint: link.Endpoint, LateJoin: link.LateJoin,
			IdentityPublicKey: link.IdentityPublicKey,
		})
	}
	coordinator.AlgorithmVersion = execution.ChoreographyGraphAlgorithmVersion
	coordinator.JoinPolicy = execution.ChoreographyJoinPolicy{
		AdmissionDurationSeconds:   timeouts.Admission,
		RetryAfterUncertainAttempt: settings.RetryAfterUncertainAttempt,
		Authorized:                 authorized,
	}
	if err := coordinator.JoinPolicy.Validate(canonicalEndpoint); err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			invalidExecutionAdmissionPolicyErrorFormat, err)
	}
	if err := coordinator.SetCanonicalChoreographyMailbox(canonicalEndpoint, canonicalMailbox); err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			invalidExecutionCoordinationEndpointErrorFormat, err)
	}
	coordinator.UseOwnership(ownership)
	return coordinator, nil
}

func setChoreographyCoordinatorMode(coordinator *execution.Coordinator,
	mode string, settings *models.ExecutionConfig) error {
	coordinator.Mode = mode
	coordinator.JoinPolicy.DistributionMode = mode
	coordinator.JoinPolicy.MaxAttempts = settings.ResolveMaxAttempts()
	if err := coordinator.JoinPolicy.Validate(coordinator.CanonicalEndpoint); err != nil {
		return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			invalidExecutionAdmissionPolicyErrorFormat, err)
	}
	return nil
}

// resolveOwnershipGeneration names the exclusion this run holds (§28.3): the
// lock objects of every participating repository, or of the single history
// that has no other name for itself.
//
// It reads the locks the acquisition recorded rather than the remote, because
// the generation names what this run took: asking the remote again would
// answer what is there now, which is the question VerifyHeld exists for.
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
// would build it, and where the run is allowed to place that build. A package
// that requires nothing is still listed, because "any node" is only
// satisfiable when there is a node at all.
func planPlatforms(pl *plan.Plan) []execution.PackagePlatforms {
	releasing := pl.Releasing()
	wanted := make([]execution.PackagePlatforms, 0, len(releasing))
	for _, rel := range releasing {
		space := rel.Pkg.Space
		wanted = append(wanted, execution.PackagePlatforms{
			Package:   rel.Pkg.Name,
			Platforms: space.BuildPlatforms,
			Placement: execution.ResolveStagePlacement(execution.StageBuild,
				space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0),
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

// taskInitializationFailure labels a run that cannot establish all required
// task refs before its first frame. An unknown push remains unknown in the
// wrapped cause; this diagnostic does not claim the remote refused or lacks
// any particular ref. Close still reports unresolved cleanup separately.
func taskInitializationFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || config.DiagnosticCode(err) != "" {
		return err
	}
	return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
		"execution task branch initialization could not be established before dispatch: %w", err)
}

// coordinationCloseTimeout bounds the close of this run's coordination refs.
//
// The close stops the pollers and deletes the run's settled branches with one
// batched push per mailbox, so it is a few round trips to remotes the run was
// already talking to. Two minutes covers several slow mailboxes with room to
// spare, and it is a bound that matters: the close runs before the release
// locks go back, so a push that never answers would otherwise hold every lock
// of the run for as long as it liked.
var coordinationCloseTimeout = 2 * time.Minute

// coordinationClosex is the one thing the way out of a run asks of its
// coordinator: close what it owns. *execution.Coordinator has it.
type coordinationClosex interface {
	Close(ctx context.Context) error
}

// closeCoordinator deletes settled coordination refs this run created and
// preserves an unknown publisher's branch for reconciliation.
//
// It runs on a context detached from cancellation, like every other
// finalization in the release path: an interrupted run has more reason to
// clean its mailboxes than a finished one. It is bounded by
// coordinationCloseTimeout, because it runs before the locks go back. An
// unexpected ref that survives is a warning; an unknown publisher's branch is
// retained on purpose for reconciliation and a possible late result. Neither
// is a release record.
//
// Only the first call closes anything. The closing phase closes the
// coordinator explicitly before it gives the locks back, and the deferred
// call that covers every earlier return then has nothing left to do.
func (a *App) closeCoordinator(ctx context.Context, coordinator coordinationClosex) {
	if a.isCoordinatorClosed {
		return
	}
	a.isCoordinatorClosed = true
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinationCloseTimeout)
	defer cancel()
	if err := coordinator.Close(closeCtx); err != nil {
		if !errors.Is(err, execution.ErrChoreographyProbeCleanup) {
			a.coordinatorCloseErr = err
		}
		event := a.log.Warn().Err(err).Str("code", execution.CodeTransportRetained).
			Str("category", execution.CategoryTransportCleanup)
		execution.AttachIdentity(event, err).Msg("coordination branches were not closed")
	}
}
