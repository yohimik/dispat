// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	workerPickupTestNode          = "node-a"
	workerPickupTestProposal      = "worker-pickup-test-claim"
	workerPickupTestDocumentLimit = 1 << 20
	workerPickupTestRejectHook    = "#!/bin/sh\nexit 1\n"
)

type workerPickupFixture struct {
	worker     *Worker
	task       *claimedTask
	claim      ChoreographyClaimProposal
	assignment Assignment
	remote     *mailboxFixture
}

func (fixture workerPickupFixture) restartedWorker() *Worker {
	return &Worker{Node: fixture.worker.Node, Endpoint: fixture.worker.Endpoint,
		StateDir: fixture.worker.StateDir, Report: fixture.worker.Report,
		Mailbox: fixture.worker.Mailbox, Cache: fixture.worker.Cache,
		ReturnSigner: fixture.worker.ReturnSigner}
}

func newWorkerPickupFixture(t *testing.T) workerPickupFixture {
	t.Helper()
	remote, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: workerPickupTestNode,
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(),
		ProposalID: workerPickupTestProposal}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	assignment, err := remote.mailbox.VerifyAssignmentCarriers(t.Context(),
		receipt.AssignmentOID, workerPickupTestDocumentLimit)
	require.NoError(t, err)
	require.NotNil(t, assignment)
	lease := NodeSlotLease{ID: claim.LeaseIntentID, Intent: NodeSlotIntent{
		Run: claim.Run, Task: claim.Task, Attempt: claim.Attempt,
		Weight: claim.LeaseWeight, PickupID: claim.PickupID}}
	worker := &Worker{Node: claim.Node, Endpoint: remote.endpoint, StateDir: t.TempDir(),
		Report: NodeReport{AuthorityID: claim.CapacityAuthorityID,
			Limits: TransferLimits{MaxManifestBytes: workerPickupTestDocumentLimit}},
		Mailbox: remote.mailbox, Cache: remote.git, ReturnSigner: remote.signer}
	return workerPickupFixture{worker: worker, task: &claimedTask{
		assignment: *assignment, claimed: receipt.OID,
		tip: ChainTip{OID: receipt.AssignmentOID, Branch: spec.Branch,
			Kind: MessageAssignment}, slotLease: lease},
		claim: claim, assignment: *assignment, remote: remote}
}

func TestWorkerPickupPersistsExactPendingBeforePushAndReusesItAfterRestart(t *testing.T) {
	fixture := newWorkerPickupFixture(t)
	hook := filepath.Join(fixture.remote.endpoint, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte(workerPickupTestRejectHook), 0o700))
	_, err := fixture.worker.ensurePickupPending(t.Context(), fixture.task)
	require.Error(t, err, "a rejected create cannot authorize command effects")
	state, found, err := fixture.worker.readPickupState(fixture.task.slotLease.ID)
	require.NoError(t, err)
	require.True(t, found, "the exact pending object is durable before its create push")
	require.NotEmpty(t, state.PendingOID)
	require.Equal(t, fixture.claim.PickupID, state.PickupID)
	info, err := os.Stat(fixture.worker.pickupStatePath(fixture.task.slotLease.ID))
	require.NoError(t, err)
	require.Equal(t, workerPickupFileMode, info.Mode().Perm(), "the local pickup journal is private")
	require.NoError(t, os.Remove(hook))

	restarted := fixture.restartedWorker()
	confirmed, err := restarted.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	require.Equal(t, state.PendingOID, confirmed.PendingOID,
		"restart retries the same signed object and pickup identity")
	second, err := restarted.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	require.Equal(t, confirmed, second, "a repeated call neither creates a new pickup nor moves its ref")
	tip, err := restarted.pickupRemoteTip(t.Context(), confirmed.PendingBranch)
	require.NoError(t, err)
	require.Equal(t, confirmed.PendingOID, tip)
	parents, err := fixture.remote.mailbox.plumbing.CommitParents(t.Context(), tip)
	require.NoError(t, err)
	require.Equal(t, []string{fixture.task.claimed}, parents)
}

func TestWorkerPickupReconcilesAppliedCreateWithLostReply(t *testing.T) {
	fixture := newWorkerPickupFixture(t)
	hook := filepath.Join(fixture.remote.endpoint, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte(workerPickupTestRejectHook), 0o700))
	_, err := fixture.worker.ensurePickupPending(t.Context(), fixture.task)
	require.Error(t, err)
	state, found, err := fixture.worker.readPickupState(fixture.task.slotLease.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, os.Remove(hook))
	require.NoError(t, fixture.remote.git.PushCreate(t.Context(), fixture.remote.endpoint,
		state.PendingOID, state.PendingBranch), "the remote applied the original exact create")

	restarted := fixture.restartedWorker()
	confirmed, err := restarted.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	require.Equal(t, state, confirmed,
		"the worker reads back its retained ref and never creates a second pickup")
}

