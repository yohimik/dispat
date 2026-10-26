package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	priorFirstRunAlreadyActiveError                 = "execution: first-node run is already active in this process"
	priorFirstRunIncompleteRemoteStateErrorFormat   = "execution: prior first-node run %s has incomplete remote state; exact reconciliation is required"
	priorReleaseInvalidPreparedLockErrorFormat      = "execution: prior release run %s has an invalid prepared lock for %q"
	priorReleaseLockReadErrorFormat                 = "execution: prior release lock for %q cannot be read: %w"
	priorReleaseLockReplacedErrorFormat             = "execution: prior release lock for %q was replaced; another run's lock is retained"
	priorReleaseLockSettleErrorFormat               = "execution: prior release lock for %q cannot be settled after delete: %w"
	priorSweepMissingSecretReferenceErrorFormat     = "execution: prior sweep %s has no current signing secret reference"
	priorSweepSigningKeyUnavailableErrorFormat      = "execution: prior sweep signing key is unavailable: %w"
	retainedSweepBootstrapUnauthenticErrorFormat    = "execution: retained sweep bootstrap is not authentic: %w"
	retainedSweepBootstrapIdentityMismatchError     = "execution: retained sweep bootstrap differs from protected identity"
	retainedSweepSelectionMismatchErrorFormat       = "execution: retained run is not a sweep: %w"
	retainedSweepLedgerReadMismatchErrorFormat      = "execution: retained sweep ledger differs from journal: %w"
	retainedSweepLedgerIdentityMismatchError        = "execution: retained sweep ledger differs from journal"
	retainedSweepReplanFailureErrorFormat           = "execution: retained sweep cannot be independently replanned: %w"
	retainedSweepRosterPolicyReadErrorFormat        = "execution: retained sweep task policy differs from signed roster: %w"
	retainedSweepRosterPolicyMismatchError          = "execution: retained sweep task policy differs from signed roster"
	retainedSweepOwnershipLostErrorFormat           = "execution: retained sweep lost protected first-node ownership: %w"
	retainedSweepOwnershipLostError                 = "execution: retained sweep lost protected first-node ownership"
	retainedSweepBrokerReplayErrorFormat            = "execution: retained sweep broker replay failed: %w"
	retainedSweepClaimFenceErrorFormat              = "execution: retained sweep claims could not be fenced: %w"
	retainedSweepUnsettledAcceptedWorkErrorFormat   = "execution: retained sweep has unsettled accepted work: %w"
	retainedFirstRosterMissingAuthorityError        = "execution: retained first-node roster has no physical authority"
	retainedFirstCapacityOpenErrorFormat            = "execution: opening retained first-node capacity: %w"
	retainedFirstAuthorityMismatchError             = "execution: retained first-node physical authority differs from signed roster"
	retainedFirstCapacityAuthenticationErrorFormat  = "execution: retained first-node capacity cannot be authenticated: %w"
	retainedFirstCapacityInspectionErrorFormat      = "execution: retained first-node capacity cannot be inspected: %w"
	retainedFirstCapacityWithoutEvidenceErrorFormat = "execution: prior run %s retains task %s capacity without exact settled evidence"
)

// reconcilePriorFirstRun runs before a new run ID is allocated. The stable OS
// gate excludes another first node in this checkout while the retained Git
// state is inspected. A prior accepted attempt is never inferred to have
// stopped merely because its HTTPS listener is gone.
func (a *App) reconcilePriorFirstRun(ctx context.Context) error {
	if a.returnJournal != nil {
		return fmt.Errorf(priorFirstRunAlreadyActiveError)
	}
	if _, err := a.git.GitDir(ctx); err != nil {
		if a.cfg.Execution == nil || !a.cfg.Execution.IsDistributed() {
			// Local scripts outside Git have no private Git directory in which
			// a first-node locator could have been published.
			return nil
		}
		return err
	}
	journal, identity, err := reopenActiveFirstRunIdentity(ctx, a.git)
	if err != nil || journal == nil {
		return err
	}
	defer journal.close()
	if identity.AdmissionState == firstRunAdmissionInertState {
		if err := a.reconcileUnstartedReleaseLocks(ctx, identity); err != nil {
			return err
		}
		return journal.markCompleted()
	}
	if identity.BootstrapOID == retainedRunAbsentValueText && identity.LedgerRootOID == retainedRunAbsentValueText &&
		len(identity.PreparedLocks) == retainedRunNoEntriesCount {
		// No lock attempt and no protocol ref was prepared. The journal and
		// certificate are the only durable effects of the interrupted run.
		return journal.markCompleted()
	}
	if identity.BootstrapOID == retainedRunAbsentValueText && identity.LedgerRootOID == retainedRunAbsentValueText &&
		len(identity.PreparedLocks) != retainedRunNoEntriesCount {
		// A lock attempt was written ahead of its remote push, but no
		// choreography frame or native command can have started yet. Settle
		// only this exact lock identity; a moved lock is never ours to delete.
		if err := a.reconcileUnstartedReleaseLocks(ctx, identity); err != nil {
			return err
		}
		return journal.markCompleted()
	}
	if identity.BootstrapOID == retainedRunAbsentValueText || identity.LedgerRootOID == retainedRunAbsentValueText {
		return fmt.Errorf(priorFirstRunIncompleteRemoteStateErrorFormat, identity.Run)
	}
	if len(identity.PreparedLocks) != retainedRunNoEntriesCount {
		return a.reconcileCompletedRelease(ctx, journal, identity)
	}
	return a.reconcilePriorSweep(ctx, journal, identity)
}

