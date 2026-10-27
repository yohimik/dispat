// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The first node's exact task-ref CAS backend. Non-first choreographers can
// stream signed result objects here, but only this backend imports and pushes
// them. A receipt is returned after the remote ref is read back at the exact
// object; an unreadable outcome remains unknown and cannot authorize work.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyClaimKind                                                                     MessageKind = "choreography-claim"
	choreographyResultKind                                                                    MessageKind = "choreography-result"
	choreographyDecisionKind                                                                  MessageKind = "choreography-decision"
	choreographyBlockedKind                                                                   MessageKind = "choreography-blocked"
	choreographyResultPackMismatchError                                                                   = "execution: result pack differs from signed transfer identity"
	choreographyStaleAssignmentMismatchError                                                              = "execution: stale claim query names another signed assignment"
	choreographyGitIncompleteFirstNodeGitPersistenceError                                                 = "execution: incomplete first-node Git persistence"
	choreographyGitDuplicateOrEmptyPersistedCompiledTemplateError                                         = "execution: duplicate or empty persisted compiled template"
	choreographyGitInvalidLocalAuthorityTaskSpecificationError                                            = "execution: invalid local authority task specification"
	choreographyGitInvalidFirstNodeTaskSpecificationError                                                 = "execution: invalid first-node task specification"
	choreographyGitInvalidCompiledAssignmentError                                                         = "execution: invalid compiled assignment"
	choreographyGitCompiledAssignmentHasNoCanonicalTaskOrSlotError                                        = "execution: compiled assignment has no canonical task or slot"
	choreographyGitInvalidLocalAuthorityOutcomeError                                                      = "execution: invalid local authority outcome"
	choreographyGitReadyFrameNamesNoCanonicalTaskOrInputError                                             = "execution: ready frame names no canonical task or input"
	choreographyGitClaimNamesNoTaskInTheCompleteSignedGraphRootError                                      = "execution: claim names no task in the complete signed graph root"
	choreographyGitCompiledClaimHasNoFirstNodeResolvedEffectiveBindingsError                              = "execution: compiled claim has no first-node-resolved effective bindings"
	choreographyGitClaimTemplateOrBindingsCannotBeDigestedCauseErrorFormat                                = "execution: claim template or bindings cannot be digested: %w"
	choreographyGitClaimTemplateDiffersFromSignedGraphError                                               = "execution: claim template differs from signed graph"
	choreographyGitUnresolvedExactClaimHasDifferentPreparedAssignmentInputsError                          = "execution: unresolved exact claim has different prepared assignment inputs"
	choreographyGitInvalidRefusalReasonError                                                              = "execution: invalid refusal reason"
	choreographyGitRefusalNamesNoCanonicalTaskRefError                                                    = "execution: refusal names no canonical task ref"
	choreographyGitStoppedClaimReceiptLookupFailedCauseErrorFormat                                        = "execution: stopped claim receipt lookup failed: %w"
	choreographyGitStoppedClaimHasNoExactDurableRefusalError                                              = "execution: stopped claim has no exact durable refusal"
	choreographyGitStoppedRefusalCannotFenceAnInconsistentTaskRefError                                    = "execution: stopped refusal cannot fence an inconsistent task ref"
	choreographyGitRefusalCannotFenceAnInconsistentTaskRefError                                           = "execution: refusal cannot fence an inconsistent task ref"
	choreographyGitRefusalHasNoDurableEpochFenceError                                                     = "execution: refusal has no durable epoch fence"
	choreographyGitResultPackIsMissingError                                                               = "execution: result pack is missing"
	choreographyGitResultPackDiffersFromSignedTransferIdentityCauseErrorFormat                            = "execution: result pack differs from signed transfer identity: %w"
	choreographyGitImportedResultIsNotASignedResultCommitError                                            = "execution: imported result is not a signed result commit"
	choreographyGitImportedResultAnswersAnotherClaimError                                                 = "execution: imported result answers another claim"
	choreographyGitInvalidCompiledCancellationError                                                       = "execution: invalid compiled cancellation"
	choreographyGitCancellationAssignmentDiffersFromActiveAttemptError                                    = "execution: cancellation assignment differs from active attempt"
	choreographyGitCancellationClaimIsNotDescendedFromOfferedAssignmentError                              = "execution: cancellation claim is not descended from offered assignment"
	choreographyGitInvalidUnclaimedQueueFenceError                                                        = "execution: invalid unclaimed queue fence"
	choreographyGitQueueFenceDiffersFromOfferedAssignmentError                                            = "execution: queue fence differs from offered assignment"
	choreographyGitInvalidUnclaimedQueueDecisionError                                                     = "execution: invalid unclaimed queue decision"
	choreographyGitFinalQueueDecisionLacksItsExactPreHookFenceError                                       = "execution: final queue decision lacks its exact pre-hook fence"
	choreographyGitFinalQueueDecisionHasNoSignedCancellationError                                         = "execution: final queue decision has no signed cancellation"
	choreographyGitQueueDecisionDiffersFromOfferedAssignmentError                                         = "execution: queue decision differs from offered assignment"
	choreographyGitInvalidCancellationAcknowledgementError                                                = "execution: invalid cancellation acknowledgement"
	choreographyGitCancellationRefIsNotAtTheObservedCancelError                                           = "execution: cancellation ref is not at the observed cancel"
	choreographyGitExpectedASignedCancellationError                                                       = "execution: expected a signed cancellation"
	choreographyGitCancellationDoesNotMatchAcknowledgementError                                           = "execution: cancellation does not match acknowledgement"
	choreographyGitInvalidTaskDecisionError                                                               = "execution: invalid task decision"
	choreographyGitDecisionNamesNoCanonicalTaskRefError                                                   = "execution: decision names no canonical task ref"
	choreographyGitResultReceiptHasNoExactOutputCarrierError                                              = "execution: result receipt has no exact output carrier"
	choreographyGitDecisionNamesNoSettledResultOrAcknowledgementError                                     = "execution: decision names no settled result or acknowledgement"
	choreographyGitSuccessfulDecisionHasNoResultOutputError                                               = "execution: successful decision has no result output"
	choreographyGitPackageOutcomeIsNotAtTaskFinalityError                                                 = "execution: package outcome is not at task finality"
	choreographyGitPublishedOutcomeRequiresSuccessfulRecordingTaskError                                   = "execution: published outcome requires successful recording task"
	choreographyGitRecordBlockedOutcomeRequiresFailedRecordingTaskError                                   = "execution: record-blocked outcome requires failed recording task"
	choreographyGitDeadPackageOutcomeRequiresFailedTaskError                                              = "execution: dead package outcome requires failed task"
	choreographyGitInvalidPackageOutcomeNamedErrorFormat                                                  = "execution: invalid package outcome %q"
	choreographyGitInvalidBlockedTaskOutcomeError                                                         = "execution: invalid blocked task outcome"
	choreographyGitBlockedTaskDoesNotMatchPlannedPackageOutcomeError                                      = "execution: blocked task does not match planned package outcome"
	choreographyGitBlockedTaskHasNoExactTaskRefPredecessorError                                           = "execution: blocked task has no exact task-ref predecessor"
	choreographyGitLocalFailureForPackageNamedHasPriorSignedOutcomeNamedWantFailedErrorFormat             = "execution: local failure for package %q has prior signed outcome %q, want failed"
	choreographyGitReceiptQueryNamesAnotherTaskRefError                                                   = "execution: receipt query names another task ref"
	choreographyGitReceiptQueryHasNoCanonicalReservationError                                             = "execution: receipt query has no canonical reservation"
	choreographyGitProposalIdReusedForAnotherMessageKindError                                             = "execution: proposal ID reused for another message kind"
	choreographyGitProposalIdReusedOnTaskRefError                                                         = "execution: proposal ID reused on task ref"
	choreographyGitTaskRefCarriesUnexpectedStateNamedErrorFormat                                          = "execution: task ref carries unexpected state %s"
	choreographyGitTaskReceiptChainMissesCanonicalReservationError                                        = "execution: task receipt chain misses canonical reservation"
	choreographyGitProposalCommitMissesCanonicalReservationError                                          = "execution: proposal commit misses canonical reservation"
	choreographyGitAcceptedProposalIdentityDiffersError                                                   = "execution: accepted proposal identity differs"
	choreographyGitTaskProposalHasNoSignedLedgerAcceptanceError                                           = "execution: task proposal has no signed ledger acceptance"
	choreographyGitRefusedProposalIdentityDiffersError                                                    = "execution: refused proposal identity differs"
	choreographyGitStaleClaimQueryNamesNoAuthenticatedExpectedTaskStateError                              = "execution: stale claim query names no authenticated expected task state"
	choreographyGitStaleClaimAttemptCountNamesAttemptOneReservationErrorFormat                            = "execution: stale claim attempt %d names attempt-one reservation"
	choreographyGitStaleClaimQueryNamesAnotherSignedAssignmentCauseErrorFormat                            = "execution: stale claim query names another signed assignment: %w"
	choreographyGitStaleClaimNamesAnotherSignedRetryDecisionError                                         = "execution: stale claim names another signed retry decision"
	choreographyGitInvalidChoreographyTaskBranchError                                                     = "execution: invalid choreography task branch"
	choreographyGitTaskRefCasDidNotSettleCauseErrorFormat                                                 = "execution: task-ref CAS did not settle: %w"
	choreographyGitTaskRefCasOutcomeIsNotExactError                                                       = "execution: task-ref CAS outcome is not exact"
	choreographyGitAbsentState                                                                            = ""
	choreographyGitMinimumPositiveBytes                                                       int64       = 1
	choreographyGitNoTasks                                                                                = 0
	choreographyGitMinimumTaskWeight                                                                      = 1
	choreographyGitSingleEligibleNode                                                                     = 1
	choreographyGitLedgerRootSequence                                                         uint64      = 0
	choreographyGitFirstAttempt                                                                           = 1
	choreographyGitNoSlotSequence                                                             uint64      = 0
	choreographyGitSeekStartOffset                                                            int64       = 0
	choreographyGitResultCopyBufferBytes                                                                  = 32 << 10
	choreographyGitResultReadOverflowBytes                                                    int64       = 1
	choreographyGitClaimCarrierParentCount                                                                = 1
	choreographyGitFirstParentIndex                                                                       = 0
	choreographyGitResultCarrierParentCount                                                               = 2
	choreographyGitResultOutputParentIndex                                                                = 1
	choreographyGitHistoryStartStep                                                                       = 0
	choreographyGitHistoryStepsPerAttempt                                                                 = 8
	choreographyGitHistoryFixedSteps                                                                      = 4
	choreographyGitParentSuffix                                                                           = "^"
	choreographyGitLedgerSequenceOffset                                                       uint64      = 1
	choreographyGitRefusalBudgetScope                                                                     = "budget"
	choreographyGitRefusalNodeScope                                                                       = "node"
	choreographyGitOutcomePublished                                                                       = "published"
	choreographyGitOutcomeRecordBlocked                                                                   = "record-blocked"
	choreographyGitOutcomeFailed                                                                          = "failed"
	choreographyGitOutcomeSkipped                                                                         = "skipped"
	choreographyGitOutcomeCancelled                                                                       = "cancelled"
	choreographyGitReceiptMissing                                                                         = false
	choreographyGitReceiptAccepted                                                                        = true
	choreographyGitCanonicalRootUnseen                                                                    = false
	choreographyGitCanonicalRootFound                                                                     = true
	choreographyGitExpectedTipUnseen                                                                      = false
	choreographyGitExpectedTipFound                                                                       = true
	choreographyGitClaimRefused                                                                           = true
	choreographyGitReceiptRefused                                                                         = true
	choreographyGitRefusalNotWritten                                                                      = false
	choreographyGitRefusalWritten                                                                         = true
)

