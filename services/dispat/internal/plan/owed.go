// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The owed windows of §13.3.
//
// A consumer that released past a provider's commit before the provider
// published it is still owed the provider's version (§13.4a). Once the
// provider has published the commit in a run the consumer sat out, no
// ordinary window holds it: the provider's window starts after its new
// release and the consumer's after its own, which is past the commit. The
// unit is then never parsed, and the debt the delivery test would find is
// invisible. The owed window of a pair (P, D) is the history after the newest
// release of P that D's baseline reaches: exactly the commits D released past
// before P released them, and the whole history when D's baseline reaches no
// release of P at all.
//
// The pairs are not only the edges. §9.2 propagates from P to every target
// within a unit's depth, so with D → M → P a consumer M in between having been
// served says nothing about D, and the window is taken for every consumer P
// reaches. A window makes a debt visible and admits nothing by itself: which
// contributions are owed is still §13.4a's question, asked of the parsed
// units exactly as for any other commit of the union.
//
// Nearly always there is nothing to read. A consumer that never got ahead of
// its provider has a baseline reaching the provider's own boundary, so its
// owed window is an ordinary one, and a window the union already holds is not
// read again. Only a pair whose window reaches further back than the union
// costs a history read, one per distinct boundary (§13.11's k).

// rootWindowKey is the cache key of the window that is the whole history.
var rootWindowKey = commitWindowCacheKey("", "")

// owedPair is one pair (P, D) of §13.3 with D's baseline in P's repository,
// the boundary delivery is measured against.
type owedPair struct {
	provider, consumer, baseline string
}

// listOwedPairs lists the pairs of §13.3 that can name a distinct window: D
// reachable from P over propagation.kinds, P with at least one parsed release
// and D with a baseline in P's repository, the first pair of each (provider,
// baseline), since the window depends on nothing else. total counts every
// pair. Providers come in plan order and consumers in walk order, so the
// windows are read, and the union extended, the same way on every run.
//
// Only the distinct pairs are kept: every consumer a provider reaches is a
// pair, which in a long dependency chain is a quadratic number of them, and
// packages released at one commit share a baseline.
func (cp *computation) listOwedPairs() (pairs []owedPair, total int) {
	kinds := cp.resolveOwedKinds()
	found := make(map[[2]string]bool)
	for _, provider := range cp.order {
		if !isAnyReleaseParsed(cp.tags[provider]) {
			continue // nothing released, so nobody can have got ahead of a release
		}
		repository := ""
		if p := cp.byName[provider]; p != nil {
			repository = p.Repository
		}
		for _, t := range cp.walk(map[string]bool{provider: true}, depthUnbounded, kinds) {
			baseline := cp.baselineBoundary(t.name, historyKey(repository, ""))
			if baseline == "" {
				continue // a consumer that never released has overtaken nothing
			}
			total++
			if key := [2]string{provider, baseline}; !found[key] {
				found[key] = true
				pairs = append(pairs, owedPair{provider: provider, consumer: t.name, baseline: baseline})
			}
		}
	}
	return pairs, total
}

// resolveOwedKinds is propagation.kinds (§8.4): the configured parser's in one
// history, and every repository parser's together in a composed workspace,
// since a pair must be taken wherever any of them lets a unit travel. With no
// parser at all the specification default applies.
func (cp *computation) resolveOwedKinds() map[model.DepKind]bool {
	var parsers []*ccme.Parser
	if len(cp.histories) == 0 {
		parsers = append(parsers, cp.parser)
	} else {
		names := make([]string, 0, len(cp.parsers))
		for name := range cp.parsers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			parsers = append(parsers, cp.parsers[name])
		}
	}
	union := make(map[model.DepKind]bool)
	for _, parser := range parsers {
		configured := ccme.DefaultPropagateKinds()
		if parser != nil {
			configured = parser.Config().Propagation.Kinds
		}
		kinds := kindSet(configured)
		if kinds == nil {
			return nil // every kind
		}
		for kind, isTraversed := range kinds {
			union[kind] = union[kind] || isTraversed
		}
	}
	return union
}

// isAnyReleaseParsed reports whether a tag listing holds at least one release
// the planner can read a version from.
func isAnyReleaseParsed(tags gitx.Tags) bool {
	for _, tag := range tags {
		if tag.Parsed {
			return true
		}
	}
	return false
}

