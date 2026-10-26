// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyLedgerKind MessageKind = "choreography-ledger"
const choreographyLocalOutcomeKind MessageKind = "choreography-local-outcome"
const choreographyLedgerPrefix = "dispat-ledger-"
const choreographyAtomicRefusalRetryCount = 1
const choreographyAtomicRefusedErrorFormat = "execution: remote refused atomic update without changing task %s, slot %s or ledger %s: %w"
const choreographyAtomicRefusedErrorMessage = "execution: atomic task and capacity update was definitively refused"
const choreographyReadyFrameReplayMismatchFormat = "execution: signed ready-frame %q differs from retained task state (needs prepared: %t, prepared: %q, task tip: %q, prior: %q)"
const choreographyPreparedCarrierReplayMismatchFormat = "execution: signed prepared carrier for %q differs from retained graph (binding match: %t, carrier task: %q, root: %q, expected root: %q, template: %q, expected template: %q, predecessors: %q, bound predecessors: %q)"
const choreographyLedgerMissingIdentity = ""
const choreographyLedgerPlatformSeparator = "/"
const choreographyLedgerStopIdentityPrefix = "stop:"
const choreographyLedgerSealIdentityPrefix = "seal:"
const choreographyLedgerAuthorityHexDigits = 32
const choreographyLedgerNoItems = 0
const choreographyLedgerFirstItemIndex = 0
const choreographyLedgerSecondItemIndex = 1
const choreographyLedgerThirdItemIndex = 2
const choreographyLedgerSingleItemCount = 1
const choreographyLedgerTwoParentCount = 2
const choreographyLedgerThreeParentCount = 3
const choreographyLedgerAtomicRefCapacity = 3
const choreographyLedgerFirstSequence = 1
const choreographyLedgerRootSequence = 0
const choreographyLedgerPageMaxEvents = 32
const choreographyLedgerPageMinEvents = 1
const choreographyLedgerRefusalStartAttempt = 0
const choreographyLedgerKnownEventKind = true
const choreographyLedgerUnknownEventKind = false
const choreographyLedgerActiveClaim = true
const choreographyLedgerInactiveClaim = false
const choreographyLedgerFinishedTask = true
const choreographyLedgerSealed = true
const choreographyLedgerStopping = true
const choreographyLedgerExecutionStarted = true
const choreographyLedgerInitialized = true
const choreographyLedgerRosterEmptyError = "execution: choreography roster has no nodes"
const choreographyLedgerRosterInvalidError = "execution: invalid choreography node roster"
const choreographyLedgerFirstNodeCountError = "execution: choreography roster must name one first node"
const choreographyLedgerBudgetInvalidError = "execution: invalid choreography run budget"
const choreographyLedgerPendingCASErr = "execution: unresolved task and capacity CAS blocks another transition"
const choreographyLedgerInitPendingErr = "execution: complete graph initialization has not settled on the ledger"
const choreographyLedgerRunSealedErr = "execution: choreography run is sealed"
const choreographyLedgerAdmissionStoppedErr = "execution: choreography claim admission has stopped"
const choreographyLedgerAdvancedErr = "execution: remote ledger advanced; broker state needs recovery before another effect"
const choreographyLedgerStaleEpochErr = "execution: claim proposal names a stale capacity epoch"
const choreographyLedgerEventSizeErr = "execution: signed ledger event exceeds the documented transfer limit"
const choreographyLedgerCASUnresolvedFormat = "execution: task and capacity CAS is unresolved (task %s, ledger %s): %w"
const choreographyLedgerRemoteRefsErr = "remote refs do not name the proposed transaction"
const choreographyLedgerRootInvalidErr = "execution: invalid choreography ledger root"
const choreographyLedgerRosterSizeErr = "execution: signed roster exceeds the documented transfer limit"
const choreographyLedgerRootReadErr = "execution: invalid ledger root read"
const choreographyLedgerRefAbsentErr = "execution: canonical ledger ref is unavailable"
const choreographyLedgerBootstrapRootErr = "execution: canonical ledger is not rooted at the pinned bootstrap"
const choreographyLedgerSignedRootErr = "execution: invalid signed choreography ledger root"
const choreographyLedgerRootParentErr = "execution: choreography ledger root has wrong bootstrap parent"
const choreographyLedgerCursorInvalidErr = "execution: invalid choreography event cursor"
const choreographyLedgerCursorOutsideErr = "execution: event cursor is outside the signed ledger"
const choreographyLedgerStopBeforeSealErr = "execution: claim admission must stop before run seal"
const choreographyLedgerActiveClaimSealErr = "execution: active claim must settle before run seal"
const choreographyLedgerUnfinishedTaskSealErr = "execution: unfinished task prevents normal run seal"
const choreographyLedgerUnfinishedEffectSealErr = "execution: unfinished local effect prevents normal run seal"
const choreographyLedgerUnsettledTaskPackageSealErr = "execution: task package outcome must settle before normal run seal"
const choreographyLedgerEventInvalidErr = "execution: invalid signed choreography event"
const choreographyLedgerInitFirstErr = "execution: first ledger transition is not exact complete graph initialization"
const choreographyLedgerInitRepeatedErr = "execution: complete graph initialization appears twice"
const choreographyLedgerEventParentErr = "execution: event carrier parents do not match signed state"
const choreographyLedgerBlockedParentErr = "execution: blocked task commit lacks exact predecessor"
const choreographyLedgerOfferedParentErr = "execution: offered assignment lacks exact task predecessor"
const choreographyLedgerSlotParentErr = "execution: offered slot lacks exact prior notice"
const choreographyLedgerSlotIncompleteErr = "execution: offered slot identity is incomplete"
const choreographyLedgerClaimAssignmentAbsentErr = "execution: accepted claim has no compiled assignment"
const choreographyLedgerClaimAssignmentRootErr = "execution: accepted claim lacks its complete-root-bound assignment"
const choreographyLedgerAssignmentParentErr = "execution: accepted assignment has another canonical task predecessor"
const choreographyLedgerAssignmentGraphErr = "execution: accepted claim assignment is not signed for the complete compiled graph"
const choreographyLedgerDecisionParentErr = "execution: cancellation or queue decision lacks exact task predecessor"
const choreographyLedgerCancelEventErr = "execution: signed cancellation differs from ledger event"
const choreographyLedgerCancelAssignmentErr = "execution: cancellation names another assignment"
const choreographyLedgerQueueEventErr = "execution: signed queue decision differs from ledger event"
const choreographyLedgerQueueFenceErr = "execution: final queue decision lacks its pre-hook fence"
const choreographyLedgerQueueRetryFenceErr = "execution: queued retry invents a pre-hook fence"
const choreographyLedgerQueueAssignmentFormat = "execution: queue decision has no verified offered assignment: %w"
const choreographyLedgerHistoryErr = "execution: ledger tip is outside signed first-parent history"
const choreographyLedgerUnsignedObjectErr = "execution: ledger object is not a signed ledger commit"
const choreographyLedgerAnotherRunErr = "execution: ledger record belongs to another pinned run"

var errChoreographyAtomicRefused = errors.New(choreographyAtomicRefusedErrorMessage)

// ChoreographyLedgerReservation is the signed, inert root created in the
// initial bounded batch beside every logical task ref.
type ChoreographyLedgerReservation struct {
	Branch string
	OID    string
}

type ChoreographyLedgerRootOptions struct {
	Mailbox      *GitMailbox
	Binding      ChoreographyRunBinding
	BootstrapOID string
	Roster       ChoreographyRuntimeRoster
}

