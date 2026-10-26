// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetainedNodeSlotRequiresExactSignedClaimAndEffectFinality(t *testing.T) {
	locator := &NodeSlotRecoveryLocator{Node: "node", AssignmentOID: "assignment",
		ProposalDigest: "exact-proposal"}
	state := &recoveredNodeSlot{lease: NodeSlotLease{Intent: NodeSlotIntent{
		Task: "pkg:build", Attempt: 1, Recovery: locator}}}
	head := ChoreographyEventCursor{OID: "later-ledger", Sequence: 4}
	require.Empty(t, state.settlementEvidence(4, head), "one unchanged epoch cannot settle a lost claim")
	require.Equal(t, head.OID, state.settlementEvidence(3, head),
		"a newer signed epoch with no matching accepted claim fences the old proposal")
	require.NoError(t, state.apply(ChoreographyEvent{Kind: ChoreographyClaimAccepted,
		Task: "pkg:build", Attempt: 1, Node: "node", ProposalDigest: "exact-proposal",
		PriorTaskOID: "assignment", TaskOID: "claim"}))
	require.Empty(t, state.settlementEvidence(3, head), "an accepted claim retains capacity")
	require.NoError(t, state.apply(ChoreographyEvent{Kind: ChoreographyResultAccepted,
		Task: "pkg:build", Attempt: 1, Node: "node", PriorTaskOID: "claim", TaskOID: "result"}))
	require.Empty(t, state.settlementEvidence(3, head), "ResultAccepted is not terminal")
	require.ErrorContains(t, state.apply(ChoreographyEvent{Kind: ChoreographyTerminal,
		Task: "pkg:build", Attempt: 1, Node: "node", TaskOID: "terminal",
		ResultReceiptOID: "foreign"}), "different accepted effect")
	require.NoError(t, state.apply(ChoreographyEvent{Kind: ChoreographyTerminal,
		Task: "pkg:build", Attempt: 1, Node: "node", TaskOID: "terminal",
		ResultReceiptOID: "result"}))
	require.Equal(t, "terminal", state.settlementEvidence(3, head))
}

func TestRetainedNodeSlotRejectsConflictingClaimAndRefusal(t *testing.T) {
	state := &recoveredNodeSlot{lease: NodeSlotLease{Intent: NodeSlotIntent{
		Task: "pkg:build", Attempt: 1, Recovery: &NodeSlotRecoveryLocator{
			Node: "node", AssignmentOID: "assignment", ProposalDigest: "exact"}}}}
	require.NoError(t, state.apply(ChoreographyEvent{Kind: ChoreographyClaimRefused,
		Task: "pkg:build", Attempt: 1, Node: "node", ProposalDigest: "exact",
		TaskOID: "assignment"}))
	require.ErrorContains(t, state.apply(ChoreographyEvent{Kind: ChoreographyClaimAccepted,
		Task: "pkg:build", Attempt: 1, Node: "node", ProposalDigest: "exact",
		PriorTaskOID: "assignment", TaskOID: "claim"}), "differs")
}
