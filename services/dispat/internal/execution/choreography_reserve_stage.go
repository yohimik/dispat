// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const choreographyWorkerLogKey = "worker"

type choreographyStageReservation struct {
	c         *Coordinator
	task      string
	stage     string
	pkg       string
	lease     *Lease
	placement Placement
	platforms []string
	preferred string
	weight    int
	mu        sync.Mutex
	used      bool
	closed    bool
}

// ReserveStage takes the physical placement before the release executor
// counts the package task in flight or runs its admission/prelude hooks.
// Prepared providers are graph predecessors and must already be settled.
func (c *Coordinator) ReserveStage(ctx context.Context,
	request release.StageReservationRequest) (release.StageReservationx, error) {
	if c == nil || c.choreographyBroker == nil {
		return nil, nil
	}
	if request.TaskID == "" || request.Release == nil || request.Release.Pkg == nil ||
		request.Release.Pkg.Space == nil || request.Descriptor.Placement != release.PlacementByBuild {
		return nil, fmt.Errorf("execution: invalid compiled choreography reservation")
	}
	space := request.Release.Pkg.Space
	c.choreographyBroker.mu.Lock()
	task := c.choreographyBroker.tasks[request.TaskID]
	weight := 0
	if task != nil {
		weight = task.spec.Weight
	}
	c.choreographyBroker.mu.Unlock()
	if weight < 1 {
		return nil, fmt.Errorf("execution: compiled task has no signed placement weight")
	}
	if c.GraphRootOID != "" {
		// Every compiled task already has a node-neutral canonical ref. The
		// Executor observes exact finality; peers reserve their own durable
		// physical capacity and select among all indexed-ready tasks.
		return &choreographyStageReservation{c: c, task: request.TaskID,
			stage: request.Descriptor.Name, pkg: request.Release.Pkg.Name,
			weight: weight}, nil
	}
	placement := resolveDescriptorPlacement(request.Descriptor, space)
	preferred := ""
	if request.Descriptor.Inputs == release.InputsOwnOutputs {
		preferred = c.producerOf(release.StageRequest{Release: request.Release,
			Stage: request.Descriptor.Name, Descriptor: request.Descriptor})
	}
	lease, err := c.Pool.AcquireNearWeight(ctx, space.BuildPlatforms, placement, preferred, weight)
	if err != nil {
		return nil, err
	}
	return &choreographyStageReservation{c: c, task: request.TaskID,
		stage: request.Descriptor.Name, pkg: request.Release.Pkg.Name, lease: lease,
		placement: placement, platforms: append([]string(nil), space.BuildPlatforms...),
		preferred: preferred, weight: weight}, nil
}

func (r *choreographyStageReservation) Stage(ctx context.Context,
	request release.StageRequest, _ release.LocalFrame,
	_ func(context.Context) error) (release.StageOutcome, error) {
	r.mu.Lock()
	if r.closed || r.used || request.TaskID != r.task || request.Stage != r.stage ||
		request.Release == nil || request.Release.Pkg == nil || request.Release.Pkg.Name != r.pkg {
		r.mu.Unlock()
		return release.StageOutcome{}, fmt.Errorf("execution: reserved choreography stage identity changed")
	}
	r.used = true
	r.mu.Unlock()
	if r.lease == nil && r.c.GraphRootOID != "" {
		return r.c.waitCompiledStage(ctx, r.task, request)
	}
	ctx, settled := r.c.watchOwnership(ctx)
	defer func() {
		if value, ok := r.c.choreographyPending.Load(r.task); ok {
			pending := value.(*choreographyPendingDecision)
			pending.mu.Lock()
			pending.watchSettled = settled
			pending.mu.Unlock()
			return
		}
		settled()
	}()
	lease := r.lease
	for attempt := 1; attempt <= maxPlacementAttempts; attempt++ {
		if attempt > 1 {
			var err error
			lease, err = r.c.Pool.AcquireNearWeight(ctx, r.platforms,
				r.placement, r.preferred, r.weight)
			if err != nil {
				return release.StageOutcome{}, r.c.refuseTask(r.task, "", attempt, err)
			}
		}
		outcome, err := r.c.runChoreographyStageOnLease(ctx, r.task, request, lease, attempt)
		if !errors.Is(err, errQueueExpired) {
			r.c.rememberPlacedTask(r.task, lease, attempt,
				placedOutcome{exports: len(outcome.Exports), err: err})
			return outcome, err
		}
		if attempt == maxPlacementAttempts {
			return outcome, r.c.refuseTask(r.task, lease.Node, attempt,
				formatPlacementExhausted(err, r.c.Timeouts.Task, attempt))
		}
		r.c.Log.Warn().Str("run", r.c.Run).Str("task", r.task).
			Str(choreographyWorkerLogKey, lease.Node).Int("attempt", attempt).
			Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the queued assignment was revoked and the task is placed again")
	}
	return release.StageOutcome{}, fmt.Errorf("execution: compiled task exhausted its placement attempts")
}

func (r *choreographyStageReservation) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	used := r.used
	r.mu.Unlock()
	if !used && r.lease != nil {
		r.lease.Release()
	}
	return nil
}
