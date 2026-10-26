// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const workerCompiledBootstrapMode = "compilation"

type fakeWorkerReturn struct {
	probe           func(ChoreographyProbeReply) (ChoreographyReceipt, error)
	claim           func(ChoreographyClaimProposal) (ChoreographyReceipt, error)
	result          func(ChoreographyResultProposal, io.ReadSeeker) (ChoreographyReceipt, error)
	ack             func(ChoreographyAckProposal) (ChoreographyReceipt, error)
	lookup          func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error)
	poll            func(ChoreographyEventRequest) (ChoreographyEventPage, error)
	pollWithContext func(context.Context, ChoreographyEventRequest) (ChoreographyEventPage, error)
}

func (f *fakeWorkerReturn) SubmitProbe(_ context.Context, p ChoreographyProbeReply) (ChoreographyReceipt, error) {
	if f.probe == nil {
		return ChoreographyReceipt{}, errors.New("unexpected probe")
	}
	return f.probe(p)
}
func (f *fakeWorkerReturn) SubmitClaim(_ context.Context, p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	return f.claim(p)
}
func (f *fakeWorkerReturn) SubmitResult(_ context.Context, p ChoreographyResultProposal,
	data io.ReadSeeker) (ChoreographyReceipt, error) {
	return f.result(p, data)
}
func (f *fakeWorkerReturn) SubmitAck(_ context.Context, p ChoreographyAckProposal) (ChoreographyReceipt, error) {
	return f.ack(p)
}
func (f *fakeWorkerReturn) LookupReceipt(_ context.Context,
	q ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	return f.lookup(q)
}
func (f *fakeWorkerReturn) PollEvents(ctx context.Context,
	request ChoreographyEventRequest) (ChoreographyEventPage, error) {
	if f.pollWithContext != nil {
		return f.pollWithContext(ctx, request)
	}
	if f.poll != nil {
		return f.poll(request)
	}
	return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-oid"},
		Head: ChoreographyEventCursor{OID: "ledger-oid"}}, nil
}
func (*fakeWorkerReturn) Close() {}

func testReturnBinding() ChoreographyRunBinding {
	return ChoreographyRunBinding{Run: "run-1", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
}

func TestWorkerReplyRetainsAuthenticatedAssignmentProtocol(t *testing.T) {
	w := &Worker{Node: "node"}
	for _, protocol := range []int{ProtocolVersion, ChoreographyProtocolVersion} {
		header := w.formatReplyHeader(Header{Protocol: protocol, Run: "run", Node: "node"})
		require.Equal(t, protocol, header.Protocol)
	}
}

func validReturnProbe(branch string) Assignment {
	probe := validProbe(branch)
	probe.ReturnEndpoint = "https://first.example:8443"
	probe.ReturnCertSHA256 = "pinned-certificate"
	probe.GraphDigest = "graph-digest"
	return probe
}

func attachFakeProbeReturn(t *testing.T, worker *Worker, assignment Assignment) {
	t.Helper()
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	worker.ReturnSigner = signer
	binding := ChoreographyRunBinding{Run: assignment.Run, Generation: assignment.Generation,
		PlanDigest: assignment.PlanDigest, GraphDigest: assignment.GraphDigest}
	fake := &fakeWorkerReturn{probe: func(reply ChoreographyProbeReply) (ChoreographyReceipt, error) {
		require.Equal(t, worker.Report, reply.Report)
		return ChoreographyReceipt{Kind: MessageResult, Task: PreflightTask,
			Node: worker.Node, Branch: assignment.Branch, ProposalID: reply.AssignmentOID,
			OID: "first-probe-oid", Accepted: true}, nil
	}}
	worker.returnChannels = map[string]*workerReturnChannel{assignment.Run: {
		binding: binding, endpoint: assignment.ReturnEndpoint,
		certSHA256: assignment.ReturnCertSHA256, client: fake}}
}

func testReturnReceipt(kind MessageKind, binding ChoreographyRunBinding,
	task string, attempt int, node, branch, proposalID, digest, oid string) ChoreographyReceipt {
	return ChoreographyReceipt{ChoreographyRunBinding: binding,
		Kind: kind, Task: task, Attempt: attempt, Node: node, Branch: branch,
		ProposalID: proposalID, ProposalDigest: digest, OID: oid, Accepted: true}
}

func refusedReturnClaim(t *testing.T, binding ChoreographyRunBinding,
	p ChoreographyClaimProposal) ChoreographyReceipt {
	t.Helper()
	query, err := FormatClaimReceiptQuery(p)
	require.NoError(t, err)
	return ChoreographyReceipt{ChoreographyRunBinding: binding,
		Kind: choreographyClaimKind, Task: p.Task, Attempt: p.Attempt,
		Node: p.Node, Branch: p.Branch, ProposalID: p.ProposalID,
		ProposalDigest: query.ProposalDigest, OID: p.ExpectedOID,
		Refused: true, FenceOID: "new-ledger-oid", FenceSequence: 1,
		RefusalReason: ChoreographyRefusalStale}
}

func TestWorkerDefinitivelyRefusedClaimReleasesSlotWithoutStarting(t *testing.T) {
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	for _, lostReply := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "lost reply recovered"}[lostReply], func(t *testing.T) {
			mailbox := &fakeMailbox{}
			fake := &fakeWorkerReturn{}
			fake.claim = func(p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
				if lostReply {
					return ChoreographyReceipt{}, errors.New("refusal reply lost")
				}
				return refusedReturnClaim(t, binding, p), nil
			}
			fake.lookup = func(query ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
				proposal := (&Worker{Node: "node-a"}).formatReturnClaim(returnClaimRequest{
					Binding: binding, LedgerOID: "ledger-oid", Tip: tip, Assignment: assignment})
				require.Equal(t, proposal.ProposalID, query.ProposalID)
				return refusedReturnClaim(t, binding, proposal), true, nil
			}
			w := &Worker{Node: "node-a", ReturnSigner: signer, Mailbox: mailbox,
				returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}},
				slotForTask:    map[string]string{tip.Branch: "dispat-slot-run-1-node-a-0"},
				slots:          make(chan struct{}, 1), Log: zerolog.Nop()}
			started, err := w.takeTask(t.Context(), tip, assignment)
			require.NoError(t, err)
			require.False(t, started)
			require.Empty(t, w.slots)
			require.Empty(t, w.pendingReturn)
			require.Contains(t, mailbox.reconsidered, tip.Branch)
			require.Contains(t, mailbox.reconsidered, "dispat-slot-run-1-node-a-0",
				"a signed stale epoch must rearm the unchanged fixed slot")
			require.Empty(t, mailbox.written, "a refused claim cannot start a command or write a peer ref")
		})
	}
}

