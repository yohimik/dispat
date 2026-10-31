// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	choreographyPickupContactNoIdentity              = ""
	choreographyPickupContactNotDueError             = "execution: pickup contact deadline has not elapsed"
	choreographyPickupContactNoActiveClaimError      = "execution: contact loss names no outstanding exact pickup"
	choreographyPickupContactSignedClaimError        = "execution: contact loss differs from the signed cancellation reply deadline"
	choreographyPickupContactTaskTipError            = "execution: contact loss cannot fence the exact canonical task tip"
	choreographyPickupContactLedgerRootError         = "execution: contact loss has no signed ledger head"
	choreographyPickupContactPersistUnavailableError = "execution: pickup contact-loss persistence is unavailable"
	choreographyPickupContactAttemptsExhaustedError  = "execution: pickup contact was lost at the final permitted attempt; its lease remains unresolved"
	choreographyPickupContactRetryUnsafeError        = "execution: signed retry safety policy forbids contact-loss successor"
	choreographyPickupContactRecorded                = true
	choreographyPickupContactCompletionFound         = true
)

type ChoreographyPickupContactLoss struct {
	ChoreographyRunBinding
	Task                   string `json:"task"`
	Attempt                int    `json:"attempt"`
	Node                   string `json:"node"`
	Branch                 string `json:"branch"`
	TaskOID                string `json:"taskOid"`
	ClaimOID               string `json:"claimOid"`
	PickupID               string `json:"pickupId"`
	CancelOID              string `json:"cancelOid"`
	CancelReplyDeadlineUTC string `json:"cancelReplyDeadlineUtc"`
}

type ChoreographyPickupContactPersistable interface {
	PersistPickupContactLost(context.Context, ChoreographyPickupContactLoss) error
}

type choreographyPickupContactDeadlineRefusal struct {
	task     string
	pickupID string
	deadline time.Time
}

func (r *choreographyPickupContactDeadlineRefusal) Error() string {
	return choreographyPickupContactNotDueError
}

// MarkPickupContactLost opens one bounded successor without releasing the
// unresolved pickup's physical lease or run budget charge. A single missed
// poll cannot invoke it: the exact signed post-Cancel reply deadline must elapse.
func (b *FirstNodeBroker) MarkPickupContactLost(ctx context.Context,
	taskID, pickupID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || task.terminal || task.spec.ExecutorOwned ||
		pickupID == choreographyPickupContactNoIdentity {
		return fmt.Errorf(choreographyPickupContactNoActiveClaimError)
	}
	if !task.spec.RetryAfterUncertainAttempt {
		return fmt.Errorf(choreographyPickupContactRetryUnsafeError)
	}
	pickup := task.pickups[pickupID]
	if pickup == nil || pickup.settled {
		return fmt.Errorf(choreographyPickupContactNoActiveClaimError)
	}
	if pickup.contactLost {
		return nil
	}
	if task.claim != pickup.claimOID || task.attempt != pickup.attempt ||
		pickup.cancelOID == choreographyPickupContactNoIdentity ||
		pickup.cancelOID != task.cancel || pickup.resultReceiptOID != choreographyPickupContactNoIdentity ||
		pickup.cancelReplyDeadlineUTC == choreographyPickupContactNoIdentity {
		return fmt.Errorf(choreographyPickupContactNoActiveClaimError)
	}
	if task.attempt >= task.spec.EffectiveMaxAttempts() {
		return fmt.Errorf(choreographyPickupContactAttemptsExhaustedError)
	}
	deadline, err := time.Parse(time.RFC3339, pickup.cancelReplyDeadlineUTC)
	if err != nil {
		return fmt.Errorf(choreographyPickupContactSignedClaimError)
	}
	if time.Now().UTC().Before(deadline) {
		return &choreographyPickupContactDeadlineRefusal{
			task: taskID, pickupID: pickupID, deadline: deadline}
	}
	persist, ok := b.persist.(ChoreographyPickupContactPersistable)
	if !ok {
		return fmt.Errorf(choreographyPickupContactPersistUnavailableError)
	}
	if err := persist.PersistPickupContactLost(ctx, ChoreographyPickupContactLoss{
		ChoreographyRunBinding: b.binding, Task: taskID,
		Attempt: pickup.attempt, Node: pickup.node, Branch: task.spec.Branch,
		TaskOID: task.tip, ClaimOID: pickup.claimOID, PickupID: pickupID,
		CancelOID:              pickup.cancelOID,
		CancelReplyDeadlineUTC: pickup.cancelReplyDeadlineUTC,
	}); err != nil {
		return err
	}
	pickup.contactLost = choreographyPickupContactRecorded
	task.claim, task.cancel, task.result, task.owner =
		choreographyPickupContactNoIdentity, choreographyPickupContactNoIdentity,
		choreographyPickupContactNoIdentity, choreographyPickupContactNoIdentity
	task.attempt++
	b.wakeBudgetWaiters()
	return nil
}

