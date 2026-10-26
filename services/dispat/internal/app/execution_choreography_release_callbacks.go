package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const choreographySignedInertCancelErrorFormat = "execution: signing inert cancellation for task %q: %w"

func cancelStoppedChoreographyLocalTask(ctx context.Context, broker *execution.FirstNodeBroker,
	taskID string, err error) error {
	if !errors.Is(err, execution.ErrChoreographyRunStopping) {
		return err
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), release.RecordTimeout)
	defer cancel()
	if cancelErr := broker.CancelInertTask(settleCtx, taskID); cancelErr != nil {
		return fmt.Errorf(choreographySignedInertCancelErrorFormat, taskID, cancelErr)
	}
	return release.ErrSignedPreEffectCancellation
}

type releaseExecutorChoreographyOptions struct {
	Executor    *release.Executor
	Plan        *plan.Plan
	Compiled    *release.CompiledTaskGraph
	Coordinator *execution.Coordinator
	Broker      *execution.FirstNodeBroker
}

// bindReleaseExecutorChoreography routes each shared Executor transition to
// the first node's signed broker. The compiled graph is the sole task-kind
// index; callback code never parses task identifiers to assign authority.
func (a *App) bindReleaseExecutorChoreography(options releaseExecutorChoreographyOptions) error {
	executor, pl, compiled, c, broker := options.Executor, options.Plan, options.Compiled, options.Coordinator, options.Broker

	if a == nil || executor == nil || pl == nil || compiled == nil || c == nil ||
		broker == nil {
		return fmt.Errorf(choreographyUnboundReleaseBrokerErrorText)
	}
	shapes := make(map[string]release.TaskShape)
	for _, shape := range compiled.TaskShapes() {
		shapes[shape.ID] = shape
	}
	binding := execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
		PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest}
	blocked := func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || shape.Package == choreographyAbsentIdentityText || blocker == choreographyAbsentIdentityText {
			return fmt.Errorf(choreographyMissingBlockedIdentityErrorText)
		}
		return broker.AcceptBlocked(ctx, execution.ChoreographyBlockedOutcome{
			ChoreographyRunBinding: binding, Task: taskID, Package: shape.Package,
			BlockedBy: blocker, PackageOutcome: string(outcome),
		})
	}
	executor.ReserveFirstOnlyTask = func(ctx context.Context, taskID string) (func(), error) {
		releasePool, err := c.ReserveFirstLocalPool(ctx, taskID)
		if err != nil {
			return nil, cancelStoppedChoreographyLocalTask(ctx, broker, taskID, err)
		}
		return releasePool, nil
	}
	executor.ProveDelegatedNoClaim = c.ProveChoreographyNoClaim
	executor.BeginFirstOnlyTask = func(ctx context.Context, taskID string) (
		func(context.Context, bool, release.PackageOutcome) error, error) {
		shape, ok := shapes[taskID]
		if !ok || !shape.FirstOnly || shape.Virtual {
			return nil, fmt.Errorf(choreographyMissingNativeTicketTaskErrorText)
		}
		var ticket *execution.ChoreographyLocalTicket
		if err := a.retryChoreographyLocalClaim(ctx, choreographyLocalClaimRetryOptions{
			Coordinator: c,
			Broker:      broker,
			TaskID:      taskID,
			Claim: func(lease execution.NodeSlotLease) error {
				var claimErr error
				ticket, claimErr = c.BeginChoreographyLocalTask(ctx, taskID, lease)
				return claimErr
			},
		}); err != nil {
			return nil, cancelStoppedChoreographyLocalTask(ctx, broker, taskID, err)
		}
		return ticket.Finish, nil
	}
	executor.BlockFirstOnlyTask = func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape := shapes[taskID]
		if !shape.FirstOnly || shape.Virtual {
			return fmt.Errorf(choreographyFirstOnlyBlockMismatchErrorText)
		}
		return c.BlockChoreographyLocalTask(ctx, taskID, blocker, outcome)
	}
	executor.BlockDelegatedTask = func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape := shapes[taskID]
		if shape.FirstOnly || shape.Virtual || shape.Kind == choreographyPublishStageName || shape.Kind == choreographyRecordStageName {
			return fmt.Errorf(choreographyDelegatedBlockMismatchErrorText)
		}
		return blocked(ctx, taskID, blocker, outcome)
	}
	executor.BlockDelegatedFailure = func(ctx context.Context, taskID string,
		evidence release.DelegatedFailureEvidence, outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || shape.FirstOnly || shape.Virtual || shape.Kind == choreographyPublishStageName ||
			shape.Kind == choreographyRecordStageName {
			return fmt.Errorf(choreographyNondelegatedFailureErrorFormat, taskID)
		}
		return c.BlockChoreographyDelegatedFailure(ctx,
			execution.ChoreographyDelegatedFailure{TaskID: taskID,
				Part: evidence.Part, ErrorSHA256: evidence.ErrorSHA256, Outcome: outcome})
	}
	executor.TaskOutcomeSettled = func(ctx context.Context, taskID string,
		outcome release.PackageOutcome) error {
		if broker.IsInertCancelled(taskID) {
			return c.SettleChoreographyTask(ctx, taskID, outcome)
		}
		shape, ok := shapes[taskID]
		if !ok || shape.Virtual || shape.FirstOnly || shape.Kind == execution.KindPrepare ||
			shape.Kind == choreographyPublishStageName || shape.Kind == choreographyRecordStageName {
			return fmt.Errorf(choreographyNondelegatedSettlementErrorText)
		}
		return c.SettleChoreographyTask(ctx, taskID, outcome)
	}
	executor.BeginLocalAuthorityTask = func(ctx context.Context, taskID string) error {
		shape, ok := shapes[taskID]
		if !ok || (shape.Kind != choreographyPublishStageName && shape.Kind != choreographyRecordStageName) || shape.Virtual {
			return fmt.Errorf(choreographyLocalAuthorityClaimMismatchErrorText)
		}
		var receipt execution.ChoreographyReceipt
		if err := a.retryChoreographyLocalClaim(ctx, choreographyLocalClaimRetryOptions{
			Coordinator: c,
			Broker:      broker,
			TaskID:      taskID,
			Claim: func(lease execution.NodeSlotLease) error {
				var claimErr error
				receipt, claimErr = c.ClaimChoreographyLocalTask(ctx, taskID, lease)
				return claimErr
			},
		}); err != nil {
			return cancelStoppedChoreographyLocalTask(ctx, broker, taskID, err)
		}
		if !receipt.Accepted || receipt.Refused || receipt.OID == choreographyAbsentIdentityText ||
			receipt.AssignmentOID == choreographyAbsentIdentityText || receipt.GraphRootOID != c.GraphRootOID ||
			receipt.Task != taskID || receipt.Node != c.Local.Name {
			return fmt.Errorf(choreographyMissingLocalClaimReceiptErrorText)
		}
		return nil
	}
	executor.LocalAuthoritySettled = func(ctx context.Context, taskID string,
		rel *plan.Release, succeeded bool, outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || (shape.Kind != choreographyPublishStageName && shape.Kind != choreographyRecordStageName) ||
			rel == nil || rel.Pkg == nil || shape.Package != rel.Pkg.Name {
			return fmt.Errorf(choreographyLocalEffectTaskMismatchErrorText)
		}
		evidence, err := a.releaseEffectEvidence(ctx, releaseEffectRequest{
			Coordinator: c, Task: taskID, Kind: shape.Kind,
			Release: rel, Succeeded: succeeded, Outcome: outcome,
		})
		if err != nil {
			return err
		}
		digest, err := execution.DigestChoreographyLocalEffect(evidence)
		if err != nil {
			return err
		}
		return broker.AcceptLocalOutcome(ctx, execution.ChoreographyLocalOutcome{
			ChoreographyRunBinding: binding, Task: taskID, Node: c.Local.Name,
			EffectEvidence: evidence, EffectDigest: digest,
			Succeeded: succeeded, PackageOutcome: string(outcome),
		})
	}
	executor.BlockLocalAuthorityTask = func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape := shapes[taskID]
		if shape.Kind != choreographyPublishStageName && shape.Kind != choreographyRecordStageName {
			return fmt.Errorf(choreographyLocalAuthorityBlockMismatchErrorText)
		}
		return blocked(ctx, taskID, blocker, outcome)
	}
	if err := a.installChoreographyReleaseSlots(choreographyReleaseSlotOptions{
		Executor:    executor,
		Plan:        pl,
		Compiled:    compiled,
		Coordinator: c,
	}); err != nil {
		return err
	}
	return a.installChoreographyOutcomeHookSlots(executor, pl, c)
}
