// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The serving node reads the signed v3 cancellation on the actual claimed
// task ref. A different run, malformed document or unsupported protocol cannot
// acknowledge the first node's accepted claim.
func TestChoreographyWorkerRefusesForeignCancelOnClaimedRef(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Withdrawal) []byte
		want   RejectReason
	}{
		{name: "another run", change: func(w *Withdrawal) []byte {
			w.Run = FormatRunID()
			body, _ := json.Marshal(w)
			return body
		}, want: ReasonReplay},
		{name: "malformed signed document", change: func(*Withdrawal) []byte {
			return []byte("{")
		}, want: ReasonUnreadable},
		{name: "unsupported signed protocol", change: func(w *Withdrawal) []byte {
			w.Protocol = ChoreographyProtocolVersion + 1
			body, _ := json.Marshal(w)
			return body
		}, want: ReasonProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
			proposal := ChoreographyClaimProposal{
				ChoreographyRunBinding: binding, Task: spec.Task, Attempt: 1,
				Node: "node-a", Branch: spec.Branch, ExpectedOID: offered.AssignmentOID,
				ExpectedLedgerOID: offered.Cursor.OID,
				InputsDigest:      spec.InputsDigest, PickupID: NewChoreographyPickupID(),
				ProposalID: "foreign-cancel"}
			completeChoreographyFixtureClaim(t, persist, &proposal)
			claim, err := broker.AcceptClaim(t.Context(), proposal)
			require.NoError(t, err)
			withdrawal := Withdrawal{Header: Header{
				Protocol: ChoreographyProtocolVersion, Kind: KindBuild, Run: binding.Run,
				Generation: binding.Generation, PlanDigest: binding.PlanDigest,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				IssuedAt: time.Now().UTC().Format(time.RFC3339)},
				Assignment: claim.AssignmentOID, Tip: claim.OID}
			assignmentHeader := withdrawal.Header
			cancelOID, err := fixture.mailbox.commitMessage(t.Context(), MessageCancel,
				tc.change(&withdrawal), []string{claim.OID}, nil)
			require.NoError(t, err)
			require.NoError(t, fixture.git.PushAtomicAdvances(t.Context(), fixture.endpoint,
				[]gitx.BranchAdvance{{Branch: spec.Branch, ExpectedOld: claim.OID, OID: cancelOID}}))
			_, err = fixture.mailbox.Reread(t.Context(), spec.Branch)
			require.NoError(t, err)
			assignmentTip, err := fixture.mailbox.Inspect(t.Context(), gitx.RemoteHead{
				Name: spec.Branch, OID: claim.AssignmentOID})
			require.NoError(t, err)
			cancelTip, err := fixture.mailbox.Inspect(t.Context(), gitx.RemoteHead{
				Name: spec.Branch, OID: cancelOID})
			require.NoError(t, err)
			worker := &Worker{Node: "node-a", Mailbox: fixture.mailbox}
			assignment := Assignment{Header: assignmentHeader,
				Limits: TransferLimits{MaxManifestBytes: 1 << 20}}
			require.Equal(t, tc.want, worker.checkWithdrawal(t.Context(), cancelTip,
				assignmentTip, claim.OID, assignment))
			require.Equal(t, cancelOID, remoteChoreographyOID(t, fixture, spec.Branch),
				"a refused foreign cancellation cannot become an Ack")
		})
	}
}
