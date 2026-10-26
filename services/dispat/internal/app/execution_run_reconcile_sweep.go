package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// retainedRunSealed reads the signed ledger from its root. A sealed sweep
// needs no TLS listener and remains cleanable even after its old certificate
// expires; an unsealed run must restore the same live certificate to accept
// outstanding Result or Ack submissions.
func retainedRunSealed(ctx context.Context, persist *execution.GitChoreographyPersist) (bool, error) {
	replay := &retainedSweepAttemptReplay{attempts: make(map[retainedSweepAttemptKey]retainedSweepAttempt)}
	if err := replay.advance(ctx, persist); err != nil {
		return retainedSweepUnsealedState, err
	}
	return replay.sealed, nil
}

type retainedSweepAttempt struct {
	claim       execution.ChoreographyEvent
	cancel      execution.ChoreographyEvent
	reply       execution.ChoreographyEvent
	contactLost bool
}

const retainedSweepPickupContactLostState = true

type retainedSweepAttemptKey struct {
	task     string
	pickupID string
}

type retainedSweepAttemptReplay struct {
	cursor   execution.ChoreographyEventCursor
	attempts map[retainedSweepAttemptKey]retainedSweepAttempt
	sealed   bool
}

const retainedSweepReplyHasNoReceiptErrorFormat = "execution: retained sweep task %s has no exact signed Result or Ack receipt"

// A pickup leaves the canonical task tip at its Claim while the ledger names
// its exact signed Result or Ack as a carried receipt. Local first-node tickets
// retain the older direct task-ref transition, where that tip is the receipt.
func retainedSweepReplyReceiptOID(attempt retainedSweepAttempt) string {
	if attempt.reply.ResultReceiptOID != retainedRunAbsentValueText {
		return attempt.reply.ResultReceiptOID
	}
	// A compiled Ack can still win the canonical Cancel tip before any
	// successor Claim. In that case the signed Ack event advances the task ref
	// itself; only a late side Ack carries a separate ResultReceiptOID.
	if attempt.reply.Kind == execution.ChoreographyAckAccepted &&
		attempt.claim.PickupID != retainedRunAbsentValueText &&
		attempt.cancel.TaskOID != retainedRunAbsentValueText &&
		attempt.reply.TaskOID != retainedRunAbsentValueText &&
		attempt.reply.PickupID == attempt.claim.PickupID &&
		attempt.cancel.PickupID == attempt.claim.PickupID &&
		attempt.cancel.PriorTaskOID == attempt.claim.TaskOID &&
		attempt.reply.PriorTaskOID == attempt.cancel.TaskOID &&
		attempt.reply.TaskOID != attempt.cancel.TaskOID &&
		attempt.reply.Attempt == attempt.claim.Attempt &&
		attempt.reply.Node == attempt.claim.Node {
		return attempt.reply.TaskOID
	}
	if attempt.reply.PickupID == retainedRunAbsentValueText &&
		attempt.reply.TaskOID != attempt.claim.TaskOID {
		return attempt.reply.TaskOID
	}
	return retainedRunAbsentValueText
}

func retainedSweepReplyPackageOutcome(attempt retainedSweepAttempt, succeeded bool) string {
	if succeeded || attempt.reply.Kind == execution.ChoreographyAckAccepted &&
		(attempt.contactLost || attempt.reply.ResultReceiptOID != retainedRunAbsentValueText &&
			attempt.reply.TaskOID == attempt.reply.PriorTaskOID) {
		// An Ack after signed contact loss only settles the old pickup's
		// retained capacity, even when it still advances the Cancel tip.
		// The successor owns the package outcome.
		return retainedRunAbsentValueText
	}
	return string(release.OutcomeFailed)
}

func (r *retainedSweepAttemptReplay) applyEvent(event execution.ChoreographyEvent) {
	key := retainedSweepAttemptKey{task: event.Task, pickupID: event.PickupID}
	switch event.Kind {
	case execution.ChoreographyClaimAccepted:
		r.attempts[key] = retainedSweepAttempt{claim: event}
	case execution.ChoreographyCancelIssued:
		attempt := r.attempts[key]
		attempt.cancel = event
		r.attempts[key] = attempt
	case execution.ChoreographyResultAccepted, execution.ChoreographyAckAccepted:
		attempt := r.attempts[key]
		attempt.reply = event
		r.attempts[key] = attempt
	case execution.ChoreographyPickupContactLost:
		attempt := r.attempts[key]
		attempt.contactLost = retainedSweepPickupContactLostState
		r.attempts[key] = attempt
	case execution.ChoreographyRetryReady, execution.ChoreographyTerminal,
		execution.ChoreographyPickupSettled:
		delete(r.attempts, key)
	case execution.ChoreographyRunSealed:
		r.sealed = retainedSweepSealedState
	}
}

func (r *retainedSweepAttemptReplay) advance(ctx context.Context,
	persist *execution.GitChoreographyPersist) error {
	for {
		page, err := persist.ReadEvents(ctx, r.cursor, retainedRunEventPageSizeCount)
		if err != nil {
			return fmt.Errorf(retainedSweepEventReadErrorFormat, err)
		}
		if page.Cursor.Sequence != r.cursor.Sequence+uint64(len(page.Events)) ||
			len(page.Events) == retainedRunNoEntriesCount && page.More {
			return fmt.Errorf(retainedSweepEventSequenceGapErrorText)
		}
		for _, event := range page.Events {
			r.applyEvent(event)
		}
		r.cursor = page.Cursor
		if !page.More {
			return nil
		}
	}
}

