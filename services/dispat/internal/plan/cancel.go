// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import "math/bits"

// ---------------------------------------------------------------------------
// §13.5 cancellation
// ---------------------------------------------------------------------------

func (cp *computation) collectCancels() {
	for _, rec := range cp.commits {
		for i, u := range rec.units {
			if !u.IsCancel() {
				continue
			}
			c := &cancelRec{
				key:      rec.key,
				scope:    rec.scope[i],
				closure:  cp.ancestorClosure(rec.key),
				pkgLabel: joinSorted(rec.scope[i]),
			}
			cp.cancels = append(cp.cancels, c)
		}
	}
	cp.cancelIndex = cp.newCancelIndex()
}

// cancelIndex answers which cancel, first in cp.cancels order, reaches a
// commit for a package with two lookups and a word of bits per 64 cancels,
// where asking each cancel costs a scope lookup and a closure call apiece
// (CCME §13.11, cancellation predicates). The cancel set and its scopes are
// settled before collectCancels and never change after it (applyCorrections),
// so the index is built once.
type cancelIndex struct {
	words int
	// naming is, per package, the cancels whose scope holds it.
	naming map[string][]uint64
	// reached is, per rank, the cancels whose barrier the commit is an
	// ancestor-or-self of, words to a commit.
	reached []uint64
}

// newCancelIndex indexes the cancels when every barrier's closure is its
// ancestor set in the marker index, and answers nil otherwise: a barrier asked
// a commit at a time is asked as it always was.
func (cp *computation) newCancelIndex() *cancelIndex {
	if len(cp.cancels) == 0 {
		return nil
	}
	sets := make([]*commitSet, len(cp.cancels))
	for j, c := range cp.cancels {
		if sets[j] = cp.barrierAncestors(c.key); sets[j] == nil {
			return nil
		}
	}
	index := &cancelIndex{words: (len(cp.cancels) + 63) / 64, naming: make(map[string][]uint64)}
	for j, c := range cp.cancels {
		for pkg, isNamed := range c.scope {
			if !isNamed {
				continue
			}
			if index.naming[pkg] == nil {
				index.naming[pkg] = make([]uint64, index.words)
			}
			index.naming[pkg][j>>6] |= 1 << (uint(j) & 63)
		}
	}
	index.reached = transposeSets(sets, len(cp.commits))
	return index
}

// transposeSets is the sets' rows: for each of n ranks, the sets holding it,
// a bit per set and ceil(len(sets)/64) words to a rank, transposed 64 by 64.
func transposeSets(sets []*commitSet, n int) []uint64 {
	words := (len(sets) + 63) / 64
	rows := make([]uint64, n*words)
	var block [64]uint64
	for w := 0; w < words; w++ {
		group := sets[w<<6 : min(len(sets), w<<6+64)]
		for start := 0; start < n; start += 64 {
			isSet := false
			for i := range block {
				block[i] = 0
				if i < len(group) {
					block[i] = group[i].word(start >> 6)
				}
				isSet = isSet || block[i] != 0
			}
			if !isSet {
				continue
			}
			transposeBlock(&block)
			for i, row := range block[:min(64, n-start)] {
				rows[(start+i)*words+w] = row
			}
		}
	}
	return rows
}

// firstCancel is the first cancel, in cp.cancels order, whose scope holds
// pkg and whose barrier the commit is an ancestor-or-self of, or nil.
func (cp *computation) firstCancel(commitKey, pkg string) *cancelRec {
	index := cp.cancelIndex
	if index == nil {
		for _, c := range cp.cancels {
			if c.scope[pkg] && c.closure(commitKey) {
				return c
			}
		}
		return nil
	}
	rec, naming := cp.byKey[commitKey], index.naming[pkg]
	if rec == nil || naming == nil {
		return nil
	}
	for w, reached := range index.reached[rec.rank*index.words : (rec.rank+1)*index.words] {
		if hit := reached & naming[w]; hit != 0 {
			return cp.cancels[w<<6+bits.TrailingZeros64(hit)]
		}
	}
	return nil
}

// ancestorClosure is the set of examined commits that are ancestors-or-self of
// key. Commits outside every pending window are irrelevant by construction:
// cancellation only ever reaches unreleased work and never a published tag
// (§10.3).
//
// With the marker pass the closure is the barrier's ancestor set itself, a bit
// per commit and already computed. Without it, it is asked a commit at a time
// and kept as the keys that answered yes.
func (cp *computation) ancestorClosure(key string) func(commitKey string) bool {
	if closure, ok := cp.closures[key]; ok {
		return closure
	}
	closure := cp.newAncestorClosure(key)
	if cp.closures == nil {
		cp.closures = make(map[string]func(string) bool)
	}
	cp.closures[key] = closure
	return closure
}

