// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"reflect"
)

const choreographyNativeFinalityMissingIdentity = ""
const choreographyNativeFinalityFirstAttempt = minimumCompiledAttempt
const choreographyNativeFinalityLeaseWeight = 1
const choreographyNativeFinalityLedgerStartSequence = 0
const choreographyNativeFinalityLedgerIndexOffset = 1
const choreographyNativeFinalityFirstParentIndex = 0
const choreographyNativeFinalityNoParentCount = 0
const choreographyNativeFinalitySingleParentCount = 1
const choreographyNativeFinalityResultParentCount = 2
const choreographyNativeFinalityLocalAuthority = true
const choreographyNativeLocatorMismatchErrorMessage = "execution: native finality differs from its retained slot locator"
const choreographyNativeLedgerMismatchErrorMessage = "execution: native finality is not in the signed ledger"

// VerifyNativeTaskFinality checks one retained first-node native task result
// against its pre-effect slot locator. A ledger field match alone never frees
// a physical slot: the exact signed Blocked or effect/attempt chain must also
// be present in the canonical Git object closure.
func (f *RetainedChoreographyFeed) VerifyNativeTaskFinality(ctx context.Context,
	taskID string, locator NodeSlotNativeRecoveryLocator, event ChoreographyEvent) error {
	if f == nil || f.reader == nil || taskID == choreographyNativeFinalityMissingIdentity ||
		(event.Kind != ChoreographyTerminal && event.Kind != ChoreographyBlocked) ||
		event.Task != taskID || event.Attempt != choreographyNativeFinalityFirstAttempt ||
		event.TaskOID == choreographyNativeFinalityMissingIdentity ||
		locator.Binding != f.reader.opts.Binding ||
		locator.BootstrapOID != f.reader.opts.BootstrapOID ||
		locator.LedgerRootOID != f.reader.opts.Ledger.OID ||
		locator.CanonicalEndpoint != f.reader.opts.Mailbox.endpoint ||
		!validNodeSlotNativeRecovery(NodeSlotIntent{Run: locator.Binding.Run,
			Task: taskID, Attempt: choreographyNativeFinalityFirstAttempt,
			Weight: choreographyNativeFinalityLeaseWeight, NativeRecovery: &locator}) {
		return fmt.Errorf(choreographyNativeLocatorMismatchErrorMessage)
	}
	g := f.reader
	g.mu.Lock()
	err := g.syncLedgerLocked(ctx)
	if err == nil && (event.Sequence == choreographyNativeFinalityLedgerStartSequence ||
		event.Sequence > uint64(len(g.events)) ||
		!reflect.DeepEqual(g.events[event.Sequence-choreographyNativeFinalityLedgerIndexOffset], event)) {
		err = fmt.Errorf(choreographyNativeLedgerMismatchErrorMessage)
	}
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if event.Kind == ChoreographyBlocked {
		return f.verifyNativeBlocked(ctx, taskID, locator, event)
	}
	if locator.TaskBranch == choreographyNativeFinalityMissingIdentity {
		if event.Node != locator.Node || event.ResultReceiptOID != choreographyNativeFinalityMissingIdentity ||
			event.Package == choreographyNativeFinalityMissingIdentity {
			return &Rejection{Reason: ReasonChain}
		}
		return verifyChoreographyLocalOutcome(ctx, choreographyLocalOutcomeProof{
			Mailbox: g.opts.Mailbox, Binding: locator.Binding, Event: event,
			Task: ChoreographyTaskSpec{Task: taskID, Package: event.Package,
				Kind: locator.Kind, LocalAuthority: choreographyNativeFinalityLocalAuthority}})
	}
	if event.Node != locator.Node {
		return &Rejection{Reason: ReasonChain}
	}
	m := g.opts.Mailbox
	if event.ResultReceiptOID == choreographyNativeFinalityMissingIdentity {
		effectParents, err := m.plumbing.CommitParents(ctx, event.TaskOID)
		if err != nil || len(effectParents) != choreographyNativeFinalitySingleParentCount ||
			effectParents[choreographyNativeFinalityFirstParentIndex] != event.PriorTaskOID {
			return &Rejection{Reason: ReasonChain}
		}
		claimParents, err := m.plumbing.CommitParents(ctx, event.PriorTaskOID)
		if err != nil || len(claimParents) != choreographyNativeFinalitySingleParentCount {
			return &Rejection{Reason: ReasonChain}
		}
		claimTree, err := m.readTree(ctx, event.PriorTaskOID)
		if err != nil || claimTree.kind != choreographyClaimKind {
			return &Rejection{Reason: ReasonChain}
		}
		claimDocument, err := m.Read(ctx, ChainTip{OID: event.PriorTaskOID,
			Kind: claimTree.kind, document: claimTree.document,
			signature: claimTree.signature}, f.maxDocumentBytes)
		if err != nil {
			return err
		}
		var claim ChoreographyClaimProposal
		if strictJSON(claimDocument, &claim) != nil ||
			claim.ChoreographyRunBinding != locator.Binding || claim.Task != taskID ||
			claim.Node != locator.Node || claim.Branch != locator.TaskBranch ||
			claim.GraphRootOID != g.opts.GraphRootOID ||
			claim.ExpectedOID != locator.ReservationOID {
			return &Rejection{Reason: ReasonChain}
		}
		assignmentOID := claimParents[choreographyNativeFinalityFirstParentIndex]
		assignmentParents, err := m.plumbing.CommitParents(ctx, assignmentOID)
		if err != nil || len(assignmentParents) == choreographyNativeFinalityNoParentCount ||
			assignmentParents[choreographyNativeFinalityFirstParentIndex] != locator.ReservationOID {
			return &Rejection{Reason: ReasonChain}
		}
		view, err := m.OpenCarriedAssignment(ctx, assignmentOID, f.maxDocumentBytes)
		if err != nil || view == nil || view.Assignment.Task != taskID ||
			view.Assignment.Kind != locator.Kind || view.Assignment.Node != locator.Node ||
			view.Assignment.GraphRootOID != g.opts.GraphRootOID ||
			view.Assignment.BootstrapOID != locator.BootstrapOID ||
			view.Assignment.Branch != locator.TaskBranch {
			return &Rejection{Reason: ReasonChain}
		}
		return verifyChoreographyLocalOutcome(ctx, choreographyLocalOutcomeProof{
			Mailbox: m, Binding: locator.Binding, Event: event,
			Task: ChoreographyTaskSpec{Task: taskID, Package: event.Package,
				Kind: locator.Kind, LocalAuthority: choreographyNativeFinalityLocalAuthority}})
	}
	receiptParents, err := m.plumbing.CommitParents(ctx, event.ResultReceiptOID)
	if err != nil {
		return &Rejection{Reason: ReasonChain}
	}
	receiptTree, err := m.readTree(ctx, event.ResultReceiptOID)
	if err != nil {
		return err
	}
	claimOID := choreographyNativeFinalityMissingIdentity
	switch receiptTree.kind {
	case choreographyResultKind:
		if len(receiptParents) != choreographyNativeFinalityResultParentCount ||
			receiptParents[choreographyNativeFinalityFirstParentIndex] == choreographyNativeFinalityMissingIdentity {
			return &Rejection{Reason: ReasonChain}
		}
		claimOID = receiptParents[choreographyNativeFinalityFirstParentIndex]
	case MessageAck:
		if len(receiptParents) != choreographyNativeFinalitySingleParentCount ||
			event.OutputOID != choreographyNativeFinalityMissingIdentity {
			return &Rejection{Reason: ReasonChain}
		}
		cancelParents, err := m.plumbing.CommitParents(ctx,
			receiptParents[choreographyNativeFinalityFirstParentIndex])
		if err != nil || len(cancelParents) != choreographyNativeFinalitySingleParentCount {
			return &Rejection{Reason: ReasonChain}
		}
		claimOID = cancelParents[choreographyNativeFinalityFirstParentIndex]
	default:
		return &Rejection{Reason: ReasonChain}
	}
	claimParents, err := m.plumbing.CommitParents(ctx, claimOID)
	if err != nil || len(claimParents) != choreographyNativeFinalitySingleParentCount ||
		claimParents[choreographyNativeFinalityFirstParentIndex] == choreographyNativeFinalityMissingIdentity {
		return &Rejection{Reason: ReasonChain}
	}
	assignmentOID := claimParents[choreographyNativeFinalityFirstParentIndex]
	assignmentParents, err := m.plumbing.CommitParents(ctx, assignmentOID)
	if err != nil || len(assignmentParents) == choreographyNativeFinalityNoParentCount ||
		assignmentParents[choreographyNativeFinalityFirstParentIndex] != locator.ReservationOID {
		return &Rejection{Reason: ReasonChain}
	}
	view, err := m.OpenCarriedAssignment(ctx, assignmentOID, f.maxDocumentBytes)
	if err != nil || view == nil {
		return &Rejection{Reason: ReasonChain}
	}
	a := view.Assignment
	if a.Protocol != ChoreographyProtocolVersion || a.Kind != locator.Kind ||
		a.Task != taskID || a.Attempt != choreographyNativeFinalityFirstAttempt ||
		a.Node != locator.Node ||
		a.Branch != locator.TaskBranch || a.BootstrapOID != locator.BootstrapOID ||
		a.Run != locator.Binding.Run || a.Generation != locator.Binding.Generation ||
		a.PlanDigest != locator.Binding.PlanDigest || a.GraphDigest != locator.Binding.GraphDigest {
		return &Rejection{Reason: ReasonChain}
	}
	return f.VerifyAttemptFinality(ctx, ChoreographyAttemptFinalityProof{
		Event: event, ReceiptOID: event.ResultReceiptOID,
		ClaimOID: claimOID, AssignmentOID: assignmentOID})
}

