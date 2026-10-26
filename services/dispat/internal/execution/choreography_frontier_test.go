// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type testedOutcomePolicy struct{ status map[string]string }

type testedSweepVirtualPolicy struct {
	status  map[string]string
	blocked map[string]string
}

func (p *testedSweepVirtualPolicy) ValidateOutcome(pkg, status string) error {
	if pkg == "" || status != "skipped" && status != "failed" {
		return fmt.Errorf("invalid sweep outcome")
	}
	return nil
}

func (p *testedSweepVirtualPolicy) ApplyOutcome(pkg, status string) error {
	if err := p.ValidateOutcome(pkg, status); err != nil {
		return err
	}
	p.status[pkg] = status
	if pkg == "middle" && status == "skipped" {
		p.blocked["leaf"] = "middle"
	}
	return nil
}

func (p *testedSweepVirtualPolicy) ShouldSkip(pkg string) (bool, string, error) {
	provider := p.blocked[pkg]
	return provider != "", provider, nil
}

func (p *testedOutcomePolicy) ValidateOutcome(pkg, status string) error {
	if status == "" || pkg == "" {
		return fmt.Errorf("invalid package outcome")
	}
	return nil
}

func (p *testedOutcomePolicy) ApplyOutcome(pkg, status string) error {
	if err := p.ValidateOutcome(pkg, status); err != nil {
		return err
	}
	p.status[pkg] = status
	return nil
}

func (p *testedOutcomePolicy) ShouldSkip(pkg string) (bool, string, error) {
	return pkg == "app" && p.status["core"] == "failed", "core", nil
}

func TestInterpretedFrontierAppliesClaimRetryAndTerminalEdges(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "assets", EligibleNodes: []string{"node-a"}},
			{Task: "ui", EligibleNodes: []string{"node-b"}, Predecessors: []string{"assets"}},
			{Task: "docs", EligibleNodes: []string{"node-c"}, Predecessors: []string{"assets"}},
			{Task: "app", EligibleNodes: []string{"node-b"}, Predecessors: []string{"ui", "docs"}},
			{Task: "publish", LocalAuthority: true, Predecessors: []string{"app"}},
		},
	})
	require.NoError(t, err)
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready)
	page := ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "assets", Attempt: 1, Node: "node-a", TaskOID: "claim-a"},
			{Sequence: 2, Kind: ChoreographyTerminal, Task: "assets", Attempt: 1, Node: "node-a", TaskOID: "done-a", OutputOID: "result-a", Succeeded: true},
		}}
	require.NoError(t, frontier.ApplyEventPage(page))
	chosen, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "ui", chosen.Spec.Task)
	outputOID, found := frontier.CompletedOutput("assets")
	require.True(t, found)
	require.Equal(t, "result-a", outputOID)
	require.Equal(t, 1, chosen.Attempt)
	_, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "docs belongs to another node and app still has two predecessors")
	page = ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-4", Sequence: 4},
		Events: []ChoreographyEvent{
			{Sequence: 3, Kind: ChoreographyClaimAccepted, Task: "ui", Attempt: 1, Node: "node-b", TaskOID: "claim-ui"},
			{Sequence: 4, Kind: ChoreographyRetryReady, Task: "ui", Attempt: 1, Node: "node-b",
				TaskOID: "retry-ui", ResultReceiptOID: "ack-ui"},
		}}
	require.NoError(t, frontier.ApplyEventPage(page))
	chosen, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "ui", chosen.Spec.Task)
	require.Equal(t, 2, chosen.Attempt)
	page = ChoreographyEventPage{Cursor: ChoreographyEventCursor{OID: "ledger-8", Sequence: 8},
		Events: []ChoreographyEvent{
			{Sequence: 5, Kind: ChoreographyClaimAccepted, Task: "ui", Attempt: 2, Node: "node-b", TaskOID: "claim-ui-2"},
			{Sequence: 6, Kind: ChoreographyTerminal, Task: "ui", Attempt: 2, Node: "node-b", TaskOID: "done-ui", OutputOID: "result-ui", Succeeded: true},
			{Sequence: 7, Kind: ChoreographyClaimAccepted, Task: "docs", Attempt: 1, Node: "node-c", TaskOID: "claim-docs"},
			{Sequence: 8, Kind: ChoreographyTerminal, Task: "docs", Attempt: 1, Node: "node-c", TaskOID: "done-docs", OutputOID: "result-docs", Succeeded: true},
		}}
	require.NoError(t, frontier.ApplyEventPage(page))
	chosen, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "app", chosen.Spec.Task)
	_, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "local publish is never a peer claim")
}

