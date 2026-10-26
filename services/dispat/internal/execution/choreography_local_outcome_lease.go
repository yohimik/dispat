// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyLocalLeaseMissingIdentity  = ""
	choreographyLocalLeaseMinimumWeight    = 1
	choreographyLocalLeaseNotReusable      = false
	choreographyLocalLeaseReusable         = true
	choreographyLocalLeaseStopScan         = false
	choreographyLocalLeaseContinueScan     = true
	choreographyLocalLeaseUnavailableError = "execution: first-local outcome lease is unavailable"
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
	if c == nil || c.choreographyBroker == nil || slots == nil || pkg == choreographyLocalLeaseMissingIdentity || requiredWeight < choreographyLocalLeaseMinimumWeight {
		return choreographyLocalLeaseNotReusable, fmt.Errorf(choreographyLocalLeaseUnavailableError)
	}
	held, err := slots.Held(ctx)
	if err != nil {
		return choreographyLocalLeaseNotReusable, err
	}
	binding := c.choreographyBroker.binding
	reusable := choreographyLocalLeaseNotReusable
	c.choreographyPending.Range(func(_, value any) bool {
		pending := value.(*choreographyPendingDecision)
		pending.mu.Lock()
		defer pending.mu.Unlock()
		if pending.packageName != pkg || pending.lease == nil || !pending.lease.IsLocal ||
			pending.decision.Unclaimed || pending.decided || pending.localEnded || pending.localError != nil ||
			(!pending.resultAccepted && !pending.ackAccepted) ||
			pending.decision.ResultOID == choreographyLocalLeaseMissingIdentity || pending.assignmentOID == choreographyLocalLeaseMissingIdentity {
			return choreographyLocalLeaseContinueScan
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
			reusable = choreographyLocalLeaseReusable
			return choreographyLocalLeaseStopScan
		}
		return choreographyLocalLeaseContinueScan
	})
	return reusable, nil
}
