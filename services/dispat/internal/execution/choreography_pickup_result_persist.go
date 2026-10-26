// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyPickupResultNoIdentity         = ""
	choreographyPickupResultTaskTipError       = "execution: pickup result has no canonical task ref to fence"
	choreographyPickupResultReceiptEncodeError = "execution: pickup result proposal cannot be encoded: %w"
)

// persistPickupResult records one physical pickup without assigning the
// logical task's canonical ref to that pickup. A later older worker can thus
// return after a successor Claim, and a losing Result cannot replace the
// winning finished branch. The ledger carries the exact signed receipt as a
// secondary parent while a no-op task CAS fences the observed task tip.
func (g *GitChoreographyPersist) persistPickupResult(ctx context.Context,
	p ChoreographyResultProposal, digest string, outputs []gitx.TreeEntry) (ChoreographyReceipt, error) {
	m := g.opts.Mailbox
	taskTip, err := m.readRemoteTip(ctx, p.Branch)
	if err != nil {
		return ChoreographyReceipt{}, err
	}
	if taskTip == choreographyPickupResultNoIdentity {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyPickupResultTaskTipError)
	}
	g.mu.Lock()
	err = g.syncLedgerLocked(ctx)
	if err != nil {
		g.mu.Unlock()
		return ChoreographyReceipt{}, err
	}
	for _, index := range g.taskEvents[p.Task] {
		event := g.events[index]
		if event.Kind == ChoreographyCancelIssued && event.PriorTaskOID == p.ClaimOID {
			g.mu.Unlock()
			return ChoreographyReceipt{}, fmt.Errorf(choreographyBrokerResultDoesNotAnswerTheActiveClaimError)
		}
	}
	g.mu.Unlock()
	document, err := json.Marshal(p)
	if err != nil {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyPickupResultReceiptEncodeError, err)
	}
	receiptOID := g.pendingResultReceiptOID(digest)
	if receiptOID == choreographyPickupResultNoIdentity {
		receiptOID, err = m.commitMessage(ctx, choreographyResultKind, document,
			[]string{p.ClaimOID, p.ResultDigest}, outputs)
		if err != nil {
			return ChoreographyReceipt{}, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, TaskBranch: p.Branch,
		OldTaskOID: taskTip, NewTaskOID: taskTip,
		Event: ChoreographyEvent{Kind: ChoreographyResultAccepted,
			Task: p.Task, Attempt: p.Attempt, Node: p.Node,
			TaskOID: taskTip, PriorTaskOID: taskTip,
			ResultReceiptOID: receiptOID, ClaimOID: p.ClaimOID,
			PickupID: p.PickupID, ProposalDigest: digest,
			ProposalID: p.ProposalID, OutputOID: p.ResultDigest,
			GraphRootOID: g.opts.GraphRootOID},
	}); err != nil {
		return ChoreographyReceipt{}, err
	}
	return ChoreographyReceipt{ChoreographyRunBinding: p.ChoreographyRunBinding,
		Kind: choreographyResultKind, Task: p.Task, Attempt: p.Attempt,
		Node: p.Node, Branch: p.Branch, ProposalID: p.ProposalID,
		ProposalDigest: digest, OID: receiptOID,
		GraphRootOID: g.opts.GraphRootOID, PickupID: p.PickupID,
		Accepted: choreographyGitReceiptAccepted, Cursor: g.acceptedCursor(digest)}, nil
}