func TestCompiledFrontierRequeuesAfterSignedCancelGraceAndSettlesLateAck(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding: binding, GraphRootOID: "root",
		Node: ChoreographyRuntimeNode{Name: "node-a", Capacity: 2},
		Tasks: []ChoreographyTaskSpec{
			{Task: "build", Kind: KindBuild, Placement: PlacementAnyNode,
				Weight: 1, ReservationOID: "reservation"},
			{Task: "consumer", Kind: KindBuild, Placement: PlacementAnyNode,
				Weight: 1, ReservationOID: "consumer-reservation", Predecessors: []string{"build"}},
		},
	})
	require.NoError(t, err)
	apply := func(events ...ChoreographyEvent) {
		t.Helper()
		for i := range events {
			events[i].Sequence = frontier.cursor.Sequence + uint64(i) + 1
		}
		require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
			Events: events,
			Cursor: ChoreographyEventCursor{OID: fmt.Sprintf("ledger-%d", frontier.cursor.Sequence+uint64(len(events))),
				Sequence: frontier.cursor.Sequence + uint64(len(events))},
		}))
	}
	apply(ChoreographyEvent{Kind: ChoreographyInitializationComplete, Task: choreographyRunTaskID,
		TaskOID: "root", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyExecutionStarted, Task: choreographyRunTaskID,
			TaskOID: "root", GraphRootOID: "root", AdmissionDeadlineUTC: "deadline"})
	chosen, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "build", chosen.Spec.Task)
	apply(ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: "build", Attempt: 1,
		Node: "node-a", TaskOID: "claim-one", PriorTaskOID: "reservation",
		PickupID: "pickup-one", AssignmentOID: "assignment-one", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyCancelIssued, Task: "build", Attempt: 1,
			Node: "node-a", TaskOID: "cancel-one", PriorTaskOID: "claim-one",
			PickupID: "pickup-one", ClaimOID: "claim-one",
			CancelReplyDeadlineUTC: "deadline", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyPickupContactLost, Task: "build", Attempt: 1,
			Node: "node-a", TaskOID: "cancel-one", PriorTaskOID: "cancel-one",
			PickupID: "pickup-one", ClaimOID: "claim-one", CancelReplyDeadlineUTC: "deadline", GraphRootOID: "root"})
	chosen, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, 2, chosen.Attempt)
	apply(ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: "build", Attempt: 2,
		Node: "node-a", TaskOID: "claim-two", PriorTaskOID: "cancel-one",
		PickupID: "pickup-two", AssignmentOID: "assignment-two", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyAckAccepted, Task: "build", Attempt: 1,
			Node: "node-a", TaskOID: "claim-two", PriorTaskOID: "claim-two",
			PickupID: "pickup-one", ClaimOID: "claim-one", ResultReceiptOID: "ack-one",
			ProposalDigest: "digest-one", ProposalID: "proposal-one", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyPickupSettled, Task: "build", Attempt: 1,
			Node: "node-a", TaskOID: "claim-two", PriorTaskOID: "claim-two",
			PickupID: "pickup-one", ClaimOID: "claim-one", ResultReceiptOID: "ack-one",
			GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyResultAccepted, Task: "build", Attempt: 2,
			Node: "node-a", TaskOID: "claim-two", PriorTaskOID: "claim-two",
			PickupID: "pickup-two", ClaimOID: "claim-two", ResultReceiptOID: "receipt-two",
			OutputOID: "result-two", ProposalDigest: "digest-two", ProposalID: "proposal-two", GraphRootOID: "root"},
		ChoreographyEvent{Kind: ChoreographyTerminal, Task: "build", Attempt: 2,
			Node: "node-a", TaskOID: "terminal-two", PriorTaskOID: "claim-two",
			PickupID: "pickup-two", ClaimOID: "claim-two", ResultReceiptOID: "receipt-two",
			OutputOID: "result-two", Succeeded: true, GraphRootOID: "root"})
	outputOID, found := frontier.CompletedOutput("build")
	require.True(t, found)
	require.Equal(t, "result-two", outputOID)
	chosen, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "consumer", chosen.Spec.Task)
}