// findOwedRelease is b* of §13.3 for one pair: the newest release of the
// provider, in inventory order, whose commit the consumer's baseline reaches.
// false means the baseline reaches none and the owed window is the whole
// history. A tag that does not parse, or has no commit to position, names no
// release that can have delivered anything, exactly as releaseCommits reads
// the same listing.
func (cp *computation) findOwedRelease(pair owedPair, isLoaded func(string) bool) (gitx.Tag, bool) {
	for _, tag := range cp.tags[pair.provider] {
		if !tag.Parsed || tag.Commit == "" {
			continue
		}
		if cp.isReleaseReachedBy(cp.tagCommitKey(pair.provider, tag.Commit), pair, isLoaded) {
			return tag, true
		}
	}
	return gitx.Tag{}, false
}

// isReleaseReachedBy reports whether a release commit is an ancestor-or-self
// of the pair's consumer baseline. A release commit the union does not hold is
// behind every boundary the union was read from, because a window is exactly
// what its boundary does not reach, so it is behind the baseline too whenever
// the baseline is one of them, or reaches one (isBaselineAboveLoadedStable):
// those answers cost nothing. Anything else is an ordinary ancestry question,
// a bitset lookup for two commits of the union.
func (cp *computation) isReleaseReachedBy(release string, pair owedPair, isLoaded func(string) bool) bool {
	if cp.byKey[release] == nil && (isLoaded(pair.baseline) || cp.isBaselineAboveLoadedStable(pair, isLoaded)) {
		return true
	}
	return cp.ancestorOrSelf(release, pair.baseline)
}

// isBaselineAboveLoadedStable reports, in a single history, that the pair's
// consumer baseline reaches the consumer's own stable boundary, which the
// union was read from. That is a consumer on a prerelease train: its baseline
// is a prerelease tag after the stable one, and no boundary itself. Only the
// marker index is asked, never git, so an answer it cannot give is a no, and
// the ordinary ancestry question follows.
func (cp *computation) isBaselineAboveLoadedStable(pair owedPair, isLoaded func(string) bool) bool {
	if len(cp.histories) > 0 {
		return false
	}
	rel := cp.rel[pair.consumer]
	if rel == nil || rel.StableCommit == "" || !isLoaded(rel.StableCommit) {
		return false
	}
	isReached, known := cp.markedAncestor(rel.StableCommit, pair.baseline)
	return known && isReached
}

// unionFrontier is where one repository's part of the union meets the history
// it leaves out: the union's commits with a parent outside it, and those
// parents. The union is closed under descendants, so every commit it leaves
// out is an ancestor-or-self of one of those parents, and the frontier is
// enough to decide whether a further window adds anything.
type unionFrontier struct {
	lower   []string
	outside map[string]bool
	commits int
}

// newUnionFrontier reads the frontier of one repository's commits in the
// union, "" being the single history.
func (cp *computation) newUnionFrontier(repository string) unionFrontier {
	frontier := unionFrontier{outside: make(map[string]bool)}
	for _, rec := range cp.commits {
		if !strings.EqualFold(rec.repository, repository) {
			continue
		}
		frontier.commits++
		isLower := false
		for _, parent := range cp.parents[rec.key] {
			if cp.byKey[parent] != nil {
				continue
			}
			frontier.outside[parent] = true
			isLower = true
		}
		if isLower {
			frontier.lower = append(frontier.lower, rec.key)
		}
	}
	return frontier
}

// isHeldByUnion reports whether the union already holds the window after the
// boundary ("" in the raw part being the whole history), which is exactly
// when every commit the union leaves out is an ancestor-or-self of the
// boundary. A boundary inside the union holds when every commit on the
// frontier is behind it; one outside holds only when it is the frontier's one
// parent; the whole history is held only by a union that leaves nothing out.
// With no parent pointers nothing says where the union ends, and the window
// is read.
func (cp *computation) isHeldByUnion(boundary string, frontier unionFrontier) bool {
	if !cp.linked {
		return false
	}
	if rawHistoryKey(boundary) == "" {
		return frontier.commits > 0 && len(frontier.outside) == 0
	}
	if cp.byKey[boundary] == nil {
		return len(frontier.outside) == 1 && frontier.outside[boundary]
	}
	for _, lower := range frontier.lower {
		if !cp.ancestorOrSelf(lower, boundary) {
			return false
		}
	}
	return true
}

// resetUnionAncestry drops what was derived from the union before it grew:
// the marker index and its budget, the tag commits recorded as behind it, the
// delivery candidates read off it and the memoised fallback answers, which the
// parent pointers and ranks of the new commits can change.
func (cp *computation) resetUnionAncestry() {
	cp.anc = nil
	cp.behindUnion = nil
	cp.releasedCommits = nil
	clear(cp.ancCache)
}

