// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type compiledRefreshFixture struct {
	worker    *Worker
	run       *workerCompiledRun
	client    *fakeWorkerReturn
	proposals []ChoreographyClaimProposal
	fence     ChoreographyEventCursor
}

func newCompiledRefreshFixture(t *testing.T) *compiledRefreshFixture {
	t.Helper()
	return newCompiledRefreshFixtureForRun(t, "run-1")
}

func newCompiledRefreshFixtureForRun(t *testing.T, runID string) *compiledRefreshFixture {
	t.Helper()
	binding := testReturnBinding()
	binding.Run = runID
	rootOID := strings.Repeat("f", 40)
	worker, _ := newWorkerFixture(t, "unused", ChainTip{}, validProbe("unused"))
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, worker.Node, 2)
	require.NoError(t, err)
	worker.NodeSlots, worker.Report.AuthorityID, worker.Report.Capacity = authority, authority.AuthorityID(), 2
	worker.slots = make(chan struct{}, 2)
	var tasks []ChoreographyTaskSpec
	for _, task := range []string{"api:build", "cli:build"} {
		tasks = append(tasks, ChoreographyTaskSpec{Task: task, Kind: KindBuild,
			Branch: FormatChoreographyTaskBranch(binding.Run, task), ReservationOID: strings.Repeat("a", 40),
			Weight: 1, TemplateDigest: strings.Repeat("b", 64), EligibleNodes: []string{worker.Node},
			Placement: PlacementWorker, Budget: ChoreographyBudgetBuild})
	}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{Binding: binding, Node: worker.Node, Tasks: tasks})
	require.NoError(t, err)
	frontier.graphRootOID, frontier.initialized, frontier.started = rootOID, true, true
	frontier.cursor = ChoreographyEventCursor{OID: strings.Repeat("c", 40), Sequence: 1}
	bootstrapOID := strings.Repeat("d", 40)
	fixture := &compiledRefreshFixture{worker: worker,
		fence: ChoreographyEventCursor{OID: strings.Repeat("e", 40), Sequence: 2}}
	fixture.client = &fakeWorkerReturn{lookup: func(ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
		return ChoreographyReceipt{}, false, nil
	}}
	fixture.run = &workerCompiledRun{binding: binding, bootstrapOID: bootstrapOID,
		rootOID: rootOID, frontier: frontier, membershipEpoch: frontier.cursor.OID,
		bootstrap: RunBootstrap{Mode: bootstrapCompilationModeName},
		pending:   make(map[string]*workerCompiledClaim), parked: make(map[string]compiledParkedCapacity),
		channel: &workerReturnChannel{binding: binding, bootstrapOID: bootstrapOID,
			bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion, Mode: bootstrapCompilationModeName,
				ChoreographyRunBinding: binding, CoordinationEndpoint: worker.Endpoint}, client: fixture.client}}
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return fixture.refusal(t, proposal, ChoreographyRefusalStale), nil
	}
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		cursor := fixture.run.frontier.Cursor()
		return ChoreographyEventPage{Cursor: cursor, Head: cursor}, nil
	}
	return fixture
}

func (fixture *compiledRefreshFixture) refusal(t *testing.T, proposal ChoreographyClaimProposal, reason string) ChoreographyReceipt {
	t.Helper()
	query, err := FormatClaimReceiptQuery(proposal)
	require.NoError(t, err)
	return ChoreographyReceipt{ChoreographyRunBinding: proposal.ChoreographyRunBinding,
		Kind: choreographyClaimKind, Task: proposal.Task, Attempt: proposal.Attempt, Node: proposal.Node,
		Branch: proposal.Branch, GraphRootOID: proposal.GraphRootOID, OID: proposal.ExpectedOID,
		ProposalID: proposal.ProposalID, ProposalDigest: query.ProposalDigest,
		Refused: true, RefusalReason: reason, FenceOID: fixture.fence.OID, FenceSequence: fixture.fence.Sequence}
}

func (fixture *compiledRefreshFixture) page(events ...ChoreographyEvent) ChoreographyEventPage {
	for index := range events {
		events[index].Sequence = fixture.run.frontier.Cursor().Sequence + uint64(index) + 1
		events[index].LedgerOID = fixture.fence.OID
	}
	return ChoreographyEventPage{Cursor: fixture.fence, Head: fixture.fence, Events: events}
}