func TestInterpretedFrontierPreservesParallelPrecedenceEdges(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	f, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "provider", EligibleNodes: []string{"node-a"}},
			{Task: "consumer", EligibleNodes: []string{"node-b"},
				Predecessors: []string{"provider", "provider"}},
		},
	})
	require.NoError(t, err, "two typed graph edges may share endpoint tasks")
	require.Equal(t, 2, f.tasks["consumer"].remaining)
	_, ready, err := f.SelectReady()
	require.NoError(t, err)
	require.False(t, ready)
	require.NoError(t, f.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "provider", Attempt: 1,
				Node: "node-a", TaskOID: "claim-provider"},
			{Sequence: 2, Kind: ChoreographyTerminal, Task: "provider", Attempt: 1,
				Node: "node-a", TaskOID: "done-provider", OutputOID: "result-provider", Succeeded: true},
		}}))
	require.Zero(t, f.tasks["consumer"].remaining, "one terminal discharges both direct edges")
	chosen, ready, err := f.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "consumer", chosen.Spec.Task)
	_, ready, err = f.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "parallel edges do not duplicate a claimable task")
}

func TestFrontierQueueRetryRequiresExactUnclaimedOffer(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	makeFrontier := func() *ChoreographyFrontier {
		f, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
			Binding: binding,
			Node:    "node-a",
			Tasks: []ChoreographyTaskSpec{{
				Task: "task", Kind: KindBuild, ReservationOID: "reservation",
				EligibleNodes: []string{"node-a"}}},
		})
		require.NoError(t, err)
		return f
	}
	offer := ChoreographyEvent{Sequence: 1, Kind: ChoreographyAssignmentOffered,
		Task: "task", Attempt: 1, Node: "node-a", PriorTaskOID: "reservation",
		TaskOID: "assignment"}
	retry := ChoreographyEvent{Sequence: 2, Kind: ChoreographyRetryReady,
		Task: "task", Attempt: 1, Node: "node-a", PriorTaskOID: "assignment",
		TaskOID: "queue-decision"}
	f := makeFrontier()
	require.NoError(t, f.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{offer, retry}}))
	ready, ok, err := f.SelectReady()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, ready.Attempt)

	wrong := makeFrontier()
	retry.PriorTaskOID = "another-assignment"
	require.ErrorContains(t, wrong.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{offer, retry}}), "exact offer")

	exhausted := makeFrontier()
	fence := ChoreographyEvent{Sequence: 2, Kind: ChoreographyQueueFenced,
		Task: "task", Attempt: 1, Node: "node-a", PriorTaskOID: "assignment",
		TaskOID: "queue-fence"}
	require.NoError(t, exhausted.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{offer, fence}}))
	_, ok, err = exhausted.SelectReady()
	require.NoError(t, err)
	require.False(t, ok, "a fenced queue cannot claim or discharge children before hooks settle")
	terminal := ChoreographyEvent{Sequence: 3, Kind: ChoreographyTerminal,
		Task: "task", Attempt: 1, Node: "node-a", PriorTaskOID: "queue-fence",
		TaskOID: "queue-terminal"}
	require.NoError(t, exhausted.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
		Events: []ChoreographyEvent{terminal}}))
	_, ok, err = exhausted.SelectReady()
	require.NoError(t, err)
	require.False(t, ok, "an exhausted unclaimed offer is logically terminal")

	unsafe := makeFrontier()
	terminal.Sequence, terminal.PriorTaskOID = 2, "assignment"
	require.ErrorContains(t, unsafe.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{offer, terminal}}), "settled attempt")
}

func TestFrontierCancellationBindsTheAcceptedClaimBeforeAck(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	f, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-a",
		Tasks: []ChoreographyTaskSpec{{
			Task: "task", Kind: KindBuild, ReservationOID: "reservation",
			EligibleNodes: []string{"node-a"}}},
	})
	require.NoError(t, err)
	require.NoError(t, f.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "task", Attempt: 1,
				Node: "node-a", TaskOID: "claim", PriorTaskOID: "reservation"},
			{Sequence: 2, Kind: ChoreographyCancelIssued, Task: "task", Attempt: 1,
				Node: "node-a", TaskOID: "cancel", PriorTaskOID: "claim"},
		}}))
	wrong, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-a",
		Tasks: []ChoreographyTaskSpec{{
			Task: "task", Kind: KindBuild, ReservationOID: "reservation",
			EligibleNodes: []string{"node-a"}}},
	})
	require.NoError(t, err)
	require.ErrorContains(t, wrong.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "task", Attempt: 1,
				Node: "node-a", TaskOID: "claim", PriorTaskOID: "reservation"},
			{Sequence: 2, Kind: ChoreographyCancelIssued, Task: "task", Attempt: 1,
				Node: "node-a", TaskOID: "cancel", PriorTaskOID: "another-claim"},
		}}), "exact active claim")
}

