// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	choreographyNoClaimKind                           MessageKind = "choreography-no-claim-expired"
	choreographyNoAdmittedWorkerCause                             = "no-admitted-worker"
	choreographyNoCompatibleAdmittedNodeCause                     = "no-compatible-admitted-node"
	choreographyNoClaimStateError                                 = "execution: no-claim deadline cannot terminalize a noninert or eligible task"
	choreographyNoClaimPolicyError                                = "execution: no-claim expiry differs from fixed admission policy"
	choreographyNoClaimPersistError                               = "execution: no-claim expiry has no canonical task persistence"
	choreographyNoClaimNoIdentity                                 = ""
	choreographyNoClaimPackageFailed                              = "failed"
	choreographyNoClaimPackageSettlementPrefix                    = "no-claim-package:"
	choreographyNoClaimPackageSettlementSeparator                 = ":"
	choreographyNoClaimNoRemainingEdges                           = 0
	choreographyNoClaimParentCount                                = 1
	choreographyNoClaimFirstParentIndex                           = 0
	choreographyNoClaimTaskTerminal                               = true
	choreographyNoClaimTaskFailed                                 = false
	choreographyNoClaimSettlementPending                          = true
	choreographyNoClaimSettlementComplete                         = false
	choreographyNoClaimNotExpired                                 = false
	choreographyNoClaimEventMissing                               = false
	choreographyNoClaimEventFound                                 = true
	choreographyNoClaimSettlementMissing                          = false
	choreographyNoClaimSettlementFound                            = true
	choreographyNoClaimSettlementOutcomeMismatchError             = "execution: no-claim package settlement differs from exact signed record"
)

func choreographyNoClaimCause(placement Placement) string {
	if placement == PlacementWorker {
		return choreographyNoAdmittedWorkerCause
	}
	return choreographyNoCompatibleAdmittedNodeCause
}

func isChoreographyNoClaimPlacement(placement Placement) bool {
	return placement == PlacementWorker || placement == PlacementOrchestrator ||
		placement == PlacementAnyNode
}

// ChoreographyNoClaimOutcome is an inert canonical task terminal. It carries
// no command Result, because no node ever acquired execution authority.
type ChoreographyNoClaimOutcome struct {
	ChoreographyRunBinding
	GraphRootOID          string `json:"graphRootOid"`
	Task                  string `json:"task"`
	Attempt               int    `json:"attempt"`
	Branch                string `json:"branch"`
	ExpectedOID           string `json:"expectedOid"`
	Package               string `json:"package,omitempty"`
	MissingPlacement      string `json:"missingPlacement"`
	AdmissionDeadlineUTC  string `json:"admissionDeadlineUtc"`
	AdmissionPolicyDigest string `json:"admissionPolicyDigest"`
}

func digestChoreographyAdmissionPolicy(policy ChoreographyJoinPolicy) (string, error) {
	document, err := json.Marshal(policy)
	if err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:]), nil
}

func (b *FirstNodeBroker) ExpireUnclaimed(ctx context.Context, taskID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, b.admissionDeadlineUTC)
	if err != nil {
		return fmt.Errorf(choreographyNoClaimPolicyError)
	}
	task := b.tasks[taskID]
	if !b.executionStarted || b.stopping || b.sealed || task == nil || task.spec.Virtual ||
		!isChoreographyNoClaimPlacement(task.spec.Placement) || task.spec.LocalAuthority ||
		task.remaining != choreographyNoClaimNoRemainingEdges ||
		task.terminal || task.claim != choreographyNoClaimNoIdentity || task.offeredNode != choreographyNoClaimNoIdentity || task.result != choreographyNoClaimNoIdentity ||
		task.queueFence != choreographyNoClaimNoIdentity || task.attempt != choreographyNoClaimFirstAttempt || task.tip != task.spec.ReservationOID ||
		time.Now().Before(deadline) {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	for node := range b.members {
		if b.nodeEligible(task.spec, node) {
			return fmt.Errorf(choreographyNoClaimStateError)
		}
	}
	policyDigest, err := digestChoreographyAdmissionPolicy(b.joinPolicy)
	if err != nil {
		return err
	}
	outcome := ChoreographyNoClaimOutcome{ChoreographyRunBinding: b.binding,
		GraphRootOID: b.graphRootOID, Task: taskID, Attempt: task.attempt,
		Branch: task.spec.Branch, ExpectedOID: task.tip, Package: task.spec.Package,
		MissingPlacement:      string(task.spec.Placement),
		AdmissionDeadlineUTC:  b.admissionDeadlineUTC,
		AdmissionPolicyDigest: policyDigest}
	persist, ok := b.persist.(interface {
		PersistNoClaim(context.Context, ChoreographyNoClaimOutcome) (string, error)
	})
	if !ok {
		return fmt.Errorf(choreographyNoClaimPersistError)
	}
	oid, err := persist.PersistNoClaim(ctx, outcome)
	if err != nil {
		return err
	}
	task.tip, task.terminal, task.succeeded = oid, choreographyNoClaimTaskTerminal, choreographyNoClaimTaskFailed
	b.wakeBudgetWaiters()
	if task.spec.Package != choreographyNoClaimNoIdentity && task.spec.Kind != string(KindPrepare) {
		task.pendingNoClaim = choreographyNoClaimSettlementPending
	} else {
		b.discharge(task)
	}
	return nil
}

