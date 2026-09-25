// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import "github.com/yohimik/dispat/pkg/ccme"

// ---------------------------------------------------------------------------
// §13.6 direct bumps
// ---------------------------------------------------------------------------

// directBumps computes direct(P) = max over surviving tuples for P.
//
// The retention rule here is the *direct* one of §13.4: a tuple counts only
// while the commit is in the unit's own package's window. The propagation pass
// deliberately uses a different rule; see §13.7a for why a single rule serving
// both cannot be correct.
func (cp *computation) directBumps() {
	for _, rec := range cp.commits {
		for i, u := range rec.units {
			if u.IsCancel() { // cancel units are dropped after §13.5
				continue
			}
			// u.Bump is bumpOf(unit) from §13.6: the type mapping and "!"
			// alone. No footer overrides it — Release-As acts on the release,
			// not on the size of the change — and ccme has already applied
			// that rule.
			bump := u.Bump
			if bump == ccme.BumpNone {
				continue
			}
			// Map order is enough, and a sort here is paid per unit: every
			// effect below is on the one package it names, in the order the
			// commits and units are visited, and cancelledFor only sets a flag.
			for name := range rec.scope[i] {
				if !cp.inWindow(name, rec.key) {
					continue
				}
				if cp.cancelledFor(rec.key, name) {
					continue
				}
				rel := cp.rel[name]
				rel.Units = append(rel.Units, u)
				rel.OwnBump = ccme.MaxBump(rel.OwnBump, bump)
				cp.ownContribs[name] = append(cp.ownContribs[name], groupContrib{key: rec.key, bump: bump})
				if !cp.containedInBaseline(name, rec.key) {
					rel.NewWork = true
					rel.FreshUnits = append(rel.FreshUnits, u)
				}
			}
		}
	}
}
