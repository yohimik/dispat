// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import "github.com/yohimik/dispat/services/dispat/internal/plan"

// ReservationStage is a compiled task that may need a worker attempt. Its
// execution inputs are intentionally absent until predecessors finish.
type ReservationStage struct {
	TaskID     string
	Release    *plan.Release
	Descriptor Stage
}

// CompiledReservationStages names every worker-eligible release task before
// execution. It uses the same graph construction as Executor.Run; the second
// construction there retains the task scheduler, while this pass exposes only
// the stable task identities needed to reserve transport branches.
func CompiledReservationStages(p *plan.Plan) []ReservationStage {
	_, changed := newReleasingResults(p)
	if len(changed) == 0 {
		return nil
	}
	plans := newStagePlans(p, changed)
	sched, _ := (&Executor{}).buildTaskGraph(p, changed, plans)
	stages := make([]ReservationStage, 0, sched.Len())
	for _, task := range sched.Nodes() {
		stage := plans[task.pkg].stage(task)
		if stage.Placement != PlacementByBuild {
			continue
		}
		stages = append(stages, ReservationStage{TaskID: taskGraphID(task),
			Release: p.Releases[task.pkg], Descriptor: stage})
	}
	return stages
}