func (f *RetainedChoreographyFeed) verifyNativeBlocked(ctx context.Context,
	taskID string, locator NodeSlotNativeRecoveryLocator, event ChoreographyEvent) error {
	if event.ResultReceiptOID != choreographyNativeFinalityMissingIdentity ||
		event.Package == choreographyNativeFinalityMissingIdentity ||
		event.BlockedBy == choreographyNativeFinalityMissingIdentity {
		return &Rejection{Reason: ReasonChain}
	}
	m := f.reader.opts.Mailbox
	data, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: event.TaskOID, Kind: choreographyBlockedKind,
		MaxBytes: f.maxDocumentBytes})
	if err != nil {
		return err
	}
	var blocked ChoreographyBlockedOutcome
	if strictJSON(data, &blocked) != nil || blocked.ChoreographyRunBinding != locator.Binding ||
		blocked.Task != taskID || blocked.Attempt != choreographyNativeFinalityFirstAttempt ||
		blocked.Package != event.Package ||
		blocked.BlockedBy != event.BlockedBy || blocked.PackageOutcome != event.PackageOutcome ||
		blocked.ExpectedOID != event.PriorTaskOID {
		return &Rejection{Reason: ReasonChain}
	}
	parents, err := m.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if locator.TaskBranch == choreographyNativeFinalityMissingIdentity {
		if len(parents) != choreographyNativeFinalityNoParentCount ||
			event.PriorTaskOID != choreographyNativeFinalityMissingIdentity {
			return &Rejection{Reason: ReasonChain}
		}
		return nil
	}
	if len(parents) != choreographyNativeFinalitySingleParentCount ||
		parents[choreographyNativeFinalityFirstParentIndex] != event.PriorTaskOID {
		return &Rejection{Reason: ReasonChain}
	}
	if event.PriorTaskOID == locator.ReservationOID {
		return nil
	}
	// An unclaimed compiled offer may precede a no-claim Blocked CAS. Its
	// first parent must still be the exact reservation captured before work.
	before, err := m.plumbing.CommitParents(ctx, event.PriorTaskOID)
	if err != nil || len(before) == choreographyNativeFinalityNoParentCount ||
		before[choreographyNativeFinalityFirstParentIndex] != locator.ReservationOID {
		return &Rejection{Reason: ReasonChain}
	}
	view, err := m.OpenCarriedAssignment(ctx, event.PriorTaskOID, f.maxDocumentBytes)
	if err != nil || view == nil ||
		view.Assignment.Task != taskID || view.Assignment.Node != locator.Node ||
		view.Assignment.Kind != locator.Kind || view.Assignment.Branch != locator.TaskBranch {
		return &Rejection{Reason: ReasonChain}
	}
	return nil
}
