// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newFinalityWakeTask(t *testing.T, fixture *compiledRefreshFixture, taskID string) (*claimedTask, ChoreographyEvent) {
	t.Helper()
	binding := fixture.run.binding
	frontierTask := fixture.run.frontier.tasks[taskID]
	intent := NodeSlotIntent{Run: binding.Run, Task: taskID, Attempt: 1, Weight: 1,
		PickupID: NewChoreographyPickupID(), Recovery: &NodeSlotRecoveryLocator{
			Binding: binding, Node: fixture.worker.Node, CanonicalEndpoint: "file:///canonical",
			BootstrapOID: fixture.run.bootstrapOID, TaskBranch: frontierTask.spec.Branch, Kind: KindBuild,
			ExpectedTaskOID: frontierTask.tipOID, ExpectedLedgerOID: fixture.run.frontier.Cursor().OID,
			GraphRootOID: fixture.run.rootOID, TemplateDigest: frontierTask.spec.TemplateDigest,
			ProposalDigest: strings.Repeat("d", 64)}}
	lease, available, err := fixture.worker.NodeSlots.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, available)
	task := &claimedTask{slotLease: lease, claimed: strings.Repeat("a", 40),
		tip: ChainTip{Branch: frontierTask.spec.Branch, OID: frontierTask.tipOID},
		assignment: Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
			Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
			Task: taskID, Attempt: intent.Attempt, Node: fixture.worker.Node},
			GraphDigest: binding.GraphDigest, GraphRootOID: fixture.run.rootOID}}
	t.Cleanup(fixture.worker.registerClaim(task))
	frontierTask.state, frontierTask.currentPickupID = frontierBusy, intent.PickupID
	frontierTask.pickups = map[string]*choreographyFrontierPickup{intent.PickupID: {
		claimOID: task.claimed, node: fixture.worker.Node, attempt: intent.Attempt,
		resultReceiptOID: strings.Repeat("b", 40)}}
	return task, ChoreographyEvent{Kind: ChoreographyTerminal, Task: taskID,
		Attempt: intent.Attempt, Node: fixture.worker.Node, PickupID: intent.PickupID,
		ClaimOID: task.claimed, GraphRootOID: fixture.run.rootOID,
		PriorTaskOID: frontierTask.tipOID, TaskOID: strings.Repeat("9", 40),
		ResultReceiptOID: strings.Repeat("b", 40)}
}

func requireNoFinalityWake(t *testing.T, task *claimedTask) {
	t.Helper()
	select {
	case <-task.finalityNotifications():
		t.Fatal("unrelated or invalid evidence woke this task")
	default:
	}
}

func TestWorkerFinalityHintRoutesOnlyExactRegisteredPickupAndCoalesces(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	first, event := newFinalityWakeTask(t, fixture, "api:build")
	second, _ := newFinalityWakeTask(t, fixture, "cli:build")
	for _, change := range []func(*ChoreographyRunBinding, *ChoreographyEvent){
		func(b *ChoreographyRunBinding, _ *ChoreographyEvent) { b.Run = "another-run" },
		func(b *ChoreographyRunBinding, _ *ChoreographyEvent) { b.Generation = "another-generation" },
		func(b *ChoreographyRunBinding, _ *ChoreographyEvent) { b.PlanDigest = "another-plan" },
		func(b *ChoreographyRunBinding, _ *ChoreographyEvent) { b.GraphDigest = "another-graph" },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.GraphRootOID = "another-root" },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.Task = "cli:build" },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.Node = "another-node" },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.Attempt++ },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.PickupID = NewChoreographyPickupID() },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.ClaimOID = "another-claim" },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.Kind = ChoreographyResultAccepted },
		func(_ *ChoreographyRunBinding, e *ChoreographyEvent) { e.Kind = ChoreographyAckAccepted },
	} {
		binding, wrong := fixture.run.binding, event
		change(&binding, &wrong)
		fixture.worker.notifyClaimedFinality(binding, []ChoreographyEvent{wrong})
		requireNoFinalityWake(t, first)
		requireNoFinalityWake(t, second)
	}
	for _, kind := range []ChoreographyEventKind{ChoreographyTerminal, ChoreographyRetryReady, ChoreographyPickupSettled} {
		event.Kind = kind
		fixture.worker.notifyClaimedFinality(fixture.run.binding, []ChoreographyEvent{event, event})
	}
	select {
	case <-first.finalityNotifications():
	default:
		t.Fatal("exact finality did not queue a hint before the waiter started")
	}
	requireNoFinalityWake(t, first)
	requireNoFinalityWake(t, second)
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 2, "a hint alone cannot release either lease")
}

