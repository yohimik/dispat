// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const choreographyCausalCancelClockOffset = 25 * time.Hour

func TestChoreographyWorkerAcceptsExactSignedCancelAfterClockSkew(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(),
		ProposalID: "causal-cancel-clock-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	view, err := fixture.mailbox.OpenCarriedAssignment(t.Context(), accepted.AssignmentOID,
		choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	assignment := view.Assignment
	assignment.Limits.MaxManifestBytes = choreographyPickupProofDocumentLimit
	worker := &Worker{Node: claim.Node, Mailbox: fixture.mailbox}
	for _, skew := range []time.Duration{-choreographyCausalCancelClockOffset,
		choreographyCausalCancelClockOffset} {
		header := assignment.Header
		header.IssuedAt = time.Now().UTC().Add(skew).Format(time.RFC3339)
		withdrawal := Withdrawal{Header: header, Assignment: accepted.AssignmentOID,
			Tip: accepted.OID}
		document, err := json.Marshal(withdrawal)
		require.NoError(t, err)
		cancelOID, err := fixture.mailbox.commitMessage(t.Context(), MessageCancel,
			document, []string{accepted.OID}, nil)
		require.NoError(t, err)
		cancelTip, err := fixture.mailbox.readChainTip(t.Context(), spec.Branch, cancelOID)
		require.NoError(t, err)
		require.Empty(t, worker.checkWithdrawal(t.Context(), cancelTip,
			ChainTip{OID: accepted.AssignmentOID, Branch: spec.Branch}, accepted.OID, assignment))
		require.Equal(t, ReasonReplay, worker.checkWithdrawal(t.Context(), cancelTip,
			ChainTip{OID: accepted.AssignmentOID, Branch: spec.Branch}, spec.ReservationOID, assignment))
	}
}