func (g *GitChoreographyPersist) PersistNoClaim(ctx context.Context,
	outcome ChoreographyNoClaimOutcome) (string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	if _, err := g.LedgerHead(ctx); err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	g.mu.Lock()
	deadlineUTC, started := g.admissionDeadlineUTC, g.executionStarted
	g.mu.Unlock()
	spec := g.tasks[outcome.Task]
	if spec.Task == choreographyNoClaimNoIdentity ||
		!isChoreographyNoClaimPlacement(spec.Placement) || spec.LocalAuthority ||
		outcome.ChoreographyRunBinding != g.opts.Binding ||
		outcome.GraphRootOID != g.opts.GraphRootOID ||
		outcome.Branch != spec.Branch || outcome.ExpectedOID != spec.ReservationOID ||
		outcome.Attempt != choreographyNoClaimFirstAttempt || outcome.Package != spec.Package ||
		outcome.MissingPlacement != string(spec.Placement) ||
		outcome.AdmissionDeadlineUTC != deadlineUTC || !started {
		return choreographyNoClaimNoIdentity, fmt.Errorf(choreographyNoClaimStateError)
	}
	digest, err := digestChoreographyAdmissionPolicy(g.opts.JoinPolicy)
	if err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	if outcome.AdmissionPolicyDigest != digest {
		return choreographyNoClaimNoIdentity, fmt.Errorf(choreographyNoClaimPolicyError)
	}
	document, err := json.Marshal(outcome)
	if err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	sum := sha256.Sum256(document)
	identity := hex.EncodeToString(sum[:])
	oid := g.pendingTaskOID(identity)
	if oid == choreographyNoClaimNoIdentity {
		oid, err = g.opts.Mailbox.commitMessage(ctx, choreographyNoClaimKind,
			document, []string{outcome.ExpectedOID}, nil)
		if err != nil {
			return choreographyNoClaimNoIdentity, err
		}
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{Identity: identity,
		TaskBranch: spec.Branch, OldTaskOID: outcome.ExpectedOID, NewTaskOID: oid,
		Event: ChoreographyEvent{Kind: ChoreographyNoClaimExpired,
			Task: outcome.Task, Attempt: outcome.Attempt, TaskOID: oid,
			PriorTaskOID: outcome.ExpectedOID, GraphRootOID: outcome.GraphRootOID,
			Package:               outcome.Package,
			BlockedBy:             choreographyNoClaimCause(spec.Placement),
			MissingPlacement:      outcome.MissingPlacement,
			AdmissionDeadlineUTC:  outcome.AdmissionDeadlineUTC,
			AdmissionPolicyDigest: outcome.AdmissionPolicyDigest},
	}); err != nil {
		return choreographyNoClaimNoIdentity, err
	}
	return oid, nil
}

