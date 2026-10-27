// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	choreographyPickupProofNoParentCount        = 0
	choreographyPickupProofOneParentCount       = 1
	choreographyPickupProofFirstParentIndex     = 0
	choreographyPickupProofMissingIdentity      = ""
	choreographyPickupProofResultIdentityError  = "execution: result pickup identity differs from its exact proposal"
	choreographyPickupProofPendingRefError      = "execution: pending branch does not name its exact signed pickup commit"
	choreographyPickupProofOutcomeRefError      = "execution: outcome branch does not name its exact signed result commit"
	choreographyPickupProofPendingChainError    = "execution: pending commit is not a direct child of the accepted claim"
	choreographyPickupProofOutcomeChainError    = "execution: outcome commit is not a direct child of its signed pending commit"
	choreographyPickupProofClaimError           = "execution: pickup names no signed accepted claim"
	choreographyPickupProofPendingDocumentError = "execution: pickup pending commit has no signed pending document"
	choreographyPickupProofPendingIdentityError = "execution: signed pending pickup differs from its accepted claim or durable lease"
	choreographyPickupProofOutcomeDocumentError = "execution: pickup outcome commit has no exact signed result document"
)

// ChoreographyPickupResultProof identifies the worker-owned refs and the
// signed Result imported from the bounded pack. The caller must separately
// verify that the Claim was accepted on this run's ledger and that the
// carried output tree matches the Result manifest.
type ChoreographyPickupResultProof struct {
	Proposal         ChoreographyResultProposal
	Result           Result
	Mode             string
	MaxDocumentBytes int64
}