func (a *App) reconcileUnstartedReleaseLocks(ctx context.Context,
	identity firstRunIdentity) error {
	return reconcileUnstartedReleaseLocksWithGit(ctx, identity, a.git)
}

type preparedLockRemote interface {
	RemoteTagObject(context.Context, string, string) (string, error)
	DeleteRemoteTagLease(context.Context, string, string, string) error
}

func reconcileUnstartedReleaseLocksWithGit(ctx context.Context,
	identity firstRunIdentity, remote preparedLockRemote) error {
	for repository, prepared := range identity.PreparedLocks {
		if prepared.Run != identity.Run || prepared.Remote == retainedRunAbsentValueText || prepared.Object == retainedRunAbsentValueText ||
			prepared.LocalTag == retainedRunAbsentValueText {
			return fmt.Errorf(priorReleaseInvalidPreparedLockErrorFormat,
				identity.Run, repository)
		}
		read := func() (string, error) {
			readCtx, cancel := context.WithTimeout(ctx, retainedRunLockReadTimeoutSeconds*time.Second)
			defer cancel()
			return remote.RemoteTagObject(readCtx, prepared.Remote, release.LockTagName)
		}
		current, err := read()
		if err != nil {
			return fmt.Errorf(priorReleaseLockReadErrorFormat, repository, err)
		}
		if current == retainedRunAbsentValueText {
			continue
		}
		if current != prepared.Object {
			return fmt.Errorf(priorReleaseLockReplacedErrorFormat,
				repository)
		}
		deleteCtx, cancel := context.WithTimeout(ctx, retainedRunLockDeleteTimeoutSeconds*time.Second)
		err = remote.DeleteRemoteTagLease(deleteCtx, prepared.Remote,
			release.LockTagName, prepared.Object)
		cancel()
		if err == nil {
			continue
		}
		current, readErr := read()
		if readErr != nil || current != retainedRunAbsentValueText {
			return fmt.Errorf(priorReleaseLockSettleErrorFormat,
				repository, err)
		}
	}
	return nil
}