func (fixture *compiledRefreshFixture) fenceEvent() ChoreographyEvent {
	task := fixture.run.frontier.tasks["api:build"].spec
	return ChoreographyEvent{Kind: ChoreographyClaimRefused, Task: task.Task, TaskOID: task.ReservationOID,
		Attempt: 1, Node: fixture.worker.Node, GraphRootOID: fixture.run.rootOID,
		ProposalID: "other-proposal", ProposalDigest: "other-digest", RefusalReason: ChoreographyRefusalStale}
}

func (fixture *compiledRefreshFixture) requireNoHeldSlots(t *testing.T) {
	t.Helper()
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
	require.Empty(t, fixture.worker.slots)
}

func TestCompiledStaleClaimStopsNewSelectionUntilAppliedFeedReachesFence(t *testing.T) {
	for _, mode := range []string{bootstrapCompilationModeName, bootstrapInterpretationModeName, bootstrapHybridModeName} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			fixture.run.bootstrap.Mode = mode
			progress, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
			require.NoError(t, err)
			require.True(t, progress)
			require.Len(t, fixture.proposals, 1, "the second ready task must not reuse a known-stale epoch")
			require.NotNil(t, fixture.run.claimRefresh)
			require.Empty(t, fixture.run.pending)
			fixture.requireNoHeldSlots(t)
			progress, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
			require.NoError(t, err)
			require.False(t, progress)
			require.Len(t, fixture.proposals, 1)

			for _, sequence := range []uint64{fixture.run.frontier.Cursor().Sequence, fixture.fence.Sequence} {
				fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
					return ChoreographyEventPage{Cursor: fixture.run.frontier.Cursor(),
						Head: ChoreographyEventCursor{OID: fixture.fence.OID, Sequence: sequence}}, nil
				}
				_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
				require.NoError(t, err)
				require.NotNil(t, fixture.run.claimRefresh, "an advisory newer Head cannot replace the applied cursor")
			}
			fixture.client.pollWithContext = func(ctx context.Context, _ ChoreographyEventRequest) (ChoreographyEventPage, error) {
				return ChoreographyEventPage{}, ctx.Err()
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = fixture.worker.pollCompiledEvents(cancelled, fixture.run)
			require.ErrorIs(t, err, context.Canceled)
			require.NotNil(t, fixture.run.claimRefresh)
			fixture.client.pollWithContext = nil

			fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				return fixture.page(fixture.fenceEvent()), nil
			}
			_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
			require.NoError(t, err)
			require.Nil(t, fixture.run.claimRefresh)
			fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
				fixture.proposals = append(fixture.proposals, proposal)
				return ChoreographyReceipt{}, errors.New("reply unavailable")
			}
			_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
			require.ErrorContains(t, err, "reply unavailable")
			require.Len(t, fixture.proposals, 2)
			require.Equal(t, fixture.fence.OID, fixture.proposals[1].ExpectedLedgerOID)
		})
	}
}

func TestCompiledPendingStaleClaimKeepsExactUnknownProposalThenRequiresRefresh(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return ChoreographyReceipt{}, errors.New("reply unavailable")
	}
	for range 2 {
		_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
		require.ErrorContains(t, err, "reply unavailable")
	}
	require.Equal(t, fixture.proposals[0], fixture.proposals[1])
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	require.Len(t, fixture.run.pending, 1)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return fixture.refusal(t, proposal, ChoreographyRefusalStale), nil
	}
	progress, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.True(t, progress)
	require.Len(t, fixture.proposals, 3, "reconciliation must not be followed by a new stale proposal")
	require.Equal(t, fixture.proposals[0], fixture.proposals[2])
	fixture.requireNoHeldSlots(t)
	require.NotNil(t, fixture.run.claimRefresh)

	fixture.run.frontier.cursor = ChoreographyEventCursor{OID: "already-applied-later-ledger", Sequence: 3}
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Nil(t, fixture.run.claimRefresh, "a delayed receipt must not demand an event after the already-applied fence")
}

