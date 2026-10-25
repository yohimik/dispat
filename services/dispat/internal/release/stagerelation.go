// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// What a provider's relation decides about the consumers of its packages: the
// ordering edges the task graph gets, and the sentence a consumer skipped
// because of it is told.
//
// Both answers live here rather than inline at the three call sites because
// the relation has three values and each of them is a different combination of
// the same two questions. Written out at the call site they read as nested
// conditions the reader has to re-derive; written here each value of the
// relation answers from its own branch.

import (
	"slices"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// resolveFirstTaskWaits answers which of a changed provider's tasks a
// consumer's first task (its version task when it has one, its build
// otherwise) has to wait for.
//
// The three relations are a chain rather than three unrelated rules: nothing,
// the provider's build, the provider's build and its publish. The publish case
// names both edges rather than relying on the provider's own build-before-
// publish edge to imply the first, because the graph is read by people as well
// as by the scheduler and an edge nobody can see is an ordering nobody can
// check.
func resolveFirstTaskWaits(relation model.StageRelation, provider string) []task {
	if !relation.IsBuildWaitingBuild() {
		return nil
	}
	if relation.IsBuildWaitingPublish() {
		return []task{{provider, taskBuild}, {provider, taskPublish}}
	}
	return []task{{provider, taskBuild}}
}

// stageGaps is one ordering's pass-through nodes in the task graph: one per
// package this run does not release that the ordering travels through
// (§19.2, §19.2a), which is what the specification's cost table prices the
// build readiness row by.
//
// A consumer reads its providers through whatever sits between them, and what
// sits between them need not be releasing: `app` depends on `ui`, `ui` depends
// on `core`, and a run that releases `app` and `core` while `ui` has nothing
// to release still has to build `core` first, because whatever `app` reads of
// `ui` may be `core`'s, and still has to publish `core` first, because `app`
// resolves `core` through `ui` at install time. Ordering only against the
// providers that are in the plan loses exactly that case, which is the
// commonest shape there is: any package with no bump this run sits where `ui`
// sits.
//
// A node for `ui` waits for the stage of every changed provider it carries the
// ordering from, and for the nodes of the gaps behind it, and the consumer
// waits for the node. Stated as one edge per consumer and reached provider
// instead, n consumers behind one gap over m providers cost n * m edges; the
// nodes cost one edge per dependency edge.
//
// The two orderings are the same walk over the same graph asking one question
// of each hop, which is why they are one type: what differs is whether the
// ordering travels through a provider with a given relation, and which stage
// it orders.
type stageGaps struct {
	stageEdges
	// isCarriedThrough is the question this ordering asks of every hop: does it
	// travel through a provider under this relation.
	isCarriedThrough func(model.StageRelation) bool
	// stage is the task of a changed provider the ordering waits for, and
	// passThrough the kind of the gaps' nodes.
	stage, passThrough taskKind
	// gaps lists the packages the ordering passes through, in the order the
	// consumers, in name order, first reach them.
	gaps  []string
	isGap map[string]bool
	// firstReached lists per consumer, sorted, the changed providers no
	// consumer before it in name order reaches through a gap. The literal
	// graph states one edge per reached provider in the consumer's turn, which
	// registers a provider's task that early, and the scheduler's insertion
	// order is the order its initial frontier is handed out in.
	firstReached map[string][]string
}

// newBuildGaps finds the gaps of the build orderings of §19.2a: a path ends at
// the first hop whose relation is `none`, which is the declaration that
// nothing the provider builds reaches the consumer's build, and what a package
// does not read it cannot pass on.
func newBuildGaps(edges stageEdges, consumers []string) *stageGaps {
	return newStageGaps(edges, consumers, buildGapKinds)
}

// newPublishGaps finds the gaps of the publication orderings of §19.2: every
// path is walked to its end, because a relation cannot declare the
// publication order away. The consumer resolves the provider at install time
// through whatever lies between them, whether or not that build read anything
// of it.
func newPublishGaps(edges stageEdges, consumers []string) *stageGaps {
	return newStageGaps(edges, consumers, publishGapKinds)
}

// gapKinds is what one ordering asks of a hop and which tasks it orders.
type gapKinds struct {
	isCarriedThrough   func(model.StageRelation) bool
	stage, passThrough taskKind
}

var (
	buildGapKinds   = gapKinds{model.StageRelation.IsBuildWaitingBuild, taskBuild, taskPassBuild}
	publishGapKinds = gapKinds{isPublicationCarried, taskPublish, taskPassPublish}
)

// newStageGaps walks the graph from each consumer in name order, visiting
// every gap once.
func newStageGaps(edges stageEdges, consumers []string, kinds gapKinds) *stageGaps {
	g := &stageGaps{
		stageEdges:       edges,
		isCarriedThrough: kinds.isCarriedThrough,
		stage:            kinds.stage,
		passThrough:      kinds.passThrough,
		isGap:            make(map[string]bool),
		firstReached:     make(map[string][]string),
	}
	isReached := make(map[string]bool)
	for _, consumer := range consumers {
		var reached []string
		for _, gap := range g.carryingGaps(consumer) {
			reached = g.visit(gap, isReached, reached)
		}
		if len(reached) > 0 {
			slices.Sort(reached)
			g.firstReached[consumer] = reached
		}
	}
	return g
}

// visit records a gap and every gap behind it, and appends the changed
// providers they carry the ordering from that nothing reached before. It
// recurses along dependency edges alone, and the workspace graph is acyclic
// by the time a release executes, because a cycle is E197 or E200 and no plan
// survives one.
func (g *stageGaps) visit(gap string, isReached map[string]bool, reached []string) []string {
	if g.isGap[gap] {
		return reached
	}
	g.isGap[gap] = true
	g.gaps = append(g.gaps, gap)
	for _, provider := range g.providers[gap] {
		if !g.isCarriedThrough(g.relations[provider]) {
			continue
		}
		if !g.changed[provider] {
			reached = g.visit(provider, isReached, reached)
			continue
		}
		if !isReached[provider] {
			isReached[provider] = true
			reached = append(reached, provider)
		}
	}
	return reached
}

// carryingGaps lists a package's providers that are gaps of this ordering:
// not released this run, under a relation the ordering travels through.
func (g *stageGaps) carryingGaps(name string) []string {
	var gaps []string
	for _, provider := range g.providers[name] {
		if !g.changed[provider] && g.isCarriedThrough(g.relations[provider]) {
			gaps = append(gaps, provider)
		}
	}
	return gaps
}

// passThroughOf is the gap's node in the task graph.
func (g *stageGaps) passThroughOf(gap string) task { return task{gap, g.passThrough} }

// addPassThroughs registers the gaps' nodes, which the scheduler needs before
// any edge.
func (g *stageGaps) addPassThroughs(sched *graph.Scheduler[task]) {
	for _, gap := range g.gaps {
		sched.AddPassThrough(g.passThroughOf(gap))
	}
}

// addConsumerEdges states, in the consumer's turn, what the literal graph
// states there: the providers' tasks it registers first, and the waits of the
// consumer's task, here on the gaps it reads through.
func (g *stageGaps) addConsumerEdges(sched *graph.Scheduler[task], consumer string, waiting task) {
	for _, provider := range g.firstReached[consumer] {
		sched.Add(task{provider, g.stage})
	}
	for _, gap := range g.carryingGaps(consumer) {
		sched.AddEdge(g.passThroughOf(gap), waiting)
	}
}

// addGapEdges states what each gap's node waits for: the stage of every
// changed provider it carries the ordering from, and the gaps behind it. It
// runs once every task is registered, so these edges register nothing.
func (g *stageGaps) addGapEdges(sched *graph.Scheduler[task]) {
	for _, gap := range g.gaps {
		for _, provider := range g.providers[gap] {
			if !g.isCarriedThrough(g.relations[provider]) {
				continue
			}
			if g.changed[provider] {
				sched.AddEdge(task{provider, g.stage}, g.passThroughOf(gap))
				continue
			}
			sched.AddEdge(g.passThroughOf(provider), g.passThroughOf(gap))
		}
	}
}

// isPublicationCarried answers the publication walk's question of a hop, and
// the answer is the same for all three relations: "under all three, P
// publishes before C does" (§19.2a). It is written out rather than left
// implicit because the walk asks each hop its question, and a walk with no
// question would be a walk with a mode.
func isPublicationCarried(model.StageRelation) bool { return true }

// stageEdges is the dependency graph the orderings walk: every package's
// providers, which of them release, and each provider's relation.
type stageEdges struct {
	providers map[string][]string
	changed   map[string]bool
	relations map[string]model.StageRelation
}

// newStageEdges indexes the plan's relations once, so a walk asks a map rather
// than following a pointer chain per edge. A name the plan carries no release
// for reads as the zero relation, which is the default one, and the walk goes
// on through it.
func newStageEdges(p *plan.Plan, changed map[string]bool) stageEdges {
	relations := make(map[string]model.StageRelation, len(p.Releases))
	for name, rel := range p.Releases {
		relations[name] = rel.Pkg.Space.ProviderRelation
	}
	return stageEdges{providers: p.Providers, changed: changed, relations: relations}
}

// stageReach lists the changed providers a consumer reaches over the
// publication order through packages this run does not release, which is the
// closure the skip cascade reads (§19.3): the providers that can block a
// package are the same set that orders it.
type stageReach struct {
	stageEdges
	// isCarriedThrough is the question this index asks of every hop. One
	// index answers one question, so its memo below is never a mixture of two.
	isCarriedThrough func(model.StageRelation) bool
	// nearest memoises, per package, the changed packages it reaches through
	// hops that carry the ordering, stopping at each one: a changed package's
	// own edges carry whatever lies behind it.
	nearest map[string][]string
}

// newPublishReach indexes the publication orderings of §19.2 the way
// newPublishGaps walks them.
func newPublishReach(edges stageEdges) *stageReach {
	return newStageReach(edges, isPublicationCarried)
}

// newStageReach indexes the closure of one ordering.
func newStageReach(edges stageEdges, isCarriedThrough func(model.StageRelation) bool) *stageReach {
	return &stageReach{
		stageEdges:       edges,
		isCarriedThrough: isCarriedThrough,
		nearest:          make(map[string][]string, len(edges.relations)),
	}
}

// Indirect lists the changed packages this index orders before one consumer
// and that the consumer does not name as a provider itself, sorted, so the
// cascade names the same blocker on every run.
func (s *stageReach) Indirect(consumer string) []string {
	var reached []string
	seen := make(map[string]bool)
	for _, provider := range s.providers[consumer] {
		if s.changed[provider] || !s.isCarriedThrough(s.relations[provider]) {
			continue
		}
		for _, behind := range s.resolve(provider) {
			if seen[behind] {
				continue
			}
			seen[behind] = true
			reached = append(reached, behind)
		}
	}
	sort.Strings(reached)
	return reached
}

// resolveCarriers walks every package the consumer reaches its providers
// through, so a later Indirect or isAnyReachedDead only reads the memo.
func (s *stageReach) resolveCarriers(consumer string) {
	for _, provider := range s.providers[consumer] {
		if !s.changed[provider] && s.isCarriedThrough(s.relations[provider]) {
			s.resolve(provider)
		}
	}
}

// isAnyReachedDead reports whether a changed provider the consumer reaches
// through packages this run does not release failed, was skipped or had its
// records blocked.
func (s *stageReach) isAnyReachedDead(consumer string, results map[string]*Result) bool {
	for _, provider := range s.providers[consumer] {
		if s.changed[provider] || !s.isCarriedThrough(s.relations[provider]) {
			continue
		}
		if slices.ContainsFunc(s.resolve(provider), func(behind string) bool { return isDead(results[behind]) }) {
			return true
		}
	}
	return false
}

// isDead reports whether a provider's result is one the skip cascade acts on.
func isDead(res *Result) bool {
	return res != nil && (res.RecordBlocked || res.Status == StatusFailed || res.Status == StatusSkipped)
}

// resolve is the memoised walk: the changed packages one package reaches
// through hops that carry this index's ordering.
func (s *stageReach) resolve(name string) []string {
	if cached, isKnown := s.nearest[name]; isKnown {
		return cached
	}
	var reached []string
	seen := make(map[string]bool)
	add := func(candidate string) {
		if seen[candidate] {
			return
		}
		seen[candidate] = true
		reached = append(reached, candidate)
	}
	for _, provider := range s.providers[name] {
		if !s.isCarriedThrough(s.relations[provider]) {
			continue
		}
		if s.changed[provider] {
			add(provider)
			continue
		}
		for _, behind := range s.resolve(provider) {
			add(behind)
		}
	}
	s.nearest[name] = reached
	return reached
}

// formatSkipReason renders why a package the run planned produced nothing. The
// sentence has to stay true for each relation separately, because it is what a
// CI log offers an operator in place of the release they expected.
func formatSkipReason(blocker string, relation model.StageRelation, isRecordBlocked bool) string {
	if isRecordBlocked {
		return "provider " + blocker +
			" has incomplete release records; repair its records before releasing dependents"
	}
	if relation.IsBuildWaitingPublish() {
		return "provider " + blocker +
			" failed or was skipped, and this package's build takes its publish as input"
	}
	if relation.IsBlocking {
		return "provider " + blocker +
			" failed or was skipped, and its space declares that its consumers publish only after it published"
	}
	return "provider " + blocker + " failed or was skipped, and the package has no changes of its own"
}

// formatEmbeddedSkipReason renders the other way a publish is blocked: the
// package had a release reason of its own and proceeded, its version stage
// wrote the provider's planned version into its manifests, its build then ran
// over them, and only afterwards did the provider die.
//
// Reconciling the manifests back to what the provider published would not
// reach the artefact the build already produced, so the conservative half of
// §19.2a applies: dispat will not silently rebuild, and a manifest naming a
// version that was never published must not be published (§19.5). The sentence
// says which of the two happened, because "provider failed" alone would send
// an operator looking at a package that has changes of its own and a relation
// that does not block.
func formatEmbeddedSkipReason(blocker string) string {
	return "provider " + blocker +
		" failed or was skipped after this package's build had already embedded its planned version; " +
		"rerun the release to rebuild against what the provider actually published"
}
