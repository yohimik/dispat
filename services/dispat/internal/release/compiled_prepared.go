// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// PreparedTaskSpec adds one unreleasing provider and its exact releasing
// consumers to the graph that preflight signs and Executor.RunCompiled drains.
type PreparedTaskSpec struct {
	TaskID          string
	Package         string
	Release         *plan.Release
	ConsumerTaskIDs []string
	Weight          int
	Placement       StagePlacement
}

// PreparedTaskSpecs returns the same explicit prepared vertices that the
// compiled scheduler drains, in canonical registration order. Release points
// to the provider's pinned plan entry, which need not be a releasing entry.
func (c *CompiledTaskGraph) PreparedTaskSpecs() []PreparedTaskSpec {
	if c == nil || c.scheduler == nil || len(c.prepared) == compiledGraphNoEntriesCount {
		return nil
	}
	out := make([]PreparedTaskSpec, compiledGraphInitialEntryCount, len(c.prepared))
	for _, task := range c.scheduler.Nodes() {
		if task.kind != taskPrepared {
			continue
		}
		spec := c.prepared[task]
		spec.ConsumerTaskIDs = slices.Clone(spec.ConsumerTaskIDs)
		out = append(out, spec)
	}
	return out
}

// IncludePreparedTasks adds the unreleasing provider closure to this one
// compiled scheduler before its digest is signed. A consumer cannot hold a
// build slot while starting one of these predecessors recursively.
func (c *CompiledTaskGraph) IncludePreparedTasks(specs []PreparedTaskSpec) error {
	if c == nil || c.scheduler == nil || c.prepared != nil {
		return fmt.Errorf(compiledPreparedGraphAlreadyUsedErrorText)
	}
	if len(specs) == compiledGraphNoEntriesCount {
		return nil
	}
	specs = slices.Clone(specs)
	slices.SortFunc(specs, func(a, b PreparedTaskSpec) int { return compareString(a.TaskID, b.TaskID) })
	scheduler := c.scheduler.Clone()
	byID := make(map[string]task, len(c.byID)+len(specs))
	for id, t := range c.byID {
		byID[id] = t
	}
	prepared := make(map[task]PreparedTaskSpec, len(specs))
	for _, spec := range specs {
		if spec.Package == compiledGraphAbsentIdentityText || spec.TaskID != spec.Package+compiledPreparedTaskIDSuffix ||
			spec.Weight < compiledGraphMinimumTaskWeight || (spec.Placement != PlacementByBuild &&
			spec.Placement != PlacementOrchestratorOnly) || spec.Release == nil ||
			spec.Release.Pkg == nil || spec.Release.Pkg.Name != spec.Package ||
			spec.Release.IsReleasing() {
			return fmt.Errorf(compiledPreparedTaskInvalidErrorFormat, spec.TaskID)
		}
		if _, exists := byID[spec.TaskID]; exists {
			return fmt.Errorf(compiledPreparedTaskDuplicateErrorFormat, spec.TaskID)
		}
		t := task{pkg: spec.Package, kind: taskPrepared}
		byID[spec.TaskID] = t
		consumers := slices.Clone(spec.ConsumerTaskIDs)
		slices.Sort(consumers)
		consumers = slices.Compact(consumers)
		prepared[t] = PreparedTaskSpec{TaskID: spec.TaskID, Package: spec.Package,
			Release:         spec.Release,
			ConsumerTaskIDs: consumers, Weight: spec.Weight,
			Placement: spec.Placement}
		scheduler.Add(t)
	}
	for _, ordered := range specs {
		t := byID[ordered.TaskID]
		spec := prepared[t]
		for _, consumerID := range spec.ConsumerTaskIDs {
			consumer, exists := byID[consumerID]
			if !exists || consumer == t {
				return fmt.Errorf(compiledPreparedTaskConsumerUnknownErrorFormat, spec.TaskID, consumerID)
			}
			scheduler.AddEdge(t, consumer)
		}
	}
	if cyclic := scheduler.CycleNodes(); len(cyclic) != compiledGraphNoEntriesCount {
		names := make([]string, len(cyclic))
		for i, t := range cyclic {
			names[i] = taskGraphID(t)
		}
		return &ExecutionCycleError{Tasks: names}
	}
	digest, err := c.digestWithPrepared(preparedGraphDigestOptions{nodes: scheduler.Nodes(), virtual: scheduler.PassThroughNodes(),
		edges: scheduler.Edges(), prepared: prepared})
	if err != nil {
		return err
	}
	c.scheduler, c.byID, c.prepared, c.digest = scheduler, byID, prepared, digest
	for _, ordered := range specs {
		spec := prepared[byID[ordered.TaskID]]
		if _, known := c.baselineOutputs[spec.Package]; !known {
			c.baselineOutputs[spec.Package] = slices.Clone(spec.Release.Outputs)
			frozen := *spec.Release
			frozen.Outputs = slices.Clone(spec.Release.Outputs)
			frozen.Updates = slices.Clone(spec.Release.Updates)
			c.framePlan.Releases[spec.Package] = &frozen
		}
		c.reservations = append(c.reservations, ReservationStage{TaskID: spec.TaskID,
			Release: spec.Release, Descriptor: Stage{Name: compiledBuildStageName, Budget: BudgetBuild,
				Placement: spec.Placement, Inputs: InputsProviderOutputs, IsProducingOutputs: compiledPreparedProducesOutputsState}})
	}
	return nil
}

