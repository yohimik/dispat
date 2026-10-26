// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	delegatedFailureOutcomeErrorFormat           = "execution: delegated local failure has package outcome %q, want failed or prior signed failure"
	delegatedFailureInertErrorFormat             = "execution: delegated task %q has no inert no-claim state"
	delegatedFailureBlockerErrorFormat           = "execution: delegated task %q has blocker %q, want its own package %q"
	choreographyNoClaimMissingIdentity           = ""
	choreographyNoClaimUnproven                  = false
	choreographyNoClaimProven                    = true
	choreographyNoClaimFirstAttempt              = 1
	choreographyNoClaimNoPredecessorEdges        = 0
	choreographyNoClaimMissingBrokerError        = "execution: choreography broker is unavailable"
	choreographyNoClaimUnknownTaskErrorFormat    = "execution: unknown choreography task %s"
	choreographyNoClaimUnsettledEdgesErrorFormat = "execution: choreography task %s has %d unsettled predecessor edges before a no-claim decision"
	choreographyNoClaimAdvancedTaskErrorFormat   = "execution: choreography task %s changed before no-claim proof"
)

// ProveChoreographyNoClaim identifies a ready task whose canonical ref is
// still its inert reservation. A release prelude can fail before offering a
// frame; only this exact state permits a no-claim terminal decision.
func (c *Coordinator) ProveChoreographyNoClaim(ctx context.Context, taskID string) (string, bool, error) {
	if c == nil || c.choreographyBroker == nil {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven,
			fmt.Errorf(choreographyNoClaimMissingBrokerError)
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven, err
	}
	b := c.choreographyBroker
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven,
			fmt.Errorf(choreographyNoClaimUnknownTaskErrorFormat, taskID)
	}
	if ctx.Err() != nil || b.stopping || b.sealed || task.spec.Virtual || task.spec.LocalAuthority ||
		task.spec.Package == choreographyNoClaimMissingIdentity || task.terminal ||
		task.claim != choreographyNoClaimMissingIdentity ||
		task.offeredNode != choreographyNoClaimMissingIdentity ||
		task.queueFence != choreographyNoClaimMissingIdentity ||
		task.result != choreographyNoClaimMissingIdentity ||
		task.attempt != choreographyNoClaimFirstAttempt || task.tip != task.spec.ReservationOID {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven, nil
	}
	if task.remaining != choreographyNoClaimNoPredecessorEdges {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven,
			fmt.Errorf(choreographyNoClaimUnsettledEdgesErrorFormat,
				taskID, task.remaining)
	}
	head, err := mailbox.Reread(ctx, task.spec.Branch)
	if err != nil {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven, err
	}
	if head.OID != task.spec.ReservationOID {
		return choreographyNoClaimMissingIdentity, choreographyNoClaimUnproven,
			fmt.Errorf(choreographyNoClaimAdvancedTaskErrorFormat, taskID)
	}
	if task.preparedBlockedBy != choreographyNoClaimMissingIdentity {
		return task.preparedBlockedBy, choreographyNoClaimProven, nil
	}
	return task.spec.Package, choreographyNoClaimProven, nil
}

// BlockChoreographyDelegatedFailure signs an actual first-node error before
// any offer or claim on a delegated task. The release executor calls it only
// after its local onFail and revert hooks have settled the package failure.
type ChoreographyDelegatedFailure struct {
	TaskID      string
	Part        string
	ErrorSHA256 string
	Outcome     release.PackageOutcome
}

func (c *Coordinator) BlockChoreographyDelegatedFailure(ctx context.Context,
	failure ChoreographyDelegatedFailure) error {
	taskID, part := failure.TaskID, failure.Part
	errorSHA256, outcome := failure.ErrorSHA256, failure.Outcome
	if outcome != release.OutcomeFailed && outcome != choreographyNoClaimMissingIdentity {
		return fmt.Errorf(delegatedFailureOutcomeErrorFormat, outcome)
	}
	blocker, proven, err := c.ProveChoreographyNoClaim(ctx, taskID)
	if err != nil {
		return err
	}
	if !proven || c.choreographyBroker == nil {
		return fmt.Errorf(delegatedFailureInertErrorFormat, taskID)
	}
	b := c.choreographyBroker
	b.mu.Lock()
	task := b.tasks[taskID]
	var pkg string
	if task != nil {
		pkg = task.spec.Package
	}
	b.mu.Unlock()
	if pkg == choreographyNoClaimMissingIdentity || blocker != pkg {
		return fmt.Errorf(delegatedFailureBlockerErrorFormat, taskID, blocker, pkg)
	}
	return b.AcceptBlocked(ctx, ChoreographyBlockedOutcome{
		ChoreographyRunBinding: b.binding, Task: taskID,
		Package: pkg, BlockedBy: pkg, PackageOutcome: string(outcome),
		LocalFailure: &ChoreographyLocalFailureEvidence{
			Part: part, ErrorSHA256: errorSHA256,
		},
	})
}
