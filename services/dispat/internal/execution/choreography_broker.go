// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The first node persists a choreographer's already selected task; it does
// not choose tasks for choreographers. The in-memory graph is an index over
// immutable pinned inputs. Each accepted terminal event touches its outgoing
// edges once, so readiness work is O(V+E), excluding transport and Git.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyBrokerZeroCancelResponseDuration                                 time.Duration = 0
	choreographyFailurePartPrelude                                                             = "prelude"
	choreographyFailurePartInputs                                                              = "inputs"
	choreographyFailurePartFrame                                                               = "frame"
	choreographyFailurePartSnapshot                                                            = "snapshot"
	choreographyFailurePartPlacement                                                           = "placement"
	choreographyFailureDigestHexSize                                                           = sha256.Size * 2
	choreographyOfferBlockedPackageError                                                       = "execution: compiled offer names blocked package"
	choreographyPreparedIdentityMismatchError                                                  = "execution: claim prepared input differs from signed ready-frame identity"
	choreographyResolvedPreparedMismatchFormat                                                 = "execution: task %q prepared input differs from first-node state: proposed %q, resolved %q"
	choreographyResolvedPredecessorMismatchFormat                                              = "execution: task %q predecessor result identities differ from first-node state: proposed %q, resolved %q"
	choreographyNoAcceptedResultOutputOID                                                      = ""
	choreographyBrokerAbsentState                                                              = ""
	choreographyBrokerNoTasks                                                                  = 0
	choreographyBrokerNoTaskWeight                                                             = 0
	choreographyBrokerFirstEligibleNodeIndex                                                   = 0
	choreographyBrokerNoEligibleNodes                                                          = 0
	choreographyBrokerNoPredecessors                                                           = 0
	choreographyBrokerNoQueueEntries                                                           = 0
	choreographyBrokerFirstQueueIndex                                                          = 0
	choreographyBrokerNextQueueIndex                                                           = 1
	choreographyBrokerNoGraphVisits                                                            = 0
	choreographyBrokerNoBindings                                                               = 0
	choreographyBrokerFirstAttempt                                                             = 1
	choreographyBrokerOneEligibleNode                                                          = 1
	choreographyBrokerMinimumCapacitySlots                                                     = 1
	choreographyBrokerMinimumTaskWeight                                                        = 1
	choreographyBrokerNoClaimableEdges                                                         = 0
	choreographyBrokerLocalFailureValid                                                        = true
	choreographyBrokerLocalFailureInvalid                                                      = false
	choreographyBrokerTaskTerminal                                                             = true
	choreographyBrokerTaskSucceeded                                                            = true
	choreographyBrokerTaskFrameReady                                                           = true
	choreographyBrokerTaskFrameUnready                                                         = false
	choreographyBrokerRunStopping                                                              = true
	choreographyBrokerSignedRefusal                                                            = true
	choreographyBrokerNodeEligible                                                             = true
	choreographyBrokerNodeIneligible                                                           = false
	choreographyBrokerReceiptNotFound                                                          = false
	choreographyBrokerPackageDead                                                              = true
	choreographyBrokerPackageSettlementPending                                                 = true
	choreographyBrokerPackageLive                                                              = false
	choreographyBrokerOutcomeFailed                                                            = "failed"
	choreographyBrokerOutcomeSkipped                                                           = "skipped"
	choreographyBrokerOutcomeCancelled                                                         = "cancelled"
	choreographyBrokerOutcomeRecordBlocked                                                     = "record-blocked"
	choreographyBrokerClaimWonTheQueuedAssignmentError                                         = "execution: claim won the queued assignment"
	choreographyBrokerFatalChoreographyInputBindingError                                       = "execution: fatal choreography input binding"
	choreographyBrokerResultWonTheCancellationRaceError                                        = "execution: result won the cancellation race"
	choreographyBrokerTaskAlreadyFinalBeforeCancellationError                                  = "execution: logical task finality won the cancellation race"
	choreographyBrokerCompiledOfferHasNoAssignmentError                                        = "execution: compiled offer has no assignment"
	choreographyBrokerCompiledOfferDiffersFromReadyTaskError                                   = "execution: compiled offer differs from ready task"
	choreographyBrokerCompiledTaskIsBlockedByPreparedProviderNamedErrorFormat                  = "execution: compiled task is blocked by prepared provider %s"
	choreographyBrokerCompiledOfferNamesBlockedPackageCauseErrorFormat                         = "execution: compiled offer names blocked package: %w"
	choreographyBrokerCompiledSlotDoesNotMatchSelectedNodeError                                = "execution: compiled slot does not match selected node"
	choreographyBrokerCompiledAssignmentHasNoDurableReceiptError                               = "execution: compiled assignment has no durable receipt"
	choreographyBrokerCancellationDoesNotNameAnActiveClaimError                                = "execution: cancellation does not name an active claim"
	choreographyBrokerCancellationHasNoDurableTaskRefError                                     = "execution: cancellation has no durable task ref"
	choreographyBrokerQueueFenceDoesNotNameAnUnclaimedOfferError                               = "execution: queue fence does not name an unclaimed offer"
	choreographyBrokerQueueFenceHasNoDurableTaskRefError                                       = "execution: queue fence has no durable task ref"
	choreographyBrokerQueueDecisionDoesNotNameAnUnclaimedOfferError                            = "execution: queue decision does not name an unclaimed offer"
	choreographyBrokerQueueDecisionHasNoDurableTaskRefError                                    = "execution: queue decision has no durable task ref"
	choreographyBrokerIncompleteFirstNodeBrokerConfigurationError                              = "execution: incomplete first-node broker configuration"
	choreographyBrokerInvalidStageBudgetError                                                  = "execution: invalid stage budget"
	choreographyBrokerInvalidNodeCapacityError                                                 = "execution: invalid node capacity"
	choreographyBrokerAdmittedNodeNamedHasInconsistentSignedCapacityErrorFormat                = "execution: admitted node %q has inconsistent signed capacity"
	choreographyBrokerDuplicateCompiledTemplateNamedErrorFormat                                = "execution: duplicate compiled template %q"
	choreographyBrokerPackageTaskNamedHasNoSharedOutcomePolicyErrorFormat                      = "execution: package task %q has no shared outcome policy"
	choreographyBrokerInvalidIndexedTaskNamedErrorFormat                                       = "execution: invalid indexed task %q"
	choreographyBrokerDuplicateIndexedTaskNamedErrorFormat                                     = "execution: duplicate indexed task %q"
	choreographyBrokerTaskNamedCannotFitNodeNamedErrorFormat                                   = "execution: task %q cannot fit node %q"
	choreographyBrokerInvalidPredecessorNamedOfNamedErrorFormat                                = "execution: invalid predecessor %q of %q"
	choreographyBrokerIndexedTaskGraphHasACycleError                                           = "execution: indexed task graph has a cycle"
	choreographyBrokerClaimDiffersFromPinnedRunOrCompleteGraphRootError                        = "execution: claim differs from pinned run or complete graph root"
	choreographyBrokerClaimLeaseIdentityDiffersFromTheExactTaskAttemptError                    = "execution: claim lease identity differs from the exact task attempt"
	choreographyBrokerDurableClaimReceiptHasAnotherExactProposalIdentityError                  = "execution: durable claim receipt has another exact proposal identity"
	choreographyBrokerClaimIsOutsideTheSignedRunGraphOrAdmittedNodeError                       = "execution: claim is outside the signed run, graph or admitted node"
	choreographyBrokerClaimBelongsToAnotherPinnedRunError                                      = "execution: claim belongs to another pinned run"
	choreographyBrokerClaimNamesAnotherCompleteCompiledGraphRootError                          = "execution: claim names another complete compiled graph root"
	choreographyBrokerResolvedInputsDifferFromSignedPredecessorFinalityError                   = "execution: resolved inputs differ from signed predecessor finality"
	choreographyBrokerResolvedInputDigestIsEmptyError                                          = "execution: resolved input digest is empty"
	choreographyBrokerClaimDoesNotMatchTheIndexedTaskError                                     = "execution: claim does not match the indexed task"
	choreographyBrokerClaimSuppliesUnverifiedCompiledTemplateOrFilledInputsError               = "execution: claim supplies unverified compiled template or filled inputs"
	choreographyBrokerClaimSExactInputIdentityDigestDiffersError                               = "execution: claim's exact input identity digest differs"
	choreographyBrokerClaimOmitsSignedPredecessorIdentitiesError                               = "execution: claim omits signed predecessor identities"
	choreographyBrokerClaimPredecessorNamedIsNotLogicallyFinalErrorFormat                      = "execution: claim predecessor %q is not logically final"
	choreographyBrokerClaimPredecessorNamedNamesAnotherSignedResultErrorFormat                 = "execution: claim predecessor %q names another signed result"
	choreographyBrokerFatalBindingCauseFormat                                                  = "%w: %v"
	choreographyBrokerPersistenceReturnedAMismatchedClaimError                                 = "execution: persistence returned a mismatched claim"
	choreographyBrokerNodeNamedHasNoCurrentAdmittedAuthorityErrorFormat                        = "execution: node %q has no current admitted authority"
	choreographyBrokerJoinDiffersFromPinnedCompleteGraphError                                  = "execution: join differs from pinned complete graph"
	choreographyBrokerJoiningNodeIsOutsideLateAdmissionPolicyError                             = "execution: joining node is outside late admission policy"
	choreographyBrokerSettledJoinReceiptHasAnotherCapacityAuthorityError                       = "execution: settled join receipt has another capacity authority"
	choreographyBrokerJoinReceiptQueryDiffersFromBrokerIdentityError                           = "execution: join receipt query differs from broker identity"
	choreographyBrokerInputPredecessorIsNotFinalError                                          = "execution: input predecessor is not final"
	choreographyBrokerTaskInputIdentityHasNoBrokerError                                        = "execution: task input identity has no broker"
	choreographyBrokerTaskNamedIsNotReadyForExactInputIdentityErrorFormat                      = "execution: task %q is not ready for exact input identity"
	choreographyBrokerReadyFrameHasNoExactInputIdentityError                                   = "execution: ready frame has no exact input identity"
	choreographyBrokerTaskNamedCannotPublishAReadyFrameErrorFormat                             = "execution: task %q cannot publish a ready frame"
	choreographyBrokerReadyFrameDiffersFromSignedPredecessorInputsError                        = "execution: ready frame differs from signed predecessor inputs"
	choreographyBrokerResultPayloadIsMissingError                                              = "execution: result payload is missing"
	choreographyBrokerCompiledPickupIdentityRequiredError                                      = "execution: compiled executable claim requires an exact pickup identity"
	choreographyBrokerResultBelongsToAnotherPinnedRunError                                     = "execution: result belongs to another pinned run"
	choreographyBrokerProposalIdReusedForAnotherResultError                                    = "execution: proposal ID reused for another result"
	choreographyBrokerPersistedResultRequiresRunStateReconciliationError                       = "execution: persisted result requires run-state reconciliation"
	choreographyBrokerResultDoesNotAnswerTheActiveClaimError                                   = "execution: result does not answer the active claim"
	choreographyBrokerPersistenceReturnedAMismatchedResultError                                = "execution: persistence returned a mismatched result"
	choreographyBrokerInvalidCancellationAcknowledgementError                                  = "execution: invalid cancellation acknowledgement"
	choreographyBrokerReusedAcknowledgementIdentityError                                       = "execution: reused acknowledgement identity"
	choreographyBrokerAcknowledgementHasNoActiveAttemptError                                   = "execution: acknowledgement has no active attempt"
	choreographyBrokerPersistedAcknowledgementDiffersError                                     = "execution: persisted acknowledgement differs"
	choreographyBrokerInvalidAttemptDecisionError                                              = "execution: invalid attempt decision"
	choreographyBrokerPersistenceReturnedNoDecisionCommitError                                 = "execution: persistence returned no decision commit"
	choreographyBrokerLocalAuthorityOutcomeIsNotAdmissibleError                                = "execution: local authority outcome is not admissible"
	choreographyBrokerPackageOutcomeHasNoSharedEvaluatorError                                  = "execution: package outcome has no shared evaluator"
	choreographyBrokerBlockedTaskIsNotAdmissibleError                                          = "execution: blocked task is not admissible"
	choreographyBrokerLocalFailureHasNoInertDelegatedTaskError                                 = "execution: local failure has no inert delegated task"
	choreographyBrokerInvalidLocalFailureEvidenceError                                         = "execution: invalid local failure evidence"
	choreographyBrokerPreparedTaskNamesAnotherFailureCauseError                                = "execution: prepared task names another failure cause"
	choreographyBrokerBlockedTaskNamesAnotherSettledPackageError                               = "execution: blocked task names another settled package"
	choreographyBrokerBlockedTaskNamesAnotherPreparedProviderError                             = "execution: blocked task names another prepared provider"
	choreographyBrokerBlockedTaskHasNoSharedOutcomeEvaluatorError                              = "execution: blocked task has no shared outcome evaluator"
	choreographyBrokerBlockedTaskDiffersFromSharedSkipPolicyError                              = "execution: blocked task differs from shared skip policy"
	choreographyBrokerReceiptQueryBelongsToAnotherRunError                                     = "execution: receipt query belongs to another run"
	choreographyBrokerReceiptQueryNamesAnotherTaskRefError                                     = "execution: receipt query names another task ref"
)