func TestWorkerFinalityHintRequiresCompleteValidAppliedPage(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	task, event := newFinalityWakeTask(t, fixture, "api:build")
	event.Sequence = 2
	_, err := fixture.worker.applyCompiledEventPage(fixture.run, ChoreographyEventPage{
		Cursor: ChoreographyEventCursor{OID: "ledger-3", Sequence: 3}, Events: []ChoreographyEvent{
			event,
			{Sequence: 3, Kind: ChoreographyTerminal, Task: "cli:build"},
		}})
	require.Error(t, err)
	requireNoFinalityWake(t, task)
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{task.slotLease}, held)
}

func TestWorkerFinalityQueuedHintRereadsExactDecisionBeforeFallback(t *testing.T) {
	for _, evidence := range []string{"valid", "wrong receipt", "invalid page", "wrong pickup"} {
		t.Run(evidence, func(t *testing.T) {
			fixture := newCompiledRefreshFixture(t)
			task, event := newFinalityWakeTask(t, fixture, "api:build")
			receipt := ChoreographyReceipt{Accepted: true, OID: event.ResultReceiptOID,
				Cursor: fixture.run.frontier.Cursor()}
			page := fixture.page(event)
			_, err := fixture.worker.applyCompiledEventPage(fixture.run, page)
			require.NoError(t, err)
			switch evidence {
			case "wrong receipt":
				page.Events[0].ResultReceiptOID = "another-receipt"
			case "invalid page":
				page.Cursor.Sequence++
			case "wrong pickup":
				page.Events[0].PickupID = NewChoreographyPickupID()
			}
			polls := 0
			fixture.client.poll = func(request ChoreographyEventRequest) (ChoreographyEventPage, error) {
				polls++
				if polls == 1 {
					return ChoreographyEventPage{Cursor: request.Cursor}, nil
				}
				return page, nil
			}
			// A missing wake cannot reach the second read before the unchanged
			// 250ms fallback. This deadline only bounds the regression test.
			ctx, cancel := context.WithTimeout(t.Context(), 125*time.Millisecond)
			defer cancel()
			err = fixture.worker.awaitNodeSlotDecisionUntil(ctx, workerFinalityWaitRequest{
				Task: task, Channel: fixture.run.channel, Receipt: receipt})
			require.Equal(t, 2, polls)
			held, readErr := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, readErr)
			if evidence == "valid" {
				require.NoError(t, err)
				require.Empty(t, held)
			} else {
				require.Error(t, err)
				require.Equal(t, []NodeSlotLease{task.slotLease}, held)
			}
		})
	}
}

func TestWorkerFinalityHintCancellationRetainsLeaseForFreshRecovery(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	task, event := newFinalityWakeTask(t, fixture, "api:build")
	receipt := ChoreographyReceipt{Accepted: true, OID: event.ResultReceiptOID,
		Cursor: fixture.run.frontier.Cursor()}
	fixture.worker.notifyClaimedFinality(fixture.run.binding, []ChoreographyEvent{event})
	fixture.client.pollWithContext = func(ctx context.Context, _ ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return ChoreographyEventPage{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := fixture.worker.awaitNodeSlotDecisionUntil(ctx, workerFinalityWaitRequest{
		Task: task, Channel: fixture.run.channel, Receipt: receipt})
	require.ErrorIs(t, err, context.Canceled)
	held, err := fixture.worker.NodeSlots.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{task.slotLease}, held)
	reopened, err := OpenNodeSlotAuthority(filepath.Dir(fixture.worker.NodeSlots.dir), fixture.worker.Node, 2)
	require.NoError(t, err)
	fixture.worker.NodeSlots = reopened
	held, err = reopened.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{task.slotLease}, held)
	recovered := &claimedTask{assignment: task.assignment, tip: task.tip, claimed: task.claimed, slotLease: held[0]}
	fixture.client.pollWithContext = nil
	fixture.client.poll = func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return fixture.page(event), nil
	}
	require.NoError(t, fixture.worker.awaitNodeSlotDecision(t.Context(), recovered, fixture.run.channel, receipt))
	held, err = reopened.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}

func TestWorkerFinalityHintConcurrentRegistrationAndWaiterInitialization(t *testing.T) {
	fixture := newCompiledRefreshFixture(t)
	task, event := newFinalityWakeTask(t, fixture, "api:build")
	var work sync.WaitGroup
	for range 16 {
		work.Go(func() {
			task.finalityNotifications()
			forget := fixture.worker.registerClaim(task)
			fixture.worker.notifyClaimedFinality(fixture.run.binding, []ChoreographyEvent{event})
			forget()
		})
	}
	work.Wait()
	task.finalityNotifications()
	require.LessOrEqual(t, len(task.finalityWake), 1)
}