// ChoreographyRuntimeRoster is the verified post-preflight placement view.
// It belongs to the signed ledger root because the bootstrap is prepared
// before node reports arrive. Peers derive task eligibility from this view
// and the shared graph rules, never from their ambient node configuration.
type ChoreographyRuntimeRoster struct {
	Nodes   []ChoreographyRuntimeNode   `json:"nodes"`
	Budgets []ChoreographyRuntimeBudget `json:"budgets"`
}

type ChoreographyRuntimeNode struct {
	Name        string `json:"name"`
	Platform    string `json:"platform"`
	Capacity    int    `json:"capacity"`
	First       bool   `json:"first,omitempty"`
	AuthorityID string `json:"authorityId,omitempty"`
}

type ChoreographyRuntimeBudget struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

func (r ChoreographyRuntimeRoster) validate() error {
	if len(r.Nodes) == choreographyLedgerNoItems {
		return fmt.Errorf(choreographyLedgerRosterEmptyError)
	}
	first := choreographyLedgerNoItems
	previous := choreographyLedgerMissingIdentity
	for _, node := range r.Nodes {
		if node.Name == choreographyLedgerMissingIdentity || (previous != choreographyLedgerMissingIdentity && node.Name <= previous) ||
			node.Capacity <= choreographyLedgerNoItems ||
			!validSlotHex(node.AuthorityID, choreographyLedgerAuthorityHexDigits) ||
			strings.Count(node.Platform, choreographyLedgerPlatformSeparator) != choreographyLedgerSingleItemCount ||
			strings.HasPrefix(node.Platform, choreographyLedgerPlatformSeparator) || strings.HasSuffix(node.Platform, choreographyLedgerPlatformSeparator) {
			return fmt.Errorf(choreographyLedgerRosterInvalidError)
		}
		if node.First {
			first++
		}
		previous = node.Name
	}
	if first != choreographyLedgerSingleItemCount {
		return fmt.Errorf(choreographyLedgerFirstNodeCountError)
	}
	previous = choreographyLedgerMissingIdentity
	for _, budget := range r.Budgets {
		if budget.Name == choreographyLedgerMissingIdentity ||
			(previous != choreographyLedgerMissingIdentity && budget.Name <= previous) ||
			budget.Capacity <= choreographyLedgerNoItems {
			return fmt.Errorf(choreographyLedgerBudgetInvalidError)
		}
		previous = budget.Name
	}
	return nil
}

// FormatChoreographyLedgerBranch names the one capacity and event CAS domain
// for a run. Every task ref participating in it must be on that same remote.
func FormatChoreographyLedgerBranch(run string) string {
	return choreographyLedgerPrefix + run
}

// ChoreographyEventKind is a durable frontier change. Only terminal events
// discharge outgoing dependency edges; accepted claims merely make a ready
// task busy until a retry-ready or terminal event arrives.
type ChoreographyEventKind string

const (
	ChoreographyClaimAccepted          ChoreographyEventKind = "claim-accepted"
	ChoreographyAssignmentOffered      ChoreographyEventKind = "assignment-offered"
	ChoreographyClaimRefused           ChoreographyEventKind = "claim-refused"
	ChoreographyResultAccepted         ChoreographyEventKind = "result-accepted"
	ChoreographyCancelIssued           ChoreographyEventKind = "cancel-issued"
	ChoreographyQueueFenced            ChoreographyEventKind = "queue-fenced"
	ChoreographyAckAccepted            ChoreographyEventKind = "ack-accepted"
	ChoreographyRetryReady             ChoreographyEventKind = "retry-ready"
	ChoreographyTerminal               ChoreographyEventKind = "terminal"
	ChoreographyBlocked                ChoreographyEventKind = "blocked"
	ChoreographyRunStopping            ChoreographyEventKind = "run-stopping"
	ChoreographyRunSealed              ChoreographyEventKind = "run-sealed"
	ChoreographyMemberJoined           ChoreographyEventKind = "member-joined"
	ChoreographyMemberDraining         ChoreographyEventKind = "member-draining"
	ChoreographyMemberCapacityChanged  ChoreographyEventKind = "member-capacity"
	ChoreographyMemberJoinRefused      ChoreographyEventKind = "member-join-refused"
	ChoreographyNoClaimExpired         ChoreographyEventKind = "no-claim-expired"
	ChoreographyNoClaimPackageSettled  ChoreographyEventKind = "no-claim-package-settled"
	ChoreographyInertCancelled         ChoreographyEventKind = "inert-cancelled"
	ChoreographyInertCancelSettled     ChoreographyEventKind = "inert-cancel-settled"
	ChoreographyTaskPackageSettled     ChoreographyEventKind = "task-package-settled"
	ChoreographyFrameReady             ChoreographyEventKind = "frame-ready"
	ChoreographyInitializationComplete ChoreographyEventKind = "initialization-complete"
	ChoreographyExecutionStarted       ChoreographyEventKind = "execution-started"
	ChoreographyInputInvalidation      ChoreographyEventKind = "input-invalidation"
)

// IsChoreographyEventKind keeps retained-feed decoding aligned with every
// canonical ledger transition; unknown kinds never enter a peer frontier.
func IsChoreographyEventKind(kind ChoreographyEventKind) bool {
	switch kind {
	case ChoreographyClaimAccepted, ChoreographyAssignmentOffered,
		ChoreographyClaimRefused, ChoreographyResultAccepted,
		ChoreographyCancelIssued, ChoreographyQueueFenced,
		ChoreographyAckAccepted, ChoreographyRetryReady,
		ChoreographyTerminal, ChoreographyBlocked,
		ChoreographyRunStopping, ChoreographyRunSealed,
		ChoreographyMemberJoined, ChoreographyMemberDraining,
		ChoreographyMemberCapacityChanged, ChoreographyMemberJoinRefused,
		ChoreographyNoClaimExpired, ChoreographyNoClaimPackageSettled,
		ChoreographyInertCancelled, ChoreographyInertCancelSettled,
		ChoreographyTaskPackageSettled,
		ChoreographyFrameReady, ChoreographyInitializationComplete,
		ChoreographyExecutionStarted, ChoreographyInputInvalidation:
		return choreographyLedgerKnownEventKind
	default:
		return choreographyLedgerUnknownEventKind
	}
}

