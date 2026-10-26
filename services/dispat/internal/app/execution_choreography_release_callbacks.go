package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// bindReleaseExecutorChoreography routes each shared Executor transition to
// the first node's signed broker. The compiled graph is the sole task-kind
// index; callback code never parses task identifiers to assign authority.
func (a *App) bindReleaseExecutorChoreography(executor *release.Executor,
	pl *plan.Plan, compiled *release.CompiledTaskGraph, c *execution.Coordinator,
	broker *execution.FirstNodeBroker) error {
	if a == nil || executor == nil || pl == nil || compiled == nil || c == nil ||
		broker == nil {
		return fmt.Errorf("execution: release choreography broker is not bound")
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
		if !ok || shape.Package == "" || blocker == "" {
			return fmt.Errorf("execution: blocked release task has no compiled identity or cause")
		}
		return broker.AcceptBlocked(ctx, execution.ChoreographyBlockedOutcome{
			ChoreographyRunBinding: binding, Task: taskID, Package: shape.Package,
			BlockedBy: blocker, PackageOutcome: string(outcome),
		})
	}
	executor.ReserveFirstOnlyTask = c.ReserveFirstLocalPool
	executor.ProveDelegatedNoClaim = c.ProveChoreographyNoClaim
	executor.BeginFirstOnlyTask = func(ctx context.Context, taskID string) (
		func(context.Context, bool, release.PackageOutcome) error, error) {
		shape, ok := shapes[taskID]
		if !ok || !shape.FirstOnly || shape.Virtual {
			return nil, fmt.Errorf("execution: first-only ticket has no compiled native task")
		}
		var ticket *execution.ChoreographyLocalTicket
		if err := a.retryChoreographyLocalClaim(ctx, c, broker, taskID,
			func(lease execution.NodeSlotLease) error {
				var claimErr error
				ticket, claimErr = c.BeginChoreographyLocalTask(ctx, taskID, lease)
				return claimErr
			}); err != nil {
			return nil, err
		}
		return ticket.Finish, nil
	}
	executor.BlockFirstOnlyTask = func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape := shapes[taskID]
		if !shape.FirstOnly || shape.Virtual {
			return fmt.Errorf("execution: first-only block names another task")
		}
		return c.BlockChoreographyLocalTask(ctx, taskID, blocker, outcome)
	}
	executor.BlockDelegatedTask = func(ctx context.Context, taskID, blocker string,
		outcome release.PackageOutcome) error {
		shape := shapes[taskID]
		if shape.FirstOnly || shape.Virtual || shape.Kind == "publish" || shape.Kind == "record" {
			return fmt.Errorf("execution: delegated block names another task")
		}
		return blocked(ctx, taskID, blocker, outcome)
	}
	executor.BlockDelegatedFailure = func(ctx context.Context, taskID string,
		evidence release.DelegatedFailureEvidence, outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || shape.FirstOnly || shape.Virtual || shape.Kind == "publish" ||
			shape.Kind == "record" {
			return fmt.Errorf("execution: delegated failure names task %q without a delegated compiled vertex", taskID)
		}
		return c.BlockChoreographyDelegatedFailure(ctx,
			execution.ChoreographyDelegatedFailure{TaskID: taskID,
				Part: evidence.Part, ErrorSHA256: evidence.ErrorSHA256, Outcome: outcome})
	}
	executor.TaskOutcomeSettled = func(ctx context.Context, taskID string,
		outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || shape.Virtual || shape.FirstOnly || shape.Kind == execution.KindPrepare ||
			shape.Kind == "publish" || shape.Kind == "record" {
			return fmt.Errorf("execution: task settlement names a nondelegated vertex")
		}
		return c.SettleChoreographyTask(ctx, taskID, outcome)
	}
	executor.BeginLocalAuthorityTask = func(ctx context.Context, taskID string) error {
		shape, ok := shapes[taskID]
		if !ok || (shape.Kind != "publish" && shape.Kind != "record") || shape.Virtual {
			return fmt.Errorf("execution: local authority claim names another compiled task")
		}
		var receipt execution.ChoreographyReceipt
		if err := a.retryChoreographyLocalClaim(ctx, c, broker, taskID,
			func(lease execution.NodeSlotLease) error {
				var claimErr error
				receipt, claimErr = c.ClaimChoreographyLocalTask(ctx, taskID, lease)
				return claimErr
			}); err != nil {
			return err
		}
		if !receipt.Accepted || receipt.Refused || receipt.OID == "" ||
			receipt.AssignmentOID == "" || receipt.GraphRootOID != c.GraphRootOID ||
			receipt.Task != taskID || receipt.Node != c.Local.Name {
			return fmt.Errorf("execution: local authority task lacks its exact signed claim receipt")
		}
		return nil
	}
	executor.LocalAuthoritySettled = func(ctx context.Context, taskID string,
		rel *plan.Release, succeeded bool, outcome release.PackageOutcome) error {
		shape, ok := shapes[taskID]
		if !ok || (shape.Kind != "publish" && shape.Kind != "record") ||
			rel == nil || rel.Pkg == nil || shape.Package != rel.Pkg.Name {
			return fmt.Errorf("execution: local release effect names another compiled task")
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
		if shape.Kind != "publish" && shape.Kind != "record" {
			return fmt.Errorf("execution: local authority block names another task")
		}
		return blocked(ctx, taskID, blocker, outcome)
	}
	if err := a.installChoreographyReleaseSlots(executor, pl, compiled, c); err != nil {
		return err
	}
	return a.installChoreographyOutcomeHookSlots(executor, pl, c)
}
