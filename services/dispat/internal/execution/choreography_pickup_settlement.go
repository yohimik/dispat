// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	choreographyPickupSettlementNoIdentity       = ""
	choreographyPickupSettlementIdentityDomain   = "pickup-settled:"
	choreographyPickupSettlementInvalidError     = "execution: pickup settlement lacks an exact verified losing result"
	choreographyPickupSettlementUnavailableError = "execution: pickup settlement persistence is unavailable"
	choreographyPickupSettlementRecorded         = true
	choreographyPickupSignedClaimFound           = true
	choreographyPickupSignedReplyFound           = true
	choreographyPickupTerminalFound              = true
	choreographyPickupPriorSettlementFound       = true
)

type ChoreographyPickupSettlement struct {
	ChoreographyDecision
	PickupID  string `json:"pickupId"`
	ClaimOID  string `json:"claimOid"`
	TaskOID   string `json:"taskOid"`
	OutputOID string `json:"outputOid"`
	Budget    string `json:"budget"`
	Weight    int    `json:"weight"`
}

type ChoreographyPickupSettlementPersistable interface {
	PersistPickupSettlement(context.Context, ChoreographyPickupSettlement) (ChoreographyEventCursor, error)
}

func choreographyPickupSettlementDigest(settlement ChoreographyPickupSettlement) (string, error) {
	document, err := json.Marshal(settlement)
	if err != nil {
		return choreographyPickupSettlementNoIdentity, err
	}
	sum := sha256.Sum256(append([]byte(choreographyPickupSettlementIdentityDomain), document...))
	return hex.EncodeToString(sum[:]), nil
}

// settleLosingPickup releases only a verified physical pickup after another
// pickup has retained the logical task or won its canonical Terminal. The
// canonical task ref does not move, and no package edge is discharged.
func (b *FirstNodeBroker) settleLosingPickup(ctx context.Context,
	task *indexedChoreographyTask, pickup *indexedChoreographyPickup,
	d ChoreographyDecision) error {
	if pickup == nil || pickup.resultReceiptOID == choreographyPickupSettlementNoIdentity ||
		d.ChoreographyRunBinding != b.binding || d.Task != task.spec.Task ||
		d.Branch != task.spec.Branch || d.Attempt != pickup.attempt ||
		d.Node != pickup.node || d.ResultOID != pickup.resultReceiptOID ||
		d.PackageOutcome != choreographyPickupSettlementNoIdentity ||
		!task.terminal && task.claim == pickup.claimOID {
		return fmt.Errorf(choreographyPickupSettlementInvalidError)
	}
	persist, ok := b.persist.(ChoreographyPickupSettlementPersistable)
	if !ok {
		return fmt.Errorf(choreographyPickupSettlementUnavailableError)
	}
	settlement := ChoreographyPickupSettlement{ChoreographyDecision: d,
		PickupID: pickup.id, ClaimOID: pickup.claimOID, TaskOID: task.tip,
		OutputOID: pickup.resultOutputOID, Budget: task.spec.Budget,
		Weight: task.spec.Weight}
	digest, err := choreographyPickupSettlementDigest(settlement)
	if err != nil {
		return err
	}
	if pickup.settled {
		if pickup.settlementDigest == digest {
			return nil
		}
		return fmt.Errorf(choreographyPickupSettlementInvalidError)
	}
	if _, err := persist.PersistPickupSettlement(ctx, settlement); err != nil {
		return err
	}
	b.used[task.spec.Budget] -= task.spec.Weight
	b.nodeUsed[pickup.node] -= task.spec.Weight
	pickup.settled = choreographyPickupSettlementRecorded
	pickup.settlementDigest = digest
	if task.claim == pickup.claimOID {
		task.claim, task.cancel, task.result, task.owner =
			choreographyPickupSettlementNoIdentity, choreographyPickupSettlementNoIdentity,
			choreographyPickupSettlementNoIdentity, choreographyPickupSettlementNoIdentity
	}
	b.wakeBudgetWaiters()
	return nil
}

