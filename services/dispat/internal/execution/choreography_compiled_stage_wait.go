// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	compiledStageWaitIdentityErrorFormat   = "execution: compiled task %q has no signed broker wait identity"
	compiledStageNoClaimErrorFormat        = "execution: compiled task %q expired without an admitted eligible worker"
	compiledStageBlockedErrorFormat        = "execution: compiled task %q was blocked by %q"
	compiledStageAckErrorFormat            = "execution: compiled task %q stopped after signed cancellation"
	compiledStageInertCancelledErrorFormat = "execution: compiled task %q was cancelled before claim after signed run stop"
	compiledStageRunStoppedErrorFormat     = "execution: compiled task %q stopped before settlement"
	compiledStageCancelErrorFormat         = "execution: compiled task %q has no signed stop acknowledgement after cancellation: %w"
	compiledStageDecisionUnresolvedFormat  = "execution: compiled task %q has an accepted effect but no verified signed task decision: %w"
	compiledStageAbsentIdentity            = ""
	compiledStageNoAttempt                 = 0
	compiledStageZeroTimeout               = 0
	compiledStageTaskEventBuffer           = 1
	compiledStageAdmissionWindowOpen       = false
	compiledStageAdmissionWindowClosed     = true
	compiledStageUnresolvedFinality        = true
	compiledStageResolvedFinality          = false
	compiledStageSignedTaskTerminal        = true
	compiledStageAcceptedResult            = true
	compiledStageAcceptedAck               = true
	compiledStageSignedDecision            = true
)

// IsPendingExternalStage lets the release graph account a signed task observer
// apart from both run execution budgets. The broker charges capacity only
// after an exact accepted claim.
func (c *Coordinator) IsPendingExternalStage(taskID string) bool {
	return c != nil && c.GraphRootOID != compiledStageAbsentIdentity &&
		c.choreographyBroker != nil && taskID != compiledStageAbsentIdentity
}

// waitCompiledStage observes a task peers selected from the signed frontier.
// It offers no Assignment and takes no peer capacity. The release executor
// retains its package lifecycle and settles finality after sibling hooks.
func (c *Coordinator) waitCompiledStage(ctx context.Context, taskID string,
	request release.StageRequest) (release.StageOutcome, error) {
	return c.waitCompiledTask(ctx, compiledTaskWaitOptions{TaskID: taskID, Request: request, Admit: func(ctx context.Context, event ChoreographyEvent, result Result) (release.StageOutcome, error) {
		return c.readTaskOutcome(ctx, taskID, event.Attempt,
			release.StageOutcome{Node: event.Node}, result, event.OutputOID,
			FormatChoreographyTaskBranch(c.Run, taskID), request)
	}})
}

func (c *Coordinator) waitCompiledSweep(ctx context.Context, taskID string,
	sweep SweepTask) (release.StageOutcome, error) {
	request := sweep.Request
	request.TaskID = taskID
	return c.waitCompiledTask(ctx, compiledTaskWaitOptions{TaskID: taskID, Request: request, Admit: func(ctx context.Context, event ChoreographyEvent, result Result) (release.StageOutcome, error) {
		return c.readSweepOutcome(ctx, taskID, event.Attempt,
			release.StageOutcome{Node: event.Node},
			taskReply{result: result, commit: event.OutputOID}, sweep)
	}})
}

type compiledTaskWaitOptions struct {
	TaskID  string
	Request release.StageRequest
	Admit   func(context.Context, ChoreographyEvent, Result) (release.StageOutcome, error)
}

