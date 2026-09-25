// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// ancestorOrSelf reports whether a is an ancestor-or-self of b.
//
// Answers are memoised on the computation: the same (commit, baseline) pair
// is asked from several nested phases, and each uncached git answer is a
// subprocess — on a prerelease train the containment checks alone would
// otherwise fork once per commit×package.
func (cp *computation) ancestorOrSelf(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if yes, known := cp.markedAncestor(a, b); known {
		return yes
	}
	key := [2]string{a, b}
	if v, ok := cp.ancCache[key]; ok {
		return v
	}
	v := cp.ancestorLookup(a, b)
	if cp.ancCache == nil {
		cp.ancCache = make(map[[2]string]bool)
	}
	// A fleet can ask many independent ancestry questions. Bound retention
	// without changing answers; repeated work after eviction is preferable to
	// retaining a quadratic pair matrix for the lifetime of a large run.
	if len(cp.ancCache) >= 65536 {
		clear(cp.ancCache)
	}
	cp.ancCache[key] = v
	return v
}

// markedAncestor answers from the marker pass when it can: a is a commit of the
// union, b is one too (it becomes a marker on first asking, and the batches
// the phases register up front share one pass) or a tag commit behind the
// union, and both belong to one repository whose parent pointers are ancestry.
// Anything else is unknown here and goes to the Git implementation, exactly as
// every question did before.
func (cp *computation) markedAncestor(a, b string) (yes, known bool) {
	if cp.anc == nil {
		return false, false
	}
	ra := cp.byKey[a]
	if ra == nil || !cp.parentsAreAncestry(ra.repository) {
		return false, false
	}
	rb := cp.byKey[b]
	if rb == nil {
		return false, cp.behindUnion[b]
	}
	if !strings.EqualFold(ra.repository, rb.repository) {
		return false, false
	}
	set := cp.anc.ancestors(int32(rb.rank))
	if set == nil {
		cp.anc.mark([]int32{int32(rb.rank)})
		if set = cp.anc.ancestors(int32(rb.rank)); set == nil {
			return false, false // past the index's budget
		}
	}
	return set.has(ra.rank), true
}

// parentsAreAncestry reports whether the repository's Git implementation
// promises complete parent lists (gitx.UnionHistoryx), which is what lets
// ancestry among its commits be read off them.
func (cp *computation) parentsAreAncestry(repository string) bool {
	folded := globx.Fold(repository)
	if isTrusted, ok := cp.ancTrusted[folded]; ok {
		return isTrusted
	}
	git := cp.git
	if len(cp.histories) > 0 {
		git = cp.histories[folded].Git
	}
	_, isTrusted := git.(gitx.UnionHistoryx)
	if cp.ancTrusted == nil {
		cp.ancTrusted = make(map[string]bool)
	}
	cp.ancTrusted[folded] = isTrusted
	return isTrusted
}

// newUnionAncestry indexes the ranked union. A commit of a repository whose
// parents are not trusted is left without any, and is never asked about.
func (cp *computation) newUnionAncestry() *ancestryIndex {
	return newAncestryIndex(len(cp.commits), func(rank int) []int32 {
		rec := cp.commits[rank]
		if !cp.parentsAreAncestry(rec.repository) {
			return nil
		}
		var parents []int32
		for _, p := range cp.parents[rec.key] {
			if parent := cp.byKey[p]; parent != nil {
				parents = append(parents, int32(parent.rank))
			}
		}
		return parents
	})
}

// indexAncestry builds the index where the loader has not, and marks every
// baseline commit in one pass: stable and newest alike, since containment in
// a prerelease baseline is the question a train asks about every commit.
func (cp *computation) indexAncestry() {
	if len(cp.histories) > 0 || !cp.parentsAreAncestry("") {
		return
	}
	if cp.anc == nil {
		if cp.anc = cp.newUnionAncestry(); cp.anc == nil {
			return
		}
	}
	cp.behindUnion = make(map[string]bool)
	var markers []int32
	for _, p := range cp.pkgs {
		rel := cp.rel[p.Name]
		for _, commit := range []string{rel.StableCommit, rel.BaselineCommit} {
			if commit == "" {
				continue
			}
			if rec := cp.byKey[commit]; rec != nil {
				markers = append(markers, int32(rec.rank))
			} else {
				cp.behindUnion[commit] = true
			}
		}
	}
	cp.anc.mark(markers)
}