func TestWorkerDelayedRefusalHoldsThenReleasesExactPendingClaim(t *testing.T) {
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	mailbox := &fakeMailbox{}
	var original ChoreographyClaimProposal
	calls := 0
	fake := &fakeWorkerReturn{}
	fake.claim = func(p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		calls++
		if calls == 1 {
			original = p
			return ChoreographyReceipt{}, errors.New("request outcome unknown")
		}
		require.Equal(t, original, p, "retry must preserve the exact body and ledger epoch")
		return refusedReturnClaim(t, binding, p), nil
	}
	fake.lookup = func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}
	w := &Worker{Node: "node-a", ReturnSigner: signer, Mailbox: mailbox,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}},
		slots:          make(chan struct{}, 1), Log: zerolog.Nop()}
	started, err := w.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err)
	require.False(t, started)
	require.Len(t, w.slots, 1)
	require.Contains(t, w.pendingReturn, tip.Branch)
	require.True(t, w.settlePendingReturnClaims(t.Context()))
	require.Empty(t, w.slots)
	require.Empty(t, w.pendingReturn)
	require.Contains(t, mailbox.reconsidered, tip.Branch)
	require.Empty(t, mailbox.written)
}

func TestWorkerV3UnknownClaimRetainsSharedNodeLeaseUntilSignedRefusal(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	other, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
		Node: "node-a", Task: "pkg:build", Attempt: 1,
		Branch: FormatChoreographyTaskBranch(binding.Run, "pkg:build")},
		Weight: 1, GraphDigest: binding.GraphDigest, BootstrapOID: strings.Repeat("c", 40)}
	tip := ChainTip{Branch: assignment.Branch, OID: strings.Repeat("a", 40)}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	mailbox := &fakeMailbox{}
	calls := 0
	fake := &fakeWorkerReturn{}
	fake.claim = func(p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		calls++
		if calls == 1 {
			return ChoreographyReceipt{}, errors.New("claim outcome unknown")
		}
		receipt := refusedReturnClaim(t, binding, p)
		receipt.FenceOID = strings.Repeat("b", 40)
		return receipt, nil
	}
	fake.lookup = func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}
	fake.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return ChoreographyEventPage{Head: ChoreographyEventCursor{OID: strings.Repeat("d", 40)}}, nil
	}
	w := &Worker{Node: "node-a", Endpoint: "file:///canonical-mailbox", ReturnSigner: signer, Mailbox: mailbox,
		Report: NodeReport{AuthorityID: authority.AuthorityID()}, NodeSlots: authority,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding,
			bootstrapOID: assignment.BootstrapOID, bootstrap: &RunBootstrap{
				Protocol: ChoreographyProtocolVersion, Mode: workerCompiledBootstrapMode,
				ChoreographyRunBinding: binding,
				CoordinationEndpoint:   "file:///canonical-mailbox"}, client: fake}},
		slots: make(chan struct{}, 1), Log: zerolog.Nop()}
	started, err := w.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err)
	require.False(t, started)
	require.Len(t, w.slots, 1)
	held, err := other.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	_, available, err := other.TryReserve(t.Context(), NodeSlotIntent{
		Run: "other", Task: "other", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.False(t, available, "another process cannot borrow an uncertain attempt's node slot")
	require.True(t, w.settlePendingReturnClaims(t.Context()))
	require.Empty(t, w.pendingReturn)
	require.Empty(t, w.slots)
	held, err = other.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
	_, available, err = other.TryReserve(t.Context(), NodeSlotIntent{
		Run: "other", Task: "other", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, available)
	require.Empty(t, mailbox.written, "the worker never pushes Git refs")
}

func TestWorkerRearmsVerifiedSlotWhenAnotherRunHoldsPhysicalCapacity(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	holder, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	serving, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	_, available, err := holder.TryReserve(t.Context(), NodeSlotIntent{
		Run: "other-run", Task: "other:build", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, available)
	binding := testReturnBinding()
	branch := FormatChoreographyTaskBranch(binding.Run, "pkg:build")
	slot := FormatSlotBranch("node-a", binding.Run, 0)
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build",
		Attempt: 1, Branch: branch}, Weight: 1, GraphDigest: binding.GraphDigest,
		BootstrapOID: strings.Repeat("c", 40)}
	tip := ChainTip{Branch: branch, OID: strings.Repeat("a", 40)}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	mailbox := &fakeMailbox{}
	claimCalls := 0
	client := &fakeWorkerReturn{
		poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
			return ChoreographyEventPage{Head: ChoreographyEventCursor{OID: strings.Repeat("d", 40)}}, nil
		},
		claim: func(ChoreographyClaimProposal) (ChoreographyReceipt, error) {
			claimCalls++
			return ChoreographyReceipt{}, errors.New("physical capacity should prevent a proposal")
		},
	}
	w := &Worker{Node: "node-a", Endpoint: "file:///canonical-mailbox", ReturnSigner: signer,
		Mailbox: mailbox, Report: NodeReport{AuthorityID: serving.AuthorityID()},
		NodeSlots: serving, slots: make(chan struct{}, 1), Log: zerolog.Nop(),
		slotForTask: map[string]string{branch: slot},
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding,
			bootstrapOID: assignment.BootstrapOID, bootstrap: &RunBootstrap{
				Protocol: ChoreographyProtocolVersion, Mode: workerCompiledBootstrapMode,
				ChoreographyRunBinding: binding,
				CoordinationEndpoint:   "file:///canonical-mailbox"}, client: client}}}
	started, err := w.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err)
	require.False(t, started)
	require.Zero(t, claimCalls)
	require.Empty(t, w.slots)
	require.Equal(t, []string{branch, slot}, mailbox.reconsidered,
		"the fixed slot must be reconsidered even though this process had a free local channel")
}

