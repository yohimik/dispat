// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyPickupDecisionBrokerMissingIdentity = ""
	choreographyPickupDecisionBrokerSettled         = true
)

// decidePickupAttempt runs under the broker mutex. The winning logical
// decision is one canonical task CAS; capacity settlement remains attached
// to the particular physical pickup that supplied its verified Result.
func (b *FirstNodeBroker) decidePickupAttempt(ctx context.Context,
	task *indexedChoreographyTask, pickup *indexedChoreographyPickup,
	d ChoreographyDecision) error {
	if d.ChoreographyRunBinding != b.binding || d.Branch != task.spec.Branch ||
		d.Attempt != pickup.attempt || d.Node != pickup.node ||
		d.ResultOID != pickup.resultReceiptOID || pickup.settled || task.terminal ||
		d.Retry && (d.Succeeded || pickup.attempt >= task.spec.EffectiveMaxAttempts()) {
		return fmt.Errorf(choreographyBrokerInvalidAttemptDecisionError)
	}
	if d.Retry && !task.spec.IsPickupRetryAllowed(pickup.resultOutputOID != choreographyNoAcceptedResultOutputOID) {
		return fmt.Errorf(choreographyBrokerInvalidAttemptDecisionError)
	}
	if !d.Succeeded && !d.Retry && task.attempt < task.spec.EffectiveMaxAttempts() && !b.stopping &&
		task.spec.IsPickupRetryAllowed(pickup.resultOutputOID != choreographyNoAcceptedResultOutputOID) {
		return fmt.Errorf(choreographyBrokerInvalidAttemptDecisionError)
	}
	if d.Retry && task.claim != pickup.claimOID {
		return fmt.Errorf(choreographyBrokerInvalidAttemptDecisionError)
	}
	if err := b.validatePackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	decisionOID, err := b.persist.PersistDecision(ctx, d)
	if err != nil {
		return err
	}
	if decisionOID == choreographyPickupDecisionBrokerMissingIdentity {
		return fmt.Errorf(choreographyBrokerPersistenceReturnedNoDecisionCommitError)
	}
	b.used[task.spec.Budget] -= task.spec.Weight
	b.nodeUsed[pickup.node] -= task.spec.Weight
	b.wakeBudgetWaiters()
	pickup.settled = choreographyPickupDecisionBrokerSettled
	task.tip = decisionOID
	if task.claim == pickup.claimOID {
		task.claim, task.cancel, task.result, task.owner =
			choreographyPickupDecisionBrokerMissingIdentity,
			choreographyPickupDecisionBrokerMissingIdentity,
			choreographyPickupDecisionBrokerMissingIdentity,
			choreographyPickupDecisionBrokerMissingIdentity
	}
	if d.Retry {
		task.attempt++
		task.resultOutputOID = choreographyNoAcceptedResultOutputOID
		task.offeredNode = choreographyPickupDecisionBrokerMissingIdentity
		task.frameReady, task.preparedInputOID = choreographyBrokerTaskFrameUnready,
			choreographyPickupDecisionBrokerMissingIdentity
		return nil
	}
	task.terminal, task.succeeded = choreographyBrokerTaskTerminal, d.Succeeded
	task.resultOutputOID = pickup.resultOutputOID
	if err := b.applyPackageOutcome(task.spec.Package, d.PackageOutcome); err != nil {
		return err
	}
	if task.spec.Package != choreographyPickupDecisionBrokerMissingIdentity &&
		d.PackageOutcome == choreographyPickupDecisionBrokerMissingIdentity &&
		!task.spec.ExecutorOwned {
		task.pendingPackageSettlement = choreographyBrokerPackageSettlementPending
	} else {
		b.discharge(task)
	}
	return nil
}
