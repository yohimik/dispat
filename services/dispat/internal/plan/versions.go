// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// MaxMajorJump is the §14.1 default: an exact Release-As may raise the major
// version at most this far above the computed version.
const MaxMajorJump = 1

// versionOne applies the §13.9 computation to a single package: its pin when
// one is in force, the ordinary computation otherwise. The single call path
// is what keeps the independent loop and the fixed-group fallback agreeing
// about pin precedence.
func (cp *computation) versionOne(name string, rel *Release) {
	if !rel.IsReleasable() {
		// A none package carries no version: Next mirrors Current (both zero)
		// so nothing downstream reads a fabricated release, and a pin aimed
		// at it is inert rather than an error — the commit may legitimately
		// pin other packages of its scope.
		if _, ok := cp.pinned[name]; ok {
			cp.pkgWarn(rel, CodeNonePinned, "",
				"Release-As names a package with versioning \"none\"; the directive moves nothing")
		}
		rel.Next = rel.Current
		return
	}
	if p, ok := cp.pinned[name]; ok {
		cp.applyPin(rel, p)
		return
	}
	cp.computeVersion(rel)
}

// computeVersion implements §13.9 for a package with no exact Release-As.
func (cp *computation) computeVersion(rel *Release) {
	if !rel.IsChanged() {
		// Nothing to release. Next stays at the baseline so that reporting
		// shows the package's current position rather than a fabricated one.
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		return
	}

	if rel.Channel == ccme.ChannelStable {
		// Graduation and the ordinary stable release are the same
		// computation: applyBump over the stable baseline, no suffix (§11.5).
		next := cp.raisedToFloor(rel, rel.Current.Bumped(rel.Bump))
		isGraduating := rel.BaselineChannel != ccme.ChannelStable
		// The channel-entry patch, on the way out (§11.5). A train entered by
		// that patch has no bump in its window, so the computation above
		// returns the stable baseline itself, below the core the train was
		// published under: 2.0.0 for a train at 2.0.1-beta.0. The same one
		// patch that let it in lets it out, at the core it carried. Anything
		// the patch does not explain still fails below.
		if isGraduating && rel.Bump == ccme.BumpNone && versionLess(next, rel.Baseline.Core()) {
			patched := cp.raisedToFloor(rel, rel.Current.Bumped(ccme.BumpPatch))
			cp.pkgWarn(rel, CodeChannelEntryPatch, "",
				fmt.Sprintf("channel-entry patch applied: graduating to %s would go backwards from the baseline %s, so %s is released instead",
					next.String(), rel.Baseline.String(), patched.String()))
			next = patched
		}
		if isGraduating && versionLess(next, rel.Baseline.Core()) {
			// Reachable from hand-edited tags, and from a train an exact
			// Release-As raised above what the window computes (§11.5): the
			// pin's effect lives in the baseline tag, not in the window, so
			// the graduation must be pinned too.
			cp.pkgErr(rel, CodeGraduateNoIncrease,
				fmt.Sprintf("graduating to %s would go backwards from the %s baseline %s",
					next.String(), rel.BaselineChannel, rel.Baseline.String()))
			return
		}
		rel.Next = next
		cp.checkGreater(rel)
		return
	}

	target := cp.raisedToFloor(rel, rel.Current.Bumped(rel.Bump).Core())
	next, ok := prereleaseOnCore(target, rel.Baseline, rel.HasBaseline, rel.Channel)
	if !ok {
		cp.pkgErr(rel, CodeBadPrereleaseTag,
			fmt.Sprintf("baseline %s has no numeric prerelease counter, so the train cannot be continued (§11.3)",
				rel.Baseline.String()))
		return
	}

	// The channel-entry patch (§11.4). Entering a train from a clean stable
	// baseline computes a version SemVer ranks *below* the baseline: from
	// 1.2.0 with no bump, the target is 1.2.0 and the next is 1.2.0-beta.0.
	// One patch is the narrowest possible fix, and it is applied only for a
	// channel-only release, so it can never mask the genuine regression E195
	// exists to catch.
	if rel.Bump == ccme.BumpNone && rel.HasBaseline && !versionLess(rel.Baseline, next) {
		patched := cp.raisedToFloor(rel, rel.Current.Bumped(ccme.BumpPatch).Core())
		bumped, okPatch := prereleaseOnCore(patched, rel.Baseline, rel.HasBaseline, rel.Channel)
		if okPatch {
			cp.pkgWarn(rel, CodeChannelEntryPatch, "",
				fmt.Sprintf("channel-entry patch applied: %s would not have exceeded the baseline %s, so %s is released instead",
					next.String(), rel.Baseline.String(), bumped.String()))
			next = bumped
		}
	}

	rel.Next = next
	cp.checkGreater(rel)
}

