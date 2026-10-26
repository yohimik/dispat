// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// One compilation of the exact release task graph, shared by preflight and
// execution. This keeps the interpreter and compiler on one graph kernel.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type CompiledTaskGraph struct {
	plan            *plan.Plan
	framePlan       *plan.Plan
	baselineOutputs map[string][]plan.Output
	plans           map[string]*stagePlan
	byID            map[string]task
	scheduler       *graph.Scheduler[task]
	blocking        *stageReach
	digest          string
	reservations    []ReservationStage
	prepared        map[task]PreparedTaskSpec
}

// BudgetCapacities uses the same run-wide budget policy as the compiled
// executor. In particular, syncLock takes the smallest releasing-space vote;
// an interpreting node must not derive a second capacity from its own tasks.
func (c *CompiledTaskGraph) BudgetCapacities(build, publish int) map[StageBudget]int {
	changed := make(map[string]bool, len(c.plans))
	for name := range c.plans {
		changed[name] = compiledGraphPackageChangedState
	}
	return executionBudgetCapacities(executionBudgetPolicy{
		Plan: c.plan, Changed: changed, Build: build, Publish: publish,
	})
}

// TaskShape is a vertex in the compiled graph. Virtual vertices are the
// zero-work pass-throughs that preserve O(V+E) dependency representation.
// They never claim a task ref; their outgoing edges fire when their last
// predecessor settles. Every other vertex is an ordinary task, including
// first-node-only publication and record stages.
type TaskShape struct {
	ID           string
	Package      string
	Kind         string
	Predecessors []string
	Virtual      bool
	Recording    bool
	FirstOnly    bool
	Weight       int
	Budget       StageBudget
	Placement    StagePlacement
	Stage        string
}