// ChoreographyEvent names the exact task ref object that caused one accepted
// change. A result commit alone is never a terminal event.
type ChoreographyEvent struct {
	Sequence              uint64                   `json:"sequence"`
	LedgerOID             string                   `json:"ledgerOid,omitempty"`
	Kind                  ChoreographyEventKind    `json:"kind"`
	Task                  string                   `json:"task"`
	Package               string                   `json:"package,omitempty"`
	PackageOutcome        string                   `json:"packageOutcome,omitempty"`
	BlockedBy             string                   `json:"blockedBy,omitempty"`
	Attempt               int                      `json:"attempt"`
	Node                  string                   `json:"node,omitempty"`
	Member                *ChoreographyRuntimeNode `json:"member,omitempty"`
	TaskOID               string                   `json:"taskOid"`
	GraphRootOID          string                   `json:"graphRootOid,omitempty"`
	OldPreparedOID        string                   `json:"oldPreparedOid,omitempty"`
	NewPreparedOID        string                   `json:"newPreparedOid,omitempty"`
	PreparedInputOID      string                   `json:"preparedInputOid,omitempty"`
	AffectedTasks         []string                 `json:"affectedTasks,omitempty"`
	AdmissionDeadlineUTC  string                   `json:"admissionDeadlineUtc,omitempty"`
	ExecutionStartedUTC   string                   `json:"executionStartedUtc,omitempty"`
	AdmissionPolicyDigest string                   `json:"admissionPolicyDigest,omitempty"`
	MissingPlacement      string                   `json:"missingPlacement,omitempty"`
	AssignmentOID         string                   `json:"assignmentOid,omitempty"`
	MembershipEpochOID    string                   `json:"membershipEpochOid,omitempty"`
	CapacityAuthorityID   string                   `json:"capacityAuthorityId,omitempty"`
	LeaseIntentID         string                   `json:"leaseIntentId,omitempty"`
	LeaseWeight           int                      `json:"leaseWeight,omitempty"`
	PriorTaskOID          string                   `json:"priorTaskOid,omitempty"`
	SlotBranch            string                   `json:"slotBranch,omitempty"`
	SlotOID               string                   `json:"slotOid,omitempty"`
	PriorSlotOID          string                   `json:"priorSlotOid,omitempty"`
	// ProposalDigest identifies the exact accepted or refused claim body.
	// OutputOID is the signed Result commit admitted by a successful final
	// decision; the decision's TaskOID alone is not an output manifest.
	ProposalDigest   string `json:"proposalDigest,omitempty"`
	ProposalID       string `json:"proposalId,omitempty"`
	InputsDigest     string `json:"inputsDigest,omitempty"`
	RefusalReason    string `json:"refusalReason,omitempty"`
	CapacityScope    string `json:"capacityScope,omitempty"`
	Budget           string `json:"budget,omitempty"`
	Weight           int    `json:"weight,omitempty"`
	OutputOID        string `json:"outputOid,omitempty"`
	ResultReceiptOID string `json:"resultReceiptOid,omitempty"`
	Succeeded        bool   `json:"succeeded,omitempty"`
	Aborted          bool   `json:"aborted,omitempty"`
}

// ChoreographyEventCursor is the exact ledger commit and sequence a peer has
// already applied. It survives reconnects; a mismatched cursor is refused.
type ChoreographyEventCursor struct {
	OID      string `json:"oid"`
	Sequence uint64 `json:"sequence"`
}

// ChoreographyEventPage is ordered oldest first. More means the peer must
// fetch another page before it treats the frontier as current.
type ChoreographyEventPage struct {
	Events []ChoreographyEvent     `json:"events"`
	Cursor ChoreographyEventCursor `json:"cursor"`
	Head   ChoreographyEventCursor `json:"head"`
	More   bool                    `json:"more"`
}

type choreographyLedgerRecord struct {
	ChoreographyRunBinding
	Sequence      uint64                     `json:"sequence"`
	BootstrapOID  string                     `json:"bootstrapOid,omitempty"`
	Roster        *ChoreographyRuntimeRoster `json:"roster,omitempty"`
	Event         *ChoreographyEvent         `json:"event,omitempty"`
	Budget        string                     `json:"budget,omitempty"`
	Node          string                     `json:"node,omitempty"`
	CapacityDelta int                        `json:"capacityDelta,omitempty"`
}

type choreographyLedgerMutation struct {
	Identity          string
	ExpectedLedgerOID string
	TaskBranch        string
	OldTaskOID        string
	NewTaskOID        string
	SlotBranch        string
	OldSlotOID        string
	NewSlotOID        string
	Budget            string
	Node              string
	Delta             int
	Event             ChoreographyEvent
}

func (g *GitChoreographyPersist) pendingTaskOID(identity string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil && g.pending.mutation.Identity == identity {
		return g.pending.mutation.NewTaskOID
	}
	return choreographyLedgerMissingIdentity
}

func (g *GitChoreographyPersist) pendingClaimAssignmentOID(identity string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil && g.pending.mutation.Identity == identity &&
		g.pending.mutation.Event.Kind == ChoreographyClaimAccepted {
		return g.pending.mutation.Event.AssignmentOID
	}
	return choreographyLedgerMissingIdentity
}

type choreographyAtomicPush struct {
	mutation  choreographyLedgerMutation
	oldLedger string
	newLedger string
}