func TestCompiledStaleRefreshAppliesStoppingMembershipAndCompetingClaimBeforeSelection(t *testing.T) {
	for _, kind := range []ChoreographyEventKind{ChoreographyRunStopping, ChoreographyMemberDraining, ChoreographyClaimAccepted} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
			require.NoError(t, err)
			event := ChoreographyEvent{Kind: kind, Task: choreographyRunTaskID, TaskOID: fixture.run.rootOID,
				GraphRootOID: fixture.run.rootOID}
			switch kind {
			case ChoreographyMemberDraining:
				event.Task, event.Node = choreographyMembershipTaskID, fixture.worker.Node
				event.Member = &ChoreographyRuntimeNode{Name: fixture.worker.Node, Capacity: 2}
			case ChoreographyClaimAccepted:
				task := fixture.run.frontier.tasks[fixture.proposals[0].Task].spec
				event.Task, event.TaskOID, event.PriorTaskOID = task.Task, "competing-claim", task.ReservationOID
				event.Node, event.Attempt, event.AssignmentOID = "another-node", 1, "assignment"
				fixture.run.frontier.tasks["cli:build"].state = frontierBusy
			}
			fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
				return fixture.page(event), nil
			}
			_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
			require.NoError(t, err)
			require.Nil(t, fixture.run.claimRefresh)
			_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
			require.NoError(t, err)
			require.Len(t, fixture.proposals, 1)
			fixture.requireNoHeldSlots(t)
		})
	}
}

func TestCompiledStaleRefreshRejectsConflictingFenceWithoutNewLease(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	page := fixture.page(fixture.fenceEvent())
	page.Events[0].LedgerOID = "different-fence"
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) { return page, nil }
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.ErrorContains(t, err, "conflicts with the signed run feed")
	require.NotNil(t, fixture.run.claimRefresh)
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Len(t, fixture.proposals, 1)
	fixture.requireNoHeldSlots(t)
}

func TestCompiledStaleRefreshDoesNotBlockAnotherRun(t *testing.T) {
	blocked := newCompiledRefreshFixture(t)
	other := newCompiledRefreshFixtureForRun(t, "other-run")
	_, err := blocked.worker.pickCompiledTasks(t.Context(), blocked.run)
	require.NoError(t, err)
	other.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		other.proposals = append(other.proposals, proposal)
		return ChoreographyReceipt{}, errors.New("another run proposed")
	}
	_, err = blocked.worker.pickCompiledTasks(t.Context(), other.run)
	require.ErrorContains(t, err, "another run proposed")
	require.Len(t, other.proposals, 1)
	require.NotNil(t, blocked.run.claimRefresh)
}

func TestCompiledMalformedStaleReceiptRetainsExactPendingLease(t *testing.T) {
	for _, malformed := range []string{"missing fence sequence", "unchanged fence epoch", "unadvanced fence sequence"} {
		t.Run(malformed, func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			switch malformed {
			case "missing fence sequence":
				fixture.fence.Sequence = 0
			case "unchanged fence epoch":
				fixture.fence.OID = fixture.run.frontier.Cursor().OID
			case "unadvanced fence sequence":
				fixture.fence.Sequence = fixture.run.frontier.Cursor().Sequence
			}
			for range 2 {
				_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
				require.Error(t, err)
			}
			require.Len(t, fixture.proposals, 2)
			require.Equal(t, fixture.proposals[0], fixture.proposals[1])
			require.Len(t, fixture.run.pending, 1)
			require.Nil(t, fixture.run.claimRefresh)
			held, err := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, err)
			require.Len(t, held, 1)
			require.Len(t, fixture.worker.slots, 1)
		})
	}
}

func TestCompiledCapacityRefusalStillParksUntilSignedRelease(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		receipt := fixture.refusal(t, proposal, ChoreographyRefusalCapacity)
		receipt.CapacityScope, receipt.Budget = compiledCapacityScopeBudget, ChoreographyBudgetBuild
		return receipt, nil
	}
	_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Len(t, fixture.proposals, 2)
	require.Len(t, fixture.run.parked, 2)
	require.Nil(t, fixture.run.claimRefresh)
	fixture.requireNoHeldSlots(t)
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Len(t, fixture.proposals, 2, "polling alone cannot release a capacity refusal")

	provider := &choreographyFrontierTask{spec: ChoreographyTaskSpec{Task: "provider", Weight: 1},
		state: frontierBusy, attempt: 1, tipOID: "provider-claim"}
	fixture.run.frontier.tasks[provider.spec.Task] = provider
	event := ChoreographyEvent{Kind: ChoreographyTerminal, Task: provider.spec.Task, TaskOID: "provider-terminal",
		Attempt: 1, Node: "provider-node", GraphRootOID: fixture.run.rootOID,
		Weight: 1, Budget: ChoreographyBudgetBuild, Succeeded: true, OutputOID: "provider-result"}
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return fixture.page(event), nil
	}
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Empty(t, fixture.run.parked)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return ChoreographyReceipt{}, errors.New("claim after capacity release")
	}
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.ErrorContains(t, err, "claim after capacity release")
	require.Len(t, fixture.proposals, 3)
}