var errChoreographyClaimWon = errors.New(choreographyBrokerClaimWonTheQueuedAssignmentError)

// ErrChoreographyBindingFatal means trusted effective-input resolution failed
// after exact graph readiness. The broker signs RunStopping before returning it.
var ErrChoreographyBindingFatal = errors.New(choreographyBrokerFatalChoreographyInputBindingError)

// ErrChoreographyResultWon asks a recovering first node to reread the signed
// ledger: a Result was accepted before its exact Cancel task CAS could land.
var ErrChoreographyResultWon = errors.New(choreographyBrokerResultWonTheCancellationRaceError)

// ErrChoreographyTaskAlreadyFinal asks a lagging observer to consume the
// signed terminal event rather than cancel an obsolete pickup identity.
var ErrChoreographyTaskAlreadyFinal = errors.New(choreographyBrokerTaskAlreadyFinalBeforeCancellationError)

// ChoreographyTaskSpec is one task independently reconstructed by every
// participant. The first node also binds it to the precreated Git task ref.
type ChoreographyTaskSpec struct {
	Task                       string
	Package                    string
	Kind                       string
	Stage                      string
	Recording                  bool
	Branch                     string
	ReservationOID             string
	InputsDigest               string
	Virtual                    bool
	LocalAuthority             bool
	ExecutorOwned              bool
	RetryAfterUncertainAttempt bool `json:"retryAfterUncertainAttempt,omitempty"`
	NeedsPreparedInput         bool
	Budget                     string
	Weight                     int
	Placement                  Placement
	Platforms                  []string
	TemplateDigest             string
	Predecessors               []string
	PreparedProviders          []string
	EligibleNodes              []string
}

// ChoreographyPersistable is the sole Git-writing capability. Only the first
// node constructs one; a non-first choreographer receives a return client.
// PersistClaim and PersistResult settle unknown push outcomes by exact ref
// reads and return a receipt only after the corresponding commit is durable.
// PersistDecision records retry or finality on that same task ref before the
// indexed graph or capacity ledger changes.
type ChoreographyPersistable interface {
	PersistAssignment(context.Context, ChoreographyAssignmentOffer) (ChoreographyAssignmentReceipt, error)
	PersistCancel(context.Context, ChoreographyCancellation) (ChoreographyCancellationReceipt, error)
	PersistQueueFence(context.Context, ChoreographyQueueFence) (string, error)
	PersistUnclaimedDecision(context.Context, ChoreographyDecision) (string, error)
	PersistClaim(context.Context, ChoreographyClaimProposal) (ChoreographyReceipt, error)
	PersistRefusal(context.Context, ChoreographyClaimProposal, string) (ChoreographyReceipt, error)
	PersistResult(context.Context, ChoreographyResultProposal, *os.File) (ChoreographyReceipt, error)
	PersistAck(context.Context, ChoreographyAckProposal) (ChoreographyReceipt, error)
	PersistDecision(context.Context, ChoreographyDecision) (string, error)
	PersistLocalOutcome(context.Context, ChoreographyLocalOutcome) (string, error)
	PersistBlocked(context.Context, ChoreographyBlockedOutcome) (string, error)
	PersistFrameReady(context.Context, ChoreographyFrameReadyRequest) error
	PersistJoin(context.Context, ChoreographyJoinProposal) (ChoreographyJoinReceipt, error)
	LookupJoinReceipt(context.Context, ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error)
	LookupReceipt(context.Context, ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error)
	ReadEvents(context.Context, ChoreographyEventCursor, int) (ChoreographyEventPage, error)
	LedgerHead(context.Context) (ChoreographyEventCursor, error)
	StopClaims(context.Context) (ChoreographyEventCursor, error)
}

// ChoreographyAssignmentOffer is the first node's compiled placement. Its
// optional slot identifies a precreated peer index; first-local work needs
// no remote notice but still advances the task and ledger atomically.
type ChoreographyAssignmentOffer struct {
	Assignment      *Assignment
	ExpectedTaskOID string
	SlotBranch      string
	OldSlotOID      string
	SlotSequence    uint64
}

type ChoreographyAssignmentReceipt struct {
	AssignmentOID string
	SlotOID       string
	Cursor        ChoreographyEventCursor
}

// ChoreographyDecision is made by the first node's release/task policy after
// an attempt result has been checked. Retry keeps the logical task live;
// terminal success admits it to successors. Failure never releases a
// successor by accident.
type ChoreographyDecision struct {
	ChoreographyRunBinding
	Task       string
	Branch     string
	Attempt    int
	Node       string
	ResultOID  string
	OfferedOID string
	Retry      bool
	// Unclaimed settles an offered assignment that lost the queue deadline
	// before any Claim. A retry names the offered Assignment in ResultOID;
	// a final exhausted attempt names its pre-hook Cancel fence there and
	// identifies that original Assignment in OfferedOID.
	Unclaimed      bool
	Succeeded      bool
	PackageOutcome string
}

// ChoreographyQueueFence revokes an exhausted unclaimed offer before a
// package failure hook runs, without discharging graph successors yet.
type ChoreographyQueueFence struct {
	ChoreographyRunBinding
	Task          string
	Branch        string
	Attempt       int
	Node          string
	AssignmentOID string
}

// ChoreographyCancellation is the first node's exact revocation of an
// accepted compiled attempt. The task ref must still name ClaimOID; a Result
// that wins that CAS is handled as a result, never as an acknowledged stop.
type ChoreographyCancellation struct {
	ChoreographyRunBinding
	Task                   string
	Branch                 string
	Attempt                int
	Node                   string
	AssignmentOID          string
	ClaimOID               string
	CancelReplyDeadlineUTC string
}

// ChoreographyCancellationReceipt repeats the deadline in the exact signed
// Cancel. An unresolved push retry must reuse that original deadline.
type ChoreographyCancellationReceipt struct {
	OID                    string
	CancelReplyDeadlineUTC string
}

// ChoreographyLocalOutcome is asserted only by the first node after its
// native publish/record path has settled. The effect digest binds exact
// publication and record evidence outside the coordination repository.
type ChoreographyLocalOutcome struct {
	ChoreographyRunBinding
	Task           string                          `json:"task"`
	Node           string                          `json:"node"`
	ClaimOID       string                          `json:"claimOid"`
	EffectDigest   string                          `json:"effectDigest"`
	EffectEvidence ChoreographyLocalEffectEvidence `json:"effectEvidence"`
	Succeeded      bool                            `json:"succeeded"`
	PackageOutcome string                          `json:"packageOutcome,omitempty"`
}

// ChoreographyBlockedOutcome is emitted after the shared release outcome
// evaluator decides that an unclaimed task must be skipped. It carries the
// exact settled package outcome only when that package has reached finality.
type ChoreographyBlockedOutcome struct {
	ChoreographyRunBinding
	Task           string                            `json:"task"`
	Attempt        int                               `json:"attempt"`
	ExpectedOID    string                            `json:"expectedOid,omitempty"`
	Package        string                            `json:"package"`
	BlockedBy      string                            `json:"blockedBy"`
	PackageOutcome string                            `json:"packageOutcome,omitempty"`
	LocalFailure   *ChoreographyLocalFailureEvidence `json:"localFailure,omitempty"`
}

// ChoreographyLocalFailureEvidence is the first node's signed attestation of
// an actual pre-claim error after its local outcome hooks have settled. It
// never contains the error text, paths, or credentials.
type ChoreographyLocalFailureEvidence struct {
	Part           string `json:"part"`
	ErrorSHA256    string `json:"errorSha256"`
	ReservationOID string `json:"reservationOid"`
}

func validChoreographyLocalFailure(e *ChoreographyLocalFailureEvidence, reservationOID string) bool {
	if e == nil || e.ReservationOID == choreographyBrokerAbsentState || e.ReservationOID != reservationOID ||
		!validSlotHex(e.ErrorSHA256, choreographyFailureDigestHexSize) {
		return choreographyBrokerLocalFailureInvalid
	}
	switch e.Part {
	case choreographyFailurePartPrelude, choreographyFailurePartInputs,
		choreographyFailurePartFrame, choreographyFailurePartSnapshot,
		choreographyFailurePartPlacement:
		return choreographyBrokerLocalFailureValid
	default:
		return choreographyBrokerLocalFailureInvalid
	}
}

