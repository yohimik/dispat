// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const workerRetainedCancelIdentityError = "execution: retained cancellation differs from the worker's exact pickup"

// A successor can move the canonical task ref before a disconnected worker
// sees its Cancel. The signed ledger still carries that exact withdrawal.
func (w *Worker) readRetainedPickupCancellation(ctx context.Context, task *claimedTask) (ChainTip, error) {
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok || task.slotLease.Intent.PickupID == workerRuntimeNoIdentity ||
		task.slotLease.Intent.Recovery == nil {
		return ChainTip{}, nil
	}
	locator := task.slotLease.Intent.Recovery
	if !validNodeSlotRecovery(task.slotLease.Intent) || locator.Node != w.Node ||
		locator.TaskBranch != task.tip.Branch || locator.BootstrapOID != task.assignment.BootstrapOID {
		return ChainTip{}, fmt.Errorf(workerRetainedCancelIdentityError)
	}
	feed, err := w.openRetainedNodeSlotFeed(ctx, mailbox, nodeSlotRunKey{
		Binding: locator.Binding, BootstrapOID: locator.BootstrapOID,
		Endpoint: locator.CanonicalEndpoint})
	if err != nil {
		return ChainTip{}, err
	}
	sequence, found, err := feed.SequenceForOID(ctx, locator.ExpectedLedgerOID)
	if err != nil {
		return ChainTip{}, err
	}
	if !found {
		return ChainTip{}, fmt.Errorf(workerRetainedCancelIdentityError)
	}
	head, err := feed.Head(ctx)
	if err != nil {
		return ChainTip{}, err
	}
	events, err := feed.reader.TaskEventsThrough(ctx, task.assignment.Task,
		ChoreographyEventCursor{OID: locator.ExpectedLedgerOID, Sequence: sequence}, head)
	if err != nil {
		return ChainTip{}, err
	}
	for _, event := range events {
		if event.Kind != ChoreographyCancelIssued || event.Task != task.assignment.Task ||
			event.Attempt != task.assignment.Attempt || event.Node != w.Node ||
			event.PickupID != task.slotLease.Intent.PickupID || event.ClaimOID != task.claimed {
			continue
		}
		withdrawal, err := mailbox.readChainTip(ctx, task.tip.Branch, event.TaskOID)
		if err != nil {
			return ChainTip{}, err
		}
		if withdrawal.Kind != MessageCancel || withdrawal.PreviousOID != task.claimed ||
			w.checkWithdrawal(ctx, withdrawal, task.tip, task.claimed, task.assignment) != workerRuntimeNoIdentity {
			return ChainTip{}, fmt.Errorf(workerRetainedCancelIdentityError)
		}
		return withdrawal, nil
	}
	return ChainTip{}, nil
}
