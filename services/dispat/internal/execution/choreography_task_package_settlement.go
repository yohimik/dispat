// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyTaskPackageSettlementStateError        = "execution: task package settlement lacks exact pending signed finality"
	choreographyTaskPackageSettlementPersistError      = "execution: task package settlement has no canonical ledger persistence"
	choreographyTaskPackageSettlementIdentityPrefix    = "task-package-settle:"
	choreographyTaskPackageSettlementIdentitySeparator = ":"
	choreographyTaskPackageSettlementNoIdentity        = ""
	choreographyTaskPackageSettlementComplete          = false
	choreographyTaskPackageSettlementFound             = true
	choreographyTaskPackageSettlementMissing           = false
	choreographyTaskPackageSettlementNoWeight          = 0
	choreographyTaskPackageSettlementOutcomeMismatch   = "execution: settled task package outcome differs from exact signed record"
	choreographyTaskPackageSettlementCausalOrderError  = "execution: noncausal failed task package settlement precedes signed package failure"
	choreographyTaskPackageSettlementAdmissionError    = "execution: rejected output admission does not match signed task and package finality"
)

// ChoreographyTaskPackageSettlement is the first node's signed conclusion
// after output admission and the package's sibling and hook barrier. The
// disposition is independent of the worker's immutable Result status.
type ChoreographyTaskPackageSettlement struct {
	TaskID                  string
	TerminalOID             string
	PackageOutcome          string
	OutputAdmissionRejected bool
}

type choreographyTaskPackageSettlementCheck struct {
	Task         ChoreographyTaskSpec
	Outcome      string
	Succeeded    bool
	PriorOutcome string
}

func validateChoreographyTaskPackageSettlementOutcome(spec ChoreographyTaskSpec,
	succeeded bool, settlement ChoreographyTaskPackageSettlement) error {
	outcome := settlement.PackageOutcome
	dead := outcome == choreographyGitOutcomeFailed || outcome == choreographyGitOutcomeSkipped ||
		outcome == choreographyGitOutcomeCancelled
	if spec.Kind == string(KindPrepare) && settlement.OutputAdmissionRejected &&
		outcome == choreographyTaskPackageSettlementNoIdentity &&
		!spec.ExecutorOwned && !spec.LocalAuthority && !spec.Recording {
		return nil
	}
	if settlement.OutputAdmissionRejected &&
		(!dead || spec.LocalAuthority || spec.Recording || spec.ExecutorOwned) {
		return fmt.Errorf(choreographyTaskPackageSettlementAdmissionError)
	}
	if succeeded && dead && !spec.LocalAuthority && !spec.Recording && !spec.ExecutorOwned {
		return nil
	}
	return validateChoreographyPackageOutcome(ChoreographyPackageOutcomeCheck{
		Task: spec, Outcome: outcome, Succeeded: succeeded})
}

func validateTaskPackageSettlementOrder(check choreographyTaskPackageSettlementCheck) error {
	if check.Outcome != choreographyTaskPackageSettlementNoIdentity || check.Succeeded ||
		check.Task.Kind == string(KindPrepare) {
		return nil
	}
	switch check.PriorOutcome {
	case choreographyBrokerOutcomeFailed, choreographyBrokerOutcomeCancelled,
		choreographyBrokerOutcomeSkipped, choreographyBrokerOutcomeRecordBlocked:
		return nil
	default:
		return fmt.Errorf(choreographyTaskPackageSettlementCausalOrderError)
	}
}

func (g *GitChoreographyPersist) taskEventPositionsLocked(taskID string) []int {
	if g.taskEvents == nil {
		g.taskEvents = make(map[string][]int)
		for position, event := range g.events {
			g.taskEvents[event.Task] = append(g.taskEvents[event.Task], position)
		}
	}
	return g.taskEvents[taskID]
}

