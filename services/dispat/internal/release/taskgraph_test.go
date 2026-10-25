// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The task graph against its literal reading.
//
// The order the scheduler hands tasks out in reaches the log and the event
// stream, so the graph the executor states must hand out the same tasks in
// the same order as the literal graph does: one build edge and one publish
// edge from every changed provider a consumer reaches through packages this
// run does not release (§19.2, §19.2a), stated in name order. The literal
// graph is kept here as the reference, and both are drained side by side
// under the same completion choices.

// buildLiteralTaskGraph is the task graph as §19.2 and §19.2a read
// literally: every ordering a consumer has through an unreleased package is
// an edge of its own.
func buildLiteralTaskGraph(p *plan.Plan, changed map[string]bool,
	publishGroup func(*plan.Release) string) *graph.Scheduler[task] {
	sched := graph.NewScheduler[task]()
	plans := newStagePlans(p, changed)
	buildOrder := newStageReach(newStageEdges(p, changed, plans), model.StageRelation.IsBuildWaitingBuild)
	publishOrder := newPublishReach(newStageEdges(p, changed, plans))
	for _, name := range slices.Sorted(maps.Keys(changed)) {
		sp := plans[name]
		sp.addEdges(sched)
		first := sp.readinessTargets()
		for _, prov := range p.Providers[name] {
			if !changed[prov] {
				continue
			}
			for _, waited := range resolveFirstTaskWaits(p.Releases[prov].Pkg.Space.ProviderRelation, plans[prov]) {
				addEdges(sched, waited, first)
			}
			sched.AddEdge(plans[prov].recording, sp.recording)
		}
		for _, behind := range buildOrder.Indirect(name) {
			for _, built := range plans[behind].built() {
				addEdges(sched, built, first)
			}
		}
		for _, behind := range publishOrder.Indirect(name) {
			sched.AddEdge(plans[behind].recording, sp.recording)
		}
	}
	if publishGroup != nil {
		last := make(map[string]string)
		for _, name := range p.Order {
			if !changed[name] {
				continue
			}
			group := publishGroup(p.Releases[name])
			if group == "" {
				continue
			}
			if previous := last[group]; previous != "" {
				sched.AddEdge(plans[previous].recording, plans[name].recording)
			}
			last[group] = name
		}
	}
	return sched
}

// completionPolicy picks which of the tasks in flight completes next.
type completionPolicy func(inFlight int) int

// drainRecordingHandOuts consumes a scheduler, completing tasks in the order
// the policy picks, and answers every batch it handed out: the initial
// frontier, then what each completion made ready.
func drainRecordingHandOuts(sched *graph.Scheduler[task], pick completionPolicy) [][]task {
	ready := sched.Ready()
	handOuts := [][]task{ready}
	inFlight := slices.Clone(ready)
	for len(inFlight) > 0 {
		next := pick(len(inFlight))
		completed := inFlight[next]
		inFlight = slices.Delete(inFlight, next, next+1)
		ready = sched.Done(completed)
		handOuts = append(handOuts, ready)
		inFlight = append(inFlight, ready...)
	}
	return handOuts
}

// completionPolicies are the drains compared: completion in hand-out order,
// the most recent first, and seeded random orders.
func completionPolicies() map[string]func() completionPolicy {
	policies := map[string]func() completionPolicy{
		"in hand-out order":   func() completionPolicy { return func(int) int { return 0 } },
		"most recent first":   func() completionPolicy { return func(inFlight int) int { return inFlight - 1 } },
		"from the middle out": func() completionPolicy { return func(inFlight int) int { return inFlight / 2 } },
	}
	for seed := range uint64(4) {
		policies[fmt.Sprintf("random seed %d", seed)] = func() completionPolicy {
			random := rand.New(rand.NewPCG(seed, seed))
			return func(inFlight int) int { return random.IntN(inFlight) }
		}
	}
	return policies
}