// raisedToFloor lifts a computed core to the package's version floor, and
// says so at trace level, because a version the package's own window does not
// explain is exactly the kind of number a reader comes looking for.
//
// The zero floor every package outside a versioning group carries is below
// every version, so this is an identity for all of them.
func (cp *computation) raisedToFloor(rel *Release, computed ccme.Version) ccme.Version {
	if !versionLess(computed, rel.versionFloor) {
		return computed
	}
	if cp.log.Trace().Enabled() {
		cp.log.Trace().Str("package", rel.Pkg.Name).
			Str("computed", computed.String()).
			Str("floor", rel.versionFloor.String()).
			Msg("plan: member target raised to its versioning group's line")
	}
	return rel.versionFloor
}

// checkGreater enforces the §13.9 requirement that a computed version be
// strictly greater than the baseline by SemVer precedence.
func (cp *computation) checkGreater(rel *Release) {
	if !rel.HasBaseline {
		return
	}
	if versionLess(rel.Baseline, rel.Next) {
		return
	}
	cp.pkgErr(rel, CodeVersionNotGreater,
		fmt.Sprintf("computed version %s is not greater than the baseline %s",
			rel.Next.String(), rel.Baseline.String()))
}

// applyPin implements `Release-As: <ver>` and its guards (§8.6, §14.1).
//
// The pin replaces the computed version but never the computed bump: how large
// a change is, is a property of the change, and the type already declares it.
// E156 is what keeps that true — a breaking change cannot be shipped as a
// patch by writing a footer.
//
// A rejected pin has §16's unit-scoped blast radius: the offending directive
// contributes nothing, and everything else the window carries still applies.
// So each guard reports its error and then falls back to the ordinary
// computed version, exactly as if the pin had never been written — a sibling
// `feat` in the same commit still releases at its computed bump rather than
// being silently swallowed with the bad footer. (Whether the raised error
// stops the whole run is the caller's `commitErrors` policy, as with any
// other unit-scoped error.)
func (cp *computation) applyPin(rel *Release, p pin) {
	baseline := rel.Previous()
	computed := rel.Current.Bumped(rel.Bump)
	rejected := func() { cp.computeVersion(rel) }

	// E154's decidable cases — two explicit includes, or a term addressing the
	// whole workspace — are caught by the parser. This is the case that needs
	// the workspace: a glob or "." whose breadth is not visible in the text.
	if p.packages > 1 {
		cp.pkgErr(rel, CodePinMultiPackage,
			fmt.Sprintf("Release-As: %s applies to %d packages; an exact version can name only one",
				p.version.String(), p.packages))
		rejected()
		return
	}
	if !versionLess(baseline, p.version) {
		cp.pkgErr(rel, CodePinNotGreater,
			fmt.Sprintf("Release-As: %s does not move %s forward from %s",
				p.version.String(), rel.Pkg.Name, baseline.String()))
		rejected()
		return
	}
	// E156 is about how *large* a release is, so it is measured on the cores
	// alone. A prerelease ranks below its own core by SemVer precedence, and
	// comparing the versions whole would read "Release-As: 1.1.0-rc.0" against
	// a computed 1.1.0 as a downgrade, reject it, and fall back to shipping
	// the stable version the operator was asking to hold back. The core is
	// what carries the bump: 1.1.0-rc.0 is on its way to 1.1.0 and satisfies
	// the minor the commits require, while an rc of 1.1.0 under a computed
	// 2.0.0 still fails, which is the case the guard exists for. E153 above
	// keeps comparing whole versions, because "does this move forward" is
	// exactly the question precedence answers.
	if rel.Bump != ccme.BumpNone && versionLess(p.version.Core(), computed.Core()) {
		cp.pkgErr(rel, CodePinBelowBump,
			fmt.Sprintf("Release-As: %s is below %s, which the pending commits require",
				p.version.String(), computed.String()))
		rejected()
		return
	}
	// E157 is a default, not an opt-in: a typo'd major in a footer is a
	// mistake nothing downstream can undo, since §19.1 forbids moving a tag.
	if jump := int64(p.version.Major) - int64(computed.Major); jump > MaxMajorJump {
		cp.pkgErr(rel, CodePinMajorJump,
			fmt.Sprintf("Release-As: %s raises the major version %d above the computed %s, more than the limit of %d",
				p.version.String(), jump, computed.String(), MaxMajorJump))
		rejected()
		return
	}

	rel.Pinned = true
	rel.Next = p.version
	// A pin states the version, not the channel: the channel it lands on is
	// whatever the version itself says (§11.1), so that a pinned
	// "1.3.0-rc.0" enters the rc line and a pinned "1.3.0" graduates.
	rel.Channel = channelOf(p.version, true)
}