func TestClaimFencingRequiresAConflictingSignedTaskTransition(t *testing.T) {
	proposal := ChoreographyClaimProposal{Task: "ui", Attempt: 1, Node: "node-b", ExpectedOID: "reservation"}
	require.False(t, IsClaimProposalFencedByEvent(proposal,
		ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: "ui", Attempt: 1,
			Node: "node-b", TaskOID: "own-claim"}), "own accepted claim still needs its exact receipt")
	require.True(t, IsClaimProposalFencedByEvent(proposal,
		ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: "ui", Attempt: 1,
			Node: "node-c", TaskOID: "foreign-claim"}))
	require.False(t, IsClaimProposalFencedByEvent(proposal,
		ChoreographyEvent{Kind: ChoreographyClaimAccepted, Task: "other", Attempt: 1,
			Node: "node-c", TaskOID: "other-claim"}))
	require.False(t, IsClaimProposalFencedByEvent(proposal,
		ChoreographyEvent{Kind: ChoreographyBlocked, Task: "ui", Attempt: 1,
			TaskOID: "blocked-proof"}), "a branchless proof does not advance the task ref")
	require.True(t, IsClaimProposalFencedByEvent(proposal,
		ChoreographyEvent{Kind: ChoreographyBlocked, Task: "ui", Attempt: 1,
			PriorTaskOID: "reservation", TaskOID: "blocked-commit"}),
		"an exact reservation-to-blocked CAS fences the old claim")
}

func TestInterpretedFrontierWaitsForSignedFailureAndBlockedFinality(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "core", Package: "core", EligibleNodes: []string{"node-a"}},
			{Task: "app-build", Package: "app", EligibleNodes: []string{"node-b"}, Predecessors: []string{"core"}},
			{Task: "app-test", Package: "app", EligibleNodes: []string{"node-b"}, Predecessors: []string{"app-build"}},
		},
	})
	require.NoError(t, err)
	policy := &testedOutcomePolicy{status: map[string]string{}}
	require.NoError(t, frontier.AttachOutcomePolicy(policy))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "core", Package: "core",
				Attempt: 1, Node: "node-a", TaskOID: "claim-core"},
			{Sequence: 2, Kind: ChoreographyTerminal, Task: "core", Package: "core",
				PackageOutcome: "failed", Attempt: 1, Node: "node-a", TaskOID: "failed-core"},
		},
	}))
	chosen, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "a failed provider cannot leave its consumer claimable")
	require.Empty(t, chosen.Spec.Task)
	blocked, found := frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, ChoreographyBlockedTask{Task: "app-build", Package: "app", BlockedBy: "core"}, blocked)
	blocked, found = frontier.PeekBlocked()
	require.True(t, found, "the local conclusion is retained until a signed Blocked event")
	require.Equal(t, "app-build", blocked.Task)

	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
		Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyBlocked, Task: "app-build",
			Package: "app", PackageOutcome: "skipped", BlockedBy: "core", Attempt: 1,
			TaskOID: "blocked-app-build"}},
	}))
	chosen, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "a later stage of the same skipped package cannot be claimed")
	require.Empty(t, chosen.Spec.Task)
	blocked, found = frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, ChoreographyBlockedTask{Task: "app-test", Package: "app", BlockedBy: "app"}, blocked)
}

func TestInterpretedFrontierBlocksRequiredPreparedInputWithoutPackageOutcome(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "ui:prepare", Package: "ui", Kind: KindPrepare, EligibleNodes: []string{"node-a"}},
			{Task: "app-build", Package: "app", Kind: KindBuild, EligibleNodes: []string{"node-b"},
				Predecessors: []string{"ui:prepare"}, PreparedProviders: []string{"ui"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "ui:prepare", Package: "ui",
				Attempt: 1, Node: "node-a", TaskOID: "claim-ui"},
			{Sequence: 2, Kind: ChoreographyTerminal, Task: "ui:prepare", Package: "ui",
				Attempt: 1, Node: "node-a", TaskOID: "failed-ui", Succeeded: false},
		},
	}))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "a failed prepared provider cannot make its consumer claimable")
	blocked, found := frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, ChoreographyBlockedTask{Task: "app-build", Package: "app", BlockedBy: "ui"}, blocked)
}