// GitChoreographyOptions must be assembled on the first node only. VerifyFirst
// checks its live run ownership before each coordination mutation. VerifyResult
// validates the imported signed Result and output manifest against the
// immutable task spec before those bytes become a Git-carried receipt.
type GitChoreographyOptions struct {
	Binding                   ChoreographyRunBinding
	Mailbox                   *GitMailbox
	VerifyFirst               func(context.Context) error
	VerifyResult              func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error
	MaxPackBytes              int64
	Ledger                    ChoreographyLedgerReservation
	BootstrapOID              string
	GraphRootOID              string
	InitializationCompleteOID string
	ReturnEndpoint            string
	ReturnCertSHA256          string
	JoinPolicy                ChoreographyJoinPolicy
	AlgorithmVersion          string
	Tasks                     []ChoreographyTaskSpec
	Templates                 []ChoreographyCompiledTemplate
	OnRefAdvance              func(context.Context, ChoreographyRefAdvance)
}

// ChoreographyRefAdvance is emitted after exact remote readback, allowing
// Coordinator.Close to keep its owned exact lease current without a bounded
// ancestry search through multiple retries.
type ChoreographyRefAdvance struct {
	Branch string
	OldOID string
	NewOID string
}

type GitChoreographyPersist struct {
	opts                 GitChoreographyOptions
	mu                   sync.Mutex
	ledgerTip            string
	sequence             uint64
	tasks                map[string]ChoreographyTaskSpec
	templates            map[string]ChoreographyCompiledTemplate
	localTasks           map[string]ChoreographyTaskSpec
	pending              *choreographyAtomicPush
	events               []ChoreographyEvent
	eventOIDs            []string
	eventIndex           map[string]uint64
	packageState         map[string]string
	taskEvents           map[string][]int
	taskNotify           map[string]chan struct{}
	accepted             map[string]ChoreographyEventCursor
	refused              map[string]ChoreographyEventCursor
	joined               map[string]ChoreographyEventCursor
	drained              map[string]ChoreographyEventCursor
	joinRefused          map[string]ChoreographyEventCursor
	sealed               bool
	stopping             bool
	initialized          bool
	executionStarted     bool
	admissionDeadlineUTC string
	replayPasses         int
}

func NewGitChoreographyPersist(opts GitChoreographyOptions) (*GitChoreographyPersist, error) {
	if err := validateChoreographyRetryPolicy(opts.JoinPolicy, opts.Tasks); err != nil {
		return nil, err
	}
	if opts.Mailbox == nil || opts.Mailbox.plumbing == nil || opts.VerifyFirst == nil ||
		opts.VerifyResult == nil || opts.MaxPackBytes < choreographyGitMinimumPositiveBytes ||
		!opts.Binding.valid() || opts.GraphRootOID == choreographyGitAbsentState || opts.InitializationCompleteOID == choreographyGitAbsentState ||
		opts.Ledger.Branch != FormatChoreographyLedgerBranch(opts.Binding.Run) ||
		opts.Ledger.OID == choreographyGitAbsentState || opts.BootstrapOID == choreographyGitAbsentState || len(opts.Tasks) == choreographyGitNoTasks {
		return nil, fmt.Errorf(choreographyGitIncompleteFirstNodeGitPersistenceError)
	}
	tasks := make(map[string]ChoreographyTaskSpec, len(opts.Tasks))
	templates := make(map[string]ChoreographyCompiledTemplate, len(opts.Templates))
	for _, template := range opts.Templates {
		if template.Task == choreographyGitAbsentState || templates[template.Task].Task != choreographyGitAbsentState {
			return nil, fmt.Errorf(choreographyGitDuplicateOrEmptyPersistedCompiledTemplateError)
		}
		templates[template.Task] = template
	}
	localTasks := make(map[string]ChoreographyTaskSpec)
	for _, task := range opts.Tasks {
		if task.Virtual {
			continue
		}
		if task.LocalAuthority {
			if task.Task == choreographyGitAbsentState || task.Branch != FormatChoreographyTaskBranch(opts.Binding.Run, task.Task) ||
				task.ReservationOID == choreographyGitAbsentState ||
				task.Budget == choreographyGitAbsentState || task.Weight < choreographyGitMinimumTaskWeight || len(task.EligibleNodes) != choreographyGitSingleEligibleNode ||
				localTasks[task.Task].Task != choreographyGitAbsentState || tasks[task.Task].Task != choreographyGitAbsentState {
				return nil, fmt.Errorf(choreographyGitInvalidLocalAuthorityTaskSpecificationError)
			}
			localTasks[task.Task] = task
		}
		if task.Task == choreographyGitAbsentState || task.Branch != FormatChoreographyTaskBranch(opts.Binding.Run, task.Task) ||
			task.Budget == choreographyGitAbsentState || task.Weight < choreographyGitMinimumTaskWeight || tasks[task.Task].Task != choreographyGitAbsentState {
			return nil, fmt.Errorf(choreographyGitInvalidFirstNodeTaskSpecificationError)
		}
		tasks[task.Task] = task
	}
	return &GitChoreographyPersist{opts: opts, ledgerTip: opts.Ledger.OID, tasks: tasks,
		templates:    templates,
		localTasks:   localTasks,
		eventIndex:   map[string]uint64{opts.Ledger.OID: choreographyGitLedgerRootSequence},
		packageState: make(map[string]string),
		accepted:     make(map[string]ChoreographyEventCursor),
		refused:      make(map[string]ChoreographyEventCursor),
		joined:       make(map[string]ChoreographyEventCursor),
		drained:      make(map[string]ChoreographyEventCursor),
		joinRefused:  make(map[string]ChoreographyEventCursor)}, nil
}

// PersistAssignment atomically publishes one compiled placement on its one
// logical task ref, the selected node's precreated index and the signed
// ledger. It reserves no capacity; only a later accepted Claim does that.
func (g *GitChoreographyPersist) PersistAssignment(ctx context.Context,
	offer ChoreographyAssignmentOffer) (ChoreographyAssignmentReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyAssignmentReceipt{}, err
	}
	a := offer.Assignment
	if a == nil || a.Protocol != ChoreographyProtocolVersion ||
		a.Run != g.opts.Binding.Run || a.Generation != g.opts.Binding.Generation ||
		a.PlanDigest != g.opts.Binding.PlanDigest || a.Task == choreographyGitAbsentState || a.Node == choreographyGitAbsentState ||
		a.GraphDigest != g.opts.Binding.GraphDigest || a.BootstrapOID != g.opts.BootstrapOID ||
		a.Attempt < choreographyGitFirstAttempt || offer.ExpectedTaskOID == choreographyGitAbsentState {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyGitInvalidCompiledAssignmentError)
	}
	spec := g.tasks[a.Task]
	if spec.Task == choreographyGitAbsentState || spec.Branch != a.Branch || spec.Kind != a.Kind || spec.Stage != a.Stage ||
		(offer.SlotBranch == choreographyGitAbsentState) != (offer.OldSlotOID == choreographyGitAbsentState) ||
		(offer.SlotBranch == choreographyGitAbsentState && offer.SlotSequence != choreographyGitNoSlotSequence) ||
		(offer.SlotBranch != choreographyGitAbsentState && offer.SlotSequence == choreographyGitNoSlotSequence) {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyGitCompiledAssignmentHasNoCanonicalTaskOrSlotError)
	}
	mailbox := g.opts.Mailbox
	document, err := json.Marshal(offer)
	if err != nil {
		return ChoreographyAssignmentReceipt{}, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	assignmentOID, slotOID := g.pendingOfferOIDs(identity)
	if assignmentOID == choreographyGitAbsentState {
		assignmentOID, err = mailbox.PrepareAssignmentOnReservation(ctx, a, offer.ExpectedTaskOID)
		if err != nil {
			return ChoreographyAssignmentReceipt{}, err
		}
	}
	if offer.SlotBranch != choreographyGitAbsentState && slotOID == choreographyGitAbsentState {
		notice := &SlotNotice{Header: Header{Protocol: ChoreographyProtocolVersion,
			Kind: KindSlot, Run: a.Run, Generation: a.Generation,
			PlanDigest: a.PlanDigest, Task: a.Task, Attempt: a.Attempt,
			Node: a.Node, Branch: offer.SlotBranch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339)},
			BootstrapOID: g.opts.BootstrapOID,
			Sequence:     offer.SlotSequence, TaskBranch: a.Branch, AssignmentOID: assignmentOID}
		slotOID, err = mailbox.PrepareSlotNotice(ctx, notice, offer.OldSlotOID)
		if err != nil {
			return ChoreographyAssignmentReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, TaskBranch: a.Branch,
		OldTaskOID: offer.ExpectedTaskOID, NewTaskOID: assignmentOID,
		SlotBranch: offer.SlotBranch, OldSlotOID: offer.OldSlotOID, NewSlotOID: slotOID,
		Event: ChoreographyEvent{Kind: ChoreographyAssignmentOffered, Task: a.Task,
			Attempt: a.Attempt, Node: a.Node, TaskOID: assignmentOID,
			PriorTaskOID: offer.ExpectedTaskOID, SlotBranch: offer.SlotBranch,
			PriorSlotOID: offer.OldSlotOID, SlotOID: slotOID,
			InputsDigest: CompiledClaimInputDigest(assignmentOID)},
	}); err != nil {
		return ChoreographyAssignmentReceipt{}, err
	}
	cursor, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyAssignmentReceipt{}, err
	}
	return ChoreographyAssignmentReceipt{AssignmentOID: assignmentOID, SlotOID: slotOID,
		Cursor: cursor}, nil
}