// providerUpdates resolves one release's Updates: every provider whose version
// the package picks up, in DueTo order first and then the remaining configured
// providers that are releasing, so a reader meets the propagated ones where
// they always were.
//
// cp.providers is indexed by edge, not by provider, so one pair declared under
// two dependency kinds appears twice; the seen set is what keeps it one update
// (narrow.go's waitingOn compensates the same way for the same reason).
func (cp *computation) providerUpdates(rel *Release, name string) []ProviderUpdate {
	provs := cp.providers[name]
	direct := make(map[string]bool, len(provs))
	for _, prov := range provs {
		direct[prov] = true
	}
	out := make([]ProviderUpdate, 0, len(rel.DueTo)+len(provs))
	seen := make(map[string]bool, len(rel.DueTo)+len(provs))
	add := func(prov string) {
		if seen[prov] {
			return
		}
		pr := cp.rel[prov]
		if pr == nil {
			return
		}
		seen[prov] = true
		// Where the provider ends the run. A provider this run publishes ends
		// it at its next version; one it does not ends it exactly where it
		// started, whatever version it has computed and is withholding. A held
		// package's Next is reported so an operator can see what lifting the
		// hold would release (W154), and it is the one number no tag will ever
		// carry, so a dependency line, a release body or a version script that
		// picked it up would point at a version that does not exist. Native
		// auto-versioning already writes the published one (providerVersion);
		// one release must not have two answers.
		to := pr.Next
		if !pr.IsReleasing() {
			to = pr.Previous()
		}
		u := ProviderUpdate{Name: prov, From: pr.Previous(), To: to}
		if u.From.Compare(u.To) == 0 {
			// The catch-up shape: the provider published in an earlier run,
			// so its own before-and-after have already collapsed onto the
			// version this release picks up, and "1.3.0 -> 1.3.0" tells the
			// reader nothing moved when everything did. What the record
			// means by From is what this package last shipped against — the
			// provider's version as of this package's own baseline tag,
			// reconstructed from tags exactly as a graduation's span is.
			if from, ok := cp.versionForConsumerAt(prov, name, false); ok {
				u.From = from
			}
		}
		out = append(out, u)
	}
	for _, prov := range rel.DueTo {
		// A blast origin hops away answers "why is this package releasing";
		// the dependencies section speaks the package's own manifest
		// language instead. The movement arrives through a direct provider,
		// which the releasing half below names, so an indirect origin here
		// would put a package the manifests never mention into the record.
		if !direct[prov] {
			continue
		}
		add(prov)
	}
	for _, prov := range provs {
		// A provider that is not releasing has published nothing new for this
		// run to pick up. It still reaches Updates through DueTo when an
		// earlier run published it and this one is the catch-up (§13.7a).
		if pr := cp.rel[prov]; pr != nil && pr.IsReleasing() {
			add(prov)
		}
	}
	// A ride's whole cause is the group moving for somebody else's work, and
	// when that work published in an earlier run its provider is not
	// releasing here: the loops above find nothing, and the ride's entry
	// would stay silent about the movement it exists to ship. The same ride
	// in a single-run release documents the provider's movement — the
	// provider releases beside it — so the catch-up reconstructs the same
	// span off the tags, from what this package's previous release shipped
	// against.
	if rel.FixedRide {
		for _, prov := range provs {
			pr := cp.rel[prov]
			if pr == nil || seen[prov] {
				continue
			}
			from, ok := cp.versionForConsumerAt(prov, name, false)
			if !ok || from.Compare(pr.Previous()) == 0 {
				continue
			}
			seen[prov] = true
			out = append(out, ProviderUpdate{Name: prov, From: from, To: pr.Previous()})
		}
	}
	// A graduation's entry is what readers of the stable line actually see,
	// so its dependencies section spans the same window as its notes:
	// everything since the last stable release. A provider that moved during
	// the train was documented piecewise by the prerelease entries, which
	// those readers skip — without this widening the movement would reach no
	// stable entry at all. From is reconstructed off the provider's tags
	// (versionAt), because the planner keeps no state between runs, and it
	// also rewrites the From of the entries added above: their fresh movement
	// is a tail of the train-long one the graduation reports.
	if rel.HasBaseline && rel.Baseline.IsPrerelease() && !rel.Next.IsPrerelease() {
		for i := range out {
			if from, ok := cp.versionForConsumerAt(out[i].Name, name, true); ok {
				out[i].From = from
			}
		}
		for _, prov := range provs {
			pr := cp.rel[prov]
			if pr == nil || seen[prov] {
				continue
			}
			from, ok := cp.versionForConsumerAt(prov, name, true)
			if !ok || from.String() == pr.Previous().String() {
				continue
			}
			seen[prov] = true
			out = append(out, ProviderUpdate{Name: prov, From: from, To: pr.Previous()})
		}
	}
	// The tags come last, over the finished list, rather than at each of the
	// three places an update is appended: the graduation widening above
	// rewrites the From of entries the earlier loops added, and a tag computed
	// alongside an append would have to be kept in step with every future
	// rewrite. Rendering here is the one pass that cannot miss one.
	for i := range out {
		out[i].Tag = TagFormatFor(cp.byName[out[i].Name]).Render(out[i].Name, out[i].To)
	}
	return out
}

