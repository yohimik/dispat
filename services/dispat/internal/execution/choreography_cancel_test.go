// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func newCompiledCancellationFixture(t *testing.T) (*mailboxFixture, *GitChoreographyPersist,
	*FirstNodeBroker, ChoreographyRunBinding, ChoreographyTaskSpec, ChoreographyAssignmentReceipt) {
	t.Helper()
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	return fixture, persist, broker, binding, spec,
		ChoreographyAssignmentReceipt{AssignmentOID: spec.ReservationOID,
			Cursor: choreographyFixtureStartCursor(t, persist)}
}

func TestCompiledNoClaimExpiryRetainsCanonicalTaskRefWithoutRetry(t *testing.T) {
	fixture, persist, _, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{{Task: "build", Placement: PlacementWorker}}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, broker.admissionDeadlineUTC)
	require.NoError(t, err)
	time.Sleep(time.Until(deadline) + time.Millisecond)
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
	require.True(t, broker.tasks[spec.Task].terminal)
	require.Equal(t, 1, broker.tasks[spec.Task].attempt)
	require.Empty(t, broker.tasks[spec.Task].claim)
	require.Zero(t, broker.nodeUsed["node-a"])
	require.Equal(t, broker.tasks[spec.Task].tip, remoteChoreographyOID(t, fixture, spec.Branch))
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyNoClaimExpired, page.Events[0].Kind)
	require.Empty(t, page.Events[0].ResultReceiptOID)
	recovered, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	require.True(t, recovered.tasks[spec.Task].terminal)
}

func TestCompiledCancellationRequiresExactAckBeforeRetry(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "claim-cancel"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	cancel := ChoreographyCancellation{ChoreographyRunBinding: binding,
		Task: spec.Task, Branch: spec.Branch, Attempt: 1, Node: "node-a",
		AssignmentOID: receipt.AssignmentOID, ClaimOID: receipt.OID}
	cancelOID, err := broker.CancelClaim(t.Context(), cancel)
	require.NoError(t, err)
	require.Equal(t, cancelOID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, 1, broker.nodeUsed["node-a"])
	require.ErrorContains(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", ResultOID: cancelOID, Retry: true}), "invalid attempt decision")
	ack := ChoreographyAckProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Branch: spec.Branch, Attempt: 1, Node: "node-a",
		AssignmentOID: receipt.AssignmentOID, CancelOID: cancelOID,
		ProposalID: "ack-cancel", Phase: PhaseAuthorizationWait}
	acked, err := broker.AcceptAck(t.Context(), ack)
	require.NoError(t, err)
	require.True(t, acked.Accepted)
	require.Equal(t, 1, broker.nodeUsed["node-a"])
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", ResultOID: acked.OID, Retry: true}))
	require.Equal(t, 0, broker.nodeUsed["node-a"])
	require.Equal(t, 2, broker.tasks[spec.Task].attempt)
	page, err := persist.ReadEvents(t.Context(), offered.Cursor, 10)
	require.NoError(t, err)
	require.Equal(t, []ChoreographyEventKind{ChoreographyClaimAccepted,
		ChoreographyCancelIssued, ChoreographyAckAccepted, ChoreographyRetryReady},
		[]ChoreographyEventKind{page.Events[0].Kind, page.Events[1].Kind,
			page.Events[2].Kind, page.Events[3].Kind})
}

func TestCompiledCancelWithoutAckCannotSealOrRetry(t *testing.T) {
	_, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "claim-no-ack"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	_, err = broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", AssignmentOID: receipt.AssignmentOID,
		ClaimOID: receipt.OID})
	require.NoError(t, err)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.SealRun(t.Context(), true)
	require.ErrorContains(t, err, "active claim")
	require.ErrorContains(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", ResultOID: receipt.OID, Retry: true}),
		"invalid attempt decision")
	require.Equal(t, 1, broker.nodeUsed["node-a"])
}

func TestCompiledNoClaimExpirySeparatesTaskFinalityAndPackageSettlement(t *testing.T) {
	fixture, persist, _, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{{Task: "build", Package: "core",
				Placement: PlacementWorker}}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, broker.admissionDeadlineUTC)
	require.NoError(t, err)
	time.Sleep(time.Until(deadline) + time.Millisecond)
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
	noClaimOID := broker.tasks[spec.Task].tip
	require.Equal(t, noClaimOID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.True(t, broker.IsNoClaimExpired(spec.Task), "package hooks have not settled")
	require.Empty(t, broker.packageState[spec.Package])
	require.NoError(t, broker.SettleNoClaimFailure(t.Context(), spec.Task, "failed"))
	require.Equal(t, noClaimOID, remoteChoreographyOID(t, fixture, spec.Branch),
		"package settlement must not rewrite typed no-claim task finality")
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, ChoreographyNoClaimExpired, page.Events[0].Kind)
	require.Equal(t, ChoreographyNoClaimPackageSettled, page.Events[1].Kind)
	require.Equal(t, noClaimOID, page.Events[1].TaskOID)
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.tasks[spec.Task].terminal)
	require.Equal(t, "failed", recovered.packageState[spec.Package])
}

