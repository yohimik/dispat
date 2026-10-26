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
	choreographyRunTaskID                 = "__run__"
	choreographyInitIncompleteError       = "execution: complete graph root cannot settle against the initial ledger"
	choreographyInitCASUnknownErrorFormat = "execution: initialization-complete ledger CAS has unresolved outcome: %w"
	choreographyInitMissingIdentity       = ""
	choreographyInitCompleteSequence      = 1
)

// CompleteChoreographyInitialization is the final admission gate. It binds
// the already settled graph manifest to the one canonical ledger before any
// participation notice is activated or broker can accept a claim.
func (c *Coordinator) CompleteChoreographyInitialization(ctx context.Context,
	ledger ChoreographyLedgerReservation, graphRootOID string) (string, error) {
	if c == nil || c.canonicalMailbox == nil || graphRootOID == choreographyInitMissingIdentity ||
		ledger.Branch != FormatChoreographyLedgerBranch(c.Run) || ledger.OID == choreographyInitMissingIdentity ||
		c.GraphRootOID != graphRootOID {
		return choreographyInitMissingIdentity, fmt.Errorf(choreographyInitIncompleteError)
	}
	mailbox := c.canonicalMailbox
	head, err := mailbox.readRemoteTip(ctx, ledger.Branch)
	if err != nil {
		return choreographyInitMissingIdentity, err
	}
	root, err := mailbox.readRemoteTip(ctx, FormatChoreographyGraphRootBranch(c.Run))
	if err != nil {
		return choreographyInitMissingIdentity, err
	}
	if head != ledger.OID || root != graphRootOID {
		return choreographyInitMissingIdentity, fmt.Errorf(choreographyInitIncompleteError)
	}
	binding := ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest}
	event := ChoreographyEvent{Sequence: choreographyInitCompleteSequence, Kind: ChoreographyInitializationComplete,
		Task: choreographyRunTaskID, TaskOID: graphRootOID, GraphRootOID: graphRootOID}
	record := choreographyLedgerRecord{ChoreographyRunBinding: binding, Sequence: choreographyInitCompleteSequence, Event: &event}
	document, err := json.Marshal(record)
	if err != nil {
		return choreographyInitMissingIdentity, err
	}
	oid, err := mailbox.commitMessage(ctx, choreographyLedgerKind, document,
		[]string{ledger.OID, graphRootOID}, nil)
	if err != nil {
		return choreographyInitMissingIdentity, err
	}
	pushErr := mailbox.remote.PushAtomicAdvances(ctx, mailbox.endpoint,
		[]gitx.BranchAdvance{{Branch: ledger.Branch, ExpectedOld: ledger.OID, OID: oid}})
	settled, readErr := mailbox.readRemoteTip(ctx, ledger.Branch)
	if readErr == nil && settled == oid {
		c.recordOwnedRef(ctx, ownedRefStep{node: c.Local.Name, branch: ledger.Branch,
			parent: ledger.OID, oid: oid})
		return oid, nil
	}
	if pushErr != nil {
		return choreographyInitMissingIdentity, fmt.Errorf(choreographyInitCASUnknownErrorFormat, pushErr)
	}
	if readErr != nil {
		return choreographyInitMissingIdentity, fmt.Errorf(choreographyInitCASUnknownErrorFormat, readErr)
	}
	return choreographyInitMissingIdentity, fmt.Errorf(choreographyInitIncompleteError)
}
