// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"reflect"
)

const (
	choreographyFinalityMissingIdentity   = ""
	choreographyFinalityNoSequence        = 0
	choreographyFinalityFirstAttempt      = 1
	choreographyFinalitySequenceOffset    = 1
	choreographyFinalityFirstParentIndex  = 0
	choreographyFinalityResultParentIndex = 1
	choreographyFinalitySingleParentCount = 1
	choreographyFinalityResultParentCount = 2
	choreographyFinalityIncompleteError   = "execution: incomplete retained attempt finality"
	choreographyFinalityNotInLedgerError  = "execution: attempt finality is not in the signed ledger"
)

// VerifyAttemptFinality proves that a retained signed terminal/retry event
// names the exact signed Decision commit whose first parent is the accepted
// Result or Ack receipt. A ledger-only field match is insufficient evidence
// to free a physical node slot after a crash.
type ChoreographyAttemptFinalityProof struct {
	Event         ChoreographyEvent
	ReceiptOID    string
	ClaimOID      string
	AssignmentOID string
}

func (f *RetainedChoreographyFeed) VerifyAttemptFinality(ctx context.Context,
	proof ChoreographyAttemptFinalityProof) error {
	event, expectedReceiptOID := proof.Event, proof.ReceiptOID
	expectedClaimOID, expectedAssignmentOID := proof.ClaimOID, proof.AssignmentOID
	if f == nil || f.reader == nil || f.maxDocumentBytes <= choreographyFinalityNoSequence ||
		(event.Kind != ChoreographyTerminal && event.Kind != ChoreographyRetryReady &&
			event.Kind != ChoreographyPickupSettled) ||
		event.Sequence == choreographyFinalityNoSequence || event.Task == choreographyFinalityMissingIdentity || event.TaskOID == choreographyFinalityMissingIdentity ||
		event.ResultReceiptOID == choreographyFinalityMissingIdentity || event.ResultReceiptOID != expectedReceiptOID ||
		expectedClaimOID == choreographyFinalityMissingIdentity || expectedAssignmentOID == choreographyFinalityMissingIdentity ||
		event.Attempt < choreographyFinalityFirstAttempt || event.Node == choreographyFinalityMissingIdentity {
		return fmt.Errorf(choreographyFinalityIncompleteError)
	}
	g := f.reader
	g.mu.Lock()
	err := g.syncLedgerLocked(ctx)
	if err == nil && (event.Sequence > uint64(len(g.events)) || !reflect.DeepEqual(g.events[event.Sequence-choreographyFinalitySequenceOffset], event)) {
		err = fmt.Errorf(choreographyFinalityNotInLedgerError)
	}
	g.mu.Unlock()
	if err != nil {
		return err
	}
	m := g.opts.Mailbox
	claimParents, err := m.plumbing.CommitParents(ctx, expectedClaimOID)
	if err != nil || len(claimParents) != choreographyFinalitySingleParentCount || claimParents[choreographyFinalityFirstParentIndex] != expectedAssignmentOID {
		return &Rejection{Reason: ReasonChain}
	}
	claimBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: expectedClaimOID, Kind: choreographyClaimKind,
		MaxBytes: f.maxDocumentBytes})
	if err != nil {
		return err
	}
	var claim ChoreographyClaimProposal
	if strictJSON(claimBytes, &claim) != nil || claim.ChoreographyRunBinding != g.opts.Binding ||
		claim.Task != event.Task || claim.Attempt != event.Attempt || claim.Node != event.Node ||
		claim.PickupID != event.PickupID ||
		claim.Branch != FormatChoreographyTaskBranch(g.opts.Binding.Run, event.Task) ||
		claim.GraphRootOID != g.opts.GraphRootOID || claim.ExpectedOID == choreographyFinalityMissingIdentity {
		return &Rejection{Reason: ReasonChain}
	}
	assignmentParents, err := m.plumbing.CommitParents(ctx, expectedAssignmentOID)
	if err != nil || len(assignmentParents) == choreographyFinalityNoSequence ||
		assignmentParents[choreographyFinalityFirstParentIndex] != claim.ExpectedOID {
		return &Rejection{Reason: ReasonChain}
	}
	view, err := m.OpenCarriedAssignment(ctx, expectedAssignmentOID, f.maxDocumentBytes)
	if err != nil || view == nil ||
		view.Assignment.GraphRootOID != g.opts.GraphRootOID ||
		view.Assignment.InputsDigest != claim.InputsDigest ||
		view.Assignment.TemplateDigest != claim.TemplateDigest ||
		view.Assignment.Task != claim.Task || view.Assignment.Node != claim.Node ||
		view.Assignment.Attempt != claim.Attempt || view.Assignment.Branch != claim.Branch {
		return &Rejection{Reason: ReasonChain}
	}
	decision := ChoreographyDecision{Branch: FormatChoreographyTaskBranch(g.opts.Binding.Run, event.Task)}
	if event.Kind == ChoreographyPickupSettled {
		if event.PickupID == choreographyFinalityMissingIdentity ||
			event.ClaimOID != expectedClaimOID || event.TaskOID != event.PriorTaskOID ||
			event.GraphRootOID != g.opts.GraphRootOID {
			return &Rejection{Reason: ReasonChain}
		}
	} else {
		decisionParents, err := m.plumbing.CommitParents(ctx, event.TaskOID)
		if err != nil {
			return &Rejection{Reason: ReasonChain}
		}
		if event.PickupID == choreographyFinalityMissingIdentity {
			if len(decisionParents) != choreographyFinalitySingleParentCount ||
				decisionParents[choreographyFinalityFirstParentIndex] != expectedReceiptOID {
				return &Rejection{Reason: ReasonChain}
			}
		} else {
			resultDecision := len(decisionParents) == choreographyFinalityResultParentCount &&
				decisionParents[choreographyFinalityFirstParentIndex] == event.PriorTaskOID &&
				decisionParents[choreographyFinalityResultParentIndex] == expectedReceiptOID &&
				event.OutputOID != choreographyFinalityMissingIdentity
			ackDecision := len(decisionParents) == choreographyFinalitySingleParentCount &&
				decisionParents[choreographyFinalityFirstParentIndex] == expectedReceiptOID &&
				event.OutputOID == choreographyFinalityMissingIdentity
			if event.ClaimOID != expectedClaimOID || !(resultDecision || ackDecision) {
				return &Rejection{Reason: ReasonChain}
			}
		}
		decisionBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
			Mailbox: m, OID: event.TaskOID, Kind: choreographyDecisionKind,
			MaxBytes: f.maxDocumentBytes})
		if err != nil {
			return err
		}
		if strictJSON(decisionBytes, &decision) != nil ||
			decision.ChoreographyRunBinding != g.opts.Binding || decision.Task != event.Task ||
			decision.Branch != FormatChoreographyTaskBranch(g.opts.Binding.Run, event.Task) ||
			decision.Attempt != event.Attempt || decision.Node != event.Node ||
			decision.ResultOID != expectedReceiptOID || decision.Retry != (event.Kind == ChoreographyRetryReady) ||
			decision.Succeeded != event.Succeeded || decision.PackageOutcome != event.PackageOutcome {
			return &Rejection{Reason: ReasonChain}
		}
	}
	receiptTree, err := m.readTree(ctx, expectedReceiptOID)
	if err != nil {
		return err
	}
	receiptParents, err := m.plumbing.CommitParents(ctx, expectedReceiptOID)
	if err != nil {
		return err
	}
	switch receiptTree.kind {
	case choreographyResultKind:
		if len(receiptParents) != choreographyFinalityResultParentCount || receiptParents[choreographyFinalityFirstParentIndex] != expectedClaimOID ||
			receiptParents[choreographyFinalityResultParentIndex] != event.OutputOID {
			return &Rejection{Reason: ReasonChain}
		}
		data, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
			Mailbox: m, OID: expectedReceiptOID, Kind: choreographyResultKind,
			MaxBytes: f.maxDocumentBytes})
		if err != nil {
			return err
		}
		var result ChoreographyResultProposal
		if strictJSON(data, &result) != nil || result.ChoreographyRunBinding != g.opts.Binding ||
			result.Task != event.Task || result.Attempt != event.Attempt || result.Node != event.Node ||
			result.PickupID != event.PickupID ||
			result.Branch != decision.Branch || result.ClaimOID != receiptParents[choreographyFinalityFirstParentIndex] ||
			result.ResultDigest != event.OutputOID {
			return &Rejection{Reason: ReasonChain}
		}
	case MessageAck:
		if len(receiptParents) != choreographyFinalitySingleParentCount || event.OutputOID != choreographyFinalityMissingIdentity {
			return &Rejection{Reason: ReasonChain}
		}
		data, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
			Mailbox: m, OID: expectedReceiptOID, Kind: MessageAck,
			MaxBytes: f.maxDocumentBytes})
		if err != nil {
			return err
		}
		var ack Ack
		if strictJSON(data, &ack) != nil || ack.Protocol != ChoreographyProtocolVersion ||
			ack.Run != g.opts.Binding.Run || ack.Generation != g.opts.Binding.Generation ||
			ack.PlanDigest != g.opts.Binding.PlanDigest || ack.Task != event.Task ||
			ack.Attempt != event.Attempt || ack.Node != event.Node ||
			ack.Branch != decision.Branch || ack.Cancel != receiptParents[choreographyFinalityFirstParentIndex] {
			return &Rejection{Reason: ReasonChain}
		}
		cancelParents, err := m.plumbing.CommitParents(ctx, ack.Cancel)
		if err != nil || len(cancelParents) != choreographyFinalitySingleParentCount {
			return &Rejection{Reason: ReasonChain}
		}
		cancelBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
			Mailbox: m, OID: ack.Cancel, Kind: MessageCancel,
			MaxBytes: f.maxDocumentBytes})
		if err != nil {
			return err
		}
		var cancel Withdrawal
		if strictJSON(cancelBytes, &cancel) != nil || cancel.Tip != cancelParents[choreographyFinalityFirstParentIndex] ||
			cancel.Tip != expectedClaimOID || cancel.Assignment != expectedAssignmentOID ||
			cancel.Assignment != ack.Assignment || cancel.Run != ack.Run ||
			cancel.Generation != ack.Generation || cancel.PlanDigest != ack.PlanDigest ||
			cancel.Task != ack.Task || cancel.Attempt != ack.Attempt ||
			cancel.Node != ack.Node || cancel.Branch != ack.Branch {
			return &Rejection{Reason: ReasonChain}
		}
	default:
		return &Rejection{Reason: ReasonChain}
	}
	return nil
}

type choreographyFinalityDocumentQuery struct {
	Mailbox  *GitMailbox
	OID      string
	Kind     MessageKind
	MaxBytes int64
}

func readChoreographyFinalityDocument(ctx context.Context,
	query choreographyFinalityDocumentQuery) ([]byte, error) {
	m, oid, kind, maxBytes := query.Mailbox, query.OID, query.Kind, query.MaxBytes
	tree, err := m.readTree(ctx, oid)
	if err != nil || tree.kind != kind {
		return nil, &Rejection{Reason: ReasonChain}
	}
	return m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxBytes)
}
