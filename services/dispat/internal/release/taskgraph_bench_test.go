// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"testing"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkTaskGraph measures stating a release's task graph and consuming it
// node by node, which is everything the executor's ordering costs before and
// around the tasks themselves.
//
// The hub shapes are the ones the ordering through unreleased packages is
// priced by: n releasing consumers whose only provider is one package this
// run does not release, over n releasing providers of its own. The chain
// shape is an ordinary workspace where every package releases, so nothing is
// reached through a gap.
func BenchmarkTaskGraph(b *testing.B) {
	for _, shape := range []struct {
		name    string
		release func() (*plan.Plan, map[string]bool)
	}{
		{"hub-100", func() (*plan.Plan, map[string]bool) { return mkHubPlan(100) }},
		{"hub-300", func() (*plan.Plan, map[string]bool) { return mkHubPlan(300) }},
		{"chain-300", func() (*plan.Plan, map[string]bool) { return mkReleasingChainPlan(300) }},
	} {
		b.Run(shape.name, func(b *testing.B) {
			p, changed := shape.release()
			e := &Executor{}
			b.ReportAllocs()
			for b.Loop() {
				sched, _ := e.buildTaskGraph(p, changed, newStagePlans(p, changed))
				drainInHandOutOrder(sched)
			}
		})
	}
}

// drainInHandOutOrder completes every node in the order the scheduler handed
// it out, which is what a drain with an unbounded budget and equal task
// durations does.
func drainInHandOutOrder(sched *graph.Scheduler[task]) []task {
	handed := sched.Ready()
	for next := 0; next < len(handed); next++ {
		handed = append(handed, sched.Done(handed[next])...)
	}
	return handed
}

// mkHubPlan is n releasing consumers behind one unreleased hub over n
// releasing providers.
func mkHubPlan(n int) (*plan.Plan, map[string]bool) {
	relations := map[string]models.StageWait{"hub": models.StageWaitBuild}
	providers := map[string][]string{}
	var releasing []string
	for i := range n {
		provider, consumer := fmt.Sprintf("p%04d", i), fmt.Sprintf("c%04d", i)
		relations[provider], relations[consumer] = models.StageWaitBuild, models.StageWaitBuild
		providers["hub"] = append(providers["hub"], provider)
		providers[consumer] = []string{"hub"}
		releasing = append(releasing, provider, consumer)
	}
	return mkReachPlan(relations, providers, releasing...)
}

// mkReleasingChainPlan is n releasing packages, each a consumer of the three
// before it.
func mkReleasingChainPlan(n int) (*plan.Plan, map[string]bool) {
	relations := map[string]models.StageWait{}
	providers := map[string][]string{}
	var releasing []string
	for i := range n {
		name := fmt.Sprintf("q%04d", i)
		relations[name] = models.StageWaitBuild
		for back := max(0, i-3); back < i; back++ {
			providers[name] = append(providers[name], fmt.Sprintf("q%04d", back))
		}
		releasing = append(releasing, name)
	}
	return mkReachPlan(relations, providers, releasing...)
}
