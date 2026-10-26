// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyPickupAckDecisionInvalidError = "execution: pickup Ack decision lacks its exact signed claim"
	choreographyPickupAckDecisionMissingOID   = ""
	choreographyPickupAckDecisionUnhandled    = false
	choreographyPickupAckDecisionHandled      = true
)

type choreographyPickupAckDecisionOptions struct {
	Decision ChoreographyDecision
	Document []byte
	Identity string
	Spec     ChoreographyTaskSpec
}

func (g *GitChoreographyPersist) persistPickupAckDecision(ctx context.Context,
	options choreographyPickupAckDecisionOptions) (string, bool, error) {
	d, document, identity, spec := options.Decision, options.Document, options.Identity, options.Spec
	m := g.opts.Mailbox
	ackBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: d.ResultOID, Kind: MessageAck, MaxBytes: g.opts.MaxPackBytes})
	if err != nil {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
	}
	var ack Ack
	if strictJSON(ackBytes, &ack) != nil || ack.Task != d.Task ||
		ack.Attempt != d.Attempt || ack.Node != d.Node ||
		ack.Branch != d.Branch || ack.Cancel == choreographyPickupAckDecisionMissingOID {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, fmt.Errorf(choreographyPickupAckDecisionInvalidError)
	}
	cancelBytes, err := readChoreographyFinalityDocument(ctx, choreographyFinalityDocumentQuery{
		Mailbox: m, OID: ack.Cancel, Kind: MessageCancel, MaxBytes: g.opts.MaxPackBytes})
	if err != nil {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
	}
	var cancel Withdrawal
	if strictJSON(cancelBytes, &cancel) != nil || cancel.Tip == choreographyPickupAckDecisionMissingOID ||
		cancel.Task != d.Task || cancel.Attempt != d.Attempt ||
		cancel.Node != d.Node || cancel.Branch != d.Branch {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, fmt.Errorf(choreographyPickupAckDecisionInvalidError)
	}
	pickupID, err := m.readChoreographyClaimPickupID(ctx, cancel.Tip)
	if err != nil || pickupID == choreographyPickupAckDecisionMissingOID {
		if err != nil {
			return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
		}
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, nil
	}
	tip, err := m.readRemoteTip(ctx, d.Branch)
	if err != nil {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
	}
	if tip != d.ResultOID {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, fmt.Errorf(choreographyPickupAckDecisionInvalidError)
	}
	oid := g.pendingTaskOID(identity)
	if oid == choreographyPickupAckDecisionMissingOID {
		oid, err = m.commitMessage(ctx, choreographyDecisionKind, document,
			[]string{d.ResultOID}, nil)
		if err != nil {
			return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
		}
	}
	kind := ChoreographyTerminal
	if d.Retry {
		kind = ChoreographyRetryReady
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, TaskBranch: d.Branch,
		OldTaskOID: d.ResultOID, NewTaskOID: oid,
		Budget: spec.Budget, Node: d.Node, Delta: -spec.Weight,
		Event: ChoreographyEvent{Kind: kind, Task: d.Task, Attempt: d.Attempt,
			Node: d.Node, TaskOID: oid, PriorTaskOID: d.ResultOID,
			ResultReceiptOID: d.ResultOID, PickupID: pickupID,
			ClaimOID: cancel.Tip, Budget: spec.Budget, Weight: spec.Weight,
			GraphRootOID: g.opts.GraphRootOID,
			Succeeded:    d.Succeeded, Package: spec.Package,
			PackageOutcome: d.PackageOutcome},
	}); err != nil {
		return choreographyPickupAckDecisionMissingOID, choreographyPickupAckDecisionUnhandled, err
	}
	return oid, choreographyPickupAckDecisionHandled, nil
}
