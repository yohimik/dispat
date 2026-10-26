package app

import (
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestRetainedSweepReplyUsesExactCanonicalAckReceipt(t *testing.T) {
	claim := execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		TaskOID: "claim", PickupID: "pickup", Attempt: 1, Node: "peer"}
	cancel := execution.ChoreographyEvent{Kind: execution.ChoreographyCancelIssued,
		TaskOID: "cancel", PriorTaskOID: "claim", PickupID: "pickup"}
	ack := execution.ChoreographyEvent{Kind: execution.ChoreographyAckAccepted,
		TaskOID: "ack", PriorTaskOID: "cancel", PickupID: "pickup", Attempt: 1, Node: "peer"}
	if got := retainedSweepReplyReceiptOID(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}); got != "ack" {
		t.Fatalf("canonical compiled Ack receipt = %q, want exact Ack tip", got)
	}
	if got := retainedSweepReplyPackageOutcome(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}, false); got != "failed" {
		t.Fatalf("canonical Ack package outcome = %q, want failed", got)
	}
	if got := retainedSweepReplyPackageOutcome(retainedSweepAttempt{
		claim: claim, cancel: cancel, reply: ack, contactLost: true}, false); got != "" {
		t.Fatalf("Ack after signed contact loss changed successor package outcome to %q", got)
	}
	ack.PriorTaskOID = "foreign-cancel"
	if got := retainedSweepReplyReceiptOID(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}); got != "" {
		t.Fatalf("Ack following another Cancel supplied receipt %q", got)
	}
	ack.PriorTaskOID, ack.PickupID = "cancel", "foreign-pickup"
	if got := retainedSweepReplyReceiptOID(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}); got != "" {
		t.Fatalf("Ack for another pickup supplied receipt %q", got)
	}
	ack.TaskOID, ack.PriorTaskOID, ack.PickupID = "claim", "claim", "pickup"
	ack.ResultReceiptOID = "side-ack"
	if got := retainedSweepReplyReceiptOID(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}); got != "side-ack" {
		t.Fatalf("late side Ack receipt = %q, want carried receipt", got)
	}
	if got := retainedSweepReplyPackageOutcome(retainedSweepAttempt{claim: claim, cancel: cancel, reply: ack}, false); got != "" {
		t.Fatalf("late side Ack changed successor package outcome to %q", got)
	}
}

func TestRetainedSweepReplayKeepsPickupsSeparateAfterWinnerAndSideAck(t *testing.T) {
	oldKey := retainedSweepAttemptKey{task: "core:run", pickupID: "old"}
	newKey := retainedSweepAttemptKey{task: "core:run", pickupID: "new"}
	replay := &retainedSweepAttemptReplay{attempts: make(map[retainedSweepAttemptKey]retainedSweepAttempt)}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyCancelIssued,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-cancel", PriorTaskOID: "old-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyPickupContactLost,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-cancel"})
	if !replay.attempts[oldKey].contactLost {
		t.Fatal("cold replay omitted signed old-pickup contact loss")
	}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		Task: newKey.task, PickupID: newKey.pickupID, TaskOID: "new-claim"})
	if got := replay.attempts[oldKey].claim.TaskOID; got != "old-claim" {
		t.Fatalf("successor claim erased old unsettled pickup: %q", got)
	}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyAckAccepted,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "new-claim",
		ResultReceiptOID: "old-side-ack"})
	if got := replay.attempts[newKey].claim.TaskOID; got != "new-claim" {
		t.Fatalf("old side Ack changed successor pickup: %q", got)
	}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyPickupSettled,
		Task: oldKey.task, PickupID: oldKey.pickupID, ResultReceiptOID: "old-side-ack"})
	if _, found := replay.attempts[oldKey]; found {
		t.Fatal("settled old pickup remained active")
	}
	if _, found := replay.attempts[newKey]; !found {
		t.Fatal("settling old pickup erased the current pickup")
	}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyTerminal,
		Task: newKey.task, PickupID: newKey.pickupID, TaskOID: "winner-terminal"})
	if len(replay.attempts) != 0 {
		t.Fatalf("terminal winner left %d active pickups", len(replay.attempts))
	}
	// A signed side Ack can also arrive after the winner's terminal event.
	replay = &retainedSweepAttemptReplay{attempts: make(map[retainedSweepAttemptKey]retainedSweepAttempt)}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyCancelIssued,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-cancel", PriorTaskOID: "old-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		Task: newKey.task, PickupID: newKey.pickupID, TaskOID: "new-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyTerminal,
		Task: newKey.task, PickupID: newKey.pickupID, TaskOID: "winner-terminal"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyAckAccepted,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "winner-terminal",
		ResultReceiptOID: "old-side-ack"})
	if len(replay.attempts) != 1 || replay.attempts[oldKey].claim.TaskOID != "old-claim" {
		t.Fatal("late old side Ack resurrected the winner or lost its own claim")
	}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyPickupSettled,
		Task: oldKey.task, PickupID: oldKey.pickupID, ResultReceiptOID: "old-side-ack"})
	if len(replay.attempts) != 0 {
		t.Fatal("late side Ack remained after exact pickup settlement")
	}
	// Blocking an unclaimed logical task cannot release an earlier pickup
	// whose contact was lost; that old worker may still owe a side Ack.
	replay = &retainedSweepAttemptReplay{attempts: make(map[retainedSweepAttemptKey]retainedSweepAttempt)}
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyClaimAccepted,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-claim"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyCancelIssued,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-cancel"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyPickupContactLost,
		Task: oldKey.task, PickupID: oldKey.pickupID, TaskOID: "old-cancel"})
	replay.applyEvent(execution.ChoreographyEvent{Kind: execution.ChoreographyBlocked,
		Task: oldKey.task, TaskOID: "blocked-terminal"})
	if got := replay.attempts[oldKey].claim.TaskOID; got != "old-claim" {
		t.Fatalf("Blocked discarded unsettled old pickup: %q", got)
	}
}