func (a *App) reconcilePriorSweep(ctx context.Context,
	journal *firstRunJournal, identity firstRunIdentity) error {
	settings := a.cfg.Execution
	if settings == nil || settings.SecretEnv == retainedRunAbsentValueText {
		return fmt.Errorf(priorSweepMissingSecretReferenceErrorFormat, identity.Run)
	}
	signer, err := execution.NewSigner(os.Getenv(settings.SecretEnv))
	if err != nil {
		return fmt.Errorf(priorSweepSigningKeyUnavailableErrorFormat, err)
	}
	limits := formatTransferLimits(settings)
	local := execution.NewGitMailbox(retainedRunAbsentValueText, a.git, signer, a.log)
	bootstrap, err := local.ReadChoreographyBootstrap(ctx, identity.BootstrapOID,
		int64(limits.MaxManifestBytes), limits.MaxPackBytes)
	if err != nil {
		return fmt.Errorf(retainedSweepBootstrapUnauthenticErrorFormat, err)
	}
	binding := bootstrap.ChoreographyRunBinding
	if bootstrap.Mode != retainedRunCompilationModeName || binding.Run != identity.Run ||
		binding.Generation != identity.Generation ||
		bootstrap.ReturnEndpoint != identity.Endpoint ||
		bootstrap.ReturnCertSHA256 != identity.CertPin {
		return fmt.Errorf(retainedSweepBootstrapIdentityMismatchError)
	}
	selection, err := decodePinnedSweepSelection(bootstrap.PinnedInputs)
	if err != nil {
		return fmt.Errorf(retainedSweepSelectionMismatchErrorFormat, err)
	}
	mailbox := execution.NewGitMailbox(bootstrap.CoordinationEndpoint, a.git, signer, a.log)
	retained, err := execution.ReadRetainedChoreographyIdentity(ctx,
		execution.RetainedChoreographyFeedOptions{Mailbox: mailbox, Binding: binding,
			BootstrapOID: identity.BootstrapOID, MaxDocumentBytes: int64(limits.MaxManifestBytes),
			MaxTagBytes: limits.MaxPackBytes})
	if err != nil {
		return fmt.Errorf(retainedSweepLedgerReadMismatchErrorFormat, err)
	}
	if retained.Ledger.OID != identity.LedgerRootOID {
		return fmt.Errorf(retainedSweepLedgerIdentityMismatchError)
	}
	ledger, roster := retained.Ledger, retained.Roster
	sources := make(map[string]string, len(bootstrap.PinnedInputs.Repositories))
	for _, source := range bootstrap.PinnedInputs.Repositories {
		sources[source.Name] = mailbox.ObjectStoreDir()
	}
	private, err := recomputePeerSweep(ctx, peerReleasePlanningOptions{
		Inputs: bootstrap.PinnedInputs, Binding: binding, SourceDirs: sources,
		SemanticGraphDigest: retained.Carrier.SemanticGraphDigest,
		MaxPackBytes:        limits.MaxPackBytes, Log: a.log,
	})
	if err != nil {
		return fmt.Errorf(retainedSweepReplanFailureErrorFormat, err)
	}
	defer private.close()
	recomputedSpecs, budgets, err := sweepChoreographySpecs(sweepChoreographySpecOptions{
		Plan:          private.plan,
		Covered:       private.covered,
		Tasks:         private.tasks,
		Roster:        roster.Nodes,
		BuildCapacity: private.app.cfg.BuildConcurrency,
	})
	if err != nil {
		return fmt.Errorf(retainedSweepRosterPolicyReadErrorFormat, err)
	}
	applyChoreographyRetrySafety(recomputedSpecs, bootstrap.JoinPolicy.RetryAfterUncertainAttempt)
	if !slices.Equal(budgets, roster.Budgets) {
		return fmt.Errorf(retainedSweepRosterPolicyMismatchError)
	}
	specs := retained.Carrier.Tasks
	if err := verifyRetainedChoreographyPolicy(retainedChoreographyPolicyOptions{
		RecomputedTasks:    recomputedSpecs,
		SignedTasks:        specs,
		RecomputedPackages: sweepChoreographyPackagePolicies(private.plan, private.covered, selection.OnError),
		SignedPackages:     retained.Carrier.PackagePolicies,
	}); err != nil {
		return err
	}
	verifyFirst := func(context.Context) error {
		loaded, readErr := journal.read()
		if readErr != nil {
			return fmt.Errorf(retainedSweepOwnershipLostErrorFormat, readErr)
		}
		if journal.lock == nil || journal.gate == nil ||
			loaded.Run != identity.Run || loaded.Generation != identity.Generation ||
			loaded.BootstrapOID != identity.BootstrapOID ||
			loaded.LedgerRootOID != identity.LedgerRootOID {
			return fmt.Errorf(retainedSweepOwnershipLostError)
		}
		return nil
	}
	persist, err := execution.NewGitChoreographyPersist(execution.GitChoreographyOptions{
		Binding: binding, Mailbox: mailbox, VerifyFirst: verifyFirst,
		VerifyResult: func(verifyCtx context.Context, proposal execution.ChoreographyResultProposal,
			result execution.Result, store *gitx.LocalGitx) error {
			return mailbox.VerifyChoreographyResultFromClaim(verifyCtx,
				execution.ChoreographyResultClaimProof{Proposal: proposal, Result: result,
					Store: store, MaxDocumentBytes: int64(limits.MaxManifestBytes)})
		},
		MaxPackBytes: limits.MaxPackBytes, Ledger: ledger,
		BootstrapOID: identity.BootstrapOID, GraphRootOID: retained.GraphRootOID,
		InitializationCompleteOID: retained.InitializationCompleteOID,
		ReturnEndpoint:            bootstrap.ReturnEndpoint, ReturnCertSHA256: bootstrap.ReturnCertSHA256,
		JoinPolicy: bootstrap.JoinPolicy, AlgorithmVersion: bootstrap.AlgorithmVersion,
		Tasks: specs, Templates: retained.Carrier.Templates,
	})
	if err != nil {
		return err
	}
	nodes := make(map[string]int, len(roster.Nodes))
	first := retainedRunAbsentValueText
	for _, node := range roster.Nodes {
		nodes[node.Name] = node.Capacity
		if node.First {
			first = node.Name
		}
	}
	capacities := make(map[string]int, len(budgets))
	for _, budget := range budgets {
		capacities[budget.Name] = budget.Capacity
	}
	policy, err := newSweepOutcomeEvaluator(private.plan, private.covered, selection.OnError)
	if err != nil {
		return err
	}
	broker, err := execution.RecoverFirstNodeBroker(ctx, execution.FirstNodeBrokerOptions{
		Binding: binding, GraphRootOID: retained.GraphRootOID,
		InitialMembershipEpochOID: retained.InitializationCompleteOID,
		JoinPolicy:                bootstrap.JoinPolicy, FirstNode: first,
		Tasks: specs, Templates: retained.Carrier.Templates, Members: roster.Nodes,
		NodeCapacity: nodes, Budgets: capacities, OutcomePolicy: policy, Persist: persist,
		CancelResponseDuration: time.Duration(a.cfg.Execution.ResolveTimeouts().Cancel) * time.Second,
	})
	if err != nil {
		return fmt.Errorf(retainedSweepBrokerReplayErrorFormat, err)
	}
	sealed, err := retainedRunSealed(ctx, persist)
	if err != nil {
		return err
	}
	if !sealed {
		if _, err := broker.StopClaims(ctx); err != nil {
			return fmt.Errorf(retainedSweepClaimFenceErrorFormat, err)
		}
		if err := a.settleRetainedSweepAttempts(ctx, retainedSweepSettlementOptions{
			Identity: identity,
			Signer:   signer,
			Limits:   limits,
			Binding:  binding,
			Mailbox:  mailbox,
			Broker:   broker,
			Persist:  persist,
		}); err != nil {
			return err
		}
		if _, err := persist.SealRun(ctx, retainedSweepAbortedSealState); err != nil {
			return fmt.Errorf(retainedSweepUnsettledAcceptedWorkErrorFormat, err)
		}
	}
	if err := a.recoverPriorRunNodeSlots(ctx, priorRunNodeSlotRecoveryOptions{
		Identity: identity,
		Roster:   roster,
		Mailbox:  mailbox,
		Signer:   signer,
		Limits:   limits,
	}); err != nil {
		return err
	}
	return journal.markCompleted()
}