func (g *GitChoreographyPersist) persistAtomic(ctx context.Context, mutation choreographyLedgerMutation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil {
		mutation.Event.Sequence = g.pending.mutation.Event.Sequence
	}
	if g.pending != nil && !reflect.DeepEqual(g.pending.mutation, mutation) {
		return fmt.Errorf(choreographyLedgerPendingCASErr)
	}
	if g.pending == nil {
		prior := g.ledgerTip
		if err := g.syncLedgerLocked(ctx); err != nil {
			return err
		}
		if !g.initialized {
			return fmt.Errorf(choreographyLedgerInitPendingErr)
		}
		if g.sealed {
			return fmt.Errorf(choreographyLedgerRunSealedErr)
		}
		if g.stopping && (mutation.Event.Kind == ChoreographyClaimAccepted || mutation.Event.Kind == ChoreographyMemberJoined || mutation.Event.Kind == ChoreographyExecutionStarted) {
			return fmt.Errorf(choreographyLedgerAdmissionStoppedErr)
		}
		if !g.executionStarted && mutation.Event.Kind == ChoreographyClaimAccepted {
			return fmt.Errorf(choreographyExecutionNotStartedError)
		}
		if g.ledgerTip != prior {
			return fmt.Errorf(choreographyLedgerAdvancedErr)
		}
		if mutation.ExpectedLedgerOID != choreographyLedgerMissingIdentity && mutation.ExpectedLedgerOID != g.ledgerTip {
			return fmt.Errorf(choreographyLedgerStaleEpochErr)
		}
		mutation.Event.Sequence = g.sequence + choreographyLedgerFirstSequence
		record := choreographyLedgerRecord{ChoreographyRunBinding: g.opts.Binding,
			Sequence: mutation.Event.Sequence, Event: &mutation.Event,
			Budget: mutation.Budget, Node: mutation.Node, CapacityDelta: mutation.Delta}
		document, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if len(document) > returnReplyLimit {
			return fmt.Errorf(choreographyLedgerEventSizeErr)
		}
		parents := []string{g.ledgerTip, mutation.NewTaskOID}
		if mutation.NewSlotOID != choreographyLedgerMissingIdentity {
			parents = append(parents, mutation.NewSlotOID)
		} else if mutation.Event.Kind == ChoreographyFrameReady && mutation.Event.PreparedInputOID != choreographyLedgerMissingIdentity {
			parents = append(parents, mutation.Event.PreparedInputOID)
		}
		newLedger, err := g.opts.Mailbox.commitMessage(ctx, choreographyLedgerKind, document,
			parents, nil)
		if err != nil {
			return err
		}
		g.pending = &choreographyAtomicPush{mutation: mutation, oldLedger: g.ledgerTip, newLedger: newLedger}
	}
	pending := g.pending
	mailbox := g.opts.Mailbox
	refs := make([]gitx.BranchAdvance, choreographyLedgerNoItems, choreographyLedgerAtomicRefCapacity)
	if pending.mutation.TaskBranch != choreographyLedgerMissingIdentity {
		refs = append(refs, gitx.BranchAdvance{Branch: pending.mutation.TaskBranch,
			ExpectedOld: pending.mutation.OldTaskOID, OID: pending.mutation.NewTaskOID})
	}
	if pending.mutation.SlotBranch != choreographyLedgerMissingIdentity {
		refs = append(refs, gitx.BranchAdvance{Branch: pending.mutation.SlotBranch,
			ExpectedOld: pending.mutation.OldSlotOID, OID: pending.mutation.NewSlotOID})
	}
	refs = append(refs, gitx.BranchAdvance{Branch: g.opts.Ledger.Branch,
		ExpectedOld: pending.oldLedger, OID: pending.newLedger})
	for refusal := choreographyLedgerRefusalStartAttempt; ; refusal++ {
		pushErr := mailbox.remote.PushAtomicAdvances(ctx, mailbox.endpoint, refs)
		taskTip, taskReadErr := pending.mutation.NewTaskOID, error(nil)
		if pending.mutation.TaskBranch != choreographyLedgerMissingIdentity {
			taskTip, taskReadErr = mailbox.readRemoteTip(ctx, pending.mutation.TaskBranch)
		}
		slotTip, slotReadErr := pending.mutation.NewSlotOID, error(nil)
		if pending.mutation.SlotBranch != choreographyLedgerMissingIdentity {
			slotTip, slotReadErr = mailbox.readRemoteTip(ctx, pending.mutation.SlotBranch)
		}
		ledgerTip, ledgerReadErr := mailbox.readRemoteTip(ctx, g.opts.Ledger.Branch)
		if taskReadErr == nil && slotReadErr == nil && ledgerReadErr == nil &&
			taskTip == pending.mutation.NewTaskOID && slotTip == pending.mutation.NewSlotOID &&
			ledgerTip == pending.newLedger {
			break
		}
		unchanged := taskReadErr == nil && slotReadErr == nil && ledgerReadErr == nil &&
			(pending.mutation.TaskBranch == choreographyLedgerMissingIdentity || taskTip == pending.mutation.OldTaskOID) &&
			(pending.mutation.SlotBranch == choreographyLedgerMissingIdentity || slotTip == pending.mutation.OldSlotOID) &&
			ledgerTip == pending.oldLedger
		if errors.Is(pushErr, gitx.ErrRemoteRefused) && unchanged {
			if refusal < choreographyAtomicRefusalRetryCount && ctx.Err() == nil {
				continue
			}
			g.pending = nil
			return fmt.Errorf(choreographyAtomicRefusedErrorFormat,
				pending.mutation.TaskBranch, pending.mutation.SlotBranch, g.opts.Ledger.Branch,
				errors.Join(errChoreographyAtomicRefused, pushErr))
		}
		return fmt.Errorf(choreographyLedgerCASUnresolvedFormat,
			pending.mutation.TaskBranch, g.opts.Ledger.Branch,
			joinAtomicPushErrors(pushErr, taskReadErr, slotReadErr, ledgerReadErr))
	}
	if g.opts.OnRefAdvance != nil {
		if pending.mutation.TaskBranch != choreographyLedgerMissingIdentity {
			g.opts.OnRefAdvance(ctx, ChoreographyRefAdvance{Branch: pending.mutation.TaskBranch,
				OldOID: pending.mutation.OldTaskOID, NewOID: pending.mutation.NewTaskOID})
		}
		if pending.mutation.SlotBranch != choreographyLedgerMissingIdentity {
			g.opts.OnRefAdvance(ctx, ChoreographyRefAdvance{Branch: pending.mutation.SlotBranch,
				OldOID: pending.mutation.OldSlotOID, NewOID: pending.mutation.NewSlotOID})
		}
		g.opts.OnRefAdvance(ctx, ChoreographyRefAdvance{Branch: g.opts.Ledger.Branch,
			OldOID: pending.oldLedger, NewOID: pending.newLedger})
	}
	g.ledgerTip = pending.newLedger
	g.sequence++
	settledEvent := pending.mutation.Event
	settledEvent.LedgerOID = pending.newLedger
	g.events = append(g.events, settledEvent)
	g.eventOIDs = append(g.eventOIDs, pending.newLedger)
	g.eventIndex[pending.newLedger] = g.sequence
	g.indexTaskEventLocked(pending.mutation.Event)
	if pending.mutation.Event.Package != choreographyLedgerMissingIdentity && pending.mutation.Event.PackageOutcome != choreographyLedgerMissingIdentity &&
		(pending.mutation.Event.Kind == ChoreographyTerminal || pending.mutation.Event.Kind == ChoreographyBlocked || pending.mutation.Event.Kind == ChoreographyNoClaimPackageSettled || pending.mutation.Event.Kind == ChoreographyInertCancelSettled || pending.mutation.Event.Kind == ChoreographyTaskPackageSettled) {
		g.packageState[pending.mutation.Event.Package] = pending.mutation.Event.PackageOutcome
	}
	if pending.mutation.Event.Kind == ChoreographyRunSealed {
		g.sealed = choreographyLedgerSealed
	}
	if pending.mutation.Event.Kind == ChoreographyRunStopping || pending.mutation.Event.Kind == ChoreographyInputInvalidation {
		g.stopping = choreographyLedgerStopping
	}
	if pending.mutation.Event.Kind == ChoreographyExecutionStarted {
		g.executionStarted = choreographyLedgerExecutionStarted
		g.admissionDeadlineUTC = pending.mutation.Event.AdmissionDeadlineUTC
	}
	if pending.mutation.Event.Kind == ChoreographyClaimAccepted ||
		pending.mutation.Event.Kind == ChoreographyResultAccepted ||
		pending.mutation.Event.Kind == ChoreographyAckAccepted {
		g.accepted[pending.mutation.Event.ProposalDigest] = ChoreographyEventCursor{
			OID: pending.newLedger, Sequence: g.sequence}
	}
	if pending.mutation.Event.Kind == ChoreographyClaimRefused {
		g.refused[pending.mutation.Event.ProposalDigest] = ChoreographyEventCursor{
			OID: pending.newLedger, Sequence: g.sequence}
	}
	if pending.mutation.Event.Kind == ChoreographyMemberJoined {
		if g.joined == nil {
			g.joined = make(map[string]ChoreographyEventCursor)
		}
		g.joined[pending.mutation.Event.ProposalDigest] = ChoreographyEventCursor{OID: pending.newLedger, Sequence: g.sequence}
	}
	if pending.mutation.Event.Kind == ChoreographyMemberDraining {
		if g.drained == nil {
			g.drained = make(map[string]ChoreographyEventCursor)
		}
		g.drained[pending.mutation.Event.ProposalDigest] = ChoreographyEventCursor{OID: pending.newLedger, Sequence: g.sequence}
	}
	if pending.mutation.Event.Kind == ChoreographyMemberJoinRefused {
		if g.joinRefused == nil {
			g.joinRefused = make(map[string]ChoreographyEventCursor)
		}
		g.joinRefused[pending.mutation.Event.ProposalDigest] = ChoreographyEventCursor{OID: pending.newLedger, Sequence: g.sequence}
	}
	g.pending = nil
	return nil
}