func (g *GitChoreographyPersist) PersistPickupSettlement(ctx context.Context,
	settlement ChoreographyPickupSettlement) (ChoreographyEventCursor, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyEventCursor{}, err
	}
	d := settlement.ChoreographyDecision
	spec := g.tasks[d.Task]
	if d.ChoreographyRunBinding != g.opts.Binding || spec.Task == choreographyPickupSettlementNoIdentity ||
		spec.Branch != d.Branch || settlement.PickupID == choreographyPickupSettlementNoIdentity ||
		settlement.ClaimOID == choreographyPickupSettlementNoIdentity ||
		settlement.ResultOID == choreographyPickupSettlementNoIdentity ||
		settlement.TaskOID == choreographyPickupSettlementNoIdentity ||
		settlement.Budget != spec.Budget || settlement.Weight != spec.Weight ||
		d.PackageOutcome != choreographyPickupSettlementNoIdentity {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyPickupSettlementInvalidError)
	}
	digest, err := choreographyPickupSettlementDigest(settlement)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	tip, err := g.opts.Mailbox.readRemoteTip(ctx, d.Branch)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	if tip != settlement.TaskOID {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyPickupSettlementInvalidError)
	}
	g.mu.Lock()
	err = g.syncLedgerLocked(ctx)
	if err != nil {
		g.mu.Unlock()
		return ChoreographyEventCursor{}, err
	}
	var signedClaim, signedResult, alreadySettled bool
	var settledCursor ChoreographyEventCursor
	activeClaim := choreographyPickupSettlementNoIdentity
	var logicalTerminal bool
	for _, index := range g.taskEvents[d.Task] {
		event := g.events[index]
		switch event.Kind {
		case ChoreographyClaimAccepted:
			activeClaim = event.TaskOID
			if event.PickupID == settlement.PickupID && event.TaskOID == settlement.ClaimOID &&
				event.Attempt == d.Attempt && event.Node == d.Node {
				signedClaim = choreographyPickupSignedClaimFound
			}
		case ChoreographyResultAccepted, ChoreographyAckAccepted:
			if event.PickupID == settlement.PickupID && event.ClaimOID == settlement.ClaimOID &&
				event.ResultReceiptOID == d.ResultOID && event.OutputOID == settlement.OutputOID &&
				event.Attempt == d.Attempt && event.Node == d.Node {
				signedResult = choreographyPickupSignedReplyFound
			}
		case ChoreographyPickupContactLost:
			if event.ClaimOID == activeClaim {
				activeClaim = choreographyPickupSettlementNoIdentity
			}
		case ChoreographyRetryReady, ChoreographyTerminal:
			if event.ClaimOID == activeClaim || event.PickupID == choreographyPickupSettlementNoIdentity {
				activeClaim = choreographyPickupSettlementNoIdentity
			}
			if event.Kind == ChoreographyTerminal {
				logicalTerminal = choreographyPickupTerminalFound
			}
		case ChoreographyPickupSettled:
			if event.PickupID == settlement.PickupID {
				alreadySettled = choreographyPickupPriorSettlementFound
				if event.ProposalDigest == digest && event.ClaimOID == settlement.ClaimOID &&
					event.ResultReceiptOID == d.ResultOID && event.OutputOID == settlement.OutputOID {
					settledCursor = ChoreographyEventCursor{OID: event.LedgerOID,
						Sequence: event.Sequence}
				}
			}
			if event.ClaimOID == activeClaim {
				activeClaim = choreographyPickupSettlementNoIdentity
			}
		}
	}
	head := ChoreographyEventCursor{OID: g.ledgerTip, Sequence: g.sequence}
	g.mu.Unlock()
	if settledCursor.OID != choreographyPickupSettlementNoIdentity {
		return settledCursor, nil
	}
	if !signedClaim || !signedResult || alreadySettled ||
		!logicalTerminal && activeClaim == settlement.ClaimOID {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyPickupSettlementInvalidError)
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: digest, ExpectedLedgerOID: head.OID,
		TaskBranch: d.Branch, OldTaskOID: tip, NewTaskOID: tip,
		Budget: spec.Budget, Node: d.Node, Delta: -spec.Weight,
		Event: ChoreographyEvent{Kind: ChoreographyPickupSettled,
			Task: d.Task, Attempt: d.Attempt, Node: d.Node,
			TaskOID: tip, PriorTaskOID: tip, PickupID: settlement.PickupID,
			ClaimOID: settlement.ClaimOID, ResultReceiptOID: d.ResultOID,
			OutputOID: settlement.OutputOID, Budget: spec.Budget,
			Weight: spec.Weight, Succeeded: d.Succeeded,
			ProposalDigest: digest, GraphRootOID: g.opts.GraphRootOID},
	}); err != nil {
		return ChoreographyEventCursor{}, err
	}
	return g.LedgerHead(ctx)
}
