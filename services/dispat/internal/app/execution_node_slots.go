package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// sharedNodeSlotRoot is independent of the worker's replaceable cache
// directory. An unresolved signed attempt must survive cache eviction and a
// second process started for the same logical node.
func sharedNodeSlotRoot() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return retainedRunAbsentValueText, fmt.Errorf(sharedNodeSlotConfigurationPathErrorFormat, err)
	}
	if !filepath.IsAbs(configDir) {
		return retainedRunAbsentValueText, fmt.Errorf(sharedNodeSlotRelativeConfigurationPathErrorText)
	}
	return filepath.Join(configDir, firstRunJournalApplicationDirectoryName, sharedNodeSlotDirectoryName), nil
}

func openSharedNodeSlots(node string, capacity int) (*execution.NodeSlotAuthority, error) {
	root, err := sharedNodeSlotRoot()
	if err != nil {
		return nil, err
	}
	return execution.OpenNodeSlotAuthority(root, node, capacity)
}

// prepareExplicitLocalSlots is the no-link path for a stated node capacity.
// It deliberately creates no return listener or per-run TLS journal. An
// absent execution.concurrency retains the ordinary local scheduler alone.
func (a *App) prepareExplicitLocalSlots() error {
	settings := a.cfg.Execution
	if settings == nil || settings.IsDistributed() || settings.Concurrency == nil {
		return nil
	}
	a.localNodeSlots = nil
	a.localSlotRunID = retainedRunAbsentValueText
	authority, err := openSharedNodeSlots(a.sender.Node, settings.ResolveConcurrency())
	if err != nil {
		return fmt.Errorf(sharedNodeSlotLocalCapacityErrorFormat, err)
	}
	a.localNodeSlots = authority
	a.localSlotRunID = execution.FormatRunID()
	return nil
}

// waitLocalSweepSlot waits on the durable cross-process capacity authority.
// The timer only schedules another atomic TryReserve; it is never treated as
// evidence that an unresolved task has stopped or released its lease.
func waitLocalSweepSlot(ctx context.Context, authority *execution.NodeSlotAuthority,
	intent execution.NodeSlotIntent, wait time.Duration) (execution.NodeSlotLease, error) {
	if authority == nil || wait <= sharedNodeSlotNoCapacityCount {
		return execution.NodeSlotLease{}, fmt.Errorf(sharedNodeSlotUnconfiguredWaitErrorText)
	}
	bounded, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(sharedNodeSlotPollIntervalMilliseconds * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, ok, err := authority.TryReserve(bounded, intent)
		if err != nil {
			return execution.NodeSlotLease{}, err
		}
		if ok {
			return lease, nil
		}
		select {
		case <-bounded.Done():
			return execution.NodeSlotLease{}, fmt.Errorf(sharedNodeSlotUnavailableCapacityErrorFormat, bounded.Err())
		case <-ticker.C:
		}
	}
}

// installExplicitLocalReleaseSlots binds every executable graph vertex to the
// same durable node capacity used by local sweeps and serving workers.
// The release executor calls this around admission hooks as well as commands,
// then settles it only after the package outcome and child process stop.
func (a *App) installExplicitLocalReleaseSlots(executor *release.Executor,
	compiled *release.CompiledTaskGraph) error {
	if a.localNodeSlots == nil {
		return nil
	}
	if executor == nil || compiled == nil || a.localSlotRunID == retainedRunAbsentValueText {
		return fmt.Errorf(sharedNodeSlotMissingTaskIdentityErrorText)
	}
	weights := make(map[string]int)
	capacity := a.cfg.Execution.ResolveConcurrency()
	for _, shape := range compiled.TaskShapes() {
		if shape.Virtual {
			continue
		}
		if shape.Weight <= sharedNodeSlotNoCapacityCount || shape.Weight > capacity {
			return fmt.Errorf(sharedNodeSlotInvalidTaskWeightErrorFormat,
				shape.ID, shape.Weight, capacity)
		}
		weights[shape.ID] = shape.Weight
	}
	if len(weights) == sharedNodeSlotNoCapacityCount {
		return fmt.Errorf(sharedNodeSlotNoExecutableTaskErrorText)
	}
	runID := a.localSlotRunID
	slots := a.localNodeSlots
	wait := time.Duration(a.cfg.Execution.ResolveTimeouts().Task) * time.Second
	executor.LocalTaskLifecycle = func(ctx context.Context, taskID string) (func(context.Context, string) error, error) {
		weight, found := weights[taskID]
		if !found {
			return nil, fmt.Errorf(sharedNodeSlotTaskAbsentFromGraphErrorFormat, taskID)
		}
		lease, err := waitLocalSweepSlot(ctx, slots, execution.NodeSlotIntent{
			Run: runID, Task: taskID, Attempt: sharedNodeSlotInitialAttemptCount, Weight: weight,
		}, wait)
		if err != nil {
			return nil, err
		}
		return func(settleCtx context.Context, status string) error {
			return slots.ReleaseLocalSettled(settleCtx, lease.ID, execution.NodeSlotLocalCompletion{
				Run: runID, Task: taskID, Attempt: sharedNodeSlotInitialAttemptCount, Status: status,
			})
		}, nil
	}
	return nil
}
