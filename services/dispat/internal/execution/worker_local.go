// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	firstLocalEventWaitInterval        = 2 * time.Second
	firstLocalNoIdentity               = ""
	firstLocalNoEvents                 = 0
	firstLocalFirstEventIncrement      = 1
	firstLocalMinimumWeight            = 1
	firstLocalSlotCapacity             = 1
	firstLocalPageSize                 = 32
	firstLocalImmediatePollInterval    = time.Duration(0)
	firstLocalAuthorityError           = "execution: first-local task lacks its exact signed authority"
	firstLocalMailboxError             = "execution: first-local mailbox cannot verify the offered assignment"
	firstLocalAssignmentErrorFormat    = "execution: authenticating first-local assignment: %w"
	firstLocalFrameError               = "execution: first-local assignment is not an executable v3 frame"
	firstLocalStageError               = "execution: first-local assignment names no configured stage"
	firstLocalAttemptSeenError         = "execution: first-local attempt was already started"
	firstLocalBrokerRunError           = "execution: first-local broker has a different signed run"
	firstLocalBootstrapReaderError     = "execution: first-local task has no authenticated bootstrap reader"
	firstLocalBootstrapReadErrorFormat = "execution: reading first-local task bootstrap: %w"
	firstLocalBootstrapMismatchError   = "execution: first-local task bootstrap differs from signed run"
	firstLocalUnsettledErrorFormat     = "execution: first-local task remains unsettled: %w"
	firstLocalReadTaskEventErrorFormat = "execution: reading exact first-local task finality: %w"
	firstLocalEventOrderError          = "execution: first-local task event is not signed and ordered"
	firstLocalDifferentClaimantError   = "execution: first-local finality names a different claimant"
	firstLocalBlockedError             = "execution: offered first-local task became blocked"
	firstLocalReadEventPageErrorFormat = "execution: reading first-local finality: %w"
	firstLocalPageOrderError           = "execution: first-local finality page is not contiguous"
	firstLocalEventGapError            = "execution: first-local finality has an event gap"
	firstLocalSealedError              = "execution: run sealed before first-local task settled"
	firstLocalReadTaskRefErrorFormat   = "execution: reading exact first-local task ref: %w"
)

