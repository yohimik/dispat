// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

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
	// One history only: a control repository's barrier also reaches the source
	// commits its snapshot observed, which no ancestor set of its own says.
	if rec := cp.byKey[key]; rec != nil && len(cp.histories) == 0 && cp.anc != nil &&
		cp.parentsAreAncestry(rec.repository) {
		if set := cp.anc.ancestors(int32(rec.rank)); set != nil {
			return func(commitKey string) bool {
				c := cp.byKey[commitKey]
				return c != nil && set.has(c.rank)
			}
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
	if cp.containedInBaseline(pkg, commitKey) {
		return false
	}
	for _, c := range cp.cancels {
		if !c.scope[pkg] {
			continue
		}
		if c.closure(commitKey) {
			c.discarded = true
			return true
		}
	}
	return false
}

// cancelledForOwed is cancelledFor for a contribution the package released
// past before its source delivered it (§13.4a). The package's own release,
// prerelease included, published the commit and not the source's version, so
// nothing about it is beyond a cancel's reach: the pending contribution lives
// in the consumer's ledger, and a later `cancel(<consumer>)` discards it
// (§13.5a, §13.7d).
func (cp *computation) cancelledForOwed(commitKey, pkg string) bool {
	for _, cancellation := range cp.cancels {
		if cancellation.scope[pkg] && cancellation.closure(commitKey) {
			cancellation.discarded = true
			return true
		}
	}
	return false
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
