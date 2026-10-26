// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func (c *Coordinator) runChoreographyStage(ctx context.Context, task string,
	request release.StageRequest) (release.StageOutcome, error) {
	c.choreographySummary.Store(task, choreographyTaskSummary{Package: request.Release.Pkg.Name,
		Stage: request.Stage})
	space := request.Release.Pkg.Space
	placement := resolveDescriptorPlacement(request.Descriptor, space)
	preferred := choreographyStageNoIdentity
	if request.Descriptor.Inputs == release.InputsOwnOutputs {
		preferred = c.producerOf(request)
	}
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, preferred,
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			return c.runChoreographyStageOnLease(ctx, choreographyStageLeaseOptions{
				Task:    task,
				Request: request,
				Lease:   lease,
				Attempt: attempt,
			})
		})
}

type choreographyStageLeaseOptions struct {
	Task    string
	Request release.StageRequest
	Lease   *Lease
	Attempt int
}

func (c *Coordinator) runChoreographyStageOnLease(ctx context.Context, options choreographyStageLeaseOptions) (release.StageOutcome, error) {
	task, request, lease, attempt := options.Task, options.Request, options.Lease, options.Attempt

	space := request.Release.Pkg.Space
	c.choreographySummary.Store(task, choreographyTaskSummary{Package: request.Release.Pkg.Name,
		Stage: request.Stage})
	kind := resolveStageKind(request.Descriptor)
	// A reserved v3 stage bypasses Coordinator.Stage. Pin this package's
	// preparation under its declared roots before its first build snapshot;
	// doing it only at the later local publish would mistake admitted build
	// outputs for native source inputs of downstream packages.
	var frame preparedFrame
	var err error
	if !request.Descriptor.IsPreparing {
		err = c.refreshPreparedOutputRoots(ctx, request)
	}
	if err == nil && request.Descriptor.Inputs == release.InputsProviderOutputs {
		err = c.requirePreparedProviderOutputs(task, request)
	}
	if err == nil {
		frame, err = c.prepareFrame(ctx, lease.Node, request)
	}
	if err != nil {
		lease.Release()
		outcome := release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs,
			UnresolvedOffer: choreographyStageUnresolvedFinality}
		if ctx.Err() == nil {
			blocker, proven, proofErr := c.ProveChoreographyNoClaim(ctx, task)
			if proofErr != nil {
				return outcome, errors.Join(err, proofErr)
			}
			if proven {
				outcome.NoClaim = &release.NoClaimFinality{Blocker: blocker}
				outcome.UnresolvedOffer = choreographyStageResolvedFinality
			}
		}
		return outcome, err
	}
	assignment := c.formatAssignment(lease.Node, kind, task, attempt,
		frame.dir, frame.repositories, frame.inputs, request)
	roots := resolveDeclaredOutputs(request)
	c.registerChoreographyResult(choreographyResultRegistration{Task: task, Roots: roots,
		Platforms: space.BuildPlatforms, Produces: request.Descriptor.IsProducingOutputs})
	return c.runChoreographyAssigned(ctx, lease, assignment,
		func(ctx context.Context, result Result, oid string) (release.StageOutcome, error) {
			return c.readTaskOutcome(ctx, task, attempt,
				release.StageOutcome{Node: lease.Node}, result, oid, assignment.Branch, request)
		})
}

func (c *Coordinator) runChoreographyPrepared(ctx context.Context, task string,
	request release.StageRequest) (release.StageOutcome, error) {
	if c.GraphRootOID != choreographyStageNoIdentity {
		request.TaskID = task
		return c.waitCompiledStage(ctx, task, request)
	}
	c.choreographySummary.Store(task, choreographyTaskSummary{Package: request.Release.Pkg.Name,
		Stage: StageBuild})
	space := request.Release.Pkg.Space
	placement := ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > choreographyStageNoLoginCommands)
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, choreographyStageNoIdentity,
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			frame, err := c.prepareFrame(ctx, lease.Node, request)
			if err != nil {
				lease.Release()
				return release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs}, err
			}
			assignment := c.formatAssignment(lease.Node, KindPrepare, task, attempt,
				frame.dir, frame.repositories, frame.inputs, request)
			c.registerChoreographyResult(choreographyResultRegistration{Task: task,
				Roots: space.BuildOutputs, Platforms: space.BuildPlatforms, Produces: choreographyStageOutputProducing})
			return c.runChoreographyAssigned(ctx, lease, assignment,
				func(ctx context.Context, result Result, oid string) (release.StageOutcome, error) {
					return c.readTaskOutcome(ctx, task, attempt,
						release.StageOutcome{Node: lease.Node}, result, oid, assignment.Branch, request)
				})
		})
}

