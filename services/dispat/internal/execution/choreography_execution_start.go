// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"time"
)

const (
	choreographyExecutionNotStartedError        = "execution: global gating hooks have not authorized compiled task claims"
	choreographyInvalidExecutionStartError      = "execution: signed execution start differs from the pinned admission duration or complete graph root"
	choreographyExecutionStartUnavailableError  = "execution: first-node broker has no signed execution-start persistence"
	choreographyExecutionStartStoppedError      = "execution: execution cannot start after run stopping"
	choreographyExecutionStartMissingIdentity   = ""
	choreographyExecutionStartIdentityPrefix    = "execution-start:"
	choreographyExecutionStartNoDurationSeconds = 0
	choreographyExecutionStartMaximumUTCYear    = 9999
	choreographyExecutionStartAccepted          = true
)

func choreographyExecutionDeadline(policy ChoreographyJoinPolicy, startedUTC string) (string, error) {
	if policy.AdmissionDurationSeconds <= choreographyExecutionStartNoDurationSeconds || policy.AdmissionDurationSeconds > maxChoreographyAdmissionSeconds {
		return choreographyExecutionStartMissingIdentity, fmt.Errorf(joinPolicyDurationErrorMessage)
	}
	started, err := time.Parse(choreographyAdmissionTimeFormat, startedUTC)
	if err != nil || started.IsZero() || started.Location() != time.UTC ||
		started.Format(choreographyAdmissionTimeFormat) != startedUTC {
		return choreographyExecutionStartMissingIdentity, fmt.Errorf(choreographyInvalidExecutionStartError)
	}
	deadline := started.Add(time.Duration(policy.AdmissionDurationSeconds) * time.Second)
	if !deadline.After(started) || deadline.Year() > choreographyExecutionStartMaximumUTCYear {
		return choreographyExecutionStartMissingIdentity, fmt.Errorf(choreographyInvalidExecutionStartError)
	}
	return deadline.Format(choreographyAdmissionTimeFormat), nil
}

func (g *GitChoreographyPersist) PersistExecutionStart(ctx context.Context) (ChoreographyEventCursor, string, error) {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, err
	}
	if _, err := g.LedgerHead(ctx); err != nil {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, err
	}
	g.mu.Lock()
	if g.executionStarted {
		for _, event := range g.events {
			if event.Kind == ChoreographyExecutionStarted {
				cursor := ChoreographyEventCursor{OID: event.LedgerOID, Sequence: event.Sequence}
				deadline := g.admissionDeadlineUTC
				g.mu.Unlock()
				return cursor, deadline, nil
			}
		}
	}
	stopping := g.stopping || g.sealed
	g.mu.Unlock()
	if stopping {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, fmt.Errorf(choreographyExecutionStartStoppedError)
	}
	startedUTC := time.Now().UTC().Format(choreographyAdmissionTimeFormat)
	deadlineUTC, err := choreographyExecutionDeadline(g.opts.JoinPolicy, startedUTC)
	if err != nil {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, err
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, err
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: choreographyExecutionStartIdentityPrefix + g.opts.GraphRootOID, ExpectedLedgerOID: head.OID,
		NewTaskOID: g.opts.GraphRootOID,
		Event: ChoreographyEvent{Kind: ChoreographyExecutionStarted,
			Task: choreographyRunTaskID, TaskOID: g.opts.GraphRootOID,
			GraphRootOID: g.opts.GraphRootOID, ExecutionStartedUTC: startedUTC,
			AdmissionDeadlineUTC: deadlineUTC},
	}); err != nil {
		return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, event := range g.events {
		if event.Kind == ChoreographyExecutionStarted {
			return ChoreographyEventCursor{OID: event.LedgerOID, Sequence: event.Sequence},
				event.AdmissionDeadlineUTC, nil
		}
	}
	return ChoreographyEventCursor{}, choreographyExecutionStartMissingIdentity, fmt.Errorf(choreographyInvalidExecutionStartError)
}

// StartExecution is called only after every global gating hook succeeds. The
// signed event is the peer-visible release of the complete inert graph.
func (b *FirstNodeBroker) StartExecution(ctx context.Context) (ChoreographyEventCursor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.executionStarted {
		return b.persist.LedgerHead(ctx)
	}
	if b.stopping || b.sealed {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyExecutionStartStoppedError)
	}
	persist, ok := b.persist.(interface {
		PersistExecutionStart(context.Context) (ChoreographyEventCursor, string, error)
	})
	if !ok {
		return ChoreographyEventCursor{}, fmt.Errorf(choreographyExecutionStartUnavailableError)
	}
	cursor, deadline, err := persist.PersistExecutionStart(ctx)
	if err != nil {
		return ChoreographyEventCursor{}, err
	}
	b.executionStarted, b.admissionDeadlineUTC = choreographyExecutionStartAccepted, deadline
	b.wakeBudgetWaiters()
	return cursor, nil
}
