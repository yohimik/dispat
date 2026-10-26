// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// BlockPreparedTask persists no-claim finality when a provider fails before
// it can return a Result. Its branch CAS and ledger event precede release of
// the preparation future, so a consumer cannot infer success from a missing
// or merely local provider failure.
func (c *Coordinator) BlockPreparedTask(ctx context.Context, packageName string) error {
	if c == nil || c.choreographyBroker == nil {
		return nil
	}
	taskID := formatPrepareTask(packageName)
	broker := c.choreographyBroker
	broker.mu.Lock()
	task := broker.tasks[taskID]
	if task == nil || task.spec.Kind != string(KindPrepare) || task.spec.Package != packageName {
		broker.mu.Unlock()
		return fmt.Errorf("execution: failed preparation %s has no canonical task", packageName)
	}
	if task.terminal {
		broker.mu.Unlock()
		return nil
	}
	blocker := task.preparedBlockedBy
	if blocker == "" {
		blocker = packageName
	}
	broker.mu.Unlock()
	return broker.AcceptBlocked(ctx, ChoreographyBlockedOutcome{
		ChoreographyRunBinding: broker.binding, Task: taskID,
		Package: packageName, BlockedBy: blocker})
}

// ChoreographyPreparedTask is an unreleasing provider command discovered
// from the same declared provider-input closure as InitializeStageTasks.
// Consumers are exact graph task IDs that must wait for its admitted output.
type ChoreographyPreparedTask struct {
	ReservationTask
	Package   string
	Release   *plan.Release
	Weight    int
	Consumers []ChoreographyPreparedConsumer
}

// ChoreographyPreparedConsumer identifies either an exact prepared task or a
// compiled releasing stage. The latter is resolved through TaskShape metadata;
// its graph ID is deliberately not guessed by the transport.
type ChoreographyPreparedConsumer struct {
	Task    string
	Package string
	Stage   string
}

type ChoreographyPreparedDiscovery struct {
	Inputs  func(string) []InputPackage
	Prepare func(string) (*PreparedProvider, error)
}

// ChoreographyPreparedTasks enumerates the complete nonreleasing provider
// closure before initial branch creation, without running any provider.
func (c *Coordinator) ChoreographyPreparedTasks(stages []release.ReservationStage) ([]ChoreographyPreparedTask, error) {
	if c == nil {
		return nil, fmt.Errorf("execution: prepared-provider discovery is unavailable")
	}
	return DiscoverChoreographyPreparedTasks(stages, ChoreographyPreparedDiscovery{
		Inputs: c.dispatch.Inputs, Prepare: c.dispatch.Prepare})
}

// DiscoverChoreographyPreparedTasks shares one provider-closure traversal
// between the first node and a peer independently compiling pinned inputs.
func DiscoverChoreographyPreparedTasks(stages []release.ReservationStage,
	options ChoreographyPreparedDiscovery) ([]ChoreographyPreparedTask, error) {
	if options.Inputs == nil || options.Prepare == nil {
		return nil, fmt.Errorf("execution: prepared-provider discovery is unavailable")
	}
	prepared := make(map[string]*ChoreographyPreparedTask)
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(consumer string, target ChoreographyPreparedConsumer) error
	visit = func(consumer string, target ChoreographyPreparedConsumer) error {
		for _, input := range options.Inputs(consumer) {
			if !input.IsPrepared {
				continue
			}
			entry := prepared[input.Package]
			if entry == nil {
				frame, err := options.Prepare(input.Package)
				if err != nil {
					return err
				}
				if frame == nil || frame.Request.Release == nil || frame.Request.Release.Pkg == nil ||
					frame.Request.Release.Pkg.Space == nil {
					return fmt.Errorf("execution: prepared provider %s has no package", input.Package)
				}
				space := frame.Request.Release.Pkg.Space
				entry = &ChoreographyPreparedTask{Package: input.Package,
					Release: frame.Request.Release,
					Weight:  frame.Request.Release.Pkg.BuildWeight,
					ReservationTask: ReservationTask{Task: formatPrepareTask(input.Package), Kind: KindPrepare,
						Platforms: space.BuildPlatforms,
						Placement: ResolveStagePlacement(StageBuild, space.RunOnly.ResolveBuild(), len(space.LoginScript) > 0)}}
				prepared[input.Package] = entry
			}
			if target.Task != "" || target.Stage != "" {
				entry.Consumers = append(entry.Consumers, target)
			}
			if visiting[input.Package] {
				return fmt.Errorf("execution: prepared-provider closure contains a cycle at %s", input.Package)
			}
			if !visited[input.Package] {
				visiting[input.Package] = true
				if err := visit(input.Package, ChoreographyPreparedConsumer{Task: entry.Task}); err != nil {
					return err
				}
				delete(visiting, input.Package)
				visited[input.Package] = true
			}
		}
		return nil
	}
	for _, stage := range stages {
		if stage.Release == nil || stage.Release.Pkg == nil || stage.Descriptor.Inputs != release.InputsProviderOutputs {
			continue
		}
		if err := visit(stage.Release.Pkg.Name, ChoreographyPreparedConsumer{
			Package: stage.Release.Pkg.Name, Stage: stage.Descriptor.Name}); err != nil {
			return nil, err
		}
	}
	result := make([]ChoreographyPreparedTask, 0, len(prepared))
	for _, entry := range prepared {
		sort.Slice(entry.Consumers, func(i, j int) bool {
			a, b := entry.Consumers[i], entry.Consumers[j]
			if a.Package != b.Package {
				return a.Package < b.Package
			}
			if a.Stage != b.Stage {
				return a.Stage < b.Stage
			}
			return a.Task < b.Task
		})
		entry.Consumers = slices.Compact(entry.Consumers)
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Task < result[j].Task })
	return result, nil
}