// assertSameHandOuts drains the executor's graph and the literal one under
// every policy and requires the same batches in the same order, and that
// every task of the literal graph was handed out.
func assertSameHandOuts(t *testing.T, p *plan.Plan, changed map[string]bool, publishGroup func(*plan.Release) string) {
	t.Helper()
	for name, policy := range completionPolicies() {
		literal := buildLiteralTaskGraph(p, changed, publishGroup)
		want := drainRecordingHandOuts(literal, policy())
		sched, _ := (&Executor{PublishGroup: publishGroup}).buildTaskGraph(p, changed, newStagePlans(p, changed))
		got := drainRecordingHandOuts(sched, policy())
		require.Equal(t, want, got, "completion %s", name)
		handed := 0
		for _, batch := range got {
			handed += len(batch)
		}
		require.Equal(t, literal.Len(), handed, "completion %s hands out every task", name)
		require.Equal(t, literal.Len(), sched.Len(), "the graph counts the tasks that run, and only those")
	}
}

// TestTaskGraphHandsOutTheLiteralOrder runs the shapes the orderings are
// about through both graphs: a diamond that releases whole, a chain that
// passes through packages with nothing to release, the fixtures the relation
// and publication tests run, and a hub of consumers behind one unreleased
// package.
func TestTaskGraphHandsOutTheLiteralOrder(t *testing.T) {
	relations := []models.StageWait{models.StageWaitNone, models.StageWaitBuild, models.StageWaitPublish}
	for _, wait := range relations {
		t.Run("diamond under "+string(wait), func(t *testing.T) {
			p := mkPlan(planSpec{Relation: relationOf(t, wait, nil),
				Deps:  map[string][]string{"b": {"a"}, "c": {"a"}, "d": {"b", "c"}},
				Names: []string{"a", "b", "c", "d"}})
			assertSameHandOuts(t, p, releasingNames(p), nil)
		})
	}
	for _, near := range relations {
		for _, far := range relations {
			t.Run(fmt.Sprintf("chain through non-building packages, %s then %s", near, far), func(t *testing.T) {
				p, changed := mkReachPlan(
					map[string]models.StageWait{"app": near, "ui": far, "mid": near, "lib": far, "zlib": near},
					map[string][]string{"app": {"ui"}, "ui": {"mid"}, "mid": {"lib"}, "lib": {"zlib"}},
					"app", "mid", "zlib")
				assertSameHandOuts(t, p, changed, nil)
			})
		}
	}
	t.Run("diamond whose arms do not release", func(t *testing.T) {
		p, changed := mkReachPlan(
			map[string]models.StageWait{
				"app": models.StageWaitBuild, "left": models.StageWaitNone, "right": models.StageWaitBuild,
				"core": models.StageWaitBuild, "util": models.StageWaitPublish, "direct": models.StageWaitBuild,
			},
			map[string][]string{"app": {"left", "right", "direct"}, "left": {"core"}, "right": {"util", "core"}},
			"app", "core", "util", "direct")
		assertSameHandOuts(t, p, changed, nil)
	})
	for _, toUI := range relations {
		t.Run("publication order through ui under "+string(toUI), func(t *testing.T) {
			p := mkPublishOrderPlan(toUI, models.StageWaitBuild)
			assertSameHandOuts(t, p, releasingNames(p), nil)
		})
	}
	t.Run("hub", func(t *testing.T) {
		p, changed := mkHubPlan(12)
		assertSameHandOuts(t, p, changed, nil)
	})
	t.Run("hub publishing as one group", func(t *testing.T) {
		p, changed := mkHubPlan(12)
		// The group chains publishes in plan order, which is providers first.
		slices.SortFunc(p.Order, func(a, b string) int { return -strings.Compare(a, b) })
		assertSameHandOuts(t, p, changed, func(*plan.Release) string { return "one" })
	})
}

// TestTaskGraphHandsOutTheLiteralOrderOnRandomWorkspaces draws workspaces
// whose packages mix all three relations, release or not at random, sign or
// not (so a first task is a build in some and a sign task in others), and
// publish in groups, and compares both graphs on each.
func TestTaskGraphHandsOutTheLiteralOrderOnRandomWorkspaces(t *testing.T) {
	for seed := range uint64(60) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			p, changed, publishGroup := mkRandomWorkspace(rand.New(rand.NewPCG(seed, 7)), 24)
			assertSameHandOuts(t, p, changed, publishGroup)
		})
	}
}

