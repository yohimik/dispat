// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Fixing the plan a distributed run executes.
//
// CCME §28.3 has the orchestrator fix the planning input and compute P =
// Plan(I) once, so that every node it dispatches to can be told which release
// it is taking part in and can refuse work belonging to another one. The name
// of that plan is its digest, and this is where a run takes it: after the
// plan is computed and narrowed, before anything is executed.
//
// It is computed only when the configuration delegates work. A repository
// with no workers pays nothing for the feature, not even a hash, and writes
// exactly the lines it always wrote.

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// recordFixedPlan names the plan this run will execute and says so once, at
// info level, because "which plan is this" is the first question of a run
// spread over several machines and the answer has to be in an ordinary log.
//
// The failure is a configuration one: a run that cannot name its plan cannot
// dispatch a task anybody could verify, and §28.9 classes that with the other
// settings no distributed run could be executed under.
func (a *App) recordFixedPlan(ctx context.Context, computed *plan.Plan, opts ReleaseOptions) error {
	if !a.cfg.Execution.IsDistributed() {
		return nil
	}
	digest, err := a.calculatePlanDigest(ctx, computed, opts)
	if err != nil {
		// Reported here rather than through the entry refusals: those are
		// about starting a release, and this one is equally about `status`,
		// which fixes the same plan and starts nothing.
		failure := execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			"the plan this run would dispatch could not be named: %w", err)
		a.logError(failure).Msg("cannot fix the plan for distributed execution")
		return failure
	}
	a.log.Info().Str("planDigest", digest).Msg("plan fixed")
	return nil
}

// calculatePlanDigest digests the narrowed plan against the inputs it was
// computed from.
//
// The heads are the one input the plan does not always carry: a composed
// workspace records the snapshot planning read per repository, and a single
// history records nothing, so this reads HEAD for it. That read is the only
// git call the digest makes, and it is made once per run.
func (a *App) calculatePlanDigest(ctx context.Context, computed *plan.Plan, opts ReleaseOptions) (string, error) {
	input := plan.DigestInput{
		Options: a.plannedOptions,
		Selection: plan.DigestSelection{
			Packages:          opts.Filter.Packages,
			Spaces:            opts.Filter.Spaces,
			Groups:            opts.Filter.Groups,
			IsStrict:          opts.Strict,
			IsReleaseRequired: opts.RequireRelease,
		},
	}
	if len(computed.RepositoryHeads) == 0 {
		head, err := a.git.HeadSHA(ctx)
		if err != nil {
			return "", err
		}
		input.Heads = map[string]string{"": head}
	}
	return computed.CalculateDigest(input)
}
