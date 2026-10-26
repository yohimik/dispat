package app

import (
	"context"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type firstReleaseSlotShape struct {
	weight int
}

const localTaskSettlementLeaseMissingError = "execution: local task settlement has no exact held lease"

// Outcome hooks run on the first node after a delegated frame stops. They
// acquire a physical slot only when no first-node task in the package still
// holds one; an accepted first-local Result can keep its Worker slot through
// this hook until the final Terminal decision.
func (a *App) installChoreographyOutcomeHookSlots(executor *release.Executor,
	pl *plan.Plan, c *execution.Coordinator) error {
	if a == nil || executor == nil || pl == nil || c == nil || a.localNodeSlots == nil {
		return fmt.Errorf(choreographyMissingOutcomeSlotAuthorityErrorText)
	}
	weights := make(map[string]int, len(pl.Releases))
	for name, rel := range pl.Releases {
		if rel == nil || rel.Pkg == nil || rel.Pkg.BuildWeight <= choreographyNoCapacityCount ||
			rel.Pkg.BuildWeight > c.Local.Capacity {
			return fmt.Errorf(choreographyOutcomeHookCapacityErrorFormat, name)
		}
		weights[name] = rel.Pkg.BuildWeight
	}
	slots := a.localNodeSlots
	wait := time.Duration(a.cfg.Execution.ResolveTimeouts().Task) * time.Second
	executor.LocalOutcomeLeaseReusable = func(ctx context.Context, pkg string) (bool, error) {
		if weights[pkg] == choreographyNoEntriesCount {
			return choreographyOutcomeLeaseUnavailableState, fmt.Errorf(choreographyUnknownOutcomePackageErrorFormat, pkg)
		}
		return c.HasStoppedFirstLocalStage(ctx, pkg, weights[pkg], slots)
	}
	executor.LocalOutcomeHookLifecycle = func(ctx context.Context, pkg, hook string) (
		func(context.Context, string) error, error) {
		weight := weights[pkg]
		if hook == choreographyAbsentIdentityText {
			hook = choreographyRevertHookName
		}
		if weight == choreographyNoCapacityCount || (hook != choreographyFailureHookName && hook != choreographySkipHookName && hook != choreographyRevertHookName) {
			return nil, fmt.Errorf(choreographyUnknownOutcomeHookErrorFormat, hook, pkg)
		}
		id := choreographyOutcomeTaskPrefixText + pkg + choreographyTaskPartSeparatorText + hook
		lease, err := waitLocalSweepSlot(ctx, slots, execution.NodeSlotIntent{
			Run: c.Run, Task: id, Attempt: choreographyInitialAttemptCount, Weight: weight,
		}, wait)
		if err != nil {
			return nil, err
		}
		return func(settleCtx context.Context, status string) error {
			return slots.ReleaseLocalSettled(settleCtx, lease.ID,
				execution.NodeSlotLocalCompletion{Run: c.Run,
					Task: id, Attempt: choreographyInitialAttemptCount, Status: status})
		}, nil
	}
	return nil
}

type choreographyReleaseSlotOptions struct {
	Executor    *release.Executor
	Plan        *plan.Plan
	Compiled    *release.CompiledTaskGraph
	Coordinator *execution.Coordinator
}

// installChoreographyReleaseSlots joins native first-node commands to the
// same physical node capacity as ordinary Worker frames. Executor's
// ReserveFirstOnlyTask obtains the Pool lease before entering this callback.
// A delegated Stage obtains its own physical slot in Worker after the signed
// Claim, so this callback intentionally does not reserve one for it.
func (a *App) installChoreographyReleaseSlots(options choreographyReleaseSlotOptions) error {
	executor, pl, compiled, coordinator := options.Executor, options.Plan, options.Compiled, options.Coordinator

	if a == nil || executor == nil || pl == nil || compiled == nil || coordinator == nil ||
		a.localNodeSlots == nil || coordinator.Run == choreographyAbsentIdentityText {
		return fmt.Errorf(choreographyMissingReleaseSlotAuthorityErrorText)
	}
	shapes := make(map[string]firstReleaseSlotShape)
	capacity := coordinator.Local.Capacity
	for _, shape := range compiled.TaskShapes() {
		if shape.Virtual || shape.Kind == execution.KindPrepare {
			continue
		}
		if !shape.FirstOnly && shape.Kind != choreographyPublishStageName && shape.Kind != choreographyRecordStageName {
			continue
		}
		weight := shape.Weight
		if rel := pl.Releases[shape.Package]; rel != nil && rel.Pkg != nil && rel.Pkg.Space != nil {
			space := rel.Pkg.Space
			if space.RevertOnFail || len(space.OnFailScript) > choreographyNoEntriesCount || len(space.OnSkipScript) > choreographyNoEntriesCount {
				weight = max(weight, rel.Pkg.BuildWeight)
			}
		}
		if weight <= choreographyNoCapacityCount || weight > capacity {
			return fmt.Errorf(choreographyFirstOnlyNodeCapacityErrorFormat, shape.ID)
		}
		shapes[shape.ID] = firstReleaseSlotShape{weight: weight}
	}
	slots := a.localNodeSlots
	wait := time.Duration(a.cfg.Execution.ResolveTimeouts().Task) * time.Second
	executor.LocalTaskLifecycle = func(ctx context.Context, taskID string) (func(context.Context, string) error, error) {
		shape, local := shapes[taskID]
		if !local {
			// Ordinary Stage frames claim their physical slot in Worker. The
			// shared Executor hook still requires a settlement closure.
			return func(context.Context, string) error { return nil }, nil
		}
		locator, err := coordinator.NativeNodeSlotLocator(ctx, taskID)
		if err != nil {
			return nil, err
		}
		lease, err := waitLocalSweepSlot(ctx, slots, execution.NodeSlotIntent{
			Run: coordinator.Run, Task: taskID, Attempt: choreographyInitialAttemptCount, Weight: shape.weight,
			NativeRecovery: &locator,
		}, wait)
		if err != nil {
			return nil, err
		}
		a.localTaskLeases.Store(taskID, lease)
		return func(settleCtx context.Context, status string) error {
			// The Executor calls this after native ticket/result finality. A
			// failed settlement retains the durable lease for cold recovery.
			current, held := a.localTaskLeases.Load(taskID)
			settledLease, valid := current.(execution.NodeSlotLease)
			if !held || !valid || settledLease.ID == choreographyAbsentIdentityText {
				return fmt.Errorf(localTaskSettlementLeaseMissingError)
			}
			err := slots.ReleaseLocalSettled(settleCtx, settledLease.ID,
				execution.NodeSlotLocalCompletion{Run: coordinator.Run,
					Task: taskID, Attempt: choreographyInitialAttemptCount, Status: status})
			if err == nil {
				a.localTaskLeases.Delete(taskID)
			}
			return err
		}, nil
	}
	return nil
}
