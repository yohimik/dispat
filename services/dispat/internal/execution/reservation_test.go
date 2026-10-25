// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestMessageCommitSwitchesToStreamingForManyCarriers(t *testing.T) {
	f := newMailboxFixture(t)
	ctx := t.Context()
	plumbing := gitx.NewPlumbing(f.git)
	tree := plumbing.MakeTree(ctx, nil)
	require.NoError(t, plumbing.Err())
	parents := make([]string, 0, gitx.MaxTransportBatch+1)
	for i := 0; i <= gitx.MaxTransportBatch; i++ {
		parents = append(parents, plumbing.CommitTree(ctx, tree, nil, fmt.Sprintf("seed-%d", i)))
	}
	require.NoError(t, plumbing.Err())
	commit, err := f.mailbox.commitMessage(ctx, MessageAssignment, []byte(`{}`), parents, nil)
	require.NoError(t, err)
	read, err := f.git.CommitParents(ctx, commit)
	require.NoError(t, err)
	require.Equal(t, parents, read)
}

type lostAtomicReply struct{ transportx }

func (t lostAtomicReply) PushAtomicAdvances(ctx context.Context, remote string, refs []gitx.BranchAdvance) error {
	if err := t.transportx.PushAtomicAdvances(ctx, remote, refs); err != nil {
		return err
	}
	return gitx.ErrPushUnknown
}

func reservationHeader(branch string) Header {
	return Header{Protocol: ProtocolVersion, Kind: KindBuild, Run: "run-v2", PlanDigest: "plan-v2",
		Task: "core:build", Attempt: 1, Generation: "generation-v2", Branch: branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339)}
}

func TestReservationAndSlotAreInertUntilAssignment(t *testing.T) {
	require.True(t, IsTransitionLegal("", MessageReservation, PartyOrchestrator))
	require.True(t, IsTransitionLegal(MessageReservation, MessageAssignment, PartyOrchestrator))
	require.True(t, IsTransitionLegal("", MessageSlot, PartyOrchestrator))
	require.True(t, IsTransitionLegal(MessageSlot, MessageSlot, PartyOrchestrator))
	require.Equal(t, ActionNone, ResolveWorkerAction(ChainTip{Kind: MessageReservation}))
	require.Equal(t, ActionNone, ResolveWorkerAction(ChainTip{Kind: MessageSlot}))
	require.Equal(t, ActionClaim, ResolveWorkerAction(ChainTip{Kind: MessageAssignment, Previous: MessageReservation}))
}

func TestCleanupReadSeesBothReservationAndSlotRefs(t *testing.T) {
	f := newMailboxFixture(t)
	ctx := t.Context()
	taskBranch, slotBranch := "dispat-task-run-a-1", "dispat-slot-node-run-0"
	taskOID, err := f.mailbox.PrepareReservation(ctx, &Reservation{Header: reservationHeader(taskBranch)})
	require.NoError(t, err)
	slotOID, err := f.mailbox.PrepareSlotNotice(ctx, &SlotNotice{Header: reservationHeader(slotBranch)}, "")
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.endpoint, []gitx.CreateRef{
		{Branch: taskBranch, OID: taskOID}, {Branch: slotBranch, OID: slotOID},
	}))
	present, err := f.mailbox.readPresentBranches(ctx, []string{taskBranch, slotBranch, "dispat-task-absent"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{taskBranch: true, slotBranch: true}, present)
}

