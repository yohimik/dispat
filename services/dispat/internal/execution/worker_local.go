// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const firstLocalEventWaitInterval = 2 * time.Second

// RunAssignedLocal runs one durable first-node offer through the same claim,
// result and cancellation path as a remote worker. The caller gives
// this invocation its own Worker; its cache is the canonical first-node object
// store and is never cleared or compacted here.
func (w *Worker) RunAssignedLocal(ctx context.Context, assignmentOID string,
	offerCursor ChoreographyEventCursor, broker *FirstNodeBroker) error {
	if w == nil || w.Mailbox == nil || w.Cache == nil || w.Seen == nil ||
		w.ReturnSigner == nil || w.NodeSlots == nil || w.Report.AuthorityID == "" ||
		w.Report.AuthorityID != w.NodeSlots.AuthorityID() || assignmentOID == "" ||
		w.Report.Limits.MaxManifestBytes <= 0 || w.Report.Limits.MaxPackBytes <= 0 ||
		offerCursor.OID == "" || offerCursor.Sequence == 0 {
		return fmt.Errorf("execution: first-local task lacks its exact signed authority")
	}
	carriers, ok := w.Mailbox.(slotMailboxx)
	if !ok || w.Report.Limits.MaxManifestBytes <= 0 {
		return fmt.Errorf("execution: first-local mailbox cannot verify the offered assignment")
	}
	view, err := carriers.OpenCarriedAssignment(ctx, assignmentOID, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return fmt.Errorf("execution: authenticating first-local assignment: %w", err)
	}
	assignment := view.Assignment
	if assignment.Protocol != ChoreographyProtocolVersion || assignment.Node != w.Node ||
		!isFrameKind(assignment.Kind) || assignment.Permits.Publish || assignment.Permits.NativeRefs ||
		assignment.Run == "" || assignment.Task == "" || assignment.Branch == "" || assignment.Weight < 1 {
		return fmt.Errorf("execution: first-local assignment is not an executable v3 frame")
	}
	if assignment.Kind == KindStage && !isStageName(assignment.Stage) {
		return fmt.Errorf("execution: first-local assignment names no configured stage")
	}
	if w.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt) {
		return fmt.Errorf("execution: first-local attempt was already started")
	}
	if broker == nil || broker.binding != (ChoreographyRunBinding{Run: assignment.Run,
		Generation: assignment.Generation, PlanDigest: assignment.PlanDigest,
		GraphDigest: assignment.GraphDigest}) {
		return fmt.Errorf("execution: first-local broker has a different signed run")
	}
	client, err := DirectChoreographyReturn(broker)
	if err != nil {
		return err
	}
	gitMailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok {
		return fmt.Errorf("execution: first-local task has no authenticated bootstrap reader")
	}
	bootstrap, err := gitMailbox.ReadChoreographyBootstrap(ctx, assignment.BootstrapOID,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil || bootstrap.ChoreographyRunBinding != broker.binding ||
		bootstrap.CoordinationEndpoint != w.Endpoint {
		return fmt.Errorf("execution: first-local task bootstrap differs from signed run: %w", err)
	}
	channel := &workerReturnChannel{binding: broker.binding, endpoint: assignment.ReturnEndpoint,
		certSHA256: assignment.ReturnCertSHA256, bootstrapOID: assignment.BootstrapOID,
		bootstrap: &bootstrap, client: client}
	w.returnChannels = map[string]*workerReturnChannel{assignment.Run: channel}
	defer func() {
		w.running.Wait()
		w.closeReturnChannels()
	}()
	w.slots = make(chan struct{}, 1)
	cursor := offerCursor
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("execution: first-local task remains unsettled: %w", ctx.Err())
		case <-timer.C:
		}
		w.settlePendingReturnClaims(ctx)
		w.pollParkedReturnClaims(ctx)
		if err := w.inspectAssignedLocalRef(ctx, assignmentOID, assignment); err != nil {
			return err
		}
		if waiter, ok := client.(workerTaskEventWaiter); ok {
			waitCtx, stopWait := context.WithTimeout(ctx, firstLocalEventWaitInterval)
			event, next, err := waiter.WaitTaskEvent(waitCtx, assignment.Task, cursor)
			// A deadline can terminate a Git read while the persistence index
			// refreshes. Its wrapped process error need not be DeadlineExceeded.
			waitTimedOut := errors.Is(waitCtx.Err(), context.DeadlineExceeded)
			stopWait()
			if err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("execution: first-local task remains unsettled: %w", ctx.Err())
				}
				if !waitTimedOut && !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("execution: reading exact first-local task finality: %w", err)
				}
				// A bounded wait expired: retry an exact pending proposal or
				// observe a Cancel without scanning the run-wide ledger.
				timer.Reset(0)
				continue
			}
			if next.OID == "" || next.Sequence <= cursor.Sequence || event.Sequence != next.Sequence ||
				event.Task != assignment.Task {
				return fmt.Errorf("execution: first-local task event is not signed and ordered")
			}
			cursor = next
			if event.Attempt == assignment.Attempt {
				switch event.Kind {
				case ChoreographyTerminal, ChoreographyRetryReady:
					if event.Node != w.Node || event.TaskOID == "" {
						return fmt.Errorf("execution: first-local finality names a different claimant")
					}
					return nil
				case ChoreographyBlocked:
					return fmt.Errorf("execution: offered first-local task became blocked")
				}
			}
			timer.Reset(0)
			continue
		}
		page, err := client.PollEvents(ctx, ChoreographyEventRequest{
			ChoreographyRunBinding: broker.binding, Node: w.Node, Cursor: cursor, MaxEvents: 32})
		if err != nil {
			return fmt.Errorf("execution: reading first-local finality: %w", err)
		}
		if page.Cursor.OID == "" || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
			(len(page.Events) == 0 && page.Cursor.OID != cursor.OID) {
			return fmt.Errorf("execution: first-local finality page is not contiguous")
		}
		for index, event := range page.Events {
			if event.Sequence != cursor.Sequence+uint64(index)+1 {
				return fmt.Errorf("execution: first-local finality has an event gap")
			}
			if event.Task == assignment.Task && event.Attempt == assignment.Attempt {
				switch event.Kind {
				case ChoreographyTerminal, ChoreographyRetryReady:
					if event.Node != w.Node || event.TaskOID == "" {
						return fmt.Errorf("execution: first-local finality names a different claimant")
					}
					return nil
				case ChoreographyBlocked:
					return fmt.Errorf("execution: offered first-local task became blocked")
				}
			}
			if event.Kind == ChoreographyRunSealed {
				return fmt.Errorf("execution: run sealed before first-local task settled")
			}
		}
		cursor = page.Cursor
		if page.More {
			timer.Reset(0)
		} else {
			timer.Reset(minimumPollInterval)
		}
	}
}

func (w *Worker) inspectAssignedLocalRef(ctx context.Context, assignmentOID string,
	assignment Assignment) error {
	heads, err := w.Mailbox.ObserveManyExact(ctx, []string{assignment.Branch})
	if err != nil {
		return fmt.Errorf("execution: reading exact first-local task ref: %w", err)
	}
	for _, head := range heads {
		tip, err := w.Mailbox.Inspect(ctx, head)
		if err != nil {
			return err
		}
		if tip.Kind == MessageCancel {
			w.observeWithdrawal(ctx, tip)
			continue
		}
		if tip.Kind != MessageAssignment || tip.OID != assignmentOID ||
			w.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt) {
			continue
		}
		if _, err := w.takeTask(ctx, tip, assignment); err != nil {
			return err
		}
	}
	return nil
}
