// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package model

// The package graph of CCME §13.11 ("Configured stages in the compiled plan"):
// the order a package's stages run in, as the flow's constraints, the default
// chain and the edges the engine needs resolve it, with the record as a node
// of its own.
//
// The graph is a function of the resolved configuration alone, which is what
// lets a configuration be refused where it is read, on every plan or on none
// (§17.2). The resolver follows the specification's definitions in its order:
// the three sources of E_p, the stages after the record, the publication and
// record edges, and the three realisability conditions with the diagnostics
// §19.2a orders.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"
)

// StageSpec is one stage of a package as the flow states it: its name and the
// stages its constraints name. The version stage may be spelled `propagate`
// anywhere, and a built-in stage the package does not run may be named, which
// states nothing for this package.
type StageSpec struct {
	Name                    string
	Before, After, Parallel []string
}

// IsPlaced reports whether the stage states its own place: a before or an
// after. A stage that does not keeps its default edges, and a parallel
// constraint alone never moves it.
func (s StageSpec) IsPlaced() bool {
	return len(s.Before)+len(s.After) > 0
}

// RecordStage is the record's node: the tag, the changelog entry and the
// GitHub release of a package, written as one task after its publish or, for a
// package with no publish stage, after the stages that precede it. The name is
// reserved: a flow may not define a stage called this.
const RecordStage = "record"

// StageGraph is one package's resolved package graph.
type StageGraph struct {
	// Nodes are the stages in the stage order (rank, then name byte-wise),
	// with the record right after the publish or, without one, right after
	// the last stage that precedes it.
	Nodes []StageNode
	// Edges are the precedence edges, each before its after, in the order of
	// their endpoints among Nodes.
	Edges []StageEdge
}

// StageNode is one stage of the graph.
type StageNode struct {
	Name string
	Rank int
	// IsGating says the stage precedes the record: its failure fails the
	// package. Every other stage follows the record, observes a release that
	// is out, and never withholds it.
	IsGating bool
}

// StageEdge is one precedence edge: Before happens before After.
type StageEdge struct {
	Before, After string
}

// IsEmpty reports whether the graph holds no stage at all, which is what a
// space built by hand carries.
func (g StageGraph) IsEmpty() bool { return len(g.Nodes) == 0 }

// IsNode reports whether the graph holds a node of this name.
func (g StageGraph) IsNode(name string) bool {
	return slices.ContainsFunc(g.Nodes, func(node StageNode) bool { return node.Name == name })
}

// Node answers one node by name.
func (g StageGraph) Node(name string) (StageNode, bool) {
	for _, node := range g.Nodes {
		if node.Name == name {
			return node, true
		}
	}
	return StageNode{}, false
}

// Predecessors lists the nodes with an edge into name, in node order.
func (g StageGraph) Predecessors(name string) []string {
	var out []string
	for _, node := range g.Nodes {
		if slices.Contains(g.Edges, StageEdge{Before: node.Name, After: name}) {
			out = append(out, node.Name)
		}
	}
	return out
}

// Successors lists the nodes with an edge out of name, in node order.
func (g StageGraph) Successors(name string) []string {
	var out []string
	for _, node := range g.Nodes {
		if slices.Contains(g.Edges, StageEdge{Before: name, After: node.Name}) {
			out = append(out, node.Name)
		}
	}
	return out
}

// IsBefore reports whether before reaches after over one or more edges.
func (g StageGraph) IsBefore(before, after string) bool {
	seen := map[string]bool{}
	frontier := []string{before}
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, edge := range g.Edges {
			if edge.Before != current || seen[edge.After] {
				continue
			}
			if edge.After == after {
				return true
			}
			seen[edge.After] = true
			frontier = append(frontier, edge.After)
		}
	}
	return false
}

// Stages lists the graph's stages in node order, the record left out.
func (g StageGraph) Stages() []string {
	var names []string
	for _, node := range g.Nodes {
		if node.Name != RecordStage {
			names = append(names, node.Name)
		}
	}
	return names
}

