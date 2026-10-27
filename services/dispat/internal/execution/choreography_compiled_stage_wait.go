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
	compiledStageNoCompatibleNodeFormat    = "execution: compiled task %q expired without an admitted compatible node"
	compiledStageBlockedErrorFormat        = "execution: compiled task %q was blocked by %q"
	compiledStageAckErrorFormat            = "execution: compiled task %q stopped after signed cancellation"
	compiledStageInertCancelledErrorFormat = "execution: compiled task %q was cancelled before claim after signed run stop"
	compiledStageRunStoppedErrorFormat     = "execution: compiled task %q stopped before settlement"
	compiledStageCancelErrorFormat         = "execution: compiled task %q has no signed stop acknowledgement after cancellation: %w"
	compiledStageDecisionUnresolvedFormat  = "execution: compiled task %q has an accepted effect but no verified signed task decision: %w"
	compiledStageAbsentIdentity            = ""
	compiledStageNoAttempt                 = 0
	compiledStageFirstAttempt              = 1
	compiledStageNoPredecessors            = 0
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
	compiledStagePackageNotSkipped         = false
	compiledStageSignedResultReadAttempts  = 3
	compiledStageFirstSignedResultRead     = 0
	compiledStageSignedResultReadIncrement = 1
	compiledStagePackageSettlementComplete = false
	compiledStageSignedResultRetryDelay    = 50 * time.Millisecond
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
	}, Validate: func(result Result) error {
		copy := result
		return rehomeChoreographyExports(choreographyExportRehomeOptions{Result: &copy,
			Repository: request.Release.Pkg.Repository, Package: request.Release.Pkg.Name,
			SourceRoot: request.Release.Pkg.Dir, Roots: request.Release.Pkg.Space.BuildOutputs})
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
	}, Validate: func(result Result) error {
		if err := validateSweepCarriedExports(sweepCarriedExportValidation{
			Result: result, Signed: result.Exports,
			Repository: sweep.Request.Release.Pkg.Repository,
			Package:    sweep.Request.Release.Pkg.Name}); err != nil {
			return err
		}
		owner, err := c.resolveOwnerRepository(sweep.Request)
		if err != nil {
			return err
		}
		copy := result
		return rehomeChoreographyExports(choreographyExportRehomeOptions{Result: &copy,
			Repository: sweep.Request.Release.Pkg.Repository,
			Package:    sweep.Request.Release.Pkg.Name,
			SourceRoot: owner.Dir, Roots: cleanSweepRoots(sweep.Roots)})
	}})
}

type compiledTaskWaitOptions struct {
	TaskID   string
	Request  release.StageRequest
	Admit    func(context.Context, ChoreographyEvent, Result) (release.StageOutcome, error)
	Validate func(Result) error
}

type compiledAcceptedPickupResult struct {
	event     ChoreographyEvent
	result    Result
	succeeded bool
}