func (g *GitChoreographyPersist) pendingOfferOIDs(identity string) (task, slot string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil && g.pending.mutation.Identity == identity {
		return g.pending.mutation.NewTaskOID, g.pending.mutation.NewSlotOID
	}
	return choreographyGitAbsentState, choreographyGitAbsentState
}

func (g *GitChoreographyPersist) acceptedCursor(digest string) ChoreographyEventCursor {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accepted[digest]
}

func (g *GitChoreographyPersist) PersistLocalOutcome(ctx context.Context,
	outcome ChoreographyLocalOutcome) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyGitAbsentState, err
	}
	if outcome.ChoreographyRunBinding != g.opts.Binding || outcome.Task == choreographyGitAbsentState ||
		outcome.Node == choreographyGitAbsentState || outcome.ClaimOID == choreographyGitAbsentState || outcome.EffectDigest == choreographyGitAbsentState ||
		g.localTasks[outcome.Task].Task == choreographyGitAbsentState {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidLocalAuthorityOutcomeError)
	}
	spec := g.localTasks[outcome.Task]
	if err := validateChoreographyLocalEffect(outcome, spec); err != nil {
		return choreographyGitAbsentState, err
	}
	if err := validateChoreographyPackageOutcome(ChoreographyPackageOutcomeCheck{
		Task: spec, Outcome: outcome.PackageOutcome, Succeeded: outcome.Succeeded}); err != nil {
		return choreographyGitAbsentState, err
	}
	document, err := json.Marshal(outcome)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyGitAbsentState {
		oid, err = g.opts.Mailbox.commitMessage(ctx, choreographyLocalOutcomeKind,
			document, []string{outcome.ClaimOID}, nil)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{Identity: identity,
		TaskBranch: spec.Branch, OldTaskOID: outcome.ClaimOID,
		NewTaskOID: oid, Budget: spec.Budget, Node: outcome.Node, Delta: -spec.Weight,
		Event: ChoreographyEvent{Kind: ChoreographyTerminal, Task: outcome.Task,
			Attempt: choreographyGitFirstAttempt, Node: outcome.Node, TaskOID: oid, PriorTaskOID: outcome.ClaimOID,
			GraphRootOID: g.opts.GraphRootOID,
			Budget:       spec.Budget, Weight: spec.Weight, Succeeded: outcome.Succeeded,
			Package: spec.Package, PackageOutcome: outcome.PackageOutcome},
	}); err != nil {
		return choreographyGitAbsentState, err
	}
	return oid, nil
}

type ChoreographyFrameReadyRequest struct {
	TaskID           string
	Attempt          int
	TaskOID          string
	PreparedInputOID string
	InputsDigest     string
}

func (g *GitChoreographyPersist) PersistFrameReady(ctx context.Context,
	request ChoreographyFrameReadyRequest) error {
	taskID, attempt := request.TaskID, request.Attempt
	taskOID, preparedInputOID, inputsDigest := request.TaskOID, request.PreparedInputOID, request.InputsDigest
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	spec := g.tasks[taskID]
	if spec.Task == choreographyGitAbsentState || attempt < choreographyGitFirstAttempt || taskOID == choreographyGitAbsentState || inputsDigest == choreographyGitAbsentState {
		return fmt.Errorf(choreographyGitReadyFrameNamesNoCanonicalTaskOrInputError)
	}
	if spec.NeedsPreparedInput {
		carrier, err := g.opts.Mailbox.ReadChoreographyPreparedCarrier(ctx, preparedInputOID, returnReplyLimit)
		if err != nil {
			return err
		}
		if carrier.ChoreographyRunBinding != g.opts.Binding || carrier.GraphRootOID != g.opts.GraphRootOID ||
			carrier.Task != taskID || carrier.TemplateDigest != spec.TemplateDigest {
			return fmt.Errorf(choreographyPreparedIdentityMismatchError)
		}
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	for _, event := range g.events {
		if event.Kind == ChoreographyFrameReady && event.Task == taskID &&
			event.Attempt == attempt &&
			event.TaskOID == taskOID && event.InputsDigest == inputsDigest &&
			event.PreparedInputOID == preparedInputOID {
			g.mu.Unlock()
			return nil
		}
	}
	g.mu.Unlock()
	identityDocument, err := json.Marshal(struct {
		Task             string
		Attempt          int
		TaskOID          string
		InputsDigest     string
		PreparedInputOID string
	}{taskID, attempt, taskOID, inputsDigest, preparedInputOID})
	if err != nil {
		return err
	}
	identityHash := sha256.Sum256(identityDocument)
	identity := hex.EncodeToString(identityHash[:])
	return g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, ExpectedLedgerOID: head.OID, NewTaskOID: taskOID,
		Event: ChoreographyEvent{Kind: ChoreographyFrameReady, Task: taskID, Attempt: attempt,
			TaskOID: taskOID, PriorTaskOID: taskOID, InputsDigest: inputsDigest,
			PreparedInputOID: preparedInputOID,
			GraphRootOID:     g.opts.GraphRootOID},
	})
}