func TestObserveManyExactKeepsIndependentMemoAndForgetsVanishedRefs(t *testing.T) {
	f := newMailboxFixture(t)
	ctx := t.Context()
	one, two, foreign := "dispat-task-one", "dispat-task-two", "dispat-task-foreign"
	first, err := f.mailbox.PrepareReservation(ctx, &Reservation{Header: reservationHeader(one)})
	require.NoError(t, err)
	second, err := f.mailbox.PrepareReservation(ctx, &Reservation{Header: reservationHeader(two)})
	require.NoError(t, err)
	other, err := f.mailbox.PrepareReservation(ctx, &Reservation{Header: reservationHeader(foreign)})
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.endpoint, []gitx.CreateRef{
		{Branch: one, OID: first}, {Branch: two, OID: second}, {Branch: foreign, OID: other},
	}))
	_, err = f.mailbox.ObserveManyExact(ctx, []string{foreign})
	require.NoError(t, err)
	heads, err := f.mailbox.ObserveManyExact(ctx, []string{two, one})
	require.NoError(t, err)
	require.Equal(t, []gitx.RemoteHead{{Name: one, OID: first}, {Name: two, OID: second}}, heads)
	heads, err = f.mailbox.ObserveManyExact(ctx, []string{one, two})
	require.NoError(t, err)
	require.Empty(t, heads, "unchanged active refs are memoized")
	f.mailbox.Reconsider(two)
	heads, err = f.mailbox.ObserveManyExact(ctx, []string{two})
	require.NoError(t, err)
	require.Equal(t, []gitx.RemoteHead{{Name: two, OID: second}}, heads)
	outcomes, err := f.git.DeleteRemoteBranchesLease(ctx, f.endpoint,
		[]gitx.BranchLease{{Branch: one, ExpectedOld: first}})
	require.NoError(t, err)
	require.Equal(t, gitx.BranchDeleted, outcomes[0].Result)
	heads, err = f.mailbox.ObserveManyExact(ctx, []string{one, two})
	require.NoError(t, err)
	require.Empty(t, heads)
	f.mailbox.memo.Lock()
	wasFetched := f.mailbox.fetched[one]
	wasObserved := f.mailbox.observed[one]
	foreignObserved := f.mailbox.observed[foreign]
	f.mailbox.memo.Unlock()
	require.False(t, wasFetched, "vanished ref's local transport name is removed")
	require.Empty(t, wasObserved, "vanished ref's observed OID is forgotten")
	require.Equal(t, other, foreignObserved, "exact poll leaves unrelated memo alone")
}

func TestReservationAssignmentCarriesOnlySignedInputsAndActivatesWithSlot(t *testing.T) {
	f := newMailboxFixture(t)
	ctx := t.Context()
	branch, slotBranch := "dispat-reserved-core-1", "dispat-slot-node-0"
	reservation := &Reservation{Header: reservationHeader(branch)}
	reservedOID, err := f.mailbox.PrepareReservation(ctx, reservation)
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.endpoint, []gitx.CreateRef{{Branch: branch, OID: reservedOID}}))

	resultDoc := []byte(`{"status":"succeeded"}`)
	resultOID, err := f.mailbox.commitMessage(ctx, MessageResult, resultDoc, nil, nil)
	require.NoError(t, err)
	snapshotOID, err := f.mailbox.commitMessage(ctx, MessageReservation, []byte(`{"snapshot":true}`), nil, nil)
	require.NoError(t, err)
	assignment := &Assignment{Header: reservation.Header}
	assignment.Node = "node-a"
	assignment.Repositories = []AssignmentRepository{{Name: "source", Snapshot: snapshotOID}}
	assignment.Inputs = []AssignmentInput{{Task: "provider:build", Commit: resultOID, Digest: "digest"}}
	assignedOID, err := f.mailbox.PrepareAssignmentOnReservation(ctx, assignment, reservedOID)
	require.NoError(t, err)
	parents, err := f.git.CommitParents(ctx, assignedOID)
	require.NoError(t, err)
	require.Equal(t, reservedOID, parents[0])
	require.ElementsMatch(t, []string{snapshotOID, resultOID}, parents[1:])
	authenticated, err := f.mailbox.VerifyAssignmentCarriers(ctx, assignedOID, 1<<20)
	require.NoError(t, err)
	require.Equal(t, assignment, authenticated)
	read, err := f.mailbox.ReadCarriedResult(ctx, assignedOID, resultOID, 1<<20)
	require.NoError(t, err)
	require.JSONEq(t, string(resultDoc), string(read))
	view, err := f.mailbox.OpenCarriedAssignment(ctx, assignedOID, 1<<20)
	require.NoError(t, err)
	require.Equal(t, *assignment, view.Assignment)
	read, err = view.ReadResult(ctx, resultOID, 1<<20)
	require.NoError(t, err)
	require.JSONEq(t, string(resultDoc), string(read))
	_, err = view.ReadResult(ctx, reservedOID, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err), "verified view still rejects an undeclared first parent")
	_, err = f.mailbox.ReadCarriedResult(ctx, assignedOID, reservedOID, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err), "the first parent is never an input result")

	slot := &SlotNotice{Header: reservationHeader(slotBranch), Sequence: 0}
	slot.Node = "node-a"
	slotOID, err := f.mailbox.PrepareSlotNotice(ctx, slot, "")
	require.NoError(t, err)
	require.NoError(t, f.git.PushCreateAtomic(ctx, f.endpoint, []gitx.CreateRef{{Branch: slotBranch, OID: slotOID}}))
	slot.Sequence = 1
	slot.TaskBranch, slot.AssignmentOID = branch, assignedOID
	activeSlotOID, err := f.mailbox.PrepareSlotNotice(ctx, slot, slotOID)
	require.NoError(t, err)
	f.mailbox.remote = lostAtomicReply{transportx: f.mailbox.remote}
	require.NoError(t, f.mailbox.ActivateReserved(ctx, Activation{
		TaskBranch: branch, ReservationOID: reservedOID, AssignmentOID: assignedOID,
		SlotBranch: slotBranch, SlotOldOID: slotOID, SlotNewOID: activeSlotOID,
	}))
	heads, err := f.git.ListRemoteHeads(ctx, f.endpoint, "refs/heads/"+branch)
	require.NoError(t, err)
	require.Len(t, heads, 1)
	require.Equal(t, assignedOID, heads[0].OID)
	heads, err = f.git.ListRemoteHeads(ctx, f.endpoint, "refs/heads/"+slotBranch)
	require.NoError(t, err)
	require.Len(t, heads, 1)
	require.Equal(t, activeSlotOID, heads[0].OID)

	worker := f.second(t)
	require.NoError(t, worker.git.FetchRefs(ctx, f.endpoint, []string{branch}))
	workerAssignment, err := worker.mailbox.VerifyAssignmentCarriers(ctx, assignedOID, 1<<20)
	require.NoError(t, err)
	require.Equal(t, assignment, workerAssignment)
	_, err = worker.mailbox.ReadCarriedResult(ctx, assignedOID, resultOID, 1<<20)
	require.NoError(t, err, "a task branch transports the carried result without a relay ref")
}