func TestInterpretedFrontierOwnDeadPackagePrecedesInheritedPreparedBlocker(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "assets:prepare", Package: "assets", Kind: KindPrepare, EligibleNodes: []string{"node-a"}},
			{Task: "docs-build", Package: "docs", Kind: KindBuild, EligibleNodes: []string{"node-b"},
				Predecessors: []string{"assets:prepare"}, PreparedProviders: []string{"assets"}},
			{Task: "docs-test", Package: "docs", Kind: KindStage, EligibleNodes: []string{"node-b"},
				Predecessors: []string{"docs-build"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "assets:prepare", Package: "assets",
				Attempt: 1, Node: "node-a", TaskOID: "claim-assets"},
			{Sequence: 2, Kind: ChoreographyTerminal, Task: "assets:prepare", Package: "assets",
				Attempt: 1, Node: "node-a", TaskOID: "failed-assets"},
		},
	}))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready)
	blocked, found := frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, ChoreographyBlockedTask{Task: "docs-build", Package: "docs", BlockedBy: "assets"}, blocked)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
		Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyBlocked, Task: "docs-build",
			Package: "docs", PackageOutcome: "failed", BlockedBy: "assets", Attempt: 1,
			TaskOID: "blocked-docs-build"}},
	}))
	_, ready, err = frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready)
	blocked, found = frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, ChoreographyBlockedTask{Task: "docs-test", Package: "docs", BlockedBy: "docs"}, blocked)
}

func TestInterpretedFrontierChoosesStableFailedPreparedBlocker(t *testing.T) {
	for _, names := range [][]string{{"zeta", "alpha"}, {"alpha", "zeta"}} {
		binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
		frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
			Binding: binding,
			Node:    "node-b",
			Tasks: []ChoreographyTaskSpec{
				{Task: "alpha:prepare", Package: "alpha", Kind: KindPrepare, EligibleNodes: []string{"node-a"}},
				{Task: "zeta:prepare", Package: "zeta", Kind: KindPrepare, EligibleNodes: []string{"node-a"}},
				{Task: "app-build", Package: "app", Kind: KindBuild, EligibleNodes: []string{"node-b"},
					Predecessors:      []string{"alpha:prepare", "zeta:prepare"},
					PreparedProviders: []string{"alpha", "zeta"}},
			},
		})
		require.NoError(t, err)
		require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
		events := make([]ChoreographyEvent, 0, 4)
		for _, name := range names {
			events = append(events,
				ChoreographyEvent{Sequence: uint64(len(events) + 1), Kind: ChoreographyClaimAccepted,
					Task: name + ":prepare", Package: name, Attempt: 1, Node: "node-a", TaskOID: "claim-" + name},
				ChoreographyEvent{Sequence: uint64(len(events) + 2), Kind: ChoreographyTerminal,
					Task: name + ":prepare", Package: name, Attempt: 1, Node: "node-a", TaskOID: "failed-" + name},
			)
		}
		require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
			Cursor: ChoreographyEventCursor{OID: "ledger-4", Sequence: 4}, Events: events,
		}))
		_, ready, err := frontier.SelectReady()
		require.NoError(t, err)
		require.False(t, ready)
		blocked, found := frontier.PeekBlocked()
		require.True(t, found)
		require.Equal(t, "alpha", blocked.BlockedBy)
	}
}

func TestInterpretedFrontierDoesNotDispatchAfterRunStops(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node-b",
		Tasks: []ChoreographyTaskSpec{
			{Task: "provider", EligibleNodes: []string{"node-a"}},
			{Task: "consumer", EligibleNodes: []string{"node-b"}, Predecessors: []string{"provider"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "provider", Attempt: 1,
				Node: "node-a", TaskOID: "claim-provider"},
			{Sequence: 2, Kind: ChoreographyRunStopping, Task: "__run__", TaskOID: "bootstrap"},
			{Sequence: 3, Kind: ChoreographyTerminal, Task: "provider", Attempt: 1,
				Node: "node-a", TaskOID: "cancelled-provider"},
		},
	}))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "cancelled work can discharge graph bookkeeping but never reopen claims")
}