// The diagnostic codes a package graph is refused with, one per condition of
// §13.11, decided as the configuration is read so no command runs under a
// configuration no order realises. Each carries the execution-configuration
// identifier of §28.9, which is the refusal's stable name.
const (
	// CodeStageUnknown is condition 1: a constraint names a word that is no
	// stage of the package (a built-in stage the package lacks excepted), or
	// the stage that carries it.
	CodeStageUnknown = "E230"
	// CodeStageCycle is condition 2: the constraint, default and required
	// edges form a cycle.
	CodeStageCycle = "E231"
	// CodeStageParallelRelated is condition 3: a pair a parallel constraint
	// names is related by the edges, the publication and record edges
	// included, so no order leaves it incomparable.
	CodeStageParallelRelated = "E232"
)

// StageDiagnostic is one reason a package graph is refused.
type StageDiagnostic struct {
	Code      string
	Condition int
	// Stage is the carrying stage (conditions 1 and 3, where it is the lesser
	// stage of the pair), or empty for a cycle, which belongs to the package.
	Stage string
	// Names are the names involved: the stated name, every stage on a cycle in
	// the stage order, or the greater stage of a pair.
	Names   []string
	Message string
}

// Error renders the diagnostic as the sentence a refusal prints.
func (d StageDiagnostic) Error() string { return d.Message }

// DiagnosticCode is the condition's code.
func (d StageDiagnostic) DiagnosticCode() string { return d.Code }

// DiagnosticCategory is the refusal's stable identifier (§28.9).
func (d StageDiagnostic) DiagnosticCategory() string { return "execution-configuration" }

// Ranks of the default chain. Every user stage ranks with the test.
const (
	rankSign     = 1
	rankVersion  = 2
	rankSyncLock = 3
	rankBuild    = 4
	rankTest     = 5
	rankPublish  = 6
	rankAnnounce = 7
)

// StageRank is a stage's rank in the default chain: the built-in stages'
// own, and the test stage's for every stage a flow defines. The record is
// ranked right after the publish.
func StageRank(name string) int {
	switch name {
	case public.FlowStageSign:
		return rankSign
	case public.FlowStageVersion, public.FlowStagePropagate:
		return rankVersion
	case public.FlowStageSyncLock:
		return rankSyncLock
	case public.FlowStageBuild:
		return rankBuild
	case public.FlowStagePublish, RecordStage:
		return rankPublish
	case public.FlowStageAnnounce:
		return rankAnnounce
	default:
		return rankTest
	}
}

// IsPreparingStage reports whether a stage rewrites the files every other
// stage reads, which is what puts it before all of them.
func IsPreparingStage(name string) bool {
	switch name {
	case public.FlowStageSign, public.FlowStageVersion, public.FlowStagePropagate, public.FlowStageSyncLock:
		return true
	}
	return false
}

// compareStageOrder is the stage order of §13.11: rank, then name byte-wise.
func compareStageOrder(a, b string) int {
	if ra, rb := StageRank(a), StageRank(b); ra != rb {
		return ra - rb
	}
	return strings.Compare(a, b)
}

// DefaultStageGraph is the graph of stages that carry no constraint.
func DefaultStageGraph(names []string) StageGraph {
	specs := make([]StageSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, StageSpec{Name: name})
	}
	graph, refusals := ResolveStageGraph(specs)
	if len(refusals) > 0 {
		panic("model: the default stage graph is realisable: " + refusals[0].Message)
	}
	return graph
}

// ResolveStageGraph resolves one package's stages, which are every stage of
// S_p, into its package graph, or answers why no order realises its
// configuration. A package that fails one condition is not checked against a
// later one, and the diagnostics come in the order §19.2a gives them.
func ResolveStageGraph(stages []StageSpec) (StageGraph, []StageDiagnostic) {
	r := newStageResolver(stages)
	if refusals := r.checkNames(); len(refusals) > 0 {
		return StageGraph{}, refusals
	}
	own := r.ownEdges()
	ownReach := newStageReach(own)
	if refusals := r.checkAcyclic(ownReach); len(refusals) > 0 {
		return StageGraph{}, refusals
	}
	r.after = r.postPublicationStages(ownReach)
	full := own.clone()
	r.addPublicationEdges(full)
	r.addRecordEdges(full)
	fullReach := newStageReach(full)
	if refusals := r.checkParallelPairs(ownReach, fullReach); len(refusals) > 0 {
		return StageGraph{}, refusals
	}
	return r.graph(full, fullReach), nil
}