func (c *Coordinator) waitCompiledTask(ctx context.Context,
	options compiledTaskWaitOptions) (release.StageOutcome, error) {
	taskID, request, admit, validate := options.TaskID, options.Request, options.Admit, options.Validate
	if c == nil || c.choreographyBroker == nil || c.choreographyPersist == nil ||
		c.GraphRootOID == compiledStageAbsentIdentity || taskID == compiledStageAbsentIdentity ||
		request.TaskID != taskID {
		return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
	}
	c.choreographySummary.Store(taskID, choreographyTaskSummary{
		Package: request.Release.Pkg.Name, Stage: request.Stage})
	c.choreographyBroker.mu.Lock()
	task := c.choreographyBroker.tasks[taskID]
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
	admissionDeadline := deadline
	claimOID, assignmentOID, claimedNode, cancelOID :=
		compiledStageAbsentIdentity, compiledStageAbsentIdentity,
		compiledStageAbsentIdentity, compiledStageAbsentIdentity
	claimedPickupID := compiledStageAbsentIdentity
	claimedAttempt := compiledStageNoAttempt
	signedBeyondClaim := func() bool {
		c.choreographyBroker.mu.Lock()
		defer c.choreographyBroker.mu.Unlock()
		indexed := c.choreographyBroker.tasks[taskID]
		return indexed != nil && claimOID != compiledStageAbsentIdentity &&
			(indexed.terminal || indexed.claim != claimOID || indexed.attempt != claimedAttempt)
	}
	acceptedPickups := make(map[string]compiledAcceptedPickupResult)
	acceptedAcks := make(map[string]ChoreographyEvent)
	admissionClosed := compiledStageAdmissionWindowOpen
	resumeAfterExpiryRefusal := func(expireErr error) bool {
		var refused *choreographyNoClaimBrokerRefusal
		if !errors.As(expireErr, &refused) || refused.task != taskID {
			return false
		}
		switch refused.disposition {
		case choreographyNoClaimDispositionAdvanced,
			choreographyNoClaimDispositionPreparedBlocked:
			// The signed task feed must account for the transition. A prepared
			// blocker is proven at the top of the next observer iteration.
			deadline = time.Time{}
			admissionClosed = compiledStageAdmissionWindowOpen
		case choreographyNoClaimDispositionEligible:
			// The member can claim after occupied capacity becomes free.
			deadline = time.Time{}
			admissionClosed = compiledStageAdmissionWindowClosed
		case choreographyNoClaimDispositionDeadlineFuture:
			deadline = admissionDeadline
			admissionClosed = compiledStageAdmissionWindowOpen
		case choreographyNoClaimDispositionStopping:
			// The next iteration reads the signed stop and settles inert or
			// claimed work through the ordinary StopClaims path.
		default:
			return false
		}
		return true
	}
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
		brokerFault := c.choreographyBroker.fault
		indexed := c.choreographyBroker.tasks[taskID]
		waitingForPredecessor := indexed != nil && indexed.remaining > compiledStageNoPredecessors &&
			!indexed.terminal && indexed.claim == compiledStageAbsentIdentity
		preparedBlocked := indexed != nil && indexed.remaining == compiledStageNoPredecessors &&
			indexed.preparedBlockedBy != compiledStageAbsentIdentity && !indexed.terminal &&
			indexed.claim == compiledStageAbsentIdentity
		packageSkipped := compiledStagePackageNotSkipped
		packageBlocker := compiledStageAbsentIdentity
		var packageSkipErr error
		if indexed != nil && indexed.remaining == compiledStageNoPredecessors &&
			!indexed.terminal && indexed.claim == compiledStageAbsentIdentity &&
			indexed.spec.Package != compiledStageAbsentIdentity &&
			indexed.spec.Kind != string(KindPrepare) && c.choreographyBroker.outcomePolicy != nil {
			packageSkipped, packageBlocker, packageSkipErr =
				c.choreographyBroker.outcomePolicy.ShouldSkip(indexed.spec.Package)
		}
		c.choreographyBroker.mu.Unlock()
		if brokerFault != nil {
			return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, brokerFault
		}
		if packageSkipErr != nil {
			return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, packageSkipErr
		}
		if packageSkipped && claimOID == compiledStageAbsentIdentity && !stopping && activeCtx.Err() == nil {
			_, proven, proofErr := c.ProveChoreographyNoClaim(activeCtx, taskID)
			if proofErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, proofErr
			}
			if proven {
				return release.StageOutcome{NoClaim: &release.NoClaimFinality{Blocker: packageBlocker}},
					fmt.Errorf(compiledStageBlockedErrorFormat, taskID, packageBlocker)
			}
		}
		if preparedBlocked && claimOID == compiledStageAbsentIdentity && !stopping && activeCtx.Err() == nil {
			blocker, proven, proofErr := c.ProveChoreographyNoClaim(activeCtx, taskID)
			if proofErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, proofErr
			}
			if proven {
				return release.StageOutcome{NoClaim: &release.NoClaimFinality{Blocker: blocker}},
					c.refuseTask(taskID, compiledStageAbsentIdentity, compiledStageFirstAttempt,
						fmt.Errorf(compiledStageBlockedErrorFormat, taskID, blocker))
			}
		}
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
			if !waitingForPredecessor && !c.choreographyBroker.IsEligibleNodeAdmitted(taskID) {
				if expireErr := c.choreographyBroker.ExpireUnclaimed(activeCtx, taskID); expireErr != nil {
					done()
					if !resumeAfterExpiryRefusal(expireErr) {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, expireErr
					}
					continue
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
							claimedPickupID = signed.PickupID
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
				if waitingForPredecessor || c.choreographyBroker.IsEligibleNodeAdmitted(taskID) {
					// A successor cannot expire before its predecessor has signed
					// finality. A compatible admitted node may also claim after
					// its occupied capacity becomes free.
					deadline = time.Time{}
					admissionClosed = compiledStageAdmissionWindowClosed
					continue
				}
				if expireErr := c.choreographyBroker.ExpireUnclaimed(activeCtx, taskID); expireErr != nil {
					if !resumeAfterExpiryRefusal(expireErr) {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, expireErr
					}
					continue
				}
				deadline = time.Time{}
				continue
			}
			bound := c.Timeouts.Cancel
			if bound <= compiledStageZeroTimeout {
				bound = taskReportTimeout
			}
			if signedBeyondClaim() {
				// A successor or finality was signed after this cursor's last
				// event. Drain that exact event before acting on the old pickup.
				deadline = time.Time{}
				continue
			}
			if cancelOID == compiledStageAbsentIdentity {
				cancelOID, err = c.choreographyBroker.CancelClaim(activeCtx, ChoreographyCancellation{
					ChoreographyRunBinding: c.choreographyBroker.binding,
					Task:                   taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
					Attempt: claimedAttempt, Node: claimedNode,
					AssignmentOID: assignmentOID, ClaimOID: claimOID})
				if errors.Is(err, ErrChoreographyResultWon) ||
					errors.Is(err, ErrChoreographyTaskAlreadyFinal) || signedBeyondClaim() {
					deadline = time.Time{}
					continue
				}
				if err != nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, err
				}
				deadline = time.Now().Add(bound)
				continue
			}
			if claimedPickupID != compiledStageAbsentIdentity && !stopping {
				lossErr := c.choreographyBroker.MarkPickupContactLost(activeCtx, taskID, claimedPickupID)
				if lossErr != nil {
					c.choreographyBroker.mu.Lock()
					indexed := c.choreographyBroker.tasks[taskID]
					var pickup *indexedChoreographyPickup
					if indexed != nil {
						pickup = indexed.pickups[claimedPickupID]
					}
					answered := pickup != nil && pickup.resultReceiptOID != compiledStageAbsentIdentity
					c.choreographyBroker.mu.Unlock()
					if !answered {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, lossErr
					}
				} else {
					c.choreographyBroker.mu.Lock()
					indexed := c.choreographyBroker.tasks[taskID]
					successorClaimed := indexed != nil && indexed.claim != compiledStageAbsentIdentity &&
						indexed.claim != claimOID
					c.choreographyBroker.mu.Unlock()
					if !successorClaimed {
						if capacityErr := c.choreographyBroker.CheckPickupSuccessorBudget(taskID); capacityErr != nil {
							return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, capacityErr
						}
					}
				}
				deadline = time.Time{}
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
			claimedPickupID = event.PickupID
			if claimedPickupID != compiledStageAbsentIdentity {
				pickupDeadline, parseErr := time.Parse(time.RFC3339Nano, event.PickupDeadlineUTC)
				if parseErr != nil || pickupDeadline.IsZero() {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
				deadline = pickupDeadline
			} else if c.Timeouts.Task > compiledStageZeroTimeout {
				deadline = time.Now().Add(c.Timeouts.Task)
			} else {
				deadline = time.Time{}
			}
			continue
		case ChoreographyCancelIssued:
			if claimedPickupID != compiledStageAbsentIdentity &&
				(event.PickupID != claimedPickupID || event.ClaimOID != claimOID) {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
					fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
			}
			cancelOID = event.TaskOID
			bound := c.Timeouts.Cancel
			if bound <= compiledStageZeroTimeout {
				bound = taskReportTimeout
			}
			if claimedPickupID != compiledStageAbsentIdentity {
				cancelDeadline, parseErr := time.Parse(time.RFC3339, event.CancelReplyDeadlineUTC)
				if parseErr != nil || cancelDeadline.IsZero() {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
				deadline = cancelDeadline
			} else {
				deadline = time.Now().Add(bound)
			}
			continue
		case ChoreographyClaimRefused, ChoreographyFrameReady:
			continue
		case ChoreographyResultAccepted:
			mailbox, mailboxErr := c.ChoreographyMailbox()
			if mailboxErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, mailboxErr
			}
			// This exact ResultAccepted is already durable. A local object read
			// can fail once without changing its signed identity; retry only the
			// same receipt verification, before any output admission or decision.
			var result Result
			var readErr error
			for readAttempt := compiledStageFirstSignedResultRead; readAttempt < compiledStageSignedResultReadAttempts; readAttempt += compiledStageSignedResultReadIncrement {
				result, readErr = mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(activeCtx,
					c.choreographyBroker.binding, event, c.Limits.MaxManifestBytes)
				if readErr == nil || activeCtx.Err() != nil ||
					readAttempt+compiledStageSignedResultReadIncrement == compiledStageSignedResultReadAttempts {
					break
				}
				select {
				case <-activeCtx.Done():
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, activeCtx.Err()
				case <-time.After(compiledStageSignedResultRetryDelay):
				}
			}
			if readErr != nil {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, readErr
			}
			receiptOID := event.TaskOID
			if event.ResultReceiptOID != compiledStageAbsentIdentity {
				receiptOID = event.ResultReceiptOID
			}
			if event.PickupID != compiledStageAbsentIdentity {
				succeeded := result.Status == StatusSucceeded
				if succeeded && validate != nil {
					succeeded = validate(result) == nil
				}
				c.choreographyBroker.mu.Lock()
				stopping := c.choreographyBroker.stopping
				indexed := c.choreographyBroker.tasks[taskID]
				retryAllowed := indexed != nil && indexed.spec.IsSettledFailureRetryAllowed() &&
					result.Status == StatusFailed
				attemptLimit := choreographyAttemptPolicyFirstAttempt
				if indexed != nil {
					attemptLimit = indexed.spec.EffectiveMaxAttempts()
				}
				var pickup *indexedChoreographyPickup
				if indexed != nil {
					pickup = indexed.pickups[event.PickupID]
				}
				alreadySettled := pickup != nil && pickup.settled
				c.choreographyBroker.mu.Unlock()
				if pickup == nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
				decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
					Task: taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
					Attempt: event.Attempt, Node: event.Node, ResultOID: receiptOID,
					Succeeded: succeeded,
					Retry:     !succeeded && retryAllowed && !stopping && event.Attempt < attemptLimit}
				if !alreadySettled {
					if decisionErr := c.choreographyBroker.DecideAttempt(activeCtx, decision); decisionErr != nil {
						return release.StageOutcome{Node: event.Node,
								UnresolvedOffer: compiledStageUnresolvedFinality},
							fmt.Errorf(compiledStageDecisionUnresolvedFormat, taskID, decisionErr)
					}
				}
				acceptedPickups[receiptOID] = compiledAcceptedPickupResult{event: event, result: result,
					succeeded: succeeded}
				continue
			}
			outcome, admissionErr := admit(activeCtx, event, result)
			decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
				Task: taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
				Attempt: event.Attempt, Node: event.Node,
				ResultOID: receiptOID, Succeeded: admissionErr == nil}
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
			if event.PickupID != compiledStageAbsentIdentity {
				receiptOID := event.TaskOID
				if event.ResultReceiptOID != compiledStageAbsentIdentity {
					receiptOID = event.ResultReceiptOID
				}
				c.choreographyBroker.mu.Lock()
				stopping := c.choreographyBroker.stopping
				indexed := c.choreographyBroker.tasks[taskID]
				retryAllowed := indexed != nil && indexed.spec.RetryAfterUncertainAttempt
				attemptLimit := choreographyAttemptPolicyFirstAttempt
				if indexed != nil {
					attemptLimit = indexed.spec.EffectiveMaxAttempts()
				}
				var pickup *indexedChoreographyPickup
				if indexed != nil {
					pickup = indexed.pickups[event.PickupID]
				}
				alreadySettled := pickup != nil && pickup.settled
				c.choreographyBroker.mu.Unlock()
				if pickup == nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
				decision := ChoreographyDecision{ChoreographyRunBinding: c.choreographyBroker.binding,
					Task: taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
					Attempt: event.Attempt, Node: event.Node, ResultOID: receiptOID,
					Retry: retryAllowed && !stopping && event.Attempt < attemptLimit}
				if !alreadySettled {
					if decisionErr := c.choreographyBroker.DecideAttempt(activeCtx, decision); decisionErr != nil {
						return release.StageOutcome{Node: event.Node,
								UnresolvedOffer: compiledStageUnresolvedFinality},
							fmt.Errorf(compiledStageDecisionUnresolvedFormat, taskID, decisionErr)
					}
				}
				acceptedAcks[receiptOID] = event
				continue
			}
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
			if event.MissingPlacement != string(PlacementWorker) {
				return release.StageOutcome{NoClaim: &release.NoClaimFinality{
						Blocker: event.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}},
					c.refuseTask(taskID, compiledStageAbsentIdentity, event.Attempt,
						fmt.Errorf(compiledStageNoCompatibleNodeFormat, taskID))
			}
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{
					Blocker: event.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}},
				fmt.Errorf(compiledStageNoClaimErrorFormat, taskID)
		case ChoreographyInertCancelled:
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{
					Blocker: event.BlockedBy, SignedTerminal: compiledStageSignedTaskTerminal}},
				fmt.Errorf(compiledStageInertCancelledErrorFormat, taskID)
		case ChoreographyBlocked:
			return release.StageOutcome{NoClaim: &release.NoClaimFinality{Blocker: event.BlockedBy}},
				c.refuseTask(taskID, compiledStageAbsentIdentity, compiledStageFirstAttempt,
					fmt.Errorf(compiledStageBlockedErrorFormat, taskID, event.BlockedBy))
		case ChoreographyRetryReady:
			delete(acceptedPickups, event.ResultReceiptOID)
			delete(acceptedAcks, event.ResultReceiptOID)
			claimOID, assignmentOID, claimedNode, cancelOID =
				compiledStageAbsentIdentity, compiledStageAbsentIdentity,
				compiledStageAbsentIdentity, compiledStageAbsentIdentity
			claimedAttempt = compiledStageNoAttempt
			claimedPickupID = compiledStageAbsentIdentity
			deadline = time.Time{}
			continue
		case ChoreographyPickupContactLost:
			if claimedPickupID == event.PickupID && claimOID == event.ClaimOID {
				claimOID, assignmentOID, claimedNode, cancelOID =
					compiledStageAbsentIdentity, compiledStageAbsentIdentity,
					compiledStageAbsentIdentity, compiledStageAbsentIdentity
				claimedAttempt = compiledStageNoAttempt
				claimedPickupID = compiledStageAbsentIdentity
				deadline = time.Time{}
			}
			continue
		case ChoreographyPickupSettled:
			delete(acceptedPickups, event.ResultReceiptOID)
			delete(acceptedAcks, event.ResultReceiptOID)
			continue
		case ChoreographyTerminal:
			if event.PickupID == compiledStageAbsentIdentity ||
				event.ResultReceiptOID == compiledStageAbsentIdentity {
				return release.StageOutcome{}, fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
			}
			if event.OutputOID == compiledStageAbsentIdentity {
				ack, found := acceptedAcks[event.ResultReceiptOID]
				if !found || ack.TaskOID != event.ResultReceiptOID ||
					ack.PickupID != event.PickupID || ack.ClaimOID != event.ClaimOID ||
					ack.Attempt != event.Attempt || ack.Node != event.Node || event.Succeeded {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
				pending := &choreographyPendingDecision{decision: ChoreographyDecision{
					ChoreographyRunBinding: c.choreographyBroker.binding,
					Task:                   taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
					Attempt: event.Attempt, Node: event.Node, ResultOID: event.ResultReceiptOID},
					packageName: request.Release.Pkg.Name, ackAccepted: compiledStageAcceptedAck,
					decided: compiledStageSignedDecision}
				c.choreographyBroker.mu.Lock()
				indexed := c.choreographyBroker.tasks[taskID]
				preparedTask := indexed != nil && indexed.spec.Kind == string(KindPrepare)
				pending.packageSettlementPending = pending.packageName != compiledStageAbsentIdentity &&
					indexed != nil && !indexed.spec.ExecutorOwned
				c.choreographyBroker.mu.Unlock()
				if preparedTask {
					settleCtx, cancel := context.WithTimeout(context.WithoutCancel(activeCtx), taskReportTimeout)
					settleErr := c.choreographyBroker.SettleTaskPackage(settleCtx,
						ChoreographyTaskPackageSettlement{TaskID: taskID})
					cancel()
					if settleErr != nil {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality}, settleErr
					}
					pending.packageSettlementPending = compiledStagePackageSettlementComplete
				}
				if pending.packageSettlementPending {
					if _, loaded := c.choreographyPending.LoadOrStore(taskID, pending); loaded {
						return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
							fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
					}
				}
				ackErr := ctx.Err()
				if ackErr == nil {
					ackErr = c.refuseTask(taskID, event.Node, event.Attempt,
						fmt.Errorf(compiledStageAckErrorFormat, taskID))
				}
				c.rememberCompiledTaskOutcome(compiledTaskOutcomeRecord{Task: taskID,
					Request: request, Event: event, Err: ackErr})
				return release.StageOutcome{Node: event.Node}, ackErr
			}
			accepted, found := acceptedPickups[event.ResultReceiptOID]
			if !found || accepted.event.PickupID != event.PickupID ||
				accepted.event.Attempt != event.Attempt || accepted.event.Node != event.Node ||
				accepted.event.ClaimOID != event.ClaimOID ||
				accepted.event.OutputOID != event.OutputOID ||
				(event.Succeeded != accepted.succeeded) {
				return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
					fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
			}
			outcome, admissionErr := admit(activeCtx, accepted.event, accepted.result)
			pending := &choreographyPendingDecision{decision: ChoreographyDecision{
				ChoreographyRunBinding: c.choreographyBroker.binding,
				Task:                   taskID, Branch: FormatChoreographyTaskBranch(c.Run, taskID),
				Attempt: event.Attempt, Node: event.Node,
				ResultOID: event.ResultReceiptOID, Succeeded: event.Succeeded},
				packageName:             request.Release.Pkg.Name,
				resultAccepted:          compiledStageAcceptedResult,
				decided:                 compiledStageSignedDecision,
				outputAdmissionRejected: outcome.OutputAdmissionRejected}
			c.choreographyBroker.mu.Lock()
			indexed := c.choreographyBroker.tasks[taskID]
			preparedTask := indexed != nil && indexed.spec.Kind == string(KindPrepare)
			pending.packageSettlementPending = pending.packageName != compiledStageAbsentIdentity &&
				indexed != nil && !indexed.spec.ExecutorOwned
			c.choreographyBroker.mu.Unlock()
			if preparedTask {
				settleCtx, cancel := context.WithTimeout(context.WithoutCancel(activeCtx), taskReportTimeout)
				settleErr := c.choreographyBroker.SettleTaskPackage(settleCtx,
					ChoreographyTaskPackageSettlement{TaskID: taskID,
						OutputAdmissionRejected: outcome.OutputAdmissionRejected})
				cancel()
				if settleErr != nil {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						errors.Join(admissionErr, settleErr)
				}
				pending.packageSettlementPending = compiledStagePackageSettlementComplete
			}
			if pending.packageSettlementPending {
				if _, loaded := c.choreographyPending.LoadOrStore(taskID, pending); loaded {
					return release.StageOutcome{UnresolvedOffer: compiledStageUnresolvedFinality},
						fmt.Errorf(compiledStageWaitIdentityErrorFormat, taskID)
				}
			}
			c.rememberCompiledTaskOutcome(compiledTaskOutcomeRecord{Task: taskID,
				Request: request, Event: accepted.event, Exports: len(outcome.Exports), Err: admissionErr})
			outcome.UnresolvedOffer = compiledStageResolvedFinality
			return outcome, admissionErr
		case ChoreographyNoClaimPackageSettled:
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
	c.choreographyBroker.mu.Lock()
	task := c.choreographyBroker.tasks[pending.decision.Task]
	pending.packageSettlementPending = pending.packageName != compiledStageAbsentIdentity &&
		task != nil
	c.choreographyBroker.mu.Unlock()
	if !pending.packageSettlementPending {
		c.choreographyPending.CompareAndDelete(pending.decision.Task, pending)
	}
	return nil
}