// loadOwedWindows adds the owed windows of §13.3 to one history's union,
// after its ordinary windows have been read from the given boundaries.
func (cp *computation) loadOwedWindows(ordinary []windowBoundary) error {
	isOrdinary := make(map[string]bool, len(ordinary))
	loaded := make(map[string]bool, len(ordinary))
	for _, b := range ordinary {
		if b.key == rootWindowKey {
			return nil // a package's window is the whole history, so the union is too
		}
		isOrdinary[b.key] = true
		if b.commit != "" {
			loaded[b.commit] = true
		}
	}
	isLoaded := func(boundary string) bool { return loaded[boundary] }

	pairs, total := cp.listOwedPairs()
	var owed []windowBoundary
	var frontier *unionFrontier
	for _, pair := range pairs {
		boundary := windowBoundary{key: rootWindowKey, pkg: pair.consumer}
		if tag, isReached := cp.findOwedRelease(pair, isLoaded); isReached {
			boundary = windowBoundary{key: commitWindowCacheKey(tag.Commit, tag.Name),
				since: tag.Name, commit: tag.Commit, pkg: pair.consumer}
		}
		if isOrdinary[boundary.key] {
			continue // the ordinary window of whoever released there
		}
		if frontier == nil {
			f := cp.newUnionFrontier("")
			frontier = &f
		}
		isOrdinary[boundary.key] = true // read at most once
		if cp.isHeldByUnion(boundary.commit, *frontier) {
			continue
		}
		owed = append(owed, boundary)
		if boundary.key == rootWindowKey {
			// The whole history holds every other owed window, so nothing
			// read after it could add a commit.
			break
		}
	}
	cp.log.Debug().Int("pairs", total).Int("boundaries", len(owed)).Msg("plan: owed windows examined")
	if len(owed) == 0 {
		return nil
	}

	lists, err := cp.readOwedWindows(owed)
	if err != nil {
		return err
	}
	for i, b := range owed {
		if cp.stats != nil {
			cp.stats.CommitWindows.Add(1)
			cp.stats.WindowCommitRefs.Add(int64(len(lists[i])))
		}
		cp.log.Debug().Str("boundary", b.key).Str("consumer", b.pkg).Int("commits", len(lists[i])).
			Msg("plan: owed window indexed")
	}
	cp.buildUnion(lists)
	cp.resetUnionAncestry()
	cp.indexAncestry()
	return nil
}

// readOwedWindows reads the owed windows' listings, each exactly the listing
// Commits gives for its boundary. A Git implementation with a union walk
// (gitx.UnionHistoryx) reads them all at once and each is recovered from the
// walk by the marker pass, as the ordinary windows are (readUnionWindow); any
// other reads one per boundary.
func (cp *computation) readOwedWindows(owed []windowBoundary) ([][]gitx.Commit, error) {
	if lists, err := cp.readUnionLists(owed); lists != nil || err != nil {
		return lists, err
	}
	lists := make([][]gitx.Commit, 0, len(owed))
	for _, b := range owed {
		if err := cp.ctx.Err(); err != nil {
			return nil, fmt.Errorf("plan: loading owed windows: %w", err)
		}
		commits, err := cp.git.Commits(cp.ctx, b.since)
		if err != nil {
			return nil, fmt.Errorf("plan: owed window for %s: %w", b.pkg, err)
		}
		lists = append(lists, commits)
	}
	return lists, nil
}

// readUnionLists reads every boundary's window in one union walk and answers
// each window's listing, in the walk's order, which is the order Commits
// lists it in (gitx.UnionHistoryx). It answers nil when the walk does not
// apply: no capability, a single boundary, a boundary with no commit id, a
// boundary off HEAD's line, or an index past its budget.
func (cp *computation) readUnionLists(boundaries []windowBoundary) ([][]gitx.Commit, error) {
	union, ok := cp.git.(gitx.UnionHistoryx)
	if !ok || len(boundaries) < 2 {
		return nil, nil
	}
	ids := make([]string, len(boundaries))
	for i, b := range boundaries {
		if b.commit == "" && b.since != "" {
			return nil, nil
		}
		ids[i] = b.commit // "" is the whole history
	}
	if err := cp.ctx.Err(); err != nil {
		return nil, fmt.Errorf("plan: loading owed windows: %w", err)
	}
	all, err := union.CommitsSinceAny(cp.ctx, ids)
	if errors.Is(err, gitx.ErrBoundaryNotBehindHead) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plan: owed window for %s: %w", boundaries[0].pkg, err)
	}
	excluded, isIndexed := excludedByBoundaries(all, ids)
	if !isIndexed {
		return nil, nil
	}
	lists := make([][]gitx.Commit, len(boundaries))
	for i := range boundaries {
		list := make([]gitx.Commit, 0, len(all)-excluded[i].len())
		for pos, commit := range all {
			if !excluded[i].has(pos) {
				list = append(list, commit)
			}
		}
		lists[i] = list
	}
	return lists, nil
}

