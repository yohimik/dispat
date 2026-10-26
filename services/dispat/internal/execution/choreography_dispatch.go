// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
)

const (
	choreographyDispatchMissingIdentity      = ""
	choreographyDispatchNoPredecessors       = 0
	choreographyDispatchNoFreeSlots          = 0
	choreographyDispatchLastSlotOffset       = 1
	choreographyDispatchNextSequenceOffset   = 1
	choreographyDispatchSlotOccupied         = true
	choreographyDispatchSlotFree             = false
	choreographyDispatchIncompleteOfferError = "execution: incomplete compiled assignment offer"
	choreographyDispatchNotReadyError        = "execution: command task is not ready to offer"
	choreographyDispatchNoIndexError         = "execution: peer dispatch index is unavailable"
	choreographyDispatchNoFreeIndexError     = "execution: peer has no free dispatch index"
)

// OfferChoreographyAssignment places one compiled command on its already
// created canonical task ref. The selected peer's fixed index is advanced
// in the same Git transaction; a first-local assignment needs no index.
// The lease remains held until finality, so its index cannot be reused while
// an earlier attempt may still execute.
func (c *Coordinator) OfferChoreographyAssignment(ctx context.Context,
	lease *Lease, assignment *Assignment) (ChoreographyAssignmentReceipt, error) {
	if c == nil || c.choreographyBroker == nil || c.choreographyPersist == nil ||
		lease == nil || assignment == nil || assignment.Task == choreographyDispatchMissingIdentity || lease.Node != assignment.Node {
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyDispatchIncompleteOfferError)
	}
	broker := c.choreographyBroker
	broker.mu.Lock()
	task := broker.tasks[assignment.Task]
	if task == nil || task.terminal || task.remaining != choreographyDispatchNoPredecessors || task.offeredNode != choreographyDispatchMissingIdentity {
		broker.mu.Unlock()
		return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyDispatchNotReadyError)
	}
	assignment.Protocol = ChoreographyProtocolVersion
	assignment.Branch = task.spec.Branch
	assignment.Attempt = task.attempt
	assignment.Weight = task.spec.Weight
	assignment.GraphDigest = c.GraphDigest
	assignment.BootstrapOID = c.BootstrapOID
	expected := task.tip
	broker.mu.Unlock()
	offer := ChoreographyAssignmentOffer{Assignment: assignment, ExpectedTaskOID: expected}
	var slot *dispatchSlot
	if !lease.IsLocal {
		state := c.choreographySlots
		if state == nil {
			return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyDispatchNoIndexError)
		}
		state.mu.Lock()
		free := state.free[lease.Node]
		if len(free) == choreographyDispatchNoFreeSlots {
			state.mu.Unlock()
			return ChoreographyAssignmentReceipt{}, fmt.Errorf(choreographyDispatchNoFreeIndexError)
		}
		slot = free[len(free)-choreographyDispatchLastSlotOffset]
		state.free[lease.Node] = free[:len(free)-choreographyDispatchLastSlotOffset]
		slot.inUse = choreographyDispatchSlotOccupied
		offer.SlotBranch, offer.OldSlotOID, offer.SlotSequence = slot.branch, slot.oid, slot.sequence+choreographyDispatchNextSequenceOffset
		lease.onSettle = func(reason string) {
			if reason != choreographyDispatchMissingIdentity {
				return
			}
			state.mu.Lock()
			if slot.inUse {
				slot.inUse = choreographyDispatchSlotFree
				state.free[lease.Node] = append(state.free[lease.Node], slot)
			}
			state.mu.Unlock()
		}
		state.mu.Unlock()
	}
	receipt, err := broker.OfferAssignment(ctx, offer)
	if err != nil {
		if errors.Is(err, errChoreographyAtomicRefused) {
			// The explicit atomic refusal and exact unchanged readback prove
			// that this assignment owns neither the task nor its fixed slot.
			lease.Release()
			return ChoreographyAssignmentReceipt{}, err
		}
		// Even a lost push reply may have activated this exact assignment.
		// Keep the node/index fenced until the signed ledger is reconciled.
		if !lease.IsLocal {
			lease.Leak(LeakTransport)
		}
		return ChoreographyAssignmentReceipt{}, err
	}
	if slot != nil {
		c.choreographySlots.mu.Lock()
		slot.oid, slot.sequence = receipt.SlotOID, offer.SlotSequence
		c.choreographySlots.mu.Unlock()
	}
	return receipt, nil
}