// RunAssignedLocal runs one durable first-node offer through the same claim,
// result and cancellation path as a remote worker. The caller gives
// this invocation its own Worker; its cache is the canonical first-node object
// store and is never cleared or compacted here.
func (w *Worker) RunAssignedLocal(ctx context.Context, assignmentOID string,
	offerCursor ChoreographyEventCursor, broker *FirstNodeBroker) error {
	if w == nil || w.Mailbox == nil || w.Cache == nil || w.Seen == nil ||
		w.ReturnSigner == nil || w.NodeSlots == nil || w.Report.AuthorityID == firstLocalNoIdentity ||
		w.Report.AuthorityID != w.NodeSlots.AuthorityID() || assignmentOID == firstLocalNoIdentity ||
		w.Report.Limits.MaxManifestBytes <= firstLocalNoEvents || w.Report.Limits.MaxPackBytes <= firstLocalNoEvents ||
		offerCursor.OID == firstLocalNoIdentity || offerCursor.Sequence == firstLocalNoEvents {
		return fmt.Errorf(firstLocalAuthorityError)
	}
	carriers, ok := w.Mailbox.(slotMailboxx)
	if !ok || w.Report.Limits.MaxManifestBytes <= firstLocalNoEvents {
		return fmt.Errorf(firstLocalMailboxError)
	}
	view, err := carriers.OpenCarriedAssignment(ctx, assignmentOID, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return fmt.Errorf(firstLocalAssignmentErrorFormat, err)
	}
	assignment := view.Assignment
	if assignment.Protocol != ChoreographyProtocolVersion || assignment.Node != w.Node ||
		!isFrameKind(assignment.Kind) || assignment.Permits.Publish || assignment.Permits.NativeRefs ||
		assignment.Run == firstLocalNoIdentity || assignment.Task == firstLocalNoIdentity || assignment.Branch == firstLocalNoIdentity || assignment.Weight < firstLocalMinimumWeight {
		return fmt.Errorf(firstLocalFrameError)
	}
	if assignment.Kind == KindStage && !isStageName(assignment.Stage) {
		return fmt.Errorf(firstLocalStageError)
	}
	if w.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt) {
		return fmt.Errorf(firstLocalAttemptSeenError)
	}
	if broker == nil || broker.binding != (ChoreographyRunBinding{Run: assignment.Run,
		Generation: assignment.Generation, PlanDigest: assignment.PlanDigest,
		GraphDigest: assignment.GraphDigest}) {
		return fmt.Errorf(firstLocalBrokerRunError)
	}
	client, err := DirectChoreographyReturn(broker)
	if err != nil {
		return err
	}
	gitMailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok {
		return fmt.Errorf(firstLocalBootstrapReaderError)
	}
	bootstrap, err := gitMailbox.ReadChoreographyBootstrap(ctx, assignment.BootstrapOID,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil {
		return fmt.Errorf(firstLocalBootstrapReadErrorFormat, err)
	}
	if bootstrap.ChoreographyRunBinding != broker.binding ||
		bootstrap.CoordinationEndpoint != w.Endpoint {
		return fmt.Errorf(firstLocalBootstrapMismatchError)
	}
	channel := &workerReturnChannel{binding: broker.binding, endpoint: assignment.ReturnEndpoint,
		certSHA256: assignment.ReturnCertSHA256, bootstrapOID: assignment.BootstrapOID,
		bootstrap: &bootstrap, client: client}
	w.returnChannels = map[string]*workerReturnChannel{assignment.Run: channel}
	defer func() {
		w.running.Wait()
		w.closeReturnChannels()
	}()
	w.slots = make(chan struct{}, firstLocalSlotCapacity)
	cursor := offerCursor
	timer := time.NewTimer(firstLocalImmediatePollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf(firstLocalUnsettledErrorFormat, ctx.Err())
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
					return fmt.Errorf(firstLocalUnsettledErrorFormat, ctx.Err())
				}
				if !waitTimedOut && !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf(firstLocalReadTaskEventErrorFormat, err)
				}
				// A bounded wait expired: retry an exact pending proposal or
				// observe a Cancel without scanning the run-wide ledger.
				timer.Reset(firstLocalImmediatePollInterval)
				continue
			}
			if next.OID == firstLocalNoIdentity || next.Sequence <= cursor.Sequence || event.Sequence != next.Sequence ||
				event.Task != assignment.Task {
				return fmt.Errorf(firstLocalEventOrderError)
			}
			cursor = next
			if event.Attempt == assignment.Attempt {
				switch event.Kind {
				case ChoreographyTerminal, ChoreographyRetryReady:
					if event.Node != w.Node || event.TaskOID == firstLocalNoIdentity {
						return fmt.Errorf(firstLocalDifferentClaimantError)
					}
					return nil
				case ChoreographyBlocked:
					return fmt.Errorf(firstLocalBlockedError)
				}
			}
			timer.Reset(firstLocalImmediatePollInterval)
			continue
		}
		page, err := client.PollEvents(ctx, ChoreographyEventRequest{
			ChoreographyRunBinding: broker.binding, Node: w.Node, Cursor: cursor, MaxEvents: firstLocalPageSize})
		if err != nil {
			return fmt.Errorf(firstLocalReadEventPageErrorFormat, err)
		}
		if page.Cursor.OID == firstLocalNoIdentity || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
			(len(page.Events) == firstLocalNoEvents && page.Cursor.OID != cursor.OID) {
			return fmt.Errorf(firstLocalPageOrderError)
		}
		for index, event := range page.Events {
			if event.Sequence != cursor.Sequence+uint64(index)+firstLocalFirstEventIncrement {
				return fmt.Errorf(firstLocalEventGapError)
			}
			if event.Task == assignment.Task && event.Attempt == assignment.Attempt {
				switch event.Kind {
				case ChoreographyTerminal, ChoreographyRetryReady:
					if event.Node != w.Node || event.TaskOID == firstLocalNoIdentity {
						return fmt.Errorf(firstLocalDifferentClaimantError)
					}
					return nil
				case ChoreographyBlocked:
					return fmt.Errorf(firstLocalBlockedError)
				}
			}
			if event.Kind == ChoreographyRunSealed {
				return fmt.Errorf(firstLocalSealedError)
			}
		}
		cursor = page.Cursor
		if page.More {
			timer.Reset(firstLocalImmediatePollInterval)
		} else {
			timer.Reset(minimumPollInterval)
		}
	}
}

func (w *Worker) inspectAssignedLocalRef(ctx context.Context, assignmentOID string,
	assignment Assignment) error {
	heads, err := w.Mailbox.ObserveManyExact(ctx, []string{assignment.Branch})
	if err != nil {
		return fmt.Errorf(firstLocalReadTaskRefErrorFormat, err)
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
