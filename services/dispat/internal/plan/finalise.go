// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// ---------------------------------------------------------------------------
// §13.9 versions, §13.10 emit
// ---------------------------------------------------------------------------

func (cp *computation) finalise() {
	// Members of a shared-versioning space version as one group; their
	// per-package version computation is deferred to the group's.
	groups := cp.fixedGroups()
	shared := make(map[string]bool)
	for _, members := range groups {
		for _, m := range members {
			shared[m] = true
		}
	}

	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		rel.Bump = ccme.MaxBump(rel.OwnBump, rel.PropagatedBump)
		// A hold suspends a release a none package was never going to make;
		// leaving Held false keeps it out of the held counts and W154, so the
		// one exclusion the graph reports for it is its versioning.
		rel.Held = cp.held[name] && rel.IsReleasable()
		// §13.10: the plan marks its corrected and suppressed entries. Both
		// maps are keyed by unit, and rel.Units holds pointers into the same
		// parsed messages, so the marks travel with the units to every
		// consumer of the release.
		rel.Corrects = cp.corrects[name]
		rel.SuppressedNotes = cp.noteDrops[name]
		// The attribution, alongside the other two unit-keyed marks. The map is
		// shared rather than copied per package for the same reason the units
		// themselves are: it is read-only from here on, and a unit reaching two
		// packages is by the same people in both.
		rel.UnitAuthors = cp.unitAuthors
		rel.UnitCommits = cp.unitCommits
		rel.Channel = cp.channel[name]
		if rel.Channel == "" {
			rel.Channel = rel.BaselineChannel
		}
		rel.ChannelFrom = cp.channelFrom[name]

		if shared[name] {
			continue // versioned by its group below
		}
		cp.versionOne(name, rel)
	}

	groupNames := make([]string, 0, len(groups))
	for gn := range groups {
		groupNames = append(groupNames, gn)
	}
	sort.Strings(groupNames)
	for _, gn := range groupNames {
		cp.applyFixedGroup(gn, groups[gn])
	}
	// After every version, fixed rides included: whether a release has an
	// entry to attribute is decided by then.
	cp.collectReleaseAuthors()

	// Provider version movements, resolved after every version is final. The
	// topological order already guarantees a provider's Next is computed
	// before its consumers are visited, but a second pass keeps the guarantee
	// independent of the loop above ever changing shape.
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		rel.Updates = cp.providerUpdates(rel, name)
		rel.owedBoundaries = cp.collectOwedBoundaries(rel)
	}

	cp.reportCatchUp()
	cp.reportChannelOnly()
}

// newerCommit reports whether commit a is newer than commit b in the examined
// history ("" counts as infinitely old).
func (cp *computation) newerCommit(a, b string) bool {
	if b == "" {
		return a != ""
	}
	ra, oka := cp.byKey[a]
	rb, okb := cp.byKey[b]
	if !oka || !okb {
		return oka
	}
	return ra.rank < rb.rank // newest first: lower rank is newer
}

// logReleases traces what each package resolved to, once the plan is final.
//
// These are the intermediates a wrong plan is diagnosed from and the emitted
// plan does not otherwise carry: which tag became the baseline, how large the
// window was, and what the bump was. Trace rather than debug because it is one
// line per package of the workspace, releasing or not, and the packages that
// did *not* release are half of what a reader is usually checking.
func (cp *computation) logReleases() {
	if !cp.log.Trace().Enabled() {
		return
	}
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		ev := cp.log.Trace().
			Str("package", name).
			Str("baseline", rel.Baseline.String()).
			Bool("hasBaseline", rel.HasBaseline).
			// The whole pending window since the stable baseline — on a train
			// it spans commits earlier prereleases already shipped, so it is
			// not "commits this release adds"; the name says so.
			Int("windowSinceStable", cp.windowSize(name)).
			Str("bump", rel.Bump.String()).
			Str("channel", rel.Channel).
			Str("next", rel.Next.String()).
			Bool("releasing", rel.IsReleasing())
		if len(rel.DueTo) > 0 {
			ev = ev.Strs("dueTo", rel.DueTo)
		}
		ev.Msg("plan: package resolved")
	}
}

// reportCatchUp emits W193: a release whose entire cause is propagation from
// packages that are not themselves in this run's plan.
//
// The diagnostic MUST carry the origin's *published* version, so that a
// reviewer can see at a glance that the plan is discharging an earlier run's
// unfinished work rather than releasing something new (§13.10).
func (cp *computation) reportCatchUp() {
	inPlan := make(map[string]bool)
	for _, name := range cp.order {
		if rel := cp.rel[name]; rel != nil && rel.IsReleasing() {
			inPlan[name] = true
		}
	}

	for _, name := range cp.order {
		rel := cp.rel[name]
		// freshOwnBump, not OwnBump: own work the train already shipped keeps
		// deciding the target, but it does not explain why the package is
		// releasing again — a package whose only fresh cause is propagation
		// from an already-published provider is a catch-up whatever its train
		// history says.
		if rel == nil || !rel.IsReleasing() || rel.IsFreshOwnBump() {
			continue
		}
		if len(rel.Sources) == 0 {
			continue
		}
		// A releasing dependency explains the package's presence in the plan,
		// so it is an ordinary propagated release rather than a catch-up.
		explained := false
		for _, s := range rel.Sources {
			if inPlan[s.Provider] {
				explained = true
				break
			}
		}
		if explained {
			continue
		}
		rel.CatchUp = true

		origins := make([]string, 0, len(rel.DueTo))
		for _, provider := range rel.DueTo {
			published := "untagged"
			if pr := cp.rel[provider]; pr != nil && pr.HasBaseline {
				published = pr.Baseline.String()
			}
			origins = append(origins, provider+"@"+published)
		}
		cp.pkgWarn(rel, CodeCatchUp, "",
			fmt.Sprintf("catch-up release at %s: discharging work already published by %s",
				rel.Next.String(), strings.Join(origins, ", ")))
	}
}

// reportChannelOnly emits W202 for a package in the plan solely because its
// channel changed. It exists for the same reason W193 does: such a package
// appears with no commits of its own and no bump, and is otherwise
// unexplainable to whoever reviews the plan.
func (cp *computation) reportChannelOnly() {
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil || !rel.IsReleasing() {
			continue
		}
		// A pinned release is explained by its footer, not by its channel,
		// even when the pinned version happens to move it between lines; a
		// fixed-versioning ride is already explained by W234.
		//
		// Bump is deliberately train-wide here, unlike the catch-up scan's
		// freshOwnBump: a graduation publishes the train's whole window, so
		// any bump in it explains the release even when nothing is fresh.
		if rel.Bump != ccme.BumpNone || rel.Pinned || rel.FixedRide || !rel.IsChannelChanged() {
			continue
		}
		rel.ChannelOnly = true
		cp.pkgWarn(rel, CodeChannelOnly, "",
			fmt.Sprintf("channel-only release at %s: %s",
				rel.Next.String(), rel.ChannelTransition()))
	}
}

// reportHeld emits W154. The engine MUST compute the would-be version for
// every held package anyway and report it, so the value needed to lift the
// hold is available without hand computation (§13.6a). The bump is not lost:
// it is recomputed from the same tuples by whichever run lifts the hold.
func (cp *computation) reportHeld() {
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil || !rel.Held || !rel.IsChanged() {
			continue
		}
		cp.pkgWarn(rel, CodeHeldVersion, "",
			fmt.Sprintf("held by Release-As: none; would release %s", rel.Next.String()))
	}
}
