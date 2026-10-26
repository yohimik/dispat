// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkerFindsExactRetainedCancellationAfterSuccessorClaim(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{
			AuthorityID:                "0123456789abcdef0123456789abcdef",
			RetryAfterUncertainAttempt: true,
			AdmissionSeconds:           choreographyTestAdmissionSeconds,
			Capacity:                   choreographyContactTestCapacity,
			DeadlineSeconds:            choreographyContactTestDeadlineSeconds})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(persist, spec))
	require.NoError(t, err)
	first := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt,
		Node: choreographyContactTestNode, Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: choreographyContactTestFirstProposal}
	completeChoreographyFixtureClaim(t, persist, &first)
	accepted, err := broker.AcceptClaim(t.Context(), first)
	require.NoError(t, err)
	assignment, err := fixture.mailbox.VerifyAssignmentCarriers(t.Context(),
		accepted.AssignmentOID, workerPickupTestDocumentLimit)
	require.NoError(t, err)
	require.NotNil(t, assignment)
	assignment.Limits = TransferLimits{MaxManifestBytes: workerPickupTestDocumentLimit,
		MaxPackBytes: workerPickupTestDocumentLimit}
	proposalDigest, err := ChoreographyProposalDigest(first)
	require.NoError(t, err)
	worker := &Worker{Node: first.Node, Endpoint: fixture.endpoint,
		Mailbox: fixture.mailbox, Report: NodeReport{Limits: assignment.Limits}}
	task := &claimedTask{assignment: *assignment, claimed: accepted.OID,
		tip: ChainTip{OID: accepted.AssignmentOID, Branch: spec.Branch, Kind: MessageAssignment},
		slotLease: NodeSlotLease{ID: first.LeaseIntentID, Intent: NodeSlotIntent{
			Run: first.Run, Task: first.Task, Attempt: first.Attempt,
			Weight: first.LeaseWeight, PickupID: first.PickupID,
			Recovery: &NodeSlotRecoveryLocator{
				Binding: binding, CanonicalEndpoint: fixture.endpoint,
				BootstrapOID: persist.opts.BootstrapOID, TaskBranch: spec.Branch,
				Kind: spec.Kind, ExpectedTaskOID: first.ExpectedOID,
				GraphRootOID: first.GraphRootOID, TemplateDigest: first.TemplateDigest,
				ExpectedLedgerOID: first.ExpectedLedgerOID,
				ProposalDigest:    proposalDigest, Node: first.Node}}}}
	require.True(t, validNodeSlotRecovery(task.slotLease.Intent))
	withdrawal, err := worker.readRetainedPickupCancellation(t.Context(), task)
	require.NoError(t, err)
	require.Empty(t, withdrawal.OID, "absence of a signed Cancel does not authorize an Ack")
	cancelOID, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node,
		ClaimOID: accepted.OID, AssignmentOID: accepted.AssignmentOID})
	require.NoError(t, err)
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
	second := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: broker.tasks[spec.Task].attempt,
		Node: first.Node, Branch: spec.Branch, ExpectedOID: broker.tasks[spec.Task].tip,
		InputsDigest: broker.tasks[spec.Task].inputsDigest,
		PickupID:     NewChoreographyPickupID(), ProposalID: choreographyContactTestSecondProposal}
	completeChoreographyFixtureClaim(t, persist, &second)
	successor, err := broker.AcceptClaim(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, successor.OID, remoteChoreographyOID(t, fixture, spec.Branch))

	withdrawal, err = worker.readRetainedPickupCancellation(t.Context(), task)
	require.NoError(t, err)
	require.Equal(t, cancelOID, withdrawal.OID)
	require.Equal(t, accepted.OID, withdrawal.PreviousOID)
	require.Equal(t, successor.OID, remoteChoreographyOID(t, fixture, spec.Branch),
		"reading the older pickup's cancellation preserves its successor")
	task.slotLease.Intent.PickupID = second.PickupID
	withdrawal, err = worker.readRetainedPickupCancellation(t.Context(), task)
	require.NoError(t, err)
	require.Empty(t, withdrawal.OID, "the old Cancel cannot settle a different pickup")
	task.slotLease.Intent.PickupID = first.PickupID
	task.slotLease.Intent.Recovery.Node = "foreign-node"
	_, err = worker.readRetainedPickupCancellation(t.Context(), task)
	require.ErrorContains(t, err, workerRetainedCancelIdentityError)
}
