// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyResultProofMissingIdentity       = ""
	choreographyResultProofNoBytes               = 0
	choreographyResultProofNoParents             = 0
	choreographyResultProofSingleParent          = 1
	choreographyResultProofFirstParent           = 0
	choreographyResultProofNoOutputs             = 0
	choreographyResultProofNoWeight              = 0
	choreographyResultProofSignedReceiptError    = "execution: accepted result has no signed proposal receipt"
	choreographyResultProofLedgerMismatchError   = "execution: accepted result proposal differs from retained ledger event"
	choreographyResultProofIncompleteError       = "execution: incomplete retained result verifier"
	choreographyResultProofClaimParentError      = "execution: result claim has no exact assignment parent"
	choreographyResultProofUnsignedClaimError    = "execution: result claim is not a signed accepted claim"
	choreographyResultProofClaimMismatchError    = "execution: result answers another signed claim"
	choreographyResultProofAssignmentRootError   = "execution: result assignment lacks its exact signed task root and input identity"
	choreographyResultProofFrameMismatchError    = "execution: result differs from retained signed command frame"
	choreographyResultProofStatusError           = "execution: result has an invalid status"
	choreographyResultProofMissingOutputError    = "execution: successful producer has no signed declared output set"
	choreographyResultProofUndeclaredOutputError = "execution: result carries outputs outside its signed declaration"
)