// StopClaims durably ends admission before the first node begins cancelling
// active work. Existing Result, Ack, retry and finality transitions remain
// writable until every active attempt has settled and SealRun succeeds.
func (g *GitChoreographyPersist) StopClaims(ctx context.Context) (ChoreographyEventCursor, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyEventCursor{}, err
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	g.mu.Lock()
	stopping, sealed := g.stopping, g.sealed
	if stopping {
		for _, event := range g.events {
			if event.Kind == ChoreographyRunStopping {
				sealed = choreographyLedgerSealed
				break
			}
		}
	}
	g.mu.Unlock()
	if sealed {
		return head, nil
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: choreographyLedgerStopIdentityPrefix + head.OID, ExpectedLedgerOID: head.OID,
		NewTaskOID: g.opts.BootstrapOID,
		Event: ChoreographyEvent{Kind: ChoreographyRunStopping,
			Task: choreographyRunTaskID, TaskOID: g.opts.BootstrapOID},
	}); err != nil {
		return ChoreographyEventCursor{}, err
	}
	return g.LedgerHead(ctx)
}

func joinAtomicPushErrors(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return fmt.Errorf(choreographyLedgerRemoteRefsErr)
}

// PrepareChoreographyLedgerRoot writes only a local signed commit. The first
// node must create the returned ref atomically with the initial task batch
// and read back its exact OID before any claim can be admitted.
func PrepareChoreographyLedgerRoot(ctx context.Context,
	opts ChoreographyLedgerRootOptions) (ChoreographyLedgerReservation, error) {
	if opts.Mailbox == nil || !opts.Binding.valid() || opts.BootstrapOID == choreographyLedgerMissingIdentity {
		return ChoreographyLedgerReservation{}, fmt.Errorf(choreographyLedgerRootInvalidErr)
	}
	if err := opts.Roster.validate(); err != nil {
		return ChoreographyLedgerReservation{}, err
	}
	document, err := json.Marshal(choreographyLedgerRecord{
		ChoreographyRunBinding: opts.Binding, BootstrapOID: opts.BootstrapOID,
		Roster: &opts.Roster})
	if err != nil {
		return ChoreographyLedgerReservation{}, err
	}
	if len(document) > returnReplyLimit {
		return ChoreographyLedgerReservation{}, fmt.Errorf(choreographyLedgerRosterSizeErr)
	}
	oid, err := opts.Mailbox.commitMessage(ctx, choreographyLedgerKind, document,
		[]string{opts.BootstrapOID}, nil)
	if err != nil {
		return ChoreographyLedgerReservation{}, err
	}
	return ChoreographyLedgerReservation{Branch: FormatChoreographyLedgerBranch(opts.Binding.Run), OID: oid}, nil
}

// ReadChoreographyLedgerRoot authenticates the one runtime roster retained
// beneath the canonical ledger ref. A cold peer may call it after verifying
// the task reservation's bootstrap OID; it never consults ambient config.
func ReadChoreographyLedgerRoot(ctx context.Context, mailbox *GitMailbox,
	binding ChoreographyRunBinding, bootstrapOID string) (ChoreographyLedgerReservation, ChoreographyRuntimeRoster, error) {
	if mailbox == nil || !binding.valid() || bootstrapOID == choreographyLedgerMissingIdentity {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{},
			fmt.Errorf(choreographyLedgerRootReadErr)
	}
	branch := FormatChoreographyLedgerBranch(binding.Run)
	tip, err := mailbox.readRemoteTip(ctx, branch)
	if err != nil {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{}, err
	}
	if tip == choreographyLedgerMissingIdentity {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{},
			fmt.Errorf(choreographyLedgerRefAbsentErr)
	}
	if err := mailbox.fetchRefs(ctx, []string{branch}); err != nil {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{}, err
	}
	oids, err := mailbox.plumbing.FirstParentOIDs(ctx, tip, bootstrapOID)
	if err != nil || len(oids) == choreographyLedgerNoItems {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{},
			fmt.Errorf(choreographyLedgerBootstrapRootErr)
	}
	rootOID := oids[choreographyLedgerFirstItemIndex]
	root, err := readChoreographyLedgerRecord(ctx, mailbox, rootOID, binding)
	if err != nil || root.Sequence != choreographyLedgerRootSequence || root.Event != nil ||
		root.BootstrapOID != bootstrapOID ||
		root.Roster == nil || root.Roster.validate() != nil {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{},
			fmt.Errorf(choreographyLedgerSignedRootErr)
	}
	parents, err := mailbox.plumbing.CommitParents(ctx, rootOID)
	if err != nil || len(parents) != choreographyLedgerSingleItemCount ||
		parents[choreographyLedgerFirstItemIndex] != bootstrapOID {
		return ChoreographyLedgerReservation{}, ChoreographyRuntimeRoster{},
			fmt.Errorf(choreographyLedgerRootParentErr)
	}
	return ChoreographyLedgerReservation{Branch: branch, OID: rootOID}, *root.Roster, nil
}

// ReadEvents pages an indexed signed ledger. A fresh first node replays the
// retained first-parent history once; later pages and accepted CAS commits
// extend or read that index without traversing prior history again.
func (g *GitChoreographyPersist) ReadEvents(ctx context.Context,
	cursor ChoreographyEventCursor, maxEvents int) (ChoreographyEventPage, error) {
	if maxEvents < choreographyLedgerPageMinEvents || maxEvents > choreographyLedgerPageMaxEvents ||
		(cursor.OID == choreographyLedgerMissingIdentity && cursor.Sequence != choreographyLedgerRootSequence) {
		return ChoreographyEventPage{}, fmt.Errorf(choreographyLedgerCursorInvalidErr)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return ChoreographyEventPage{}, err
	}
	if cursor.OID == choreographyLedgerMissingIdentity {
		cursor.OID = g.opts.Ledger.OID
	}
	position, found := g.eventIndex[cursor.OID]
	if !found || position != cursor.Sequence {
		return ChoreographyEventPage{}, fmt.Errorf(choreographyLedgerCursorOutsideErr)
	}
	end := min(int(position)+maxEvents, len(g.events))
	page := ChoreographyEventPage{Events: append([]ChoreographyEvent(nil), g.events[position:end]...),
		Cursor: cursor, Head: ChoreographyEventCursor{OID: g.ledgerTip, Sequence: g.sequence},
		More: end < len(g.events)}
	if end > int(position) {
		for index := range page.Events {
			page.Events[index].LedgerOID = g.eventOIDs[int(position)+index]
		}
		page.Cursor = ChoreographyEventCursor{OID: g.eventOIDs[end-choreographyLedgerSingleItemCount], Sequence: uint64(end)}
	}
	return page, nil
}

// LedgerHead is the exact signed capacity epoch used by a claim proposal.
// A proposal naming an older epoch cannot become accepted after another
// durable ledger transition, including after an ambiguous HTTP response.
func (g *GitChoreographyPersist) LedgerHead(ctx context.Context) (ChoreographyEventCursor, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return ChoreographyEventCursor{}, err
	}
	return ChoreographyEventCursor{OID: g.ledgerTip, Sequence: g.sequence}, nil
}

