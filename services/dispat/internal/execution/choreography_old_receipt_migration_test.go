// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A retained receipt belongs to its exact signed run and ownership
// generation. Looking it up as an earlier run cannot discharge the current
// Claim, while the current run can still recover its own lost reply.
func TestChoreographyOldRunReceiptGrantsNothing(t *testing.T) {
	_, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		PickupID:    NewChoreographyPickupID(),
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "current-run-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	query, err := FormatClaimReceiptQuery(claim)
	require.NoError(t, err)
	old := query
	old.Run = "earlier-run"
	old.Generation = "earlier-generation"
	_, found, err := broker.LookupReceipt(t.Context(), old)
	require.ErrorContains(t, err, "another run")
	require.False(t, found)
	current, found, err := broker.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, receipt, current)
	page, err := persist.ReadEvents(t.Context(), offered.Cursor, 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
	require.False(t, broker.tasks[spec.Task].terminal)
}

// The verifier used by the retained return channel reads the signed Claim
// and carried Assignment before admitting a Result. Each old v2 receipt
// identity has a real v3 counterpart here; none can answer this claim.
func TestChoreographyResultRefusesForeignClaimIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Result)
	}{
		{"run", func(r *Result) { r.Run = "earlier-run" }},
		{"generation", func(r *Result) { r.Generation = "earlier-generation" }},
		{"plan", func(r *Result) { r.PlanDigest = "earlier-plan" }},
		{"task", func(r *Result) { r.Task = "another-task" }},
		{"attempt", func(r *Result) { r.Attempt = 2 }},
		{"node", func(r *Result) { r.Node = "another-node" }},
		{"branch", func(r *Result) { r.Branch = "dispat-task-other" }},
		{"assignment", func(r *Result) { r.Assignment = "0123456789abcdef0123456789abcdef01234567" }},
		{"kind", func(r *Result) { r.Kind = KindStage }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				PickupID:    NewChoreographyPickupID(),
				ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
				InputsDigest: spec.InputsDigest, ProposalID: "signed-claim"}
			completeChoreographyFixtureClaim(t, persist, &claim)
			receipt, err := broker.AcceptClaim(t.Context(), claim)
			require.NoError(t, err)
			result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
				Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
				PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
				Node: "node-a", Branch: spec.Branch}, Assignment: receipt.OID,
				Status: StatusSucceeded}
			proposal, _ := prepareChoreographyFixturePickupResult(t, fixture,
				choreographyFixturePickupResult{Claim: claim, Accepted: receipt,
					Result: result, ProposalID: "foreign-result"})
			result.PickupID, result.PendingBranch, result.PendingOID, result.OutcomeBranch =
				claim.PickupID, proposal.PendingBranch, proposal.PendingOID, proposal.OutcomeBranch
			require.NoError(t, fixture.mailbox.VerifyChoreographyResultFromClaim(t.Context(),
				ChoreographyResultClaimProof{Proposal: proposal, Result: result,
					Store: fixture.git, MaxDocumentBytes: 1 << 20}))
			tc.change(&result)
			require.Error(t, fixture.mailbox.VerifyChoreographyResultFromClaim(t.Context(),
				ChoreographyResultClaimProof{Proposal: proposal, Result: result,
					Store: fixture.git, MaxDocumentBytes: 1 << 20}))
			require.False(t, broker.tasks[spec.Task].terminal)
		})
	}
}

func TestChoreographyClaimRefusesForeignDirectIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ChoreographyClaimProposal)
	}{
		{"run", func(p *ChoreographyClaimProposal) { p.Run = "earlier-run" }},
		{"generation", func(p *ChoreographyClaimProposal) { p.Generation = "earlier-generation" }},
		{"plan", func(p *ChoreographyClaimProposal) { p.PlanDigest = "earlier-plan" }},
		{"task", func(p *ChoreographyClaimProposal) { p.Task = "another-task" }},
		{"attempt", func(p *ChoreographyClaimProposal) { p.Attempt = 2 }},
		{"node", func(p *ChoreographyClaimProposal) { p.Node = "another-node" }},
		{"branch", func(p *ChoreographyClaimProposal) { p.Branch = "dispat-task-other" }},
		{"assignment", func(p *ChoreographyClaimProposal) { p.ExpectedOID = "0123456789abcdef0123456789abcdef01234567" }},
		{"inputs", func(p *ChoreographyClaimProposal) { p.InputsDigest = "other-inputs" }},
		{"ledger", func(p *ChoreographyClaimProposal) { p.ExpectedLedgerOID = "0123456789abcdef0123456789abcdef01234567" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				PickupID:    NewChoreographyPickupID(),
				ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
				InputsDigest: spec.InputsDigest, ProposalID: "foreign-claim"}
			completeChoreographyFixtureClaim(t, persist, &claim)
			tc.change(&claim)
			receipt, err := broker.AcceptClaim(t.Context(), claim)
			require.False(t, receipt.Accepted, "%v", err)
			require.Equal(t, offered.AssignmentOID, remoteChoreographyOID(t, fixture, spec.Branch))
			require.Zero(t, broker.nodeUsed["node-a"])
			page, readErr := persist.ReadEvents(t.Context(), offered.Cursor, 10)
			require.NoError(t, readErr)
			for _, event := range page.Events {
				require.NotEqual(t, ChoreographyClaimAccepted, event.Kind)
			}
		})
	}
}
