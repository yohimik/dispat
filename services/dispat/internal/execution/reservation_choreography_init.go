// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"time"
)

const (
	choreographyReservationInitMissingIdentity  = ""
	choreographyInitFirstAttempt                = 1
	choreographyInitTaskSeen                    = true
	choreographyInitStarted                     = true
	choreographyInitSlotsReady                  = true
	choreographyInitPreflightRequiredError      = "execution: choreography task initialization requires preflight"
	choreographyInitDuplicateTaskError          = "execution: duplicate or empty choreography task"
	choreographyInitNoncommandBranchErrorFormat = "execution: noncommand task %s has a branch"
	choreographyInitInvalidTaskErrorFormat      = "execution: invalid choreography task %s"
	choreographyInitDuplicateIndexError         = "execution: duplicate choreography participation index"
)

// ChoreographyInitialization names the retained bootstrap and the exact
// independently compiled task shapes. Only branch-bearing tasks get refs;
// virtual gates and first-only native effects use the signed ledger instead.
type ChoreographyInitialization struct {
	BootstrapOID        string
	Tasks               []ChoreographyTaskSpec
	Templates           []ChoreographyCompiledTemplate
	PackagePolicies     []ChoreographyPackagePolicy
	SemanticGraphDigest string
	Budgets             []ChoreographyRuntimeBudget
	// OnPreparedLedger must durably record the exact root before any remote
	// create, so a crash during the initial batch can resume or clean it.
	OnPreparedLedger func(ChoreographyLedgerReservation) error
}

type InitializedChoreography struct {
	Ledger                    ChoreographyLedgerReservation
	InitializationCompleteOID string
	Tasks                     []ChoreographyTaskSpec
	Templates                 []ChoreographyCompiledTemplate
	PackagePolicies           []ChoreographyPackagePolicy
}