// FirstNodeBrokerOptions supplies the independently compiled graph and
// capacities. Capacity is a weighted slot budget, not a task-count limit.
type FirstNodeBrokerOptions struct {
	Binding                   ChoreographyRunBinding
	GraphRootOID              string
	InitialMembershipEpochOID string
	JoinPolicy                ChoreographyJoinPolicy
	FirstNode                 string
	Tasks                     []ChoreographyTaskSpec
	Templates                 []ChoreographyCompiledTemplate
	Members                   []ChoreographyRuntimeNode
	NodeCapacity              map[string]int
	Budgets                   map[string]int
	ResolveInputs             func(context.Context, string) (string, error)
	ResolveBindings           func(context.Context, string, string) (ChoreographyTaskBindings, error)
	OnClaimAccepted           func(ChoreographyTaskSpec, ChoreographyTaskBindings)
	OnFatalBinding            func(error)
	ValidateJoinReport        func(*NodeReport) error
	VerifyNodeJoin            func(context.Context, ChoreographyJoinProposal) error
	OutcomePolicy             ChoreographyOutcomePolicyx
	Persist                   ChoreographyPersistable
	CancelResponseDuration    time.Duration
}

type indexedChoreographyTask struct {
	spec                     ChoreographyTaskSpec
	remaining                int
	children                 []string
	attempt                  int
	tip                      string
	claim                    string
	cancel                   string
	queueFence               string
	queueOffered             string
	result                   string
	resultOutputOID          string
	offeredNode              string
	owner                    string
	inputsDigest             string
	preparedBlockedBy        string
	terminal                 bool
	succeeded                bool
	pendingNoClaim           bool
	pendingInertCancel       bool
	pendingPackageSettlement bool
	frameReady               bool
	preparedInputOID         string
	pickups                  map[string]*indexedChoreographyPickup
}

type indexedChoreographyPickup struct {
	id                     string
	attempt                int
	node                   string
	claimOID               string
	assignmentOID          string
	leaseIntentID          string
	resultReceiptOID       string
	resultOutputOID        string
	cancelOID              string
	cancelReplyDeadlineUTC string
	contactLost            bool
	settled                bool
	settlementDigest       string
	deadlineUTC            string
}

// choreographyPredecessorIdentity retains the accepted raw Result for a
// settled executable predecessor. Native and other no-Result terminals use
// their exact signed terminal task OID.
func choreographyPredecessorIdentity(resultOutputOID, terminalTaskOID string) string {
	if resultOutputOID != choreographyNoAcceptedResultOutputOID {
		return resultOutputOID
	}
	return terminalTaskOID
}

// OfferAssignment applies the compilation mode's chosen placement without
// charging capacity. The selected node must still return a signed Claim
// receipt before executing the frame.
func (b *FirstNodeBroker) OfferAssignment(ctx context.Context,
	offer ChoreographyAssignmentOffer) (ChoreographyAssignmentReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := offer.Assignment
	if a == nil {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledOfferHasNoAssignmentError)
	}
	task := b.tasks[a.Task]
	if task == nil || task.spec.Virtual || task.spec.LocalAuthority ||
		task.remaining != choreographyBrokerNoClaimableEdges || task.terminal || task.claim != choreographyBrokerAbsentState || task.offeredNode != choreographyBrokerAbsentState ||
		b.stopping || b.sealed || a.Run != b.binding.Run || a.Generation != b.binding.Generation ||
		a.PlanDigest != b.binding.PlanDigest || a.Protocol != ChoreographyProtocolVersion ||
		a.GraphDigest != b.binding.GraphDigest ||
		a.Attempt != task.attempt || a.Kind != task.spec.Kind || a.Stage != task.spec.Stage ||
		a.Branch != task.spec.Branch || !slices.Contains(task.spec.EligibleNodes, a.Node) ||
		a.Weight != task.spec.Weight ||
		offer.ExpectedTaskOID != task.tip {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledOfferDiffersFromReadyTaskError)
	}
	if task.preparedBlockedBy != choreographyBrokerAbsentState {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledTaskIsBlockedByPreparedProviderNamedErrorFormat, task.preparedBlockedBy)
	}
	if task.spec.Kind != string(KindPrepare) && task.spec.Package != choreographyBrokerAbsentState && b.outcomePolicy != nil {
		skip, _, err := b.outcomePolicy.ShouldSkip(task.spec.Package)
		if err != nil {
			return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledOfferNamesBlockedPackageCauseErrorFormat, err)
		}
		if skip || b.packageDead(task.spec.Package) {
			return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyOfferBlockedPackageError)
		}
	}
	if b.firstNode != choreographyBrokerAbsentState && (a.Node == b.firstNode) != (offer.SlotBranch == choreographyBrokerAbsentState) {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledSlotDoesNotMatchSelectedNodeError)
	}
	receipt, err := b.persist.PersistAssignment(ctx, offer)
	if err != nil {
		return ChoreographyAssignmentReceipt{}, err
	}
	if receipt.AssignmentOID == choreographyBrokerAbsentState || offer.SlotBranch != choreographyBrokerAbsentState && receipt.SlotOID == choreographyBrokerAbsentState {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyBrokerCompiledAssignmentHasNoDurableReceiptError)
	}
	task.tip, task.offeredNode = receipt.AssignmentOID, a.Node
	task.inputsDigest = CompiledClaimInputDigest(receipt.AssignmentOID)
	return receipt, nil
}

// CancelClaim writes the exact signed Cancel before the first node treats a
// timed-out command as stopped. Capacity remains charged until Ack or Result
// is followed by a durable decision.
func (b *FirstNodeBroker) CancelClaim(ctx context.Context,
	c ChoreographyCancellation) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[c.Task]
	if c.ChoreographyRunBinding == b.binding && task != nil && task.terminal &&
		c.Branch == task.spec.Branch {
		return choreographyBrokerAbsentState, ErrChoreographyTaskAlreadyFinal
	}
	if task != nil && task.result != choreographyBrokerAbsentState && task.claim == c.ClaimOID &&
		task.spec.Branch == c.Branch && task.attempt == c.Attempt {
		return choreographyBrokerAbsentState, ErrChoreographyResultWon
	}
	if c.ChoreographyRunBinding != b.binding || task == nil || task.terminal ||
		c.Branch != task.spec.Branch || c.Attempt != task.attempt ||
		c.Node != task.owner || c.ClaimOID != task.claim ||
		c.AssignmentOID == choreographyBrokerAbsentState || task.result != choreographyBrokerAbsentState || task.claim == choreographyBrokerAbsentState {
		return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerCancellationDoesNotNameAnActiveClaimError)
	}
	if task.cancel != choreographyBrokerAbsentState {
		return task.cancel, nil
	}
	deadline := time.Now().UTC().Add(b.cancelResponseDuration)
	if deadline.Year() > choreographyPickupLastUTCYear {
		return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerInvalidCancellationAcknowledgementError)
	}
	c.CancelReplyDeadlineUTC = deadline.Format(time.RFC3339Nano)
	receipt, err := b.persist.PersistCancel(ctx, c)
	if err != nil {
		return choreographyBrokerAbsentState, err
	}
	if receipt.OID == choreographyBrokerAbsentState || receipt.CancelReplyDeadlineUTC == choreographyBrokerAbsentState {
		return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerCancellationHasNoDurableTaskRefError)
	}
	task.cancel, task.tip = receipt.OID, receipt.OID
	for _, pickup := range task.pickups {
		if pickup.claimOID == c.ClaimOID {
			pickup.cancelOID = receipt.OID
			pickup.cancelReplyDeadlineUTC = receipt.CancelReplyDeadlineUTC
			break
		}
	}
	return receipt.OID, nil
}

// FenceUnclaimedOffer closes the task-ref Claim CAS before failure hooks run.
// The task remains logically unsettled until DecideUnclaimedOffer writes the
// terminal event after those hooks and package outcome have settled.
func (b *FirstNodeBroker) FenceUnclaimedOffer(ctx context.Context,
	f ChoreographyQueueFence) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[f.Task]
	if task != nil && task.claim != choreographyBrokerAbsentState && task.attempt == f.Attempt &&
		task.spec.Branch == f.Branch {
		return choreographyBrokerAbsentState, errChoreographyClaimWon
	}
	if f.ChoreographyRunBinding != b.binding || task == nil || task.terminal ||
		task.claim != choreographyBrokerAbsentState || task.queueFence != choreographyBrokerAbsentState || task.offeredNode != f.Node ||
		task.attempt != f.Attempt || task.spec.Branch != f.Branch ||
		task.tip != f.AssignmentOID || f.AssignmentOID == choreographyBrokerAbsentState {
		return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerQueueFenceDoesNotNameAnUnclaimedOfferError)
	}
	oid, err := b.persist.PersistQueueFence(ctx, f)
	if err != nil {
		return choreographyBrokerAbsentState, err
	}
	if oid == choreographyBrokerAbsentState {
		return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerQueueFenceHasNoDurableTaskRefError)
	}
	task.queueFence, task.queueOffered, task.tip = oid, f.AssignmentOID, oid
	return oid, nil
}

// DecideUnclaimedOffer advances the one logical task ref after an exact
// queue timeout. No Claim won its CAS, so no node or budget capacity was ever
// charged. The final exhausted attempt is held for release outcome policy.
func (b *FirstNodeBroker) DecideUnclaimedOffer(ctx context.Context,
	d ChoreographyDecision) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[d.Task]
	if task != nil && task.claim != choreographyBrokerAbsentState && task.attempt == d.Attempt &&
		task.spec.Branch == d.Branch {
		return errChoreographyClaimWon
	}
	if d.ChoreographyRunBinding != b.binding || task == nil || task.terminal ||
		task.claim != choreographyBrokerAbsentState || task.offeredNode == choreographyBrokerAbsentState ||
		d.Branch != task.spec.Branch || d.Attempt != task.attempt ||
		d.Node != task.offeredNode || d.ResultOID != task.tip ||
		(d.Retry && (task.queueFence != choreographyBrokerAbsentState || d.OfferedOID != choreographyBrokerAbsentState) ||
			!d.Retry && (task.queueFence == choreographyBrokerAbsentState || d.ResultOID != task.queueFence ||
				d.OfferedOID != task.queueOffered)) ||
		!d.Unclaimed || d.Succeeded || d.Retry &&
		(d.PackageOutcome != choreographyBrokerAbsentState || task.attempt >= maxPlacementAttempts) {
		return fmt.Errorf(choreographyBrokerQueueDecisionDoesNotNameAnUnclaimedOfferError)
	}
	if err := b.validatePackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	oid, err := b.persist.PersistUnclaimedDecision(ctx, d)
	if err != nil {
		return err
	}
	if oid == choreographyBrokerAbsentState {
		return fmt.Errorf(choreographyBrokerQueueDecisionHasNoDurableTaskRefError)
	}
	task.tip, task.offeredNode, task.queueFence, task.queueOffered = oid, choreographyBrokerAbsentState, choreographyBrokerAbsentState, choreographyBrokerAbsentState
	if d.Retry {
		task.attempt++
		task.frameReady, task.preparedInputOID = choreographyBrokerTaskFrameUnready, choreographyBrokerAbsentState
		return nil
	}
	task.terminal = choreographyBrokerTaskTerminal
	if err := b.applyPackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	b.discharge(task)
	return nil
}