// SettleTaskPackage releases a terminal task's graph edges only after the
// executor has settled same-package siblings and any failure hooks. Its
// earlier signed terminal already released weighted run and node capacity.
func (b *FirstNodeBroker) SettleTaskPackage(ctx context.Context, settlement ChoreographyTaskPackageSettlement) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	taskID, packageOutcome := settlement.TaskID, settlement.PackageOutcome
	task := b.tasks[taskID]
	if task == nil || !task.terminal ||
		task.spec.Package == choreographyTaskPackageSettlementNoIdentity ||
		task.tip == choreographyTaskPackageSettlementNoIdentity {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	lookup, ok := b.persist.(interface {
		LookupTaskPackageSettlement(context.Context, string, string) (ChoreographyTaskPackageSettlement, bool, error)
	})
	if !ok {
		return fmt.Errorf(choreographyTaskPackageSettlementPersistError)
	}
	settled, found, err := lookup.LookupTaskPackageSettlement(ctx, taskID, task.tip)
	if err != nil {
		return err
	}
	if found {
		if settled.PackageOutcome != packageOutcome ||
			settled.OutputAdmissionRejected != settlement.OutputAdmissionRejected {
			return fmt.Errorf(choreographyTaskPackageSettlementOutcomeMismatch)
		}
		if task.pendingPackageSettlement {
			return b.completeTaskPackageSettlement(task, settlement)
		}
		return nil
	}
	if !task.pendingPackageSettlement {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	if err := b.validatePackageOutcome(task.spec.Package, packageOutcome); err != nil {
		return err
	}
	if err := validateChoreographyTaskPackageSettlementOutcome(task.spec, task.succeeded, settlement); err != nil {
		return err
	}
	if err := validateTaskPackageSettlementOrder(choreographyTaskPackageSettlementCheck{
		Task: task.spec, Outcome: packageOutcome, Succeeded: task.succeeded,
		PriorOutcome: b.packageState[task.spec.Package]}); err != nil {
		return err
	}
	persist, ok := b.persist.(interface {
		PersistTaskPackageSettlement(context.Context, ChoreographyTaskPackageSettlement) error
	})
	if !ok {
		return fmt.Errorf(choreographyTaskPackageSettlementPersistError)
	}
	settlement.TerminalOID = task.tip
	if err := persist.PersistTaskPackageSettlement(ctx, settlement); err != nil {
		return err
	}
	return b.completeTaskPackageSettlement(task, settlement)
}

func (b *FirstNodeBroker) completeTaskPackageSettlement(task *indexedChoreographyTask,
	settlement ChoreographyTaskPackageSettlement) error {
	packageOutcome := settlement.PackageOutcome
	if err := b.applyPackageOutcome(task.spec.Package, packageOutcome); err != nil {
		return err
	}
	if settlement.OutputAdmissionRejected && task.spec.Kind == string(KindPrepare) {
		task.preparedBlockedBy = task.spec.Package
	}
	task.pendingPackageSettlement = choreographyTaskPackageSettlementComplete
	b.discharge(task)
	return nil
}

// LookupTaskPackageSettlement reads the exact durable settlement record. It is
// used after a lost CAS response as well as by a cold broker replay.
func (g *GitChoreographyPersist) LookupTaskPackageSettlement(ctx context.Context,
	taskID, terminalOID string) (ChoreographyTaskPackageSettlement, bool, error) {
	if _, err := g.LedgerHead(ctx); err != nil {
		return ChoreographyTaskPackageSettlement{}, choreographyTaskPackageSettlementMissing, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, position := range g.taskEventPositionsLocked(taskID) {
		event := g.events[position]
		if event.Kind == ChoreographyTaskPackageSettled &&
			event.Task == taskID && event.TaskOID == terminalOID {
			return ChoreographyTaskPackageSettlement{TaskID: taskID, TerminalOID: terminalOID,
				PackageOutcome:          event.PackageOutcome,
				OutputAdmissionRejected: event.OutputAdmissionRejected}, choreographyTaskPackageSettlementFound, nil
		}
	}
	return ChoreographyTaskPackageSettlement{}, choreographyTaskPackageSettlementMissing, nil
}

func (g *GitChoreographyPersist) PersistTaskPackageSettlement(ctx context.Context,
	settlement ChoreographyTaskPackageSettlement) error {
	taskID, terminalOID, packageOutcome := settlement.TaskID, settlement.TerminalOID, settlement.PackageOutcome
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	spec := g.tasks[taskID]
	if spec.Task == choreographyTaskPackageSettlementNoIdentity ||
		spec.Package == choreographyTaskPackageSettlementNoIdentity ||
		terminalOID == choreographyTaskPackageSettlementNoIdentity {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	found, settled := choreographyTaskPackageSettlementMissing, choreographyTaskPackageSettlementMissing
	var attempt int
	var succeeded bool
	for _, position := range g.taskEventPositionsLocked(taskID) {
		event := g.events[position]
		if event.Kind == ChoreographyTerminal && event.TaskOID == terminalOID &&
			event.ResultReceiptOID != choreographyTaskPackageSettlementNoIdentity &&
			event.PackageOutcome == choreographyTaskPackageSettlementNoIdentity {
			found, attempt, succeeded = choreographyTaskPackageSettlementFound, event.Attempt, event.Succeeded
		}
		if event.Kind == ChoreographyTaskPackageSettled {
			settled = choreographyTaskPackageSettlementFound
		}
	}
	g.mu.Unlock()
	if !found || settled {
		if settled {
			outcome, known, lookupErr := g.LookupTaskPackageSettlement(ctx, taskID, terminalOID)
			if lookupErr != nil {
				return lookupErr
			}
			if known && outcome.PackageOutcome == packageOutcome &&
				outcome.OutputAdmissionRejected == settlement.OutputAdmissionRejected {
				return nil
			}
			return fmt.Errorf(choreographyTaskPackageSettlementOutcomeMismatch)
		}
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	if err := validateChoreographyTaskPackageSettlementOutcome(spec, succeeded, settlement); err != nil {
		return err
	}
	g.mu.Lock()
	priorOutcome := g.packageState[spec.Package]
	g.mu.Unlock()
	if err := validateTaskPackageSettlementOrder(choreographyTaskPackageSettlementCheck{
		Task: spec, Outcome: packageOutcome, Succeeded: succeeded,
		PriorOutcome: priorOutcome}); err != nil {
		return err
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: choreographyTaskPackageSettlementIdentityPrefix + terminalOID +
			choreographyTaskPackageSettlementIdentitySeparator + packageOutcome +
			choreographyTaskPackageSettlementIdentitySeparator + fmt.Sprint(settlement.OutputAdmissionRejected),
		ExpectedLedgerOID: head.OID, NewTaskOID: terminalOID,
		Event: ChoreographyEvent{Kind: ChoreographyTaskPackageSettled, Task: taskID,
			Attempt: attempt, TaskOID: terminalOID, PriorTaskOID: terminalOID,
			GraphRootOID: g.opts.GraphRootOID, Package: spec.Package,
			PackageOutcome:          packageOutcome,
			OutputAdmissionRejected: settlement.OutputAdmissionRejected},
	}); err != nil {
		outcome, known, lookupErr := g.LookupTaskPackageSettlement(ctx, taskID, terminalOID)
		if lookupErr == nil && known && outcome.PackageOutcome == packageOutcome &&
			outcome.OutputAdmissionRejected == settlement.OutputAdmissionRejected {
			return nil
		}
		return err
	}
	return nil
}

func (g *GitChoreographyPersist) verifyTaskPackageSettlementEvent(ctx context.Context,
	event ChoreographyEvent) error {
	spec := g.tasks[event.Task]
	if spec.Task == choreographyTaskPackageSettlementNoIdentity ||
		spec.Package == choreographyTaskPackageSettlementNoIdentity ||
		event.Package != spec.Package || event.GraphRootOID != g.opts.GraphRootOID ||
		event.TaskOID == choreographyTaskPackageSettlementNoIdentity ||
		event.PriorTaskOID != event.TaskOID ||
		event.Node != choreographyTaskPackageSettlementNoIdentity ||
		event.Budget != choreographyTaskPackageSettlementNoIdentity ||
		event.Weight != choreographyTaskPackageSettlementNoWeight ||
		event.ResultReceiptOID != choreographyTaskPackageSettlementNoIdentity ||
		event.OutputOID != choreographyTaskPackageSettlementNoIdentity {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	found, settled := choreographyTaskPackageSettlementMissing, choreographyTaskPackageSettlementMissing
	var succeeded bool
	for _, position := range g.taskEventPositionsLocked(event.Task) {
		prior := g.events[position]
		if prior.Kind == ChoreographyTerminal && prior.TaskOID == event.TaskOID &&
			prior.Attempt == event.Attempt && prior.ResultReceiptOID != choreographyTaskPackageSettlementNoIdentity &&
			prior.PackageOutcome == choreographyTaskPackageSettlementNoIdentity {
			found, succeeded = choreographyTaskPackageSettlementFound, prior.Succeeded
		}
		if prior.Kind == ChoreographyTaskPackageSettled {
			settled = choreographyTaskPackageSettlementFound
		}
	}
	if !found || settled {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	if err := validateChoreographyTaskPackageSettlementOutcome(spec, succeeded,
		ChoreographyTaskPackageSettlement{TaskID: event.Task, TerminalOID: event.TaskOID,
			PackageOutcome:          event.PackageOutcome,
			OutputAdmissionRejected: event.OutputAdmissionRejected}); err != nil {
		return err
	}
	if err := validateTaskPackageSettlementOrder(choreographyTaskPackageSettlementCheck{
		Task: spec, Outcome: event.PackageOutcome, Succeeded: succeeded,
		PriorOutcome: g.packageState[spec.Package]}); err != nil {
		return err
	}
	tip, err := g.opts.Mailbox.readRemoteTip(ctx, spec.Branch)
	if err != nil {
		return err
	}
	if tip != event.TaskOID {
		return fmt.Errorf(choreographyTaskPackageSettlementStateError)
	}
	return nil
}
