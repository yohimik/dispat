// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	interpretedContactFirstProposal  = "interpreted-contact-first"
	interpretedContactSecondProposal = "interpreted-contact-second"
	interpretedContactSecondResult   = "interpreted-contact-second-result"
	interpretedContactOldResult      = "interpreted-contact-old-result"
	interpretedContactOldAck         = "interpreted-contact-old-ack"
	interpretedContactNode           = "node-a"
	interpretedContactMaxAttempts    = 2
)

func interpretedContactFixture(t *testing.T, retrySafe bool) (*mailboxFixture,
	*GitChoreographyPersist, ChoreographyRunBinding, ChoreographyTaskSpec, *FirstNodeBroker,
	ChoreographyClaimProposal, ChoreographyReceipt) {
	t.Helper()
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: interpretedAttemptTestAuthority,
			AdmissionSeconds: choreographyTestAdmissionSeconds,
			Mode:             bootstrapInterpretationModeName, MaxAttempts: interpretedContactMaxAttempts,
			RetryAfterUncertainAttempt: retrySafe, Capacity: choreographyContactTestCapacity,
			DeadlineSeconds: choreographyContactTestDeadlineSeconds})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(persist, spec))
	require.NoError(t, err)
	first := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: interpretedContactNode,
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: NewInterpretedChoreographyPickupID(),
		ProposalID: interpretedContactFirstProposal}
	completeChoreographyFixtureClaim(t, persist, &first)
	accepted, err := broker.AcceptClaim(t.Context(), first)
	require.NoError(t, err)
	_, err = broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, ClaimOID: accepted.OID,
		AssignmentOID: accepted.AssignmentOID})
	require.NoError(t, err)
	return fixture, persist, binding, spec, broker, first, accepted
}

func TestInterpretedContactLossCannotRetryWithoutSignedSafety(t *testing.T) {
	_, _, _, spec, broker, first, accepted := interpretedContactFixture(t, false)
	require.ErrorContains(t, broker.MarkPickupContactLost(t.Context(), spec.Task, first.PickupID),
		choreographyPickupContactRetryUnsafeError)
	require.Equal(t, accepted.OID, broker.tasks[spec.Task].claim)
	require.Equal(t, spec.Weight, broker.used[spec.Budget])
	require.False(t, broker.tasks[spec.Task].pickups[first.PickupID].contactLost)
}

func TestInterpretedContactLossKeepsOldChargeAndSettlesOnlyLateSideAck(t *testing.T) {
	fixture, persist, binding, spec, broker, first, accepted := interpretedContactFixture(t, true)
	oldCancel := broker.tasks[spec.Task].pickups[first.PickupID].cancelOID
	deadline, err := time.Parse(time.RFC3339,
		broker.tasks[spec.Task].pickups[first.PickupID].cancelReplyDeadlineUTC)
	require.NoError(t, err)
	if remaining := time.Until(deadline.Add(choreographyContactTestClockMargin)); remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	require.NoError(t, broker.MarkPickupContactLost(t.Context(), spec.Task, first.PickupID))
	require.Equal(t, spec.Weight, broker.used[spec.Budget])
	second := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: broker.tasks[spec.Task].attempt,
		Node: first.Node, Branch: spec.Branch, ExpectedOID: broker.tasks[spec.Task].tip,
		InputsDigest: broker.tasks[spec.Task].inputsDigest,
		PickupID:     NewInterpretedChoreographyPickupID(), ProposalID: interpretedContactSecondProposal}
	require.NotEqual(t, first.PickupID, second.PickupID)
	completeChoreographyFixtureClaim(t, persist, &second)
	secondClaim, err := broker.AcceptClaim(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, choreographyContactTestCapacity*spec.Weight, broker.used[spec.Budget])
	secondResult := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: second.Attempt,
		Node: second.Node, Branch: spec.Branch}, Status: StatusSucceeded}
	secondProposal, secondPack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: second, Accepted: secondClaim,
			Result: secondResult, ProposalID: interpretedContactSecondResult})
	secondReceipt, err := broker.AcceptResult(t.Context(), secondProposal, secondPack)
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: second.Attempt, Node: second.Node, ResultOID: secondReceipt.OID,
		Succeeded: true}))
	winnerTip := broker.tasks[spec.Task].tip
	require.Equal(t, spec.Weight, broker.used[spec.Budget])
	oldResult := secondResult
	oldResult.Attempt = first.Attempt
	oldProposal, oldPack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: first, Accepted: accepted,
			Result: oldResult, ProposalID: interpretedContactOldResult})
	_, err = broker.AcceptResult(t.Context(), oldProposal, oldPack)
	require.ErrorContains(t, err, choreographyBrokerResultDoesNotAnswerTheActiveClaimError)
	require.Equal(t, winnerTip, broker.tasks[spec.Task].tip)
	oldAck, err := broker.AcceptAck(t.Context(), ChoreographyAckProposal{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, AssignmentOID: accepted.AssignmentOID,
		CancelOID: oldCancel, ProposalID: interpretedContactOldAck,
		Phase: PhaseAuthorizationWait})
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, ResultOID: oldAck.OID}))
	require.Zero(t, broker.used[spec.Budget])
	require.Equal(t, winnerTip, broker.tasks[spec.Task].tip)
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(coldPersist, spec))
	require.NoError(t, err)
	require.True(t, cold.tasks[spec.Task].terminal)
	require.Zero(t, cold.used[spec.Budget])
}
