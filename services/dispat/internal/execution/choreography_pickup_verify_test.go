// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	choreographyPickupProofTestNode          = "node-a"
	choreographyPickupProofTestClaimProposal = "pickup-proof-claim"
	choreographyPickupProofDocumentLimit     = 1 << 20
	choreographyPickupProofResultProposalID  = "pickup-proof-result"
	choreographyPickupProofPackPattern       = "pickup-proof-*.pack"
	choreographyPickupProofOneEventCount     = 1
)

func TestChoreographyPickupResultRequiresExactSignedPendingAndOutcomeRefs(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build"},
		ChoreographyTaskSpec{Task: "consumer", Predecessors: []string{"build"}, ExecutorOwned: true})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	identity := ChoreographyPickupIdentity{Run: binding.Run, Task: spec.Task,
		Node: choreographyPickupProofTestNode, PickupID: NewChoreographyPickupID()}
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: identity.Node,
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: identity.PickupID,
		ProposalID: choreographyPickupProofTestClaimProposal}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	pendingBranch, err := FormatChoreographyPendingBranch(identity)
	require.NoError(t, err)
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, StatusSucceeded)
	require.NoError(t, err)
	pending := Pending{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt,
		Node: identity.Node, Branch: pendingBranch},
		TaskBranch: spec.Branch, PickupID: identity.PickupID,
		ClaimOID: accepted.OID, AssignmentOID: accepted.AssignmentOID,
		GraphDigest: binding.GraphDigest, GraphRootOID: claim.GraphRootOID,
		InputsDigest: claim.InputsDigest, MembershipEpochOID: claim.MembershipEpochOID,
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
		Node: identity.Node, Branch: spec.Branch}, Assignment: accepted.OID,
		Status: StatusSucceeded, PickupID: identity.PickupID,
		PendingBranch: pendingBranch, PendingOID: pendingOID,
		OutcomeBranch: outcomeBranch}
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult,
		resultDocument, []string{pendingOID}, nil)
	require.NoError(t, err)
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: claim.Attempt, Node: identity.Node,
		Branch: spec.Branch, ClaimOID: accepted.OID, InputsDigest: claim.InputsDigest,
		PickupID: identity.PickupID, PendingBranch: pendingBranch,
		PendingOID: pendingOID, OutcomeBranch: outcomeBranch,
		ResultDigest: resultOID}
	proof := ChoreographyPickupResultProof{Proposal: proposal, Result: result,
		MaxDocumentBytes: choreographyPickupProofDocumentLimit}
	require.ErrorContains(t, fixture.mailbox.VerifyChoreographyPickupResult(t.Context(), proof),
		choreographyPickupProofOutcomeRefError)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, resultOID, outcomeBranch))
	require.NoError(t, fixture.mailbox.VerifyChoreographyPickupResult(t.Context(), proof))
	pack, err := os.CreateTemp(t.TempDir(), choreographyPickupProofPackPattern)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal.ProposalID = choreographyPickupProofResultProposalID
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack,
		choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.NotEqual(t, accepted.OID, acceptedResult.OID)
	page, err := persist.ReadEvents(t.Context(), accepted.Cursor,
		choreographyPickupProofOneEventCount)
	require.NoError(t, err)
	require.Len(t, page.Events, choreographyPickupProofOneEventCount)
	require.Equal(t, ChoreographyResultAccepted, page.Events[0].Kind)
	require.Equal(t, accepted.OID, page.Events[0].TaskOID)
	require.Equal(t, acceptedResult.OID, page.Events[0].ResultReceiptOID)
	require.Equal(t, resultOID, page.Events[0].OutputOID)
	recoveredResult, err := fixture.mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(
		t.Context(), binding, page.Events[0], choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	require.Equal(t, result.PickupID, recoveredResult.PickupID)
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(coldPersist))
	require.NoError(t, err)
	require.Equal(t, acceptedResult.OID, cold.tasks[spec.Task].result)
	require.Equal(t, accepted.OID, cold.tasks[spec.Task].tip)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task,
		Branch: spec.Branch, Attempt: claim.Attempt, Node: claim.Node,
		ResultOID: acceptedResult.OID, Succeeded: true}))
	finishedOID := remoteChoreographyOID(t, fixture, spec.Branch)
	require.NotEqual(t, accepted.OID, finishedOID)
	finalityPage, err := persist.ReadEvents(t.Context(), acceptedResult.Cursor,
		choreographyPickupProofOneEventCount)
	require.NoError(t, err)
	require.Len(t, finalityPage.Events, choreographyPickupProofOneEventCount)
	feed := &RetainedChoreographyFeed{reader: persist,
		maxDocumentBytes: choreographyPickupProofDocumentLimit}
	require.NoError(t, feed.VerifyAttemptFinality(t.Context(), ChoreographyAttemptFinalityProof{
		Event: finalityPage.Events[0], ReceiptOID: acceptedResult.OID,
		ClaimOID: accepted.OID, AssignmentOID: accepted.AssignmentOID}))
	finishedPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	finished, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(finishedPersist))
	require.NoError(t, err)
	require.True(t, finished.tasks[spec.Task].terminal)
	require.Equal(t, finishedOID, finished.tasks[spec.Task].tip)
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: binding, LedgerRootOID: finishedPersist.opts.Ledger.OID,
		Reader: finishedPersist, Mailbox: fixture.mailbox,
		MaxDocumentBytes: choreographyPickupProofDocumentLimit,
		Tasks:            finishedPersist.opts.Tasks,
		OutputContexts: map[string]ChoreographyBindingOutputContext{
			spec.Task: {},
		},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	dependent, err := cache.State(t.Context(), "consumer")
	require.NoError(t, err)
	require.Equal(t, []string{resultOID}, dependent.PredecessorTaskOIDs,
		"cold signed replay indexes the separate Result receipt and exposes the raw Result")

	wrongPickup := proof
	wrongPickup.Result.PickupID = NewChoreographyPickupID()
	require.ErrorContains(t, fixture.mailbox.VerifyChoreographyPickupResult(t.Context(), wrongPickup),
		choreographyPickupProofResultIdentityError)
	wrongDocument := proof
	wrongDocument.Result.Reason = choreographyPickupProofTestClaimProposal
	require.ErrorContains(t, fixture.mailbox.VerifyChoreographyPickupResult(t.Context(), wrongDocument),
		choreographyPickupProofOutcomeDocumentError)
	wrongLease := pending
	wrongLease.LeaseIntentID = spec.ReservationOID
	wrongLeaseDocument, err := json.Marshal(wrongLease)
	require.NoError(t, err)
	wrongLeaseOID, err := fixture.mailbox.commitMessage(t.Context(), MessagePending,
		wrongLeaseDocument, []string{accepted.OID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAdvance(t.Context(), fixture.endpoint,
		wrongLeaseOID, pendingBranch, pendingOID))
	require.ErrorContains(t, fixture.mailbox.VerifyChoreographyPickupResult(t.Context(), proof),
		choreographyPickupProofPendingRefError)
}
