// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	choreographyContactTestCapacity        = 2
	choreographyContactTestDeadlineSeconds = 1
	choreographyContactTestClockMargin     = 50 * time.Millisecond
	choreographyContactTestFirstProposal   = "contact-first-claim"
	choreographyContactTestSecondProposal  = "contact-successor-claim"
	choreographyContactTestSecondResult    = "contact-successor-result"
	choreographyContactTestOldAck          = "contact-old-ack"
	choreographyContactTestNode            = "node-a"
)

func choreographyContactBrokerOptions(persist *GitChoreographyPersist,
	spec ChoreographyTaskSpec) FirstNodeBrokerOptions {
	opts := choreographyFixtureBrokerOptions(persist)
	opts.Members[0].Capacity = choreographyContactTestCapacity
	opts.NodeCapacity[opts.Members[0].Name] = choreographyContactTestCapacity
	opts.Budgets[spec.Budget] = choreographyContactTestCapacity
	opts.CancelResponseDuration = time.Duration(choreographyContactTestDeadlineSeconds) * time.Second
	return opts
}

func TestChoreographyPostCancelContactLossRetainsOldChargeAndAcceptsLateAck(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{
			AuthorityID:                "0123456789abcdef0123456789abcdef",
			AdmissionSeconds:           choreographyTestAdmissionSeconds,
			RetryAfterUncertainAttempt: true,
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
	firstClaim, err := broker.AcceptClaim(t.Context(), first)
	require.NoError(t, err)
	cancelIssuedAfter := time.Now().UTC()
	firstCancel, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task,
		Branch: spec.Branch, Attempt: first.Attempt, Node: first.Node,
		ClaimOID: firstClaim.OID, AssignmentOID: firstClaim.AssignmentOID})
	require.NoError(t, err)
	deadlineText := broker.tasks[spec.Task].pickups[first.PickupID].cancelReplyDeadlineUTC
	require.NotEmpty(t, deadlineText)
	deadline, err := time.Parse(time.RFC3339, deadlineText)
	require.NoError(t, err)
	require.False(t, deadline.Before(cancelIssuedAfter.Add(time.Duration(choreographyContactTestDeadlineSeconds)*time.Second)))
	lossErr := broker.MarkPickupContactLost(t.Context(), spec.Task, first.PickupID)
	require.ErrorContains(t, lossErr, choreographyPickupContactNotDueError)
	var refused *choreographyPickupContactDeadlineRefusal
	require.ErrorAs(t, lossErr, &refused)
	require.Equal(t, spec.Task, refused.task)
	require.Equal(t, first.PickupID, refused.pickupID)
	require.Equal(t, deadline, refused.deadline)
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
	require.Equal(t, firstCancel, remoteChoreographyOID(t, fixture, spec.Branch))
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(coldPersist, spec))
	require.NoError(t, err)
	require.True(t, cold.tasks[spec.Task].pickups[first.PickupID].contactLost)
	require.Equal(t, deadlineText,
		cold.tasks[spec.Task].pickups[first.PickupID].cancelReplyDeadlineUTC)
	second := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: cold.tasks[spec.Task].attempt,
		Node: first.Node, Branch: spec.Branch, ExpectedOID: cold.tasks[spec.Task].tip,
		InputsDigest: cold.tasks[spec.Task].inputsDigest,
		PickupID:     NewChoreographyPickupID(), ProposalID: choreographyContactTestSecondProposal}
	completeChoreographyFixtureClaim(t, coldPersist, &second)
	secondClaim, err := cold.AcceptClaim(t.Context(), second)
	require.NoError(t, err)
	require.Equal(t, choreographyContactTestCapacity*spec.Weight, cold.used[spec.Budget])
	oldAck, err := cold.AcceptAck(t.Context(), ChoreographyAckProposal{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node,
		AssignmentOID: firstClaim.AssignmentOID, CancelOID: firstCancel,
		ProposalID: choreographyContactTestOldAck, Phase: PhaseAuthorizationWait})
	require.NoError(t, err)
	require.Equal(t, first.PickupID, oldAck.PickupID)
	require.Equal(t, secondClaim.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.NoError(t, cold.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, ResultOID: oldAck.OID}))
	require.Equal(t, spec.Weight, cold.used[spec.Budget])
	page, err := coldPersist.ReadEvents(t.Context(), oldAck.Cursor, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyPickupSettled, page.Events[0].Kind)
	feed := &RetainedChoreographyFeed{reader: coldPersist,
		maxDocumentBytes: choreographyPickupProofDocumentLimit}
	require.NoError(t, feed.VerifyAttemptFinality(t.Context(), ChoreographyAttemptFinalityProof{
		Event: page.Events[0], ReceiptOID: oldAck.OID,
		ClaimOID: firstClaim.OID, AssignmentOID: firstClaim.AssignmentOID}))
	secondResult := choreographyContactAcceptPickupResult(t, fixture, cold, binding,
		spec, second, secondClaim)
	require.NoError(t, cold.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: second.Attempt, Node: second.Node, ResultOID: secondResult.OID,
		Succeeded: true}))
	_, err = cold.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, ClaimOID: firstClaim.OID,
		AssignmentOID: firstClaim.AssignmentOID})
	require.ErrorIs(t, err, ErrChoreographyTaskAlreadyFinal)
	require.Zero(t, cold.used[spec.Budget])
	_, err = cold.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = coldPersist.SealRun(t.Context(), true)
	require.NoError(t, err)
	sealedPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	sealed, err := RecoverFirstNodeBroker(t.Context(), choreographyContactBrokerOptions(sealedPersist, spec))
	require.NoError(t, err)
	require.True(t, sealed.tasks[spec.Task].terminal)
	require.Zero(t, sealed.used[spec.Budget])
	_, graphRootOID, err := fixture.mailbox.ReadChoreographyGraphCarrier(t.Context(),
		binding, persist.opts.BootstrapOID, choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	require.Equal(t, persist.opts.GraphRootOID, graphRootOID)
}