// SealRun writes a signed retained ledger fence before the first node ends
// its return service. An old proposal names an earlier ledger epoch and can
// never acquire a task afterward. The ledger ref remains readable for cold
// peers and exact receipt reconciliation; cleanup of it is a separate gate.
func (g *GitChoreographyPersist) SealRun(ctx context.Context, abort bool) (ChoreographyEventCursor, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyEventCursor{}, err
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	g.mu.Lock()
	sealed, stopping := g.sealed, g.stopping
	g.mu.Unlock()
	if sealed {
		return head, nil
	}
	if !stopping {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyLedgerStopBeforeSealErr)
	}
	g.mu.Lock()
	active := make(map[string]bool, len(g.tasks))
	done := make(map[string]bool, len(g.tasks)+len(g.localTasks))
	pendingTaskPackages := make(map[string]bool, len(g.tasks))
	for _, event := range g.events {
		switch event.Kind {
		case ChoreographyClaimAccepted:
			active[event.Task] = choreographyLedgerActiveClaim
		case ChoreographyRetryReady:
			active[event.Task] = choreographyLedgerInactiveClaim
		case ChoreographyTerminal, ChoreographyBlocked:
			active[event.Task] = choreographyLedgerInactiveClaim
			done[event.Task] = choreographyLedgerFinishedTask
			if event.Kind == ChoreographyTerminal && event.ResultReceiptOID != choreographyLedgerMissingIdentity &&
				event.Package != choreographyLedgerMissingIdentity && event.PackageOutcome == choreographyLedgerMissingIdentity {
				pendingTaskPackages[event.Task] = choreographyLedgerActiveClaim
			}
		case ChoreographyTaskPackageSettled:
			pendingTaskPackages[event.Task] = choreographyLedgerInactiveClaim
		}
	}
	for task := range active {
		if active[task] {
			g.mu.Unlock()
			return ChoreographyEventCursor{}, fmt.Errorf(choreographyLedgerActiveClaimSealErr)
		}
	}
	if !abort {
		for _, pending := range pendingTaskPackages {
			if pending {
				g.mu.Unlock()
				return ChoreographyEventCursor{}, fmt.Errorf(choreographyLedgerUnsettledTaskPackageSealErr)
			}
		}
		for task := range g.tasks {
			if !done[task] {
				g.mu.Unlock()
				return ChoreographyEventCursor{}, fmt.Errorf(choreographyLedgerUnfinishedTaskSealErr)
			}
		}
		for task := range g.localTasks {
			if !done[task] {
				g.mu.Unlock()
				return ChoreographyEventCursor{}, fmt.Errorf(choreographyLedgerUnfinishedEffectSealErr)
			}
		}
	}
	g.mu.Unlock()
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: choreographyLedgerSealIdentityPrefix + head.OID, ExpectedLedgerOID: head.OID,
		NewTaskOID: g.opts.BootstrapOID,
		Event: ChoreographyEvent{Kind: ChoreographyRunSealed, Task: choreographyRunTaskID,
			TaskOID: g.opts.BootstrapOID, Aborted: abort},
	}); err != nil {
		return ChoreographyEventCursor{}, err
	}
	return g.LedgerHead(ctx)
}

