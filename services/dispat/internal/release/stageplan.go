// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// One package's release as the task graph states it: the package graph of
// CCME §13.11 turned into tasks, with the record as the publish's tail or as a
// task of its own, and the tasks its neighbours attach to.
//
// The graph is the configuration's (model.StageGraph). Everything
// position-dependent about a stage is decided here once per package rather
// than at each place the executor asks: whether a stage gates the release or
// observes one that is out, what a node installs before a stage the build
// reaches, which tasks a provider's readiness edge lands on and what its
// exports reach. The executor then reads descriptors, as it always did, and
// never a position.

import (
	public "github.com/yohimik/dispat/pkg/models"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// stagePlan is one package's release as a graph of tasks.
type stagePlan struct {
	name  string
	graph model.StageGraph
	// Adjacency is indexed once. Joining absent stages and querying readiness
	// then follows edges instead of rescanning the entire graph per task.
	stageBefore map[string][]string
	stageAfter  map[string][]string
	// tasks are the package's tasks in the stage order, which is the order
	// they are registered in and handed out among ready tasks (§13.11's
	// tie-break). The record is the publish task where the package has one.
	tasks  []task
	byTask map[task]Stage
	byName map[string]task
	// edges are the graph's edges over the present tasks, an absent stage's
	// predecessors joined to its successors.
	edges []stageTaskEdge
	// taskBefore retains predecessor order for every compiled task.
	taskBefore map[task][]task
	// beforeAll is the task the beforeAll hook runs as, present when the hook
	// is configured: it precedes every task that waits on nothing else of the
	// package, so a package with several first stages waits for one hook.
	beforeAll *task
	// recording is the task that writes the records: the publish, or a record
	// task of its own for a package without one.
	recording task
}

type stageTaskEdge struct{ before, after task }

// newStagePlans states every releasing package's plan.
func newStagePlans(p *plan.Plan, changed map[string]bool) map[string]*stagePlan {
	plans := make(map[string]*stagePlan, len(changed))
	for name := range changed {
		plans[name] = newStagePlan(p.Releases[name])
	}
	return plans
}

// newStagePlan reads one release's stages off its configuration's graph. A
// stage the plan does not run, the version stage with nothing to reconcile
// and the lock-file stage behind it, has no task; its predecessors are joined
// to its successors so nothing it ordered is lost.
func newStagePlan(rel *plan.Release) *stagePlan {
	name, space := rel.Pkg.Name, rel.Pkg.Space
	sp := &stagePlan{name: name, graph: space.StageGraph, byTask: make(map[task]Stage), byName: make(map[string]task)}
	if sp.graph.IsEmpty() {
		sp.graph = model.DefaultStageGraph(presentFlowStages(space))
	}
	sp.indexStages()
	hasPublish := sp.graph.IsNode(public.FlowStagePublish)
	for _, node := range sp.graph.Nodes {
		if !isStageRun(rel, node.Name) {
			continue
		}
		descriptor := sp.describe(rel, node)
		t := task{name, descriptor.kind, descriptor.userName()}
		if node.Name == model.RecordStage {
			if hasPublish {
				sp.byName[node.Name] = task{name, taskPublish, ""}
				continue
			}
			sp.recording = t
		}
		if descriptor.kind == taskPublish {
			sp.recording = t
		}
		sp.tasks = append(sp.tasks, t)
		sp.byTask[t] = descriptor
		sp.byName[node.Name] = t
	}
	sp.joinEdges()
	sp.indexTaskPredecessors()
	if len(space.BeforeAllScript) > 0 {
		first := sp.byTask[sp.tasks[0]]
		sp.beforeAll = &task{name, taskBeforeAll, ""}
		sp.byTask[*sp.beforeAll] = Stage{Name: first.Name, Title: first.Title, Budget: BudgetBuild, kind: taskBeforeAll}
	}
	return sp
}

func (sp *stagePlan) indexStages() {
	sp.stageBefore = make(map[string][]string, len(sp.graph.Nodes))
	sp.stageAfter = make(map[string][]string, len(sp.graph.Nodes))
	for _, edge := range sp.graph.Edges {
		sp.stageBefore[edge.After] = append(sp.stageBefore[edge.After], edge.Before)
		sp.stageAfter[edge.Before] = append(sp.stageAfter[edge.Before], edge.After)
	}
}

// isStageRun answers whether this plan runs a stage of the graph: every stage
// but the version stage with nothing to reconcile and the lock-file stage
// that follows it.
func isStageRun(rel *plan.Release, stageName string) bool {
	switch stageName {
	case public.FlowStageVersion:
		return hasVersionTask(rel)
	case public.FlowStageSyncLock:
		return hasSyncLockTask(rel)
	}
	return true
}

// joinEdges states the graph's edges over the present tasks: an edge between
// two present stages as it is, and the edges through an absent stage joined
// across it.
func (sp *stagePlan) joinEdges() {
	seen := map[stageTaskEdge]bool{}
	behind := make(map[string][]task)
	ahead := make(map[string][]task)
	for _, edge := range sp.graph.Edges {
		for _, before := range sp.presentBehind(edge.Before, behind) {
			for _, after := range sp.presentAhead(edge.After, ahead) {
				joined := stageTaskEdge{before, after}
				if before == after || seen[joined] {
					continue
				}
				seen[joined] = true
				sp.edges = append(sp.edges, joined)
			}
		}
	}
}

// presentBehind answers the task of a stage, or, for a stage the plan does
// not run, the tasks of its present predecessors.
func (sp *stagePlan) presentBehind(stageName string, memo map[string][]task) []task {
	if t, isRun := sp.byName[stageName]; isRun {
		return []task{t}
	}
	if tasks, known := memo[stageName]; known {
		return tasks
	}
	var tasks []task
	seen := make(map[task]bool)
	for _, predecessor := range sp.stageBefore[stageName] {
		for _, t := range sp.presentBehind(predecessor, memo) {
			if !seen[t] {
				seen[t] = true
				tasks = append(tasks, t)
			}
		}
	}
	memo[stageName] = tasks
	return tasks
}

// presentAhead answers the task of a stage, or, for a stage the plan does not
// run, the tasks of its present successors.
func (sp *stagePlan) presentAhead(stageName string, memo map[string][]task) []task {
	if t, isRun := sp.byName[stageName]; isRun {
		return []task{t}
	}
	if tasks, known := memo[stageName]; known {
		return tasks
	}
	var tasks []task
	seen := make(map[task]bool)
	for _, successor := range sp.stageAfter[stageName] {
		for _, t := range sp.presentAhead(successor, memo) {
			if !seen[t] {
				seen[t] = true
				tasks = append(tasks, t)
			}
		}
	}
	memo[stageName] = tasks
	return tasks
}

// describe resolves one node's descriptor: a built-in stage from the table,
// a user stage from its definition, the record as a task of its own, and for
// all of them what the node's place decides.
func (sp *stagePlan) describe(rel *plan.Release, node model.StageNode) Stage {
	space := rel.Pkg.Space
	var descriptor Stage
	switch {
	case node.Name == model.RecordStage:
		return Stage{Name: model.RecordStage, Title: "Record", Budget: BudgetPublish, kind: taskRecord}
	case node.Name == public.FlowStageVersion:
		descriptor = versionStageOf(rel)
	default:
		var isBuiltIn bool
		if descriptor, isBuiltIn = builtInStageOf(node.Name); !isBuiltIn {
			descriptor = userStageOf(space, node.Name)
		}
	}
	descriptor.Label = space.StageLabels[descriptor.Name]
	descriptor.OwnOutputStages = sp.outputAncestors(node.Name, space)
	if descriptor.kind == taskUser && len(space.BuildOutputs) > 0 && len(sp.stageAfter[node.Name]) > 0 {
		defined, _ := space.FindUserStage(node.Name)
		descriptor.IsProducingOutputs = len(defined.BeforeHook)+len(defined.Commands)+len(defined.PostHook) > 0
	}
	if !node.IsGating {
		// A stage that does not precede the record never withholds it: it
		// only warns, and it runs where the record is written.
		descriptor.IsObserving = true
		descriptor.Placement = PlacementOrchestratorOnly
		descriptor.Inputs = InputsNone
		return descriptor
	}
	if !descriptor.IsPreparing {
		// A stage the build reaches reads what the build produced; one it
		// does not is given what the build is given (§28.5).
		descriptor.Inputs = InputsProviderOutputs
		if len(descriptor.OwnOutputStages) > 0 {
			descriptor.Inputs = InputsOwnOutputs
		}
	}
	return descriptor
}

// outputAncestors lists only producers that can reach this stage. A producer
// on another parallel branch is never an input, even when it finished first.
func (sp *stagePlan) outputAncestors(name string, space *model.Space) []string {
	seen := make(map[string]bool)
	var visit func(string)
	visit = func(current string) {
		for _, before := range sp.stageBefore[current] {
			if seen[before] {
				continue
			}
			seen[before] = true
			visit(before)
		}
	}
	visit(name)
	var producers []string
	for _, node := range sp.graph.Nodes {
		if !seen[node.Name] {
			continue
		}
		if node.Name == public.FlowStageBuild {
			producers = append(producers, node.Name)
			continue
		}
		if defined, ok := space.FindUserStage(node.Name); ok &&
			len(defined.BeforeHook)+len(defined.Commands)+len(defined.PostHook) > 0 {
			producers = append(producers, node.Name)
		}
	}
	return producers
}

// builtInStageOf is the descriptor of one built-in stage by name.
func builtInStageOf(stageName string) (Stage, bool) {
	for _, descriptor := range stageTable {
		if descriptor.Name == stageName {
			return descriptor, true
		}
	}
	return Stage{}, false
}

// userStageOf is the descriptor of a stage the flow defines: placed like the
// build, under the build budget.
func userStageOf(space *model.Space, stageName string) Stage {
	defined, _ := space.FindUserStage(stageName)
	return Stage{Name: defined.Name, Title: defined.Title(), Budget: BudgetBuild, Placement: PlacementByBuild,
		kind: taskUser,
		frame: func(*model.Space) stage {
			return stage{before: defined.BeforeHook, commands: defined.Commands, after: defined.PostHook}
		}}
}

// presentFlowStages lists the stages of a space built by hand, which carries
// no resolved graph: the version stage and the build every package has, the
// built-in ones its scripts and hooks give something to do, and its user
// stages.
func presentFlowStages(space *model.Space) []string {
	names := []string{public.FlowStageVersion, public.FlowStageBuild}
	if hasSignStage(space) {
		names = append(names, public.FlowStageSign)
	}
	if hasSyncLockStage(space) {
		names = append(names, public.FlowStageSyncLock)
	}
	for _, descriptor := range stageTable {
		if !descriptor.IsPreparing && descriptor.Name != public.FlowStageBuild && descriptor.isPresent(space) {
			names = append(names, descriptor.Name)
		}
	}
	for _, stage := range space.UserStages {
		names = append(names, stage.Name)
	}
	return names
}

// versionStageOf is the version stage's descriptor under the name the
// package's space configured it by: the one stage with two names.
func versionStageOf(rel *plan.Release) Stage {
	descriptor := stageOf(taskVersion)
	descriptor.Name, descriptor.Title = rel.Pkg.Space.VersionStage.String(), rel.Pkg.Space.VersionStage.Title()
	return descriptor
}

// stage is the descriptor one task runs and reports under.
func (sp *stagePlan) stage(t task) Stage { return sp.byTask[t] }

// predecessors lists the tasks a task waits for inside its package, in the
// stage order, the beforeAll task first where it precedes the task: they are
// whose exports the task reads.
func (sp *stagePlan) predecessors(t task) []task {
	var before []task
	if sp.beforeAll != nil && t != *sp.beforeAll && len(sp.graphPredecessors(t)) == 0 {
		before = append(before, *sp.beforeAll)
	}
	return append(before, sp.graphPredecessors(t)...)
}

// graphPredecessors lists the graph's edges into a task, in the stage order.
func (sp *stagePlan) graphPredecessors(t task) []task {
	return sp.taskBefore[t]
}

func (sp *stagePlan) indexTaskPredecessors() {
	position := make(map[task]int, len(sp.tasks))
	for i, t := range sp.tasks {
		position[t] = i
	}
	sp.taskBefore = make(map[task][]task, len(sp.tasks))
	for _, edge := range sp.edges {
		sp.taskBefore[edge.after] = append(sp.taskBefore[edge.after], edge.before)
	}
	for after := range sp.taskBefore {
		before := sp.taskBefore[after]
		sort.Slice(before, func(i, j int) bool { return position[before[i]] < position[before[j]] })
	}
}

// sources lists the tasks that wait on nothing else of the package.
func (sp *stagePlan) sources() []task {
	var sources []task
	for _, t := range sp.tasks {
		if len(sp.graphPredecessors(t)) == 0 {
			sources = append(sources, t)
		}
	}
	return sources
}

// readinessTargets lists the tasks a provider's readiness edge lands on
// (§19.2a, §13.11): every stage other than sign and version can read the
// provider, so it waits for what the relation names. The lock-file stage
// stands for all of them where the package has one, since it precedes every
// stage that is not preparing; otherwise the stages whose predecessors are
// all preparing do, and the rest wait through them.
func (sp *stagePlan) readinessTargets() []task {
	if t, isRun := sp.byName[public.FlowStageSyncLock]; isRun {
		return []task{t}
	}
	var targets []task
	for _, t := range sp.tasks {
		if sp.stage(t).IsPreparing {
			continue
		}
		isEntry := true
		for _, before := range sp.graphPredecessors(t) {
			if !sp.stage(before).IsPreparing {
				isEntry = false
				break
			}
		}
		if isEntry {
			targets = append(targets, t)
		}
	}
	return targets
}

// built lists the task a consumer reading what this package's build leaves in
// the workspace waits for (§19.2a): the build, which every package has.
func (sp *stagePlan) built() []task { return []task{sp.byName[public.FlowStageBuild]} }

// addEdges registers the package's tasks in the stage order, which is the
// order the scheduler hands them out in among ready tasks (§13.11), and
// states its edges: the graph's over the present tasks, and the beforeAll
// task before every task that waits on nothing else.
func (sp *stagePlan) addEdges(sched *graph.Scheduler[task]) {
	if sp.beforeAll != nil {
		sched.Add(*sp.beforeAll)
	}
	for _, t := range sp.tasks {
		sched.Add(t)
	}
	if sp.beforeAll != nil {
		for _, t := range sp.sources() {
			sched.AddEdge(*sp.beforeAll, t)
		}
	}
	for _, edge := range sp.edges {
		sched.AddEdge(edge.before, edge.after)
	}
}
