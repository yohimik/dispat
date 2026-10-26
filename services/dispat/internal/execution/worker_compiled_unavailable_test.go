// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type uncertainCompiledDrainClient struct {
	*staleCompiledJoinClient
	proposals []ChoreographyDrainProposal
}

func (c *uncertainCompiledDrainClient) SubmitDrain(_ context.Context,
	proposal ChoreographyDrainProposal) (ChoreographyDrainReceipt, error) {
	c.proposals = append(c.proposals, proposal)
	if len(c.proposals) == 1 {
		return ChoreographyDrainReceipt{}, errors.New("reply lost")
	}
	query, err := FormatChoreographyDrainReceiptQuery(proposal)
	if err != nil {
		return ChoreographyDrainReceipt{}, err
	}
	return ChoreographyDrainReceipt{ChoreographyRunBinding: proposal.ChoreographyRunBinding,
		BootstrapOID: proposal.BootstrapOID, GraphRootOID: proposal.GraphRootOID,
		Node: proposal.Node, RequestID: proposal.RequestID,
		RequestDigest: query.RequestDigest, Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "signed-drain"}}, nil
}

func (*uncertainCompiledDrainClient) LookupDrain(context.Context,
	ChoreographyDrainReceiptQuery) (ChoreographyDrainReceipt, bool, error) {
	return ChoreographyDrainReceipt{}, false, nil
}

func TestCompiledUnavailableDrainKeepsExactProposalAfterUnknownReply(t *testing.T) {
	client := &uncertainCompiledDrainClient{staleCompiledJoinClient: &staleCompiledJoinClient{}}
	proposal := ChoreographyDrainProposal{ChoreographyRunBinding: ChoreographyRunBinding{
		Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"},
		BootstrapOID: "bootstrap", GraphRootOID: "complete-root", Node: "peer",
		CapacityAuthorityID: "authority", MembershipEpochOID: "initial-epoch",
		ExpectedLedgerOID: "current-head", RequestID: "exact-unavailability"}
	state := &workerCompiledUnavailable{bootstrapOID: proposal.BootstrapOID,
		rootOID: proposal.GraphRootOID, channel: &workerReturnChannel{client: client},
		proposal: &proposal}
	w := &Worker{}
	settled, err := w.settleCompiledUnavailableDrain(t.Context(), state)
	require.ErrorContains(t, err, "no signed receipt")
	require.False(t, settled)
	require.False(t, state.drained)
	require.Same(t, &proposal, state.proposal)
	settled, err = w.settleCompiledUnavailableDrain(t.Context(), state)
	require.NoError(t, err)
	require.True(t, settled)
	require.True(t, state.drained)
	require.Nil(t, state.proposal)
	require.Nil(t, state.channel)
	require.Equal(t, []ChoreographyDrainProposal{proposal, proposal}, client.proposals,
		"an unknown reply cannot authorize a new drain identity")
}
