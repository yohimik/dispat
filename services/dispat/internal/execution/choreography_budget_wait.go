// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
)

const (
	choreographyBudgetWaitStateErrorFormat = "execution: task %q cannot wait for an eligible signed claim budget"
	choreographyBudgetWaitNoPredecessors   = 0
	choreographyBudgetWaitMissingClaim     = ""
	choreographyBudgetWaitSignedStopping   = "execution: signed run stopping bars a new claim budget"
)

var ErrChoreographyRunStopping = errors.New(choreographyBudgetWaitSignedStopping)

// WaitClaimBudget waits for this caller's own task to fit the current signed
// run budget and admitted node capacity. It neither reserves capacity nor
// chooses a task; the subsequent exact claim CAS remains authoritative.
func (b *FirstNodeBroker) WaitClaimBudget(ctx context.Context, taskID, node string) error {
	if b == nil {
		return fmt.Errorf(choreographyBudgetWaitStateErrorFormat, taskID)
	}
	for {
		b.mu.Lock()
		task := b.tasks[taskID]
		if b.stopping {
			b.mu.Unlock()
			return ErrChoreographyRunStopping
		}
		if b.sealed || b.fault != nil || task == nil ||
			task.spec.Virtual || task.remaining != choreographyBudgetWaitNoPredecessors || task.terminal || task.claim != choreographyBudgetWaitMissingClaim ||
			!b.nodeEligible(task.spec, node) {
			b.mu.Unlock()
			return fmt.Errorf(choreographyBudgetWaitStateErrorFormat, taskID)
		}
		if b.executionStarted &&
			b.used[task.spec.Budget]+task.spec.Weight <= b.budgets[task.spec.Budget] &&
			b.nodeUsed[node]+task.spec.Weight <= b.nodeCaps[node] {
			b.mu.Unlock()
			return nil
		}
		wake := b.budgetWake
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

// EligibilityWake closes after a signed membership, capacity, readiness, or
// stop transition. Observers reread task readiness and membership after waking.
func (b *FirstNodeBroker) EligibilityWake() <-chan struct{} {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.budgetWake
}

// Call while b.mu is held after a signed release, drain or stop transition.
func (b *FirstNodeBroker) wakeBudgetWaiters() {
	if b.budgetWake != nil {
		close(b.budgetWake)
	}
	b.budgetWake = make(chan struct{})
}