func (c *Coordinator) waitCompiledTask(ctx context.Context,
	options compiledTaskWaitOptions) (release.StageOutcome, error) {
	taskID, request, admit := options.TaskID, options.Request, options.Admit
	if c == nil || c.choreographyBroker == nil || c.choreographyPersist == nil ||
		c.GraphRootOID == compiledStageAbsentIdentity || taskID == compiledStageAbsentIdentity ||
		request.TaskID != taskID {
		return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
	}
	c.choreographySummary.Store(taskID, choreographyTaskSummary{
		Package: request.Release.Pkg.Name, Stage: request.Stage})
	c.choreographyBroker.mu.Lock()
	task := c.choreographyBroker.tasks[taskID]
	workerOnly := task != nil && task.spec.Placement == PlacementWorker
	started := c.choreographyBroker.executionStarted
	deadlineUTC := c.choreographyBroker.admissionDeadlineUTC
	c.choreographyBroker.mu.Unlock()
	if task == nil || !started || deadlineUTC == compiledStageAbsentIdentity {
		return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
	}
	ctx, settled := c.watchOwnership(ctx)
	defer func() {
		if value, ok := c.choreographyPending.Load(taskID); ok {
			pending := value.(*choreographyPendingDecision)
			pending.mu.Lock()
			pending.watchSettled = settled
			pending.mu.Unlock()
			return
		}
		settled()
	}()
	cursor := ChoreographyEventCursor{OID: c.choreographyPersist.opts.Ledger.OID}
	deadline, parseErr := time.Parse(time.RFC3339Nano, deadlineUTC)
	if parseErr != nil || deadline.IsZero() {
		return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
	}
	if !workerOnly {
		deadline = time.Time{}
	}
	claimOID, assignmentOID, claimedNode, cancelOID :=
		compiledStageAbsentIdentity, compiledStageAbsentIdentity,
		compiledStageAbsentIdentity, compiledStageAbsentIdentity
	claimedAttempt := compiledStageNoAttempt
	admissionClosed := compiledStageAdmissionWindowOpen
	activeCtx := ctx
	var stopSettlement context.CancelFunc
	defer func() {
		if stopSettlement != nil {
			stopSettlement()
		}
	}()
	for {
		wake := c.choreographyBroker.EligibilityWake()
		c.choreographyBroker.mu.Lock()
		stopping := c.choreographyBroker.stopping
		c.choreographyBroker.mu.Unlock()
		waitCtx := activeCtx
		var done context.CancelFunc
		if !deadline.IsZero() {
			waitCtx, done = context.WithDeadline(activeCtx, deadline)
		} else {
			waitCtx, done = context.WithCancel(activeCtx)
		}
		var event ChoreographyEvent
		var next ChoreographyEventCursor
		var err error
		if stopping && stopSettlement == nil {
			err = context.Canceled
		} else if admissionClosed && claimOID == compiledStageAbsentIdentity {
			if !c.choreographyBroker.IsEligibleNodeAdmitted(taskID) {
				if expireErr := c.choreographyBroker.ExpireUnclaimed(activeCtx, taskID); expireErr != nil {
					done()
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, expireErr
				}
				done()
				admissionClosed = compiledStageAdmissionWindowOpen
				continue
			}
			type taskEventResult struct {
				event  ChoreographyEvent
				cursor ChoreographyEventCursor
				err    error
			}
			results := make(chan taskEventResult, compiledStageTaskEventBuffer)
			go func() {
				got, position, waitErr := c.choreographyBroker.WaitTaskEvent(waitCtx, taskID, cursor)
				results <- taskEventResult{event: got, cursor: position, err: waitErr}
			}()
			select {
			case got := <-results:
				event, next, err = got.event, got.cursor, got.err
			case <-wake:
				done()
				<-results
				continue
			}
		} else {
			type taskEventResult struct {
				event  ChoreographyEvent
				cursor ChoreographyEventCursor
				err    error
			}
			results := make(chan taskEventResult, compiledStageTaskEventBuffer)
			go func() {
				got, position, waitErr := c.choreographyBroker.WaitTaskEvent(waitCtx, taskID, cursor)
				results <- taskEventResult{event: got, cursor: position, err: waitErr}
			}()
			select {
			case got := <-results:
				event, next, err = got.event, got.cursor, got.err
			case <-wake:
				done()
				<-results
				continue
			}
		}
		timedOut := waitCtx.Err() == context.DeadlineExceeded
		if done != nil {
			done()
		}
		if err != nil {
			stopReason := ctx.Err()
			if stopReason == nil && stopping {
				stopReason = fmt.Errorf(compiledStageRunStoppedErrorFormat, taskID)
			}
			if stopReason != nil && stopSettlement == nil {
				bound := c.Timeouts.Cancel
				if bound <= compiledStageZeroTimeout {
					bound = taskReportTimeout
				}
				activeCtx, stopSettlement = context.WithTimeout(context.WithoutCancel(ctx), bound)
				defer stopSettlement()
				stopCursor, stopErr := c.choreographyBroker.StopClaims(activeCtx)
				if stopErr != nil {
					stopSettlement()
					stopSettlement = nil
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, stopErr
				}
				if claimOID == compiledStageAbsentIdentity {
					events, intervalErr := c.choreographyBroker.TaskEventsThrough(activeCtx,
						taskID, cursor, stopCursor)
					if intervalErr != nil {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, intervalErr
					}
					for _, signed := range events {
						switch signed.Kind {
						case ChoreographyClaimAccepted:
							claimOID, assignmentOID, claimedNode, claimedAttempt =
								signed.TaskOID, signed.AssignmentOID, signed.Node, signed.Attempt
						case ChoreographyNoClaimExpired, ChoreographyInertCancelled:
							return release.StageOutcome{NoClaim: &release.NoClaimFinality{
								Blocker: signed.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}}, stopReason
						case ChoreographyBlocked:
							return release.StageOutcome{NoClaim: &release.NoClaimFinality{
								Blocker: signed.BlockedBy}}, stopReason
						}
					}
					if claimOID == compiledStageAbsentIdentity {
						if cancelErr := c.choreographyBroker.CancelInertTask(activeCtx, taskID); cancelErr != nil {
							return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, cancelErr
						}
						return release.StageOutcome{NoClaim: &release.NoClaimFinality{
							Blocker:        choreographyInertCancelCause,
							SignedTerminal: compiledStageSignedTaskTerminal}}, stopReason
					}
				}
				deadline = time.Now()
				continue
			}
			if deadline.IsZero() || activeCtx.Err() != nil ||
				!timedOut {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, err
			}
			if claimOID == compiledStageAbsentIdentity {
				if c.choreographyBroker.IsEligibleNodeAdmitted(taskID) {
					// Admission has ended, but an admitted compatible node can
					// still claim as soon as its accepted work releases capacity.
					deadline = time.Time{}
					admissionClosed = compiledStageAdmissionWindowClosed
					continue
				}
				if expireErr := c.choreographyBroker.ExpireUnclaimed(activeCtx, taskID); expireErr != nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, expireErr
				}
				deadline = time.Time{}
				continue
			}
			bound := c.Timeouts.Cancel
			if bound <= compiledStageZeroTimeout {
				bound = taskReportTimeout
			}
			if cancelOID == compiledStageAbsentIdentity {
				cancelOID, err = c.choreographyBroker.CancelClaim(activeCtx, ChoreographyCancellation{
					ChoreographyRunBinding: c.choreographyBroker.binding,
					Task:                   taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
					Attempt: claimedAttempt, Node: claimedNode,
					AssignmentOID: assignmentOID, ClaimOID: claimOID})
				if errors.Is(err, ErrChoreographyResultWon) {
					deadline = time.Now().Add(bound)
					continue
				}
				if err != nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, err
				}
				deadline = time.Now().Add(bound)
				continue
			}
			return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
				fmt.Errorf(compiledStageCancelErrorFormat, taskID, err)
		}
		cursor = next
		switch event.Kind {
		case ChoreographyClaimAccepted:
			claimOID, assignmentOID, claimedNode, claimedAttempt =
				event.TaskOID, event.AssignmentOID, event.Node, event.Attempt
			if c.Timeouts.Task > compiledStageZeroTimeout {
				deadline = time.Now().Add(c.Timeouts.Task)
			} else {
				deadline = time.Time{}
			}
			continue
		case ChoreographyCancelIssued:
			cancelOID = event.TaskOID
			bound := c.Timeouts.Cancel
			if bound <= compiledStageZeroTimeout {
				bound = taskReportTimeout
			}
			deadline = time.Now().Add(bound)
			continue
		case ChoreographyClaimRefused, ChoreographyFrameReady:
			continue
		case ChoreographyResultAccepted:
			mailbox, mailboxErr := c.ChoreographyMailbox()
			if mailboxErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, mailboxErr
			}
			result, readErr := mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(activeCtx,
				c.choreographyBroker.binding, event, c.Limits.MaxManifestBytes)
			if readErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, readErr
			}
			outcome, admissionErr := admit(activeCtx, event, result)
			decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
				Task: taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
				Attempt: event.Attempt, Node: event.Node,
				ResultOID: event.TaskOID, Succeeded: admissionErr == nil}
			pending := &choreographyPendingDecision{decision: decision,
				packageName:    request.Release.Pkg.Name,
				resultAccepted: compiledStageAcceptedResult}
			if _, loaded := c.choreographyPending.LoadOrStore(taskID, pending); loaded {
				return outcome, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
			}
			if decisionErr := c.decideCompiledAttempt(activeCtx, pending); decisionErr != nil {
				return release.StageOutcome{Node: event.Node,
						UnresolvedOffer: compiledStageUnresolvedFinality},
					fmt.Errorf(compiledStageDecisionUnresolvedFormat, taskID, decisionErr)
			}
			c.rememberCompiledTaskOutcome(compiledTaskOutcomeRecord{Task: taskID,
				Request: request, Event: event, Exports: len(outcome.Exports), Err: admissionErr})
			outcome.UnresolvedOffer = compiledStageResolvedFinality
			return outcome, admissionErr
		case ChoreographyAckAccepted:
			decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
				Task: taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
				Attempt: event.Attempt, Node: event.Node, ResultOID: event.TaskOID}
			pending := &choreographyPendingDecision{decision: decision,
				packageName: request.Release.Pkg.Name,
				ackAccepted: compiledStageAcceptedAck}
			if _, loaded := c.choreographyPending.LoadOrStore(taskID, pending); loaded {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
					fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
			}
			if decisionErr := c.decideCompiledAttempt(activeCtx, pending); decisionErr != nil {
				return release.StageOutcome{Node: event.Node,
						UnresolvedOffer: compiledStageUnresolvedFinality},
					fmt.Errorf(compiledStageDecisionUnresolvedFormat, taskID, decisionErr)
			}
			ackErr := ctx.Err()
			if ackErr == nil {
				ackErr = c.refuseTask(taskID, event.Node, event.Attempt,
					fmt.Errorf(compiledStageAckErrorFormat, taskID))
			}
			c.rememberCompiledTaskOutcome(compiledTaskOutcomeRecord{Task: taskID,
				Request: request, Event: event, Err: ackErr})
			return release.StageOutcome{Node: event.Node}, ackErr
		case ChoreographyNoClaimExpired:
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{
					Blocker: event.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}},
				fmt.Errorf(compiledStageNoClaimErrorFormat, taskID)
		case ChoreographyInertCancelled:
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{
					Blocker: event.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}},
				fmt.Errorf(compiledStageInertCancelledErrorFormat, taskID)
		case ChoreographyBlocked:
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{Blocker: event.BlockedBy}},
				fmt.Errorf(compiledStageBlockedErrorFormat, taskID, event.BlockedBy)
		case ChoreographyRetryReady:
			continue
		case ChoreographyTerminal, ChoreographyNoClaimPackageSettled:
			return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
		default:
			return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
				fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
		}
	}
}

type compiledTaskOutcomeRecord struct {
	Task    string
	Request release.StageRequest
	Event   ChoreographyEvent
	Exports int
	Err     error
}

func (c *Coordinator) rememberCompiledTaskOutcome(record compiledTaskOutcomeRecord) {
	computation, publication := c.resolveTaskOutcome(record.Task, record.Request.Stage, record.Err)
	c.rememberTaskOutcome(TaskRecord{Package: record.Request.Release.Pkg.Name,
		Stage: record.Request.Stage, Task: record.Task, Node: record.Event.Node,
		Attempt: record.Event.Attempt, Computation: computation, Outputs: OutputsNone,
		Publication: publication, Recording: RecordingNone, Exports: record.Exports})
}

func (c *Coordinator) decideCompiledAttempt(ctx context.Context,
	pending *choreographyPendingDecision) error {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
	defer cancel()
	if err := c.choreographyBroker.DecideAttempt(settleCtx, pending.decision); err != nil {
		return err
	}
	pending.decided = compiledStageSignedDecision
	pending.packageSettlementPending = pending.packageName != compiledStageAbsentIdentity
	return nil
}