func TestWorkerNodeSlotWaitsForMatchingSignedDecisionAfterResultReceipt(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	binding := testReturnBinding()
	receiptOID := strings.Repeat("b", 40)
	receipt := ChoreographyReceipt{OID: receiptOID, Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	polls := 0
	fake := &fakeWorkerReturn{poll: func(request ChoreographyEventRequest) (ChoreographyEventPage, error) {
		polls++
		require.Equal(t, binding, request.ChoreographyRunBinding)
		held, err := authority.Held(t.Context())
		require.NoError(t, err)
		require.Len(t, held, 1, "ResultAccepted cannot release node capacity")
		if polls == 1 {
			return ChoreographyEventPage{Cursor: receipt.Cursor}, nil
		}
		return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
			Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal,
				Task: intent.Task, Attempt: intent.Attempt, Node: "node-a",
				TaskOID: strings.Repeat("c", 40), ResultReceiptOID: receiptOID}}}, nil
	}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease}
	channel := &workerReturnChannel{binding: binding, client: fake}
	require.NoError(t, w.awaitNodeSlotDecision(t.Context(), task, channel, receipt))
	require.Equal(t, 2, polls)
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}

func TestWorkerAcceptedResultWaitsBeyondSubmissionDeadlineForSignedFinality(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	receiptOID := strings.Repeat("b", 40)
	receipt := ChoreographyReceipt{OID: receiptOID, Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	submitCtx, stopSubmit := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer stopSubmit()
	polls := 0
	fake := &fakeWorkerReturn{poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		polls++
		held, err := authority.Held(t.Context())
		require.NoError(t, err)
		require.Len(t, held, 1, "an accepted Result is not physical capacity finality")
		if polls == 1 {
			return ChoreographyEventPage{Cursor: receipt.Cursor}, nil
		}
		require.ErrorIs(t, submitCtx.Err(), context.DeadlineExceeded)
		return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
			Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal,
				Task: intent.Task, Attempt: 1, Node: "node-a", TaskOID: strings.Repeat("c", 40),
				ResultReceiptOID: receiptOID}}}, nil
	}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease,
		finalityCtx: t.Context()}
	require.NoError(t, w.awaitNodeSlotDecision(submitCtx, task,
		&workerReturnChannel{binding: testReturnBinding(), client: fake}, receipt))
	require.Equal(t, 2, polls)
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}

func TestWorkerStoppedBeforeFinalityRetainsPhysicalNodeSlot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	finalityCtx, stopFinality := context.WithCancel(t.Context())
	stopFinality()
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	receipt := ChoreographyReceipt{OID: strings.Repeat("b", 40), Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease,
		finalityCtx: finalityCtx}
	err = w.awaitNodeSlotDecision(ctx, task, &workerReturnChannel{binding: testReturnBinding(),
		client: &fakeWorkerReturn{poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
			return ChoreographyEventPage{Cursor: receipt.Cursor}, nil
		}}}, receipt)
	require.ErrorIs(t, err, context.Canceled)
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
}