type retainedSweepSettlementOptions struct {
	Identity firstRunIdentity
	Signer   *execution.Signer
	Limits   execution.TransferLimits
	Binding  execution.ChoreographyRunBinding
	Mailbox  *execution.GitMailbox
	Broker   *execution.FirstNodeBroker
	Persist  *execution.GitChoreographyPersist
}

// settleRetainedSweepAttempts stops new claims, serves the old pinned HTTPS
// identity for existing workers, and decides only outcomes proved by signed
// ResultAccepted or AckAccepted events. A live command whose stop is unknown
// leaves the old generation and its physical capacity fenced after the wait.
func (a *App) settleRetainedSweepAttempts(ctx context.Context, options retainedSweepSettlementOptions) (err error) {
	identity, signer, limits, binding := options.Identity, options.Signer, options.Limits, options.Binding
	mailbox, broker, persist := options.Mailbox, options.Broker, options.Persist
	replay := &retainedSweepAttemptReplay{attempts: make(map[retainedSweepAttemptKey]retainedSweepAttempt)}
	if err := replay.advance(ctx, persist); err != nil {
		return err
	}
	var channel *runReturnChannel
	defer func() {
		if channel != nil {
			err = errors.Join(err, channel.close())
		}
	}()
	deadline := time.NewTimer(retainedSweepSettlementTimeoutSeconds * time.Second)
	defer deadline.Stop()
	for {
		if err := replay.advance(ctx, persist); err != nil {
			return err
		}
		if replay.sealed {
			return fmt.Errorf(retainedSweepSealDuringReconciliationErrorText)
		}
		if len(replay.attempts) == retainedRunNoEntriesCount {
			return nil
		}
		advanced := retainedSweepNoProgressState
		for attemptKey, attempt := range replay.attempts {
			task := attemptKey.task
			if attempt.claim.TaskOID == retainedRunAbsentValueText {
				return fmt.Errorf(retainedSweepReplyWithoutClaimErrorFormat, task)
			}
			if attempt.reply.TaskOID != retainedRunAbsentValueText {
				receiptOID := retainedSweepReplyReceiptOID(attempt)
				if receiptOID == retainedRunAbsentValueText {
					return fmt.Errorf(retainedSweepReplyHasNoReceiptErrorFormat, task)
				}
				succeeded := retainedSweepUnsuccessfulState
				if attempt.reply.Kind == execution.ChoreographyResultAccepted {
					result, err := mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(
						ctx, binding, attempt.reply, int64(limits.MaxManifestBytes))
					if err != nil {
						return fmt.Errorf(retainedSweepInadmissibleResultErrorFormat, task, err)
					}
					succeeded = result.Status == execution.StatusSucceeded
				} else if attempt.reply.Kind != execution.ChoreographyAckAccepted ||
					attempt.cancel.TaskOID == retainedRunAbsentValueText {
					return fmt.Errorf(retainedSweepUnverifiedTaskStopErrorFormat, task)
				}
				outcome := retainedSweepReplyPackageOutcome(attempt, succeeded)
				if err := broker.DecideAttempt(ctx, execution.ChoreographyDecision{
					ChoreographyRunBinding: binding, Task: task,
					Branch:  execution.FormatChoreographyTaskBranch(binding.Run, task),
					Attempt: attempt.reply.Attempt, Node: attempt.reply.Node,
					ResultOID: receiptOID, Succeeded: succeeded,
					PackageOutcome: outcome,
				}); err != nil {
					return fmt.Errorf(retainedSweepResultFinalityErrorFormat, task, err)
				}
				advanced = retainedSweepProgressState
				continue
			}
			if channel == nil {
				var err error
				transferTimeout, err := a.configuredResultTransferTimeout()
				if err != nil {
					return err
				}
				channel, err = openRetainedRunReturnChannel(retainedRunReturnChannelOptions{
					identity: identity, signer: signer, limits: limits,
					resultTransferTimeout: transferTimeout,
					binding:               binding, broker: broker})
				if err != nil {
					return err
				}
			}
			if attempt.cancel.TaskOID == retainedRunAbsentValueText {
				_, err := broker.CancelClaim(ctx, execution.ChoreographyCancellation{
					ChoreographyRunBinding: binding, Task: task,
					Branch:  execution.FormatChoreographyTaskBranch(binding.Run, task),
					Attempt: attempt.claim.Attempt, Node: attempt.claim.Node,
					AssignmentOID: attempt.claim.AssignmentOID, ClaimOID: attempt.claim.TaskOID,
				})
				if err != nil && !errors.Is(err, execution.ErrChoreographyResultWon) {
					return fmt.Errorf(retainedSweepTaskCancellationErrorFormat, task, err)
				}
				advanced = retainedSweepProgressState
			}
		}
		if advanced {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf(retainedSweepUnverifiedAcceptedStopErrorText)
		case <-time.After(retainedSweepPollIntervalMilliseconds * time.Millisecond):
		}
	}
}