// versionAt is the package's newest published version as of the given commit:
// the newest parsed tag pointing at an ancestor of it. False when the package
// carried no tag there, or when the commit is unknown (a package never
// released on the stable line has no stable commit to ask about).
func (cp *computation) versionAt(pkg, commit string) (ccme.Version, bool) {
	if commit == "" {
		return ccme.Version{}, false
	}
	for _, t := range cp.tags[pkg] {
		if !t.Parsed || t.Commit == "" {
			continue
		}
		key := t.Commit
		if len(cp.histories) > 0 {
			key = historyKey(cp.byName[pkg].Repository, t.Commit)
		}
		if cp.ancestorOrSelf(key, commit) {
			return t.Version, true
		}
	}
	return ccme.Version{}, false
}

// versionForConsumerAt reconstructs the provider version contained in one of
// the consumer's published boundaries. In a composed workspace the relevant
// commit belongs to the provider repository rather than to the consumer tag's
// repository; repositoryBoundary resolved that correspondence once while the
// windows were loaded.
func (cp *computation) versionForConsumerAt(provider, consumer string, stable bool) (ccme.Version, bool) {
	if len(cp.histories) == 0 {
		rel := cp.rel[consumer]
		if rel == nil {
			return ccme.Version{}, false
		}
		commit := rel.BaselineCommit
		if stable {
			commit = rel.StableCommit
		}
		return cp.versionAt(provider, commit)
	}
	p := cp.byName[provider]
	if p == nil {
		return ccme.Version{}, false
	}
	boundaries := cp.publishedBoundaries
	if stable {
		boundaries = cp.stableBoundaries
	}
	return cp.versionAt(provider, boundaries[consumer][globx.Fold(p.Repository)])
}