func TestWorkerPickupOutcomeDirectlyDescendsFromConfirmedPending(t *testing.T) {
	fixture := newWorkerPickupFixture(t)
	_, _, err := fixture.worker.publishPickupOutcome(t.Context(), pickupOutcomeRequest{
		Task: fixture.task, Report: Result{Header: fixture.assignment.Header,
			Assignment: fixture.task.claimed, Status: StatusSucceeded}})
	require.ErrorContains(t, err, workerPickupPendingMissingError,
		"an outcome cannot be written before the worker's pending branch is confirmed")
	state, err := fixture.worker.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	result := Result{Header: fixture.assignment.Header,
		Assignment: fixture.task.claimed, Status: StatusSucceeded}
	result, resultOID, err := fixture.worker.publishPickupOutcome(t.Context(), pickupOutcomeRequest{
		Task: fixture.task, Report: result})
	require.NoError(t, err)
	require.NotEmpty(t, resultOID)
	require.Equal(t, state.PickupID, result.PickupID)
	require.Equal(t, state.PendingOID, result.PendingOID)
	tip, err := fixture.worker.pickupRemoteTip(t.Context(), result.OutcomeBranch)
	require.NoError(t, err)
	require.Equal(t, resultOID, tip)
	parents, err := fixture.remote.mailbox.plumbing.CommitParents(t.Context(), resultOID)
	require.NoError(t, err)
	require.Equal(t, []string{state.PendingOID}, parents)
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: fixture.claim.ChoreographyRunBinding,
		Task: fixture.claim.Task, Attempt: fixture.claim.Attempt, Node: fixture.claim.Node,
		Branch: fixture.claim.Branch, ClaimOID: fixture.task.claimed,
		InputsDigest: fixture.claim.InputsDigest, PickupID: fixture.claim.PickupID,
		PendingBranch: state.PendingBranch, PendingOID: state.PendingOID,
		OutcomeBranch: result.OutcomeBranch, ResultDigest: resultOID}
	require.NoError(t, fixture.remote.mailbox.VerifyChoreographyPickupResult(t.Context(),
		ChoreographyPickupResultProof{Proposal: proposal, Result: result,
			MaxDocumentBytes: workerPickupTestDocumentLimit}))

	restarted := fixture.restartedWorker()
	again, sameOID, err := restarted.publishPickupOutcome(t.Context(), pickupOutcomeRequest{
		Task: fixture.task, Report: Result{Header: fixture.assignment.Header,
			Assignment: fixture.task.claimed, Status: StatusSucceeded}})
	require.NoError(t, err)
	require.Equal(t, resultOID, sameOID)
	require.Equal(t, result, again)
	changed := result
	changed.Reason = workerPickupTestProposal
	_, _, err = restarted.publishPickupOutcome(t.Context(), pickupOutcomeRequest{
		Task: fixture.task, Report: changed})
	require.ErrorContains(t, err, workerPickupResultMismatchError,
		"an unknown reply cannot be replaced by a different signed result")
	state, found, err := restarted.readPickupState(fixture.task.slotLease.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, state.ResultDocumentDigest)
}

func TestWorkerPickupFailedOutcomeUsesRetainedFailedBranch(t *testing.T) {
	fixture := newWorkerPickupFixture(t)
	state, err := fixture.worker.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	result, oid, err := fixture.worker.publishPickupOutcome(t.Context(), pickupOutcomeRequest{
		Task: fixture.task, Report: Result{Header: fixture.assignment.Header,
			Assignment: fixture.task.claimed, Status: StatusFailed,
			FailedPart: fixture.assignment.Kind}})
	require.NoError(t, err)
	require.Contains(t, result.OutcomeBranch, choreographyPickupFailedBranchPrefix)
	tip, err := fixture.worker.pickupRemoteTip(t.Context(), result.OutcomeBranch)
	require.NoError(t, err)
	require.Equal(t, oid, tip)
	parents, err := fixture.remote.mailbox.plumbing.CommitParents(t.Context(), tip)
	require.NoError(t, err)
	require.Equal(t, []string{state.PendingOID}, parents)
}
