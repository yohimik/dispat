// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"sort"
)

const (
	choreographyTaskWaitMissingIdentity          = ""
	choreographyTaskWaitPreviousEventOffset      = 1
	choreographyTaskWaitNextSequenceOffset       = 1
	choreographyTaskWaitNoEvents                 = 0
	choreographyTaskWaitIncompleteError          = "execution: incomplete task event wait"
	choreographyTaskWaitCursorOutsideError       = "execution: task wait cursor is outside the signed ledger"
	choreographyTaskWaitUnknownTaskError         = "execution: task event wait names no run task"
	choreographyTaskWaitNoIndexError             = "execution: first-node persistence lacks an indexed task event wait"
	choreographyTaskWaitIncompleteIntervalError  = "execution: incomplete task event interval"
	choreographyTaskWaitIntervalOutsideError     = "execution: task event interval is outside the signed ledger"
	choreographyTaskWaitUnknownIntervalTaskError = "execution: task event interval names no run task"
	choreographyTaskWaitNoIntervalIndexError     = "execution: first-node persistence lacks an indexed task event interval"
)

// indexTaskEventLocked extends the in-memory per-task view at the same point
// the signed ledger event becomes durable. The complete ordered feed remains
// available to remote peers; local task waiters inspect only their own events.
func (g *GitChoreographyPersist) indexTaskEventLocked(event ChoreographyEvent) {
	if g.taskEvents == nil {
		g.taskEvents = make(map[string][]int)
		for position, prior := range g.events {
			g.taskEvents[prior.Task] = append(g.taskEvents[prior.Task], position)
		}
	} else {
		g.taskEvents[event.Task] = append(g.taskEvents[event.Task], len(g.events)-choreographyTaskWaitPreviousEventOffset)
	}
	if g.taskNotify == nil {
		g.taskNotify = make(map[string]chan struct{})
	}
	if old := g.taskNotify[event.Task]; old != nil {
		close(old)
	}
	g.taskNotify[event.Task] = make(chan struct{})
}

// WaitTaskEvent waits for the next authenticated event for one task after an
// exact ledger cursor. One Git replay builds the index; successful local CAS
// updates it once and notifies only waiters for the changed task. No active
// local task pages or filters the global history independently.
func (g *GitChoreographyPersist) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	if g == nil || task == choreographyTaskWaitMissingIdentity || after.OID == choreographyTaskWaitMissingIdentity {
		return ChoreographyEvent{}, ChoreographyEventCursor{}, fmt.Errorf(choreographyTaskWaitIncompleteError)
	}
	for {
		g.mu.Lock()
		if err := g.syncLedgerLocked(ctx); err != nil {
			g.mu.Unlock()
			return ChoreographyEvent{}, ChoreographyEventCursor{}, err
		}
		sequence, found := g.eventIndex[after.OID]
		if !found || sequence != after.Sequence {
			g.mu.Unlock()
			return ChoreographyEvent{}, ChoreographyEventCursor{}, fmt.Errorf(choreographyTaskWaitCursorOutsideError)
		}
		if g.taskEvents == nil {
			g.taskEvents = make(map[string][]int)
			for position, event := range g.events {
				g.taskEvents[event.Task] = append(g.taskEvents[event.Task], position)
			}
		}
		positions := g.taskEvents[task]
		index := sort.Search(len(positions), func(i int) bool {
			return uint64(positions[i]) >= after.Sequence
		})
		if index < len(positions) {
			position := positions[index]
			event := g.events[position]
			cursor := ChoreographyEventCursor{OID: g.eventOIDs[position], Sequence: uint64(position + choreographyTaskWaitNextSequenceOffset)}
			g.mu.Unlock()
			return event, cursor, nil
		}
		if g.taskNotify == nil {
			g.taskNotify = make(map[string]chan struct{})
		}
		changed := g.taskNotify[task]
		if changed == nil {
			changed = make(chan struct{})
			g.taskNotify[task] = changed
		}
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ChoreographyEvent{}, ChoreographyEventCursor{}, ctx.Err()
		}
	}
}

func (b *FirstNodeBroker) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	if b == nil || b.tasks[task] == nil {
		return ChoreographyEvent{}, ChoreographyEventCursor{}, fmt.Errorf(choreographyTaskWaitUnknownTaskError)
	}
	indexed, ok := b.persist.(interface {
		WaitTaskEvent(context.Context, string, ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error)
	})
	if !ok {
		return ChoreographyEvent{}, ChoreographyEventCursor{}, fmt.Errorf(choreographyTaskWaitNoIndexError)
	}
	return indexed.WaitTaskEvent(ctx, task, after)
}

// TaskEventsThrough reads one task's signed events within two exact ledger
// cursors. It is used when stopping admissions: a lost claim reply must not
// let the first node release an offer that was durably claimed.
func (g *GitChoreographyPersist) TaskEventsThrough(ctx context.Context, task string,
	after, through ChoreographyEventCursor) ([]ChoreographyEvent, error) {
	if g == nil || task == choreographyTaskWaitMissingIdentity || after.OID == choreographyTaskWaitMissingIdentity || through.OID == choreographyTaskWaitMissingIdentity ||
		through.Sequence < after.Sequence {
		return nil, fmt.Errorf(choreographyTaskWaitIncompleteIntervalError)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return nil, err
	}
	afterSequence, afterFound := g.eventIndex[after.OID]
	throughSequence, throughFound := g.eventIndex[through.OID]
	if !afterFound || !throughFound ||
		afterSequence != after.Sequence || throughSequence != through.Sequence ||
		through.Sequence > g.sequence {
		return nil, fmt.Errorf(choreographyTaskWaitIntervalOutsideError)
	}
	if g.taskEvents == nil {
		g.taskEvents = make(map[string][]int)
		for position, event := range g.events {
			g.taskEvents[event.Task] = append(g.taskEvents[event.Task], position)
		}
	}
	positions := g.taskEvents[task]
	start := sort.Search(len(positions), func(i int) bool {
		return uint64(positions[i]) >= after.Sequence
	})
	events := make([]ChoreographyEvent, choreographyTaskWaitNoEvents)
	for _, position := range positions[start:] {
		if uint64(position+choreographyTaskWaitNextSequenceOffset) > through.Sequence {
			break
		}
		events = append(events, g.events[position])
	}
	return events, nil
}

func (b *FirstNodeBroker) TaskEventsThrough(ctx context.Context, task string,
	after, through ChoreographyEventCursor) ([]ChoreographyEvent, error) {
	if b == nil || b.tasks[task] == nil {
		return nil, fmt.Errorf(choreographyTaskWaitUnknownIntervalTaskError)
	}
	indexed, ok := b.persist.(interface {
		TaskEventsThrough(context.Context, string, ChoreographyEventCursor,
			ChoreographyEventCursor) ([]ChoreographyEvent, error)
	})
	if !ok {
		return nil, fmt.Errorf(choreographyTaskWaitNoIntervalIndexError)
	}
	return indexed.TaskEventsThrough(ctx, task, after, through)
}
