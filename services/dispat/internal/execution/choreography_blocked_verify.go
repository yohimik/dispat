// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

// verifyBlockedEvent binds the ledger event to the signed task terminal. A
// first-node pre-claim failure is valid only over the original inert root;
// an offered or claimed task cannot be relabeled as a local failure.
func (g *GitChoreographyPersist) verifyBlockedEvent(ctx context.Context, event ChoreographyEvent) error {
	m := g.opts.Mailbox
	tree, err := m.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyBlockedKind {
		return fmt.Errorf("execution: blocked event names another task kind")
	}
	data, err := m.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var blocked ChoreographyBlockedOutcome
	if strictJSON(data, &blocked) != nil || blocked.ChoreographyRunBinding != g.opts.Binding ||
		blocked.Task != event.Task || blocked.Attempt != event.Attempt ||
		blocked.ExpectedOID != event.PriorTaskOID || blocked.Package != event.Package ||
		blocked.BlockedBy != event.BlockedBy || blocked.PackageOutcome != event.PackageOutcome {
		return fmt.Errorf("execution: blocked task differs from signed ledger event")
	}
	if blocked.LocalFailure == nil {
		if blocked.PackageOutcome == "failed" && blocked.BlockedBy == blocked.Package {
			return fmt.Errorf("execution: failed local block has no signed evidence")
		}
		return nil
	}
	if blocked.PackageOutcome != "failed" &&
		(blocked.PackageOutcome != "" || g.packageState[blocked.Package] != "failed") ||
		blocked.BlockedBy != blocked.Package ||
		!validChoreographyLocalFailure(blocked.LocalFailure, event.PriorTaskOID) {
		return fmt.Errorf("execution: invalid signed local failure evidence")
	}
	root, err := m.readTree(ctx, event.PriorTaskOID)
	if err != nil {
		return err
	}
	if root.kind != MessageReservation {
		return fmt.Errorf("execution: local failure did not fence an inert reservation")
	}
	rootData, err := m.Read(ctx, ChainTip{OID: event.PriorTaskOID, Kind: root.kind,
		document: root.document, signature: root.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var reservation Reservation
	if strictJSON(rootData, &reservation) != nil ||
		validateChoreographyReservation(&reservation) != nil ||
		reservation.Run != g.opts.Binding.Run ||
		reservation.Generation != g.opts.Binding.Generation ||
		reservation.PlanDigest != g.opts.Binding.PlanDigest ||
		reservation.Task != event.Task || reservation.Attempt != event.Attempt {
		return fmt.Errorf("execution: local failure names another reservation")
	}
	return nil
}
