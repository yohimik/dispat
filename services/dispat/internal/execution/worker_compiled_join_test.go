// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/ed25519"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type staleCompiledJoinClient struct {
	heads     []string
	proposals []ChoreographyJoinProposal
}

type compiledCapacityClaimClient struct {
	*staleCompiledJoinClient
	scope string
}

func (c *compiledCapacityClaimClient) SubmitClaim(_ context.Context,
	p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	query, err := FormatClaimReceiptQuery(p)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyClaimKind, Task: p.Task, Attempt: p.Attempt, Node: p.Node,
		Branch: p.Branch, GraphRootOID: p.GraphRootOID,
		ProposalID: p.ProposalID, ProposalDigest: query.ProposalDigest,
		OID: p.ExpectedOID, Refused: true, FenceOID: "ledger-new", FenceSequence: 2,
		RefusalReason: ChoreographyRefusalCapacity, CapacityScope: c.scope,
		Budget: ChoreographyBudgetBuild}, nil
}

func (*staleCompiledJoinClient) SubmitProbe(context.Context, ChoreographyProbeReply) (ChoreographyReceipt, error) {
	return ChoreographyReceipt{}, nil
}
func (*staleCompiledJoinClient) SubmitClaim(context.Context, ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	return ChoreographyReceipt{}, nil
}
func (*staleCompiledJoinClient) SubmitResult(context.Context, ChoreographyResultProposal, io.ReadSeeker) (ChoreographyReceipt, error) {
	return ChoreographyReceipt{}, nil
}
func (*staleCompiledJoinClient) SubmitAck(context.Context, ChoreographyAckProposal) (ChoreographyReceipt, error) {
	return ChoreographyReceipt{}, nil
}
func (*staleCompiledJoinClient) LookupReceipt(context.Context, ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	return ChoreographyReceipt{}, false, nil
}
func (c *staleCompiledJoinClient) PollEvents(context.Context, ChoreographyEventRequest) (ChoreographyEventPage, error) {
	index := len(c.proposals)
	if index >= len(c.heads) {
		index = len(c.heads) - 1
	}
	return ChoreographyEventPage{Head: ChoreographyEventCursor{OID: c.heads[index]}}, nil
}
func (*staleCompiledJoinClient) Close() {}
func (c *staleCompiledJoinClient) SubmitJoin(_ context.Context,
	p ChoreographyJoinProposal) (ChoreographyJoinReceipt, error) {
	c.proposals = append(c.proposals, p)
	query, err := FormatChoreographyJoinReceiptQuery(p)
	if err != nil {
		return ChoreographyJoinReceipt{}, err
	}
	receipt := ChoreographyJoinReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		BootstrapOID: p.BootstrapOID, GraphRootOID: p.GraphRootOID, Node: p.Node,
		RequestID: p.RequestID, RequestDigest: query.RequestDigest}
	if len(c.proposals) == 1 {
		receipt.Refused, receipt.Reason = true, choreographyJoinRefusalStale
		receipt.Cursor = ChoreographyEventCursor{OID: c.heads[1], Sequence: 2}
	} else {
		receipt.Accepted = true
		receipt.Member = ChoreographyRuntimeNode{Name: p.Node,
			Platform: p.Report.OS + nodePlatformSeparator + p.Report.Arch,
			Capacity: p.Report.Capacity, AuthorityID: p.Report.AuthorityID}
		receipt.Cursor = ChoreographyEventCursor{OID: c.heads[2], Sequence: 3}
	}
	return receipt, nil
}
func (*staleCompiledJoinClient) LookupJoin(context.Context, ChoreographyJoinReceiptQuery) (ChoreographyJoinReceipt, bool, error) {
	return ChoreographyJoinReceipt{}, false, nil
}

