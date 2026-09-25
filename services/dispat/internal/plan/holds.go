// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// ---------------------------------------------------------------------------
// §13.6a holds
// ---------------------------------------------------------------------------

// resolveHolds resolves each package's effective `Release-As` directive.
//
// Holds are resolved *before* propagation, so that a held package cannot bump
// its dependents with work it has not released. Precedence (§8.6): the newest
// surviving directive in the package's own window wins; a directive discarded
// by cancellation carries no weight, which is how a `cancel` clears a hold.
func (cp *computation) resolveHolds() {
	type directiveRec struct {
		directive releaseAs
		commit    string
		packages  int // how many packages the directive's scope-set addressed
	}

	// Collect every directive still in force, newest commit first. Deciding
	// in a second pass is what lets W158 be accurate: whether an `auto` lifted
	// anything is a question about the *older* directives it outranks, which a
	// single newest-wins pass has already discarded by the time it asks.
	pending := make(map[string][]directiveRec)
	var names []string
	for _, rec := range cp.commits { // newest first
		for i, u := range rec.units {
			directive, ok := unitReleaseAs(u)
			if !ok {
				continue
			}
			// Map order is enough: names is sorted once below, and each
			// package's directives stay in the order the commits are visited.
			for name := range rec.scope[i] {
				if !cp.inWindow(name, rec.key) {
					continue // already released: no longer in force
				}
				if cp.containedInBaseline(name, rec.key) {
					// Released by a prerelease of the train: consumed exactly
					// as a stable release consumes a directive. Without this a
					// pin published as 2.0.0-rc.0 would raise E153 ("does not
					// move forward") on every later run of the train.
					continue
				}
				if cp.cancelledFor(rec.key, name) {
					continue // a cancel clears a hold by discarding its unit
				}
				if len(pending[name]) == 0 {
					names = append(names, name)
				}
				pending[name] = append(pending[name], directiveRec{
					directive: directive,
					commit:    rec.key,
					packages:  len(rec.scope[i]),
				})
			}
		}
	}
	sort.Strings(names)

	for _, name := range names {
		recs := pending[name]
		// Collapse each repository to its newest semantic candidate before
		// comparing histories. This keeps validation linear in the number of
		// directives plus participating repositories instead of all pairs.
		frontier := make([]directiveRec, 0, len(recs))
		repositoryIndex := make(map[string]int)
		for _, candidate := range recs {
			repository, _ := splitHistoryKey(candidate.commit)
			key := globx.Fold(repository)
			if index, exists := repositoryIndex[key]; exists {
				previous := frontier[index]
				if newer, comparable := cp.commitPrecedence(candidate.commit, previous.commit); comparable && newer {
					frontier[index] = candidate
				}
				continue
			}
			repositoryIndex[key] = len(frontier)
			frontier = append(frontier, candidate)
		}
		winner := frontier[0]
		values := make(map[string]bool, len(frontier))
		for _, candidate := range frontier {
			values[candidate.directive.raw] = true
			if newer, comparable := cp.commitPrecedence(candidate.commit, winner.commit); comparable && newer {
				winner = candidate
			}
		}
		if len(values) > 1 && len(frontier) > 1 {
			picks := make([]channelPick, 0, len(frontier))
			for _, candidate := range frontier {
				picks = append(picks, channelPick{channel: candidate.directive.raw, commit: candidate.commit})
			}
			resolved := false
			for _, candidate := range frontier {
				repository, _ := splitHistoryKey(candidate.commit)
				if strings.EqualFold(repository, cp.controlRepo) && cp.controlResolves(candidate.commit, picks) {
					winner, resolved = candidate, true
					break
				}
			}
			if !resolved {
				cp.err(CodeRepositoryPrecedence, name, "",
					"conflicting Release-As directives come from incomparable revisions"+cp.precedenceRemedy())
			}
		}
		if len(recs) > 1 {
			cp.warn(CodeReleaseAsConflict, name, winner.commit,
				fmt.Sprintf("%d Release-As directives are pending; the newest (%q) wins",
					len(recs), winner.directive.raw))
		}
		switch {
		case winner.directive.isHold():
			cp.held[name] = true
		case winner.directive.isAuto():
			lifted := false
			for _, older := range recs[1:] {
				if older.directive.isHold() {
					lifted = true
					break
				}
			}
			if !lifted {
				cp.warn(CodeAutoNoHold, name, winner.commit,
					"Release-As: auto with no hold in force; this is already the default behaviour")
			}
		case winner.directive.isExact():
			cp.pinned[name] = pin{
				version:  winner.directive.version,
				commit:   winner.commit,
				packages: winner.packages,
			}
		}
		// No default: a Release-As value that is none of the three never
		// reaches here. ccme rejects it at parse time (E151), which
		// invalidates the unit, and an invalid unit is not in ValidUnits.
	}
}