// InitializeChoreographyTasks precreates one task ref per logical command
// and one ledger ref in bounded atomic batches before any task frame runs.
// Every worker must read the same canonical endpoint. A partial
// create leaves this run unavailable; Close cleans the refs it can prove.
func (c *Coordinator) InitializeChoreographyTasks(ctx context.Context,
	opts ChoreographyInitialization) (InitializedChoreography, error) {
	if c == nil || c.Pool == nil || c.choreographyInitialized ||
		c.reservations != nil || opts.BootstrapOID == choreographyReservationInitMissingIdentity ||
		c.BootstrapOID != opts.BootstrapOID || c.Run == choreographyReservationInitMissingIdentity ||
		c.Generation == choreographyReservationInitMissingIdentity || c.PlanDigest == choreographyReservationInitMissingIdentity || c.GraphDigest == choreographyReservationInitMissingIdentity ||
		c.canonicalMailbox == nil || c.CanonicalEndpoint == choreographyReservationInitMissingIdentity {
		return InitializedChoreography{}, fmt.Errorf(choreographyInitPreflightRequiredError)
	}
	mailbox := c.canonicalMailbox
	result := InitializedChoreography{Tasks: append([]ChoreographyTaskSpec(nil), opts.Tasks...),
		Templates:       append([]ChoreographyCompiledTemplate(nil), opts.Templates...),
		PackagePolicies: append([]ChoreographyPackagePolicy(nil), opts.PackagePolicies...)}
	binding := ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest}
	ledger, err := PrepareChoreographyLedgerRoot(ctx, ChoreographyLedgerRootOptions{
		Mailbox: mailbox, Binding: binding, BootstrapOID: opts.BootstrapOID,
		Roster: ChoreographyRuntimeRoster{
			Nodes: c.Pool.choreographyRosterNodes(), Budgets: opts.Budgets,
		},
	})
	if err != nil {
		return InitializedChoreography{}, err
	}
	result.Ledger = ledger
	if opts.OnPreparedLedger != nil {
		if err := opts.OnPreparedLedger(ledger); err != nil {
			return InitializedChoreography{}, err
		}
	}
	batch := &endpointBatch{mailbox: mailbox, nodes: []string{c.Local.Name},
		refs: map[string]string{ledger.Branch: ledger.OID}}
	slots := &reservationState{free: make(map[string][]*dispatchSlot)}
	seen := map[string]bool{}
	for index := range result.Tasks {
		task := &result.Tasks[index]
		if task.Task == choreographyReservationInitMissingIdentity || seen[task.Task] {
			return InitializedChoreography{}, fmt.Errorf(choreographyInitDuplicateTaskError)
		}
		seen[task.Task] = choreographyInitTaskSeen
		if task.Virtual {
			if task.LocalAuthority || task.Branch != choreographyReservationInitMissingIdentity || task.ReservationOID != choreographyReservationInitMissingIdentity {
				return InitializedChoreography{}, fmt.Errorf(choreographyInitNoncommandBranchErrorFormat, task.Task)
			}
			continue
		}
		branch := FormatChoreographyTaskBranch(c.Run, task.Task)
		if task.Kind == choreographyReservationInitMissingIdentity || task.ReservationOID != choreographyReservationInitMissingIdentity ||
			task.Branch != choreographyReservationInitMissingIdentity && task.Branch != branch || batch.refs[branch] != choreographyReservationInitMissingIdentity {
			return InitializedChoreography{}, fmt.Errorf(choreographyInitInvalidTaskErrorFormat, task.Task)
		}
		task.Branch = branch
		reservation := &Reservation{Header: Header{
			Protocol: ChoreographyProtocolVersion, Kind: task.Kind, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: task.Task, Attempt: choreographyInitFirstAttempt,
			Generation: c.Generation, Branch: branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		}, BootstrapOID: opts.BootstrapOID, MaxAttempts: task.EffectiveMaxAttempts()}
		oid, err := mailbox.PrepareChoreographyReservation(ctx, reservation)
		if err != nil {
			return InitializedChoreography{}, err
		}
		task.ReservationOID = oid
		batch.refs[branch] = oid
	}
	// Compiled pickup no longer needs an orchestrator-written offer slot.
	for _, authorized := range c.JoinPolicy.Authorized {
		slot, err := PrepareChoreographySlotRoot(ctx, ChoreographySlotRootOptions{
			Mailbox: mailbox, Binding: binding, BootstrapOID: opts.BootstrapOID,
			Node: authorized.Name, Index: choreographyParticipationSlotIndex,
		})
		if err != nil {
			return InitializedChoreography{}, err
		}
		if batch.refs[slot.branch] != choreographyReservationInitMissingIdentity {
			return InitializedChoreography{}, fmt.Errorf(choreographyInitDuplicateIndexError)
		}
		batch.refs[slot.branch] = slot.oid
		slots.free[authorized.Name] = []*dispatchSlot{&slot}
	}
	// Mark this before the first network mutation so a failed or ambiguous
	// batch cannot be started a second time under a new root identity.
	c.choreographyInitialized = choreographyInitStarted
	c.choreographySlots = slots
	if err := c.createInitialRefs(ctx, c.CanonicalEndpoint, batch); err != nil {
		return InitializedChoreography{}, err
	}
	root, err := c.PublishChoreographyGraphRoot(ctx, ChoreographyGraphRootPublication{
		Tasks: result.Tasks, Templates: result.Templates,
		PackagePolicies: result.PackagePolicies, SemanticGraphDigest: opts.SemanticGraphDigest})
	if err != nil {
		return InitializedChoreography{}, err
	}
	c.GraphRootOID = root
	completeOID, err := c.CompleteChoreographyInitialization(ctx, ledger, root)
	if err != nil {
		return InitializedChoreography{}, err
	}
	result.InitializationCompleteOID = completeOID
	if err := c.ActivateChoreographyGraphNotices(ctx, root); err != nil {
		return InitializedChoreography{}, err
	}
	slots.ready = choreographyInitSlotsReady
	return result, nil
}
