// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"strings"
)

// The bump axis's admission (§9.2 phase 3, §13.4a): a source package's
// contribution at one commit is owed to a dependent until a release of that
// source delivered it.
//
// The window alone cannot say that. A window answers "has this package
// released past this commit", which is the right question for the package's
// own work and the wrong one for somebody else's: a consumer that released on
// a reason of its own (a fresh bump, a channel change) while its provider's
// publish failed carries the commit behind its own baseline and is owed the
// provider's version all the same. Asked only about its window it is never
// planned again, keeps the provider's previous version for ever and reports
// nothing: the orphan this file exists to prevent.
//
// The two halves are ordered by cost, and the order is not an optimisation
// detail but what keeps the rule affordable. A target whose pending window
// still holds the commit has been delivered nothing by anybody (delivery
// happens in a release, and a release leaves the commit behind), so the owed
// set is the whole source set and one bitset lookup answers the whole
// question. Only a target that got ahead of the commit is asked the finer
// one, and getting ahead is rare: it takes a failed or held provider and a
// consumer with work of its own.

// owedSources is owed(u, d) for a target whose pending window no longer holds
// the unit's commit: the unit's source packages that no release of theirs has
// delivered to the target. An empty result means the unit has nothing left to
// give this target and admission stands exactly where it stood before the
// delivery test existed.
//
// sources is the unit's source set after §13.4a suppression, and the result is
// a subset of it in name order, because §9.2's prov[d] |= owed attributes the
// owed sources to the dependent and only those: a source whose version the
// target already carries is not a reason it releases again.
func (cp *computation) owedSources(target, commitKey string, sources map[string]bool) []string {
	baseline := cp.baselineBoundary(target, commitKey)
	if baseline == "" || cp.withoutDelivery {
		// Two ways there is nothing to ask: a target that has released nothing
		// has overtaken nothing, its window holding every commit; and the
		// window-only rule the differential test compares against never asks
		// the delivery question at all.
		return nil
	}
	if cp.byKey[baseline] == nil {
		// The target's baseline is not one of the union's commits, so no commit
		// of the union is behind it: a window is exactly the commits its
		// boundary does not reach, so a boundary the union does not hold is
		// behind every window. Nothing was overtaken, and the question is
		// answered without asking Git anything.
		return nil
	}
	if !cp.ancestorOrSelf(commitKey, baseline) {
		return nil // the target has not released past the commit
	}
	owed := make([]string, 0, len(sources))
	for _, name := range sortedKeys(sources) {
		if cp.isDelivered(name, commitKey, baseline) {
			continue
		}
		owed = append(owed, name)
	}
	if len(owed) == 0 {
		return nil
	}
	return owed
}

// baselineBoundary is baselineCommit(D) as a qualified key: the commit of the
// target's newest release tag in the history the commit belongs to. A fleet
// asks per repository, because a consumer has one boundary per participating
// history and the commit names which one is being asked about.
func (cp *computation) baselineBoundary(pkg, commitKey string) string {
	if len(cp.histories) > 0 {
		repository, _ := splitHistoryKey(commitKey)
		return cp.publishedBoundaries[pkg][strings.ToLower(repository)]
	}
	if rel := cp.rel[pkg]; rel != nil {
		// A single history's keys are raw commit ids, which is what
		// containedInBaseline reads here too.
		return rel.BaselineCommit
	}
	return ""
}

// isDelivered is delivered(P, C, D) from §13.4a: some release tag of P sits on
// a commit that carries C and that the target's baseline reaches. Both
// conditions are needed and neither implies the other. A release of P
// carrying C that the target's own release did not reach has delivered nothing
// to it, and a release of P the target reached that does not carry C delivered
// something older.
func (cp *computation) isDelivered(provider, commitKey, baseline string) bool {
	for _, released := range cp.releaseCommits(provider) {
		if !cp.ancestorOrSelf(commitKey, released) {
			continue // that release of the provider does not carry the commit
		}
		if cp.ancestorOrSelf(released, baseline) {
			return true
		}
	}
	return false
}

// releaseCommits lists, as qualified keys, the commits of the provider's
// release tags that the union holds, and registers them with the ancestry
// index in one pass the first time the provider is asked about.
//
// Tags outside the union are left out rather than asked about, for the reason
// owedSources leaves out a baseline outside it: a commit the union does not
// hold is behind every window, so it cannot carry a commit of the union and
// cannot have delivered one. That is what keeps the delivery test off Git:
// every question it does ask is a marker-pass bitset lookup (§13.11) rather
// than an `is-ancestor` fork per pair.
func (cp *computation) releaseCommits(provider string) []string {
	if known, ok := cp.releasedCommits[provider]; ok {
		return known
	}
	var released []string
	var markers []int32
	seen := make(map[string]bool, len(cp.tags[provider]))
	for _, tag := range cp.tags[provider] {
		if !tag.Parsed || tag.Commit == "" {
			continue // an unparseable tag names no release of this package
		}
		key := cp.tagCommitKey(provider, tag.Commit)
		if seen[key] {
			continue // a moving alias and its release tag are one commit
		}
		rec := cp.byKey[key]
		if rec == nil {
			continue
		}
		seen[key] = true
		released = append(released, key)
		markers = append(markers, int32(rec.rank))
	}
	if cp.anc != nil && len(markers) > 0 {
		cp.anc.mark(markers)
	}
	if cp.releasedCommits == nil {
		cp.releasedCommits = make(map[string][]string, len(cp.pkgs))
	}
	cp.releasedCommits[provider] = released
	return released
}

// tagCommitKey qualifies a tag's raw commit with the repository that owns the
// package, which is the history the tag was read from.
func (cp *computation) tagCommitKey(pkg, commit string) string {
	if p := cp.byName[pkg]; p != nil {
		return historyKey(p.Repository, commit)
	}
	return commit
}
