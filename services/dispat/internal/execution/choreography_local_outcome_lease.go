// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

// HasStoppedFirstLocalStage reports whether a package outcome hook can reuse
// an already-held first-node physical slot. It accepts only an indexed signed
// ResultAccepted whose command returned or an exact signed AckAccepted after
// Cancel→Claim→Assignment, paired with the durable NodeSlot lease. A running
// Claim or merely fenced unclaimed offer is insufficient.
// The Executor asks only after the package's in-flight work reaches zero;
// task finality keeps the matching slot held until that hook completes.
func (c *Coordinator) HasStoppedFirstLocalStage(ctx context.Context, pkg string,
	requiredWeight int, slots *NodeSlotAuthority) (bool, error) {
	if c == nil || c.choreographyBroker == nil || slots == nil || pkg == "" || requiredWeight < 1 {
		return false, fmt.Errorf("execution: first-local outcome lease is unavailable")
	}
	held, err := slots.Held(ctx)
	if err != nil {
		return false, err
	}
	binding := c.choreographyBroker.binding
	reusable := false
	c.choreographyPending.Range(func(_, value any) bool {
		pending := value.(*choreographyPendingDecision)
		pending.mu.Lock()
		defer pending.mu.Unlock()
		if pending.packageName != pkg || pending.lease == nil || !pending.lease.IsLocal ||
			pending.decision.Unclaimed || pending.decided || pending.localEnded || pending.localError != nil ||
			(!pending.resultAccepted && !pending.ackAccepted) ||
			pending.decision.ResultOID == "" || pending.assignmentOID == "" {
			return true
		}
		for _, lease := range held {
			locator := lease.Intent.Recovery
			if locator == nil || lease.Intent.Weight < requiredWeight ||
				lease.Intent.Run != binding.Run ||
				lease.Intent.Task != pending.decision.Task ||
				lease.Intent.Attempt != pending.decision.Attempt ||
				locator.Binding != binding || locator.Node != c.Local.Name ||
				locator.AssignmentOID != pending.assignmentOID {
				continue
			}
			reusable = true
			return false
		}
		return true
	})
	return reusable, nil
}