func (g *GitChoreographyPersist) verifyNoClaimEvent(ctx context.Context,
	event ChoreographyEvent) error {
	spec := g.tasks[event.Task]
	if spec.Task == choreographyNoClaimNoIdentity ||
		!isChoreographyNoClaimPlacement(spec.Placement) || spec.LocalAuthority ||
		event.GraphRootOID != g.opts.GraphRootOID ||
		event.PriorTaskOID != spec.ReservationOID ||
		event.MissingPlacement != string(spec.Placement) ||
		event.BlockedBy != choreographyNoClaimCause(spec.Placement) {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	parents, err := g.opts.Mailbox.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if len(parents) != choreographyNoClaimParentCount || parents[choreographyNoClaimFirstParentIndex] != spec.ReservationOID {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	tree, err := g.opts.Mailbox.readTree(ctx, event.TaskOID)
	if err != nil {
		return err
	}
	if tree.kind != choreographyNoClaimKind {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	document, err := g.opts.Mailbox.Read(ctx, ChainTip{OID: event.TaskOID,
		Kind: tree.kind, document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var outcome ChoreographyNoClaimOutcome
	if err := strictJSON(document, &outcome); err != nil {
		return err
	}
	digest, err := digestChoreographyAdmissionPolicy(g.opts.JoinPolicy)
	if err != nil {
		return err
	}
	if outcome.ChoreographyRunBinding != g.opts.Binding ||
		outcome.GraphRootOID != g.opts.GraphRootOID || outcome.Task != event.Task ||
		outcome.Branch != spec.Branch || outcome.ExpectedOID != spec.ReservationOID ||
		outcome.Attempt != event.Attempt || outcome.Package != event.Package ||
		outcome.MissingPlacement != event.MissingPlacement ||
		outcome.AdmissionDeadlineUTC != event.AdmissionDeadlineUTC ||
		outcome.AdmissionPolicyDigest != event.AdmissionPolicyDigest ||
		outcome.AdmissionPolicyDigest != digest {
		return fmt.Errorf(choreographyNoClaimPolicyError)
	}
	return nil
}

// SettleNoClaimFailure is called after the release executor has drained all
// same-package siblings and completed onFail/revert. Only then can the failed
// package outcome discharge graph edges.
func (b *FirstNodeBroker) SettleNoClaimFailure(ctx context.Context, taskID, packageOutcome string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	if task == nil || !task.terminal ||
		task.spec.Package == choreographyNoClaimNoIdentity || task.tip == choreographyNoClaimNoIdentity {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	if lookup, ok := b.persist.(interface {
		LookupNoClaimPackageSettlement(context.Context, string, string) (string, bool, error)
	}); ok {
		settledOutcome, found, err := lookup.LookupNoClaimPackageSettlement(ctx, taskID, task.tip)
		if err != nil {
			return err
		}
		if found {
			if settledOutcome != packageOutcome {
				return fmt.Errorf(choreographyNoClaimSettlementOutcomeMismatchError)
			}
			if task.pendingNoClaim {
				return b.completeNoClaimPackageSettlement(task, packageOutcome)
			}
			return nil
		}
	}
	if !task.pendingNoClaim {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	if packageOutcome == choreographyNoClaimPackageFailed {
		if b.packageState[task.spec.Package] != choreographyNoClaimNoIdentity {
			return fmt.Errorf(choreographyNoClaimStateError)
		}
		if err := b.validatePackageOutcome(task.spec.Package, packageOutcome); err != nil {
			return err
		}
	} else if packageOutcome != choreographyNoClaimNoIdentity || b.packageState[task.spec.Package] != choreographyNoClaimPackageFailed {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	persist, ok := b.persist.(interface {
		PersistNoClaimPackageSettlement(context.Context, string, string, string) error
	})
	if !ok {
		return fmt.Errorf(choreographyNoClaimPersistError)
	}
	if err := persist.PersistNoClaimPackageSettlement(ctx, taskID, task.tip, packageOutcome); err != nil {
		return err
	}
	return b.completeNoClaimPackageSettlement(task, packageOutcome)
}

func (b *FirstNodeBroker) completeNoClaimPackageSettlement(task *indexedChoreographyTask,
	packageOutcome string) error {
	if err := b.applyPackageOutcome(task.spec.Package, packageOutcome); err != nil {
		return err
	}
	task.pendingNoClaim = choreographyNoClaimSettlementComplete
	b.discharge(task)
	return nil
}

func (g *GitChoreographyPersist) LookupNoClaimPackageSettlement(ctx context.Context,
	taskID, noClaimOID string) (string, bool, error) {
	if _, err := g.LedgerHead(ctx); err != nil {
		return choreographyNoClaimNoIdentity, choreographyNoClaimSettlementMissing, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, position := range g.taskEventPositionsLocked(taskID) {
		event := g.events[position]
		if event.Kind == ChoreographyNoClaimPackageSettled && event.TaskOID == noClaimOID {
			return event.PackageOutcome, choreographyNoClaimSettlementFound, nil
		}
	}
	return choreographyNoClaimNoIdentity, choreographyNoClaimSettlementMissing, nil
}

func (b *FirstNodeBroker) IsNoClaimExpired(taskID string) bool {
	if b == nil {
		return choreographyNoClaimNotExpired
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[taskID]
	return task != nil && task.pendingNoClaim
}

func (g *GitChoreographyPersist) PersistNoClaimPackageSettlement(ctx context.Context,
	taskID, noClaimOID, packageOutcome string) error {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	spec := g.tasks[taskID]
	if spec.Task == choreographyNoClaimNoIdentity || spec.Package == choreographyNoClaimNoIdentity || noClaimOID == choreographyNoClaimNoIdentity {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	found, settled := choreographyNoClaimEventMissing, choreographyNoClaimSettlementMissing
	priorPackage := g.packageState[spec.Package]
	for _, position := range g.taskEventPositionsLocked(taskID) {
		event := g.events[position]
		if event.Kind == ChoreographyNoClaimExpired && event.TaskOID == noClaimOID {
			found = choreographyNoClaimEventFound
		}
		if event.Kind == ChoreographyNoClaimPackageSettled {
			settled = choreographyNoClaimSettlementFound
		}
	}
	g.mu.Unlock()
	if !found || settled {
		if settled {
			outcome, known, lookupErr := g.LookupNoClaimPackageSettlement(ctx, taskID, noClaimOID)
			if lookupErr != nil {
				return lookupErr
			}
			if known && outcome == packageOutcome {
				return nil
			}
			return fmt.Errorf(choreographyNoClaimSettlementOutcomeMismatchError)
		}
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	if packageOutcome == choreographyNoClaimPackageFailed {
		if priorPackage != choreographyNoClaimNoIdentity {
			return fmt.Errorf(choreographyNoClaimStateError)
		}
	} else if packageOutcome != choreographyNoClaimNoIdentity || priorPackage != choreographyNoClaimPackageFailed {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity:          choreographyNoClaimPackageSettlementPrefix + noClaimOID + choreographyNoClaimPackageSettlementSeparator + packageOutcome,
		ExpectedLedgerOID: head.OID, NewTaskOID: noClaimOID,
		Event: ChoreographyEvent{Kind: ChoreographyNoClaimPackageSettled,
			Task: taskID, Attempt: choreographyNoClaimFirstAttempt, TaskOID: noClaimOID, PriorTaskOID: noClaimOID,
			GraphRootOID: g.opts.GraphRootOID, Package: spec.Package,
			PackageOutcome: packageOutcome, BlockedBy: choreographyNoClaimCause(spec.Placement)},
	}); err != nil {
		outcome, known, lookupErr := g.LookupNoClaimPackageSettlement(ctx, taskID, noClaimOID)
		if lookupErr == nil && known && outcome == packageOutcome {
			return nil
		}
		return err
	}
	return nil
}

func (g *GitChoreographyPersist) verifyNoClaimPackageSettlementEvent(ctx context.Context,
	event ChoreographyEvent) error {
	spec := g.tasks[event.Task]
	if spec.Task == choreographyNoClaimNoIdentity || spec.Package == choreographyNoClaimNoIdentity || event.Package != spec.Package ||
		(event.PackageOutcome != choreographyNoClaimPackageFailed && event.PackageOutcome != choreographyNoClaimNoIdentity) ||
		event.BlockedBy != choreographyNoClaimCause(spec.Placement) ||
		event.GraphRootOID != g.opts.GraphRootOID || event.TaskOID == choreographyNoClaimNoIdentity || event.Attempt != choreographyNoClaimFirstAttempt ||
		event.PriorTaskOID != event.TaskOID {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	found, settled := choreographyNoClaimEventMissing, choreographyNoClaimSettlementMissing
	for _, position := range g.taskEventPositionsLocked(event.Task) {
		previous := g.events[position]
		if previous.Kind == ChoreographyNoClaimExpired && previous.TaskOID == event.TaskOID {
			found = choreographyNoClaimEventFound
		}
		if previous.Kind == ChoreographyNoClaimPackageSettled {
			settled = choreographyNoClaimSettlementFound
		}
	}
	if !found || settled {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	priorPackage := g.packageState[spec.Package]
	if event.PackageOutcome == choreographyNoClaimPackageFailed && priorPackage != choreographyNoClaimNoIdentity ||
		event.PackageOutcome == choreographyNoClaimNoIdentity && priorPackage != choreographyNoClaimPackageFailed {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	tip, err := g.opts.Mailbox.readRemoteTip(ctx, spec.Branch)
	if err != nil {
		return err
	}
	if tip != event.TaskOID {
		return fmt.Errorf(choreographyNoClaimStateError)
	}
	return nil
}
