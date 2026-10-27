package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	retainedReleaseMissingSecretReferenceErrorFormat      = "execution: prior release %s has no current signing secret reference"
	retainedReleaseSigningKeyUnavailableErrorFormat       = "execution: prior release signing key is unavailable: %w"
	retainedReleaseBootstrapUnauthenticErrorFormat        = "execution: retained release bootstrap is not authentic: %w"
	retainedReleaseBootstrapIdentityMismatchError         = "execution: retained release bootstrap differs from protected identity"
	retainedReleaseSelectionMismatchErrorFormat           = "execution: retained run is not a release: %w"
	retainedReleaseLedgerReadMismatchErrorFormat          = "execution: retained release ledger differs from journal: %w"
	retainedReleaseLedgerIdentityMismatchError            = "execution: retained release ledger differs from journal"
	retainedReleaseReplanFailureErrorFormat               = "execution: retained release cannot be independently replanned: %w"
	retainedReleaseRosterPolicyErrorFormat                = "execution: retained release policy differs from signed roster: %w"
	retainedReleaseOwnershipLostErrorFormat               = "execution: retained release lost protected first-node ownership: %w"
	retainedReleaseOwnershipLostError                     = "execution: retained release lost protected first-node ownership"
	retainedReleaseCleanupRejectsResultError              = "execution: completed release cleanup does not admit new results"
	retainedReleaseBrokerReplayErrorFormat                = "execution: retained release broker replay failed: %w"
	retainedReleaseDuplicateEffectErrorFormat             = "execution: repeated retained release effect %s"
	retainedReleaseEffectUnauthenticErrorFormat           = "execution: retained release effect %s is not authentic: %w"
	retainedReleaseEffectPlanMismatchErrorFormat          = "execution: retained release effect %s differs from pinned plan"
	retainedReleaseEffectRecordingModeMismatchErrorFormat = "execution: retained release effect %s recording mode differs from pinned policy"
	retainedReleaseRecordBlockedUnverifiedErrorFormat     = "execution: retained release effect %s has an unverified record-blocked publication"
	retainedReleaseDuplicateDeferredTagErrorFormat        = "execution: retained release tag %s has two successful deferred tasks"
	retainedReleaseDeferredTagReceiptMismatchErrorFormat  = "execution: deferred release tag %s differs from signed receipt and protected intent"
	retainedReleaseLocalTagReadErrorFormat                = "execution: retained local tag %s differs from signed effect: %w"
	retainedReleaseLocalTagMismatchErrorFormat            = "execution: retained local tag %s differs from signed effect"
	retainedReleaseLocalTargetReadErrorFormat             = "execution: retained local tag target %s differs from signed effect: %w"
	retainedReleaseLocalTargetMismatchErrorFormat         = "execution: retained local tag target %s differs from signed effect"
	retainedReleaseDestinationMissingErrorFormat          = "execution: retained pushed effect %s has no exact release destination"
	retainedReleaseTagReadErrorFormat                     = "execution: retained release tag %s differs from signed effect: %w"
	retainedReleaseTagMismatchErrorFormat                 = "execution: retained release tag %s differs from signed effect"
	retainedReleaseTargetReadErrorFormat                  = "execution: retained release target %s differs from signed effect: %w"
	retainedReleaseTargetMismatchErrorFormat              = "execution: retained release target %s differs from signed effect"
	retainedReleaseUnsafeRepositoryPathErrorFormat        = "execution: retained repository %s has an unsafe path"
	retainedReleaseEscapedRepositoryPathErrorFormat       = "execution: retained repository %s escaped its checkout"
	retainedReleaseMissingRepositoryErrorFormat           = "execution: retained repository %s is absent from pinned sources"
	retainedReleaseFinalityReadErrorFormat                = "execution: retained release finality cannot be read: %w"
	retainedReleaseFinalityGapError                       = "execution: retained release finality has a gap"
	retainedReleaseMissingNormalSealError                 = "execution: prior release has no normal signed run seal"
)

