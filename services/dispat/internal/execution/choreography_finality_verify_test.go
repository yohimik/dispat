// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetainedFinalityRejectsLedgerEventWithoutExactDecisionParent(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task,
		Attempt: 1, Node: "node-a", Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		ExpectedLedgerOID: persist.opts.Ledger.OID, InputsDigest: spec.InputsDigest,
		ProposalID: "finality-parent"}
	claimBytes, err := json.Marshal(claim)
	require.NoError(t, err)
	claimOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyClaimKind,
		claimBytes, []string{spec.ReservationOID}, nil)
	require.NoError(t, err)
	ackOID, err := fixture.mailbox.commitMessage(t.Context(), MessageAck,
		[]byte(`{"ack":"stand-in"}`), []string{claimOID}, nil)
	require.NoError(t, err)
	decision := ChoreographyDecision{ChoreographyRunBinding: binding, Task: spec.Task,
		Branch: spec.Branch, Attempt: 1, Node: "node-a", ResultOID: ackOID}
	document, err := json.Marshal(decision)
	require.NoError(t, err)
	decisionOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyDecisionKind,
		document, []string{spec.ReservationOID}, nil) // deliberately not ackOID
	require.NoError(t, err)
	require.NoError(t, persist.persistAtomic(t.Context(), choreographyLedgerMutation{
		Identity: "forged-finality-parent", TaskBranch: spec.Branch,
		OldTaskOID: spec.ReservationOID, NewTaskOID: decisionOID,
		Event: ChoreographyEvent{Kind: ChoreographyTerminal, Task: spec.Task,
			Attempt: 1, Node: "node-a", TaskOID: decisionOID,
			PriorTaskOID:     spec.ReservationOID,
			ResultReceiptOID: ackOID},
	}))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	feed := &RetainedChoreographyFeed{reader: persist, maxDocumentBytes: 1 << 20}
	require.Error(t, feed.VerifyAttemptFinality(t.Context(), ChoreographyAttemptFinalityProof{
		Event: page.Events[0], ReceiptOID: ackOID,
		ClaimOID: claimOID, AssignmentOID: spec.ReservationOID}),
		"matching signed ledger fields do not replace exact task-commit ancestry")
}