func (c *Coordinator) runChoreographySweep(ctx context.Context, task string,
	sweep SweepTask) (release.StageOutcome, error) {
	if c.GraphRootOID != choreographyStageNoIdentity {
		return c.waitCompiledSweep(ctx, task, sweep)
	}
	c.choreographySummary.Store(task, choreographyTaskSummary{Package: sweep.Request.Release.Pkg.Name,
		Stage: choreographyStageSweepPrefix + sweep.Script})
	space := sweep.Request.Release.Pkg.Space
	placement := ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > choreographyStageNoLoginCommands)
	return c.placeTask(ctx, task, placement, space.BuildPlatforms, choreographyStageNoIdentity,
		func(ctx context.Context, lease *Lease, attempt int) (release.StageOutcome, error) {
			sources := c.dispatch.Sources(sweep.Request.Release.Pkg.Name)
			dir, err := resolvePackageDir(sources, sweep.Request)
			if err != nil {
				lease.Release()
				return release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs}, err
			}
			commits, err := c.captureInputs(ctx, sources)
			if err != nil {
				lease.Release()
				return release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs}, err
			}
			repositories, err := c.carrySourceInputs(ctx, sources, commits)
			if err != nil {
				lease.Release()
				return release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs}, err
			}
			exports, inputs, err := c.sweepAssignmentInputs(sweep.Request.Release.Outputs, sources)
			if err != nil {
				lease.Release()
				return release.StageOutcome{Node: lease.Node, FailedPart: release.PartInputs}, err
			}
			assignment := c.formatSweepAssignment(lease.Node, task, attempt, dir, repositories, sweep)
			assignment.Exports, assignment.Inputs = exports, inputs
			c.registerChoreographyResult(choreographyResultRegistration{Task: task,
				Roots: cleanSweepRoots(sweep.Roots), Produces: choreographyStageOutputProducing})
			return c.runChoreographyAssigned(ctx, lease, assignment,
				func(ctx context.Context, result Result, oid string) (release.StageOutcome, error) {
					return c.readSweepOutcome(ctx, task, attempt, release.StageOutcome{Node: lease.Node},
						taskReply{result: result, commit: oid}, sweep)
				})
		})
}

type choreographyResultAdmission func(context.Context, Result, string) (release.StageOutcome, error)

type choreographyTaskSummary struct{ Package, Stage string }

type choreographyPendingDecision struct {
	mu                       sync.Mutex
	decision                 ChoreographyDecision
	packageName              string
	assignmentOID            string
	lease                    *Lease
	localDone                <-chan error
	resultAccepted           bool
	ackAccepted              bool
	decided                  bool
	packageSettlementPending bool
	localError               error
	localEnded               bool
	watchSettled             func()
}

// SettleChoreographyTask signs package finality after the release executor
// completes sibling and hook settlement. A compiled task's earlier signed
// terminal released capacity; this callback now discharges its held edges.
func (c *Coordinator) SettleChoreographyTask(ctx context.Context, taskID string,
	status release.PackageOutcome) error {
	if c != nil && c.choreographyBroker != nil && c.choreographyBroker.IsInertCancelled(taskID) {
		return c.choreographyBroker.SettleInertCancellation(ctx, taskID, string(status))
	}
	if c != nil && c.choreographyBroker != nil && c.choreographyBroker.IsNoClaimExpired(taskID) {
		return c.choreographyBroker.SettleNoClaimFailure(ctx, taskID, string(status))
	}
	value, ok := c.choreographyPending.Load(taskID)
	if !ok {
		return fmt.Errorf(choreographyStageMissingSettlementErrorFormat, taskID)
	}
	pending := value.(*choreographyPendingDecision)
	pending.mu.Lock()
	defer pending.mu.Unlock()
	decision := pending.decision
	decision.PackageOutcome = string(status)
	if pending.packageSettlementPending {
		if err := c.choreographyBroker.SettleTaskPackage(ctx, taskID, string(status)); err != nil {
			return err
		}
		pending.packageSettlementPending = choreographyStagePackageSettlementComplete
	} else if !pending.decided {
		var err error
		if decision.Unclaimed {
			err = c.choreographyBroker.DecideUnclaimedOffer(ctx, decision)
		} else {
			err = c.choreographyBroker.DecideAttempt(ctx, decision)
		}
		if err != nil {
			return err
		}
		pending.decided = choreographyStageDecisionSigned
	}
	if pending.localDone != nil && !pending.localEnded {
		select {
		case pending.localError = <-pending.localDone:
			pending.localEnded = choreographyStageLocalSlotSettled
		case <-ctx.Done():
			return fmt.Errorf(choreographyStageLocalSlotPendingErrorFormat, taskID, ctx.Err())
		}
	}
	if pending.localError != nil {
		return fmt.Errorf(choreographyStageLocalSlotFailedErrorFormat, taskID, pending.localError)
	}
	c.choreographyPending.CompareAndDelete(taskID, pending)
	if pending.lease != nil {
		pending.lease.Release()
	}
	if pending.watchSettled != nil {
		pending.watchSettled()
		pending.watchSettled = nil
	}
	return nil
}