func (g *GitChoreographyPersist) PersistClaim(ctx context.Context, p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := p.validate(); err != nil {
		return ChoreographyReceipt{}, err
	}
	digest, err := ChoreographyProposalDigest(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	m := g.opts.Mailbox
	spec := g.tasks[p.Task]
	if spec.Task == choreographyGitAbsentState || spec.Branch != p.Branch || p.GraphRootOID != g.opts.GraphRootOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitClaimNamesNoTaskInTheCompleteSignedGraphRootError)
	}
	if err := validateChoreographySignedPickupID(g.opts.JoinPolicy, spec, p.PickupID); err != nil {
		return ChoreographyReceipt{}, err
	}
	assignmentOID := choreographyGitAbsentState
	if spec.TemplateDigest != choreographyGitAbsentState {
		if p.resolvedBindings == nil {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitCompiledClaimHasNoFirstNodeResolvedEffectiveBindingsError)
		}
		template := g.templates[p.Task]
		templateDigest, templateErr := DigestChoreographyCompiledTemplate(template)
		bindingsDigest, bindingsErr := DigestChoreographyTaskBindings(*p.resolvedBindings)
		if templateErr != nil || bindingsErr != nil {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitClaimTemplateOrBindingsCannotBeDigestedCauseErrorFormat, errors.Join(templateErr, bindingsErr))
		}
		if templateDigest != spec.TemplateDigest ||
			p.TemplateDigest != templateDigest {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitClaimTemplateDiffersFromSignedGraphError)
		}
		assignmentOID = g.pendingClaimAssignmentOID(digest)
		if assignmentOID != choreographyGitAbsentState {
			view, readErr := m.OpenCarriedAssignment(ctx, assignmentOID, returnReplyLimit)
			if readErr != nil {
				return ChoreographyReceipt{}, readErr
			}
			if view == nil || view.Assignment.Protocol != ChoreographyProtocolVersion ||
				view.Assignment.Kind != spec.Kind || view.Assignment.Run != p.Run ||
				view.Assignment.Generation != p.Generation ||
				view.Assignment.PlanDigest != p.PlanDigest ||
				view.Assignment.GraphDigest != p.GraphDigest ||
				view.Assignment.Task != p.Task || view.Assignment.Attempt != p.Attempt ||
				view.Assignment.Node != p.Node || view.Assignment.Branch != p.Branch ||
				view.Assignment.BootstrapOID != g.opts.BootstrapOID ||
				view.Assignment.ReturnEndpoint != g.opts.ReturnEndpoint ||
				view.Assignment.ReturnCertSHA256 != g.opts.ReturnCertSHA256 ||
				view.Assignment.TemplateDigest != templateDigest ||
				view.Assignment.BindingsDigest != bindingsDigest ||
				view.Assignment.GraphRootOID != g.opts.GraphRootOID ||
				view.Assignment.InputsDigest != p.InputsDigest {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyGitUnresolvedExactClaimHasDifferentPreparedAssignmentInputsError)
			}
		} else {
			header := Header{Protocol: ChoreographyProtocolVersion, Kind: spec.Kind,
				Run: p.Run, PlanDigest: p.PlanDigest, Task: p.Task,
				Attempt: p.Attempt, Generation: p.Generation, Node: p.Node,
				Branch: p.Branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)}
			assignment, bindErr := BindChoreographyCompiledTemplate(ChoreographyTemplateBindingOptions{
				Template:     template,
				Bindings:     *p.resolvedBindings,
				Header:       header,
				BootstrapOID: g.opts.BootstrapOID,
				GraphDigest:  p.GraphDigest,
			})
			if bindErr != nil {
				return ChoreographyReceipt{}, bindErr
			}
			assignment.ReturnEndpoint = g.opts.ReturnEndpoint
			assignment.ReturnCertSHA256 = g.opts.ReturnCertSHA256
			assignment.GraphRootOID = g.opts.GraphRootOID
			assignment.InputsDigest = p.InputsDigest
			assignment.TemplateDigest, assignment.BindingsDigest = templateDigest, bindingsDigest
			assignmentOID, err = m.PrepareAssignmentOnReservation(ctx, &assignment, p.ExpectedOID)
			if err != nil {
				return ChoreographyReceipt{}, err
			}
		}
	}
	pickupDeadlineUTC := choreographyGitAbsentState
	if p.PickupID != choreographyGitAbsentState {
		if assignmentOID == choreographyGitAbsentState {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitCompiledClaimHasNoFirstNodeResolvedEffectiveBindingsError)
		}
		view, readErr := m.OpenCarriedAssignment(ctx, assignmentOID, returnReplyLimit)
		if readErr != nil {
			return ChoreographyReceipt{}, readErr
		}
		pickupDeadlineUTC, err = choreographyPickupDeadline(view.Assignment.IssuedAt,
			view.Assignment.DeadlineSeconds)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	document, err := json.Marshal(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	newOID := g.pendingTaskOID(digest)
	if newOID == choreographyGitAbsentState {
		parent := p.ExpectedOID
		if assignmentOID != choreographyGitAbsentState {
			parent = assignmentOID
		}
		newOID, err = m.commitMessage(ctx, choreographyClaimKind, document, []string{parent}, nil)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, ExpectedLedgerOID: p.ExpectedLedgerOID,
		TaskBranch: p.Branch, OldTaskOID: p.ExpectedOID, NewTaskOID: newOID,
		Budget: spec.Budget, Node: p.Node, Delta: spec.Weight,
		Event: ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: p.Task,
			Attempt: p.Attempt, Node: p.Node, TaskOID: newOID,
			AssignmentOID: assignmentOID, GraphRootOID: p.GraphRootOID,
			MembershipEpochOID:  p.MembershipEpochOID,
			CapacityAuthorityID: p.CapacityAuthorityID,
			LeaseIntentID:       p.LeaseIntentID, LeaseWeight: p.LeaseWeight,
			PickupID:          p.PickupID,
			PickupDeadlineUTC: pickupDeadlineUTC,
			PriorTaskOID:      p.ExpectedOID,
			ProposalDigest:    digest, ProposalID: p.ProposalID,
			InputsDigest: p.InputsDigest},
	}); err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyClaimKind,
		Task: p.Task, Attempt: p.Attempt, Node: p.Node, Branch: p.Branch,
		ProposalID: p.ProposalID, ProposalDigest: digest, OID: newOID,
		GraphRootOID: p.GraphRootOID, AssignmentOID: assignmentOID,
		PickupID:          p.PickupID,
		PickupDeadlineUTC: pickupDeadlineUTC,
		Accepted:          choreographyGitReceiptAccepted, Cursor: g.acceptedCursor(digest)}, nil
}

// PersistRefusal durably fences a well-formed but currently unclaimable
// proposal. The ledger-only CAS changes the exact epoch named by the body;
// a delayed copy of that same body can no longer acquire work. If the epoch
// already changed, that immutable newer head itself is the fence.
func (g *GitChoreographyPersist) PersistRefusal(ctx context.Context,
	p ChoreographyClaimProposal, reason string) (ChoreographyReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := p.validate(); err != nil {
		return ChoreographyReceipt{}, err
	}
	if reason != ChoreographyRefusalCapacity && reason != ChoreographyRefusalNodeCapacity &&
		reason != ChoreographyRefusalBudgetCapacity && reason != ChoreographyRefusalBusy &&
		reason != ChoreographyRefusalNotReady && reason != ChoreographyRefusalStale &&
		reason != ChoreographyRefusalTerminal && reason != ChoreographyRefusalSealed &&
		reason != ChoreographyRefusalStopping {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitInvalidRefusalReasonError)
	}
	spec := g.tasks[p.Task]
	if spec.Task == choreographyGitAbsentState || spec.Branch != p.Branch {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitRefusalNamesNoCanonicalTaskRefError)
	}
	scope := choreographyGitAbsentState
	if reason == ChoreographyRefusalCapacity || reason == ChoreographyRefusalBudgetCapacity {
		scope, reason = choreographyGitRefusalBudgetScope, ChoreographyRefusalCapacity
	} else if reason == ChoreographyRefusalNodeCapacity {
		scope, reason = choreographyGitRefusalNodeScope, ChoreographyRefusalCapacity
	}
	digest, err := ChoreographyProposalDigest(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	wroteRefusal := choreographyGitRefusalNotWritten
	g.mu.Lock()
	stopped := g.stopping
	_, alreadyRefused := g.refused[digest]
	g.mu.Unlock()
	if reason == ChoreographyRefusalStopping && stopped && head.OID != p.ExpectedLedgerOID {
		if alreadyRefused {
			query, queryErr := FormatClaimReceiptQuery(p)
			if queryErr != nil {
				return ChoreographyReceipt{}, queryErr
			}
			receipt, found, lookupErr := g.LookupReceipt(ctx, query)
			if lookupErr != nil {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyGitStoppedClaimReceiptLookupFailedCauseErrorFormat, lookupErr)
			}
			if !found {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyGitStoppedClaimHasNoExactDurableRefusalError)
			}
			return receipt, nil
		}
		taskTip, tipErr := g.opts.Mailbox.readRemoteTip(ctx, p.Branch)
		if tipErr != nil || taskTip != p.ExpectedOID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitStoppedRefusalCannotFenceAnInconsistentTaskRefError)
		}
		if err := g.persistAtomic(ctx, choreographyLedgerMutation{
			Identity: digest, ExpectedLedgerOID: head.OID, NewTaskOID: p.ExpectedOID,
			Event: ChoreographyEvent{Kind: ChoreographyClaimRefused, Task: p.Task,
				Attempt: p.Attempt, Node: p.Node, TaskOID: p.ExpectedOID,
				GraphRootOID: p.GraphRootOID, ProposalDigest: digest,
				ProposalID: p.ProposalID, RefusalReason: ChoreographyRefusalStopping,
				Budget: spec.Budget},
		}); err != nil {
			return ChoreographyReceipt{}, err
		}
		head, err = g.LedgerHead(ctx)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		wroteRefusal = choreographyGitRefusalWritten
	}
	if head.OID == p.ExpectedLedgerOID {
		taskTip, err := g.opts.Mailbox.readRemoteTip(ctx, p.Branch)
		if err != nil || taskTip != p.ExpectedOID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyGitRefusalCannotFenceAnInconsistentTaskRefError)
		}
		if err := g.persistAtomic(ctx, choreographyLedgerMutation{
			Identity: digest, ExpectedLedgerOID: p.ExpectedLedgerOID,
			NewTaskOID: p.ExpectedOID,
			Event: ChoreographyEvent{Kind: ChoreographyClaimRefused, Task: p.Task,
				Attempt: p.Attempt, Node: p.Node, TaskOID: p.ExpectedOID,
				GraphRootOID:   p.GraphRootOID,
				ProposalDigest: digest, ProposalID: p.ProposalID,
				RefusalReason: reason, CapacityScope: scope, Budget: spec.Budget},
		}); err != nil {
			return ChoreographyReceipt{}, err
		}
		head, err = g.LedgerHead(ctx)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		wroteRefusal = choreographyGitRefusalWritten
	}
	if head.OID == p.ExpectedLedgerOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitRefusalHasNoDurableEpochFenceError)
	}
	if !wroteRefusal {
		// A pre-existing epoch change is sufficient proof of refusal even if
		// it came from another task. The signed reason is stale in that case.
		reason = ChoreographyRefusalStale
		scope = choreographyGitAbsentState
		g.mu.Lock()
		if g.sealed {
			reason = ChoreographyRefusalSealed
		} else if g.stopping {
			reason = ChoreographyRefusalStopping
		}
		g.mu.Unlock()
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyClaimKind, Task: p.Task, Attempt: p.Attempt, Node: p.Node,
		Branch: p.Branch, ProposalID: p.ProposalID, ProposalDigest: digest,
		GraphRootOID: p.GraphRootOID,
		OID:          p.ExpectedOID, Refused: choreographyGitReceiptRefused, FenceOID: head.OID,
		FenceSequence: head.Sequence, RefusalReason: reason,
		CapacityScope: scope, Budget: spec.Budget}, nil
}

