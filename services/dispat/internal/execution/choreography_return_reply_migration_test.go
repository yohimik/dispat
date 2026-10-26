package execution

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerLostAcknowledgementReplyUsesExactReceipt(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	task := &claimedTask{assignment: assignment,
		tip: ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "assignment-oid"}, claimed: "claim-oid"}
	var submitted ChoreographyAckProposal
	ackCalls, lookupCalls := 0, 0
	fake := &fakeWorkerReturn{}
	fake.ack = func(proposal ChoreographyAckProposal) (ChoreographyReceipt, error) {
		ackCalls++
		submitted = proposal
		return ChoreographyReceipt{}, errors.New("accepted acknowledgement reply lost")
	}
	fake.lookup = func(query ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		lookupCalls++
		exact, err := FormatAckReceiptQuery(submitted)
		require.NoError(t, err)
		require.Equal(t, exact, query)
		return testReturnReceipt(MessageAck, binding, submitted.Task, submitted.Attempt,
			submitted.Node, submitted.Branch, submitted.ProposalID,
			query.ProposalDigest, "first-ack-oid"), true, nil
	}
	worker := &Worker{Node: "node-a", ReturnSigner: fixture.signer,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	ack, err := worker.returnTaskAck(t.Context(), taskAckReturnRequest{
		Task: task, CancelOID: "cancel-oid", Phase: "commands", CommandStarted: true,
	})
	require.NoError(t, err)
	require.Equal(t, "first-ack-oid", ack)
	require.Equal(t, "cancel-oid", submitted.CancelOID)
	require.Equal(t, 1, ackCalls, "the uncertain Ack must not be sent under a new proposal")
	require.Equal(t, 1, lookupCalls)
	require.Empty(t, fixture.remoteBranches(t), "the peer must not create a Git ref")
}