func (c *Coordinator) runChoreographyAssigned(ctx context.Context, lease *Lease,
	assignment *Assignment, admit choreographyResultAdmission) (release.StageOutcome, error) {
	base := release.StageOutcome{Node: lease.Node, UnresolvedOffer: choreographyStageUnresolvedFinality}
	offered, err := c.OfferChoreographyAssignment(ctx, lease, assignment)
	if err != nil {
		if errors.Is(err, errChoreographyAtomicRefused) && ctx.Err() == nil {
			blocker, proven, proofErr := c.ProveChoreographyNoClaim(ctx, assignment.Task)
			if proofErr != nil {
				return base, errors.Join(err, proofErr)
			}
			if proven {
				base.NoClaim = &release.NoClaimFinality{Blocker: blocker}
				base.UnresolvedOffer = choreographyStageResolvedFinality
			}
		}
		return base, err // Unknown outcomes retain the offer's fenced lease.
	}
	var localDone <-chan error
	if lease.IsLocal {
		if c.LocalChoreographer == nil {
			return base, fmt.Errorf(choreographyStageMissingLocalRunnerError)
		}
		finished := make(chan error, choreographyStageTaskEventBuffer)
		localDone = finished
		go func() { finished <- c.LocalChoreographer(ctx, offered.AssignmentOID, offered.Cursor) }()
	}
	cursor := offered.Cursor
	var deadline time.Time
	if c.Timeouts.Task > choreographyStageNoTimeout {
		deadline = time.Now().Add(c.Timeouts.Task)
	}
	claimOID, cancelOID := choreographyStageNoIdentity, choreographyStageNoIdentity
	activeCtx := ctx
	var settleDone context.CancelFunc
	defer func() {
		if settleDone != nil {
			settleDone()
		}
	}()
	interrupted := choreographyStageOfferActive
	for {
		waitCtx, stop := choreographyWaitContext(activeCtx, deadline)
		event, next, err := c.waitChoreographyTaskEvent(waitCtx, assignment.Task, cursor, &localDone)
		stop()
		if err != nil {
			if ctx.Err() != nil && settleDone == nil && claimOID == choreographyStageNoIdentity {
				bound := c.Timeouts.Cancel
				if bound <= choreographyStageNoTimeout {
					bound = taskReportTimeout
				}
				activeCtx, settleDone = context.WithTimeout(context.WithoutCancel(ctx), bound)
				defer settleDone()
				stopped, stopErr := c.choreographyBroker.StopClaims(activeCtx)
				if stopErr != nil {
					fenceChoreographyLease(lease)
					return base, fmt.Errorf(choreographyStageOfferFenceErrorFormat, stopErr)
				}
				// The return path may have lost a Claim receipt after the atomic
				// task/ledger CAS. Only the signed interval through StopClaims can
				// prove whether this offer remained unclaimed at the fence.
				events, readErr := c.choreographyBroker.TaskEventsThrough(activeCtx,
					assignment.Task, offered.Cursor, stopped)
				if readErr != nil {
					fenceChoreographyLease(lease)
					return base, fmt.Errorf(choreographyStageOfferHistoryErrorFormat, readErr)
				}
				for _, prior := range events {
					if prior.Kind == ChoreographyClaimAccepted && prior.Attempt == assignment.Attempt &&
						prior.Node == assignment.Node && prior.PriorTaskOID == offered.AssignmentOID {
						claimOID = prior.TaskOID
					}
				}
				c.choreographyBroker.mu.Lock()
				if task := c.choreographyBroker.tasks[assignment.Task]; task != nil {
					if task.claim != claimOID {
						readErr = fmt.Errorf(choreographyStageClaimStateMismatchError)
					}
				}
				c.choreographyBroker.mu.Unlock()
				if readErr != nil {
					fenceChoreographyLease(lease)
					return base, readErr
				}
				if claimOID == choreographyStageNoIdentity {
					if err := c.joinChoreographyLocal(activeCtx, localDone); err != nil {
						fenceChoreographyLease(lease)
						return base, fmt.Errorf(choreographyStageUnclaimedSlotErrorFormat, err)
					}
					lease.Release()
					base.UnresolvedOffer = choreographyStageResolvedFinality
					return base, ctx.Err()
				}
				interrupted = choreographyStageOfferInterrupted
			}
			if errors.Is(err, context.DeadlineExceeded) && activeCtx.Err() == nil ||
				ctx.Err() != nil && claimOID != choreographyStageNoIdentity && (settleDone == nil || interrupted) {
				if ctx.Err() != nil {
					interrupted = choreographyStageOfferInterrupted
				}
				if claimOID == choreographyStageNoIdentity {
					// Offer→Decision is an exact task CAS. A Claim that wins
					// first makes this operation fail closed rather than
					// granting a second attempt beside the running one.
					retry := assignment.Attempt < maxPlacementAttempts
					decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
						Task: assignment.Task, Branch: assignment.Branch, Attempt: assignment.Attempt,
						Node: assignment.Node, ResultOID: offered.AssignmentOID,
						Unclaimed: choreographyStageUnclaimedDecision, Retry: retry}
					if retry {
						if err := c.choreographyBroker.DecideUnclaimedOffer(ctx, decision); err != nil {
							if errors.Is(err, errChoreographyClaimWon) {
								deadline = time.Now().Add(c.Timeouts.Task)
								continue
							}
							fenceChoreographyLease(lease)
							return base, err
						}
						if err := c.joinChoreographyLocal(ctx, localDone); err != nil {
							fenceChoreographyLease(lease)
							return base, err
						}
						lease.Release()
						return base, errQueueExpired
					}
					fenceOID, err := c.choreographyBroker.FenceUnclaimedOffer(ctx,
						ChoreographyQueueFence{ChoreographyRunBinding: c.choreographyBroker.binding,
							Task: assignment.Task, Branch: assignment.Branch, Attempt: assignment.Attempt,
							Node: assignment.Node, AssignmentOID: offered.AssignmentOID})
					if errors.Is(err, errChoreographyClaimWon) {
						deadline = time.Now().Add(c.Timeouts.Task)
						continue
					}
					if err != nil {
						fenceChoreographyLease(lease)
						return base, err
					}
					decision.ResultOID, decision.OfferedOID = fenceOID, offered.AssignmentOID
					// The exact task CAS proves nobody claimed this offer, so it
					// holds no physical slot. Return its placement capacity before
					// onFail/revert hooks run; they may need this same first node.
					lease.Release()
					c.choreographyPending.Store(assignment.Task,
						&choreographyPendingDecision{decision: decision,
							assignmentOID: offered.AssignmentOID, localDone: localDone})
					base.UnresolvedOffer = choreographyStageResolvedFinality
					return base, formatPlacementExhausted(errQueueExpired, c.Timeouts.Task, assignment.Attempt)
				}
				bound := c.Timeouts.Cancel
				if bound <= choreographyStageNoTimeout {
					bound = taskReportTimeout
				}
				if settleDone == nil {
					activeCtx, settleDone = context.WithTimeout(context.WithoutCancel(ctx), bound)
					defer settleDone()
				}
				if cancelOID == choreographyStageNoIdentity {
					cancelOID, err = c.choreographyBroker.CancelClaim(activeCtx, ChoreographyCancellation{
						ChoreographyRunBinding: c.choreographyBroker.binding,
						Task:                   assignment.Task, Branch: assignment.Branch, Attempt: assignment.Attempt,
						Node: assignment.Node, AssignmentOID: offered.AssignmentOID, ClaimOID: claimOID})
					if errors.Is(err, ErrChoreographyResultWon) {
						deadline = time.Now().Add(bound)
						continue
					}
					if err != nil {
						fenceChoreographyLease(lease)
						return base, fmt.Errorf(choreographyStageCancelClaimErrorFormat, err)
					}
					deadline = time.Now().Add(bound)
					continue
				}
				fenceChoreographyLease(lease)
				return base, fmt.Errorf(choreographyStageCancelAckMissingErrorFormat, assignment.Task, err)
			}
			fenceChoreographyLease(lease)
			return base, err
		}
		cursor = next
		if event.Attempt != assignment.Attempt {
			continue
		}
		if event.Kind == ChoreographyClaimAccepted {
			claimOID = event.TaskOID
			if c.Timeouts.Task > choreographyStageNoTimeout {
				deadline = time.Now().Add(c.Timeouts.Task)
			}
			continue
		}
		if event.Kind == ChoreographyCancelIssued {
			if claimOID == choreographyStageNoIdentity || event.PriorTaskOID != claimOID {
				fenceChoreographyLease(lease)
				return base, fmt.Errorf(choreographyStageCancelClaimMismatchError)
			}
			cancelOID = event.TaskOID
			bound := c.Timeouts.Cancel
			if bound <= choreographyStageNoTimeout {
				bound = taskReportTimeout
			}
			deadline = time.Now().Add(bound)
			continue
		}
		if event.Kind == ChoreographyResultAccepted {
			if ctx.Err() != nil && settleDone == nil {
				interrupted = choreographyStageOfferInterrupted
				bound := c.Timeouts.Cancel
				if bound <= choreographyStageNoTimeout {
					bound = taskReportTimeout
				}
				activeCtx, settleDone = context.WithTimeout(context.WithoutCancel(ctx), bound)
				defer settleDone()
				deadline = time.Now().Add(bound)
			}
			admitCtx, done := choreographyWaitContext(activeCtx, deadline)
			defer done()
			mailbox, err := c.ChoreographyMailbox()
			if err != nil {
				return base, err
			}
			result, err := mailbox.ReadChoreographyAcceptedResult(admitCtx,
				c.choreographyBroker.binding, event, c.Limits.MaxPackBytes)
			if err != nil {
				fenceChoreographyLease(lease)
				return base, err
			}
			outcome, admissionErr := admit(admitCtx, result, event.OutputOID)
			if interrupted && admissionErr == nil {
				admissionErr = ctx.Err()
			}
			decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
				Task: assignment.Task, Branch: assignment.Branch, Attempt: assignment.Attempt,
				Node: assignment.Node, ResultOID: event.TaskOID,
				Succeeded: admissionErr == nil}
			packageName := choreographyStageNoPackage
			if assignment.Package != nil {
				packageName = assignment.Package.Name
			}
			pending := &choreographyPendingDecision{decision: decision, lease: lease,
				packageName: packageName, assignmentOID: offered.AssignmentOID,
				localDone: localDone, resultAccepted: choreographyStageAcceptedResult}
			if _, loaded := c.choreographyPending.LoadOrStore(assignment.Task, pending); loaded {
				fenceChoreographyLease(lease)
				return outcome, fmt.Errorf(choreographyStageEarlierResultErrorFormat, assignment.Task)
			}
			outcome.UnresolvedOffer = choreographyStageResolvedFinality
			return outcome, admissionErr
		}
		if event.Kind == ChoreographyAckAccepted {
			if ctx.Err() != nil {
				interrupted = choreographyStageOfferInterrupted
				if settleDone == nil {
					bound := c.Timeouts.Cancel
					if bound <= choreographyStageNoTimeout {
						bound = taskReportTimeout
					}
					activeCtx, settleDone = context.WithTimeout(context.WithoutCancel(ctx), bound)
					defer settleDone()
				}
			}
			if cancelOID == choreographyStageNoIdentity || claimOID == choreographyStageNoIdentity {
				fenceChoreographyLease(lease)
				if settleDone != nil {
					settleDone()
					settleDone = nil
				}
				return base, fmt.Errorf(choreographyStageAckWithoutCancelError)
			}
			mailbox, err := c.ChoreographyMailbox()
			if err != nil {
				return base, err
			}
			tip, err := mailbox.readTree(activeCtx, event.TaskOID)
			if err != nil {
				fenceChoreographyLease(lease)
				return base, fmt.Errorf(choreographyStageAckUnreadableCauseErrorFormat, err)
			}
			if tip.kind != MessageAck {
				fenceChoreographyLease(lease)
				return base, fmt.Errorf(choreographyStageAckUnreadableError)
			}
			document, err := mailbox.Read(activeCtx, ChainTip{OID: event.TaskOID,
				Kind: tip.kind, document: tip.document, signature: tip.signature}, c.Limits.MaxManifestBytes)
			if err != nil {
				fenceChoreographyLease(lease)
				return base, err
			}
			var ack Ack
			if err := json.Unmarshal(document, &ack); err != nil ||
				ack.Cancel != cancelOID || ack.Assignment != offered.AssignmentOID ||
				ack.Run != assignment.Run || ack.Task != assignment.Task ||
				ack.Attempt != assignment.Attempt || ack.Node != assignment.Node {
				fenceChoreographyLease(lease)
				return base, fmt.Errorf(choreographyStageAckMismatchError)
			}
			decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
				Task: assignment.Task, Branch: assignment.Branch, Attempt: assignment.Attempt,
				Node: assignment.Node, ResultOID: event.TaskOID,
				Retry: !interrupted && !ack.CommandStarted && assignment.Attempt < maxPlacementAttempts}
			if decision.Retry {
				if err := c.choreographyBroker.DecideAttempt(activeCtx, decision); err != nil {
					fenceChoreographyLease(lease)
					return base, err
				}
				if err := c.joinChoreographyLocal(ctx, localDone); err != nil {
					fenceChoreographyLease(lease)
					return base, err
				}
				lease.Release()
				return base, errQueueExpired
			}
			packageName := choreographyStageNoPackage
			if assignment.Package != nil {
				packageName = assignment.Package.Name
			}
			c.choreographyPending.Store(assignment.Task,
				&choreographyPendingDecision{decision: decision, lease: lease,
					packageName:   packageName,
					assignmentOID: offered.AssignmentOID, localDone: localDone,
					ackAccepted: choreographyStageAcceptedAck})
			base.UnresolvedOffer = choreographyStageResolvedFinality
			return base, fmt.Errorf(choreographyStageSignedCancelErrorFormat, assignment.Task)
		}
		if event.Kind == ChoreographyBlocked || event.Kind == ChoreographyTerminal {
			fenceChoreographyLease(lease)
			return base, fmt.Errorf(choreographyStageUnexpectedTerminalErrorFormat, event.Kind)
		}
	}
}