func (g *GitChoreographyPersist) PersistResult(ctx context.Context, p ChoreographyResultProposal,
	payload *os.File) (ChoreographyReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := p.validate(g.opts.MaxPackBytes); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := validateChoreographySignedPickupID(g.opts.JoinPolicy, g.tasks[p.Task], p.PickupID); err != nil {
		return ChoreographyReceipt{}, err
	}
	digest, err := ChoreographyProposalDigest(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if payload == nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitResultPackIsMissingError)
	}
	m := g.opts.Mailbox
	if _, err := payload.Seek(choreographyGitSeekStartOffset, io.SeekStart); err != nil {
		return ChoreographyReceipt{}, err
	}
	// The HTTPS handler checks these fields while receiving the stream. Keep
	// the same bound and digest gate here for first-local in-process returns.
	hasher := sha256.New()
	count, err := io.CopyBuffer(hasher, io.LimitReader(payload, g.opts.MaxPackBytes+choreographyGitResultReadOverflowBytes),
		make([]byte, choreographyGitResultCopyBufferBytes))
	if err != nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitResultPackDiffersFromSignedTransferIdentityCauseErrorFormat, err)
	}
	if count != p.PayloadBytes || count > g.opts.MaxPackBytes ||
		hex.EncodeToString(hasher.Sum(nil)) != p.PayloadSHA256 {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyResultPackMismatchError)
	}
	if _, err := payload.Seek(choreographyGitSeekStartOffset, io.SeekStart); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := m.plumbing.ImportCommitPack(ctx, p.ResultDigest, payload, g.opts.MaxPackBytes); err != nil {
		return ChoreographyReceipt{}, err
	}
	carried, err := m.readTree(ctx, p.ResultDigest)
	if err != nil || carried.kind != MessageResult {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitImportedResultIsNotASignedResultCommitError)
	}
	document, err := m.Read(ctx, ChainTip{Kind: carried.kind, document: carried.document,
		signature: carried.signature}, g.opts.MaxPackBytes)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	var result Result
	if err := strictJSON(document, &result); err != nil {
		return ChoreographyReceipt{}, err
	}
	if result.Run != p.Run || result.Generation != p.Generation || result.PlanDigest != p.PlanDigest ||
		result.Task != p.Task || result.Attempt != p.Attempt || result.Node != p.Node ||
		result.Branch != p.Branch || result.Assignment != p.ClaimOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitImportedResultAnswersAnotherClaimError)
	}
	if err := g.opts.VerifyResult(ctx, p, result, m.plumbing); err != nil {
		return ChoreographyReceipt{}, err
	}
	if err := m.VerifyChoreographyResultFromClaim(ctx, ChoreographyResultClaimProof{
		Proposal: p, Result: result, Store: m.plumbing,
		MaxDocumentBytes: g.opts.MaxPackBytes}); err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.PickupID != choreographyGitAbsentState {
		if err := m.VerifyChoreographyPickupResult(ctx, ChoreographyPickupResultProof{
			Proposal: p, Result: result, Mode: g.opts.JoinPolicy.pickupMode(),
			MaxDocumentBytes: g.opts.MaxPackBytes}); err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	outputs, err := m.exactChoreographyOutputEntry(ctx, p.ResultDigest, result)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.PickupID != choreographyGitAbsentState {
		return g.persistPickupResult(ctx, p, digest, outputs)
	}
	record, err := json.Marshal(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	newOID := g.pendingTaskOID(digest)
	if newOID == choreographyGitAbsentState {
		newOID, err = m.commitMessage(ctx, choreographyResultKind, record,
			[]string{p.ClaimOID, p.ResultDigest}, outputs)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, TaskBranch: p.Branch,
		OldTaskOID: p.ClaimOID, NewTaskOID: newOID,
		Event: ChoreographyEvent{Kind: ChoreographyResultAccepted,
			Task: p.Task, Attempt: p.Attempt, Node: p.Node, TaskOID: newOID,
			PriorTaskOID:   p.ClaimOID,
			ProposalDigest: digest, ProposalID: p.ProposalID, OutputOID: p.ResultDigest,
			PickupID: p.PickupID},
	}); err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyResultKind,
		Task: p.Task, Attempt: p.Attempt, Node: p.Node, Branch: p.Branch,
		ProposalID: p.ProposalID, ProposalDigest: digest, OID: newOID,
		PickupID: p.PickupID,
		Accepted: choreographyGitReceiptAccepted, Cursor: g.acceptedCursor(digest)}, nil
}

// PersistCancel atomically advances Claim to a signed Cancel and records the
// new exact task tip in the ledger. A Result that wins the task CAS prevents
// this cancellation from being reported as a stopped command.
func (g *GitChoreographyPersist) PersistCancel(ctx context.Context,
	c ChoreographyCancellation) (ChoreographyCancellationReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyCancellationReceipt{}, err
	}
	pendingIdentity, pendingOID := choreographyGitAbsentState, choreographyGitAbsentState
	g.mu.Lock()
	if pending := g.pending; pending != nil &&
		pending.mutation.Event.Kind == ChoreographyCancelIssued &&
		pending.mutation.Event.Task == c.Task &&
		pending.mutation.Event.Attempt == c.Attempt &&
		pending.mutation.Event.Node == c.Node &&
		pending.mutation.Event.ClaimOID == c.ClaimOID &&
		pending.mutation.TaskBranch == c.Branch &&
		pending.mutation.OldTaskOID == c.ClaimOID {
		c.CancelReplyDeadlineUTC = pending.mutation.Event.CancelReplyDeadlineUTC
		pendingIdentity, pendingOID = pending.mutation.Identity, pending.mutation.NewTaskOID
	}
	g.mu.Unlock()
	if c.ChoreographyRunBinding != g.opts.Binding || c.Task == choreographyGitAbsentState || c.Attempt < choreographyGitFirstAttempt ||
		c.Node == choreographyGitAbsentState || c.ClaimOID == choreographyGitAbsentState || c.AssignmentOID == choreographyGitAbsentState ||
		g.tasks[c.Task].Branch != c.Branch {
		return ChoreographyCancellationReceipt{}, fmt.Errorf(choreographyGitInvalidCompiledCancellationError)
	}
	if _, err := time.Parse(time.RFC3339, c.CancelReplyDeadlineUTC); err != nil {
		return ChoreographyCancellationReceipt{}, fmt.Errorf(choreographyGitInvalidCompiledCancellationError)
	}
	m := g.opts.Mailbox
	view, err := m.OpenCarriedAssignment(ctx, c.AssignmentOID, returnReplyLimit)
	if err != nil {
		return ChoreographyCancellationReceipt{}, err
	}
	a := view.Assignment
	if a.Protocol != ChoreographyProtocolVersion || a.Run != c.Run ||
		a.Generation != c.Generation || a.PlanDigest != c.PlanDigest ||
		a.GraphDigest != c.GraphDigest || a.Task != c.Task ||
		a.Branch != c.Branch || a.Attempt != c.Attempt || a.Node != c.Node {
		return ChoreographyCancellationReceipt{}, fmt.Errorf(choreographyGitCancellationAssignmentDiffersFromActiveAttemptError)
	}
	parents, err := m.plumbing.CommitParents(ctx, c.ClaimOID)
	if err != nil || len(parents) != choreographyGitClaimCarrierParentCount || parents[choreographyGitFirstParentIndex] != c.AssignmentOID {
		return ChoreographyCancellationReceipt{}, fmt.Errorf(choreographyGitCancellationClaimIsNotDescendedFromOfferedAssignmentError)
	}
	pickupID, err := m.readChoreographyClaimPickupID(ctx, c.ClaimOID)
	if err != nil {
		return ChoreographyCancellationReceipt{}, err
	}
	identity, oid := pendingIdentity, pendingOID
	if identity == choreographyGitAbsentState {
		header := a.Header
		header.IssuedAt = time.Now().UTC().Format(time.RFC3339)
		withdrawal := Withdrawal{Header: header, Assignment: c.AssignmentOID, Tip: c.ClaimOID,
			CancelReplyDeadlineUTC: c.CancelReplyDeadlineUTC}
		document, marshalErr := json.Marshal(withdrawal)
		if marshalErr != nil {
			return ChoreographyCancellationReceipt{}, marshalErr
		}
		sum := sha256.Sum256(document)
		identity = hex.EncodeToString(sum[:])
		oid, err = m.commitMessage(ctx, MessageCancel, document, []string{c.ClaimOID}, nil)
		if err != nil {
			return ChoreographyCancellationReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, TaskBranch: c.Branch,
		OldTaskOID: c.ClaimOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyCancelIssued, Task: c.Task,
			Attempt: c.Attempt, Node: c.Node, TaskOID: oid,
			PriorTaskOID: c.ClaimOID, ClaimOID: c.ClaimOID, PickupID: pickupID,
			CancelReplyDeadlineUTC: c.CancelReplyDeadlineUTC,
			GraphRootOID:           g.opts.GraphRootOID},
	}); err != nil {
		return ChoreographyCancellationReceipt{}, err
	}
	return ChoreographyCancellationReceipt{OID: oid,
		CancelReplyDeadlineUTC: c.CancelReplyDeadlineUTC}, nil
}

// PersistQueueFence revokes a final unclaimed offer before any failure hook.
// The signed Cancel wins Assignment's exact CAS against a late Claim, while
// the ledger has no terminal event until the hook has settled.
func (g *GitChoreographyPersist) PersistQueueFence(ctx context.Context,
	f ChoreographyQueueFence) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyGitAbsentState, err
	}
	if f.ChoreographyRunBinding != g.opts.Binding || f.Task == choreographyGitAbsentState ||
		f.Attempt < choreographyGitFirstAttempt || f.Node == choreographyGitAbsentState || f.AssignmentOID == choreographyGitAbsentState ||
		g.tasks[f.Task].Branch != f.Branch {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidUnclaimedQueueFenceError)
	}
	m := g.opts.Mailbox
	view, err := m.OpenCarriedAssignment(ctx, f.AssignmentOID, returnReplyLimit)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	a := view.Assignment
	if a.Protocol != ChoreographyProtocolVersion || a.Run != f.Run ||
		a.Generation != f.Generation || a.PlanDigest != f.PlanDigest ||
		a.GraphDigest != f.GraphDigest || a.Task != f.Task ||
		a.Branch != f.Branch || a.Attempt != f.Attempt || a.Node != f.Node {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitQueueFenceDiffersFromOfferedAssignmentError)
	}
	header := a.Header
	header.IssuedAt = time.Now().UTC().Format(time.RFC3339)
	withdrawal := Withdrawal{Header: header, Assignment: f.AssignmentOID, Tip: f.AssignmentOID}
	document, err := json.Marshal(withdrawal)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyGitAbsentState {
		oid, err = m.commitMessage(ctx, MessageCancel, document, []string{f.AssignmentOID}, nil)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, TaskBranch: f.Branch,
		OldTaskOID: f.AssignmentOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyQueueFenced, Task: f.Task,
			Attempt: f.Attempt, Node: f.Node, TaskOID: oid,
			PriorTaskOID: f.AssignmentOID},
	}); err != nil {
		return choreographyGitAbsentState, err
	}
	return oid, nil
}