// FirstNodeBroker validates a proposal against an indexed graph and budgets,
// then asks its Git backend to persist before issuing a receipt. Its mutex
// serializes competing claims and decisions, not script execution.
type FirstNodeBroker struct {
	mu                     sync.Mutex
	nativeInputLane        sync.RWMutex
	binding                ChoreographyRunBinding
	graphRootOID           string
	joinPolicy             ChoreographyJoinPolicy
	templates              map[string]ChoreographyCompiledTemplate
	members                map[string]ChoreographyRuntimeNode
	memberEpoch            map[string]string
	draining               map[string]bool
	firstNode              string
	tasks                  map[string]*indexedChoreographyTask
	nodeCaps               map[string]int
	nodeUsed               map[string]int
	budgets                map[string]int
	used                   map[string]int
	budgetWake             chan struct{}
	resolveInputs          func(context.Context, string) (string, error)
	resolveBindings        func(context.Context, string, string) (ChoreographyTaskBindings, error)
	onClaimAccepted        func(ChoreographyTaskSpec, ChoreographyTaskBindings)
	onFatalBinding         func(error)
	validateJoinReport     func(*NodeReport) error
	verifyNodeJoin         func(context.Context, ChoreographyJoinProposal) error
	outcomePolicy          ChoreographyOutcomePolicyx
	packageState           map[string]string
	sealed                 bool
	stopping               bool
	signedStop             bool
	cancelResponseDuration time.Duration
	executionStarted       bool
	admissionDeadlineUTC   string
	invalidated            bool
	fault                  error
	persist                ChoreographyPersistable
}