// reconcileCompletedRelease retires only a normally sealed generation. It
// reconstructs the semantic plan and the effective task graph from retained
// immutable inputs before trusting the signed terminal feed. An unfinished
// native effect cannot be resumed by this cleanup path.
func (a *App) reconcileCompletedRelease(ctx context.Context,
	journal *firstRunJournal, identity firstRunIdentity) error {
	settings := a.cfg.Execution
	if settings == nil || settings.SecretEnv == retainedRunAbsentValueText {
		return fmt.Errorf(retainedReleaseMissingSecretReferenceErrorFormat, identity.Run)
	}
	signer, err := execution.NewSigner(os.Getenv(settings.SecretEnv))
	if err != nil {
		return fmt.Errorf(retainedReleaseSigningKeyUnavailableErrorFormat, err)
	}
	limits := formatTransferLimits(settings)
	local := execution.NewGitMailbox(retainedRunAbsentValueText, a.git, signer, a.log)
	bootstrap, err := local.ReadChoreographyBootstrap(ctx, identity.BootstrapOID,
		int64(limits.MaxManifestBytes), limits.MaxPackBytes)
	if err != nil {
		return fmt.Errorf(retainedReleaseBootstrapUnauthenticErrorFormat, err)
	}
	binding := bootstrap.ChoreographyRunBinding
	if (bootstrap.Mode != retainedRunCompilationModeName && bootstrap.Mode != interpretedModeName) || binding.Run != identity.Run ||
		binding.Generation != identity.Generation ||
		bootstrap.ReturnEndpoint != identity.Endpoint ||
		bootstrap.ReturnCertSHA256 != identity.CertPin {
		return fmt.Errorf(retainedReleaseBootstrapIdentityMismatchError)
	}
	if _, err := decodePinnedReleaseSelection(bootstrap.PinnedInputs.Selection); err != nil {
		return fmt.Errorf(retainedReleaseSelectionMismatchErrorFormat, err)
	}
	mailbox := execution.NewGitMailbox(bootstrap.CoordinationEndpoint, a.git, signer, a.log)
	retained, err := execution.ReadRetainedChoreographyIdentity(ctx,
		execution.RetainedChoreographyFeedOptions{Mailbox: mailbox, Binding: binding,
			BootstrapOID: identity.BootstrapOID, MaxDocumentBytes: int64(limits.MaxManifestBytes),
			MaxTagBytes: limits.MaxPackBytes})
	if err != nil {
		return fmt.Errorf(retainedReleaseLedgerReadMismatchErrorFormat, err)
	}
	if retained.Ledger.OID != identity.LedgerRootOID {
		return fmt.Errorf(retainedReleaseLedgerIdentityMismatchError)
	}
	ledger, roster := retained.Ledger, retained.Roster
	sources := make(map[string]string, len(bootstrap.PinnedInputs.Repositories))
	for _, source := range bootstrap.PinnedInputs.Repositories {
		sources[source.Name] = mailbox.ObjectStoreDir()
	}
	private, err := recomputePeerReleasePlan(ctx, peerReleasePlanningOptions{
		Inputs: bootstrap.PinnedInputs, Binding: binding, SourceDirs: sources,
		SemanticGraphDigest: retained.Carrier.SemanticGraphDigest,
		MaxPackBytes:        limits.MaxPackBytes, Log: a.log,
	})
	if err != nil {
		return fmt.Errorf(retainedReleaseReplanFailureErrorFormat, err)
	}
	defer private.close()
	recomputedSpecs, err := private.interpretedReleaseSpecs(roster)
	if err != nil {
		return fmt.Errorf(retainedReleaseRosterPolicyErrorFormat, err)
	}
	applyChoreographyRetrySafety(recomputedSpecs, bootstrap.JoinPolicy.RetryAfterUncertainAttempt)
	applyChoreographyAttemptLimit(recomputedSpecs, bootstrap.JoinPolicy)
	specs := retained.Carrier.Tasks
	if err := verifyRetainedChoreographyPolicy(retainedChoreographyPolicyOptions{
		RecomputedTasks:    recomputedSpecs,
		SignedTasks:        specs,
		RecomputedPackages: releaseChoreographyPackagePolicies(private.compiled),
		SignedPackages:     retained.Carrier.PackagePolicies,
	}); err != nil {
		return err
	}
	verifyFirst := func(context.Context) error {
		loaded, readErr := journal.read()
		if readErr != nil {
			return fmt.Errorf(retainedReleaseOwnershipLostErrorFormat, readErr)
		}
		if journal.lock == nil || journal.gate == nil ||
			loaded.Run != identity.Run || loaded.Generation != identity.Generation ||
			loaded.BootstrapOID != identity.BootstrapOID ||
			loaded.LedgerRootOID != identity.LedgerRootOID {
			return fmt.Errorf(retainedReleaseOwnershipLostError)
		}
		return nil
	}
	persist, err := execution.NewGitChoreographyPersist(execution.GitChoreographyOptions{
		Binding: binding, Mailbox: mailbox, VerifyFirst: verifyFirst,
		VerifyResult: func(context.Context, execution.ChoreographyResultProposal,
			execution.Result, *gitx.LocalGitx) error {
			return fmt.Errorf(retainedReleaseCleanupRejectsResultError)
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
	budgets := make(map[string]int, len(roster.Budgets))
	for _, budget := range roster.Budgets {
		budgets[budget.Name] = budget.Capacity
	}
	if _, err := execution.RecoverFirstNodeBroker(ctx, execution.FirstNodeBrokerOptions{
		Binding: binding, GraphRootOID: retained.GraphRootOID,
		InitialMembershipEpochOID: retained.InitializationCompleteOID,
		JoinPolicy:                bootstrap.JoinPolicy, FirstNode: first,
		Tasks: specs, Templates: retained.Carrier.Templates, Members: roster.Nodes,
		NodeCapacity: nodes, Budgets: budgets,
		CancelResponseDuration: time.Duration(a.cfg.Execution.ResolveTimeouts().Cancel) * time.Second,
		OutcomePolicy:          private.compiled.NewOutcomeEvaluator(), Persist: persist,
	}); err != nil {
		return fmt.Errorf(retainedReleaseBrokerReplayErrorFormat, err)
	}
	localEffects, err := requireNormalRetainedSeal(ctx, persist)
	if err != nil {
		return err
	}
	if err := a.verifyRetainedReleaseEffects(ctx, retainedReleaseEffectOptions{
		Identity:     identity,
		Binding:      binding,
		Mailbox:      mailbox,
		Repositories: bootstrap.PinnedInputs.Repositories,
		Plan:         private,
		Tasks:        specs,
		Events:       localEffects,
	}); err != nil {
		return err
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
	// The lock may already have been deleted when the process died. Exact
	// compare-and-delete accepts that absence, and refuses any foreign owner.
	if err := reconcileUnstartedReleaseLocksWithGit(ctx, identity, a.git); err != nil {
		return err
	}
	return journal.markCompleted()
}

type retainedReleaseEffectOptions struct {
	Identity     firstRunIdentity
	Binding      execution.ChoreographyRunBinding
	Mailbox      *execution.GitMailbox
	Repositories []execution.AssignmentRepository
	Plan         *peerReleasePlan
	Tasks        []execution.ChoreographyTaskSpec
	Events       []execution.ChoreographyEvent
}

// verifyRetainedReleaseEffects compares each successful signed native record
// with the exact destination held in this run's write-ahead lock journal.
// A receipt ref may have been removed after the durable release tag landed;
// the tag's live object and peeled target are the surviving native record.
func (a *App) verifyRetainedReleaseEffects(ctx context.Context, options retainedReleaseEffectOptions) error {
	identity, binding, mailbox, repositories := options.Identity, options.Binding, options.Mailbox, options.Repositories
	private, specs, events := options.Plan, options.Tasks, options.Events
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(specs))
	for _, spec := range specs {
		byTask[spec.Task] = spec
	}
	seen := make(map[string]bool, len(events))
	deferred := make(map[string]bool)
	for _, event := range events {
		spec := byTask[event.Task]
		if !spec.LocalAuthority {
			continue
		}
		if seen[event.Task] {
			return fmt.Errorf(retainedReleaseDuplicateEffectErrorFormat, event.Task)
		}
		seen[event.Task] = retainedReleaseObservedEffectState
		outcome, err := mailbox.ReadChoreographyLocalOutcome(ctx, binding, event, spec)
		if err != nil {
			return fmt.Errorf(retainedReleaseEffectUnauthenticErrorFormat, event.Task, err)
		}
		evidence := outcome.EffectEvidence
		rel := private.plan.Releases[spec.Package]
		if rel == nil || rel.Pkg == nil || evidence.Repository != rel.Pkg.Repository ||
			evidence.Package != rel.Pkg.Name || evidence.Tag != rel.TagName() ||
			evidence.Version != rel.Next.String() || evidence.PlanDigest != binding.PlanDigest {
			return fmt.Errorf(retainedReleaseEffectPlanMismatchErrorFormat, event.Task)
		}
		commit, err := private.app.releaseCommitConfig(evidence.Repository)
		if err != nil {
			return err
		}
		mode := releaseRecordingMode(commit)
		isDeferred := private.app.workspace == nil && commit.IsEnabled() &&
			(spec.Kind == retainedRunPublishStageName || spec.Kind == retainedRunRecordStageName)
		if isDeferred {
			mode = releaseEffectDeferredFinalizeMode
		}
		if evidence.RecordingMode != mode {
			return fmt.Errorf(retainedReleaseEffectRecordingModeMismatchErrorFormat, event.Task)
		}
		if outcome.PackageOutcome == string(release.OutcomeRecordBlocked) {
			// A publish can have reached the destination before its record was
			// blocked. This failed observation carries no complete receipt, so
			// cleanup cannot prove which native effect a later run would see.
			return fmt.Errorf(retainedReleaseRecordBlockedUnverifiedErrorFormat, event.Task)
		}
		if !outcome.Succeeded {
			continue
		}
		if isDeferred {
			if deferred[evidence.Tag] {
				return fmt.Errorf(retainedReleaseDuplicateDeferredTagErrorFormat, evidence.Tag)
			}
			deferred[evidence.Tag] = retainedReleaseObservedEffectState
			intent, prepared := identity.DeferredTags[evidence.Tag]
			if !prepared || intent.Tag != evidence.Tag ||
				(commit.IsPushEnabled() && (evidence.ReceiptObject == retainedRunAbsentValueText ||
					evidence.ReceiptCommit == retainedRunAbsentValueText || intent.ReceiptObject != evidence.ReceiptObject ||
					intent.ReceiptCommit != evidence.ReceiptCommit)) ||
				(!commit.IsPushEnabled() && (evidence.ReceiptObject != retainedRunAbsentValueText || evidence.ReceiptCommit != retainedRunAbsentValueText)) {
				return fmt.Errorf(retainedReleaseDeferredTagReceiptMismatchErrorFormat, evidence.Tag)
			}
			continue
		}
		if evidence.RecordingMode == retainedRunLocalRecordingModeName {
			// A local-only tag is never checked through a possibly changed
			// remote alias. Its checkout is the exact retained repository path.
			path, err := pinnedLiveRepositoryPath(a.root, repositories, evidence.Repository)
			if err != nil {
				return err
			}
			git := &gitx.LocalGitx{Dir: path, Log: a.log}
			object, err := git.TagObject(ctx, evidence.Tag)
			if err != nil {
				return fmt.Errorf(retainedReleaseLocalTagReadErrorFormat, evidence.Tag, err)
			}
			if object == retainedRunAbsentValueText || object != evidence.TagObject {
				return fmt.Errorf(retainedReleaseLocalTagMismatchErrorFormat, evidence.Tag)
			}
			target, err := git.ResolveCommit(ctx, evidence.Tag)
			if err != nil {
				return fmt.Errorf(retainedReleaseLocalTargetReadErrorFormat, evidence.Tag, err)
			}
			if target != evidence.TagTarget {
				return fmt.Errorf(retainedReleaseLocalTargetMismatchErrorFormat, evidence.Tag)
			}
			continue
		}
		prepared, ok := identity.PreparedLocks[evidence.Repository]
		if !ok || prepared.Remote == retainedRunAbsentValueText || prepared.Run != identity.Run {
			return fmt.Errorf(retainedReleaseDestinationMissingErrorFormat, event.Task)
		}
		object, err := a.git.RemoteTagObject(ctx, prepared.Remote, evidence.Tag)
		if err != nil {
			return fmt.Errorf(retainedReleaseTagReadErrorFormat, evidence.Tag, err)
		}
		if object == retainedRunAbsentValueText || evidence.TagObject != retainedRunAbsentValueText && object != evidence.TagObject {
			return fmt.Errorf(retainedReleaseTagMismatchErrorFormat, evidence.Tag)
		}
		target, err := a.git.RemoteTagCommit(ctx, prepared.Remote, evidence.Tag)
		want := evidence.TagTarget
		if want == retainedRunAbsentValueText {
			want = evidence.ReceiptCommit
		}
		if err != nil {
			return fmt.Errorf(retainedReleaseTargetReadErrorFormat, evidence.Tag, err)
		}
		if want == retainedRunAbsentValueText || target != want ||
			evidence.PreparedTarget != retainedRunAbsentValueText && target != evidence.PreparedTarget {
			return fmt.Errorf(retainedReleaseTargetMismatchErrorFormat, evidence.Tag)
		}
	}
	return a.verifyDeferredTagInventory(ctx, deferredTagInventoryCheck{
		Identity: identity, Expected: deferred,
		Push: private.app.cfg.Commit.IsPushEnabled()})
}

func pinnedLiveRepositoryPath(root string, repositories []execution.AssignmentRepository,
	name string) (string, error) {
	for _, repository := range repositories {
		if repository.Name != name {
			continue
		}
		if repository.Path != retainedRunAbsentValueText && !filepath.IsLocal(repository.Path) {
			return retainedRunAbsentValueText, fmt.Errorf(retainedReleaseUnsafeRepositoryPathErrorFormat, name)
		}
		base, err := filepath.EvalSymlinks(root)
		if err != nil {
			return retainedRunAbsentValueText, err
		}
		candidate, err := filepath.EvalSymlinks(filepath.Join(base, repository.Path))
		if err != nil {
			return retainedRunAbsentValueText, err
		}
		relative, err := filepath.Rel(base, candidate)
		if err != nil || relative == retainedRunParentDirectoryName || strings.HasPrefix(relative, retainedRunParentDirectoryName+string(filepath.Separator)) {
			return retainedRunAbsentValueText, fmt.Errorf(retainedReleaseEscapedRepositoryPathErrorFormat, name)
		}
		return candidate, nil
	}
	return retainedRunAbsentValueText, fmt.Errorf(retainedReleaseMissingRepositoryErrorFormat, name)
}

func requireNormalRetainedSeal(ctx context.Context, persist *execution.GitChoreographyPersist) (
	[]execution.ChoreographyEvent, error) {
	var cursor execution.ChoreographyEventCursor
	var last execution.ChoreographyEvent
	var local []execution.ChoreographyEvent
	for {
		page, err := persist.ReadEvents(ctx, cursor, retainedRunEventPageSizeCount)
		if err != nil {
			return nil, fmt.Errorf(retainedReleaseFinalityReadErrorFormat, err)
		}
		if len(page.Events) == retainedRunNoEntriesCount && page.More || page.Cursor.Sequence < cursor.Sequence ||
			page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) {
			return nil, fmt.Errorf(retainedReleaseFinalityGapError)
		}
		if len(page.Events) != retainedRunNoEntriesCount {
			last = page.Events[len(page.Events)-retainedRunLastEventOffsetCount]
			for _, event := range page.Events {
				if event.Kind == execution.ChoreographyTerminal && event.ResultReceiptOID == retainedRunAbsentValueText {
					local = append(local, event)
				}
			}
		}
		cursor = page.Cursor
		if !page.More {
			if cursor != page.Head || last.Kind != execution.ChoreographyRunSealed || last.Aborted ||
				last.Sequence != cursor.Sequence {
				return nil, fmt.Errorf(retainedReleaseMissingNormalSealError)
			}
			return local, nil
		}
	}
}
