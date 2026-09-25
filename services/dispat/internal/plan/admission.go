// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import "github.com/yohimik/dispat/services/dispat/internal/globx"

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
// detail but what keeps the rule affordable. A target whose fresh window still
// holds the commit has been delivered nothing by anybody (delivery happens in
// a release, and a release carries the commit), so the owed set is every
// source within the unit's depth of it and one bitset lookup answers the whole
// question. Only a target whose release carries the commit is asked the finer
// one: one that got ahead of it on its stable line, and one whose prerelease
// train shipped it. Either takes a failed or held provider and a consumer
// with work of its own, or a train that already delivered, which the ancestry
// index answers without Git.

// bumpAdmission is one target's answer to one unit's bump (§9.2 phase 3).
type bumpAdmission struct {
	// owed are the sources that still owe the target this contribution, in
	// name order: the providers its release picks a version up from. Empty
	// when nothing is owed.
	owed []string
	// isCarried reports that a prerelease of the target's current train
	// already published the commit, so the bump counts toward the train's
	// target (§11.4) whether or not anything is still owed.
	isCarried bool
}

// admitBump is §13.4a's admission of one unit's bump for one target, from the
// sources within the unit's depth of it. false means the unit neither counts
// toward the target's version nor gives it a reason to release.
//
// Three positions of the target against the commit, and one question each:
//
//   - The commit is in Wfresh(target): nobody has delivered it, so every source
//     within reach owes it, unless a cancel of the target discarded the
//     contribution (§13.5a).
//   - A prerelease of the target's train published the commit: the bump
//     counts toward the train whatever else holds, and the target is still
//     owed by every source whose release it has not reached. The train
//     published the commit, not the source's version, so reading the pair as
//     shipped would discharge the debt §13.4a finds outstanding.
//   - The target released past the commit on its stable line: it is admitted
//     only for the sources that still owe it.
//
// In the last two a cancel of the target discards only the owed part: the
// target's own release is published and beyond any cancel (§10.3).
func (cp *computation) admitBump(target, commitKey string, sources []string) (bumpAdmission, bool) {
	isPending := cp.inWindow(target, commitKey)
	isCarried := isPending && cp.containedInBaseline(target, commitKey)
	if isPending && !isCarried {
		if cp.cancelledFor(commitKey, target) {
			return bumpAdmission{}, false
		}
		return bumpAdmission{owed: sources}, true
	}
	owed := cp.owedSources(target, commitKey, sources)
	if len(owed) > 0 && cp.cancelledForOwed(commitKey, target) {
		owed = nil
	}
	if len(owed) == 0 && !isCarried {
		return bumpAdmission{}, false
	}
	return bumpAdmission{owed: owed, isCarried: isCarried}, true
}

// owedSources is owed(u, d) for a target whose baseline carries the unit's
// commit, past its stable tag or on its prerelease train: the unit's source
// packages that no release of theirs has delivered to the target. An empty
// result means the unit has nothing left to give this target.
//
// sources is the unit's source set after §13.4a suppression, restricted to the
// sources within the unit's depth of the target (§9.2's from(d)) and in name
// order, and the result is a subset of it in that order, because §9.2's
// prov[d] |= owed attributes the owed sources to the dependent and only those:
// a source whose version the target already carries is not a reason it
// releases again.
func (cp *computation) owedSources(target, commitKey string, sources []string) []string {
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
	for _, name := range sources {
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
		return cp.publishedBoundaries[pkg][globx.Fold(repository)]
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
// package, which is the history the tag was read from. A name the workspace
// holds no package for owns no tags either, and the unqualified key it would
// produce is the one a single history uses anyway.
func (cp *computation) tagCommitKey(pkg, commit string) string {
	repository := ""
	if p := cp.byName[pkg]; p != nil {
		repository = p.Repository
	}
	return historyKey(repository, commit)
}
