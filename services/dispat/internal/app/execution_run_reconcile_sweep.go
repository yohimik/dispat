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
	replay := &retainedSweepAttemptReplay{attempts: make(map[string]retainedSweepAttempt)}
	if err := replay.advance(ctx, persist); err != nil {
		return false, err
	}
	return replay.sealed, nil
}

type retainedSweepAttempt struct {
	claim  execution.ChoreographyEvent
	cancel execution.ChoreographyEvent
	reply  execution.ChoreographyEvent
}

type retainedSweepAttemptReplay struct {
	cursor   execution.ChoreographyEventCursor
	attempts map[string]retainedSweepAttempt
	sealed   bool
}

func (r *retainedSweepAttemptReplay) advance(ctx context.Context,
	persist *execution.GitChoreographyPersist) error {
	for {
		page, err := persist.ReadEvents(ctx, r.cursor, 32)
		if err != nil {
			return fmt.Errorf("execution: retained sweep events cannot be read: %w", err)
		}
		if page.Cursor.Sequence != r.cursor.Sequence+uint64(len(page.Events)) ||
			len(page.Events) == 0 && page.More {
			return fmt.Errorf("execution: retained sweep event sequence has a gap")
		}
		for _, event := range page.Events {
			switch event.Kind {
			case execution.ChoreographyClaimAccepted:
				r.attempts[event.Task] = retainedSweepAttempt{claim: event}
			case execution.ChoreographyCancelIssued:
				attempt := r.attempts[event.Task]
				attempt.cancel = event
				r.attempts[event.Task] = attempt
			case execution.ChoreographyResultAccepted, execution.ChoreographyAckAccepted:
				attempt := r.attempts[event.Task]
				attempt.reply = event
				r.attempts[event.Task] = attempt
			case execution.ChoreographyRetryReady, execution.ChoreographyTerminal,
				execution.ChoreographyBlocked:
				delete(r.attempts, event.Task)
			case execution.ChoreographyRunSealed:
				r.sealed = true
			}
		}
		r.cursor = page.Cursor
		if !page.More {
			return nil
		}
	}
}

// settleRetainedSweepAttempts stops new claims, serves the old pinned HTTPS
// identity for existing workers, and decides only outcomes proved by signed
// ResultAccepted or AckAccepted events. A live command whose stop is unknown
// leaves the old generation and its physical capacity fenced after the wait.
func (a *App) settleRetainedSweepAttempts(ctx context.Context, identity firstRunIdentity,
	signer *execution.Signer, limits execution.TransferLimits,
	binding execution.ChoreographyRunBinding, mailbox *execution.GitMailbox,
	broker *execution.FirstNodeBroker, persist *execution.GitChoreographyPersist) (err error) {
	replay := &retainedSweepAttemptReplay{attempts: make(map[string]retainedSweepAttempt)}
	if err := replay.advance(ctx, persist); err != nil {
		return err
	}
	var channel *runReturnChannel
	defer func() {
		if channel != nil {
			err = errors.Join(err, channel.close())
		}
	}()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		if err := replay.advance(ctx, persist); err != nil {
			return err
		}
		if replay.sealed {
			return fmt.Errorf("execution: retained sweep was sealed during reconciliation")
		}
		if len(replay.attempts) == 0 {
			return nil
		}
		advanced := false
		for task, attempt := range replay.attempts {
			if attempt.claim.TaskOID == "" {
				return fmt.Errorf("execution: retained sweep task %s has a reply without a signed claim", task)
			}
			if attempt.reply.TaskOID != "" {
				succeeded := false
				if attempt.reply.Kind == execution.ChoreographyResultAccepted {
					result, err := mailbox.ReadVerifiedChoreographyAcceptedResultFromAssignment(
						ctx, binding, attempt.reply, int64(limits.MaxManifestBytes))
					if err != nil {
						return fmt.Errorf("execution: retained sweep result %s is not admissible: %w", task, err)
					}
					succeeded = result.Status == execution.StatusSucceeded
				} else if attempt.reply.Kind != execution.ChoreographyAckAccepted ||
					attempt.cancel.TaskOID == "" {
					return fmt.Errorf("execution: retained sweep task %s has no verified stop", task)
				}
				outcome := ""
				if !succeeded {
					outcome = string(release.OutcomeFailed)
				}
				if err := broker.DecideAttempt(ctx, execution.ChoreographyDecision{
					ChoreographyRunBinding: binding, Task: task,
					Branch:  execution.FormatChoreographyTaskBranch(binding.Run, task),
					Attempt: attempt.reply.Attempt, Node: attempt.reply.Node,
					ResultOID: attempt.reply.TaskOID, Succeeded: succeeded,
					PackageOutcome: outcome,
				}); err != nil {
					return fmt.Errorf("execution: retained sweep result %s cannot reach finality: %w", task, err)
				}
				advanced = true
				continue
			}
			if channel == nil {
				var err error
				channel, err = openRetainedRunReturnChannel(retainedRunReturnChannelOptions{
					identity: identity, signer: signer, limits: limits,
					binding: binding, broker: broker})
				if err != nil {
					return err
				}
			}
			if attempt.cancel.TaskOID == "" {
				_, err := broker.CancelClaim(ctx, execution.ChoreographyCancellation{
					ChoreographyRunBinding: binding, Task: task,
					Branch:  execution.FormatChoreographyTaskBranch(binding.Run, task),
					Attempt: attempt.claim.Attempt, Node: attempt.claim.Node,
					AssignmentOID: attempt.claim.PriorTaskOID, ClaimOID: attempt.claim.TaskOID,
				})
				if err != nil && !errors.Is(err, execution.ErrChoreographyResultWon) {
					return fmt.Errorf("execution: retained sweep task %s cannot be cancelled: %w", task, err)
				}
				advanced = true
			}
		}
		if advanced {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("execution: retained sweep has an accepted attempt without verified stop")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