// PersistUnclaimedDecision advances a queued attempt to a signed retry or
// final decision. A final decision's parent is the pre-hook queue fence.
func (g *GitChoreographyPersist) PersistUnclaimedDecision(ctx context.Context,
	d ChoreographyDecision) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyGitAbsentState, err
	}
	if d.ChoreographyRunBinding != g.opts.Binding || !d.Unclaimed || d.Task == choreographyGitAbsentState ||
		d.Attempt < choreographyGitFirstAttempt || d.Node == choreographyGitAbsentState || d.ResultOID == choreographyGitAbsentState || d.Succeeded ||
		g.tasks[d.Task].Branch != d.Branch || d.Retry &&
		(d.PackageOutcome != choreographyGitAbsentState || d.OfferedOID != choreographyGitAbsentState) ||
		!d.Retry && d.OfferedOID == choreographyGitAbsentState {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidUnclaimedQueueDecisionError)
	}
	m := g.opts.Mailbox
	assignmentOID := d.ResultOID
	if !d.Retry {
		assignmentOID = d.OfferedOID
		parents, err := m.plumbing.CommitParents(ctx, d.ResultOID)
		if err != nil || len(parents) != choreographyGitClaimCarrierParentCount || parents[choreographyGitFirstParentIndex] != d.OfferedOID {
			return choreographyGitAbsentState, fmt.Errorf(choreographyGitFinalQueueDecisionLacksItsExactPreHookFenceError)
		}
		fence, err := m.readTree(ctx, d.ResultOID)
		if err != nil || fence.kind != MessageCancel {
			return choreographyGitAbsentState, fmt.Errorf(choreographyGitFinalQueueDecisionHasNoSignedCancellationError)
		}
	}
	view, err := m.OpenCarriedAssignment(ctx, assignmentOID, returnReplyLimit)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	a := view.Assignment
	if a.Run != d.Run || a.Generation != d.Generation ||
		a.PlanDigest != d.PlanDigest || a.GraphDigest != d.GraphDigest ||
		a.Protocol != ChoreographyProtocolVersion || a.Task != d.Task ||
		a.Branch != d.Branch || a.Attempt != d.Attempt || a.Node != d.Node {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitQueueDecisionDiffersFromOfferedAssignmentError)
	}
	document, err := json.Marshal(d)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyGitAbsentState {
		oid, err = m.commitMessage(ctx, choreographyDecisionKind, document, []string{d.ResultOID}, nil)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	}
	kind := ChoreographyTerminal
	if d.Retry {
		kind = ChoreographyRetryReady
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, TaskBranch: d.Branch,
		OldTaskOID: d.ResultOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: kind, Task: d.Task, Attempt: d.Attempt,
			Node: d.Node, TaskOID: oid, PriorTaskOID: d.ResultOID,
			Package: g.tasks[d.Task].Package, PackageOutcome: d.PackageOutcome},
	}); err != nil {
		return choreographyGitAbsentState, err
	}
	return oid, nil
}

func (g *GitChoreographyPersist) PersistAck(ctx context.Context, p ChoreographyAckProposal) (ChoreographyReceipt, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.Task == choreographyGitAbsentState || p.Attempt < choreographyGitFirstAttempt || p.Node == choreographyGitAbsentState || p.Branch == choreographyGitAbsentState || p.CancelOID == choreographyGitAbsentState ||
		p.AssignmentOID == choreographyGitAbsentState || p.ProposalID == choreographyGitAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitInvalidCancellationAcknowledgementError)
	}
	digest, err := ChoreographyProposalDigest(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	m := g.opts.Mailbox
	head, err := m.Reread(ctx, p.Branch)
	if err != nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitCancellationRefIsNotAtTheObservedCancelError)
	}
	if head.OID != p.CancelOID {
		return g.persistPickupSideAck(ctx, p, digest, head.OID)
	}
	tip, err := m.Inspect(ctx, head)
	if err != nil || tip.Kind != MessageCancel {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitExpectedASignedCancellationError)
	}
	document, err := m.Read(ctx, tip, returnReplyLimit)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	var withdrawal Withdrawal
	if err := strictJSON(document, &withdrawal); err != nil || withdrawal.Assignment != p.AssignmentOID ||
		withdrawal.Run != p.Run || withdrawal.Generation != p.Generation || withdrawal.PlanDigest != p.PlanDigest ||
		withdrawal.Task != p.Task || withdrawal.Attempt != p.Attempt || withdrawal.Branch != p.Branch ||
		withdrawal.Node != p.Node {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyGitCancellationDoesNotMatchAcknowledgementError)
	}
	pickupID, err := m.readChoreographyClaimPickupID(ctx, withdrawal.Tip)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	oid := g.pendingTaskOID(digest)
	if oid == choreographyGitAbsentState {
		ack := Ack{Header: withdrawal.Header, ProposalID: p.ProposalID, ProposalDigest: digest, Assignment: p.AssignmentOID,
			Cancel: p.CancelOID, Phase: p.Phase, CommandStarted: p.CommandStarted}
		ack.IssuedAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.Marshal(ack)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		oid, err = m.commitMessage(ctx, MessageAck, data, []string{p.CancelOID}, nil)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, TaskBranch: p.Branch,
		OldTaskOID: p.CancelOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyAckAccepted,
			Task: p.Task, Attempt: p.Attempt, Node: p.Node, TaskOID: oid,
			PriorTaskOID: p.CancelOID, ClaimOID: withdrawal.Tip,
			PickupID:       pickupID,
			GraphRootOID:   g.opts.GraphRootOID,
			ProposalDigest: digest, ProposalID: p.ProposalID},
	}); err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding, Kind: MessageAck,
		Task: p.Task, Attempt: p.Attempt, Node: p.Node, Branch: p.Branch, ProposalID: p.ProposalID,
		ProposalDigest: digest, OID: oid, PickupID: pickupID, GraphRootOID: g.opts.GraphRootOID,
		Accepted: choreographyGitReceiptAccepted, Cursor: g.acceptedCursor(digest)}, nil
}

func (g *GitChoreographyPersist) PersistDecision(ctx context.Context, d ChoreographyDecision) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyGitAbsentState, err
	}
	if d.Task == choreographyGitAbsentState || d.Branch == choreographyGitAbsentState || d.Attempt < choreographyGitFirstAttempt || d.ResultOID == choreographyGitAbsentState || d.Node == choreographyGitAbsentState ||
		(d.Retry && d.Succeeded) {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidTaskDecisionError)
	}
	// A decision's branch is derived from the same one-task-ref rule; its
	// run/task identity, not an attempt, determines the ref name.
	branch := d.Branch
	document, err := json.Marshal(d)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	m := g.opts.Mailbox
	decisionSum := sha256.Sum256(document)
	identity := hex.EncodeToString(decisionSum[:])
	spec := g.tasks[d.Task]
	if spec.Task == choreographyGitAbsentState || spec.Branch != branch {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitDecisionNamesNoCanonicalTaskRefError)
	}
	if err := validateChoreographyPackageOutcome(ChoreographyPackageOutcomeCheck{
		Task: spec, Outcome: d.PackageOutcome, Succeeded: d.Succeeded, Retry: d.Retry}); err != nil {
		return choreographyGitAbsentState, err
	}
	var outputOID string
	var outputEntries []gitx.TreeEntry
	resultTree, err := m.readTree(ctx, d.ResultOID)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	if d.Retry && (d.Attempt >= spec.EffectiveMaxAttempts() ||
		!spec.IsPickupRetryAllowed(resultTree.kind == choreographyResultKind)) {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidTaskDecisionError)
	}
	if resultTree.kind == choreographyResultKind {
		outputOID, outputEntries, err = g.decisionOutputEntries(ctx, d)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	} else if resultTree.kind != MessageAck {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitDecisionNamesNoSettledResultOrAcknowledgementError)
	} else if _, err := m.exactChoreographyOutputEntry(ctx, d.ResultOID, Result{}); err != nil {
		return choreographyGitAbsentState, err
	}
	if d.Succeeded && outputOID == choreographyGitAbsentState {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitSuccessfulDecisionHasNoResultOutputError)
	}
	if d.Retry {
		outputEntries = nil
	}
	if resultTree.kind == choreographyResultKind {
		receiptBytes, readErr := m.Read(ctx, ChainTip{OID: d.ResultOID, Kind: resultTree.kind,
			document: resultTree.document, signature: resultTree.signature}, g.opts.MaxPackBytes)
		if readErr != nil {
			return choreographyGitAbsentState, readErr
		}
		var proposal ChoreographyResultProposal
		if strictJSON(receiptBytes, &proposal) != nil {
			return choreographyGitAbsentState, fmt.Errorf(choreographyGitDecisionNamesNoSettledResultOrAcknowledgementError)
		}
		if proposal.PickupID != choreographyGitAbsentState {
			if d.Retry && spec.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified {
				accepted := ChoreographyEvent{Kind: ChoreographyResultAccepted,
					Task: d.Task, TaskOID: d.ResultOID, ResultReceiptOID: d.ResultOID,
					OutputOID: proposal.ResultDigest, ClaimOID: proposal.ClaimOID,
					PickupID: proposal.PickupID, Attempt: d.Attempt, Node: d.Node}
				result, readErr := m.ReadChoreographyAcceptedResult(ctx, g.opts.Binding,
					accepted, g.opts.MaxPackBytes)
				if readErr != nil {
					return choreographyGitAbsentState, readErr
				}
				if result.Status != StatusFailed {
					return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidTaskDecisionError)
				}
			}
			return g.persistPickupDecision(ctx, choreographyPickupDecisionOptions{
				Decision: d, Document: document, Identity: identity,
				Spec: spec, OutputOID: outputOID, OutputEntries: outputEntries,
				Proposal: proposal})
		}
	} else if resultTree.kind == MessageAck {
		if oid, handled, err := g.persistPickupAckDecision(ctx, choreographyPickupAckDecisionOptions{
			Decision: d, Document: document, Identity: identity, Spec: spec}); err != nil {
			return choreographyGitAbsentState, err
		} else if handled {
			return oid, nil
		}
	}
	oid := g.pendingTaskOID(identity)
	if oid == choreographyGitAbsentState {
		oid, err = m.commitMessage(ctx, choreographyDecisionKind, document,
			[]string{d.ResultOID}, outputEntries)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	}
	kind := ChoreographyTerminal
	if d.Retry {
		kind = ChoreographyRetryReady
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity:   identity,
		TaskBranch: branch, OldTaskOID: d.ResultOID, NewTaskOID: oid,
		Budget: spec.Budget, Node: d.Node, Delta: -spec.Weight,
		Event: ChoreographyEvent{Kind: kind, Task: d.Task, Attempt: d.Attempt,
			Node: d.Node, TaskOID: oid, OutputOID: outputOID,
			PriorTaskOID: d.ResultOID,
			Budget:       spec.Budget, Weight: spec.Weight,
			ResultReceiptOID: d.ResultOID, Succeeded: d.Succeeded,
			Package: spec.Package, PackageOutcome: d.PackageOutcome},
	}); err != nil {
		return choreographyGitAbsentState, err
	}
	return oid, nil
}

