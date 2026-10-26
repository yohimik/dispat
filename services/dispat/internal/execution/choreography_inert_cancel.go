// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	choreographyInertCancelKind                        MessageKind = "choreography-inert-cancelled"
	choreographyInertCancelCause                                   = "run-stopping"
	choreographyInertCancelStateError                              = "execution: inert cancellation requires a stopped, unclaimed canonical task"
	choreographyInertCancelPersistError                            = "execution: inert cancellation has no canonical task persistence"
	choreographyInertCancelOutcomeCancelled                        = "cancelled"
	choreographyInertCancelOutcomeFailed                           = "failed"
	choreographyInertCancelOutcomeAbsent                           = ""
	choreographyInertCancelSettlementIdentityPrefix                = "inert-cancel-settle:"
	choreographyInertCancelSettlementIdentitySeparator             = ":"
	choreographyInertCancelEmptyIdentity                           = choreographyInertCancelOutcomeAbsent
	choreographyInertCancelMinimumAttempt                          = 1
	choreographyInertCancelParentCount                             = 1
	choreographyInertCancelFirstParent                             = 0
	choreographyInertCancelTerminalState                           = true
	choreographyInertCancelFailedState                             = false
	choreographyInertCancelPendingState                            = true
	choreographyInertCancelSettledState                            = false
	choreographyInertCancelFoundState                              = true
	choreographyInertCancelMissingState                            = false
	choreographyInertCancelInvalidOutcome                          = false
)

func isValidChoreographyInertCancelOutcome(outcome, prior string) bool {
	switch outcome {
	case choreographyInertCancelOutcomeCancelled, choreographyInertCancelOutcomeFailed:
		return prior == choreographyInertCancelOutcomeAbsent
	case choreographyInertCancelOutcomeAbsent:
		return prior == choreographyInertCancelOutcomeCancelled || prior == choreographyInertCancelOutcomeFailed
	default:
		return choreographyInertCancelInvalidOutcome
	}
}

type ChoreographyInertCancellation struct {
	ChoreographyRunBinding
	GraphRootOID string `json:"graphRootOid"`
	Task         string `json:"task"`
	Attempt      int    `json:"attempt"`
	Branch       string `json:"branch"`
	ExpectedOID  string `json:"expectedOid"`
	Package      string `json:"package,omitempty"`
	Cause        string `json:"cause"`
}