func TestCompiledStaleRefreshWaitsThroughAllPagesAndLaggingCursors(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	fixture.fence.Sequence = 3
	_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	event := fixture.fenceEvent()
	event.Sequence, event.LedgerOID = 2, "intermediate-ledger"
	lagging := ChoreographyEventPage{Events: []ChoreographyEvent{event},
		Cursor: ChoreographyEventCursor{OID: event.LedgerOID, Sequence: event.Sequence},
		Head:   ChoreographyEventCursor{OID: event.LedgerOID, Sequence: event.Sequence}}
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) { return lagging, nil }
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.NotNil(t, fixture.run.claimRefresh)
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Len(t, fixture.proposals, 1)

	fencePage := fixture.page(fixture.fenceEvent())
	fencePage.More = true
	fencePage.Head = ChoreographyEventCursor{OID: "later-ledger", Sequence: 4}
	last := fixture.fenceEvent()
	last.Sequence, last.LedgerOID = 4, fencePage.Head.OID
	finalPage := ChoreographyEventPage{Events: []ChoreographyEvent{last}, Cursor: fencePage.Head, Head: fencePage.Head}
	pages := []ChoreographyEventPage{fencePage, finalPage}
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		require.NotNil(t, fixture.run.claimRefresh, "reaching the fence cannot admit work before the remaining signed events")
		page := pages[0]
		pages = pages[1:]
		return page, nil
	}
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Empty(t, pages)
	require.Nil(t, fixture.run.claimRefresh)
	require.Equal(t, finalPage.Cursor, fixture.run.frontier.Cursor())
}

func TestCompiledPendingStaleReceiptPreservesAlreadyAppliedCompetingClaim(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return ChoreographyReceipt{}, errors.New("reply unavailable")
	}
	_, err := fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.ErrorContains(t, err, "reply unavailable")
	proposal := fixture.proposals[0]
	event := ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: proposal.Task,
		TaskOID: "competing-claim", PriorTaskOID: proposal.ExpectedOID, Node: "another-node",
		Attempt: 1, AssignmentOID: "assignment", GraphRootOID: fixture.run.rootOID}
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return fixture.page(event), nil
	}
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Equal(t, frontierBusy, fixture.run.frontier.tasks[proposal.Task].state)
	fixture.client.claim = func(proposal ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, proposal)
		return fixture.refusal(t, proposal, ChoreographyRefusalStale), nil
	}
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Len(t, fixture.proposals, 2)
	require.Equal(t, proposal, fixture.proposals[1])
	require.Equal(t, frontierBusy, fixture.run.frontier.tasks[proposal.Task].state)
	require.NotNil(t, fixture.run.claimRefresh)
	fixture.requireNoHeldSlots(t)
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return ChoreographyEventPage{Cursor: fixture.fence, Head: fixture.fence}, nil
	}
	_, err = fixture.worker.pollCompiledEvents(t.Context(), fixture.run)
	require.NoError(t, err)
	require.Nil(t, fixture.run.claimRefresh)
	fixture.client.claim = func(next ChoreographyClaimProposal) (ChoreographyReceipt, error) {
		fixture.proposals = append(fixture.proposals, next)
		return ChoreographyReceipt{}, errors.New("another task proposed")
	}
	_, err = fixture.worker.pickCompiledTasks(t.Context(), fixture.run)
	require.ErrorContains(t, err, "another task proposed")
	require.Len(t, fixture.proposals, 3)
	require.NotEqual(t, proposal.Task, fixture.proposals[2].Task)
}
