// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"fmt"
)

const (
	choreographyPickupBudgetHeldErrorText     = "execution: unresolved pickup retains the run budget needed for a successor"
	choreographyPickupBudgetTaskErrorText     = "execution: successor budget names no live compiled task"
	choreographyPickupBudgetIdentityErrorText = "execution: successor budget names no matching contact-lost pickup"
	choreographyPickupBudgetMissingIdentity   = ""
)

var ErrChoreographyPickupCapacityHeld = errors.New(choreographyPickupBudgetHeldErrorText)

type ChoreographyPickupSuccessor struct {
	ChoreographyRunBinding
	Task     string
	ClaimOID string
	PickupID string
}

// CheckPickupSuccessorBudget observes advancement and budget under one lock;
// a successor admitted during contact-loss handling already owns its charge.
// Success only permits event draining, never a claim or physical release.
func (b *FirstNodeBroker) CheckPickupSuccessorBudget(query ChoreographyPickupSuccessor) error {
	if b == nil {
		return fmt.Errorf(choreographyPickupBudgetTaskErrorText)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fault != nil {
		return b.fault
	}
	task := b.tasks[query.Task]
	if task == nil {
		return fmt.Errorf(choreographyPickupBudgetTaskErrorText)
	}
	pickup := task.pickups[query.PickupID]
	if query.ChoreographyRunBinding != b.binding ||
		query.ClaimOID == choreographyPickupBudgetMissingIdentity ||
		query.PickupID == choreographyPickupBudgetMissingIdentity || pickup == nil ||
		pickup.id != query.PickupID || pickup.claimOID != query.ClaimOID || !pickup.contactLost {
		return fmt.Errorf(choreographyPickupBudgetIdentityErrorText)
	}
	if task.terminal || pickup.settled || pickup.resultReceiptOID != choreographyPickupBudgetMissingIdentity ||
		task.claim != choreographyPickupBudgetMissingIdentity && task.claim != query.ClaimOID {
		return nil
	}
	if b.used[task.spec.Budget]+task.spec.Weight > b.budgets[task.spec.Budget] {
		return ErrChoreographyPickupCapacityHeld
	}
	return nil
}