func TestCompiledLateJoinReproposesOnlyAfterDefinitiveStaleReceipt(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	client := &staleCompiledJoinClient{heads: []string{"head-old", "head-new", "head-joined"}}
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	w := &Worker{Node: "late", Endpoint: "https://canonical.example",
		Report: NodeReport{Protocol: ProtocolVersion, OS: "linux", Arch: "amd64", Capacity: 1,
			AuthorityID: "authority"}, JoinIdentityKey: key}
	run := &workerCompiledRun{binding: binding, bootstrapOID: "bootstrap",
		rootOID: "complete-root", bootstrap: RunBootstrap{
			AlgorithmVersion: ChoreographyGraphAlgorithmVersion},
		carrier: ChoreographyGraphCarrier{Tasks: []ChoreographyTaskSpec{
			{Task: "gate", Virtual: true}}},
		channel: &workerReturnChannel{client: client}, joining: true}
	changed, err := w.joinCompiledRun(t.Context(), run)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, run.joining)
	require.Nil(t, run.joinProposal, "only a definitive signed stale receipt permits a new proposal")
	changed, err = w.joinCompiledRun(t.Context(), run)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, run.joining)
	require.Equal(t, "head-new", client.proposals[1].ExpectedLedgerOID)
	require.NotEqual(t, client.proposals[0].RequestID, client.proposals[1].RequestID)
	require.Equal(t, ChoreographyProtocolVersion, client.proposals[0].Report.Protocol)
	require.Equal(t, ChoreographyProtocolVersion, client.proposals[1].Report.Protocol)
	require.Equal(t, ProtocolVersion, w.Report.Protocol,
		"late join upgrades only the signed report copy for this protocol")
	require.NotNil(t, run.frontier)
}

func TestCompiledAcceptedClaimFetchesExactCanonicalBranchBeforeAssignmentRead(t *testing.T) {
	first, persist, binding, spec := newChoreographyLedgerFixture(t)
	peer := first.second(t)
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		ProposalID: "remote-accepted-claim"}
	completeChoreographyFixtureClaim(t, persist, &proposal)
	receipt, err := persist.PersistClaim(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	_, err = peer.mailbox.VerifyAssignmentCarriers(t.Context(), receipt.AssignmentOID,
		returnReplyLimit)
	require.Error(t, err, "the HTTP receipt does not import Git objects into the peer store")
	require.NoError(t, verifyFetchedCompiledClaim(t.Context(), peer.mailbox, proposal, receipt))
	assignment, err := peer.mailbox.VerifyAssignmentCarriers(t.Context(), receipt.AssignmentOID,
		returnReplyLimit)
	require.NoError(t, err)
	require.Equal(t, receipt.GraphRootOID, assignment.GraphRootOID)
	wrong := receipt
	wrong.AssignmentOID = spec.ReservationOID
	require.Error(t, verifyFetchedCompiledClaim(t.Context(), peer.mailbox, proposal, wrong),
		"a fetched branch does not authorize a substituted assignment")
}

func TestCompiledWorkerAcceptsScopedSignedCapacityRefusal(t *testing.T) {
	for _, scope := range []string{"budget", "node"} {
		t.Run(scope, func(t *testing.T) {
			binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
				PlanDigest: "plan", GraphDigest: "graph"}
			client := &compiledCapacityClaimClient{staleCompiledJoinClient: &staleCompiledJoinClient{}, scope: scope}
			proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: "build", Attempt: 1, Node: "peer", Branch: "task-ref",
				ExpectedOID: "task-root", ExpectedLedgerOID: "ledger-old",
				GraphRootOID: "complete-root", ProposalID: "exact-proposal"}
			w := &Worker{Node: "peer"}
			receipt, settled, err := w.submitReturnClaim(t.Context(),
				&workerReturnChannel{client: client}, proposal)
			require.NoError(t, err)
			require.True(t, settled)
			require.True(t, receipt.Refused)
			require.Equal(t, scope, receipt.CapacityScope)
			require.Equal(t, "ledger-new", receipt.FenceOID)
		})
	}
}