func TestWorkerStopReadsAlreadySignedFinalityBeforeRetainingNodeSlot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	finalityCtx, stopFinality := context.WithCancel(t.Context())
	stopFinality()
	receiptOID := strings.Repeat("b", 40)
	receipt := ChoreographyReceipt{OID: receiptOID, Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease,
		finalityCtx: finalityCtx}
	polls := 0
	fake := &fakeWorkerReturn{pollWithContext: func(readCtx context.Context,
		request ChoreographyEventRequest) (ChoreographyEventPage, error) {
		polls++
		require.Equal(t, receipt.Cursor, request.Cursor)
		if polls == 1 {
			require.ErrorIs(t, readCtx.Err(), context.Canceled)
			return ChoreographyEventPage{}, context.Canceled
		}
		require.NoError(t, readCtx.Err(), "the finality probe must detach from serving cancellation")
		return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
			Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal,
				Task: intent.Task, Attempt: intent.Attempt, Node: "node-a",
				TaskOID: strings.Repeat("c", 40), ResultReceiptOID: receiptOID}}}, nil
	}}
	require.NoError(t, w.awaitNodeSlotDecision(t.Context(), task,
		&workerReturnChannel{binding: testReturnBinding(), client: fake}, receipt))
	require.Equal(t, 2, polls, "the canceled first read must not release capacity")
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}

func TestWorkerStopRechecksMaskedCancellationWithoutTrustingMismatch(t *testing.T) {
	for _, exact := range []bool{true, false} {
		t.Run(map[bool]string{true: "exact finality", false: "mismatched finality"}[exact], func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Chmod(root, 0o700))
			authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
			require.NoError(t, err)
			intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
			lease, available, err := authority.TryReserve(t.Context(), intent)
			require.NoError(t, err)
			require.True(t, available)
			finalityCtx, stopFinality := context.WithCancel(t.Context())
			stopFinality()
			receiptOID := strings.Repeat("b", 40)
			receipt := ChoreographyReceipt{OID: receiptOID, Accepted: true,
				Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
			polls := 0
			fake := &fakeWorkerReturn{poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				if polls == 1 {
					return ChoreographyEventPage{}, errors.New("masked cancelled read")
				}
				resultReceiptOID := receiptOID
				if !exact {
					resultReceiptOID = strings.Repeat("d", 40)
				}
				return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
					Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal,
						Task: intent.Task, Attempt: intent.Attempt, Node: "node-a",
						TaskOID: strings.Repeat("c", 40), ResultReceiptOID: resultReceiptOID}}}, nil
			}}
			w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
			task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
				Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease,
				finalityCtx: finalityCtx}
			err = w.awaitNodeSlotDecision(t.Context(), task,
				&workerReturnChannel{binding: testReturnBinding(), client: fake}, receipt)
			held, heldErr := authority.Held(t.Context())
			require.NoError(t, heldErr)
			if exact {
				require.NoError(t, err)
				require.Empty(t, held)
			} else {
				require.Error(t, err)
				require.Equal(t, []NodeSlotLease{lease}, held)
			}
			require.Equal(t, 2, polls)
		})
	}
}

func TestWorkerNodeSlotRejectsMismatchedSignedFinality(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	receipt := ChoreographyReceipt{OID: strings.Repeat("b", 40), Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	fake := &fakeWorkerReturn{poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
			Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyRetryReady,
				Task: intent.Task, Attempt: intent.Attempt, Node: "node-a",
				TaskOID: strings.Repeat("c", 40), ResultReceiptOID: strings.Repeat("d", 40)}}}, nil
	}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease}
	err = w.awaitNodeSlotDecision(t.Context(), task,
		&workerReturnChannel{binding: testReturnBinding(), client: fake}, receipt)
	require.ErrorContains(t, err, "different accepted result")
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
}

func TestWorkerLosingPickupRetainsSlotUntilItsOwnSignedSettlement(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
	require.NoError(t, err)
	pickupID := strings.Repeat("a", 32)
	intent := NodeSlotIntent{Run: "run", Task: "pkg:build", Attempt: 1, Weight: 1, PickupID: pickupID}
	lease, available, err := authority.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	receipt := ChoreographyReceipt{OID: strings.Repeat("b", 40), Accepted: true,
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2}}
	polls := 0
	fake := &fakeWorkerReturn{poll: func(request ChoreographyEventRequest) (ChoreographyEventPage, error) {
		polls++
		held, err := authority.Held(t.Context())
		require.NoError(t, err)
		require.Equal(t, []NodeSlotLease{lease}, held, "the winning pickup cannot release the losing worker")
		if polls == 1 {
			return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
				Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal,
					Task: intent.Task, Attempt: intent.Attempt, Node: "node-a", PickupID: strings.Repeat("c", 32),
					TaskOID: strings.Repeat("d", 40), ResultReceiptOID: strings.Repeat("e", 40)}}}, nil
		}
		require.Equal(t, uint64(3), request.Cursor.Sequence)
		return ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: strings.Repeat("f", 40), Sequence: 4},
			Events: []ChoreographyEvent{{Sequence: 4, Kind: ChoreographyPickupSettled,
				Task: intent.Task, Attempt: intent.Attempt, Node: "node-a", PickupID: pickupID,
				TaskOID: strings.Repeat("d", 40), ResultReceiptOID: receipt.OID}}}, nil
	}}
	w := &Worker{Node: "node-a", NodeSlots: authority, Log: zerolog.Nop()}
	task := &claimedTask{assignment: Assignment{Header: Header{Run: intent.Run,
		Task: intent.Task, Attempt: intent.Attempt}}, slotLease: lease}
	require.NoError(t, w.awaitNodeSlotDecision(t.Context(), task,
		&workerReturnChannel{binding: testReturnBinding(), client: fake}, receipt))
	require.Equal(t, 2, polls)
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}