type ChoreographyPackageOutcomeCheck struct {
	Task      ChoreographyTaskSpec
	Outcome   string
	Succeeded bool
	Retry     bool
}

func validateChoreographyPackageOutcome(check ChoreographyPackageOutcomeCheck) error {
	spec, outcome, succeeded, retry := check.Task, check.Outcome, check.Succeeded, check.Retry
	if outcome == choreographyGitAbsentState {
		return nil
	}
	if spec.Package == choreographyGitAbsentState || retry {
		return fmt.Errorf(choreographyGitPackageOutcomeIsNotAtTaskFinalityError)
	}
	switch outcome {
	case choreographyGitOutcomePublished:
		if !spec.Recording || !succeeded {
			return fmt.Errorf(choreographyGitPublishedOutcomeRequiresSuccessfulRecordingTaskError)
		}
	case choreographyGitOutcomeRecordBlocked:
		if !spec.Recording || succeeded {
			return fmt.Errorf(choreographyGitRecordBlockedOutcomeRequiresFailedRecordingTaskError)
		}
	case choreographyGitOutcomeFailed, choreographyGitOutcomeSkipped, choreographyGitOutcomeCancelled:
		if succeeded {
			return fmt.Errorf(choreographyGitDeadPackageOutcomeRequiresFailedTaskError)
		}
	default:
		return fmt.Errorf(choreographyGitInvalidPackageOutcomeNamedErrorFormat, outcome)
	}
	return nil
}

// PersistBlocked atomically advances a branch-bearing task's inert reservation
// to a signed blocked terminal and records the same finality on the ledger.
// Branchless local-authority tasks advance only the ledger.
func (g *GitChoreographyPersist) PersistBlocked(ctx context.Context,
	outcome ChoreographyBlockedOutcome) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyGitAbsentState, err
	}
	if outcome.ChoreographyRunBinding != g.opts.Binding || outcome.Task == choreographyGitAbsentState ||
		outcome.Attempt < choreographyGitFirstAttempt || outcome.Package == choreographyGitAbsentState || outcome.BlockedBy == choreographyGitAbsentState {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitInvalidBlockedTaskOutcomeError)
	}
	spec, found := g.tasks[outcome.Task]
	if !found {
		spec, found = g.localTasks[outcome.Task]
	}
	providerFailure := outcome.PackageOutcome == choreographyGitOutcomeFailed && outcome.BlockedBy != outcome.Package
	localFailure := (outcome.PackageOutcome == choreographyGitOutcomeFailed || outcome.PackageOutcome == choreographyGitAbsentState) &&
		outcome.BlockedBy == outcome.Package &&
		!spec.LocalAuthority && outcome.ExpectedOID == spec.ReservationOID &&
		validChoreographyLocalFailure(outcome.LocalFailure, spec.ReservationOID)
	if !found || spec.Package != outcome.Package ||
		(outcome.PackageOutcome != choreographyGitAbsentState && outcome.PackageOutcome != choreographyGitOutcomeSkipped &&
			outcome.PackageOutcome != choreographyGitOutcomeCancelled && !providerFailure && !localFailure) ||
		outcome.LocalFailure != nil && !localFailure {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitBlockedTaskDoesNotMatchPlannedPackageOutcomeError)
	}
	if outcome.ExpectedOID == choreographyGitAbsentState {
		return choreographyGitAbsentState, fmt.Errorf(choreographyGitBlockedTaskHasNoExactTaskRefPredecessorError)
	}
	if localFailure && outcome.PackageOutcome == choreographyGitAbsentState {
		if _, err := g.LedgerHead(ctx); err != nil {
			return choreographyGitAbsentState, err
		}
		g.mu.Lock()
		prior := g.packageState[outcome.Package]
		g.mu.Unlock()
		if prior != choreographyGitOutcomeFailed {
			return choreographyGitAbsentState, fmt.Errorf(choreographyGitLocalFailureForPackageNamedHasPriorSignedOutcomeNamedWantFailedErrorFormat, outcome.Package, prior)
		}
	}
	document, err := json.Marshal(outcome)
	if err != nil {
		return choreographyGitAbsentState, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyGitAbsentState {
		parents := []string(nil)
		if outcome.ExpectedOID != choreographyGitAbsentState {
			parents = []string{outcome.ExpectedOID}
		}
		oid, err = g.opts.Mailbox.commitMessage(ctx, choreographyBlockedKind, document, parents, nil)
		if err != nil {
			return choreographyGitAbsentState, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{Identity: identity,
		TaskBranch: spec.Branch, OldTaskOID: outcome.ExpectedOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyBlocked, Task: outcome.Task,
			Attempt: outcome.Attempt, PriorTaskOID: outcome.ExpectedOID,
			GraphRootOID: g.opts.GraphRootOID,
			Package:      outcome.Package, PackageOutcome: outcome.PackageOutcome,
			BlockedBy: outcome.BlockedBy, TaskOID: oid},
	}); err != nil {
		return choreographyGitAbsentState, err
	}
	return oid, nil
}

