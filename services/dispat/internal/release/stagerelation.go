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
	"maps"
	"slices"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// resolveFirstTaskWaits answers which of a changed provider's tasks a
// consumer's first tasks (its beforeAll task when it has one, its first
// stages otherwise) have to wait for.
//
// The three relations are a chain rather than three unrelated rules: nothing,
// what the provider built, what it built and its records. The publish case
// names both edges rather than relying on the provider's own build-before-
// publish edge to imply the first, because the graph is read by people as well
// as by the scheduler and an edge nobody can see is an ordering nobody can
// check.
func resolveFirstTaskWaits(relation model.StageRelation, provider *stagePlan) []task {
	if !relation.IsBuildWaitingBuild() {
		return nil
	}
	if relation.IsBuildWaitingPublish() {
		return append(provider.built(), provider.recording)
	}
	return provider.built()
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
	gapKinds
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

// newBlockingGaps carries a failed provider's outcome through unreleasing
// packages without adding one edge per reachable provider and consumer pair.
func newBlockingGaps(edges stageEdges, consumers []string) *stageGaps {
	return newStageGaps(edges, consumers, blockingGapKinds)
}

// gapKinds is what one ordering asks of a hop and which tasks it orders.
type gapKinds struct {
	// isCarriedThrough is the question the ordering asks of every hop: does
	// it travel through a provider under this relation.
	isCarriedThrough func(model.StageRelation) bool
	// providerTasks are the tasks of a changed provider the ordering waits
	// for, and passThrough the kind of the gaps' nodes.
	providerTasks func(*stagePlan) []task
	passThrough   taskKind
}

var (
	buildGapKinds    = gapKinds{model.StageRelation.IsBuildWaitingBuild, (*stagePlan).built, taskPassBuild}
	publishGapKinds  = gapKinds{isPublicationCarried, recordingTask, taskPassPublish}
	blockingGapKinds = gapKinds{isPublicationCarried, recordingTask, taskPassBlock}
)

// recordingTask is the task of a provider the publication order waits for.
func recordingTask(sp *stagePlan) []task { return []task{sp.recording} }

// newStageGaps walks the graph from each consumer in name order, visiting
// every gap once.
func newStageGaps(edges stageEdges, consumers []string, kinds gapKinds) *stageGaps {
	g := &stageGaps{
		stageEdges:   edges,
		gapKinds:     kinds,
		isGap:        make(map[string]bool),
		firstReached: make(map[string][]string),
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
func (g *stageGaps) passThroughOf(gap string) task { return task{gap, g.passThrough, ""} }

// addPassThroughs registers the gaps' nodes, which the scheduler needs before
// any edge.
func (g *stageGaps) addPassThroughs(sched *graph.Scheduler[task]) {
	for _, gap := range g.gaps {
		sched.AddPassThrough(g.passThroughOf(gap))
	}
}

// addConsumerEdges states, in the consumer's turn, what the literal graph
// states there: the providers' tasks it registers first, and the waits of the
// consumer's tasks, here on the gaps it reads through.
func (g *stageGaps) addConsumerEdges(sched *graph.Scheduler[task], consumer string, waiting []task) {
	for _, provider := range g.firstReached[consumer] {
		for _, t := range g.providerTasks(g.plans[provider]) {
			sched.Add(t)
		}
	}
	for _, gap := range g.carryingGaps(consumer) {
		addEdges(sched, g.passThroughOf(gap), waiting)
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
				for _, t := range g.providerTasks(g.plans[provider]) {
					sched.AddEdge(t, g.passThroughOf(gap))
				}
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
// providers, which of them release, each provider's relation and each
// releasing package's stage plan.
type stageEdges struct {
	providers map[string][]string
	changed   map[string]bool
	relations map[string]model.StageRelation
	plans     map[string]*stagePlan
}

// newStageEdges indexes the plan's relations once, so a walk asks a map rather
// than following a pointer chain per edge. A name the plan carries no release
// for reads as the zero relation, which is the default one, and the walk goes
// on through it.
func newStageEdges(p *plan.Plan, changed map[string]bool, plans map[string]*stagePlan) stageEdges {
	relations := make(map[string]model.StageRelation, len(p.Releases))
	for name, rel := range p.Releases {
		relations[name] = rel.Pkg.Space.ProviderRelation
	}
	return stageEdges{providers: p.Providers, changed: changed, relations: relations, plans: plans}
}

// publicationStageEdges follows only the manifest kinds selected for
// publication. Hand-constructed plans without this index keep their legacy
// provider graph, which is the default relation in their tests and callers.
func publicationStageEdges(p *plan.Plan, changed map[string]bool, plans map[string]*stagePlan) stageEdges {
	edges := newStageEdges(p, changed, plans)
	if p.PublishProviders != nil {
		edges.providers = p.PublishProviders
	}
	return edges
}

func blockingStageEdges(p *plan.Plan, changed map[string]bool, plans map[string]*stagePlan) stageEdges {
	edges := newStageEdges(p, changed, plans)
	if p.BlockingProviders != nil {
		edges.providers = p.BlockingProviders
	}
	edges.providers = condenseBlockingGaps(edges.providers, changed)
	return edges
}

// condenseBlockingGaps keeps only paths that can carry a releasing provider's
// outcome, and collapses cycles made solely of unreleasing packages. Such a
// cycle is not itself an execution cycle: the packages perform no task, and
// every consumer of the component needs the same changed providers behind it.
// Cycles among releasing packages stay as task edges for E233 to diagnose.
func condenseBlockingGaps(providers map[string][]string, changed map[string]bool) map[string][]string {
	nodes := make(map[string]bool)
	for consumer, direct := range providers {
		if !changed[consumer] {
			nodes[consumer] = true
		}
		for _, provider := range direct {
			if !changed[provider] {
				nodes[provider] = true
			}
		}
	}
	index := 0
	indices := make(map[string]int, len(nodes))
	low := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	representative := make(map[string]string, len(nodes))
	var stack []string
	var visit func(string)
	visit = func(name string) {
		index++
		indices[name], low[name] = index, index
		stack = append(stack, name)
		onStack[name] = true
		for _, provider := range providers[name] {
			if changed[provider] {
				continue
			}
			if indices[provider] == 0 {
				visit(provider)
				low[name] = min(low[name], low[provider])
			} else if onStack[provider] {
				low[name] = min(low[name], indices[provider])
			}
		}
		if low[name] != indices[name] {
			return
		}
		var component []string
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == name {
				break
			}
		}
		sort.Strings(component)
		for _, member := range component {
			representative[member] = component[0]
		}
	}
	for _, name := range slices.Sorted(maps.Keys(nodes)) {
		if indices[name] == 0 {
			visit(name)
		}
	}
	canonical := func(name string) string {
		if rep := representative[name]; rep != "" {
			return rep
		}
		return name
	}
	condensed := make(map[string][]string, len(providers))
	seen := make(map[string]map[string]bool)
	for consumer, direct := range providers {
		from := canonical(consumer)
		if seen[from] == nil {
			seen[from] = make(map[string]bool)
		}
		for _, provider := range direct {
			to := canonical(provider)
			if from == to || seen[from][to] {
				continue
			}
			seen[from][to] = true
			condensed[from] = append(condensed[from], to)
		}
	}
	relevant := make(map[string]bool)
	known := make(map[string]bool)
	var carriesChanged func(string) bool
	carriesChanged = func(name string) bool {
		if changed[name] {
			return true
		}
		if known[name] {
			return relevant[name]
		}
		known[name] = true
		for _, provider := range condensed[name] {
			if carriesChanged(provider) {
				relevant[name] = true
			}
		}
		return relevant[name]
	}
	for consumer, direct := range condensed {
		kept := direct[:0]
		for _, provider := range direct {
			if carriesChanged(provider) {
				kept = append(kept, provider)
			}
		}
		sort.Strings(kept)
		condensed[consumer] = kept
	}
	return condensed
}

// blockingNeedsOutcomeEdges answers whether the publication graph already
// orders every blocking edge. When it does, its pass-throughs carry the whole
// blocking closure and a second set of nodes and edges would say nothing new.
func blockingNeedsOutcomeEdges(blocking, publication stageEdges) bool {
	for consumer, blocked := range blocking.providers {
		ordered := make(map[string]bool, len(publication.providers[consumer]))
		for _, provider := range publication.providers[consumer] {
			ordered[provider] = true
		}
		for _, provider := range blocked {
			if !ordered[provider] {
				return true
			}
		}
	}
	return false
}

// releaseOutcomeProviders lists the changed providers whose outcomes a
// consumer's record must know even when publish.orderKinds omits their edges.
// The blocking closure can withhold a record despite independent work. Other
// causes need an outcome wait only when the release has no cause that already
// survives every provider failure. A provider already ordered by publication
// needs no second task edge.
func releaseOutcomeProviders(name string, p *plan.Plan, changed map[string]bool) []string {
	rel := p.Releases[name]
	publish := p.PublishProviders
	if publish == nil {
		publish = p.Providers
	}
	seen := make(map[string]bool)
	for _, provider := range publish[name] {
		seen[provider] = true
	}
	var providers []string
	add := func(provider string) {
		if provider == name || !changed[provider] || seen[provider] {
			return
		}
		seen[provider] = true
		providers = append(providers, provider)
	}
	if !hasIndependentOutcomeCause(rel, changed) {
		for _, provider := range rel.DueTo {
			add(provider)
		}
		for _, update := range rel.Updates {
			add(update.Name)
		}
		add(rel.ChannelFrom)
	}
	sort.Strings(providers)
	return providers
}

// hasIndependentOutcomeCause is §19.3's own-cause test with every releasing
// provider provisionally failed. A prior publication is already settled;
// fresh work, an exact pin and a direct channel directive need no provider.
func hasIndependentOutcomeCause(rel *plan.Release, changed map[string]bool) bool {
	if rel.IsFreshOwnBump() || rel.Pinned ||
		(rel.IsChannelChanged() && !changed[rel.ChannelFrom]) {
		return true
	}
	for _, update := range rel.Updates {
		if !changed[update.Name] {
			return true
		}
	}
	return false
}

// versionPickupProviders is the changed portion of a release's declared
// version pickups. These providers must settle before a version frame that
// consumes pickups starts, so the frame writes their actual published state
// once rather than a speculative planned state it may have to undo later.
func versionPickupProviders(rel *plan.Release, changed map[string]bool) []string {
	space := rel.Pkg.Space
	if len(rel.Updates) == 0 || (space.AutoVersion == nil && len(space.BeforeVersionScript) == 0 &&
		len(space.VersionScript) == 0 && len(space.PostVersionScript) == 0) {
		return nil
	}
	seen := make(map[string]bool)
	var providers []string
	for _, update := range rel.Updates {
		if !changed[update.Name] || update.Name == rel.Pkg.Name || seen[update.Name] {
			continue
		}
		seen[update.Name] = true
		providers = append(providers, update.Name)
	}
	sort.Strings(providers)
	return providers
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
		nearest:          make(map[string][]string),
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
