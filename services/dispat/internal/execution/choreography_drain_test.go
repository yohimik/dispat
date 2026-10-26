// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyDrainPersistsSignedMembershipAndReplays(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 3600,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: f.binding, Mailbox: f.endpoint.mailbox, Ledger: ChoreographyLedgerReservation{
			Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
	})
	require.NoError(t, err)
	first := ChoreographyRuntimeNode{Name: "first", Platform: "linux/amd64", Capacity: 1,
		First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}
	peer := ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1,
		AuthorityID: "11111111111111111111111111111111"}
	opts := FirstNodeBrokerOptions{Binding: f.binding, GraphRootOID: f.root,
		InitialMembershipEpochOID: f.complete, FirstNode: "first", Tasks: f.carrier.Tasks,
		Templates: f.carrier.Templates, Members: []ChoreographyRuntimeNode{first, peer},
		NodeCapacity: map[string]int{"first": 1, "peer": 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy, Persist: persist}
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	proposal := ChoreographyDrainProposal{ChoreographyRunBinding: f.binding,
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, Node: peer.Name,
		CapacityAuthorityID: peer.AuthorityID, MembershipEpochOID: f.complete,
		ExpectedLedgerOID: f.complete, RequestID: "peer-normal-stop"}
	receipt, err := broker.DrainNode(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.False(t, receipt.Refused)
	require.NotEmpty(t, receipt.Cursor.OID)
	require.True(t, broker.draining[peer.Name])
	replayed, err := broker.DrainNode(t.Context(), proposal)
	require.NoError(t, err)
	require.Equal(t, receipt.Cursor, replayed.Cursor)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.draining[peer.Name])
	require.Equal(t, receipt.Cursor.OID, recovered.memberEpoch[peer.Name])
}

func TestChoreographyStoppingRefusesDrainWhileClaimRemainsSettledByAck(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 3600,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: f.binding, Mailbox: f.endpoint.mailbox, Ledger: ChoreographyLedgerReservation{
			Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
	})
	require.NoError(t, err)
	first := ChoreographyRuntimeNode{Name: "first", Platform: "linux/amd64", Capacity: 1,
		First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}
	peer := ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1,
		AuthorityID: "11111111111111111111111111111111"}
	sourceStore := gitx.NewPlumbing(f.endpoint.git)
	sourceTree := sourceStore.MakeTree(t.Context(), nil)
	sourceOID := sourceStore.CommitTree(t.Context(), sourceTree, nil, "drain-claim-source")
	require.NoError(t, sourceStore.Err())
	resolved := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{Path: ".", Snapshot: sourceOID}}}
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: f.binding,
		GraphRootOID: f.root, InitialMembershipEpochOID: f.complete, FirstNode: "first",
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates,
		Members:      []ChoreographyRuntimeNode{first, peer},
		NodeCapacity: map[string]int{"first": 1, "peer": 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy, Persist: persist,
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) {
			return resolved, nil
		}})
	require.NoError(t, err)
	_, err = broker.StartExecution(t.Context())
	require.NoError(t, err)
	inputsDigest, _, err := broker.ExpectedInputIdentity(f.spec.Task)
	require.NoError(t, err)
	head, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	bindings := ChoreographyTaskBindings{}
	bindingsDigest, err := DigestChoreographyTaskBindings(bindings)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding,
		GraphRootOID: f.root, Task: f.spec.Task, Attempt: 1, Node: peer.Name,
		Branch: f.spec.Branch, ExpectedOID: f.spec.ReservationOID,
		ExpectedLedgerOID: head.OID, InputsDigest: inputsDigest, ProposalID: "claim-before-stop",
		PickupID:           NewChoreographyPickupID(),
		MembershipEpochOID: f.complete, CapacityAuthorityID: peer.AuthorityID,
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: f.binding.Run, Task: f.spec.Task, Attempt: 1}),
		LeaseWeight:   f.spec.Weight, TemplateDigest: f.spec.TemplateDigest,
		Bindings: bindings, BindingsDigest: bindingsDigest}
	claim.LeaseIntentID = nodeSlotLeaseID(NodeSlotIntent{Run: f.binding.Run,
		Task: f.spec.Task, Attempt: claim.Attempt, PickupID: claim.PickupID})
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.Equal(t, 1, broker.nodeUsed[peer.Name])
	stop, err := broker.StopClaims(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, broker.WaitClaimBudget(t.Context(), f.spec.Task, peer.Name), ErrChoreographyRunStopping)
	drain := ChoreographyDrainProposal{ChoreographyRunBinding: f.binding,
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, Node: peer.Name,
		CapacityAuthorityID: peer.AuthorityID, MembershipEpochOID: f.complete,
		ExpectedLedgerOID: stop.OID, RequestID: "stop-raced-peer-drain"}
	refused, err := broker.DrainNode(t.Context(), drain)
	require.NoError(t, err)
	require.False(t, refused.Accepted)
	require.True(t, refused.Refused)
	require.Equal(t, choreographyDrainRefusalStopping, refused.Reason)
	require.Equal(t, stop, refused.Cursor)
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{OID: f.complete, Sequence: 1}, 4)
	require.NoError(t, err)
	require.Equal(t, []ChoreographyEventKind{ChoreographyExecutionStarted,
		ChoreographyClaimAccepted, ChoreographyRunStopping},
		[]ChoreographyEventKind{page.Events[0].Kind, page.Events[1].Kind, page.Events[2].Kind})
	require.Equal(t, stop.OID, page.Events[2].LedgerOID)
	require.False(t, broker.draining[peer.Name])
	require.Equal(t, accepted.OID, broker.tasks[f.spec.Task].claim)
	require.Equal(t, 1, broker.nodeUsed[peer.Name], "stopping does not release the accepted physical claim")

	later := claim
	later.ProposalID = "claim-after-stop"
	later.ExpectedOID = accepted.OID
	later.ExpectedLedgerOID = stop.OID
	blocked, err := broker.AcceptClaim(t.Context(), later)
	require.NoError(t, err)
	require.True(t, blocked.Refused)
	require.Equal(t, ChoreographyRefusalStopping, blocked.RefusalReason)
	require.Equal(t, 1, broker.nodeUsed[peer.Name])

	cancelOID, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: f.binding, Task: f.spec.Task, Branch: f.spec.Branch,
		Attempt: claim.Attempt, Node: peer.Name, AssignmentOID: accepted.AssignmentOID,
		ClaimOID: accepted.OID})
	require.NoError(t, err)
	acked, err := broker.AcceptAck(t.Context(), ChoreographyAckProposal{
		ChoreographyRunBinding: f.binding, Task: f.spec.Task, Branch: f.spec.Branch,
		Attempt: claim.Attempt, Node: peer.Name, AssignmentOID: accepted.AssignmentOID,
		CancelOID: cancelOID, ProposalID: "ack-after-stop", Phase: PhaseAuthorizationWait})
	require.NoError(t, err)
	require.True(t, acked.Accepted)
	require.Equal(t, 1, broker.nodeUsed[peer.Name])
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: f.binding, Task: f.spec.Task, Branch: f.spec.Branch,
		Attempt: claim.Attempt, Node: peer.Name, ResultOID: acked.OID}))
	require.Zero(t, broker.nodeUsed[peer.Name])
}