func TestCapacityRefusalParksAcrossIdlePollsAndUnrelatedFinality(t *testing.T) {
	for _, scope := range []string{"node", "budget"} {
		t.Run(scope, func(t *testing.T) {
			binding := testReturnBinding()
			assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
				PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
			branch := "dispat-task-run-1-pkg-1"
			mailbox := &fakeMailbox{}
			page := ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-1", Sequence: 1},
				Head: ChoreographyEventCursor{OID: "ledger-1", Sequence: 1}}
			polls := 0
			fake := &fakeWorkerReturn{poll: func(request ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				require.Equal(t, binding, request.ChoreographyRunBinding)
				return page, nil
			}}
			const slot = "dispat-slot-run-1-node-a-0"
			worker := &Worker{Node: "node-a", Mailbox: mailbox, Log: zerolog.Nop(),
				slotForTask: map[string]string{branch: slot}}
			channel := &workerReturnChannel{binding: binding, client: fake}
			worker.parkRefusedClaim(parkedClaimRequest{Channel: channel, Branch: branch,
				Assignment: assignment, Receipt: ChoreographyReceipt{
					Refused: true, RefusalReason: ChoreographyRefusalCapacity,
					CapacityScope: scope, Budget: "build", FenceOID: "ledger-1", FenceSequence: 1}})
			worker.IdleTimeout = time.Second
			worker.lastActive = time.Now().Add(-2 * time.Second)
			require.Equal(t, time.Second, worker.resolveIdleRemainder(),
				"a parked ready task keeps its node available for a later capacity release")
			for range 25 {
				require.False(t, worker.pollParkedReturnClaims(t.Context()))
			}
			require.Equal(t, 25, polls)
			require.Empty(t, mailbox.reconsidered,
				"a long capacity holder cannot cause one refusal write per polling tick")
			page = ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
				Head: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
				Events: []ChoreographyEvent{{Sequence: 2, Kind: ChoreographyTerminal,
					Task: "unrelated", Node: "other", TaskOID: "done-other", Budget: "publish", Weight: 1}}}
			require.False(t, worker.pollParkedReturnClaims(t.Context()))
			require.Empty(t, mailbox.reconsidered)
			releaseNode, releaseBudget := "other", "build"
			if scope == "node" {
				releaseNode, releaseBudget = "node-a", "publish"
			}
			page = ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
				Head: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
				Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyRetryReady,
					Task: "holder", Node: releaseNode, TaskOID: "retry-holder",
					Budget: releaseBudget, Weight: 1}}}
			require.True(t, worker.pollParkedReturnClaims(t.Context()))
			require.Equal(t, []string{branch, slot}, mailbox.reconsidered)
			require.Empty(t, worker.slotForTask, "the slot association ends at the signed wake")
			require.Empty(t, worker.parkedReturn[binding.Run].entries)
		})
	}
}

func TestWorkerUnansweredClaimKeepsSlotAndExactProposal(t *testing.T) {
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	var first ChoreographyClaimProposal
	calls := 0
	fake := &fakeWorkerReturn{}
	fake.claim = func(p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		calls++
		if calls == 1 {
			first = p
			return ChoreographyReceipt{}, errors.New("reply lost")
		}
		require.Equal(t, first, p, "an unknown claim must retain its exact body")
		query, err := FormatClaimReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(choreographyClaimKind, binding, p.Task, p.Attempt, p.Node,
			p.Branch, p.ProposalID, query.ProposalDigest, "claim-oid"), nil
	}
	fake.lookup = func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}
	w := &Worker{Node: "node-a", ReturnSigner: signer,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}},
		slots:          make(chan struct{}, 1), Log: zerolog.Nop()}
	started, err := w.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err)
	require.False(t, started)
	require.Len(t, w.slots, 1, "one absent receipt read cannot release local capacity")
	require.NotNil(t, w.pendingReturn[tip.Branch])
	require.Equal(t, "ledger-oid", first.ExpectedLedgerOID)
	_, settled, err := w.submitReturnClaim(t.Context(), w.returnChannels[binding.Run], first)
	require.NoError(t, err)
	require.True(t, settled)
	require.Equal(t, 2, calls)
}

func TestWorkerNoSignedCapacityEpochCannotProposeClaim(t *testing.T) {
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	fake := &fakeWorkerReturn{poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return ChoreographyEventPage{}, errors.New("event feed unavailable")
	}, claim: func(ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		t.Fatal("claim cannot be sent without a signed capacity epoch")
		return ChoreographyReceipt{}, nil
	}}
	w := &Worker{Node: "node-a", ReturnSigner: signer,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}},
		slots:          make(chan struct{}, 1), Log: zerolog.Nop()}
	started, err := w.takeTask(t.Context(), tip, assignment)
	require.Error(t, err)
	require.False(t, started)
	require.Empty(t, w.slots, "no proposal was sent, so the slot is safe to reuse")
	require.Empty(t, w.pendingReturn)
}

