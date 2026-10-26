// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// A first node cancels a v3 attempt on top of its choreography Claim, not
// the legacy worker Claim. The serving node must recognize that exact signed
// transition so it can stop its process and return an acknowledgement.
func TestWorkerRecognizesSignedCancelOfCompiledClaim(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(),
		ProposalID: "claim-worker-cancel"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	cancelOID, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", AssignmentOID: receipt.AssignmentOID,
		ClaimOID: receipt.OID})
	require.NoError(t, err)
	_, err = fixture.mailbox.Reread(t.Context(), spec.Branch)
	require.NoError(t, err)
	assignmentTip, err := fixture.mailbox.Inspect(t.Context(), gitx.RemoteHead{
		Name: spec.Branch, OID: receipt.AssignmentOID})
	require.NoError(t, err)
	cancelTip, err := fixture.mailbox.Inspect(t.Context(), gitx.RemoteHead{
		Name: spec.Branch, OID: cancelOID})
	require.NoError(t, err)
	require.Equal(t, choreographyClaimKind, cancelTip.Previous)
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch},
		Limits: TransferLimits{MaxManifestBytes: 1 << 20}}
	worker := &Worker{Node: "node-a", Mailbox: fixture.mailbox}
	require.Empty(t, worker.checkWithdrawal(t.Context(), cancelTip, assignmentTip,
		receipt.OID, assignment))
	require.Equal(t, ReasonReplay, worker.checkWithdrawal(t.Context(), cancelTip,
		assignmentTip, spec.ReservationOID, assignment))
}