// LookupReceipt walks only one exact task ref, never all task refs. This is
// for ambiguous responses/restart reconciliation, not the normal poll path.
func (g *GitChoreographyPersist) LookupReceipt(ctx context.Context, q ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	if !strings.HasPrefix(q.Branch, reservationBranchPrefix) ||
		(q.Kind != choreographyClaimKind && q.Kind != choreographyResultKind && q.Kind != MessageAck) || q.ProposalDigest == choreographyGitAbsentState ||
		q.ChoreographyRunBinding != g.opts.Binding ||
		(q.Kind == choreographyClaimKind &&
			(q.ExpectedLedgerOID == choreographyGitAbsentState || q.GraphRootOID != g.opts.GraphRootOID)) {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitReceiptQueryNamesAnotherTaskRefError)
	}
	spec := g.tasks[q.Task]
	if spec.Task == choreographyGitAbsentState || spec.Branch != q.Branch || spec.ReservationOID == choreographyGitAbsentState {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitReceiptQueryHasNoCanonicalReservationError)
	}
	m := g.opts.Mailbox
	oid, err := m.readRemoteTip(ctx, q.Branch)
	if err != nil {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
	}
	if oid != choreographyGitAbsentState {
		if err := m.fetchRefs(ctx, []string{q.Branch}); err != nil {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
		}
	}
	foundRoot := choreographyGitCanonicalRootUnseen
	candidateOID := choreographyGitAbsentState
	expectedFound := choreographyGitExpectedTipUnseen
	var expectedKind MessageKind
	for steps := choreographyGitHistoryStartStep; oid != choreographyGitAbsentState && steps < spec.EffectiveMaxAttempts()*choreographyGitHistoryStepsPerAttempt+choreographyGitHistoryFixedSteps; steps++ {
		if err := ctx.Err(); err != nil {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
		}
		if oid == spec.ReservationOID {
			if oid == q.ExpectedOID {
				expectedFound, expectedKind = choreographyGitExpectedTipFound, MessageReservation
			}
			expected := Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
				Kind: spec.Kind, Run: q.Run, Generation: q.Generation,
				PlanDigest: q.PlanDigest, Task: q.Task, Attempt: choreographyGitFirstAttempt, Branch: q.Branch},
				BootstrapOID: g.opts.BootstrapOID, MaxAttempts: spec.EffectiveMaxAttempts()}
			if err := m.VerifyChoreographyReservation(ctx, oid, expected, returnReplyLimit); err != nil {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
			}
			foundRoot = choreographyGitCanonicalRootFound
			break
		}
		carried, err := m.readTree(ctx, oid)
		if err != nil {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
		}
		if oid == q.ExpectedOID {
			expectedFound, expectedKind = choreographyGitExpectedTipFound, carried.kind
		}
		if carried.kind == choreographyClaimKind || carried.kind == choreographyResultKind || carried.kind == MessageAck {
			document, err := m.Read(ctx, ChainTip{Kind: carried.kind, document: carried.document,
				signature: carried.signature}, returnReplyLimit)
			if err != nil {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
			}
			var identity struct {
				ChoreographyRunBinding
				Task           string `json:"task"`
				Attempt        int    `json:"attempt"`
				Node           string `json:"node"`
				Branch         string `json:"branch"`
				ProposalID     string `json:"proposalId"`
				ProposalDigest string `json:"proposalDigest"`
			}
			if err := json.Unmarshal(document, &identity); err != nil {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
			}
			if identity.ProposalID == q.ProposalID {
				storedDigest := identity.ProposalDigest
				bindingMatches := identity.ChoreographyRunBinding == q.ChoreographyRunBinding
				if carried.kind != MessageAck {
					sum := sha256.Sum256(document)
					storedDigest = hex.EncodeToString(sum[:])
				} else {
					// Ack embeds Header, which signs run, generation and plan,
					// but has no GraphDigest field. The query's complete binding
					// was checked against this retained graph above.
					bindingMatches = identity.Run == q.Run &&
						identity.Generation == q.Generation && identity.PlanDigest == q.PlanDigest
				}
				if carried.kind != q.Kind {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitProposalIdReusedForAnotherMessageKindError)
				}
				if !bindingMatches || identity.Task != q.Task ||
					identity.Attempt != q.Attempt || identity.Node != q.Node || identity.Branch != q.Branch ||
					storedDigest != q.ProposalDigest {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitProposalIdReusedOnTaskRefError)
				}
				candidateOID = oid
			}
		} else if carried.kind != MessageReservation && carried.kind != choreographyDecisionKind &&
			carried.kind != MessageAssignment && carried.kind != MessageCancel && carried.kind != MessageResult {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitTaskRefCarriesUnexpectedStateNamedErrorFormat, carried.kind)
		}
		parent, err := m.remote.ResolveCommit(ctx, oid+choreographyGitParentSuffix)
		if err != nil {
			break
		}
		oid = parent
	}
	if oid != choreographyGitAbsentState && !foundRoot {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitTaskReceiptChainMissesCanonicalReservationError)
	}
	if oid == choreographyGitAbsentState && candidateOID != choreographyGitAbsentState && !foundRoot {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitProposalCommitMissesCanonicalReservationError)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
	}
	if accepted, found := g.accepted[q.ProposalDigest]; found {
		event := g.events[accepted.Sequence-choreographyGitLedgerSequenceOffset]
		receiptOID := event.TaskOID
		if (q.Kind == choreographyResultKind || q.Kind == MessageAck) &&
			event.ResultReceiptOID != choreographyGitAbsentState {
			receiptOID = event.ResultReceiptOID
		}
		kindMatches := q.Kind == choreographyClaimKind && event.Kind == ChoreographyClaimAccepted ||
			q.Kind == choreographyResultKind && event.Kind == ChoreographyResultAccepted ||
			q.Kind == MessageAck && event.Kind == ChoreographyAckAccepted
		if !kindMatches || candidateOID != choreographyGitAbsentState && candidateOID != receiptOID || event.Task != q.Task ||
			event.Attempt != q.Attempt || event.Node != q.Node ||
			event.ProposalID != q.ProposalID ||
			q.Branch != FormatChoreographyTaskBranch(q.Run, q.Task) {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitAcceptedProposalIdentityDiffersError)
		}
		return ChoreographyReceipt{ChoreographyRunBinding: q.ChoreographyRunBinding,
			Kind: q.Kind, Task: q.Task, Attempt: q.Attempt,
			Node: q.Node, Branch: q.Branch, ProposalID: q.ProposalID,
			ProposalDigest: q.ProposalDigest, OID: receiptOID,
			GraphRootOID: event.GraphRootOID, AssignmentOID: event.AssignmentOID,
			PickupID: event.PickupID,
			Accepted: choreographyGitReceiptAccepted, Cursor: accepted}, choreographyGitReceiptAccepted, nil
	}
	for _, index := range g.taskEvents[q.Task] {
		event := g.events[index]
		if event.ProposalID == q.ProposalID &&
			((q.Kind == choreographyResultKind && event.Kind == ChoreographyResultAccepted) ||
				(q.Kind == choreographyClaimKind && event.Kind == ChoreographyClaimAccepted) ||
				(q.Kind == MessageAck && event.Kind == ChoreographyAckAccepted)) &&
			event.ProposalDigest != q.ProposalDigest {
			return ChoreographyReceipt{}, choreographyGitReceiptMissing,
				fmt.Errorf(choreographyGitProposalIdReusedOnTaskRefError)
		}
	}
	if candidateOID != choreographyGitAbsentState {
		return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitTaskProposalHasNoSignedLedgerAcceptanceError)
	}
	if q.Kind == choreographyClaimKind {
		if fence, found := g.refused[q.ProposalDigest]; found {
			event := g.events[fence.Sequence-choreographyGitLedgerSequenceOffset]
			if event.Kind != ChoreographyClaimRefused || event.Task != q.Task ||
				event.Attempt != q.Attempt || event.Node != q.Node ||
				event.ProposalID != q.ProposalID ||
				q.Branch != FormatChoreographyTaskBranch(q.Run, q.Task) {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitRefusedProposalIdentityDiffersError)
			}
			return ChoreographyReceipt{ChoreographyRunBinding: q.ChoreographyRunBinding,
				Kind: choreographyClaimKind, Task: q.Task, Attempt: q.Attempt,
				Node: q.Node, Branch: q.Branch, ProposalID: q.ProposalID,
				ProposalDigest: q.ProposalDigest, OID: event.TaskOID,
				GraphRootOID: q.GraphRootOID,
				Refused:      choreographyGitClaimRefused, FenceOID: fence.OID, FenceSequence: fence.Sequence,
				RefusalReason: event.RefusalReason,
				CapacityScope: event.CapacityScope, Budget: event.Budget}, choreographyGitReceiptAccepted, nil
		}
		if prior, found := g.eventIndex[q.ExpectedLedgerOID]; found && prior < g.sequence {
			if !expectedFound || expectedKind != MessageReservation &&
				expectedKind != MessageAssignment && expectedKind != choreographyDecisionKind {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitStaleClaimQueryNamesNoAuthenticatedExpectedTaskStateError)
			}
			if expectedKind == MessageReservation && q.Attempt != choreographyGitFirstAttempt {
				return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitStaleClaimAttemptCountNamesAttemptOneReservationErrorFormat, q.Attempt)
			}
			if expectedKind == MessageAssignment {
				view, err := m.OpenCarriedAssignment(ctx, q.ExpectedOID, returnReplyLimit)
				if err != nil {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitStaleClaimQueryNamesAnotherSignedAssignmentCauseErrorFormat, err)
				}
				if view == nil || view.Assignment.Run != q.Run ||
					view.Assignment.Generation != q.Generation || view.Assignment.PlanDigest != q.PlanDigest ||
					view.Assignment.GraphDigest != q.GraphDigest ||
					view.Assignment.BootstrapOID != g.opts.BootstrapOID ||
					view.Assignment.Task != q.Task || view.Assignment.Attempt != q.Attempt ||
					view.Assignment.Node != q.Node || view.Assignment.Branch != q.Branch {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyStaleAssignmentMismatchError)
				}
			}
			if expectedKind == choreographyDecisionKind {
				decisionTree, err := m.readTree(ctx, q.ExpectedOID)
				if err != nil {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
				}
				decisionBytes, err := m.Read(ctx, ChainTip{OID: q.ExpectedOID,
					Kind: decisionTree.kind, document: decisionTree.document,
					signature: decisionTree.signature}, returnReplyLimit)
				if err != nil {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, err
				}
				var decision ChoreographyDecision
				if strictJSON(decisionBytes, &decision) != nil ||
					decision.ChoreographyRunBinding != q.ChoreographyRunBinding ||
					decision.Task != q.Task || decision.Branch != q.Branch ||
					!decision.Retry || decision.Attempt+choreographyGitFirstAttempt != q.Attempt {
					return ChoreographyReceipt{}, choreographyGitReceiptMissing, fmt.Errorf(choreographyGitStaleClaimNamesAnotherSignedRetryDecisionError)
				}
			}
			reason := ChoreographyRefusalStale
			if g.sealed {
				reason = ChoreographyRefusalSealed
			}
			return ChoreographyReceipt{ChoreographyRunBinding: q.ChoreographyRunBinding,
				Kind: choreographyClaimKind, Task: q.Task, Attempt: q.Attempt,
				Node: q.Node, Branch: q.Branch, ProposalID: q.ProposalID,
				ProposalDigest: q.ProposalDigest, OID: q.ExpectedOID,
				GraphRootOID: q.GraphRootOID,
				Refused:      choreographyGitClaimRefused, FenceOID: g.ledgerTip, FenceSequence: g.sequence,
				RefusalReason: reason}, choreographyGitReceiptAccepted, nil
		}
	}
	return ChoreographyReceipt{}, choreographyGitReceiptMissing, nil
}

func (g *GitChoreographyPersist) advance(ctx context.Context, branch, oldOID, newOID string) error {
	m := g.opts.Mailbox
	if !strings.HasPrefix(branch, reservationBranchPrefix) {
		return fmt.Errorf(choreographyGitInvalidChoreographyTaskBranchError)
	}
	if err := m.remote.PushAdvance(ctx, m.endpoint, newOID, branch, oldOID); err != nil {
		if m.resolveOwnPush(ctx, branch, newOID, err) != pushLanded {
			return fmt.Errorf(choreographyGitTaskRefCasDidNotSettleCauseErrorFormat, err)
		}
	}
	// A successful push response can still race another writer; exact remote
	// readback is the receipt's durable evidence.
	tip, err := m.readRemoteTip(ctx, branch)
	if err != nil || tip != newOID {
		return fmt.Errorf(choreographyGitTaskRefCasOutcomeIsNotExactError)
	}
	if g.opts.OnRefAdvance != nil {
		g.opts.OnRefAdvance(ctx, ChoreographyRefAdvance{Branch: branch, OldOID: oldOID, NewOID: newOID})
	}
	return nil
}