func (c *Coordinator) waitChoreographyTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor, localDone *<-chan error) (ChoreographyEvent, ChoreographyEventCursor, error) {
	if localDone == nil || *localDone == nil {
		return c.choreographyBroker.WaitTaskEvent(ctx, task, after)
	}
	type answer struct {
		event  ChoreographyEvent
		cursor ChoreographyEventCursor
		err    error
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan answer, choreographyStageTaskEventBuffer)
	go func() {
		event, cursor, err := c.choreographyBroker.WaitTaskEvent(waitCtx, task, after)
		ready <- answer{event: event, cursor: cursor, err: err}
	}()
	select {
	case next := <-ready:
		return next.event, next.cursor, next.err
	case err := <-*localDone:
		*localDone = nil
		if err != nil {
			return ChoreographyEvent{}, ChoreographyEventCursor{}, err
		}
		select {
		case next := <-ready:
			return next.event, next.cursor, next.err
		case <-ctx.Done():
			return ChoreographyEvent{}, ChoreographyEventCursor{}, ctx.Err()
		}
	case <-ctx.Done():
		return ChoreographyEvent{}, ChoreographyEventCursor{}, ctx.Err()
	}
}

func fenceChoreographyLease(lease *Lease) {
	if lease != nil && !lease.IsLocal {
		lease.Leak(LeakTransport)
	}
}

func choreographyWaitContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline)
}

// A first-local worker holds the same physical slot while it observes the
// signed retry decision. The next placement must wait until it has returned;
// a missing acknowledgement leaves that slot and its Pool lease fenced.
func (c *Coordinator) joinChoreographyLocal(ctx context.Context, done <-chan error) error {
	if done == nil {
		return nil
	}
	bound := c.Timeouts.Cancel
	if bound <= choreographyStageNoTimeout {
		bound = taskReportTimeout
	}
	settleCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), bound)
	defer stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf(choreographyStageRetrySlotErrorFormat, err)
		}
		return nil
	case <-settleCtx.Done():
		return fmt.Errorf(choreographyStageRetrySlotErrorFormat, settleCtx.Err())
	}
}