func TestCompiledFrontierAcceptsCausalFailedInertCancellationAfterStop(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      binding,
		GraphRootOID: "complete-root",
		Node:         ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1},
		Tasks: []ChoreographyTaskSpec{{Task: "core:build", Package: "core", Kind: KindBuild,
			ReservationOID: "inert-task", Placement: PlacementWorker}},
	})
	require.NoError(t, err)
	policy := &testedOutcomePolicy{status: map[string]string{}}
	require.NoError(t, frontier.AttachOutcomePolicy(policy))
	events := []ChoreographyEvent{
		{Sequence: 1, Kind: ChoreographyInitializationComplete, Task: choreographyRunTaskID,
			TaskOID: "complete-root", GraphRootOID: "complete-root"},
		{Sequence: 2, Kind: ChoreographyExecutionStarted, Task: choreographyRunTaskID,
			TaskOID: "complete-root", GraphRootOID: "complete-root", AdmissionDeadlineUTC: "deadline"},
		{Sequence: 3, Kind: ChoreographyRunStopping, Task: choreographyRunTaskID,
			TaskOID: "complete-root", GraphRootOID: "complete-root"},
		{Sequence: 4, Kind: ChoreographyInertCancelled, Task: "core:build", Package: "core",
			Attempt: 1, GraphRootOID: "complete-root", PriorTaskOID: "inert-task",
			TaskOID: "cancelled-task", BlockedBy: choreographyInertCancelCause},
		{Sequence: 5, Kind: ChoreographyInertCancelSettled, Task: "core:build", Package: "core",
			Attempt: 1, GraphRootOID: "complete-root", PriorTaskOID: "cancelled-task",
			TaskOID: "cancelled-task", PackageOutcome: "failed"},
	}
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-five", Sequence: 5}, Events: events}))
	require.Equal(t, "failed", policy.status["core"])
}

func TestCompiledFrontierHoldsAcceptedResultEdgesUntilPackageSettlement(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      binding,
		GraphRootOID: "complete-root",
		Node:         ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1},
		Tasks: []ChoreographyTaskSpec{
			{Task: "core:build", Package: "core", Kind: KindBuild,
				ReservationOID: "inert-core", Placement: PlacementWorker},
			{Task: "app:build", Package: "app", Kind: KindBuild,
				ReservationOID: "inert-app", Placement: PlacementWorker,
				Predecessors: []string{"core:build"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-five", Sequence: 5},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyInitializationComplete, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root"},
			{Sequence: 2, Kind: ChoreographyExecutionStarted, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root", AdmissionDeadlineUTC: "deadline"},
			{Sequence: 3, Kind: ChoreographyClaimAccepted, Task: "core:build", Package: "core",
				Attempt: 1, Node: "peer", TaskOID: "claim-core", PriorTaskOID: "inert-core",
				AssignmentOID: "assignment-core", GraphRootOID: "complete-root"},
			{Sequence: 4, Kind: ChoreographyResultAccepted, Task: "core:build", Package: "core",
				Attempt: 1, Node: "peer", TaskOID: "receipt-core", OutputOID: "raw-output",
				ProposalID: "result-proposal", ProposalDigest: "result-digest"},
			{Sequence: 5, Kind: ChoreographyTerminal, Task: "core:build", Package: "core",
				Attempt: 1, Node: "peer", TaskOID: "terminal-core", ResultReceiptOID: "receipt-core",
				OutputOID: "raw-output", Succeeded: true, GraphRootOID: "complete-root"},
		},
	}))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready, "a terminal task's package still owns its outgoing graph edges")
	_, found := frontier.CompletedResultEvent("core:build")
	require.False(t, found, "the accepted Result is not visible as predecessor finality yet")
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-six", Sequence: 6},
		Events: []ChoreographyEvent{{Sequence: 6, Kind: ChoreographyTaskPackageSettled,
			Task: "core:build", Package: "core", Attempt: 1, TaskOID: "terminal-core",
			PriorTaskOID: "terminal-core", GraphRootOID: "complete-root"}},
	}))
	selected, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "app:build", selected.Spec.Task)
	terminal, found := frontier.CompletedResultEvent("core:build")
	require.True(t, found)
	require.Equal(t, "terminal-core", terminal.TaskOID)
	require.Equal(t, "raw-output", terminal.OutputOID,
		"package settlement must retain the earlier signed Result identity")
}