func NewFirstNodeBroker(opts FirstNodeBrokerOptions) (*FirstNodeBroker, error) {
	if err := validateChoreographyRetryPolicy(opts.JoinPolicy, opts.Tasks); err != nil {
		return nil, err
	}
	if opts.CancelResponseDuration == choreographyBrokerZeroCancelResponseDuration {
		opts.CancelResponseDuration = taskReportTimeout
	}
	if opts.CancelResponseDuration < time.Second ||
		opts.CancelResponseDuration > time.Duration(maxChoreographyAdmissionSeconds)*time.Second ||
		opts.CancelResponseDuration%time.Second != choreographyBrokerZeroCancelResponseDuration {
		return nil, fmt.Errorf(choreographyBrokerIncompleteFirstNodeBrokerConfigurationError)
	}
	if !opts.Binding.valid() || opts.GraphRootOID == choreographyBrokerAbsentState || opts.InitialMembershipEpochOID == choreographyBrokerAbsentState ||
		opts.Persist == nil || len(opts.Tasks) == choreographyBrokerNoTasks {
		return nil, fmt.Errorf(choreographyBrokerIncompleteFirstNodeBrokerConfigurationError)
	}
	b := &FirstNodeBroker{binding: opts.Binding, graphRootOID: opts.GraphRootOID,
		cancelResponseDuration: opts.CancelResponseDuration,
		joinPolicy:             opts.JoinPolicy, templates: make(map[string]ChoreographyCompiledTemplate, len(opts.Templates)),
		members: make(map[string]ChoreographyRuntimeNode), memberEpoch: make(map[string]string),
		draining:  make(map[string]bool),
		firstNode: opts.FirstNode, tasks: make(map[string]*indexedChoreographyTask, len(opts.Tasks)),
		nodeCaps: make(map[string]int, len(opts.NodeCapacity)), nodeUsed: make(map[string]int),
		budgets: make(map[string]int, len(opts.Budgets)), used: make(map[string]int),
		resolveInputs: opts.ResolveInputs, resolveBindings: opts.ResolveBindings,
		onClaimAccepted:    opts.OnClaimAccepted,
		onFatalBinding:     opts.OnFatalBinding,
		validateJoinReport: opts.ValidateJoinReport,
		verifyNodeJoin:     opts.VerifyNodeJoin,
		outcomePolicy:      opts.OutcomePolicy,
		packageState:       make(map[string]string), persist: opts.Persist,
		budgetWake: make(chan struct{})}
	for name, capacity := range opts.Budgets {
		if name == choreographyBrokerAbsentState || capacity < choreographyBrokerMinimumCapacitySlots {
			return nil, fmt.Errorf(choreographyBrokerInvalidStageBudgetError)
		}
		b.budgets[name] = capacity
	}
	for node, capacity := range opts.NodeCapacity {
		if node == choreographyBrokerAbsentState || capacity < choreographyBrokerMinimumCapacitySlots {
			return nil, fmt.Errorf(choreographyBrokerInvalidNodeCapacityError)
		}
		b.nodeCaps[node] = capacity
	}
	for _, member := range opts.Members {
		if member.Name == choreographyBrokerAbsentState || member.Capacity < choreographyBrokerMinimumCapacitySlots || b.members[member.Name].Name != choreographyBrokerAbsentState ||
			member.Capacity != b.nodeCaps[member.Name] {
			return nil, fmt.Errorf(choreographyBrokerAdmittedNodeNamedHasInconsistentSignedCapacityErrorFormat, member.Name)
		}
		b.members[member.Name] = member
		b.memberEpoch[member.Name] = opts.InitialMembershipEpochOID
	}
	for _, template := range opts.Templates {
		if template.Task == choreographyBrokerAbsentState || b.templates[template.Task].Task != choreographyBrokerAbsentState {
			return nil, fmt.Errorf(choreographyBrokerDuplicateCompiledTemplateNamedErrorFormat, template.Task)
		}
		b.templates[template.Task] = template
	}
	for _, spec := range opts.Tasks {
		if spec.Package != choreographyBrokerAbsentState && opts.OutcomePolicy == nil {
			return nil, fmt.Errorf(choreographyBrokerPackageTaskNamedHasNoSharedOutcomePolicyErrorFormat, spec.Task)
		}
		if spec.Task == choreographyBrokerAbsentState || (spec.LocalAuthority && spec.Virtual) ||
			(spec.ExecutorOwned && (spec.Virtual || spec.Placement != PlacementOrchestrator)) ||
			(spec.Kind == KindStage && spec.Stage == choreographyBrokerAbsentState) ||
			(spec.Kind != KindStage && spec.Stage != choreographyBrokerAbsentState) ||
			(spec.Virtual && (spec.Branch != choreographyBrokerAbsentState || spec.ReservationOID != choreographyBrokerAbsentState ||
				spec.Budget != choreographyBrokerAbsentState || spec.Weight != choreographyBrokerNoTaskWeight || len(spec.EligibleNodes) != choreographyBrokerNoEligibleNodes)) ||
			(spec.LocalAuthority && (spec.Branch == choreographyBrokerAbsentState || spec.ReservationOID == choreographyBrokerAbsentState ||
				spec.Budget == choreographyBrokerAbsentState || spec.Weight < choreographyBrokerMinimumTaskWeight || len(spec.EligibleNodes) != choreographyBrokerOneEligibleNode ||
				spec.EligibleNodes[choreographyBrokerFirstEligibleNodeIndex] != opts.FirstNode)) ||
			(!spec.Virtual &&
				(!strings.HasPrefix(spec.Branch, reservationBranchPrefix) || spec.ReservationOID == choreographyBrokerAbsentState ||
					spec.Budget == choreographyBrokerAbsentState || spec.Weight < choreographyBrokerMinimumTaskWeight)) {
			return nil, fmt.Errorf(choreographyBrokerInvalidIndexedTaskNamedErrorFormat, spec.Task)
		}
		if _, exists := b.tasks[spec.Task]; exists {
			return nil, fmt.Errorf(choreographyBrokerDuplicateIndexedTaskNamedErrorFormat, spec.Task)
		}
		for _, node := range spec.EligibleNodes {
			if b.nodeCaps[node] < spec.Weight || b.budgets[spec.Budget] < spec.Weight {
				return nil, fmt.Errorf(choreographyBrokerTaskNamedCannotFitNodeNamedErrorFormat, spec.Task, node)
			}
		}
		b.tasks[spec.Task] = &indexedChoreographyTask{spec: spec, remaining: len(spec.Predecessors),
			attempt: choreographyBrokerFirstAttempt, tip: spec.ReservationOID, inputsDigest: spec.InputsDigest}
	}
	// Build reverse adjacency and reject cycles once, before a proposal can
	// consume capacity. The runtime updates only children of completed tasks.
	indegree := make(map[string]int, len(b.tasks))
	for name, task := range b.tasks {
		for _, predecessor := range task.spec.Predecessors {
			parent := b.tasks[predecessor]
			if parent == nil || predecessor == name {
				return nil, fmt.Errorf(choreographyBrokerInvalidPredecessorNamedOfNamedErrorFormat, predecessor, name)
			}
			// The shared scheduler can state the same dependency edge more
			// than once. Each edge contributes one indegree unit and must be
			// discharged once; a set here would diverge from that graph.
			parent.children = append(parent.children, name)
		}
		indegree[name] = task.remaining
	}
	queue := make([]string, choreographyBrokerNoQueueEntries, len(b.tasks))
	for name, count := range indegree {
		if count == choreographyBrokerNoPredecessors {
			queue = append(queue, name)
		}
	}
	visited := choreographyBrokerNoGraphVisits
	for len(queue) > choreographyBrokerNoQueueEntries {
		name := queue[choreographyBrokerFirstQueueIndex]
		queue = queue[choreographyBrokerNextQueueIndex:]
		visited++
		for _, child := range b.tasks[name].children {
			indegree[child]--
			if indegree[child] == choreographyBrokerNoPredecessors {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(b.tasks) {
		return nil, fmt.Errorf(choreographyBrokerIndexedTaskGraphHasACycleError)
	}
	for _, task := range b.tasks {
		if task.spec.Virtual && task.remaining == choreographyBrokerNoClaimableEdges {
			b.completeVirtual(task)
		}
	}
	if b.fault != nil {
		return nil, b.fault
	}
	return b, nil
}

func (b *FirstNodeBroker) completeVirtual(first *indexedChoreographyTask) {
	queue := []*indexedChoreographyTask{first}
	for len(queue) > choreographyBrokerNoQueueEntries {
		task := queue[choreographyBrokerFirstQueueIndex]
		queue = queue[choreographyBrokerNextQueueIndex:]
		if task.terminal {
			continue
		}
		if task.spec.Kind != string(KindPrepare) && task.spec.Package != choreographyBrokerAbsentState &&
			b.packageDead(task.spec.Package) {
			task.terminal = choreographyBrokerTaskTerminal
		} else if task.preparedBlockedBy != choreographyBrokerAbsentState {
			task.terminal = choreographyBrokerTaskTerminal
		} else if task.spec.Kind != string(KindPrepare) && task.spec.Package != choreographyBrokerAbsentState && b.outcomePolicy != nil {
			skip, _, err := b.outcomePolicy.ShouldSkip(task.spec.Package)
			if err != nil {
				b.fault = err
				return
			}
			if skip || b.packageDead(task.spec.Package) {
				task.terminal = choreographyBrokerTaskTerminal
				if b.packageState[task.spec.Package] == choreographyBrokerAbsentState {
					if err := b.applyPackageOutcome(task.spec.Package, choreographyBrokerOutcomeSkipped); err != nil {
						b.fault = err
						return
					}
				}
			} else {
				task.terminal, task.succeeded = choreographyBrokerTaskTerminal, choreographyBrokerTaskSucceeded
			}
		} else {
			task.terminal, task.succeeded = choreographyBrokerTaskTerminal, choreographyBrokerTaskSucceeded
		}
		for _, childName := range task.children {
			child := b.tasks[childName]
			if blocker := task.preparedBlockedBy; blocker != choreographyBrokerAbsentState &&
				choreographyPreparedProviderFor(child.spec, blocker) &&
				(child.preparedBlockedBy == choreographyBrokerAbsentState || blocker < child.preparedBlockedBy) {
				child.preparedBlockedBy = blocker
			}
			child.remaining--
			if child.spec.Virtual && child.remaining == choreographyBrokerNoClaimableEdges {
				queue = append(queue, child)
			}
		}
	}
}

// AcceptClaim is the same path for a remote choreographer and for first-node
// local work. The caller chooses a task; the broker only checks eligibility.
func (b *FirstNodeBroker) AcceptClaim(ctx context.Context, p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	if err := p.validate(); err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.ChoreographyRunBinding != b.binding || p.GraphRootOID != b.graphRootOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimDiffersFromPinnedRunOrCompleteGraphRootError)
	}
	if err := VerifyChoreographyClaimIdentity(b.joinPolicy, p); err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.LeaseIntentID != nodeSlotLeaseID(NodeSlotIntent{Run: p.Run, Task: p.Task, Attempt: p.Attempt, PickupID: p.PickupID}) {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimLeaseIdentityDiffersFromTheExactTaskAttemptError)
	}
	query, err := FormatClaimReceiptQuery(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	// Exact durable replay takes precedence over current membership, readiness
	// and capacity. A later drain or finality cannot undo an accepted attempt.
	if old, found, err := b.persist.LookupReceipt(ctx, query); err != nil {
		return ChoreographyReceipt{}, err
	} else if found {
		if old.Kind != choreographyClaimKind || old.ProposalDigest != query.ProposalDigest ||
			old.Task != p.Task || old.Attempt != p.Attempt || old.Node != p.Node ||
			old.Branch != p.Branch || old.GraphRootOID != p.GraphRootOID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerDurableClaimReceiptHasAnotherExactProposalIdentityError)
		}
		if old.Accepted {
			b.mu.Lock()
			task := b.tasks[p.Task]
			if task != nil && p.PickupID != choreographyBrokerAbsentState {
				if task.pickups == nil {
					task.pickups = make(map[string]*indexedChoreographyPickup)
				}
				if task.pickups[p.PickupID] == nil {
					task.pickups[p.PickupID] = &indexedChoreographyPickup{
						id: p.PickupID, attempt: p.Attempt, node: p.Node,
						claimOID: old.OID, assignmentOID: old.AssignmentOID,
						leaseIntentID: p.LeaseIntentID, deadlineUTC: old.PickupDeadlineUTC}
				}
			}
			if task != nil && task.claim == choreographyBrokerAbsentState && task.tip == p.ExpectedOID &&
				task.attempt == p.Attempt && task.remaining == choreographyBrokerNoClaimableEdges && !task.terminal {
				task.claim, task.tip, task.owner = old.OID, old.OID, p.Node
				b.used[task.spec.Budget] += task.spec.Weight
				b.nodeUsed[p.Node] += task.spec.Weight
			}
			b.mu.Unlock()
		}
		return old, nil
	}
	b.nativeInputLane.RLock()
	defer b.nativeInputLane.RUnlock()
	// Dynamic environment and source bindings can require Git/object reads.
	// Resolve them outside the broker mutex, then recheck the exact task and
	// ledger epoch under that mutex before the remote CAS.
	b.mu.Lock()
	precheckTask := b.tasks[p.Task]
	if p.ChoreographyRunBinding != b.binding || p.GraphRootOID != b.graphRootOID ||
		precheckTask == nil || !b.nodeEligible(precheckTask.spec, p.Node) {
		b.mu.Unlock()
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimIsOutsideTheSignedRunGraphOrAdmittedNodeError)
	}
	if !precheckTask.spec.ExecutorOwned && precheckTask.spec.TemplateDigest != choreographyBrokerAbsentState &&
		p.PickupID == choreographyBrokerAbsentState {
		b.mu.Unlock()
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerCompiledPickupIdentityRequiredError)
	}
	if !b.executionStarted {
		b.mu.Unlock()
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNotReady)
	}
	needsBindings := precheckTask != nil && precheckTask.spec.TemplateDigest != choreographyBrokerAbsentState
	resolver := b.resolveBindings
	b.mu.Unlock()
	var resolved ChoreographyTaskBindings
	var resolveErr error
	if needsBindings && resolver != nil {
		resolved, resolveErr = resolver(ctx, p.Task, p.Node)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fault != nil {
		return ChoreographyReceipt{}, b.fault
	}
	if p.ChoreographyRunBinding != b.binding {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimBelongsToAnotherPinnedRunError)
	}
	if p.GraphRootOID != b.graphRootOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimNamesAnotherCompleteCompiledGraphRootError)
	}
	task := b.tasks[p.Task]
	if task != nil && task.inputsDigest == choreographyBrokerAbsentState && task.remaining == choreographyBrokerNoClaimableEdges {
		digest, err := b.readyInputsDigest(task)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		if b.resolveInputs != nil {
			provided, err := b.resolveInputs(ctx, p.Task)
			if err != nil {
				return ChoreographyReceipt{}, err
			}
			if provided != digest {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResolvedInputsDifferFromSignedPredecessorFinalityError)
			}
		}
		if digest == choreographyBrokerAbsentState {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResolvedInputDigestIsEmptyError)
		}
		task.inputsDigest = digest
	}
	if task == nil || p.Branch != task.spec.Branch || p.Attempt != task.attempt ||
		!b.nodeEligible(task.spec, p.Node) ||
		p.MembershipEpochOID != b.memberEpoch[p.Node] ||
		p.CapacityAuthorityID != b.members[p.Node].AuthorityID ||
		p.LeaseWeight != task.spec.Weight ||
		(task.offeredNode != choreographyBrokerAbsentState && p.Node != task.offeredNode) ||
		p.InputsDigest != task.inputsDigest {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimDoesNotMatchTheIndexedTaskError)
	}
	if !b.executionStarted || task.remaining != choreographyBrokerNoClaimableEdges {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNotReady)
	}
	if b.stopping {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalStopping)
	}
	if b.sealed || b.packageDead(task.spec.Package) || task.preparedBlockedBy != choreographyBrokerAbsentState || task.terminal {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalTerminal)
	}
	if b.outcomePolicy != nil && task.spec.Kind != string(KindPrepare) && task.spec.Package != choreographyBrokerAbsentState {
		skip, _, err := b.outcomePolicy.ShouldSkip(task.spec.Package)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		if skip {
			return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNotReady)
		}
	}
	if task.claim != choreographyBrokerAbsentState || p.ExpectedOID != task.tip {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalBusy)
	}
	if p.PickupID != choreographyBrokerAbsentState && task.pickups[p.PickupID] != nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerDurableClaimReceiptHasAnotherExactProposalIdentityError)
	}
	if b.used[task.spec.Budget]+task.spec.Weight > b.budgets[task.spec.Budget] {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalBudgetCapacity)
	}
	if b.nodeUsed[p.Node]+task.spec.Weight > b.nodeCaps[p.Node] {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNodeCapacity)
	}
	head, err := b.persist.LedgerHead(ctx)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if p.ExpectedLedgerOID != head.OID {
		return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalStale)
	}
	if task.spec.TemplateDigest != choreographyBrokerAbsentState {
		if p.TemplateDigest != task.spec.TemplateDigest || resolver == nil ||
			len(p.Bindings.Repositories) != choreographyBrokerNoBindings || len(p.Bindings.Inputs) != choreographyBrokerNoBindings ||
			len(p.Bindings.Exports) != choreographyBrokerNoBindings || len(p.Bindings.Env) != choreographyBrokerNoBindings {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimSuppliesUnverifiedCompiledTemplateOrFilledInputsError)
		}
		identityDigest, err := DigestChoreographyTaskBindings(p.Bindings)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		if identityDigest != p.BindingsDigest {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimSExactInputIdentityDigestDiffersError)
		}
		if len(p.Bindings.PredecessorResultOIDs) != len(task.spec.Predecessors) {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimOmitsSignedPredecessorIdentitiesError)
		}
		for index, name := range task.spec.Predecessors {
			parent := b.tasks[name]
			if parent == nil || !parent.terminal {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimPredecessorNamedIsNotLogicallyFinalErrorFormat, name)
			}
			expected := choreographyPredecessorIdentity(parent.resultOutputOID, parent.tip)
			if p.Bindings.PredecessorResultOIDs[index] != expected {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerClaimPredecessorNamedNamesAnotherSignedResultErrorFormat, name)
			}
		}
		if resolveErr != nil {
			fatal := fmt.Errorf(choreographyBrokerFatalBindingCauseFormat, ErrChoreographyBindingFatal, resolveErr)
			_, stopErr := b.persist.StopClaims(ctx)
			if stopErr != nil {
				return ChoreographyReceipt{}, errors.Join(fatal, stopErr)
			}
			b.stopping = choreographyBrokerRunStopping
			b.wakeBudgetWaiters()
			if b.onFatalBinding != nil {
				b.onFatalBinding(fatal)
			}
			receipt, refuseErr := b.persist.PersistRefusal(ctx, p, ChoreographyRefusalStopping)
			if refuseErr != nil {
				return ChoreographyReceipt{}, errors.Join(fatal, refuseErr)
			}
			return receipt, fatal
		}
		if task.spec.NeedsPreparedInput && !task.frameReady && resolved.PreparedInputOID != choreographyBrokerAbsentState {
			if err := b.persist.PersistFrameReady(ctx, ChoreographyFrameReadyRequest{
				TaskID: p.Task, Attempt: task.attempt, TaskOID: task.tip,
				PreparedInputOID: resolved.PreparedInputOID, InputsDigest: task.inputsDigest}); err != nil {
				return ChoreographyReceipt{}, err
			}
			task.frameReady, task.preparedInputOID = choreographyBrokerTaskFrameReady, resolved.PreparedInputOID
			return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNotReady)
		}
		if task.spec.NeedsPreparedInput && (!task.frameReady || task.preparedInputOID == choreographyBrokerAbsentState) {
			return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalNotReady)
		}
		if p.Bindings.PreparedInputOID != task.preparedInputOID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyPreparedIdentityMismatchError)
		}
		if resolved.PreparedInputOID != p.Bindings.PreparedInputOID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyResolvedPreparedMismatchFormat,
				p.Task, p.Bindings.PreparedInputOID, resolved.PreparedInputOID)
		}
		if !slices.Equal(resolved.PredecessorResultOIDs, p.Bindings.PredecessorResultOIDs) {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyResolvedPredecessorMismatchFormat,
				p.Task, p.Bindings.PredecessorResultOIDs, resolved.PredecessorResultOIDs)
		}
		p.resolvedBindings = &resolved
	}
	receipt, err := b.persist.PersistClaim(ctx, p)
	if err != nil {
		if errors.Is(err, errChoreographyAtomicRefused) {
			if b.onFatalBinding != nil {
				b.onFatalBinding(err)
			}
			if _, stopErr := b.persist.StopClaims(ctx); stopErr != nil {
				b.fault = errors.Join(err, stopErr)
				b.wakeBudgetWaiters()
				return ChoreographyReceipt{}, b.fault
			}
			b.stopping = choreographyBrokerRunStopping
			b.wakeBudgetWaiters()
			return b.persist.PersistRefusal(ctx, p, ChoreographyRefusalStopping)
		}
		if errors.Is(err, gitx.ErrPushUnknown) {
			// The exact task and ledger CAS may still land after readback.
			// Keep its pending identity and every physical lease fenced, but
			// wake task observers so the run fails boundedly instead of waiting
			// for a task event that cannot safely be synthesized.
			b.fault = err
			b.wakeBudgetWaiters()
		}
		return ChoreographyReceipt{}, err
	}
	if !receipt.Accepted || receipt.Kind != choreographyClaimKind || receipt.ChoreographyRunBinding != b.binding || receipt.Task != p.Task ||
		receipt.Attempt != p.Attempt || receipt.Node != p.Node || receipt.Branch != p.Branch ||
		receipt.ProposalID != p.ProposalID || receipt.ProposalDigest != query.ProposalDigest || receipt.OID == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistenceReturnedAMismatchedClaimError)
	}
	task.claim = receipt.OID
	task.tip = receipt.OID
	task.owner = p.Node
	if p.PickupID != choreographyBrokerAbsentState {
		if task.pickups == nil {
			task.pickups = make(map[string]*indexedChoreographyPickup)
		}
		task.pickups[p.PickupID] = &indexedChoreographyPickup{
			id: p.PickupID, attempt: p.Attempt, node: p.Node,
			claimOID: receipt.OID, assignmentOID: receipt.AssignmentOID,
			leaseIntentID: p.LeaseIntentID, deadlineUTC: receipt.PickupDeadlineUTC}
	}
	b.used[task.spec.Budget] += task.spec.Weight
	b.nodeUsed[p.Node] += task.spec.Weight
	if p.resolvedBindings != nil && b.onClaimAccepted != nil {
		b.onClaimAccepted(task.spec, *p.resolvedBindings)
	}
	return receipt, nil
}

