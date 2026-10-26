// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	choreographyPickupSideAckInvalidError = "execution: late pickup Ack lacks its exact signed cancellation"
	choreographyPickupSideAckMissingOID   = ""
	choreographyPickupSideAckSignedCancel = true
)

// persistPickupSideAck retains a late Ack after a successor has moved the
// canonical task ref. The Ack commit remains a child of its own Cancel;
// signed ledger admission and an unchanged task-ref CAS bind it to this run.
func (g *GitChoreographyPersist) persistPickupSideAck(ctx context.Context,
	p ChoreographyAckProposal, digest, taskTip string) (ChoreographyReceipt, error) {
	m := g.opts.Mailbox
	cancelBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: p.CancelOID, Kind: MessageCancel,
		MaxBytes: g.opts.MaxPackBytes})
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	var withdrawal Withdrawal
	if strictJSON(cancelBytes, &withdrawal) != nil ||
		withdrawal.Assignment != p.AssignmentOID || withdrawal.Run != p.Run ||
		withdrawal.Generation != p.Generation || withdrawal.PlanDigest != p.PlanDigest ||
		withdrawal.Task != p.Task || withdrawal.Attempt != p.Attempt ||
		withdrawal.Node != p.Node || withdrawal.Branch != p.Branch ||
		withdrawal.Tip == choreographyPickupSideAckMissingOID || withdrawal.CancelReplyDeadlineUTC == choreographyPickupSideAckMissingOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyPickupSideAckInvalidError)
	}
	pickupID, err := m.readChoreographyClaimPickupID(ctx, withdrawal.Tip)
	if err != nil || pickupID == choreographyPickupSideAckMissingOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyPickupSideAckInvalidError)
	}
	g.mu.Lock()
	err = g.syncLedgerLocked(ctx)
	var signedCancel bool
	if err == nil {
		for _, index := range g.taskEvents[p.Task] {
			event := g.events[index]
			if event.Kind == ChoreographyCancelIssued && event.TaskOID == p.CancelOID &&
				event.PriorTaskOID == withdrawal.Tip && event.PickupID == pickupID &&
				event.CancelReplyDeadlineUTC == withdrawal.CancelReplyDeadlineUTC {
				signedCancel = choreographyPickupSideAckSignedCancel
				break
			}
		}
	}
	g.mu.Unlock()
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if !signedCancel || taskTip == choreographyPickupSideAckMissingOID {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyPickupSideAckInvalidError)
	}
	ack := Ack{Header: withdrawal.Header, ProposalID: p.ProposalID,
		ProposalDigest: digest, Assignment: p.AssignmentOID,
		Cancel: p.CancelOID, Phase: p.Phase, CommandStarted: p.CommandStarted}
	ack.IssuedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(ack)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	ackOID := g.pendingResultReceiptOID(digest)
	if ackOID == choreographyPickupSideAckMissingOID {
		ackOID, err = m.commitMessage(ctx, MessageAck, data, []string{p.CancelOID}, nil)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, TaskBranch: p.Branch,
		OldTaskOID: taskTip, NewTaskOID: taskTip,
		Event: ChoreographyEvent{Kind: ChoreographyAckAccepted,
			Task: p.Task, Attempt: p.Attempt, Node: p.Node,
			TaskOID: taskTip, PriorTaskOID: taskTip,
			ClaimOID: withdrawal.Tip, PickupID: pickupID,
			ResultReceiptOID: ackOID, ProposalDigest: digest,
			ProposalID: p.ProposalID, GraphRootOID: g.opts.GraphRootOID},
	}); err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: MessageAck, Task: p.Task, Attempt: p.Attempt, Node: p.Node,
		Branch: p.Branch, ProposalID: p.ProposalID, ProposalDigest: digest,
		OID: ackOID, PickupID: pickupID, GraphRootOID: g.opts.GraphRootOID,
		Accepted: choreographyGitReceiptAccepted, Cursor: g.acceptedCursor(digest)}, nil
}