func TestCompiledFrontierReleasesFirstOnlyVersionAfterItsSettledTicket(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      binding,
		GraphRootOID: "complete-root",
		Node:         ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1},
		Tasks: []ChoreographyTaskSpec{
			{Task: "core:version", Package: "core", Kind: "version", ExecutorOwned: true,
				ReservationOID: "inert-version", Placement: PlacementOrchestrator},
			{Task: "core:build", Package: "core", Kind: KindBuild,
				ReservationOID: "inert-build", Placement: PlacementWorker,
				Predecessors: []string{"core:version"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-five", Sequence: 5},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyInitializationComplete, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root"},
			{Sequence: 2, Kind: ChoreographyExecutionStarted, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root", AdmissionDeadlineUTC: "deadline"},
			{Sequence: 3, Kind: ChoreographyClaimAccepted, Task: "core:version", Package: "core",
				Attempt: 1, Node: "first", TaskOID: "claim-version", PriorTaskOID: "inert-version",
				AssignmentOID: "assignment-version", GraphRootOID: "complete-root"},
			{Sequence: 4, Kind: ChoreographyResultAccepted, Task: "core:version", Package: "core",
				Attempt: 1, Node: "first", TaskOID: "receipt-version", OutputOID: "raw-result",
				ProposalID: "result-proposal", ProposalDigest: "result-digest"},
			{Sequence: 5, Kind: ChoreographyTerminal, Task: "core:version", Package: "core",
				Attempt: 1, Node: "first", TaskOID: "terminal-version", ResultReceiptOID: "receipt-version",
				OutputOID: "raw-result", Succeeded: true, GraphRootOID: "complete-root"},
		},
	}))
	selected, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready, "a first-only ticket finishes after the package hook barrier")
	require.Equal(t, "core:build", selected.Spec.Task)
	terminal, found := frontier.CompletedResultEvent("core:version")
	require.True(t, found)
	require.Equal(t, "raw-result", terminal.OutputOID)
}

func TestCompiledFrontierFailedPreparedProviderSettlesWithoutReleaseOutcome(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      binding,
		GraphRootOID: "complete-root",
		Node:         ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1},
		Tasks: []ChoreographyTaskSpec{
			{Task: "assets:prepare", Package: "assets", Kind: KindPrepare,
				ReservationOID: "inert-assets", Placement: PlacementWorker},
			{Task: "ui:build", Package: "ui", Kind: KindBuild,
				ReservationOID: "inert-ui", Placement: PlacementWorker,
				Predecessors: []string{"assets:prepare"}, PreparedProviders: []string{"assets"}},
			{Task: "solo:publish", Package: "solo", Kind: KindPublish,
				ReservationOID: "inert-solo", Placement: PlacementOrchestrator,
				ExecutorOwned: true, Predecessors: []string{"ui:build"}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, frontier.AttachOutcomePolicy(&testedOutcomePolicy{status: map[string]string{}}))
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-four", Sequence: 4},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyInitializationComplete, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root"},
			{Sequence: 2, Kind: ChoreographyExecutionStarted, Task: choreographyRunTaskID,
				TaskOID: "complete-root", GraphRootOID: "complete-root", AdmissionDeadlineUTC: "deadline"},
			{Sequence: 3, Kind: ChoreographyClaimAccepted, Task: "assets:prepare", Package: "assets",
				Attempt: 1, Node: "peer", TaskOID: "claim-assets", PriorTaskOID: "inert-assets",
				AssignmentOID: "assignment-assets", GraphRootOID: "complete-root"},
			{Sequence: 4, Kind: ChoreographyTerminal, Task: "assets:prepare", Package: "assets",
				Attempt: 1, Node: "peer", TaskOID: "terminal-assets",
				ResultReceiptOID: "ack-assets", Succeeded: false, GraphRootOID: "complete-root"},
		},
	}))
	require.Zero(t, frontier.tasks["ui:build"].remaining,
		"a prepared provider settles its edge at its signed terminal without a release package event")
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.False(t, ready)
	blocked, found := frontier.PeekBlocked()
	require.True(t, found)
	require.Equal(t, "assets", blocked.BlockedBy)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-five", Sequence: 5},
		Events: []ChoreographyEvent{{Sequence: 5, Kind: ChoreographyBlocked,
			Task: "ui:build", Package: "ui", Attempt: 1, TaskOID: "blocked-ui",
			PriorTaskOID: "inert-ui", BlockedBy: "assets", PackageOutcome: "failed",
			GraphRootOID: "complete-root"}},
	}))
	require.Zero(t, frontier.tasks["solo:publish"].remaining)
	require.Empty(t, frontier.tasks["solo:publish"].failedPrepared,
		"a publication-order edge does not turn an independent package into a prepared-input consumer")
}