// MembershipEpoch is the signed ledger epoch at which a node acquired its
// current capacity authority. Claims pin both values.
func (b *FirstNodeBroker) MembershipEpoch(node string) (string, ChoreographyRuntimeNode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	member := b.members[node]
	epoch := b.memberEpoch[node]
	if member.Name == choreographyBrokerAbsentState || epoch == choreographyBrokerAbsentState || b.draining[node] {
		return choreographyBrokerAbsentState, ChoreographyRuntimeNode{}, fmt.Errorf(choreographyBrokerNodeNamedHasNoCurrentAdmittedAuthorityErrorFormat, node)
	}
	return epoch, member, nil
}

// AdmitNode adds one preauthorized late identity at the exact ledger epoch.
// It holds the same broker lock as claims and stopping; Git persistence then
// serializes the admission with those transitions on the ledger ref.
func (b *FirstNodeBroker) AdmitNode(ctx context.Context, p ChoreographyJoinProposal) (ChoreographyJoinReceipt, error) {
	if err := p.validate(); err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if p.ChoreographyRunBinding != b.binding || p.GraphRootOID != b.graphRootOID {
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyBrokerJoinDiffersFromPinnedCompleteGraphError)
	}
	query, err := FormatChoreographyJoinReceiptQuery(p)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if err := VerifyChoreographyJoinIdentity(b.joinPolicy, p); err != nil {
		return b.refuseUnadmittedJoin(ctx, p, query.RequestDigest, choreographyJoinRefusalIdentity)
	}
	if b.verifyNodeJoin != nil {
		if err := b.verifyNodeJoin(ctx, p); err != nil {
			return b.refuseUnadmittedJoin(ctx, p, query.RequestDigest, choreographyJoinRefusalIdentity)
		}
	}
	if b.validateJoinReport != nil {
		if err := b.validateJoinReport(&p.Report); err != nil {
			return b.refuseUnadmittedJoin(ctx, p, query.RequestDigest, choreographyJoinRefusalReport)
		}
	}
	if old, found, err := b.persist.LookupJoinReceipt(ctx, query); err != nil {
		return ChoreographyJoinReceipt{}, err
	} else if found {
		return old, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fault != nil {
		return ChoreographyJoinReceipt{}, b.fault
	}
	if b.stopping || b.sealed || b.members[p.Node].Name != choreographyBrokerAbsentState {
		head, err := b.persist.LedgerHead(ctx)
		if err != nil {
			return ChoreographyJoinReceipt{}, err
		}
		reason := choreographyJoinRefusalAlreadyAdmitted
		if b.stopping || b.sealed {
			reason = choreographyJoinRefusalStopping
		}
		return ChoreographyJoinReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
			BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID,
			Node: p.Node, RequestID: p.RequestID, RequestDigest: query.RequestDigest,
			Refused: choreographyBrokerSignedRefusal, Reason: reason, Cursor: head}, nil
	}
	allowed, found := b.joinPolicy.AuthorizedNode(p.Node)
	if !found || !allowed.LateJoin || allowed.CoordinationEndpoint != p.Endpoint {
		return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyBrokerJoiningNodeIsOutsideLateAdmissionPolicyError)
	}
	receipt, err := b.persist.PersistJoin(ctx, p)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	if receipt.Accepted {
		if receipt.Member.Name != p.Node || receipt.Member.AuthorityID != p.Report.AuthorityID ||
			receipt.Member.Capacity != p.Report.Capacity || receipt.Cursor.OID == choreographyBrokerAbsentState {
			return ChoreographyJoinReceipt{}, fmt.Errorf(choreographyBrokerSettledJoinReceiptHasAnotherCapacityAuthorityError)
		}
		b.members[p.Node] = receipt.Member
		b.memberEpoch[p.Node] = receipt.Cursor.OID
		b.nodeCaps[p.Node] = receipt.Member.Capacity
		b.wakeBudgetWaiters()
	}
	return receipt, nil
}

func (b *FirstNodeBroker) refuseUnadmittedJoin(ctx context.Context, p ChoreographyJoinProposal,
	digest, reason string) (ChoreographyJoinReceipt, error) {
	head, err := b.persist.LedgerHead(ctx)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	return ChoreographyJoinReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID,
		Node: p.Node, RequestID: p.RequestID, RequestDigest: digest,
		Refused: choreographyBrokerSignedRefusal, Reason: reason, Cursor: head}, nil
}

func (b *FirstNodeBroker) LookupJoinReceipt(ctx context.Context, query ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error) {
	if query.ChoreographyRunBinding != b.binding || query.GraphRootOID != b.graphRootOID {
		return ChoreographyJoinReceipt{}, choreographyBrokerReceiptNotFound, fmt.Errorf(choreographyBrokerJoinReceiptQueryDiffersFromBrokerIdentityError)
	}
	return b.persist.LookupJoinReceipt(ctx, query)
}

func (b *FirstNodeBroker) nodeEligible(spec ChoreographyTaskSpec, name string) bool {
	member := b.members[name]
	if member.Name == choreographyBrokerAbsentState || b.draining[name] || member.Capacity < spec.Weight ||
		!isPlatformCompatible(member.Platform, spec.Platforms) {
		return choreographyBrokerNodeIneligible
	}
	switch spec.Placement {
	case PlacementOrchestrator:
		return member.First
	case PlacementWorker:
		return !member.First
	case PlacementAnyNode:
		return choreographyBrokerNodeEligible
	default:
		return slices.Contains(spec.EligibleNodes, name)
	}
}

