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
	"sort"

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

// stageReach indexes the orderings the dependency graph imposes on a consumer
// beyond the changed providers it names itself (§19.2, §19.2a).
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
// The two orderings are the same walk over the same graph asking one question
// of each hop, which is why they are one type: what differs is whether the
// ordering travels through a provider with a given relation, and nothing else.
//
// It is an index rather than extra nodes in the task graph. The specification
// suggests a pass-through node per package that does not build, which is the
// same asymptotics; here it would mean the scheduler holding nodes that no
// stage budget prices and that nothing executes, so every task the drain hands
// out would have to be asked first whether it is real. Answering the question
// beside the graph instead leaves the drain and the task execution exactly as
// they were. The walk visits each package once and memoises its answer, so one
// index costs one pass over the graph rather than a search per pair.
type stageReach struct {
	providers map[string][]string
	changed   map[string]bool
	relations map[string]model.StageRelation
	// isCarriedThrough is the question this index asks of every hop: does the
	// ordering it indexes travel through a provider under this relation. One
	// index answers one question, so its memo below is never a mixture of two.
	isCarriedThrough func(model.StageRelation) bool
	// nearest memoises, per package, the changed packages it reaches through
	// hops that carry the ordering, stopping at each one: a changed package's
	// own edges carry whatever lies behind it.
	nearest map[string][]string
}

// newBuildReach indexes the build orderings of §19.2a: a path ends at the
// first hop whose relation is `none`, which is the declaration that nothing
// the provider builds reaches the consumer's build, and what a package does
// not read it cannot pass on.
func newBuildReach(p *plan.Plan, changed map[string]bool) *stageReach {
	return newStageReach(p, changed, model.StageRelation.IsBuildWaitingBuild)
}

// newPublishReach indexes the publication orderings of §19.2: every path is
// walked to its end, because a relation cannot declare the publication order
// away. The consumer resolves the provider at install time through whatever
// lies between them, whether or not that build read anything of it.
func newPublishReach(p *plan.Plan, changed map[string]bool) *stageReach {
	return newStageReach(p, changed, isPublicationCarried)
}

// isPublicationCarried answers the publication walk's question of a hop, and
// the answer is the same for all three relations: "under all three, P
// publishes before C does" (§19.2a). It is written out rather than left
// implicit because the walk asks each hop its question, and a walk with no
// question would be a walk with a mode.
func isPublicationCarried(model.StageRelation) bool { return true }

// newStageReach indexes the plan's relations once, so the walk below asks a
// map rather than following a pointer chain per edge. A name the plan carries
// no release for reads as the zero relation, which is the default one, and the
// walk goes on through it.
func newStageReach(p *plan.Plan, changed map[string]bool,
	isCarriedThrough func(model.StageRelation) bool) *stageReach {
	relations := make(map[string]model.StageRelation, len(p.Releases))
	for name, rel := range p.Releases {
		relations[name] = rel.Pkg.Space.ProviderRelation
	}
	return &stageReach{
		providers:        p.Providers,
		changed:          changed,
		relations:        relations,
		isCarriedThrough: isCarriedThrough,
		nearest:          make(map[string][]string, len(p.Releases)),
	}
}

// Indirect lists the changed packages this index orders before one consumer
// and that the consumer does not name as a provider itself: the orderings
// reached through packages this run does not release. The direct ones are
// stated where each provider's own relation is read, so the graph is written
// once in the place a reader looks for it.
//
// The result is sorted, so the derived edges enter the scheduler in the same
// order on every run, which is what keeps launch order deterministic (§17.2).
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
// through hops that carry this index's ordering. It recurses along dependency
// edges alone, and the workspace graph is acyclic by the time a release
// executes, because a cycle is E197 or E200 and no plan survives one, so the
// recursion terminates.
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
