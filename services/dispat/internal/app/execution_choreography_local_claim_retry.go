package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	localClaimLeaseMissingErrorFormat = "execution: first-node task %q has no exact held physical lease"
	localClaimRefusalFenceErrorFormat = "execution: first-node task %q has no exact signed refusal fence"
)

type choreographyLocalClaimRetryOptions struct {
	Coordinator *execution.Coordinator
	Broker      *execution.FirstNodeBroker
	TaskID      string
	Claim       func(execution.NodeSlotLease) error
}

// retryChoreographyLocalClaim releases an unaccepted physical lease only
// after a definitive signed refusal. A lost/unknown reply retains it for
// exact recovery. Budget waits are read-only and hold no physical slot.
func (a *App) retryChoreographyLocalClaim(ctx context.Context, options choreographyLocalClaimRetryOptions) error {
	c, broker, taskID, claim := options.Coordinator, options.Broker, options.TaskID, options.Claim

	for {
		value, ok := a.localTaskLeases.Load(taskID)
		lease, valid := value.(execution.NodeSlotLease)
		if !ok || !valid || lease.ID == choreographyAbsentIdentityText {
			return fmt.Errorf(localClaimLeaseMissingErrorFormat, taskID)
		}
		err := claim(lease)
		if err == nil {
			return nil
		}
		var refused *execution.ChoreographyClaimRefusedError
		if !errors.As(err, &refused) {
			return err
		}
		receipt := refused.Receipt
		if !receipt.Refused || receipt.Accepted || receipt.Task != taskID ||
			receipt.Node != c.Local.Name || receipt.GraphRootOID != c.GraphRootOID ||
			receipt.FenceOID == choreographyAbsentIdentityText || receipt.FenceSequence == choreographyAbsentFenceSequence {
			return fmt.Errorf(localClaimRefusalFenceErrorFormat, taskID)
		}
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), release.RecordTimeout)
		settleErr := a.localNodeSlots.ReleaseSettled(settleCtx, lease.ID, receipt.FenceOID)
		cancel()
		if settleErr != nil {
			return settleErr
		}
		a.localTaskLeases.Delete(taskID)
		if receipt.RefusalReason == execution.ChoreographyRefusalStopping {
			return execution.ErrChoreographyRunStopping
		}
		if receipt.RefusalReason != execution.ChoreographyRefusalCapacity {
			return err
		}
		if err := broker.WaitClaimBudget(ctx, taskID, c.Local.Name); err != nil {
			return err
		}
		locator, err := c.NativeNodeSlotLocator(ctx, taskID)
		if err != nil {
			return err
		}
		wait := time.Duration(a.cfg.Execution.ResolveTimeouts().Task) * time.Second
		next, err := waitLocalSweepSlot(ctx, a.localNodeSlots, execution.NodeSlotIntent{
			Run: c.Run, Task: taskID, Attempt: lease.Intent.Attempt,
			Weight: lease.Intent.Weight, NativeRecovery: &locator,
		}, wait)
		if err != nil {
			return err
		}
		a.localTaskLeases.Store(taskID, next)
	}
}