func (g *GitChoreographyPersist) PersistPickupContactLost(ctx context.Context,
	loss ChoreographyPickupContactLoss) error {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	spec, knownTask := g.tasks[loss.Task]
	if loss.ChoreographyRunBinding != g.opts.Binding || loss.Task == choreographyPickupContactNoIdentity ||
		!knownTask ||
		loss.PickupID == choreographyPickupContactNoIdentity ||
		loss.Attempt >= spec.EffectiveMaxAttempts() ||
		loss.ClaimOID == choreographyPickupContactNoIdentity ||
		loss.CancelOID == choreographyPickupContactNoIdentity ||
		loss.CancelReplyDeadlineUTC == choreographyPickupContactNoIdentity ||
		spec.Branch != loss.Branch ||
		!spec.RetryAfterUncertainAttempt {
		return fmt.Errorf(choreographyPickupContactSignedClaimError)
	}
	g.mu.Lock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		g.mu.Unlock()
		return err
	}
	var signedClaim, signedCancel ChoreographyEvent
	var completionAccepted bool
	for _, index := range g.taskEvents[loss.Task] {
		event := g.events[index]
		if event.Kind == ChoreographyClaimAccepted && event.PickupID == loss.PickupID {
			signedClaim = event
		}
		if event.Kind == ChoreographyCancelIssued && event.PickupID == loss.PickupID &&
			event.ClaimOID == loss.ClaimOID {
			signedCancel = event
		}
		if event.Kind == ChoreographyAckAccepted && event.PickupID == loss.PickupID &&
			event.ClaimOID == loss.ClaimOID {
			completionAccepted = choreographyPickupContactCompletionFound
		}
		if event.Kind == ChoreographyResultAccepted && event.PickupID == loss.PickupID &&
			event.ClaimOID == loss.ClaimOID {
			completionAccepted = choreographyPickupContactCompletionFound
		}
	}
	g.mu.Unlock()
	if completionAccepted || signedClaim.TaskOID != loss.ClaimOID || signedClaim.Attempt != loss.Attempt ||
		signedClaim.Node != loss.Node || signedCancel.TaskOID != loss.CancelOID ||
		signedCancel.CancelReplyDeadlineUTC != loss.CancelReplyDeadlineUTC ||
		signedCancel.Attempt != loss.Attempt || signedCancel.Node != loss.Node {
		return fmt.Errorf(choreographyPickupContactSignedClaimError)
	}
	deadline, err := time.Parse(time.RFC3339, loss.CancelReplyDeadlineUTC)
	if err != nil {
		return fmt.Errorf(choreographyPickupContactNotDueError)
	}
	if time.Now().UTC().Before(deadline) {
		return &choreographyPickupContactDeadlineRefusal{
			task: loss.Task, pickupID: loss.PickupID, deadline: deadline}
	}
	tip, err := g.opts.Mailbox.readRemoteTip(ctx, loss.Branch)
	if err != nil {
		return err
	}
	if tip != loss.TaskOID {
		return fmt.Errorf(choreographyPickupContactTaskTipError)
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	if head.OID == choreographyPickupContactNoIdentity {
		return fmt.Errorf(choreographyPickupContactLedgerRootError)
	}
	document, err := json.Marshal(loss)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(document)
	return g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: hex.EncodeToString(digest[:]), ExpectedLedgerOID: head.OID,
		TaskBranch: loss.Branch, OldTaskOID: loss.TaskOID, NewTaskOID: loss.TaskOID,
		Event: ChoreographyEvent{Kind: ChoreographyPickupContactLost,
			Task: loss.Task, Attempt: loss.Attempt, Node: loss.Node,
			TaskOID: loss.TaskOID, PriorTaskOID: loss.TaskOID,
			ClaimOID: loss.ClaimOID, PickupID: loss.PickupID,
			CancelReplyDeadlineUTC: loss.CancelReplyDeadlineUTC,
			GraphRootOID:           g.opts.GraphRootOID},
	})
}
