// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"fmt"
)

const (
	choreographyPickupBudgetHeldErrorText = "execution: unresolved pickup retains the run budget needed for a successor"
	choreographyPickupBudgetTaskErrorText = "execution: successor budget names no live compiled task"
)

var ErrChoreographyPickupCapacityHeld = errors.New(choreographyPickupBudgetHeldErrorText)

// CheckPickupSuccessorBudget reports only the signed run-budget charge. It
// cannot infer a worker's physical availability across independent runs.
func (b *FirstNodeBroker) CheckPickupSuccessorBudget(taskID string) error {
	if b == nil {
		return fmt.Errorf(choreographyPickupBudgetTaskErrorText)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || task.terminal {
		return fmt.Errorf(choreographyPickupBudgetTaskErrorText)
	}
	if b.used[task.spec.Budget]+task.spec.Weight > b.budgets[task.spec.Budget] {
		return ErrChoreographyPickupCapacityHeld
	}
	return nil
}
