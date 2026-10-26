// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyReceiptQueryBindsTheCompleteProposal(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: "build", Attempt: 1,
		Node: "node-a", Branch: "dispat-task-run-build", ExpectedOID: "old", ExpectedLedgerOID: "ledger",
		InputsDigest: "inputs",
		ProposalID:   "proposal"}
	first, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	claim.InputsDigest = "different-inputs"
	second, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	require.Equal(t, first.ProposalID, second.ProposalID)
	require.NotEqual(t, first.ProposalDigest, second.ProposalDigest,
		"one ID cannot settle a changed input selection")
}

func TestPrepareChoreographyResultBoundsAndKeepsExactQuery(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding, Task: "build", Attempt: 1,
		Node: "node-a", Branch: "dispat-task-run-build", ClaimOID: "claim", InputsDigest: "inputs",
		ProposalID: "proposal", ResultDigest: "result"}
	payload := bytes.NewReader([]byte("artifact"))
	prepared, err := PrepareChoreographyResult(t.Context(), proposal, payload, 8)
	require.NoError(t, err)
	require.EqualValues(t, 8, prepared.PayloadBytes)
	query, err := FormatResultReceiptQuery(prepared)
	require.NoError(t, err)
	require.NotEmpty(t, query.ProposalDigest)
	_, err = PrepareChoreographyResult(t.Context(), proposal, payload, 7)
	require.ErrorContains(t, err, "exceeds limit")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PrepareChoreographyResult(ctx, proposal, payload, 8)
	require.ErrorIs(t, err, context.Canceled)
}