// stageResolver is the state of one resolution: the stages in the stage
// order and their specs, with the version stage under its canonical name and
// every built-in stage the package lacks left out of the constraints.
type stageResolver struct {
	order []string
	specs map[string]StageSpec
	// after is A_p: the stages the publish reaches, or, without one, an
	// announce that does not place itself and what it reaches.
	after map[string]bool
}

func newStageResolver(stages []StageSpec) *stageResolver {
	r := &stageResolver{specs: make(map[string]StageSpec, len(stages))}
	for _, stage := range stages {
		name := canonicalStage(stage.Name)
		r.specs[name] = StageSpec{Name: name}
		r.order = append(r.order, name)
	}
	slices.SortFunc(r.order, compareStageOrder)
	for _, stage := range stages {
		name := canonicalStage(stage.Name)
		r.specs[name] = StageSpec{Name: name,
			Before: r.statedNames(stage.Before), After: r.statedNames(stage.After), Parallel: r.statedNames(stage.Parallel)}
	}
	return r
}

// statedNames canonicalises a constraint's names and drops the built-in
// stages the package does not run, so one configuration shared by packages
// with different targets stays valid for each.
func (r *stageResolver) statedNames(names []string) []string {
	var out []string
	for _, name := range names {
		name = canonicalStage(name)
		if isBuiltInStage(name) && !r.has(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// canonicalStage spells the version stage by its canonical name.
func canonicalStage(name string) string {
	if name == public.FlowStagePropagate {
		return public.FlowStageVersion
	}
	return name
}

func isBuiltInStage(name string) bool {
	switch name {
	case public.FlowStageSign, public.FlowStageVersion, public.FlowStageSyncLock, public.FlowStageBuild,
		public.FlowStageTest, public.FlowStagePublish, public.FlowStageAnnounce:
		return true
	}
	return false
}

func (r *stageResolver) has(name string) bool {
	_, isStage := r.specs[name]
	return isStage
}

// checkNames is condition 1: every remaining constraint name is a stage of
// S_p other than the one that carries it. One diagnostic per carrying stage
// and stated name, both in the stage order.
func (r *stageResolver) checkNames() []StageDiagnostic {
	var refusals []StageDiagnostic
	for _, name := range r.order {
		if name == "tests" {
			refusals = append(refusals, StageDiagnostic{Code: CodeStageUnknown, Condition: 1, Stage: name,
				Names: []string{name}, Message: "flow.tests was renamed to flow.test (stage order condition 1)"})
			continue
		}
		spec := r.specs[name]
		keyOf := map[string]string{}
		var stated []string
		for _, constraint := range []struct {
			key   string
			names []string
		}{{"before", spec.Before}, {"after", spec.After}, {"parallel", spec.Parallel}} {
			for _, target := range constraint.names {
				if _, isSeen := keyOf[target]; isSeen || (r.has(target) && target != name && target != "tests") {
					continue
				}
				keyOf[target] = constraint.key
				stated = append(stated, target)
			}
		}
		slices.SortFunc(stated, compareStageOrder)
		for _, target := range stated {
			refusals = append(refusals, StageDiagnostic{Code: CodeStageUnknown, Condition: 1, Stage: name,
				Names: []string{target}, Message: unknownStageMessage(name, keyOf[target], target)})
		}
	}
	return refusals
}

func unknownStageMessage(carrier, key, target string) string {
	if target == "tests" {
		return fmt.Sprintf("flow.%s.%s names tests; use test for the optional stage (stage order condition 1)",
			carrier, key)
	}
	if target == carrier {
		return fmt.Sprintf("flow.%s.%s names the stage itself, which no order places before, after or beside itself (stage order condition 1)",
			carrier, key)
	}
	return fmt.Sprintf("flow.%s.%s names %q, which is not a stage this package runs (stage order condition 1)",
		carrier, key, target)
}

// ownEdges is E_p: the constraint edges, the default edges into every stage
// that does not place itself, and the required edges.
func (r *stageResolver) ownEdges() stageEdges {
	edges := stageEdges{}
	r.addConstraintEdges(edges)
	r.addDefaultEdges(edges)
	r.addRequiredEdges(edges)
	return edges
}

// addConstraintEdges states what before and after say: before(s) = t is
// s -> t, after(s) = t is t -> s. parallel states no edge.
func (r *stageResolver) addConstraintEdges(edges stageEdges) {
	for _, name := range r.order {
		spec := r.specs[name]
		for _, target := range spec.Before {
			edges.add(name, target)
		}
		for _, target := range spec.After {
			edges.add(target, name)
		}
	}
}

// addDefaultEdges gives every stage that does not place itself an edge from
// every stage at the greatest rank below its own. A default edge only ever
// enters the stage it belongs to, so a stage placing itself moves itself and
// never its neighbours.
func (r *stageResolver) addDefaultEdges(edges stageEdges) {
	for _, name := range r.order {
		if r.specs[name].IsPlaced() {
			continue
		}
		below := r.greatestRankBelow(StageRank(name))
		if below == 0 {
			continue
		}
		for _, other := range r.order {
			if StageRank(other) == below {
				edges.add(other, name)
			}
		}
	}
}

// greatestRankBelow is the greatest rank of a stage of S_p below rank, or 0
// where no stage ranks below it.
func (r *stageResolver) greatestRankBelow(rank int) int {
	below := 0
	for _, other := range r.order {
		if otherRank := StageRank(other); otherRank < rank && otherRank > below {
			below = otherRank
		}
	}
	return below
}

// addRequiredEdges states the edges that hold whatever the constraints say:
// every preparing stage before every stage that is not preparing, and the
// build before the publish. The preparing stages' own order is the default
// chain's.
func (r *stageResolver) addRequiredEdges(edges stageEdges) {
	for _, preparing := range r.order {
		if !IsPreparingStage(preparing) {
			continue
		}
		for _, other := range r.order {
			if !IsPreparingStage(other) {
				edges.add(preparing, other)
			}
		}
	}
	if r.has(public.FlowStageBuild) && r.has(public.FlowStagePublish) {
		edges.add(public.FlowStageBuild, public.FlowStagePublish)
	}
}

// checkAcyclic is condition 2, decided on E_p: one diagnostic for the
// package, naming in the stage order every stage that lies on a cycle.
func (r *stageResolver) checkAcyclic(own *stageReach) []StageDiagnostic {
	var onCycle []string
	for _, name := range r.order {
		if own.reaches(name, name) {
			onCycle = append(onCycle, name)
		}
	}
	if len(onCycle) == 0 {
		return nil
	}
	return []StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: onCycle,
		Message: fmt.Sprintf("the stages %s lie on a cycle of the stage order, so no order runs them (stage order condition 2)",
			strings.Join(onCycle, ", "))}}
}

// postPublicationStages is A_p: what the publish reaches, or, without a
// publish, an announce that does not place itself and what it reaches.
func (r *stageResolver) postPublicationStages(own *stageReach) map[string]bool {
	if r.has(public.FlowStagePublish) {
		return own.reach(public.FlowStagePublish)
	}
	announce, hasAnnounce := r.specs[public.FlowStageAnnounce]
	if !hasAnnounce || announce.IsPlaced() {
		return map[string]bool{}
	}
	after := own.reach(public.FlowStageAnnounce)
	after[public.FlowStageAnnounce] = true
	return after
}

// addPublicationEdges puts every stage outside A_p before the publish.
func (r *stageResolver) addPublicationEdges(edges stageEdges) {
	if !r.has(public.FlowStagePublish) {
		return
	}
	for _, name := range r.order {
		if name != public.FlowStagePublish && !r.after[name] {
			edges.add(name, public.FlowStagePublish)
		}
	}
}

// addRecordEdges puts the record after the publish, or, without one, after
// every stage outside A_p, and before every stage of A_p.
func (r *stageResolver) addRecordEdges(edges stageEdges) {
	if r.has(public.FlowStagePublish) {
		edges.add(public.FlowStagePublish, RecordStage)
	} else {
		for _, name := range r.order {
			if !r.after[name] {
				edges.add(name, RecordStage)
			}
		}
	}
	for name := range r.after {
		edges.add(RecordStage, name)
	}
}

// parallelPairs lists every pair a parallel constraint names, each once, the
// lesser stage first, in the stage order of the lesser and then the greater.
func (r *stageResolver) parallelPairs() [][2]string {
	seen := map[[2]string]bool{}
	var pairs [][2]string
	for _, name := range r.order {
		for _, target := range r.specs[name].Parallel {
			pair := [2]string{name, target}
			if compareStageOrder(target, name) < 0 {
				pair = [2]string{target, name}
			}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			pairs = append(pairs, pair)
		}
	}
	slices.SortFunc(pairs, func(a, b [2]string) int {
		if c := compareStageOrder(a[0], b[0]); c != 0 {
			return c
		}
		return compareStageOrder(a[1], b[1])
	})
	return pairs
}

// checkParallelPairs is condition 3, decided on G_p: one diagnostic per pair
// the edges relate, the publication and record edges included.
func (r *stageResolver) checkParallelPairs(own, full *stageReach) []StageDiagnostic {
	var refusals []StageDiagnostic
	for _, pair := range r.parallelPairs() {
		first, second := pair[0], pair[1]
		if full.reaches(second, first) {
			first, second = second, first
		} else if !full.reaches(first, second) {
			continue
		}
		message := fmt.Sprintf("flow names %s and %s as parallel, but the stage order runs %s before %s, so no order runs them at once",
			pair[0], pair[1], first, second)
		if !own.reaches(first, second) {
			message += fmt.Sprintf("; every stage the publication does not reach runs before it, so place %s after the publish to run it beside the release",
				first)
		}
		refusals = append(refusals, StageDiagnostic{Code: CodeStageParallelRelated, Condition: 3,
			Stage: pair[0], Names: []string{pair[1]}, Message: message + " (stage order condition 3)"})
	}
	return refusals
}

// graph renders the resolution: the nodes in the stage order with the record
// at its place, every node's gating answered, and the edges in node order.
func (r *stageResolver) graph(edges stageEdges, reach *stageReach) StageGraph {
	if len(r.order) == 0 {
		return StageGraph{}
	}
	names := slices.Insert(slices.Clone(r.order), r.recordPosition(reach), RecordStage)
	position := make(map[string]int, len(names))
	var g StageGraph
	for index, name := range names {
		position[name] = index
		g.Nodes = append(g.Nodes, StageNode{Name: name, Rank: StageRank(name),
			IsGating: name != RecordStage && reach.reaches(name, RecordStage)})
	}
	for edge := range edges {
		g.Edges = append(g.Edges, edge)
	}
	slices.SortFunc(g.Edges, func(a, b StageEdge) int {
		if c := position[a.Before] - position[b.Before]; c != 0 {
			return c
		}
		return position[a.After] - position[b.After]
	})
	return g
}

// recordPosition is where the record sits in the stage order: right after
// the publish, or right after the last stage that precedes it.
func (r *stageResolver) recordPosition(reach *stageReach) int {
	at := 0
	for index, name := range r.order {
		if name == public.FlowStagePublish {
			return index + 1
		}
		if reach.reaches(name, RecordStage) {
			at = index + 1
		}
	}
	return at
}

// stageEdges is a set of precedence edges.
type stageEdges map[StageEdge]bool

func (edges stageEdges) add(before, after string) {
	if before != after {
		edges[StageEdge{Before: before, After: after}] = true
	}
}

func (edges stageEdges) clone() stageEdges {
	return maps.Clone(edges)
}

// stageReach indexes one graph before ancestry queries. Each source's closure
// is computed once, including cycle checks and record gating.
type stageReach struct {
	after map[string][]string
	memo  map[string]map[string]bool
}

func newStageReach(edges stageEdges) *stageReach {
	r := &stageReach{after: make(map[string][]string), memo: make(map[string]map[string]bool)}
	for edge := range edges {
		r.after[edge.Before] = append(r.after[edge.Before], edge.After)
	}
	return r
}

func (r *stageReach) reaches(from, to string) bool { return r.reach(from)[to] }

// reach is every stage reached from a source over one or more edges.
func (r *stageReach) reach(from string) map[string]bool {
	if found, ok := r.memo[from]; ok {
		return found
	}
	seen := map[string]bool{}
	frontier := []string{from}
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, after := range r.after[current] {
			if seen[after] {
				continue
			}
			seen[after] = true
			frontier = append(frontier, after)
		}
	}
	r.memo[from] = seen
	return seen
}