// ReadVerifiedChoreographyAcceptedResultFromAssignment reconstructs an
// accepted Result and its signed proposal after a first-node restart. The
// proposal is read from the exact ledger-named receipt; its Claim then leads
// to the authenticated Assignment carrying the output policy.
func (m *GitMailbox) ReadVerifiedChoreographyAcceptedResultFromAssignment(ctx context.Context,
	binding ChoreographyRunBinding, event ChoreographyEvent,
	maxDocumentBytes int64) (Result, error) {
	result, err := m.ReadChoreographyAcceptedResult(ctx, binding, event, maxDocumentBytes)
	if err != nil {
		return Result{}, err
	}
	receiptOID := event.TaskOID
	if event.ResultReceiptOID != choreographyResultProofMissingIdentity {
		receiptOID = event.ResultReceiptOID
	}
	tree, err := m.readTree(ctx, receiptOID)
	if err != nil || tree.kind != choreographyResultKind {
		return Result{}, fmt.Errorf(choreographyResultProofSignedReceiptError)
	}
	document, err := m.Read(ctx, ChainTip{OID: receiptOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxDocumentBytes)
	if err != nil {
		return Result{}, err
	}
	var proposal ChoreographyResultProposal
	if strictJSON(document, &proposal) != nil ||
		proposal.ChoreographyRunBinding != binding || proposal.Task != event.Task ||
		proposal.Attempt != event.Attempt || proposal.Node != event.Node ||
		proposal.ResultDigest != event.OutputOID ||
		proposal.Branch != FormatChoreographyTaskBranch(binding.Run, event.Task) {
		return Result{}, fmt.Errorf(choreographyResultProofLedgerMismatchError)
	}
	if err := m.VerifyChoreographyResultFromClaim(ctx, ChoreographyResultClaimProof{
		Proposal: proposal, Result: result, Store: m.plumbing,
		MaxDocumentBytes: maxDocumentBytes}); err != nil {
		return Result{}, err
	}
	return result, nil
}

// VerifyChoreographyResultFromClaim reconstructs the result policy from the
// exact signed Assignment that the accepted Claim answers. It is suitable for
// first-node restart, when the command's in-memory expectation map is gone.
// It never reads mutable source refs or substitutes an ambient task frame.
type ChoreographyResultClaimProof struct {
	Proposal         ChoreographyResultProposal
	Result           Result
	Store            *gitx.LocalGitx
	MaxDocumentBytes int64
}

func (m *GitMailbox) VerifyChoreographyResultFromClaim(ctx context.Context,
	proof ChoreographyResultClaimProof) error {
	p, result, store := proof.Proposal, proof.Result, proof.Store
	maxDocumentBytes := proof.MaxDocumentBytes
	if m == nil || store == nil || p.ClaimOID == choreographyResultProofMissingIdentity ||
		maxDocumentBytes <= choreographyResultProofNoBytes ||
		!p.ChoreographyRunBinding.valid() {
		return fmt.Errorf(choreographyResultProofIncompleteError)
	}
	parents, err := m.plumbing.CommitParents(ctx, p.ClaimOID)
	if err != nil || len(parents) != choreographyResultProofSingleParent {
		return fmt.Errorf(choreographyResultProofClaimParentError)
	}
	claimTree, err := m.readTree(ctx, p.ClaimOID)
	if err != nil || claimTree.kind != choreographyClaimKind {
		return fmt.Errorf(choreographyResultProofUnsignedClaimError)
	}
	claimBytes, err := m.Read(ctx, ChainTip{OID: p.ClaimOID, Kind: claimTree.kind,
		document: claimTree.document, signature: claimTree.signature}, maxDocumentBytes)
	if err != nil {
		return err
	}
	var claim ChoreographyClaimProposal
	if strictJSON(claimBytes, &claim) != nil ||
		claim.ChoreographyRunBinding != p.ChoreographyRunBinding ||
		claim.Task != p.Task || claim.Attempt != p.Attempt || claim.Node != p.Node ||
		claim.Branch != p.Branch || claim.GraphRootOID == choreographyResultProofMissingIdentity ||
		claim.InputsDigest != p.InputsDigest || claim.PickupID != p.PickupID {
		return fmt.Errorf(choreographyResultProofClaimMismatchError)
	}
	view, err := m.OpenCarriedAssignment(ctx, parents[choreographyResultProofFirstParent], maxDocumentBytes)
	if err != nil {
		return err
	}
	a := view.Assignment
	assignmentParents, err := m.plumbing.CommitParents(ctx, parents[choreographyResultProofFirstParent])
	if err != nil {
		return err
	}
	if len(assignmentParents) == choreographyResultProofNoParents ||
		assignmentParents[choreographyResultProofFirstParent] != claim.ExpectedOID ||
		a.GraphRootOID != claim.GraphRootOID || a.InputsDigest != claim.InputsDigest ||
		a.TemplateDigest != claim.TemplateDigest {
		return fmt.Errorf(choreographyResultProofAssignmentRootError)
	}
	if a.Protocol != ChoreographyProtocolVersion || a.Run != p.Run ||
		a.Generation != p.Generation || a.PlanDigest != p.PlanDigest ||
		a.GraphDigest != p.GraphDigest || a.Task != p.Task || a.Attempt != p.Attempt ||
		a.Node != p.Node || a.Branch != p.Branch || a.Weight <= choreographyResultProofNoWeight ||
		result.Run != p.Run || result.Generation != p.Generation ||
		result.PlanDigest != p.PlanDigest || result.Task != p.Task ||
		result.Attempt != p.Attempt || result.Node != p.Node ||
		result.Branch != p.Branch || result.Kind != a.Kind ||
		result.Assignment != p.ClaimOID {
		return fmt.Errorf(choreographyResultProofFrameMismatchError)
	}
	if result.Status != StatusSucceeded && result.Status != StatusFailed && result.Status != StatusCancelled {
		return fmt.Errorf(choreographyResultProofStatusError)
	}
	if result.Outputs == nil {
		if result.Status == StatusSucceeded && len(a.Outputs) > choreographyResultProofNoOutputs {
			return fmt.Errorf(choreographyResultProofMissingOutputError)
		}
		return nil
	}
	if len(a.Outputs) == choreographyResultProofNoOutputs {
		return fmt.Errorf(choreographyResultProofUndeclaredOutputError)
	}
	_, err = ValidateOutputs(ctx, store, result.Outputs, OutputExpectation{
		Run: p.Run, PlanDigest: p.PlanDigest, Generation: p.Generation,
		Task: p.Task, Attempt: p.Attempt, Roots: a.Outputs,
		Platforms: a.Platforms, Limits: a.Limits})
	return err
}