func TestWorkerRetriesOnePendingClaimWithoutSlotNoticeMovement(t *testing.T) {
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	proposal := (&Worker{Node: "node-a"}).formatReturnClaim(returnClaimRequest{
		Binding: binding, LedgerOID: "ledger-oid", Tip: tip, Assignment: assignment})
	fake := &fakeWorkerReturn{}
	fake.claim = func(got ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		require.Equal(t, proposal, got)
		return ChoreographyReceipt{}, errors.New("unanswered")
	}
	fake.lookup = func(got ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		query, err := FormatClaimReceiptQuery(proposal)
		require.NoError(t, err)
		require.Equal(t, query, got)
		return ChoreographyReceipt{}, false, nil
	}
	worker := &Worker{Node: "node-a", Log: zerolog.Nop(), slots: make(chan struct{}, 1),
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}},
		pendingReturn: map[string]*pendingReturnClaim{tip.Branch: {
			tip: tip, assignment: assignment, proposal: proposal}}}
	worker.slots <- struct{}{}
	require.False(t, worker.settlePendingReturnClaims(t.Context()))
	require.Len(t, worker.slots, 1)
	require.Contains(t, worker.pendingReturn, tip.Branch)
}

func TestWorkerAcceptedClaimSeesCancelBeforeStartingCommand(t *testing.T) {
	binding := testReturnBinding()
	branch := "dispat-task-run-1-pkg-1"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	assignment := validProbe(branch)
	assignment.Kind, assignment.Task = KindBuild, "pkg:build"
	worker, mailbox := newWorkerFixture(t, branch, tip, assignment)
	worker.slots = make(chan struct{}, 1)
	worker.slots <- struct{}{}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	worker.ReturnSigner = signer
	binding.Run, binding.Generation, binding.PlanDigest = assignment.Run, assignment.Generation, assignment.PlanDigest
	fake := &fakeWorkerReturn{ack: func(p ChoreographyAckProposal) (ChoreographyReceipt, error) {
		require.Equal(t, "cancel-oid", p.CancelOID)
		require.Equal(t, release.PartInputs, p.Phase)
		require.False(t, p.CommandStarted)
		query, err := FormatAckReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(MessageAck, binding, p.Task, p.Attempt, p.Node,
			p.Branch, p.ProposalID, query.ProposalDigest, "first-ack-oid"), nil
	}}
	worker.returnChannels = map[string]*workerReturnChannel{assignment.Run: {
		binding: binding, client: fake}}
	mailbox.tips[branch] = ChainTip{Branch: branch, OID: "cancel-oid", Kind: MessageCancel,
		Previous: MessageClaim, PreviousOID: "claim-oid"}
	mailbox.documents[branch] = mustMarshal(t, Withdrawal{Header: assignment.Header,
		Assignment: tip.OID, Tip: "claim-oid"})
	progress, done, err := worker.startReturnedClaim(returnedClaimStart{
		LookupContext: t.Context(), TaskContext: t.Context(), Tip: tip,
		Assignment: assignment, ClaimOID: "claim-oid",
	})
	require.NoError(t, err)
	require.True(t, done)
	require.True(t, progress)
	require.Empty(t, worker.slots)
	require.True(t, worker.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt))
	require.Empty(t, mailbox.written)
}

func TestWorkerLostAcceptedReplyThenCancelAcknowledgesWithoutCommand(t *testing.T) {
	branch := "dispat-task-run-1-pkg-1"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	assignment := validProbe(branch)
	assignment.Kind, assignment.Task = KindBuild, "pkg:build"
	worker, mailbox := newWorkerFixture(t, branch, tip, assignment)
	worker.slots = make(chan struct{}, 1)
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	worker.ReturnSigner = signer
	binding := ChoreographyRunBinding{Run: assignment.Run, Generation: assignment.Generation,
		PlanDigest: assignment.PlanDigest, GraphDigest: "graph"}
	claims, acknowledgements := 0, 0
	var original ChoreographyClaimProposal
	fake := &fakeWorkerReturn{}
	fake.claim = func(p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		claims++
		original = p
		return ChoreographyReceipt{}, errors.New("accepted claim reply lost")
	}
	fake.lookup = func(query ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		exact, err := FormatClaimReceiptQuery(original)
		require.NoError(t, err)
		require.Equal(t, exact, query)
		return testReturnReceipt(choreographyClaimKind, binding, original.Task,
			original.Attempt, original.Node, original.Branch, original.ProposalID,
			query.ProposalDigest, "claim-oid"), true, nil
	}
	fake.ack = func(p ChoreographyAckProposal) (ChoreographyReceipt, error) {
		acknowledgements++
		require.Equal(t, "cancel-oid", p.CancelOID)
		require.False(t, p.CommandStarted)
		query, err := FormatAckReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(MessageAck, binding, p.Task, p.Attempt, p.Node,
			p.Branch, p.ProposalID, query.ProposalDigest, "first-ack-oid"), nil
	}
	worker.returnChannels = map[string]*workerReturnChannel{assignment.Run: {binding: binding, client: fake}}
	mailbox.tips[branch] = ChainTip{Branch: branch, OID: "cancel-oid", Kind: MessageCancel,
		Previous: MessageClaim, PreviousOID: "claim-oid"}
	mailbox.documents[branch] = mustMarshal(t, Withdrawal{Header: assignment.Header,
		Assignment: tip.OID, Tip: "claim-oid"})
	started, err := worker.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err)
	require.True(t, started, "the cancellation acknowledgement is progress, not command execution")
	require.Equal(t, 1, claims, "lost reply is settled by lookup, without a duplicate proposal")
	require.Equal(t, 1, acknowledgements)
	require.Empty(t, worker.slots)
	require.Empty(t, worker.pendingReturn)
	require.Empty(t, mailbox.written)
}