// CancelInertTask settles a stopped task which never acquired a lease. A
// release package keeps its outgoing outcome gate until the executor settles
// same-package siblings and failure hooks.
func (b *FirstNodeBroker) CancelInertTask(ctx context.Context, taskID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if !b.stopping || task == nil || task.spec.Virtual ||
		task.terminal || task.claim != choreographyInertCancelEmptyIdentity || task.result != choreographyInertCancelEmptyIdentity || task.offeredNode != choreographyInertCancelEmptyIdentity ||
		task.queueFence != choreographyInertCancelEmptyIdentity || task.tip == choreographyInertCancelEmptyIdentity {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	persist, ok := b.persist.(interface {
		PersistInertCancellation(context.Context, ChoreographyInertCancellation) (string, error)
	})
	if !ok {
		return fmt.Errorf(choreographyInertCancelPersistError)
	}
	outcome := ChoreographyInertCancellation{ChoreographyRunBinding: b.binding,
		GraphRootOID: b.graphRootOID, Task: taskID, Attempt: task.attempt,
		Branch: task.spec.Branch, ExpectedOID: task.tip, Package: task.spec.Package,
		Cause: choreographyInertCancelCause}
	oid, err := persist.PersistInertCancellation(ctx, outcome)
	if err != nil {
		return err
	}
	task.tip, task.terminal, task.succeeded = oid, choreographyInertCancelTerminalState, choreographyInertCancelFailedState
	if task.spec.Package != choreographyInertCancelEmptyIdentity && task.spec.Kind != string(KindPrepare) {
		task.pendingInertCancel = choreographyInertCancelPendingState
	} else {
		b.discharge(task)
	}
	b.wakeBudgetWaiters()
	return nil
}

func (b *FirstNodeBroker) IsInertCancelled(taskID string) bool {
	if b == nil {
		return choreographyInertCancelMissingState
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	return task != nil && task.pendingInertCancel
}

func (b *FirstNodeBroker) SettleInertCancellation(ctx context.Context, taskID, packageOutcome string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || !task.pendingInertCancel || !task.terminal || task.spec.Package == choreographyInertCancelEmptyIdentity {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	prior := b.packageState[task.spec.Package]
	if !isValidChoreographyInertCancelOutcome(packageOutcome, prior) {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	if packageOutcome != choreographyInertCancelOutcomeAbsent {
		if err := b.validatePackageOutcome(task.spec.Package, packageOutcome); err != nil {
			return err
		}
	}
	persist, ok := b.persist.(interface {
		PersistInertCancelSettlement(context.Context, string, string, string) error
	})
	if !ok {
		return fmt.Errorf(choreographyInertCancelPersistError)
	}
	if err := persist.PersistInertCancelSettlement(ctx, taskID, task.tip, packageOutcome); err != nil {
		return err
	}
	if err := b.applyPackageOutcome(task.spec.Package, packageOutcome); err != nil {
		return err
	}
	task.pendingInertCancel = choreographyInertCancelSettledState
	b.discharge(task)
	return nil
}

func (g *GitChoreographyPersist) PersistInertCancellation(ctx context.Context,
	outcome ChoreographyInertCancellation) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyInertCancelEmptyIdentity, err
	}
	spec := g.tasks[outcome.Task]
	if spec.Task == choreographyInertCancelEmptyIdentity || outcome.ChoreographyRunBinding != g.opts.Binding ||
		outcome.GraphRootOID != g.opts.GraphRootOID || outcome.Branch != spec.Branch ||
		outcome.ExpectedOID == choreographyInertCancelEmptyIdentity || outcome.Attempt < choreographyInertCancelMinimumAttempt || outcome.Package != spec.Package ||
		outcome.Cause != choreographyInertCancelCause {
		return choreographyInertCancelEmptyIdentity, fmt.Errorf(choreographyInertCancelStateError)
	}
	if _, err := g.LedgerHead(ctx); err != nil {
		return choreographyInertCancelEmptyIdentity, err
	}
	g.mu.Lock()
	stopping := g.stopping
	g.mu.Unlock()
	if !stopping {
		return choreographyInertCancelEmptyIdentity, fmt.Errorf(choreographyInertCancelStateError)
	}
	document, err := json.Marshal(outcome)
	if err != nil {
		return choreographyInertCancelEmptyIdentity, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyInertCancelEmptyIdentity {
		oid, err = g.opts.Mailbox.commitMessage(ctx, choreographyInertCancelKind, document, []string{outcome.ExpectedOID}, nil)
		if err != nil {
			return choreographyInertCancelEmptyIdentity, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{Identity: identity,
		TaskBranch: spec.Branch, OldTaskOID: outcome.ExpectedOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyInertCancelled, Task: outcome.Task,
			Attempt: outcome.Attempt, TaskOID: oid, PriorTaskOID: outcome.ExpectedOID,
			GraphRootOID: outcome.GraphRootOID, Package: outcome.Package,
			BlockedBy: choreographyInertCancelCause}}); err != nil {
		return choreographyInertCancelEmptyIdentity, err
	}
	return oid, nil
}

func (g *GitChoreographyPersist) PersistInertCancelSettlement(ctx context.Context,
	taskID, cancelOID, packageOutcome string) error {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	spec := g.tasks[taskID]
	if spec.Task == choreographyInertCancelEmptyIdentity || spec.Package == choreographyInertCancelEmptyIdentity || cancelOID == choreographyInertCancelEmptyIdentity {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	found, settled := choreographyInertCancelMissingState, choreographyInertCancelMissingState
	prior := g.packageState[spec.Package]
	var attempt int
	for _, position := range g.taskEventPositionsLocked(taskID) {
		event := g.events[position]
		if event.Kind == ChoreographyInertCancelled && event.TaskOID == cancelOID {
			found, attempt = choreographyInertCancelFoundState, event.Attempt
		}
		if event.Kind == ChoreographyInertCancelSettled {
			settled = choreographyInertCancelFoundState
		}
	}
	g.mu.Unlock()
	if !found || settled || !isValidChoreographyInertCancelOutcome(packageOutcome, prior) {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	return g.persistAtomic(ctx, choreographyLedgerMutation{Identity: choreographyInertCancelSettlementIdentityPrefix + cancelOID + choreographyInertCancelSettlementIdentitySeparator + packageOutcome,
		ExpectedLedgerOID: head.OID, NewTaskOID: cancelOID,
		Event: ChoreographyEvent{Kind: ChoreographyInertCancelSettled, Task: taskID, Attempt: attempt,
			TaskOID: cancelOID, PriorTaskOID: cancelOID, GraphRootOID: g.opts.GraphRootOID,
			Package: spec.Package, PackageOutcome: packageOutcome, BlockedBy: choreographyInertCancelCause}})
}

func (g *GitChoreographyPersist) verifyInertCancelEvent(ctx context.Context, event ChoreographyEvent) error {
	spec := g.tasks[event.Task]
	if spec.Task == choreographyInertCancelEmptyIdentity || event.GraphRootOID != g.opts.GraphRootOID ||
		event.Package != spec.Package || event.BlockedBy != choreographyInertCancelCause ||
		event.PriorTaskOID == choreographyInertCancelEmptyIdentity || event.TaskOID == choreographyInertCancelEmptyIdentity || event.PackageOutcome != choreographyInertCancelEmptyIdentity || !g.stopping {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	parents, err := g.opts.Mailbox.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil || len(parents) != choreographyInertCancelParentCount || parents[choreographyInertCancelFirstParent] != event.PriorTaskOID {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	tree, err := g.opts.Mailbox.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyInertCancelKind {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	document, err := g.opts.Mailbox.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind, document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var outcome ChoreographyInertCancellation
	if err := strictJSON(document, &outcome); err != nil {
		return err
	}
	if outcome.ChoreographyRunBinding != g.opts.Binding || outcome.GraphRootOID != g.opts.GraphRootOID ||
		outcome.Task != event.Task || outcome.Attempt != event.Attempt || outcome.Branch != spec.Branch ||
		outcome.ExpectedOID != event.PriorTaskOID || outcome.Package != spec.Package ||
		outcome.Cause != choreographyInertCancelCause {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	return nil
}

func (g *GitChoreographyPersist) verifyInertCancelSettlementEvent(ctx context.Context, event ChoreographyEvent) error {
	spec := g.tasks[event.Task]
	if spec.Task == choreographyInertCancelEmptyIdentity || spec.Package == choreographyInertCancelEmptyIdentity || event.Package != spec.Package ||
		event.GraphRootOID != g.opts.GraphRootOID || event.TaskOID != event.PriorTaskOID ||
		event.BlockedBy != choreographyInertCancelCause {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	found, settled := choreographyInertCancelMissingState, choreographyInertCancelMissingState
	for _, position := range g.taskEventPositionsLocked(event.Task) {
		prior := g.events[position]
		if prior.Kind == ChoreographyInertCancelled && prior.TaskOID == event.TaskOID && prior.Attempt == event.Attempt {
			found = choreographyInertCancelFoundState
		}
		if prior.Kind == ChoreographyInertCancelSettled {
			settled = choreographyInertCancelFoundState
		}
	}
	if !found || settled || !isValidChoreographyInertCancelOutcome(event.PackageOutcome, g.packageState[spec.Package]) {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	tip, err := g.opts.Mailbox.readRemoteTip(ctx, spec.Branch)
	if err != nil {
		return err
	}
	if tip != event.TaskOID {
		return fmt.Errorf(choreographyInertCancelStateError)
	}
	return nil
}