type graphDigestVertex struct {
	ID            string         `json:"id"`
	Package       string         `json:"package,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Recording     bool           `json:"recording,omitempty"`
	FirstOnly     bool           `json:"firstOnly,omitempty"`
	Budget        StageBudget    `json:"budget,omitempty"`
	Placement     StagePlacement `json:"placement,omitempty"`
	Inputs        StageInputs    `json:"inputs,omitempty"`
	BuildWeight   int            `json:"buildWeight,omitempty"`
	PublishWeight int            `json:"publishWeight,omitempty"`
}

type graphDigestEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type graphDigestDocument struct {
	Version  int                 `json:"version"`
	Vertices []graphDigestVertex `json:"vertices"`
	Edges    []graphDigestEdge   `json:"edges"`
}

func taskGraphID(t task) string {
	if t.kind == taskPrepared {
		return t.pkg + compiledPreparedTaskIDSuffix
	}
	return fmt.Sprintf(compiledTaskIDFormat, t.pkg, t.kind, t.name)
}

func firstOnlyTask(t task, stage Stage) bool {
	return t.kind != taskPublish && t.kind != taskRecord &&
		(t.kind == taskVersion || t.kind == taskSign || t.kind == taskSyncLock ||
			t.kind == taskBeforeAll || stage.Placement != PlacementByBuild || stage.IsObserving)
}

// CompileTaskGraph constructs the same scheduler Executor.Run drains. The
// caller should do this before signed preflight and pass the returned graph
// back to that executor; no second graph algorithm or plan file is involved.
func (e *Executor) CompileTaskGraph(p *plan.Plan) (*CompiledTaskGraph, error) {
	if p == nil {
		return nil, fmt.Errorf(compiledGraphNilPlanErrorText)
	}
	_, changed := newReleasingResults(p)
	plans := newStagePlans(p, changed)
	sched, blocking := e.buildTaskGraph(p, changed, plans)
	if cyclic := sched.CycleNodes(); len(cyclic) != compiledGraphNoEntriesCount {
		names := make([]string, len(cyclic))
		for i, t := range cyclic {
			names[i] = taskGraphID(t)
		}
		return nil, &ExecutionCycleError{Tasks: names}
	}
	doc := graphDigestDocument{Version: compiledGraphDigestVersion}
	byID := make(map[string]task, sched.Len()+len(sched.PassThroughNodes()))
	for _, t := range sched.PassThroughNodes() {
		byID[taskGraphID(t)] = t
		doc.Vertices = append(doc.Vertices, graphDigestVertex{ID: taskGraphID(t), Package: t.pkg,
			Kind: t.kind.String()})
	}
	reservations := make([]ReservationStage, compiledGraphInitialEntryCount, sched.Len())
	for _, t := range sched.Nodes() {
		byID[taskGraphID(t)] = t
		stage := plans[t.pkg].stage(t)
		pkg := p.Releases[t.pkg].Pkg
		doc.Vertices = append(doc.Vertices, graphDigestVertex{ID: taskGraphID(t), Package: t.pkg,
			Kind:      t.kind.String(),
			Recording: t == plans[t.pkg].recording, FirstOnly: firstOnlyTask(t, stage), Budget: stage.Budget,
			Placement: stage.Placement, Inputs: stage.Inputs, BuildWeight: pkg.BuildWeight,
			PublishWeight: pkg.PublishWeight})
		if stage.Placement == PlacementByBuild {
			reservations = append(reservations, ReservationStage{TaskID: taskGraphID(t),
				Release: p.Releases[t.pkg], Descriptor: stage})
		}
	}
	for _, edge := range sched.Edges() {
		doc.Edges = append(doc.Edges, graphDigestEdge{From: taskGraphID(edge.Before), To: taskGraphID(edge.After)})
	}
	slices.SortFunc(doc.Vertices, func(a, b graphDigestVertex) int { return compareString(a.ID, b.ID) })
	slices.SortFunc(doc.Edges, func(a, b graphDigestEdge) int {
		if a.From != b.From {
			return compareString(a.From, b.From)
		}
		return compareString(a.To, b.To)
	})
	bytes, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes)
	baselineOutputs := make(map[string][]plan.Output, len(p.Releases))
	framePlan := *p
	framePlan.Releases = make(map[string]*plan.Release, len(p.Releases))
	for name, rel := range p.Releases {
		if rel != nil {
			baselineOutputs[name] = slices.Clone(rel.Outputs)
			frozen := *rel
			frozen.Outputs = slices.Clone(rel.Outputs)
			frozen.Updates = slices.Clone(rel.Updates)
			framePlan.Releases[name] = &frozen
		}
	}
	return &CompiledTaskGraph{plan: p, framePlan: &framePlan, plans: plans, byID: byID, scheduler: sched, blocking: blocking,
		digest: hex.EncodeToString(sum[:]), reservations: reservations,
		baselineOutputs: baselineOutputs}, nil
}

func compareString(a, b string) int {
	if a < b {
		return compiledGraphLexicalBeforeOrder
	}
	if a > b {
		return compiledGraphLexicalAfterOrder
	}
	return compiledGraphLexicalEqualOrder
}

// Digest names the exact graph shape and stage budget metadata.
func (c *CompiledTaskGraph) Digest() string { return c.digest }

// ReservationStages returns the precomputed delegable identities.
func (c *CompiledTaskGraph) ReservationStages() []ReservationStage {
	return slices.Clone(c.reservations)
}

// TaskShapes exposes the same indexed vertices/edges as the scheduler Run
// drains. A decentralized interpreter derives this list from its own pinned
// inputs and compares Digest before it proposes any task.
func (c *CompiledTaskGraph) TaskShapes() []TaskShape {
	byID := make(map[string]*TaskShape, c.scheduler.Len()+len(c.scheduler.PassThroughNodes()))
	for _, t := range c.scheduler.PassThroughNodes() {
		id := taskGraphID(t)
		byID[id] = &TaskShape{ID: id, Package: t.pkg, Kind: t.kind.String(), Virtual: compiledGraphVirtualTaskState}
	}
	for _, t := range c.scheduler.Nodes() {
		if t.kind == taskPrepared {
			spec := c.prepared[t]
			id := taskGraphID(t)
			byID[id] = &TaskShape{ID: id, Package: spec.Package, Kind: taskPrepared.String(),
				Weight: spec.Weight, Budget: BudgetBuild, Placement: spec.Placement, Stage: compiledBuildStageName}
			continue
		}
		stage := c.plans[t.pkg].stage(t)
		pkg := c.plan.Releases[t.pkg].Pkg
		weight := pkg.BuildWeight
		switch stage.Budget {
		case BudgetPublish:
			weight = pkg.PublishWeight
		case BudgetSyncLock:
			weight = compiledGraphSyncLockWeight
		}
		id := taskGraphID(t)
		byID[id] = &TaskShape{ID: id, Package: t.pkg, Kind: t.kind.String(),
			Recording: t == c.plans[t.pkg].recording, FirstOnly: firstOnlyTask(t, stage), Weight: weight, Budget: stage.Budget,
			Placement: stage.Placement, Stage: stage.Name}
	}
	for _, edge := range c.scheduler.Edges() {
		to := byID[taskGraphID(edge.After)]
		to.Predecessors = append(to.Predecessors, taskGraphID(edge.Before))
	}
	out := make([]TaskShape, compiledGraphInitialEntryCount, len(byID))
	for _, shape := range byID {
		slices.Sort(shape.Predecessors)
		out = append(out, *shape)
	}
	slices.SortFunc(out, func(a, b TaskShape) int { return compareString(a.ID, b.ID) })
	return out
}