func TestWorkerResultReturnsPackWithoutRemoteGitRef(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	signer := fixture.signer
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1},
		Limits: TransferLimits{MaxPackBytes: 1 << 20}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	var received ChoreographyResultProposal
	fake := &fakeWorkerReturn{}
	fake.result = func(p ChoreographyResultProposal, payload io.ReadSeeker) (ChoreographyReceipt, error) {
		received = p
		bytes, err := io.ReadAll(payload)
		require.NoError(t, err)
		require.Equal(t, p.PayloadBytes, int64(len(bytes)))
		query, err := FormatResultReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(choreographyResultKind, binding, p.Task, p.Attempt,
			p.Node, p.Branch, p.ProposalID, query.ProposalDigest, "first-result-oid"), nil
	}
	w := &Worker{Node: "node-a", Cache: fixture.git, ReturnSigner: signer,
		StateDir: t.TempDir(), Report: NodeReport{Limits: TransferLimits{MaxPackBytes: 1 << 20}},
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	task := &claimedTask{assignment: assignment, tip: tip, claimed: "claim-oid"}
	reported, err := w.returnTaskResult(t.Context(), task, Result{
		Header: assignment.Header, Status: StatusSucceeded}, nil)
	require.NoError(t, err)
	require.Equal(t, "first-result-oid", reported)
	require.Equal(t, "claim-oid", received.ClaimOID)
	require.Equal(t, CompiledClaimInputDigest(tip.OID), received.InputsDigest)
	require.Empty(t, fixture.remoteBranches(t), "the peer must not create a Git ref")
}

func TestWorkerLostResultReplyUsesExactReceipt(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1},
		Limits: TransferLimits{MaxPackBytes: 1 << 20}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	var submitted ChoreographyResultProposal
	fake := &fakeWorkerReturn{}
	fake.result = func(p ChoreographyResultProposal, _ io.ReadSeeker) (ChoreographyReceipt, error) {
		submitted = p
		return ChoreographyReceipt{}, errors.New("accepted but response lost")
	}
	fake.lookup = func(query ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		require.Equal(t, submitted.ProposalID, query.ProposalID)
		require.Equal(t, choreographyResultKind, query.Kind)
		digest, err := ChoreographyProposalDigest(submitted)
		require.NoError(t, err)
		require.Equal(t, digest, query.ProposalDigest)
		return testReturnReceipt(choreographyResultKind, binding, submitted.Task,
			submitted.Attempt, submitted.Node, submitted.Branch, submitted.ProposalID,
			digest, "first-result-oid"), true, nil
	}
	w := &Worker{Node: "node-a", Cache: fixture.git, ReturnSigner: fixture.signer,
		StateDir: t.TempDir(), Report: NodeReport{Limits: TransferLimits{MaxPackBytes: 1 << 20}},
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	task := &claimedTask{assignment: assignment, tip: tip, claimed: "claim-oid"}
	reported, err := w.returnTaskResult(t.Context(), task,
		Result{Header: assignment.Header, Status: StatusSucceeded}, nil)
	require.NoError(t, err)
	require.Equal(t, "first-result-oid", reported)
	require.Empty(t, fixture.remoteBranches(t))
}

func TestWorkerRetriesOnlyTheIdenticalPreparedResult(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1},
		Limits: TransferLimits{MaxPackBytes: 1 << 20}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	var first ChoreographyResultProposal
	calls := 0
	fake := &fakeWorkerReturn{}
	fake.result = func(p ChoreographyResultProposal, _ io.ReadSeeker) (ChoreographyReceipt, error) {
		calls++
		if calls == 1 {
			first = p
			return ChoreographyReceipt{}, errors.New("no reply")
		}
		require.Equal(t, first, p, "retry must preserve pack hash and proposal ID")
		query, err := FormatResultReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(choreographyResultKind, binding, p.Task, p.Attempt,
			p.Node, p.Branch, p.ProposalID, query.ProposalDigest, "first-result-oid"), nil
	}
	fake.lookup = func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}
	worker := &Worker{Node: "node-a", Cache: fixture.git, ReturnSigner: fixture.signer,
		StateDir: t.TempDir(), Report: NodeReport{Limits: TransferLimits{MaxPackBytes: 1 << 20}},
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	task := &claimedTask{assignment: assignment, tip: tip, claimed: "claim-oid"}
	ctx, done := context.WithTimeout(t.Context(), time.Second)
	defer done()
	reported, err := worker.returnTaskResult(ctx, task,
		Result{Header: assignment.Header, Status: StatusSucceeded}, nil)
	require.NoError(t, err)
	require.Equal(t, "first-result-oid", reported)
	require.Equal(t, 2, calls)
	require.Empty(t, fixture.remoteBranches(t))
}