func choreographyContactAcceptPickupResult(t *testing.T, fixture *mailboxFixture,
	broker *FirstNodeBroker, binding ChoreographyRunBinding, spec ChoreographyTaskSpec,
	claim ChoreographyClaimProposal, accepted ChoreographyReceipt) ChoreographyReceipt {
	t.Helper()
	identity := ChoreographyPickupIdentity{Run: binding.Run, Task: spec.Task,
		Node: claim.Node, PickupID: claim.PickupID}
	pendingBranch, err := FormatChoreographyPendingBranch(identity)
	require.NoError(t, err)
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, StatusSucceeded)
	require.NoError(t, err)
	pending := Pending{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: pendingBranch}, TaskBranch: spec.Branch,
		PickupID: claim.PickupID, ClaimOID: accepted.OID,
		AssignmentOID: accepted.AssignmentOID, GraphDigest: binding.GraphDigest,
		GraphRootOID: claim.GraphRootOID, InputsDigest: claim.InputsDigest,
		MembershipEpochOID:  claim.MembershipEpochOID,
		CapacityAuthorityID: claim.CapacityAuthorityID,
		LeaseIntentID:       claim.LeaseIntentID, LeaseWeight: claim.LeaseWeight}
	pendingDocument, err := json.Marshal(pending)
	require.NoError(t, err)
	pendingOID, err := fixture.mailbox.commitMessage(t.Context(), MessagePending,
		pendingDocument, []string{accepted.OID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, pendingOID, pendingBranch))
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: spec.Branch}, Assignment: accepted.OID,
		Status: StatusSucceeded, PickupID: claim.PickupID,
		PendingBranch: pendingBranch, PendingOID: pendingOID,
		OutcomeBranch: outcomeBranch}
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult,
		resultDocument, []string{pendingOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, resultOID, outcomeBranch))
	pack, err := os.CreateTemp(t.TempDir(), choreographyPickupProofPackPattern)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: claim.Attempt, Node: claim.Node,
		Branch: spec.Branch, ClaimOID: accepted.OID,
		InputsDigest: claim.InputsDigest, PickupID: claim.PickupID,
		PendingBranch: pendingBranch, PendingOID: pendingOID,
		OutcomeBranch: outcomeBranch, ResultDigest: resultOID,
		ProposalID: choreographyContactTestSecondResult}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack,
		choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	receipt, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	return receipt
}
