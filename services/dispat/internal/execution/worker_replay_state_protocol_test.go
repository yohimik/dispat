// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

const (
	workerReplayTestNodeCapacity = 1
	workerReplayTestLeaseWeight  = 1
)

func TestWorkerReadyCompiledTaskWaitsForWritableReplayRecord(t *testing.T) {
	binding := testReturnBinding()
	branch := FormatChoreographyTaskBranch(binding.Run, "pkg:build")
	reservationOID := strings.Repeat("a", 40)
	bootstrapOID := strings.Repeat("c", 40)
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding, Node: "build-a", Tasks: []ChoreographyTaskSpec{{
			Task: "pkg:build", Kind: KindBuild, Branch: branch,
			ReservationOID: reservationOID, Weight: workerReplayTestLeaseWeight,
			TemplateDigest: strings.Repeat("b", 64),
			EligibleNodes:  []string{"build-a"},
		}},
	})
	require.NoError(t, err)
	graphRootOID := strings.Repeat("f", 40)
	frontier.graphRootOID = graphRootOID
	frontier.initialized, frontier.started = true, true
	frontier.cursor = ChoreographyEventCursor{OID: strings.Repeat("d", 40)}
	require.Len(t, frontier.ready, 1, "the fixture must contain a selectable task")

	worker, _ := newWorkerFixture(t, branch, ChainTip{}, validProbe(branch))
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, worker.Node, workerReplayTestNodeCapacity)
	require.NoError(t, err)
	worker.NodeSlots = authority
	worker.Report.AuthorityID = authority.AuthorityID()
	worker.Report.Capacity = workerReplayTestNodeCapacity
	worker.slots = make(chan struct{}, workerReplayTestNodeCapacity)
	claims := 0
	client := &fakeWorkerReturn{
		claim: func(ChoreographyClaimProposal) (ChoreographyReceipt, error) {
			claims++
			return ChoreographyReceipt{}, errors.New("claim response unavailable")
		},
		lookup: func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
			return ChoreographyReceipt{}, false, nil
		},
	}
	run := &workerCompiledRun{binding: binding, bootstrapOID: bootstrapOID,
		rootOID:  graphRootOID,
		frontier: frontier, pending: make(map[string]*workerCompiledClaim),
		channel: &workerReturnChannel{binding: binding, bootstrapOID: bootstrapOID,
			bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion,
				Mode: workerCompiledBootstrapMode, ChoreographyRunBinding: binding,
				CoordinationEndpoint: worker.Endpoint}, client: client}}
	require.NoError(t, os.Mkdir(worker.Seen.path, 0o755))
	progress, err := worker.pickCompiledTasks(t.Context(), run)
	require.Error(t, err)
	require.False(t, progress)
	require.Zero(t, claims, "a ready task cannot be proposed before replay state is writable")
	require.Zero(t, frontier.head, "a refused local precondition must not consume ready selection")
	require.Empty(t, run.pending)
	held, err := authority.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)

	require.NoError(t, os.Remove(worker.Seen.path))
	progress, err = worker.pickCompiledTasks(t.Context(), run)
	require.Error(t, err, "the deliberate lost claim reply remains unsettled")
	require.False(t, progress)
	require.Equal(t, 1, claims, "the repaired replay record permits exact proposal submission")
	require.Contains(t, run.pending, "pkg:build")
	held, err = authority.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	require.Equal(t, run.pending["pkg:build"].lease.ID, held[0].ID)
}

func TestWorkerAcceptedClaimKeepsExactPendingProposalAndDurableSlotOnReplayWriteFailure(t *testing.T) {
	binding := testReturnBinding()
	branch := FormatChoreographyTaskBranch(binding.Run, "pkg:build")
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: "node-a", Task: "pkg:build",
		Attempt: 1, Branch: branch}, Weight: workerReplayTestLeaseWeight,
		GraphDigest: binding.GraphDigest, BootstrapOID: strings.Repeat("c", 40)}
	tip := ChainTip{Branch: branch, OID: strings.Repeat("a", 40)}
	worker, mailbox := newWorkerFixture(t, branch, tip, assignment)
	worker.Node = "node-a"
	worker.Endpoint = "file:///canonical-mailbox"
	worker.Log = zerolog.Nop()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, worker.Node, workerReplayTestNodeCapacity)
	require.NoError(t, err)
	observer, err := OpenNodeSlotAuthority(root, worker.Node, workerReplayTestNodeCapacity)
	require.NoError(t, err)
	worker.NodeSlots = authority
	worker.Report.AuthorityID = authority.AuthorityID()
	worker.slots = make(chan struct{}, workerReplayTestNodeCapacity)
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	worker.ReturnSigner = signer
	claimOID := strings.Repeat("e", 40)
	var accepted ChoreographyClaimProposal
	client := &fakeWorkerReturn{
		poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
			return ChoreographyEventPage{Head: ChoreographyEventCursor{OID: strings.Repeat("d", 40)}}, nil
		},
		claim: func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
			accepted = proposal
			query, err := FormatClaimReceiptQuery(proposal)
			require.NoError(t, err)
			require.NoError(t, os.Remove(worker.Seen.path))
			require.NoError(t, os.Mkdir(worker.Seen.path, 0o755))
			mailbox.tips[branch] = ChainTip{Branch: branch, OID: claimOID, Kind: MessageClaim}
			return testReturnReceipt(choreographyClaimKind, binding, proposal.Task,
				proposal.Attempt, proposal.Node, proposal.Branch, proposal.ProposalID,
				query.ProposalDigest, claimOID), nil
		},
	}
	worker.returnChannels = map[string]*workerReturnChannel{binding.Run: {
		binding: binding, bootstrapOID: assignment.BootstrapOID,
		bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion,
			Mode: workerCompiledBootstrapMode, ChoreographyRunBinding: binding,
			CoordinationEndpoint: worker.Endpoint}, client: client}}

	started, err := worker.takeTask(t.Context(), tip, assignment)
	require.NoError(t, err, "an accepted claim with failed local replay write stays pending")
	require.False(t, started)
	require.NotEmpty(t, accepted.ProposalID)
	require.Contains(t, worker.pendingReturn, branch)
	pending := worker.pendingReturn[branch]
	require.Equal(t, accepted, pending.proposal)
	require.NotEmpty(t, pending.lease.ID)
	held, err := observer.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	require.Equal(t, pending.lease.ID, held[0].ID,
		"the exact accepted lease remains charged across another process")
	require.Len(t, worker.slots, 1)
	require.Empty(t, mailbox.written, "a replay write failure cannot start task effects")
	_, available, err := observer.TryReserve(t.Context(), NodeSlotIntent{
		Run: "another-run", Task: "another:build", Attempt: 1,
		Weight: workerReplayTestLeaseWeight})
	require.NoError(t, err)
	require.False(t, available)
}