func TestRetainedResultVerifierUsesSignedClaimAndAssignment(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	bootstrap, err := fixture.mailbox.ReadChoreographyBootstrap(t.Context(),
		persist.opts.BootstrapOID, 1<<20, 1<<20)
	require.NoError(t, err)
	require.Len(t, bootstrap.PinnedInputs.Repositories, 1)
	source := bootstrap.PinnedInputs.Repositories[0]
	bindings := ChoreographyTaskBindings{Repositories: []AssignmentRepository{source}}
	broker.resolveBindings = func(context.Context, string, string) (ChoreographyTaskBindings, error) {
		return bindings, nil
	}
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "retained-result"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	claim.resolvedBindings = &bindings
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	assignmentParents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), receipt.AssignmentOID)
	require.NoError(t, err)
	require.Equal(t, []string{spec.ReservationOID, source.Snapshot}, assignmentParents)
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ClaimOID: receipt.OID, InputsDigest: claim.InputsDigest}
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch}, Assignment: receipt.OID,
		Status: StatusSucceeded}
	require.NoError(t, fixture.mailbox.VerifyChoreographyResultFromClaim(t.Context(),
		ChoreographyResultClaimProof{Proposal: proposal, Result: result,
			Store: fixture.git, MaxDocumentBytes: 1 << 20}))
	result.Node = "another-node"
	require.ErrorContains(t, fixture.mailbox.VerifyChoreographyResultFromClaim(t.Context(),
		ChoreographyResultClaimProof{Proposal: proposal, Result: result,
			Store: fixture.git, MaxDocumentBytes: 1 << 20}), "retained signed command frame")
	result.Node = "node-a"
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, resultDocument, nil, nil)
	require.NoError(t, err)
	pack, err := os.CreateTemp(t.TempDir(), "carried-result-*.pack")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal.ProposalID, proposal.ResultDigest = "retained-carried-result", resultOID
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack, 1<<20)
	require.NoError(t, err)
	persist.opts.VerifyResult = func(ctx context.Context, p ChoreographyResultProposal,
		result Result, store *gitx.LocalGitx) error {
		return fixture.mailbox.VerifyChoreographyResultFromClaim(ctx,
			ChoreographyResultClaimProof{Proposal: p, Result: result,
				Store: store, MaxDocumentBytes: 1 << 20})
	}
	accepted, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	recovered, err := fixture.mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(
		t.Context(), binding, page.Events[1], 1<<20)
	require.NoError(t, err)
	require.Equal(t, result, recovered)
}

func TestCarriedSourceClaimCancelSurvivesColdBrokerReplay(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	bootstrap, err := fixture.mailbox.ReadChoreographyBootstrap(t.Context(),
		persist.opts.BootstrapOID, 1<<20, 1<<20)
	require.NoError(t, err)
	require.Len(t, bootstrap.PinnedInputs.Repositories, 1)
	bindings := ChoreographyTaskBindings{Repositories: bootstrap.PinnedInputs.Repositories}
	broker.resolveBindings = func(context.Context, string, string) (ChoreographyTaskBindings, error) {
		return bindings, nil
	}
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, ExpectedLedgerOID: offered.Cursor.OID,
		InputsDigest: spec.InputsDigest, ProposalID: "carried-source-cancel"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	parents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), accepted.AssignmentOID)
	require.NoError(t, err)
	require.Equal(t, []string{spec.ReservationOID, bootstrap.PinnedInputs.Repositories[0].Snapshot}, parents)
	cancel := ChoreographyCancellation{ChoreographyRunBinding: binding,
		Task: spec.Task, Branch: spec.Branch, Attempt: 1, Node: "node-a",
		AssignmentOID: accepted.AssignmentOID, ClaimOID: accepted.OID}
	cancelOID, err := broker.CancelClaim(t.Context(), cancel)
	require.NoError(t, err)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	recovered, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(restarted))
	require.NoError(t, err)
	require.Equal(t, accepted.OID, recovered.tasks[spec.Task].claim)
	require.Equal(t, cancelOID, recovered.tasks[spec.Task].cancel)
	require.Equal(t, 1, recovered.nodeUsed["node-a"])
}