// VerifyChoreographyPickupResult checks that two immutable remote refs carry
// this exact physical pickup: task Claim -> pending -> output/failed Result.
// It does not settle the logical task or infer that another pickup stopped.
func (m *GitMailbox) VerifyChoreographyPickupResult(ctx context.Context,
	proof ChoreographyPickupResultProof) error {
	p, result := proof.Proposal, proof.Result
	identity := ChoreographyPickupIdentity{Run: p.Run, Task: p.Task, Node: p.Node,
		PickupID: p.PickupID, Mode: proof.Mode}
	pendingBranch, err := FormatChoreographyPendingBranch(identity)
	if err != nil {
		return err
	}
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, result.Status)
	if err != nil {
		return err
	}
	if p.PendingOID == choreographyPickupProofMissingIdentity ||
		p.PendingBranch != pendingBranch || p.OutcomeBranch != outcomeBranch ||
		result.PickupID != p.PickupID || result.PendingBranch != p.PendingBranch ||
		result.PendingOID != p.PendingOID || result.OutcomeBranch != p.OutcomeBranch {
		return fmt.Errorf(choreographyPickupProofResultIdentityError)
	}
	pendingTip, err := m.readRemoteTip(ctx, pendingBranch)
	if err != nil {
		return err
	}
	if pendingTip != p.PendingOID {
		return fmt.Errorf(choreographyPickupProofPendingRefError)
	}
	outcomeTip, err := m.readRemoteTip(ctx, outcomeBranch)
	if err != nil {
		return err
	}
	if outcomeTip != p.ResultDigest {
		return fmt.Errorf(choreographyPickupProofOutcomeRefError)
	}
	outcomeTree, err := m.readTree(ctx, p.ResultDigest)
	if err != nil {
		return err
	}
	if outcomeTree.kind != MessageResult {
		return fmt.Errorf(choreographyPickupProofOutcomeDocumentError)
	}
	outcomeDocument, err := m.Read(ctx, ChainTip{OID: p.ResultDigest, Kind: outcomeTree.kind,
		document: outcomeTree.document, signature: outcomeTree.signature}, proof.MaxDocumentBytes)
	if err != nil {
		return err
	}
	providedDocument, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if !bytes.Equal(outcomeDocument, providedDocument) {
		return fmt.Errorf(choreographyPickupProofOutcomeDocumentError)
	}
	pendingParents, err := m.plumbing.CommitParents(ctx, p.PendingOID)
	if err != nil {
		return err
	}
	if len(pendingParents) != choreographyPickupProofOneParentCount ||
		pendingParents[choreographyPickupProofFirstParentIndex] != p.ClaimOID {
		return fmt.Errorf(choreographyPickupProofPendingChainError)
	}
	outcomeParents, err := m.plumbing.CommitParents(ctx, p.ResultDigest)
	if err != nil {
		return err
	}
	if len(outcomeParents) != choreographyPickupProofOneParentCount ||
		outcomeParents[choreographyPickupProofFirstParentIndex] != p.PendingOID {
		return fmt.Errorf(choreographyPickupProofOutcomeChainError)
	}
	claimTree, err := m.readTree(ctx, p.ClaimOID)
	if err != nil {
		return err
	}
	if claimTree.kind != choreographyClaimKind {
		return fmt.Errorf(choreographyPickupProofClaimError)
	}
	claimDocument, err := m.Read(ctx, ChainTip{OID: p.ClaimOID, Kind: claimTree.kind,
		document: claimTree.document, signature: claimTree.signature}, proof.MaxDocumentBytes)
	if err != nil {
		return err
	}
	var claim ChoreographyClaimProposal
	if err := strictJSON(claimDocument, &claim); err != nil {
		return err
	}
	pendingTree, err := m.readTree(ctx, p.PendingOID)
	if err != nil {
		return err
	}
	if pendingTree.kind != MessagePending {
		return fmt.Errorf(choreographyPickupProofPendingDocumentError)
	}
	pendingDocument, err := m.Read(ctx, ChainTip{OID: p.PendingOID, Kind: pendingTree.kind,
		document: pendingTree.document, signature: pendingTree.signature}, proof.MaxDocumentBytes)
	if err != nil {
		return err
	}
	var pending Pending
	if err := strictJSON(pendingDocument, &pending); err != nil {
		return err
	}
	claimParents, err := m.plumbing.CommitParents(ctx, p.ClaimOID)
	if err != nil {
		return err
	}
	if len(claimParents) == choreographyPickupProofNoParentCount ||
		claim.PickupID != p.PickupID || claim.ChoreographyRunBinding != p.ChoreographyRunBinding ||
		claim.Task != p.Task || claim.Attempt != p.Attempt || claim.Node != p.Node ||
		claim.Branch != p.Branch || claim.InputsDigest != p.InputsDigest ||
		pending.Protocol != ChoreographyProtocolVersion || pending.Kind != result.Kind ||
		pending.Run != p.Run || pending.Generation != p.Generation ||
		pending.PlanDigest != p.PlanDigest || pending.Task != p.Task ||
		pending.Attempt != p.Attempt || pending.Node != p.Node ||
		pending.Branch != p.PendingBranch || pending.TaskBranch != p.Branch ||
		pending.PickupID != p.PickupID || pending.ClaimOID != p.ClaimOID ||
		pending.AssignmentOID != claimParents[choreographyPickupProofFirstParentIndex] ||
		pending.GraphDigest != p.GraphDigest || pending.GraphRootOID != claim.GraphRootOID ||
		pending.InputsDigest != p.InputsDigest ||
		pending.MembershipEpochOID != claim.MembershipEpochOID ||
		pending.CapacityAuthorityID != claim.CapacityAuthorityID ||
		pending.LeaseIntentID != claim.LeaseIntentID ||
		pending.LeaseWeight != claim.LeaseWeight {
		return fmt.Errorf(choreographyPickupProofPendingIdentityError)
	}
	if identity.effectiveMode() == bootstrapInterpretationModeName {
		issuedAt, err := interpretedPickupTime(p.PickupID)
		if err != nil || pending.IssuedAt != issuedAt.Format(time.RFC3339Nano) {
			return fmt.Errorf(choreographyPickupProofPendingIdentityError)
		}
	}
	return nil
}