// IsEligibleNodeAdmitted checks stable signed placement, not free capacity.
// An occupied eligible worker keeps a ready task queued past the admission
// deadline; only absence of every eligible member permits no-claim expiry.
func (b *FirstNodeBroker) IsEligibleNodeAdmitted(taskID string) bool {
	if b == nil {
		return choreographyBrokerNodeIneligible
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil {
		return choreographyBrokerNodeIneligible
	}
	for node := range b.members {
		if b.nodeEligible(task.spec, node) {
			return choreographyBrokerNodeEligible
		}
	}
	return choreographyBrokerNodeIneligible
}

func (b *FirstNodeBroker) readyInputsDigest(task *indexedChoreographyTask) (string, error) {
	predecessors := make([]ChoreographyInputFinality, choreographyBrokerNoPredecessors, len(task.spec.Predecessors))
	for _, name := range task.spec.Predecessors {
		parent := b.tasks[name]
		if parent == nil || !parent.terminal {
			return choreographyBrokerAbsentState, fmt.Errorf(choreographyBrokerInputPredecessorIsNotFinalError)
		}
		predecessors = append(predecessors, ChoreographyInputFinality{
			Task: name, OID: parent.tip, Succeeded: parent.succeeded})
	}
	return ChoreographyReadyInputsDigest(b.binding, task.spec.Task, predecessors)
}

// ExpectedInputIdentity exposes only signed immediate-predecessor identities
// to the first node's ready-frame registration and local picker.
func (b *FirstNodeBroker) ExpectedInputIdentity(taskID string) (string, []string, error) {
	if b == nil {
		return choreographyBrokerAbsentState, nil, fmt.Errorf(choreographyBrokerTaskInputIdentityHasNoBrokerError)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || task.remaining != choreographyBrokerNoClaimableEdges || task.terminal {
		return choreographyBrokerAbsentState, nil, fmt.Errorf(choreographyBrokerTaskNamedIsNotReadyForExactInputIdentityErrorFormat, taskID)
	}
	digest, err := b.readyInputsDigest(task)
	if err != nil {
		return choreographyBrokerAbsentState, nil, err
	}
	ids := make([]string, choreographyBrokerNoQueueEntries, len(task.spec.Predecessors))
	for _, name := range task.spec.Predecessors {
		parent := b.tasks[name]
		id := choreographyPredecessorIdentity(parent.resultOutputOID, parent.tip)
		ids = append(ids, id)
	}
	return digest, ids, nil
}

// MarkFrameReady signs a ledger-only wake after the first node has prepared
// the dynamic frame needed to validate an exact peer claim.
func (b *FirstNodeBroker) MarkFrameReady(ctx context.Context, taskID, preparedInputOID, inputsDigest string) error {
	if b == nil || inputsDigest == choreographyBrokerAbsentState {
		return fmt.Errorf(choreographyBrokerReadyFrameHasNoExactInputIdentityError)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || task.terminal || task.remaining != choreographyBrokerNoClaimableEdges || task.claim != choreographyBrokerAbsentState ||
		task.spec.Virtual || task.spec.TemplateDigest == choreographyBrokerAbsentState {
		return fmt.Errorf(choreographyBrokerTaskNamedCannotPublishAReadyFrameErrorFormat, taskID)
	}
	expected, err := b.readyInputsDigest(task)
	if err != nil {
		return err
	}
	if expected != inputsDigest {
		return fmt.Errorf(choreographyBrokerReadyFrameDiffersFromSignedPredecessorInputsError)
	}
	if task.frameReady && task.preparedInputOID != preparedInputOID {
		return fmt.Errorf(choreographyPreparedIdentityMismatchError)
	}
	if task.spec.NeedsPreparedInput && preparedInputOID == choreographyBrokerAbsentState {
		return fmt.Errorf(choreographyPreparedIdentityMismatchError)
	}
	if err := b.persist.PersistFrameReady(ctx, ChoreographyFrameReadyRequest{
		TaskID: taskID, Attempt: task.attempt, TaskOID: task.tip,
		PreparedInputOID: preparedInputOID, InputsDigest: inputsDigest}); err != nil {
		return err
	}
	task.frameReady, task.preparedInputOID = choreographyBrokerTaskFrameReady, preparedInputOID
	return nil
}

// AcceptResult imports bytes and records an attempt result. It deliberately
// retains capacity and predecessor counters until the first node persists a
// retry/finality decision; Result alone is not a logical task outcome.
func (b *FirstNodeBroker) AcceptResult(ctx context.Context, p ChoreographyResultProposal, payload *os.File) (ChoreographyReceipt, error) {
	if payload == nil || p.validate(p.PayloadBytes) != nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResultPayloadIsMissingError)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if p.ChoreographyRunBinding != b.binding {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResultBelongsToAnotherPinnedRunError)
	}
	query, err := FormatResultReceiptQuery(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if old, found, err := b.persist.LookupReceipt(ctx, query); err != nil {
		return ChoreographyReceipt{}, err
	} else if found {
		if old.Kind != choreographyResultKind || old.ProposalDigest != query.ProposalDigest || old.Task != p.Task || old.Attempt != p.Attempt ||
			old.Node != p.Node || old.Branch != p.Branch {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerProposalIdReusedForAnotherResultError)
		}
		task := b.tasks[p.Task]
		if p.PickupID != choreographyBrokerAbsentState {
			if task == nil || task.pickups[p.PickupID] == nil ||
				task.pickups[p.PickupID].claimOID != p.ClaimOID {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedResultRequiresRunStateReconciliationError)
			}
			pickup := task.pickups[p.PickupID]
			if pickup.resultReceiptOID == choreographyBrokerAbsentState {
				pickup.resultReceiptOID, pickup.resultOutputOID = old.OID, p.ResultDigest
				b.wakeBudgetWaiters()
			}
			if pickup.resultReceiptOID != old.OID {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedResultRequiresRunStateReconciliationError)
			}
			if task.claim == p.ClaimOID {
				task.result, task.resultOutputOID = old.OID, p.ResultDigest
			}
			return old, nil
		}
		if task != nil && task.result == choreographyBrokerAbsentState && task.claim == p.ClaimOID &&
			task.attempt == p.Attempt && task.owner == p.Node {
			task.result, task.resultOutputOID = old.OID, p.ResultDigest
			if p.PickupID == choreographyBrokerAbsentState {
				task.tip = old.OID
			}
		}
		if task == nil || task.result != old.OID {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedResultRequiresRunStateReconciliationError)
		}
		return old, nil
	}
	task := b.tasks[p.Task]
	if task != nil && !task.spec.ExecutorOwned &&
		task.spec.TemplateDigest != choreographyBrokerAbsentState &&
		p.PickupID == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerCompiledPickupIdentityRequiredError)
	}
	if p.PickupID != choreographyBrokerAbsentState {
		pickup := (*indexedChoreographyPickup)(nil)
		if task != nil {
			pickup = task.pickups[p.PickupID]
		}
		if pickup == nil || pickup.resultReceiptOID != choreographyBrokerAbsentState ||
			pickup.cancelOID != choreographyBrokerAbsentState ||
			pickup.claimOID != p.ClaimOID || pickup.attempt != p.Attempt ||
			pickup.node != p.Node || p.Branch != task.spec.Branch ||
			p.InputsDigest != task.inputsDigest {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResultDoesNotAnswerTheActiveClaimError)
		}
		receipt, err := b.persist.PersistResult(ctx, p, payload)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
		if !receipt.Accepted || receipt.Kind != choreographyResultKind ||
			receipt.ProposalDigest != query.ProposalDigest || receipt.PickupID != p.PickupID ||
			receipt.OID == choreographyBrokerAbsentState {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistenceReturnedAMismatchedResultError)
		}
		pickup.resultReceiptOID, pickup.resultOutputOID = receipt.OID, p.ResultDigest
		b.wakeBudgetWaiters()
		if task.claim == p.ClaimOID {
			task.result, task.resultOutputOID = receipt.OID, p.ResultDigest
		}
		return receipt, nil
	}
	if task == nil || task.terminal || task.result != choreographyBrokerAbsentState || task.claim == choreographyBrokerAbsentState || task.cancel != choreographyBrokerAbsentState ||
		p.Branch != task.spec.Branch || p.InputsDigest != task.inputsDigest ||
		p.Attempt != task.attempt || p.ClaimOID != task.claim || p.Node != task.owner {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResultDoesNotAnswerTheActiveClaimError)
	}
	receipt, err := b.persist.PersistResult(ctx, p, payload)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if !receipt.Accepted || receipt.Terminal || receipt.Kind != choreographyResultKind || receipt.ChoreographyRunBinding != b.binding ||
		receipt.Task != p.Task || receipt.Attempt != p.Attempt || receipt.Node != p.Node ||
		receipt.Branch != p.Branch || receipt.ProposalID != p.ProposalID || receipt.ProposalDigest != query.ProposalDigest || receipt.OID == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistenceReturnedAMismatchedResultError)
	}
	task.result = receipt.OID
	task.resultOutputOID = p.ResultDigest
	if p.PickupID == choreographyBrokerAbsentState {
		task.tip = receipt.OID
	}
	return receipt, nil
}

// AcceptAck records that the selected choreographer stopped after observing
// the exact Cancel commit. Capacity remains fenced until first-node policy
// explicitly decides whether this attempt is retryable.
func (b *FirstNodeBroker) AcceptAck(ctx context.Context, p ChoreographyAckProposal) (ChoreographyReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p.ChoreographyRunBinding != b.binding || p.Task == choreographyBrokerAbsentState || p.Attempt < choreographyBrokerFirstAttempt || p.ProposalID == choreographyBrokerAbsentState ||
		p.Node == choreographyBrokerAbsentState || p.Branch == choreographyBrokerAbsentState || p.CancelOID == choreographyBrokerAbsentState || p.AssignmentOID == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerInvalidCancellationAcknowledgementError)
	}
	query, err := FormatAckReceiptQuery(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if old, found, err := b.persist.LookupReceipt(ctx, query); err != nil {
		return ChoreographyReceipt{}, err
	} else if found {
		if old.Kind != MessageAck || old.ProposalDigest != query.ProposalDigest || old.Branch != p.Branch || old.Node != p.Node || old.Task != p.Task ||
			old.Attempt != p.Attempt {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerReusedAcknowledgementIdentityError)
		}
		return old, nil
	}
	task := b.tasks[p.Task]
	if task != nil && task.spec.Branch == p.Branch {
		for _, pickup := range task.pickups {
			if pickup.cancelOID != p.CancelOID || pickup.attempt != p.Attempt ||
				pickup.node != p.Node || !pickup.contactLost {
				continue
			}
			if pickup.resultReceiptOID != choreographyBrokerAbsentState || pickup.settled {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerAcknowledgementHasNoActiveAttemptError)
			}
			receipt, persistErr := b.persist.PersistAck(ctx, p)
			if persistErr != nil {
				return ChoreographyReceipt{}, persistErr
			}
			if !receipt.Accepted || receipt.Kind != MessageAck ||
				receipt.PickupID != pickup.id || receipt.OID == choreographyBrokerAbsentState {
				return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedAcknowledgementDiffersError)
			}
			pickup.resultReceiptOID = receipt.OID
			b.wakeBudgetWaiters()
			return receipt, nil
		}
	}
	if task == nil || task.terminal || task.spec.Branch != p.Branch || task.attempt != p.Attempt ||
		task.owner != p.Node || task.claim == choreographyBrokerAbsentState || task.cancel == choreographyBrokerAbsentState ||
		p.CancelOID != task.cancel || task.result != choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerAcknowledgementHasNoActiveAttemptError)
	}
	receipt, err := b.persist.PersistAck(ctx, p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if !receipt.Accepted || receipt.Kind != MessageAck || receipt.Task != p.Task || receipt.Attempt != p.Attempt ||
		receipt.Node != p.Node || receipt.Branch != p.Branch || receipt.ProposalID != p.ProposalID || receipt.ProposalDigest != query.ProposalDigest || receipt.OID == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedAcknowledgementDiffersError)
	}
	if receipt.PickupID != choreographyBrokerAbsentState {
		pickup := task.pickups[receipt.PickupID]
		if pickup == nil || pickup.claimOID != task.claim ||
			pickup.cancelOID != p.CancelOID || pickup.resultReceiptOID != choreographyBrokerAbsentState {
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerPersistedAcknowledgementDiffersError)
		}
		pickup.resultReceiptOID = receipt.OID
	}
	task.result, task.tip, task.resultOutputOID = receipt.OID, receipt.OID, choreographyNoAcceptedResultOutputOID
	return receipt, nil
}

// DecideAttempt is first-node policy, after validating the imported result.
// A retry starts at the next attempt on the same branch. Every logical
// finality releases precedence edges once; the shared package outcome rule
// separately determines which newly ready descendants require Blocked.
func (b *FirstNodeBroker) DecideAttempt(ctx context.Context, d ChoreographyDecision) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[d.Task]
	if task != nil {
		for _, pickup := range task.pickups {
			if pickup.resultReceiptOID == d.ResultOID {
				if task.terminal || !d.Succeeded && task.claim != pickup.claimOID {
					return b.settleLosingPickup(ctx, task, pickup, d)
				}
				return b.decidePickupAttempt(ctx, task, pickup, d)
			}
		}
	}
	if d.ChoreographyRunBinding != b.binding || task == nil || d.Branch != task.spec.Branch || task.result == choreographyBrokerAbsentState ||
		task.terminal || d.Attempt != task.attempt || d.ResultOID != task.result || d.Unclaimed ||
		d.Retry && (d.Succeeded || task.attempt >= maxPlacementAttempts) {
		return fmt.Errorf(choreographyBrokerInvalidAttemptDecisionError)
	}
	d.Node = task.owner
	if err := b.validatePackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	decisionOID, err := b.persist.PersistDecision(ctx, d)
	if err != nil {
		return err
	}
	if decisionOID == choreographyBrokerAbsentState {
		return fmt.Errorf(choreographyBrokerPersistenceReturnedNoDecisionCommitError)
	}
	b.used[task.spec.Budget] -= task.spec.Weight
	b.nodeUsed[task.owner] -= task.spec.Weight
	b.wakeBudgetWaiters()
	task.claim, task.cancel, task.result, task.owner = choreographyBrokerAbsentState, choreographyBrokerAbsentState, choreographyBrokerAbsentState, choreographyBrokerAbsentState
	task.tip = decisionOID
	if d.Retry {
		task.attempt++
		task.resultOutputOID = choreographyNoAcceptedResultOutputOID
		task.offeredNode = choreographyBrokerAbsentState
		task.frameReady, task.preparedInputOID = choreographyBrokerTaskFrameUnready, choreographyBrokerAbsentState
		return nil
	}
	task.terminal, task.succeeded = choreographyBrokerTaskTerminal, d.Succeeded
	if err := b.applyPackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	if task.spec.Package != choreographyBrokerAbsentState && d.PackageOutcome == choreographyBrokerAbsentState &&
		!task.spec.ExecutorOwned {
		task.pendingPackageSettlement = choreographyBrokerPackageSettlementPending
	} else {
		b.discharge(task)
	}
	return nil
}

