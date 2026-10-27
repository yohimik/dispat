// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const interpretedAttemptTestAuthority = "0123456789abcdef0123456789abcdef"

func TestInterpretedFailedResultConsumesOneOfTwoSignedPickups(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: interpretedAttemptTestAuthority,
			AdmissionSeconds: choreographyTestAdmissionSeconds,
			Mode:             bootstrapInterpretationModeName, MaxAttempts: 2})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: "node-a",
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: NewInterpretedChoreographyPickupID(),
		ProposalID: "interpreted-first-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	wrongMode := claim
	wrongMode.PickupID = NewChoreographyPickupID()
	wrongMode.ProposalID = "compiled-id-in-interpreted-run"
	require.ErrorContains(t, func() error {
		_, claimErr := broker.AcceptClaim(t.Context(), wrongMode)
		return claimErr
	}(), choreographyAttemptPickupIdentityError)
	require.Equal(t, choreographyGitFirstAttempt, broker.tasks[spec.Task].attempt)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	failed := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: spec.Branch}, Status: StatusFailed}
	proposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: claim, Accepted: accepted,
			Result: failed, ProposalID: "interpreted-failed-result"})
	receipt, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: claim.Attempt, Node: claim.Node, ResultOID: receipt.OID, Retry: true}))
	require.Equal(t, choreographyGitFirstAttempt+1, broker.tasks[spec.Task].attempt)
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(coldPersist))
	require.NoError(t, err)
	require.Equal(t, broker.tasks[spec.Task].attempt, cold.tasks[spec.Task].attempt)
	require.Equal(t, broker.tasks[spec.Task].tip, cold.tasks[spec.Task].tip)
}

func TestInterpretedSuccessfulResultCannotBeRetriedAsFailed(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: interpretedAttemptTestAuthority,
			AdmissionSeconds: choreographyTestAdmissionSeconds,
			Mode:             bootstrapInterpretationModeName, MaxAttempts: 2})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: "node-a",
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: NewInterpretedChoreographyPickupID(),
		ProposalID: "interpreted-success-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	succeeded := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: spec.Branch}, Status: StatusSucceeded}
	proposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: claim, Accepted: accepted,
			Result: succeeded, ProposalID: "interpreted-success-result"})
	receipt, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	before, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	err = broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: claim.Attempt, Node: claim.Node, ResultOID: receipt.OID, Retry: true})
	require.ErrorContains(t, err, choreographyGitInvalidTaskDecisionError)
	after, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, before.OID, after.OID)
}