// excludedByBoundaries is, for each boundary, the commits of a union walk it
// is an ancestor-or-self of: what its window leaves out of the walk. A
// boundary the walk does not hold is behind every commit of it, so it leaves
// nothing out (nil). isIndexed is false when the parents do not describe a
// DAG or the marker index is past its budget.
func excludedByBoundaries(all []gitx.Commit, ids []string) (excluded []*commitSet, isIndexed bool) {
	at := make(map[string]int32, len(all))
	for i, c := range all {
		at[c.SHA] = int32(i)
	}
	index := newAncestryIndex(len(all), func(pos int) []int32 {
		var parents []int32
		for _, p := range all[pos].Parents {
			if i, ok := at[p]; ok {
				parents = append(parents, i)
			}
		}
		return parents
	})
	if index == nil {
		return nil, false
	}
	var markers []int32
	for _, id := range ids {
		if pos, ok := at[id]; ok {
			markers = append(markers, pos)
		}
	}
	index.mark(markers)
	excluded = make([]*commitSet, len(ids))
	for i, id := range ids {
		if pos, ok := at[id]; ok {
			if excluded[i] = index.ancestors(pos); excluded[i] == nil {
				return nil, false
			}
		}
	}
	return excluded, true
}

// loadRepositoryOwedWindows is loadOwedWindows over a composed workspace:
// each pair's window is a window over the provider's repository, where the
// consumer's position is its boundary there (§§27.6, 27.11).
func (cp *computation) loadRepositoryOwedWindows(idx *windowIndex) error {
	// Windows are decided first and read after, so that one repository's
	// owed windows are one union walk. A window decided on is read as far as
	// every later decision is concerned, exactly as when each was read the
	// moment it was decided.
	type owedLoad struct {
		history            RepositoryHistory
		boundary, consumer string
	}
	var queued []owedLoad
	decided := make(map[string]bool)
	isRead := func(key string) bool {
		_, isListed := idx.commitLists[key]
		return isListed || decided[key]
	}
	isLoaded := func(boundary string) bool {
		repository, raw := splitHistoryKey(boundary)
		history, ok := cp.histories[globx.Fold(repository)]
		return ok && isRead(historyKey(history.Name, raw))
	}
	pairs, total := cp.listOwedPairs()
	frontiers := make(map[string]unionFrontier)
	listed := len(idx.lists)
	for _, pair := range pairs {
		history, ok := cp.histories[globx.Fold(cp.byName[pair.provider].Repository)]
		if !ok {
			continue
		}
		if isRead(historyKey(history.Name, "")) {
			continue // that repository's union is its whole history
		}
		boundary := historyKey(history.Name, "")
		if tag, isReached := cp.findOwedRelease(pair, isLoaded); isReached {
			boundary = historyKey(history.Name, tag.Commit)
		}
		if isRead(boundary) {
			continue // an ordinary window, or an owed one already read
		}
		folded := globx.Fold(history.Name)
		frontier, isComputed := frontiers[folded]
		if !isComputed {
			frontier = cp.newUnionFrontier(history.Name)
			frontiers[folded] = frontier
		}
		if cp.isHeldByUnion(boundary, frontier) {
			continue
		}
		decided[boundary] = true
		queued = append(queued, owedLoad{history: history, boundary: boundary, consumer: pair.consumer})
	}
	cp.log.Debug().Int("pairs", total).Int("boundaries", len(queued)).Msg("plan: owed windows examined")
	if len(queued) == 0 {
		return nil
	}
	var order []owedLoad // the first window of each repository
	byRepository := make(map[string][]string)
	for _, q := range queued {
		folded := globx.Fold(q.history.Name)
		if _, isSeen := byRepository[folded]; !isSeen {
			order = append(order, q)
		}
		_, raw := splitHistoryKey(q.boundary)
		byRepository[folded] = append(byRepository[folded], raw)
	}
	for _, first := range order {
		raws := byRepository[globx.Fold(first.history.Name)]
		if err := cp.readRepositoryUnion(idx, first.history, raws, first.consumer); err != nil {
			return err
		}
	}
	for _, q := range queued {
		if _, _, err := cp.load(idx, q.history, q.boundary, q.consumer); err != nil {
			return err
		}
	}
	cp.buildRepositoryUnion(idx.lists[listed:], idx.canonical)
	cp.resetUnionAncestry()
	cp.indexRepositoryAncestry()
	return nil
}