func TestInterpretedVirtualSweepGatesApplySkipPolicyBeforeReleasingChildren(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node",
		Tasks: []ChoreographyTaskSpec{
			{Task: "middle:run", Package: "middle", Virtual: true},
			{Task: "leaf:run", Package: "leaf", Virtual: true, Predecessors: []string{"middle:run"}},
			{Task: "consumer:run", Package: "consumer", EligibleNodes: []string{"node"},
				Predecessors: []string{"leaf:run"}},
		},
	})
	require.NoError(t, err)
	policy := &testedSweepVirtualPolicy{
		status: map[string]string{}, blocked: map[string]string{"middle": "provider"},
	}
	require.NoError(t, frontier.AttachOutcomePolicy(policy))
	require.Equal(t, "skipped", policy.status["middle"])
	require.Equal(t, "skipped", policy.status["leaf"])
	require.Equal(t, "", policy.status["consumer"])
	chosen, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "consumer:run", chosen.Spec.Task)
}

func TestInterpretedVirtualPackageCannotStartWithoutOutcomePolicy(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node",
		Tasks: []ChoreographyTaskSpec{
			{Task: "virtual:run", Package: "virtual", Virtual: true},
			{Task: "child:run", Package: "child", EligibleNodes: []string{"node"},
				Predecessors: []string{"virtual:run"}},
		},
	})
	require.NoError(t, err)
	_, _, err = frontier.SelectReady()
	require.ErrorContains(t, err, "no outcome policy")
}

func TestInterpretedFailedFinalSweepResultRetainsPartialOutputs(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "gen", PlanDigest: "plan", GraphDigest: "graph"}
	frontier, err := NewChoreographyFrontier(ChoreographyFrontierOptions{
		Binding: binding,
		Node:    "node",
		Tasks: []ChoreographyTaskSpec{
			{Task: "provider:run", Package: "provider", EligibleNodes: []string{"node"}},
			{Task: "consumer:run", Package: "consumer", EligibleNodes: []string{"node"},
				Predecessors: []string{"provider:run"}},
		},
	})
	require.NoError(t, err)
	policy := &testedSweepVirtualPolicy{status: map[string]string{}, blocked: map[string]string{}}
	require.NoError(t, frontier.AttachOutcomePolicy(policy))
	_, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-2", Sequence: 2},
		Events: []ChoreographyEvent{
			{Sequence: 1, Kind: ChoreographyClaimAccepted, Task: "provider:run", Package: "provider",
				Attempt: 1, Node: "node", TaskOID: "claim"},
			{Sequence: 2, Kind: ChoreographyResultAccepted, Task: "provider:run", Package: "provider",
				Attempt: 1, Node: "node", TaskOID: "receipt", OutputOID: "failed-result",
				ProposalID: "proposal", ProposalDigest: "digest"},
		},
	}))
	_, found := frontier.CompletedResultEvent("provider:run")
	require.False(t, found, "a raw accepted result is not logical finality")
	require.NoError(t, frontier.ApplyEventPage(ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3},
		Events: []ChoreographyEvent{{Sequence: 3, Kind: ChoreographyTerminal, Task: "provider:run",
			Package: "provider", PackageOutcome: "failed", Attempt: 1, Node: "node",
			TaskOID: "terminal", OutputOID: "failed-result", Succeeded: false}},
	}))
	oid, found := frontier.CompletedOutput("provider:run")
	require.True(t, found)
	require.Equal(t, "failed-result", oid)
	terminal, found := frontier.CompletedResultEvent("provider:run")
	require.True(t, found)
	require.Equal(t, ChoreographyTerminal, terminal.Kind)
	require.Equal(t, "failed-result", terminal.OutputOID)
	require.Equal(t, "terminal", terminal.TaskOID)
	chosen, ready, err := frontier.SelectReady()
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "consumer:run", chosen.Spec.Task)
}