// mkRandomWorkspace draws a workspace of n packages, each depending on some
// of the packages before it.
func mkRandomWorkspace(random *rand.Rand, n int) (*plan.Plan, map[string]bool, func(*plan.Release) string) {
	waits := []models.StageWait{models.StageWaitNone, models.StageWaitBuild, models.StageWaitPublish}
	p := &plan.Plan{Releases: map[string]*plan.Release{}, Providers: map[string][]string{}}
	changed := map[string]bool{}
	groups := map[string]string{}
	names := make([]string, n)
	for i := range names {
		// Names out of dependency order, so a consumer can sort before the
		// providers it reaches.
		names[i] = fmt.Sprintf("p%02d", random.IntN(90)) + fmt.Sprint(i)
	}
	for i, name := range names {
		space := &model.Space{Name: name,
			ProviderRelation: model.NewStageRelation(&models.StageRelation{Build: waits[random.IntN(len(waits))]})}
		if random.IntN(3) == 0 {
			space.SignScript = []string{"sign"}
		}
		p.Releases[name] = &plan.Release{Pkg: &model.Package{Name: name, Dir: name, Space: space},
			Current: ccme.Version{Major: 1}}
		p.Order = append(p.Order, name)
		for back := range i {
			if random.IntN(5) == 0 {
				p.Providers[name] = append(p.Providers[name], names[back])
			}
		}
		if random.IntN(2) == 0 {
			changed[name] = true
		}
		if random.IntN(2) == 0 {
			groups[name] = fmt.Sprint(random.IntN(3))
		}
	}
	return p, changed, func(rel *plan.Release) string { return groups[rel.Pkg.Name] }
}

// releasingNames is the set of a plan's releasing packages.
func releasingNames(p *plan.Plan) map[string]bool {
	changed := map[string]bool{}
	for name, rel := range p.Releases {
		if rel.IsReleasing() {
			changed[name] = true
		}
	}
	return changed
}

// TestTaskGraphHandOutsAreNotVacuous: the comparison above would pass for two
// graphs that both handed out nothing, so one shape is pinned outright. The
// consumers behind the hub become ready only once every provider built, and
// together, ahead of the provider's own publish, because their names sort
// first and the literal graph states their edges first.
func TestTaskGraphHandOutsAreNotVacuous(t *testing.T) {
	p, changed := mkHubPlan(2)
	sched, _ := (&Executor{}).buildTaskGraph(p, changed, newStagePlans(p, changed))
	got := drainRecordingHandOuts(sched, func(int) int { return 0 })
	assert.Equal(t, [][]task{
		{{"p0000", taskBuild, ""}, {"p0001", taskBuild, ""}},
		{{"p0000", taskPublish, ""}},
		{{"c0000", taskBuild, ""}, {"c0001", taskBuild, ""}, {"p0001", taskPublish, ""}},
		nil,
		nil,
		nil,
		{{"c0000", taskPublish, ""}, {"c0001", taskPublish, ""}},
		nil,
		nil,
	}, got)
}

// TestTaskGraphStatesOneEdgePerDependencyEdge pins the size of the graph
// behind one unreleased hub: n consumers over n providers cost the
// providers' and consumers' build-to-publish edges, one build and one
// publish edge from each provider into the hub's nodes, and one of each from
// the hub's nodes into each consumer, 6n in all. Stated per consumer and
// reached provider, as the literal reading does, they cost 2n * n more.
func TestTaskGraphStatesOneEdgePerDependencyEdge(t *testing.T) {
	for _, n := range []int{1, 50, 200} {
		p, changed := mkHubPlan(n)
		sched, _ := (&Executor{}).buildTaskGraph(p, changed, newStagePlans(p, changed))
		assert.Equal(t, 6*n, sched.EdgeLen(), "hub over %d", n)
		assert.Equal(t, 2*n*n+2*n, buildLiteralTaskGraph(p, changed, nil).EdgeLen(), "literal hub over %d", n)
	}
}
