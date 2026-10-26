// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyResultReadNoIdentity                = ""
	choreographyResultReadNoBytes                   = 0
	choreographyResultReadFirstAttempt              = 1
	choreographyResultReadFirstParentIndex          = 0
	choreographyResultReadRawParentIndex            = 1
	choreographyResultReadDecisionParentCount       = 1
	choreographyResultReadPickupDecisionParentCount = 2
	choreographyResultReadReceiptParentCount        = 2
	choreographyResultReadIncompleteAcceptedError   = "execution: incomplete accepted-result event"
	choreographyResultReadIncompleteCompletedError  = "execution: incomplete signed completed-result query"
)

type ChoreographyCompletedResultQuery struct {
	Binding ChoreographyRunBinding
	Task    string
	Event   ChoreographyEvent
}

// ReadChoreographyAcceptedResult opens the signed raw Result only after the
// first node has returned a durable ResultAccepted receipt. This is suitable
// for output admission before the first node writes retry/terminal finality.
func (m *GitMailbox) ReadChoreographyAcceptedResult(ctx context.Context,
	binding ChoreographyRunBinding, event ChoreographyEvent, maxBytes int64) (Result, error) {
	if m == nil || !binding.valid() || event.Kind != ChoreographyResultAccepted ||
		event.Task == choreographyResultReadNoIdentity || event.TaskOID == choreographyResultReadNoIdentity || event.OutputOID == choreographyResultReadNoIdentity ||
		event.Attempt < choreographyResultReadFirstAttempt || event.Node == choreographyResultReadNoIdentity || maxBytes <= choreographyResultReadNoBytes {
		return Result{}, fmt.Errorf(choreographyResultReadIncompleteAcceptedError)
	}
	receiptOID := event.TaskOID
	if event.ResultReceiptOID != choreographyResultReadNoIdentity {
		receiptOID = event.ResultReceiptOID
	}
	parents, err := m.plumbing.CommitParents(ctx, receiptOID)
	if err != nil || len(parents) != choreographyResultReadReceiptParentCount || parents[choreographyResultReadRawParentIndex] != event.OutputOID {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	tree, err := m.readTree(ctx, receiptOID)
	if err != nil || tree.kind != choreographyResultKind {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	data, err := m.Read(ctx, ChainTip{OID: receiptOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxBytes)
	if err != nil {
		return Result{}, err
	}
	var proposed ChoreographyResultProposal
	if err := strictJSON(data, &proposed); err != nil ||
		proposed.ChoreographyRunBinding != binding || proposed.Task != event.Task ||
		proposed.Attempt != event.Attempt || proposed.Node != event.Node ||
		proposed.ResultDigest != event.OutputOID || proposed.ClaimOID != parents[choreographyResultReadFirstParentIndex] ||
		(event.ClaimOID != choreographyResultReadNoIdentity && proposed.ClaimOID != event.ClaimOID) ||
		(event.PickupID != choreographyResultReadNoIdentity && proposed.PickupID != event.PickupID) ||
		proposed.Branch != FormatChoreographyTaskBranch(binding.Run, event.Task) {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	resultTree, err := m.readTree(ctx, event.OutputOID)
	if err != nil || resultTree.kind != MessageResult {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	resultData, err := m.Read(ctx, ChainTip{OID: event.OutputOID, Kind: resultTree.kind,
		document: resultTree.document, signature: resultTree.signature}, maxBytes)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := strictJSON(resultData, &result); err != nil ||
		result.Run != binding.Run || result.Generation != binding.Generation ||
		result.PlanDigest != binding.PlanDigest || result.Task != event.Task ||
		result.Attempt != event.Attempt || result.Node != event.Node ||
		result.Branch != proposed.Branch || result.Assignment != parents[choreographyResultReadFirstParentIndex] {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, event.OutputOID, result); err != nil {
		return Result{}, err
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, receiptOID, result); err != nil {
		return Result{}, err
	}
	if event.PickupID != choreographyResultReadNoIdentity {
		if err := m.VerifyChoreographyPickupResult(ctx, ChoreographyPickupResultProof{
			Proposal: proposed, Result: result, MaxDocumentBytes: maxBytes}); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// ReadChoreographyCompletedResult opens the signed raw Result behind an
// authenticated terminal event. A failed final task may still carry valid
// partial exports (notably sweep Continue); success is not a read condition.
// The caller must obtain Event from the verified, ordered ledger feed.
func (m *GitMailbox) ReadChoreographyCompletedResult(ctx context.Context,
	query ChoreographyCompletedResultQuery, maxBytes int64) (Result, error) {
	event := query.Event
	if m == nil || !query.Binding.valid() || query.Task == choreographyResultReadNoIdentity || maxBytes <= choreographyResultReadNoBytes ||
		event.Kind != ChoreographyTerminal || event.Task != query.Task ||
		event.TaskOID == choreographyResultReadNoIdentity || event.ResultReceiptOID == choreographyResultReadNoIdentity || event.OutputOID == choreographyResultReadNoIdentity ||
		event.Attempt < choreographyResultReadFirstAttempt || event.Node == choreographyResultReadNoIdentity {
		return Result{}, fmt.Errorf(choreographyResultReadIncompleteCompletedError)
	}
	decision, err := m.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil {
		return Result{}, err
	}
	if event.PickupID == choreographyResultReadNoIdentity &&
		(len(decision) != choreographyResultReadDecisionParentCount ||
			decision[choreographyResultReadFirstParentIndex] != event.ResultReceiptOID) ||
		event.PickupID != choreographyResultReadNoIdentity &&
			(len(decision) != choreographyResultReadPickupDecisionParentCount ||
				decision[choreographyResultReadFirstParentIndex] != event.PriorTaskOID ||
				decision[choreographyResultReadRawParentIndex] != event.ResultReceiptOID) {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	decisionTree, err := m.readTree(ctx, event.TaskOID)
	if err != nil || decisionTree.kind != choreographyDecisionKind {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	decisionBytes, err := m.Read(ctx, ChainTip{OID: event.TaskOID, Kind: decisionTree.kind,
		document: decisionTree.document, signature: decisionTree.signature}, maxBytes)
	if err != nil {
		return Result{}, err
	}
	var decided ChoreographyDecision
	if err := strictJSON(decisionBytes, &decided); err != nil ||
		decided.ChoreographyRunBinding != query.Binding || decided.Task != query.Task ||
		decided.Attempt != event.Attempt || decided.Node != event.Node ||
		decided.ResultOID != event.ResultReceiptOID || decided.Succeeded != event.Succeeded ||
		decided.Branch != FormatChoreographyTaskBranch(query.Binding.Run, query.Task) {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	receipt, err := m.plumbing.CommitParents(ctx, event.ResultReceiptOID)
	if err != nil || len(receipt) != choreographyResultReadReceiptParentCount || receipt[choreographyResultReadRawParentIndex] != event.OutputOID {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	receiptTree, err := m.readTree(ctx, event.ResultReceiptOID)
	if err != nil || receiptTree.kind != choreographyResultKind {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	receiptBytes, err := m.Read(ctx, ChainTip{OID: event.ResultReceiptOID, Kind: receiptTree.kind,
		document: receiptTree.document, signature: receiptTree.signature}, maxBytes)
	if err != nil {
		return Result{}, err
	}
	var proposed ChoreographyResultProposal
	if err := strictJSON(receiptBytes, &proposed); err != nil ||
		proposed.ChoreographyRunBinding != query.Binding || proposed.Task != query.Task ||
		proposed.Attempt != event.Attempt || proposed.Node != event.Node ||
		proposed.Branch != FormatChoreographyTaskBranch(query.Binding.Run, query.Task) ||
		proposed.ResultDigest != event.OutputOID || proposed.ClaimOID != receipt[choreographyResultReadFirstParentIndex] ||
		(event.PickupID != choreographyResultReadNoIdentity &&
			(proposed.PickupID != event.PickupID || proposed.ClaimOID != event.ClaimOID)) {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	resultTree, err := m.readTree(ctx, event.OutputOID)
	if err != nil || resultTree.kind != MessageResult {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	data, err := m.Read(ctx, ChainTip{OID: event.OutputOID, Kind: resultTree.kind,
		document: resultTree.document, signature: resultTree.signature}, maxBytes)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := strictJSON(data, &result); err != nil ||
		result.Run != query.Binding.Run || result.Generation != query.Binding.Generation ||
		result.PlanDigest != query.Binding.PlanDigest || result.Task != query.Task ||
		result.Attempt != event.Attempt || result.Node != event.Node ||
		result.Branch != FormatChoreographyTaskBranch(query.Binding.Run, query.Task) ||
		result.Assignment != receipt[choreographyResultReadFirstParentIndex] ||
		(event.Succeeded && result.Status != StatusSucceeded) {
		return Result{}, &Rejection{Reason: ReasonChain}
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, event.OutputOID, result); err != nil {
		return Result{}, err
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, event.ResultReceiptOID, result); err != nil {
		return Result{}, err
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, event.TaskOID, result); err != nil {
		return Result{}, err
	}
	return result, nil
}