// newAncestorClosure computes one barrier's closure, from the marker pass
// where it answers and a commit at a time where it does not.
func (cp *computation) newAncestorClosure(key string) func(commitKey string) bool {
	if set := cp.barrierAncestors(key); set != nil {
		return func(commitKey string) bool {
			c := cp.byKey[commitKey]
			return c != nil && set.has(c.rank)
		}
	}
	keys := make(map[string]bool)
	for _, rec := range cp.commits {
		if cp.ancestorOrSelf(rec.key, key) {
			keys[rec.key] = true
		}
	}
	return func(commitKey string) bool { return keys[commitKey] }
}

// barrierAncestors is a barrier's ancestor set in the marker index, or nil
// where the index does not answer for it. One history only: a control
// repository's barrier also reaches the source commits its snapshot observed,
// which no ancestor set of its own says.
func (cp *computation) barrierAncestors(key string) *commitSet {
	rec := cp.byKey[key]
	if rec == nil || len(cp.histories) > 0 || cp.anc == nil || !cp.parentsAreAncestry(rec.repository) {
		return nil
	}
	return cp.anc.ancestors(int32(rec.rank))
}

// cancelledFor is cancelledFor(C, X) from §13.4a: C is an ancestor-or-self of
// some `cancel` commit whose resolved scope contains X.
//
// Work the package's baseline tag already contains is beyond cancellation's
// reach: §10.3 says cancellation never retracts a published tag, and a
// prerelease tag is a published tag. Without the guard, a cancel landing after
// beta.0 would discard the train's published units, shrink the effective bump
// below the baseline's core, and abort the run with E195 — for work that is
// already public and cannot be unshipped.
func (cp *computation) cancelledFor(commitKey, pkg string) bool {
	if len(cp.cancels) == 0 {
		// The common case, asked once per incidence and once per propagation
		// target: no cancel, so no containment question worth asking.
		return false
	}
	c := cp.firstCancel(commitKey, pkg)
	if c == nil || cp.containedInBaseline(pkg, commitKey) {
		return false
	}
	c.discarded = true
	return true
}

// cancelledForOwed is cancelledFor for a contribution the package released
// past before its source delivered it (§13.4a). The package's own release,
// prerelease included, published the commit and not the source's version, so
// nothing about it is beyond a cancel's reach: the pending contribution lives
// in the consumer's ledger, and a later `cancel(<consumer>)` discards it
// (§13.5a, §13.7d).
func (cp *computation) cancelledForOwed(commitKey, pkg string) bool {
	c := cp.firstCancel(commitKey, pkg)
	if c == nil {
		return false
	}
	c.discarded = true
	return true
}

// reportCancels emits W170 for a `cancel` that discarded nothing. §13.7d calls
// this out as the signal that the directive addressed the wrong package: a
// cancel aimed at a provider that has already published cannot retract what
// its consumers are owed, and the right target is the consumer.
//
// The warning is for a *live* cancel only. A cancel whose own commit has been
// discharged for every package it names belongs to history: whatever it had
// to discard, it discarded in an earlier run's window, and that discard is
// invisible now — the discarded units left the window together with the
// cancel. Warning "discarded nothing" about it on every later run (it stays
// in the union window as long as any other package's window spans it) would
// misreport a spent directive as a misaimed one.
func (cp *computation) reportCancels() {
	for _, c := range cp.cancels {
		if c.discarded || cp.cancelSpent(c) {
			continue
		}
		cp.warn(CodeEmptyCancel, c.pkgLabel, c.key,
			"cancel discarded nothing; already released work cannot be retracted (§10.3); to stop a pending catch-up, cancel the consumer (§13.7d)")
	}
}

// cancelSpent reports whether the cancel's own commit is discharged for every
// package its scope names: each has either released past it (the commit left
// its window) or published it inside a prerelease train (contained in the
// baseline). An inert cancel — one whose scope resolved to no package at all —
// is spent trivially; W131 already reports the inert unit.
func (cp *computation) cancelSpent(c *cancelRec) bool {
	for pkg := range c.scope {
		if cp.inWindow(pkg, c.key) && !cp.containedInBaseline(pkg, c.key) {
			return false
		}
	}
	return true
}