// indexRepositoryAncestry is indexAncestry over a composed workspace. The
// index is per repository without saying so: parents never leave the
// repository that owns them, so no set ever crosses one. A boundary the union
// does not hold stays unknown here, because a fleet's boundaries are pins and
// tuples as well as tags, and only a tag is promised to be behind HEAD.
func (cp *computation) indexRepositoryAncestry() {
	isTrusted := false
	for _, history := range cp.histories {
		isTrusted = isTrusted || cp.parentsAreAncestry(history.Name)
	}
	if !isTrusted {
		return
	}
	if cp.anc = cp.newUnionAncestry(); cp.anc == nil {
		return
	}
	var markers []int32
	for _, boundaries := range []map[string]map[string]string{cp.stableBoundaries, cp.publishedBoundaries} {
		for _, p := range cp.pkgs {
			// Marker order decides which bit a marker takes and nothing else.
			for _, boundary := range boundaries[p.Name] {
				if rec := cp.byKey[boundary]; rec != nil {
					markers = append(markers, int32(rec.rank))
				}
			}
		}
	}
	cp.anc.mark(markers)
}

// markDirectiveCommits registers, in one pass, the commits the phases after
// parsing ask about: every cancel barrier (§10.3) and every commit carrying a
// correction or a Reverts footer (§7.3, §13.4b).
func (cp *computation) markDirectiveCommits() {
	if cp.anc == nil {
		return
	}
	var markers []int32
	for _, rec := range cp.commits {
		for _, u := range rec.units {
			if u.IsCancel() || len(u.Directives.Edits)+len(u.Directives.Deletes)+len(u.Directives.Reverts) > 0 {
				markers = append(markers, int32(rec.rank))
				break
			}
		}
	}
	cp.anc.mark(markers)
}

// ancestorLookup answers one uncached ancestry question. Three sources of
// truth, in order of preference: the Git implementation, the parent pointers
// carried by the commits, and — when neither is available — history order,
// which is exact for the linear case and the only thing left otherwise.
// Ancestry rather than commit dates is what keeps cancellation deterministic
// under merges and rebases (§10.4).
//
// A real git failure — as opposed to gitx.ErrNoAncestry's "I have no answer,
// use the fallback" — is recorded once in cp.ancErr and aborts Compute: the
// fallbacks answer a weaker question, and silently degrading to them (on a
// cancelled context, say) would change which releases get cancelled or
// contained.
func (cp *computation) ancestorLookup(a, b string) bool {
	repoA, rawA := splitHistoryKey(a)
	repoB, rawB := splitHistoryKey(b)
	if !strings.EqualFold(repoA, repoB) {
		if strings.EqualFold(repoB, cp.controlRepo) {
			return cp.controlObserves(b, a)
		}
		return false
	}
	git, _, hasGit := cp.gitForKey(a)
	if !hasGit {
		git = cp.git
	}
	if !cp.ancNoGit[globx.Fold(repoA)] && cp.ancErr == nil {
		if cp.stats != nil {
			cp.stats.AncestryLookups.Add(1)
		}
		yes, err := git.IsAncestor(cp.ctx, rawA, rawB)
		switch {
		case err == nil:
			return yes
		case errors.Is(err, gitx.ErrNoAncestry):
			cp.ancNoGit[globx.Fold(repoA)] = true
		default:
			cp.ancErr = err
		}
	}
	if cp.linked {
		seen := map[string]bool{b: true}
		queue := []string{b}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, p := range cp.parents[cur] {
				if p == a {
					return true
				}
				if seen[p] {
					continue
				}
				seen[p] = true
				queue = append(queue, p)
			}
		}
		return false
	}
	ra, oka := cp.byKey[a]
	rb, okb := cp.byKey[b]
	if !oka || !okb {
		return false
	}
	return ra.rank >= rb.rank // newest first: later in the list is older
}

// ancestryFailed surfaces the first real git failure an ancestry question
// hit. Compute checks it between phases: once git has failed, every fallback
// answer after it is untrustworthy, so no plan may be emitted.
func (cp *computation) ancestryFailed() error {
	if cp.ancErr != nil {
		return fmt.Errorf("plan: ancestry query failed: %w", cp.ancErr)
	}
	return nil
}