// AcceptLocalOutcome admits a first-node-only effect after native
// publication/recording has settled. A raw script return is insufficient:
// the caller must name the accepted effect identity in EffectDigest.
func (b *FirstNodeBroker) AcceptLocalOutcome(ctx context.Context, outcome ChoreographyLocalOutcome) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[outcome.Task]
	if outcome.ChoreographyRunBinding != b.binding || outcome.Task == choreographyBrokerAbsentState || outcome.Node == choreographyBrokerAbsentState ||
		outcome.EffectDigest == choreographyBrokerAbsentState || task == nil || !task.spec.LocalAuthority ||
		task.remaining != choreographyBrokerNoClaimableEdges || task.terminal || task.claim == choreographyBrokerAbsentState || task.owner != outcome.Node {
		return fmt.Errorf(choreographyBrokerLocalAuthorityOutcomeIsNotAdmissibleError)
	}
	if err := validateChoreographyLocalEffect(outcome, task.spec); err != nil {
		return err
	}
	if err := b.validatePackageOutcome(task.spec.Package, outcome.PackageOutcome); err != nil {
		return err
	}
	outcome.ClaimOID = task.claim
	decisionOID, err := b.persist.PersistLocalOutcome(ctx, outcome)
	if err != nil {
		return err
	}
	task.terminal, task.succeeded, task.tip = choreographyBrokerTaskTerminal, outcome.Succeeded, decisionOID
	task.claim = choreographyBrokerAbsentState
	b.used[task.spec.Budget] -= task.spec.Weight
	b.nodeUsed[outcome.Node] -= task.spec.Weight
	if err := b.applyPackageOutcome(task.spec.Package, outcome.PackageOutcome); err != nil {
		return err
	}
	b.discharge(task)
	b.wakeBudgetWaiters()
	return nil
}

func (b *FirstNodeBroker) packageDead(pkg string) bool {
	switch b.packageState[pkg] {
	case choreographyBrokerOutcomeFailed, choreographyBrokerOutcomeSkipped, choreographyBrokerOutcomeCancelled, choreographyBrokerOutcomeRecordBlocked:
		return choreographyBrokerPackageDead
	default:
		return choreographyBrokerPackageLive
	}
}

func (b *FirstNodeBroker) applyPackageOutcome(pkg, outcome string) error {
	if outcome == choreographyBrokerAbsentState {
		return nil
	}
	if b.outcomePolicy != nil {
		if err := b.outcomePolicy.ApplyOutcome(pkg, outcome); err != nil {
			return err
		}
	}
	b.packageState[pkg] = outcome
	return nil
}

func (b *FirstNodeBroker) validatePackageOutcome(pkg, outcome string) error {
	if outcome == choreographyBrokerAbsentState {
		return nil
	}
	if b.outcomePolicy == nil {
		return fmt.Errorf(choreographyBrokerPackageOutcomeHasNoSharedEvaluatorError)
	}
	return b.outcomePolicy.ValidateOutcome(pkg, outcome)
}

func (b *FirstNodeBroker) discharge(task *indexedChoreographyTask) {
	for _, child := range task.children {
		next := b.tasks[child]
		blocker := task.preparedBlockedBy
		if blocker == choreographyBrokerAbsentState && task.spec.Kind == string(KindPrepare) && !task.succeeded {
			blocker = task.spec.Package
		}
		if blocker != choreographyBrokerAbsentState &&
			choreographyPreparedProviderFor(next.spec, blocker) &&
			(next.preparedBlockedBy == choreographyBrokerAbsentState || blocker < next.preparedBlockedBy) {
			next.preparedBlockedBy = blocker
		}
		next.remaining--
		if next.spec.Virtual && next.remaining == choreographyBrokerNoClaimableEdges {
			b.completeVirtual(next)
		}
	}
}

// AcceptBlocked persists the shared evaluator's no-claim conclusion before
// releasing this task's outgoing precedence edges.
func (b *FirstNodeBroker) AcceptBlocked(ctx context.Context,
	outcome ChoreographyBlockedOutcome) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[outcome.Task]
	if outcome.ChoreographyRunBinding != b.binding || task == nil || task.spec.Virtual ||
		task.spec.Package != outcome.Package || task.remaining != choreographyBrokerNoClaimableEdges || task.terminal ||
		task.claim != choreographyBrokerAbsentState || task.offeredNode != choreographyBrokerAbsentState || task.queueFence != choreographyBrokerAbsentState ||
		outcome.BlockedBy == choreographyBrokerAbsentState || outcome.LocalFailure == nil &&
		outcome.BlockedBy == outcome.Package && outcome.PackageOutcome == choreographyBrokerOutcomeFailed {
		return fmt.Errorf(choreographyBrokerBlockedTaskIsNotAdmissibleError)
	}
	if outcome.LocalFailure != nil {
		if task.spec.LocalAuthority || task.attempt != choreographyBrokerFirstAttempt ||
			task.tip != task.spec.ReservationOID || task.preparedBlockedBy != choreographyBrokerAbsentState ||
			outcome.BlockedBy != outcome.Package ||
			outcome.PackageOutcome != choreographyBrokerOutcomeFailed &&
				(outcome.PackageOutcome != choreographyBrokerAbsentState || b.packageState[outcome.Package] != choreographyBrokerOutcomeFailed) {
			return fmt.Errorf(choreographyBrokerLocalFailureHasNoInertDelegatedTaskError)
		}
		if outcome.LocalFailure.ReservationOID == choreographyBrokerAbsentState {
			outcome.LocalFailure.ReservationOID = task.spec.ReservationOID
		}
		if !validChoreographyLocalFailure(outcome.LocalFailure, task.spec.ReservationOID) {
			return fmt.Errorf(choreographyBrokerInvalidLocalFailureEvidenceError)
		}
	} else if task.spec.Kind == string(KindPrepare) && outcome.PackageOutcome == choreographyBrokerAbsentState {
		blocker := task.preparedBlockedBy
		if blocker == choreographyBrokerAbsentState {
			blocker = task.spec.Package
		}
		if outcome.BlockedBy != blocker {
			return fmt.Errorf(choreographyBrokerPreparedTaskNamesAnotherFailureCauseError)
		}
	} else if b.packageDead(outcome.Package) && outcome.PackageOutcome == choreographyBrokerAbsentState {
		if outcome.BlockedBy != outcome.Package {
			return fmt.Errorf(choreographyBrokerBlockedTaskNamesAnotherSettledPackageError)
		}
	} else if task.preparedBlockedBy != choreographyBrokerAbsentState {
		if outcome.BlockedBy != task.preparedBlockedBy {
			return fmt.Errorf(choreographyBrokerBlockedTaskNamesAnotherPreparedProviderError)
		}
	} else if b.packageDead(outcome.Package) {
		if outcome.BlockedBy != outcome.Package {
			return fmt.Errorf(choreographyBrokerBlockedTaskNamesAnotherSettledPackageError)
		}
	} else {
		if b.outcomePolicy == nil {
			return fmt.Errorf(choreographyBrokerBlockedTaskHasNoSharedOutcomeEvaluatorError)
		}
		skip, blocker, err := b.outcomePolicy.ShouldSkip(outcome.Package)
		if err != nil {
			return err
		}
		if !skip || blocker != outcome.BlockedBy {
			return fmt.Errorf(choreographyBrokerBlockedTaskDiffersFromSharedSkipPolicyError)
		}
	}
	if err := b.validatePackageOutcome(outcome.Package, outcome.PackageOutcome); err != nil {
		return err
	}
	outcome.Attempt = task.attempt
	outcome.ExpectedOID = task.tip
	blockedOID, err := b.persist.PersistBlocked(ctx, outcome)
	if err != nil {
		return err
	}
	task.terminal = choreographyBrokerTaskTerminal
	task.tip = blockedOID
	if err := b.applyPackageOutcome(outcome.Package, outcome.PackageOutcome); err != nil {
		return err
	}
	b.discharge(task)
	b.wakeBudgetWaiters()
	return nil
}

func (b *FirstNodeBroker) LookupReceipt(ctx context.Context, q ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	if q.ChoreographyRunBinding != b.binding ||
		(q.Kind != choreographyClaimKind && q.Kind != choreographyResultKind && q.Kind != MessageAck) ||
		q.Task == choreographyBrokerAbsentState || q.Attempt < choreographyBrokerFirstAttempt ||
		q.Branch == choreographyBrokerAbsentState || q.Node == choreographyBrokerAbsentState || q.ProposalID == choreographyBrokerAbsentState || q.ProposalDigest == choreographyBrokerAbsentState {
		return ChoreographyReceipt{}, choreographyBrokerReceiptNotFound, fmt.Errorf(choreographyBrokerReceiptQueryBelongsToAnotherRunError)
	}
	if task := b.tasks[q.Task]; task == nil || task.spec.Branch != q.Branch {
		return ChoreographyReceipt{}, choreographyBrokerReceiptNotFound, fmt.Errorf(choreographyBrokerReceiptQueryNamesAnotherTaskRefError)
	}
	return b.persist.LookupReceipt(ctx, q)
}

// StopClaims publishes the durable admission fence while leaving active
// attempts able to return Results and Acks for explicit settlement.
func (b *FirstNodeBroker) StopClaims(ctx context.Context) (ChoreographyEventCursor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cursor, err := b.persist.StopClaims(ctx)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	b.stopping = choreographyBrokerRunStopping
	b.wakeBudgetWaiters()
	return cursor, nil
}

// ReadEvents exposes only durable signed ledger changes. It does not derive
// readiness by rescanning the graph for a polling peer.
func (b *FirstNodeBroker) ReadEvents(ctx context.Context, cursor ChoreographyEventCursor,
	maxEvents int) (ChoreographyEventPage, error) {
	return b.persist.ReadEvents(ctx, cursor, maxEvents)
}