// A Cancel that wins the exact Claim ref while a finished Result is being
// returned must be acknowledged inside the live report window. Repeating the
// rejected Result until that window expires would strand the accepted Claim.
func TestWorkerRejectedResultAcknowledgesExactCancelBeforeRetry(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	branch := FormatChoreographyTaskBranch(binding.Run, "pkg:build")
	tip := ChainTip{Branch: branch, OID: strings.Repeat("a", 40), Kind: MessageAssignment}
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest,
		Node:       "build-a", Task: "pkg:build", Attempt: 1, Branch: branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		GraphDigest: binding.GraphDigest, BootstrapOID: strings.Repeat("c", 40), Limits: TransferLimits{
			MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}
	worker, mailbox := newWorkerFixture(t, branch, tip, assignment)
	worker.Cache, worker.ReturnSigner = fixture.git, fixture.signer
	worker.Report.Limits.MaxPackBytes = 1 << 20
	worker.Endpoint = "file:///canonical-mailbox"
	claimOID, cancelOID := strings.Repeat("b", 40), strings.Repeat("d", 40)
	mailbox.tips[branch] = ChainTip{Branch: branch, OID: cancelOID, Kind: MessageCancel,
		Previous: MessageClaim, PreviousOID: claimOID}
	mailbox.documents[branch] = mustMarshal(t, Withdrawal{Header: assignment.Header,
		Assignment: tip.OID, Tip: claimOID})
	resultCalls, ackCalls := 0, 0
	fake := &fakeWorkerReturn{}
	fake.result = func(ChoreographyResultProposal, io.ReadSeeker) (ChoreographyReceipt, error) {
		resultCalls++
		return ChoreographyReceipt{}, gitx.ErrLeaseRejected
	}
	fake.lookup = func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}
	fake.ack = func(p ChoreographyAckProposal) (ChoreographyReceipt, error) {
		ackCalls++
		require.Equal(t, cancelOID, p.CancelOID)
		require.Equal(t, tip.OID, p.AssignmentOID)
		require.Equal(t, "build", p.Phase)
		require.True(t, p.CommandStarted)
		query, err := FormatAckReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(MessageAck, binding, p.Task, p.Attempt, p.Node,
			p.Branch, p.ProposalID, query.ProposalDigest, strings.Repeat("e", 40)), nil
	}
	worker.returnChannels = map[string]*workerReturnChannel{binding.Run: {binding: binding,
		bootstrapOID: assignment.BootstrapOID, bootstrap: &RunBootstrap{
			Protocol: ChoreographyProtocolVersion, Mode: workerCompiledBootstrapMode,
			ChoreographyRunBinding: binding,
			CoordinationEndpoint:   worker.Endpoint}, client: fake}}
	task := &claimedTask{assignment: assignment, tip: tip, claimed: claimOID}
	task.reportPhase("build", true)
	ctx, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	_, err := worker.returnTaskResult(ctx, task,
		Result{Header: assignment.Header, Status: StatusSucceeded}, nil)
	require.ErrorIs(t, err, errResultWithdrawn)
	require.Equal(t, 1, resultCalls, "the rejected Result must not be retried after exact Cancel")
	require.Equal(t, 1, ackCalls, "the stopped task must acknowledge within the report budget")
	require.NoError(t, ctx.Err())
	require.Empty(t, mailbox.written, "the peer must not push an Ack ref")
}

func TestWorkerCancellationAckReturnsExactCancelWithoutGitPush(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1}}
	fake := &fakeWorkerReturn{}
	fake.ack = func(p ChoreographyAckProposal) (ChoreographyReceipt, error) {
		require.Equal(t, "cancel-oid", p.CancelOID)
		require.Equal(t, "assignment-oid", p.AssignmentOID)
		require.Equal(t, "build", p.Phase)
		require.True(t, p.CommandStarted)
		query, err := FormatAckReceiptQuery(p)
		require.NoError(t, err)
		return testReturnReceipt(MessageAck, binding, p.Task, p.Attempt, p.Node,
			p.Branch, p.ProposalID, query.ProposalDigest, "first-ack-oid"), nil
	}
	w := &Worker{Node: "node-a", ReturnSigner: fixture.signer,
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	task := &claimedTask{assignment: assignment,
		tip: ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "assignment-oid"}, claimed: "claim-oid"}
	ack, err := w.returnTaskAck(t.Context(), taskAckReturnRequest{
		Task: task, CancelOID: "cancel-oid", Phase: "build", CommandStarted: true,
	})
	require.NoError(t, err)
	require.Equal(t, "first-ack-oid", ack)
	require.Empty(t, fixture.remoteBranches(t))
}

func TestWorkerOversizedPackNeverSubmitsResult(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := testReturnBinding()
	assignment := Assignment{Header: Header{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build", Attempt: 1},
		Limits: TransferLimits{MaxPackBytes: 1}}
	tip := ChainTip{Branch: "dispat-task-run-1-pkg-1", OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	fake := &fakeWorkerReturn{result: func(ChoreographyResultProposal,
		io.ReadSeeker) (ChoreographyReceipt, error) {
		t.Fatal("an oversized pack must not reach the return channel")
		return ChoreographyReceipt{}, nil
	}}
	worker := &Worker{Node: "node-a", Cache: fixture.git, ReturnSigner: fixture.signer,
		StateDir: t.TempDir(), Report: NodeReport{Limits: TransferLimits{MaxPackBytes: 1}},
		returnChannels: map[string]*workerReturnChannel{binding.Run: {binding: binding, client: fake}}}
	task := &claimedTask{assignment: assignment, tip: tip, claimed: "claim-oid"}
	_, err := worker.returnTaskResult(t.Context(), task,
		Result{Header: assignment.Header, Status: StatusSucceeded}, nil)
	require.ErrorIs(t, err, gitx.ErrTransportLimit)
	require.Empty(t, fixture.remoteBranches(t))
}