type preparedGraphDigestOptions struct {
	nodes    []task
	virtual  []task
	edges    []graph.Edge[task]
	prepared map[task]PreparedTaskSpec
}

func (c *CompiledTaskGraph) digestWithPrepared(options preparedGraphDigestOptions) (string, error) {
	nodes, virtual, edges, prepared := options.nodes, options.virtual, options.edges, options.prepared
	doc := graphDigestDocument{Version: compiledPreparedGraphDigestVersion}
	for _, t := range virtual {
		doc.Vertices = append(doc.Vertices, graphDigestVertex{ID: taskGraphID(t), Package: t.pkg,
			Kind: t.kind.String()})
	}
	for _, t := range nodes {
		if t.kind == taskPrepared {
			spec := prepared[t]
			doc.Vertices = append(doc.Vertices, graphDigestVertex{ID: spec.TaskID, Package: spec.Package,
				Kind: taskPrepared.String(), Budget: BudgetBuild, Placement: spec.Placement,
				Inputs: InputsProviderOutputs, BuildWeight: spec.Weight})
			continue
		}
		stage := c.plans[t.pkg].stage(t)
		pkg := c.plan.Releases[t.pkg].Pkg
		doc.Vertices = append(doc.Vertices, graphDigestVertex{ID: taskGraphID(t), Package: t.pkg,
			Kind: t.kind.String(), Recording: t == c.plans[t.pkg].recording,
			FirstOnly: firstOnlyTask(t, stage),
			Budget:    stage.Budget, Placement: stage.Placement, Inputs: stage.Inputs,
			BuildWeight: pkg.BuildWeight, PublishWeight: pkg.PublishWeight})
	}
	for _, edge := range edges {
		doc.Edges = append(doc.Edges, graphDigestEdge{From: taskGraphID(edge.Before), To: taskGraphID(edge.After)})
	}
	slices.SortFunc(doc.Vertices, func(a, b graphDigestVertex) int { return compareString(a.ID, b.ID) })
	slices.SortFunc(doc.Edges, func(a, b graphDigestEdge) int {
		if a.From != b.From {
			return compareString(a.From, b.From)
		}
		return compareString(a.To, b.To)
	})
	encoded, err := json.Marshal(doc)
	if err != nil {
		return compiledGraphAbsentIdentityText, err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
