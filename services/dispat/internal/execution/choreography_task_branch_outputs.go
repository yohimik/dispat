// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyTaskBranchOutputTreeType       = "tree"
	choreographyTaskBranchNoOID                = ""
	choreographyTaskBranchOutputMismatchError  = "execution: carried task outputs differ from the signed declared output tree"
	choreographyTaskBranchDecisionProofError   = "execution: task decision cannot mirror an unverified accepted output set"
	choreographyTaskBranchReceiptParentCount   = 2
	choreographyTaskBranchClaimParentIndex     = 0
	choreographyTaskBranchRawResultParentIndex = 1
)

// exactChoreographyOutputEntry requires the direct outputs/ entry to match
// the signed Result manifest. An absent manifest requires an absent entry,
// including when the unexpected entry is an otherwise invisible empty tree.
func (m *GitMailbox) exactChoreographyOutputEntry(ctx context.Context,
	commitOID string, result Result) ([]gitx.TreeEntry, error) {
	plumbing := gitx.NewPlumbing(m.plumbing)
	entry, found := plumbing.TreeEntryAtPath(ctx, commitOID, outputsDir)
	if err := plumbing.Err(); err != nil {
		return nil, err
	}
	if result.Outputs == nil {
		if found {
			return nil, fmt.Errorf(choreographyTaskBranchOutputMismatchError)
		}
		return nil, nil
	}
	if !found || entry.Mode != gitx.TreeModeDir || entry.OID != result.Outputs.OutputTree {
		return nil, fmt.Errorf(choreographyTaskBranchOutputMismatchError)
	}
	return []gitx.TreeEntry{{Mode: gitx.TreeModeDir, Type: choreographyTaskBranchOutputTreeType,
		OID: entry.OID, Name: outputsDir}}, nil
}

// decisionOutputEntries reopens the exact accepted Result receipt before
// mirroring its validated output tree at the canonical final task tip.
func (g *GitChoreographyPersist) decisionOutputEntries(ctx context.Context,
	decision ChoreographyDecision) (string, []gitx.TreeEntry, error) {
	m := g.opts.Mailbox
	parents, err := m.plumbing.CommitParents(ctx, decision.ResultOID)
	if err != nil || len(parents) != choreographyTaskBranchReceiptParentCount {
		return choreographyTaskBranchNoOID, nil,
			fmt.Errorf(choreographyTaskBranchDecisionProofError)
	}
	receiptTree, err := m.readTree(ctx, decision.ResultOID)
	if err != nil || receiptTree.kind != choreographyResultKind {
		return choreographyTaskBranchNoOID, nil,
			fmt.Errorf(choreographyTaskBranchDecisionProofError)
	}
	receiptBytes, err := m.Read(ctx, ChainTip{OID: decision.ResultOID, Kind: receiptTree.kind,
		document: receiptTree.document, signature: receiptTree.signature}, g.opts.MaxPackBytes)
	if err != nil {
		return choreographyTaskBranchNoOID, nil, err
	}
	var proposal ChoreographyResultProposal
	if strictJSON(receiptBytes, &proposal) != nil ||
		proposal.ChoreographyRunBinding != decision.ChoreographyRunBinding ||
		proposal.Task != decision.Task || proposal.Attempt != decision.Attempt ||
		proposal.Node != decision.Node || proposal.Branch != decision.Branch ||
		proposal.ClaimOID != parents[choreographyTaskBranchClaimParentIndex] ||
		proposal.ResultDigest != parents[choreographyTaskBranchRawResultParentIndex] {
		return choreographyTaskBranchNoOID, nil,
			fmt.Errorf(choreographyTaskBranchDecisionProofError)
	}
	rawOID := parents[choreographyTaskBranchRawResultParentIndex]
	rawTree, err := m.readTree(ctx, rawOID)
	if err != nil || rawTree.kind != MessageResult {
		return choreographyTaskBranchNoOID, nil,
			fmt.Errorf(choreographyTaskBranchDecisionProofError)
	}
	rawBytes, err := m.Read(ctx, ChainTip{OID: rawOID, Kind: rawTree.kind,
		document: rawTree.document, signature: rawTree.signature}, g.opts.MaxPackBytes)
	if err != nil {
		return choreographyTaskBranchNoOID, nil, err
	}
	var result Result
	if strictJSON(rawBytes, &result) != nil {
		return choreographyTaskBranchNoOID, nil,
			fmt.Errorf(choreographyTaskBranchDecisionProofError)
	}
	if err := m.VerifyChoreographyResultFromClaim(ctx, ChoreographyResultClaimProof{
		Proposal: proposal, Result: result, Store: m.plumbing,
		MaxDocumentBytes: g.opts.MaxPackBytes}); err != nil {
		return choreographyTaskBranchNoOID, nil, err
	}
	outputs, err := m.exactChoreographyOutputEntry(ctx, rawOID, result)
	if err != nil {
		return choreographyTaskBranchNoOID, nil, err
	}
	if _, err := m.exactChoreographyOutputEntry(ctx, decision.ResultOID, result); err != nil {
		return choreographyTaskBranchNoOID, nil, err
	}
	return rawOID, outputs, nil
}
