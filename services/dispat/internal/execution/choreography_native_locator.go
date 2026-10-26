// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyNativeLocatorMissingIdentity     = ""
	choreographyNativeLocatorNoPredecessors      = 0
	choreographyNativeLocatorFirstAttempt        = 1
	choreographyNativeLocatorMissingRunError     = "execution: first-node native task has no retained run identity"
	choreographyNativeLocatorWrongRootError      = "execution: native task is not at its exact initial root"
	choreographyNativeLocatorBrokerMismatchError = "execution: native task roots differ from attached broker"
	choreographyNativeLocatorIncompleteError     = "execution: native task locator is incomplete"
)

// NativeNodeSlotLocator captures the retained signed task and run roots before
// a first-node physical slot is reserved. The slot journal writes this exact
// locator before a native effect or a branch-bearing Claim can begin; after a
// crash, read-only ledger replay can distinguish proven finality from an
// unresolved accepted attempt without relying on a live HTTPS listener.
func (c *Coordinator) NativeNodeSlotLocator(ctx context.Context,
	taskID string) (NodeSlotNativeRecoveryLocator, error) {
	if err := ctx.Err(); err != nil {
		return NodeSlotNativeRecoveryLocator{}, err
	}
	if c == nil || c.choreographyBroker == nil || c.choreographyPersist == nil ||
		c.Local.Name == choreographyNativeLocatorMissingIdentity || taskID == choreographyNativeLocatorMissingIdentity {
		return NodeSlotNativeRecoveryLocator{}, fmt.Errorf(choreographyNativeLocatorMissingRunError)
	}
	b := c.choreographyBroker
	b.mu.Lock()
	task := b.tasks[taskID]
	if task == nil || task.spec.Virtual || task.remaining != choreographyNativeLocatorNoPredecessors || task.terminal ||
		task.claim != choreographyNativeLocatorMissingIdentity || task.offeredNode != choreographyNativeLocatorMissingIdentity || b.stopping || b.sealed ||
		!containsNode(task.spec.EligibleNodes, c.Local.Name) ||
		(!task.spec.LocalAuthority && task.tip != task.spec.ReservationOID) {
		b.mu.Unlock()
		return NodeSlotNativeRecoveryLocator{}, fmt.Errorf(choreographyNativeLocatorWrongRootError)
	}
	spec, binding := task.spec, b.binding
	b.mu.Unlock()
	m, err := c.ChoreographyMailbox()
	if err != nil {
		return NodeSlotNativeRecoveryLocator{}, err
	}
	p := c.choreographyPersist
	if p.opts.Binding != binding || p.opts.BootstrapOID != c.BootstrapOID ||
		p.opts.Mailbox != m || p.opts.Ledger.OID == choreographyNativeLocatorMissingIdentity {
		return NodeSlotNativeRecoveryLocator{}, fmt.Errorf(choreographyNativeLocatorBrokerMismatchError)
	}
	locator := NodeSlotNativeRecoveryLocator{Binding: binding,
		CanonicalEndpoint: m.endpoint, BootstrapOID: c.BootstrapOID,
		LedgerRootOID: p.opts.Ledger.OID, TaskBranch: spec.Branch,
		ReservationOID: spec.ReservationOID, Kind: spec.Kind, Node: c.Local.Name}
	if !validNodeSlotNativeRecovery(NodeSlotIntent{Run: binding.Run, Task: taskID,
		Attempt: choreographyNativeLocatorFirstAttempt, Weight: spec.Weight, NativeRecovery: &locator}) {
		return NodeSlotNativeRecoveryLocator{}, fmt.Errorf(choreographyNativeLocatorIncompleteError)
	}
	return locator, nil
}
