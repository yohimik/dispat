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

const (
	choreographyWorkerLogKey                = "worker"
	choreographyStageMissingIdentity        = ""
	choreographyStageNoWeight               = 0
	choreographyStageMinimumWeight          = 1
	choreographyStageFirstAttempt           = 1
	choreographyStageNextAttempt            = 1
	choreographyStageUsed                   = true
	choreographyStageClosed                 = true
	choreographyStageInvalidRequestError    = "execution: invalid compiled choreography reservation"
	choreographyStageMissingWeightError     = "execution: compiled task has no signed placement weight"
	choreographyStageIdentityChangedError   = "execution: reserved choreography stage identity changed"
	choreographyStageExhaustedAttemptsError = "execution: compiled task exhausted its placement attempts"
	choreographyStageRetryLogCodeKey        = "code"
	choreographyStageRetryLogCategoryKey    = "category"
	choreographyStageRetryLogRunKey         = "run"
	choreographyStageRetryLogTaskKey        = "task"
	choreographyStageRetryLogAttemptKey     = "attempt"
	choreographyStageRetryMessage           = "the queued assignment was revoked and the task is placed again"
)

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
	if request.TaskID == choreographyStageMissingIdentity || request.Release == nil || request.Release.Pkg == nil ||
		request.Release.Pkg.Space == nil || request.Descriptor.Placement != release.PlacementByBuild {
		return nil, fmt.Errorf(choreographyStageInvalidRequestError)
	}
	space := request.Release.Pkg.Space
	c.choreographyBroker.mu.Lock()
	task := c.choreographyBroker.tasks[request.TaskID]
	weight := choreographyStageNoWeight
	if task != nil {
		weight = task.spec.Weight
	}
	c.choreographyBroker.mu.Unlock()
	if weight < choreographyStageMinimumWeight {
		return nil, fmt.Errorf(choreographyStageMissingWeightError)
	}
	if c.GraphRootOID != choreographyStageMissingIdentity {
		// Every compiled task already has a node-neutral canonical ref. The
		// Executor observes exact finality; peers reserve their own durable
		// physical capacity and select among all indexed-ready tasks.
		return &choreographyStageReservation{c: c, task: request.TaskID,
			stage: request.Descriptor.Name, pkg: request.Release.Pkg.Name,
			weight: weight}, nil
	}
	placement := resolveDescriptorPlacement(request.Descriptor, space)
	preferred := choreographyStageMissingIdentity
	if request.Descriptor.Inputs == release.InputsOwnOutputs {
		preferred = c.producerOf(release.StageRequest{Release: request.Release,
			Stage: request.Descriptor.Name, Descriptor: request.Descriptor})
	}
	lease, err := c.Pool.AcquireNearWeight(ctx, WeightedPlacementRequest{
		Platforms: space.BuildPlatforms, Placement: placement, Preferred: preferred, Weight: weight,
	})
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
		return release.StageOutcome{}, fmt.Errorf(choreographyStageIdentityChangedError)
	}
	r.used = choreographyStageUsed
	r.mu.Unlock()
	if r.lease == nil && r.c.GraphRootOID != choreographyStageMissingIdentity {
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
	for attempt := choreographyStageFirstAttempt; attempt <= maxPlacementAttempts; attempt++ {
		if attempt > choreographyStageFirstAttempt {
			var err error
			lease, err = r.c.Pool.AcquireNearWeight(ctx, WeightedPlacementRequest{
				Platforms: r.platforms, Placement: r.placement, Preferred: r.preferred, Weight: r.weight,
			})
			if err != nil {
				return release.StageOutcome{}, r.c.refuseTask(r.task, choreographyStageMissingIdentity, attempt, err)
			}
		}
		outcome, err := r.c.runChoreographyStageOnLease(ctx, choreographyStageLeaseOptions{
			Task:    r.task,
			Request: request,
			Lease:   lease,
			Attempt: attempt,
		})
		if !errors.Is(err, errQueueExpired) {
			r.c.rememberPlacedTask(r.task, lease, attempt,
				placedOutcome{exports: len(outcome.Exports), err: err})
			return outcome, err
		}
		if attempt == maxPlacementAttempts {
			return outcome, r.c.refuseTask(r.task, lease.Node, attempt,
				formatPlacementExhausted(err, r.c.Timeouts.Task, attempt))
		}
		r.c.Log.Warn().Str(choreographyStageRetryLogRunKey, r.c.Run).Str(choreographyStageRetryLogTaskKey, r.task).
			Str(choreographyWorkerLogKey, lease.Node).Int(choreographyStageRetryLogAttemptKey, attempt).
			Str(choreographyStageRetryLogCodeKey, CodeIntegrity).Str(choreographyStageRetryLogCategoryKey, CategoryIntegrity).
			Msg(choreographyStageRetryMessage)
	}
	return release.StageOutcome{}, fmt.Errorf(choreographyStageExhaustedAttemptsError)
}

func (r *choreographyStageReservation) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = choreographyStageClosed
	used := r.used
	r.mu.Unlock()
	if !used && r.lease != nil {
		r.lease.Release()
	}
	return nil
}