// collectOwedBoundaries is the release's owedBoundaries: per provider in its
// Sources, the raw commit of its newest release in that provider's repository.
func (cp *computation) collectOwedBoundaries(rel *Release) map[string]string {
	if len(rel.Sources) == 0 {
		return nil
	}
	boundaries := make(map[string]string)
	for _, source := range rel.Sources {
		if _, isRecorded := boundaries[source.Provider]; isRecorded {
			continue
		}
		repository := ""
		if provider := cp.byName[source.Provider]; provider != nil {
			repository = provider.Repository
		}
		if boundary := rawHistoryKey(cp.baselineBoundary(rel.Pkg.Name, historyKey(repository, ""))); boundary != "" {
			boundaries[source.Provider] = boundary
		}
	}
	return boundaries
}

// OwedBoundary is the raw commit this package's newest release sits on in the
// provider's repository, when the provider is one of the sources it is owed:
// the commit a release of that provider must not land on or behind while the
// package has not released after it (§19.3). Empty otherwise.
func (r *Release) OwedBoundary(provider string) string { return r.owedBoundaries[provider] }

// OwedPair is one provider a run would release at the baseline commit of a
// consumer it still owes, without releasing the consumer after it (E201).
type OwedPair struct {
	// Provider releases in the run; Consumer is owed its contribution and does
	// not release; Commit is the consumer's baseline, where the provider's tag
	// would land.
	Provider, Consumer, Commit string
}

// OwedAtHead lists, in plan order, the pairs §19.3 refuses before anything is
// published: the provider releases after Narrow, the consumer has it among
// the sources it is owed and does not release, and a release of the provider
// made at its repository's head would sit at or behind the consumer's newest
// release in that repository, where ancestry reads the consumer as served.
// Two releases on one commit have no ancestry order, so the consumer would
// then stay on the provider's old version with nothing left to detect it.
//
// isHeadReached answers whether a repository's head is an ancestor-or-self of
// a commit in it, "" naming the single history. It is ancestry rather than
// equality because a consumer's boundary in a composed workspace is a pin or
// a tuple, which need not be behind the head, and the check after
// publication asks the same question of the tag it wrote.
//
// The test is conservative where a release commit moves the provider's tag
// off the head: a run cannot know before it publishes whether that commit will
// be empty, and an empty one leaves the tag on the head.
func (p *Plan) OwedAtHead(isHeadReached func(repository, commit string) bool) []OwedPair {
	return p.listOwedAtHead(isHeadReached, (*Release).IsReleasing)
}

// OwedAtHeadAmong is OwedAtHead for an invocation that releases only the named
// packages of the plan, in plan order, as the standalone `dispat commit --tag`
// does: a package the plan releases and the invocation does not cover is not
// released after the provider, so it is owed exactly as a consumer the plan
// leaves out.
func (p *Plan) OwedAtHeadAmong(names []string, isHeadReached func(repository, commit string) bool) []OwedPair {
	isCovered := make(map[string]bool, len(names))
	for _, name := range names {
		isCovered[name] = true
	}
	return p.listOwedAtHead(isHeadReached, func(r *Release) bool {
		return isCovered[r.Pkg.Name] && r.IsReleasing()
	})
}

// listOwedAtHead is OwedAtHead with the releasing packages told by a
// predicate.
func (p *Plan) listOwedAtHead(isHeadReached func(repository, commit string) bool, isReleased func(*Release) bool) []OwedPair {
	position := make(map[string]int, len(p.Order))
	for i, name := range p.Order {
		position[name] = i
	}
	var pairs []OwedPair
	for _, consumerName := range p.Order {
		consumer := p.Releases[consumerName]
		if consumer == nil || isReleased(consumer) || len(consumer.owedBoundaries) == 0 {
			continue
		}
		providers := make([]string, 0, len(consumer.owedBoundaries))
		for provider := range consumer.owedBoundaries {
			providers = append(providers, provider)
		}
		sort.Slice(providers, func(i, j int) bool { return position[providers[i]] < position[providers[j]] })
		for _, providerName := range providers {
			provider := p.Releases[providerName]
			if provider == nil || !isReleased(provider) {
				continue
			}
			boundary := consumer.owedBoundaries[providerName]
			if !isHeadReached(provider.Pkg.Repository, boundary) {
				continue
			}
			pairs = append(pairs, OwedPair{Provider: providerName, Consumer: consumerName, Commit: boundary})
		}
	}
	return pairs
}