func (g *GitChoreographyPersist) syncLedgerLocked(ctx context.Context) error {
	mailbox := g.opts.Mailbox
	tip, err := mailbox.readRemoteTip(ctx, g.opts.Ledger.Branch)
	if err != nil {
		return err
	}
	if tip == choreographyLedgerMissingIdentity {
		return fmt.Errorf(choreographyLedgerRefAbsentErr)
	}
	if tip == g.ledgerTip && g.eventIndex != nil {
		return nil
	}
	if err := mailbox.fetchRefs(ctx, []string{g.opts.Ledger.Branch}); err != nil {
		return err
	}
	root, err := g.readLedgerRecord(ctx, g.opts.Ledger.OID)
	if err != nil || root.Sequence != choreographyLedgerRootSequence || root.Event != nil ||
		root.BootstrapOID != g.opts.BootstrapOID ||
		root.Roster == nil || root.Roster.validate() != nil {
		return fmt.Errorf(choreographyLedgerSignedRootErr)
	}
	rootParents, err := mailbox.plumbing.CommitParents(ctx, g.opts.Ledger.OID)
	if err != nil || len(rootParents) != choreographyLedgerSingleItemCount ||
		rootParents[choreographyLedgerFirstItemIndex] != g.opts.BootstrapOID {
		return fmt.Errorf(choreographyLedgerRootParentErr)
	}
	oids, err := mailbox.plumbing.FirstParentOIDs(ctx, tip, g.ledgerTip)
	if err != nil {
		return err
	}
	previous := g.ledgerTip
	for _, oid := range oids {
		record, err := g.readLedgerRecord(ctx, oid)
		if err != nil || record.Sequence != g.sequence+choreographyLedgerFirstSequence || record.Event == nil ||
			record.Event.Sequence != record.Sequence || record.Event.Task == choreographyLedgerMissingIdentity || record.Event.TaskOID == choreographyLedgerMissingIdentity {
			return fmt.Errorf(choreographyLedgerEventInvalidErr)
		}
		if g.sequence == choreographyLedgerRootSequence {
			if oid != g.opts.InitializationCompleteOID ||
				record.Event.Kind != ChoreographyInitializationComplete ||
				record.Event.Task != choreographyRunTaskID ||
				record.Event.TaskOID != g.opts.GraphRootOID ||
				record.Event.GraphRootOID != g.opts.GraphRootOID {
				return fmt.Errorf(choreographyLedgerInitFirstErr)
			}
		} else if record.Event.Kind == ChoreographyInitializationComplete {
			return fmt.Errorf(choreographyLedgerInitRepeatedErr)
		}
		parents, err := mailbox.plumbing.CommitParents(ctx, oid)
		wantParents := choreographyLedgerTwoParentCount
		if record.Event.Kind == ChoreographyAssignmentOffered && record.Event.SlotOID != choreographyLedgerMissingIdentity {
			wantParents = choreographyLedgerThreeParentCount
		} else if record.Event.Kind == ChoreographyFrameReady && record.Event.PreparedInputOID != choreographyLedgerMissingIdentity {
			wantParents = choreographyLedgerThreeParentCount
		}
		if err != nil || len(parents) != wantParents ||
			parents[choreographyLedgerFirstItemIndex] != previous ||
			parents[choreographyLedgerSecondItemIndex] != record.Event.TaskOID ||
			wantParents == choreographyLedgerThreeParentCount &&
				record.Event.Kind == ChoreographyAssignmentOffered &&
				parents[choreographyLedgerThirdItemIndex] != record.Event.SlotOID ||
			wantParents == choreographyLedgerThreeParentCount &&
				record.Event.Kind == ChoreographyFrameReady &&
				parents[choreographyLedgerThirdItemIndex] != record.Event.PreparedInputOID {
			return fmt.Errorf(choreographyLedgerEventParentErr)
		}
		if record.Event.Kind == ChoreographyBlocked {
			blockedParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.TaskOID)
			if err != nil || record.Event.PriorTaskOID == choreographyLedgerMissingIdentity &&
				len(blockedParents) != choreographyLedgerNoItems ||
				record.Event.PriorTaskOID != choreographyLedgerMissingIdentity &&
					(len(blockedParents) != choreographyLedgerSingleItemCount ||
						blockedParents[choreographyLedgerFirstItemIndex] != record.Event.PriorTaskOID) {
				return fmt.Errorf(choreographyLedgerBlockedParentErr)
			}
			if err := g.verifyBlockedEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyNoClaimExpired {
			if !g.executionStarted || record.Event.AdmissionDeadlineUTC != g.admissionDeadlineUTC {
				return fmt.Errorf(choreographyExecutionNotStartedError)
			}
			if err := g.verifyNoClaimEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyFrameReady {
			spec := g.tasks[record.Event.Task]
			if spec.Task == choreographyLedgerMissingIdentity || record.Event.TaskOID == choreographyLedgerMissingIdentity ||
				record.Event.GraphRootOID != g.opts.GraphRootOID ||
				record.Event.PriorTaskOID != record.Event.TaskOID ||
				record.Event.InputsDigest == choreographyLedgerMissingIdentity ||
				spec.NeedsPreparedInput && record.Event.PreparedInputOID == choreographyLedgerMissingIdentity {
				return fmt.Errorf(choreographyReadyFrameReplayMismatchFormat, record.Event.Task,
					spec.NeedsPreparedInput, record.Event.PreparedInputOID,
					record.Event.TaskOID, record.Event.PriorTaskOID)
			}
			if spec.NeedsPreparedInput {
				carrier, err := mailbox.ReadChoreographyPreparedCarrier(ctx,
					record.Event.PreparedInputOID, returnReplyLimit)
				if err != nil {
					return err
				}
				if carrier.ChoreographyRunBinding != g.opts.Binding ||
					carrier.GraphRootOID != g.opts.GraphRootOID ||
					carrier.Task != spec.Task || carrier.TemplateDigest != spec.TemplateDigest ||
					!slices.Equal(carrier.PredecessorResultOIDs, carrier.Bindings.PredecessorResultOIDs) {
					return fmt.Errorf(choreographyPreparedCarrierReplayMismatchFormat, record.Event.Task,
						carrier.ChoreographyRunBinding == g.opts.Binding, carrier.Task,
						carrier.GraphRootOID, g.opts.GraphRootOID,
						carrier.TemplateDigest, spec.TemplateDigest,
						carrier.PredecessorResultOIDs, carrier.Bindings.PredecessorResultOIDs)
				}
			}
		}
		if record.Event.Kind == ChoreographyNoClaimPackageSettled {
			if err := g.verifyNoClaimPackageSettlementEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyInertCancelled {
			if err := g.verifyInertCancelEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyInertCancelSettled {
			if err := g.verifyInertCancelSettlementEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyTaskPackageSettled {
			if err := g.verifyTaskPackageSettlementEvent(ctx, *record.Event); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyMemberJoined {
			if err := g.verifyJoinedMemberEvent(ctx, *record.Event, previous); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyMemberDraining {
			if err := g.verifyDrainingMemberEvent(ctx, *record.Event, previous); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyAssignmentOffered {
			assignmentParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.TaskOID)
			if err != nil || len(assignmentParents) == choreographyLedgerNoItems ||
				assignmentParents[choreographyLedgerFirstItemIndex] != record.Event.PriorTaskOID {
				return fmt.Errorf(choreographyLedgerOfferedParentErr)
			}
			if record.Event.SlotOID != choreographyLedgerMissingIdentity {
				slotParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.SlotOID)
				if err != nil || len(slotParents) != choreographyLedgerSingleItemCount ||
					slotParents[choreographyLedgerFirstItemIndex] != record.Event.PriorSlotOID ||
					record.Event.SlotBranch == choreographyLedgerMissingIdentity {
					return fmt.Errorf(choreographyLedgerSlotParentErr)
				}
			} else if record.Event.SlotBranch != choreographyLedgerMissingIdentity || record.Event.PriorSlotOID != choreographyLedgerMissingIdentity {
				return fmt.Errorf(choreographyLedgerSlotIncompleteErr)
			}
		}
		if record.Event.Kind == ChoreographyClaimAccepted {
			if !g.executionStarted {
				return fmt.Errorf(choreographyExecutionNotStartedError)
			}
			if record.Event.AssignmentOID == choreographyLedgerMissingIdentity {
				return fmt.Errorf(choreographyLedgerClaimAssignmentAbsentErr)
			}
			claimParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.TaskOID)
			if err != nil || len(claimParents) != choreographyLedgerSingleItemCount ||
				claimParents[choreographyLedgerFirstItemIndex] != record.Event.AssignmentOID ||
				record.Event.GraphRootOID != g.opts.GraphRootOID {
				return fmt.Errorf(choreographyLedgerClaimAssignmentRootErr)
			}
			assignmentParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.AssignmentOID)
			if err != nil || len(assignmentParents) == choreographyLedgerNoItems ||
				assignmentParents[choreographyLedgerFirstItemIndex] != record.Event.PriorTaskOID {
				return fmt.Errorf(choreographyLedgerAssignmentParentErr)
			}
			assignment, err := mailbox.OpenCarriedAssignment(ctx, record.Event.AssignmentOID, returnReplyLimit)
			if err != nil {
				return err
			}
			if assignment == nil || assignment.Assignment.TemplateDigest == choreographyLedgerMissingIdentity ||
				assignment.Assignment.GraphDigest != g.opts.Binding.GraphDigest ||
				assignment.Assignment.GraphRootOID != g.opts.GraphRootOID ||
				assignment.Assignment.InputsDigest != record.Event.InputsDigest {
				return fmt.Errorf(choreographyLedgerAssignmentGraphErr)
			}
			if err := g.verifyAcceptedClaimEvent(ctx, *record.Event, previous); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyResultAccepted {
			if _, err := mailbox.ReadChoreographyAcceptedResult(ctx, g.opts.Binding,
				*record.Event, g.opts.MaxPackBytes); err != nil {
				return err
			}
		}
		if record.Event.Kind == ChoreographyTerminal &&
			record.Event.ResultReceiptOID != choreographyLedgerMissingIdentity {
			if record.Event.OutputOID != choreographyLedgerMissingIdentity {
				if _, err := mailbox.ReadChoreographyCompletedResult(ctx,
					ChoreographyCompletedResultQuery{Binding: g.opts.Binding,
						Task: record.Event.Task, Event: *record.Event}, g.opts.MaxPackBytes); err != nil {
					return err
				}
			} else {
				if _, err := mailbox.exactChoreographyOutputEntry(ctx,
					record.Event.ResultReceiptOID, Result{}); err != nil {
					return err
				}
				if _, err := mailbox.exactChoreographyOutputEntry(ctx,
					record.Event.TaskOID, Result{}); err != nil {
					return err
				}
			}
		}
		if record.Event.Kind == ChoreographyCancelIssued || record.Event.Kind == ChoreographyQueueFenced ||
			(record.Event.Kind == ChoreographyRetryReady ||
				record.Event.Kind == ChoreographyTerminal && g.localTasks[record.Event.Task].Task == choreographyLedgerMissingIdentity) &&
				record.Event.ResultReceiptOID == choreographyLedgerMissingIdentity && record.Event.PriorTaskOID != choreographyLedgerMissingIdentity {
			parents, err := mailbox.plumbing.CommitParents(ctx, record.Event.TaskOID)
			if err != nil || len(parents) != choreographyLedgerSingleItemCount ||
				parents[choreographyLedgerFirstItemIndex] != record.Event.PriorTaskOID {
				return fmt.Errorf(choreographyLedgerDecisionParentErr)
			}
			tree, err := mailbox.readTree(ctx, record.Event.TaskOID)
			if err != nil {
				return err
			}
			data, err := mailbox.Read(ctx, ChainTip{OID: record.Event.TaskOID,
				Kind: tree.kind, document: tree.document, signature: tree.signature}, returnReplyLimit)
			if err != nil {
				return err
			}
			if record.Event.Kind == ChoreographyCancelIssued || record.Event.Kind == ChoreographyQueueFenced {
				var cancellation Withdrawal
				if tree.kind != MessageCancel || strictJSON(data, &cancellation) != nil ||
					cancellation.Run != g.opts.Binding.Run || cancellation.Generation != g.opts.Binding.Generation ||
					cancellation.PlanDigest != g.opts.Binding.PlanDigest ||
					cancellation.Task != record.Event.Task || cancellation.Attempt != record.Event.Attempt ||
					cancellation.Node != record.Event.Node || cancellation.Tip != record.Event.PriorTaskOID ||
					cancellation.Assignment == choreographyLedgerMissingIdentity {
					return fmt.Errorf(choreographyLedgerCancelEventErr)
				}
				claimParents, err := mailbox.plumbing.CommitParents(ctx, record.Event.PriorTaskOID)
				if record.Event.Kind == ChoreographyCancelIssued &&
					(err != nil || len(claimParents) != choreographyLedgerSingleItemCount ||
						claimParents[choreographyLedgerFirstItemIndex] != cancellation.Assignment) ||
					record.Event.Kind == ChoreographyQueueFenced &&
						cancellation.Assignment != record.Event.PriorTaskOID {
					return fmt.Errorf(choreographyLedgerCancelAssignmentErr)
				}
			} else {
				var queue ChoreographyDecision
				if tree.kind != choreographyDecisionKind || strictJSON(data, &queue) != nil ||
					queue.ChoreographyRunBinding != g.opts.Binding || !queue.Unclaimed ||
					queue.Task != record.Event.Task || queue.Attempt != record.Event.Attempt ||
					queue.Node != record.Event.Node || queue.ResultOID != record.Event.PriorTaskOID ||
					queue.Retry != (record.Event.Kind == ChoreographyRetryReady) || queue.Succeeded ||
					queue.PackageOutcome != record.Event.PackageOutcome {
					return fmt.Errorf(choreographyLedgerQueueEventErr)
				}
				assignmentOID := queue.ResultOID
				if !queue.Retry {
					assignmentOID = queue.OfferedOID
					fenceParents, err := mailbox.plumbing.CommitParents(ctx, queue.ResultOID)
					if err != nil || len(fenceParents) != choreographyLedgerSingleItemCount ||
						fenceParents[choreographyLedgerFirstItemIndex] != assignmentOID {
						return fmt.Errorf(choreographyLedgerQueueFenceErr)
					}
				} else if queue.OfferedOID != choreographyLedgerMissingIdentity {
					return fmt.Errorf(choreographyLedgerQueueRetryFenceErr)
				}
				if _, err := mailbox.OpenCarriedAssignment(ctx, assignmentOID, returnReplyLimit); err != nil {
					return fmt.Errorf(choreographyLedgerQueueAssignmentFormat, err)
				}
			}
		}
		if record.Event.Kind == ChoreographyTerminal && g.localTasks[record.Event.Task].Task != choreographyLedgerMissingIdentity {
			if err := g.verifyLocalOutcome(ctx, *record.Event); err != nil {
				return err
			}
			if _, err := mailbox.exactChoreographyOutputEntry(ctx,
				record.Event.TaskOID, Result{}); err != nil {
				return err
			}
		}
		settledEvent := *record.Event
		settledEvent.LedgerOID = oid
		g.events = append(g.events, settledEvent)
		g.eventOIDs = append(g.eventOIDs, oid)
		g.sequence++
		g.eventIndex[oid] = g.sequence
		g.indexTaskEventLocked(*record.Event)
		if record.Event.Package != choreographyLedgerMissingIdentity && record.Event.PackageOutcome != choreographyLedgerMissingIdentity &&
			(record.Event.Kind == ChoreographyTerminal || record.Event.Kind == ChoreographyBlocked || record.Event.Kind == ChoreographyNoClaimPackageSettled || record.Event.Kind == ChoreographyInertCancelSettled || record.Event.Kind == ChoreographyTaskPackageSettled) {
			g.packageState[record.Event.Package] = record.Event.PackageOutcome
		}
		if record.Event.Kind == ChoreographyRunSealed {
			g.sealed = choreographyLedgerSealed
		}
		if record.Event.Kind == ChoreographyRunStopping || record.Event.Kind == ChoreographyInputInvalidation {
			g.stopping = choreographyLedgerStopping
		}
		if record.Event.Kind == ChoreographyExecutionStarted {
			deadline, deadlineErr := choreographyExecutionDeadline(g.opts.JoinPolicy, record.Event.ExecutionStartedUTC)
			if g.executionStarted || record.Event.Task != choreographyRunTaskID ||
				record.Event.TaskOID != g.opts.GraphRootOID ||
				record.Event.GraphRootOID != g.opts.GraphRootOID || g.stopping ||
				deadlineErr != nil || record.Event.AdmissionDeadlineUTC != deadline {
				return fmt.Errorf(choreographyInvalidExecutionStartError)
			}
			g.executionStarted = choreographyLedgerExecutionStarted
			g.admissionDeadlineUTC = deadline
		}
		if record.Event.Kind == ChoreographyInitializationComplete {
			g.initialized = choreographyLedgerInitialized
		}
		if record.Event.Kind == ChoreographyClaimAccepted ||
			record.Event.Kind == ChoreographyResultAccepted ||
			record.Event.Kind == ChoreographyAckAccepted {
			g.accepted[record.Event.ProposalDigest] = ChoreographyEventCursor{
				OID: oid, Sequence: g.sequence}
		}
		if record.Event.Kind == ChoreographyClaimRefused {
			g.refused[record.Event.ProposalDigest] = ChoreographyEventCursor{
				OID: oid, Sequence: g.sequence}
		}
		if record.Event.Kind == ChoreographyMemberJoined {
			if g.joined == nil {
				g.joined = make(map[string]ChoreographyEventCursor)
			}
			g.joined[record.Event.ProposalDigest] = ChoreographyEventCursor{OID: oid, Sequence: g.sequence}
		}
		if record.Event.Kind == ChoreographyMemberDraining {
			if g.drained == nil {
				g.drained = make(map[string]ChoreographyEventCursor)
			}
			g.drained[record.Event.ProposalDigest] = ChoreographyEventCursor{OID: oid, Sequence: g.sequence}
		}
		if record.Event.Kind == ChoreographyMemberJoinRefused {
			if g.joinRefused == nil {
				g.joinRefused = make(map[string]ChoreographyEventCursor)
			}
			g.joinRefused[record.Event.ProposalDigest] = ChoreographyEventCursor{OID: oid, Sequence: g.sequence}
		}
		previous = oid
	}
	if previous != tip {
		return fmt.Errorf(choreographyLedgerHistoryErr)
	}
	if len(oids) > choreographyLedgerNoItems {
		g.replayPasses++
	}
	g.ledgerTip = tip
	return nil
}

func (g *GitChoreographyPersist) readLedgerRecord(ctx context.Context, oid string) (choreographyLedgerRecord, error) {
	return readChoreographyLedgerRecord(ctx, g.opts.Mailbox, oid, g.opts.Binding)
}

func readChoreographyLedgerRecord(ctx context.Context, mailbox *GitMailbox,
	oid string, binding ChoreographyRunBinding) (choreographyLedgerRecord, error) {
	tree, err := mailbox.readTree(ctx, oid)
	if err != nil || tree.kind != choreographyLedgerKind {
		return choreographyLedgerRecord{}, fmt.Errorf(choreographyLedgerUnsignedObjectErr)
	}
	document, err := mailbox.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return choreographyLedgerRecord{}, err
	}
	var record choreographyLedgerRecord
	if err := strictJSON(document, &record); err != nil || record.ChoreographyRunBinding != binding {
		return choreographyLedgerRecord{}, fmt.Errorf(choreographyLedgerAnotherRunErr)
	}
	return record, nil
}