type priorRunNodeSlotRecoveryOptions struct {
	Identity firstRunIdentity
	Roster   execution.ChoreographyRuntimeRoster
	Mailbox  *execution.GitMailbox
	Signer   *execution.Signer
	Limits   execution.TransferLimits
}

// recoverPriorRunNodeSlots reconciles the physical capacity held by this
// first-node run against its retained signed feed before its active locator
// retires. A sealed run alone cannot prove that a preclaim native hook or
// outcome command stopped; those leases remain held without exact finality.
func (a *App) recoverPriorRunNodeSlots(ctx context.Context, options priorRunNodeSlotRecoveryOptions) error {
	identity, roster, mailbox, signer, limits := options.Identity, options.Roster, options.Mailbox, options.Signer, options.Limits
	var first *execution.ChoreographyRuntimeNode
	for index := range roster.Nodes {
		if roster.Nodes[index].First {
			first = &roster.Nodes[index]
			break
		}
	}
	if first == nil || first.Name == retainedRunAbsentValueText || first.Capacity <= retainedRunNoEntriesCount || first.AuthorityID == retainedRunAbsentValueText {
		return fmt.Errorf(retainedFirstRosterMissingAuthorityError)
	}
	slots, err := openSharedNodeSlots(first.Name, first.Capacity)
	if err != nil {
		return fmt.Errorf(retainedFirstCapacityOpenErrorFormat, err)
	}
	if slots.AuthorityID() != first.AuthorityID {
		return fmt.Errorf(retainedFirstAuthorityMismatchError)
	}
	worker := &execution.Worker{
		Node: first.Name, Mailbox: mailbox, ReturnSigner: signer,
		NodeSlots: slots, Cache: a.git, Log: a.log,
		Report: execution.NodeReport{Limits: limits},
	}
	if err := worker.RecoverHeldNodeSlotsForRun(ctx, identity.Run); err != nil {
		return fmt.Errorf(retainedFirstCapacityAuthenticationErrorFormat, err)
	}
	held, err := slots.Held(ctx)
	if err != nil {
		return fmt.Errorf(retainedFirstCapacityInspectionErrorFormat, err)
	}
	for _, lease := range held {
		if lease.Intent.Run == identity.Run {
			return fmt.Errorf(retainedFirstCapacityWithoutEvidenceErrorFormat,
				identity.Run, lease.Intent.Task)
		}
	}
	return nil
}