func TestCarrierGateRefusesUnsignedOrMismatchedDirectParentSet(t *testing.T) {
	f := newMailboxFixture(t)
	ctx := t.Context()
	reservation := &Reservation{Header: reservationHeader("dispat-carrier-check")}
	reservedOID, err := f.mailbox.PrepareReservation(ctx, reservation)
	require.NoError(t, err)
	resultOID, err := f.mailbox.commitMessage(ctx, MessageResult, []byte(`{"status":"succeeded"}`), nil, nil)
	require.NoError(t, err)
	assignment := &Assignment{Header: reservation.Header, Inputs: []AssignmentInput{{Commit: resultOID}}}
	assignment.Node = "node-a"
	document, err := json.Marshal(assignment)
	require.NoError(t, err)
	missingCarrier, err := f.mailbox.commitMessage(ctx, MessageAssignment, document, []string{reservedOID}, nil)
	require.NoError(t, err)
	_, err = f.mailbox.VerifyAssignmentCarriers(ctx, missingCarrier, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err))
	_, err = f.mailbox.ReadCarriedResult(ctx, missingCarrier, resultOID, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err))
	unrelatedResultOID, err := f.mailbox.commitMessage(ctx, MessageResult, []byte(`{"status":"failed"}`), nil, nil)
	require.NoError(t, err)
	extraCarrier, err := f.mailbox.commitMessage(ctx, MessageAssignment, document,
		[]string{reservedOID, resultOID, unrelatedResultOID}, nil)
	require.NoError(t, err)
	_, err = f.mailbox.VerifyAssignmentCarriers(ctx, extraCarrier, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err), "a present but undeclared result is not an input")

	wrongFirstParent, err := f.mailbox.commitMessage(ctx, MessageAssignment, document, []string{resultOID, reservedOID}, nil)
	require.NoError(t, err)
	_, err = f.mailbox.VerifyAssignmentCarriers(ctx, wrongFirstParent, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err))
}
