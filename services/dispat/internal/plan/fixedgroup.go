package plan

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme/v2"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Versioning groups: the packages that hold some leading part of their version
// in common.
//
// A mode declares a shared *depth* (model.Versioning.SharedDepth): the number
// of leading version components the group keeps equal. At the full depth the
// group shares the whole version, which is the original `fixed` behaviour and
// means every movement of any member is the group's. Below it the group shares
// only a prefix — the major alone, or the major and minor — and the components
// under that prefix are each package's own, so a fix in one member of a
// `fixedMajor` group moves nothing else.
//
// One rule carries the difference: the group engages when the shared prefix
// moves, and versions its members individually when it does not. Assignment
// itself is the same either way, because ccme.Version.Bumped already zeroes
// everything below the component it bumps: a group major bump computes 2.0.0
// and a group minor bump 1.3.0, which is exactly the version every member
// takes.

// groupContrib is one direct contribution as the fixed-group aggregate needs
// it: the bump and the commit that carried it. directBumps records them beside
// OwnBump, which collapses the same tuples into a single maximum and loses the
// commits the aggregate's re-measuring needs.
type groupContrib struct {
	key  string
	bump ccme.Bump
}

// samePrefix reports whether two versions agree on their first d core
// components. Prerelease identifiers are deliberately ignored, so 2.0.0-beta.0
// and 2.1.0 share a major: the prefix is about which line a version belongs
// to, not about how far along that line it is.
func samePrefix(a, b ccme.Version, d int) bool {
	if a.Major != b.Major {
		return false
	}
	if d >= 2 && a.Minor != b.Minor {
		return false
	}
	return d < 3 || a.Patch == b.Patch
}

// groupTarget is the version a member adopts to join the group's shared
// prefix: the group's baseline with every component below the shared depth
// zeroed. A prerelease baseline ranks below its own core, so a member joining
// a group that is mid-train joins the train rather than jumping past it to a
// stable version the group has not published. At the full depth the answer is
// always the baseline itself.
func groupTarget(baseline ccme.Version, d int) ccme.Version {
	prefix := baseline.Core()
	switch d {
	case 1:
		prefix.Minor, prefix.Patch = 0, 0
	case 2:
		prefix.Patch = 0
	}
	if baseline.Compare(prefix) <= 0 {
		return baseline
	}
	return prefix
}

// SharedPartName spells the part of the version a group of the given shared
// depth holds in common. Diagnostics and release records render it from here
// rather than each writing their own phrase, so a reader is never told that a
// whole version is shared when only the major is.
func SharedPartName(depth int) string {
	switch depth {
	case 1:
		return "one major version"
	case 2:
		return "one major and minor version"
	default:
		return "one version"
	}
}

// fixedGroups maps each versioning group onto its member packages, in plan
// order: every package with shared versioning belongs to its resolved
// VersionGroup — a space's own name unless configuration joined it to a
// declared group, so groups may span spaces.
func (cp *computation) fixedGroups() map[string][]string {
	out := make(map[string][]string)
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		if group := rel.Pkg.VersionGroupName(); group != "" {
			out[group] = append(out[group], name)
		}
	}
	return out
}

// groupDepth is the shared depth the whole group versions at: the deepest any
// of its members declares. A mode is each member's own — a group joined from
// two spaces may mix them — and the deepest declaration satisfies all of them
// at once, since holding the major and minor equal also holds the major equal.
// Members that disagree are reported (W235-style: deterministic winner, one
// warning) rather than resolved silently, because nothing in the commit log
// explains the extra sharing.
func (cp *computation) groupDepth(g *Release, groupName string, members []string) int {
	depth, mixed := 0, false
	for _, name := range members {
		d := cp.rel[name].Pkg.Space.Versioning.SharedDepth()
		if depth != 0 && d != depth {
			mixed = true
		}
		if d > depth {
			depth = d
		}
	}
	if mixed {
		cp.warn(CodeFixedDepthConflict, g.Pkg.Name, "",
			fmt.Sprintf("members of versioning group %q share different parts of the version; the group holds %s in common, the deepest any member asks for",
				groupName, SharedPartName(depth)))
	}
	return depth
}

// groupMoves reports whether the group's shared prefix moves on this run,
// which is what decides between the two paths of applyFixedGroup.
//
// Sharing the whole version means every movement is the group's, so the full
// depth always moves. A partial mode moves when the computed version leaves
// the group's prefix behind, and stays moving while the group sits on a
// prerelease train whose prefix has already left the stable line: that train's
// later prereleases and its graduation belong to the whole group even though
// neither of them moves the prefix again.
func groupMoves(g *Release, d int) bool {
	if d >= model.SharedVersioningDepth {
		return true
	}
	if !samePrefix(g.Next, g.Baseline, d) {
		return true
	}
	return g.HasBaseline && !samePrefix(g.Baseline, g.Current, d)
}

// applyFixedGroup versions one versioning group and assigns the result to its
// members.
//
// When the shared prefix moves, the group is versioned as a single virtual
// package. The aggregate collects what the version computation reads: the
// baselines of every member (held ones included, so the shared version can
// never fall below a position a member has already published), and the bumps,
// new work and channel movements of the members that would release (a held
// member's withheld work moves nothing, exactly as it propagates nothing). It
// then goes through the ordinary §13.9 computation — pins, trains and the
// E15x/E19x guards included — so a versioning group runs one prerelease train
// and one Release-As applies to the group's shared version.
//
// Assignment is where sparseness shows, and it is each member's own: a plain
// mode releases every non-held member at the group version, marking members
// with no cause of their own as FixedRide (W234); a sparse mode assigns the
// group version only to members with a cause of their own and leaves the rest
// at their previous versions.
//
// When the shared prefix does not move — a patch under `fixedMajorMinor`, a
// minor under `fixedMajor`, or nothing pending at all — every member is
// versioned on its own, and alignFixedGroup afterwards is what keeps the
// prefix invariant true.
func (cp *computation) applyFixedGroup(groupName string, members []string) {
	if len(members) == 0 {
		return
	}
	g, channelCands := cp.fixedGroupAggregate(groupName, members)
	depth := cp.groupDepth(g, groupName, members)
	cp.reportMajorSpread(g, groupName, members)

	groupPin, hasPin := cp.fixedGroupPin(g, groupName, members, depth)
	if hasPin {
		cp.applyPin(g, groupPin)
	} else {
		cp.computeVersion(g)
	}
	for _, d := range g.Diagnostics {
		if d.Level == LevelError && IsRepositoryScoped(d.Code) {
			return // no correct plan exists; the run aborts, leave members untouched
		}
	}
	if cp.log.Trace().Enabled() {
		cp.log.Trace().Str("group", groupName).Strs("members", members).
			Int("depth", depth).Str("target", g.Next.String()).
			Bool("moves", g.Changed() && groupMoves(g, depth)).
			Bool("absorbed", g.absorbed).
			Msg("plan: fixed group unified")
	}

	// The aggregate can also fail to move under the full depth: transitional
	// states (heterogeneous member baselines) leave a member changed while the
	// aggregate is not — one member graduating while the max baseline is
	// already stable. Both cases take the per-member path.
	if !g.Changed() || !groupMoves(g, depth) {
		for _, name := range members {
			cp.versionOne(name, cp.rel[name])
		}
		cp.alignFixedGroup(groupName, g, members, depth)
		return
	}

	// Only now is a divergent channel a conflict. The group is about to take
	// every member onto one channel, so the members that asked for another one
	// have been overridden, which is what W236 reports. Had the group stayed
	// put, each member would have kept the channel it asked for and there
	// would have been nothing to report.
	if len(channelCands) > 1 {
		cp.warn(CodeFixedChannelConflict, g.Pkg.Name, "",
			fmt.Sprintf("members of versioning group %q resolve to different channels %v; the group moves as one, using %q",
				groupName, channelCands, g.Channel))
	}

	for _, name := range members {
		rel := cp.rel[name]
		if rel.Held {
			// W154 reports the version the hold withholds; in a versioning
			// group that is the group version the member will catch up to.
			rel.Next = g.Next
			continue
		}
		own := rel.Changed()
		if _, ok := cp.pinned[name]; ok {
			own = true // the member's pin has not been applied to it, only to the group
		}
		if rel.Pkg.Space.Versioning.Sparse() && !own {
			continue // sparse: an unchanged member keeps its previous version
		}
		if !own {
			rel.FixedRide = true
			cp.pkgWarn(rel, CodeFixedAlign, "", fmt.Sprintf(
				"released at %s with no changes of its own, to keep versioning group %q on %s",
				g.Next.String(), groupName, SharedPartName(depth)))
		}
		rel.Next = g.Next
		rel.Bump = g.Bump
		rel.NewWork = rel.NewWork || g.NewWork
		rel.Channel = g.Channel
		rel.Pinned = rel.Pinned || g.Pinned
	}
}

// reportMajorSpread warns (W233) when the group's members are published on
// different major versions.
//
// The group versions from its newest member, so whoever is furthest ahead
// decides where everybody lands. Across a patch or a minor that is ordinary:
// a failed ride leaves a laggard behind and the next run catches it up, which
// W234 already explains. Across a *major* it is almost always a mistake, and
// an expensive one: a package tagged 9.0.0 by hand next to a group on 1.x
// takes every member to 9.x, and §19.1 forbids moving the tags back.
//
// Nothing here is refused. Every one of those versions is legitimately
// published, so there is no correct plan that ignores them; what the group
// needs is for the outlier to be named rather than quietly obeyed.
//
// Sparseness cuts one way only. A sparse member behind the group's major is
// the mode doing its job and is never reported, but a sparse member *holding*
// the group's major decided everybody else's version, and a group can mix
// modes, so it is named like any other.
func (cp *computation) reportMajorSpread(g *Release, groupName string, members []string) {
	if !g.HasBaseline {
		return
	}
	ahead, behind := "", ""
	for _, name := range members {
		rel := cp.rel[name]
		// A member with no baseline has no major to disagree with.
		if !rel.HasBaseline {
			continue
		}
		if rel.Baseline.Major == g.Baseline.Major {
			// Whoever sits on the group's major is the one that decided where
			// everybody lands, sparse or not: the aggregate reads every
			// member's baseline, so a sparse member can be the outlier this
			// warning exists to name.
			if ahead == "" {
				ahead = name
			}
			continue
		}
		// Falling behind, on the other hand, is a sparse mode working as
		// promised, so a sparse member is never what the warning reports on.
		if behind == "" && !rel.Pkg.Space.Versioning.Sparse() {
			behind = name
		}
	}
	// A group with a baseline always has a member holding it, so ahead is set
	// whenever behind is. The guard is here because the member that decides
	// the aggregate and the member this loop is willing to name are chosen by
	// two separate pieces of code, and they have drifted apart before.
	if ahead == "" || behind == "" {
		return
	}
	cp.warn(CodeFixedMajorSpread, g.Pkg.Name, "", fmt.Sprintf(
		"members of versioning group %q are on different major versions: %s is at %s while %s is at %s; the group versions from the newest, so every member moves to major %d",
		groupName, ahead, cp.rel[ahead].Baseline.String(),
		behind, cp.rel[behind].Baseline.String(), g.Baseline.Major))
}

// fixedGroupAggregate builds the synthetic release the group computation runs
// on. Its Pkg exists so diagnostics raised against the group name the group
// rather than an arbitrary member; its Space is the first member's, read
// only for those group-level diagnostics — member tags always render
// against each member's own space, so a group spanning tag formats is safe.
// The aggregate collects every member's baseline (held ones included) and
// the bumps, new work and channel movements of the members that would
// release; members resolving to different channels are reduced to one
// deterministic winner, because the group can only move as one. The distinct
// channels are returned rather than reported here: whether the reduction
// overrode anybody depends on the group actually moving, which only the
// caller knows.
func (cp *computation) fixedGroupAggregate(groupName string, members []string) (*Release, []string) {
	first := cp.rel[members[0]]
	g := &Release{Pkg: &model.Package{Name: "group:" + groupName, Space: first.Pkg.Space}}
	for _, name := range members {
		rel := cp.rel[name]
		if rel.HasBaseline && (!g.HasBaseline || versionLess(g.Baseline, rel.Baseline)) {
			g.Baseline, g.HasBaseline = rel.Baseline, true
		}
		if versionLess(g.Current, rel.Current) {
			g.Current = rel.Current
		}
	}
	g.BaselineChannel = channelOf(g.Baseline, g.HasBaseline)
	g.Channel = g.BaselineChannel
	g.Next = g.Current
	if g.HasBaseline {
		g.Next = g.Baseline
	}

	// The commit the group's freshness is measured against: what the tag
	// holding the group baseline names. Work at or behind it has already been
	// versioned into the group's line — a run that died after one member
	// published left the others still carrying the very commits that version
	// contains — so counting it again would bump the shared prefix past a
	// version the group has already published for that exact work (a G3
	// violation the per-package computation cannot have). Confined to a
	// stable group baseline: a train's window deliberately spans work its own
	// prereleases shipped (§11.4), and re-measuring it here would fight that.
	mask := ""
	if g.HasBaseline && len(g.Baseline.Prerelease) == 0 {
		for _, name := range members {
			rel := cp.rel[name]
			if rel.HasBaseline && rel.Baseline.Compare(g.Baseline) == 0 && rel.BaselineCommit != "" {
				mask = rel.BaselineCommit
				break
			}
		}
	}

	var channelCands []string // distinct member channels departing from the group baseline
	seenChan := make(map[string]bool)
	for _, name := range members {
		rel := cp.rel[name]
		if rel.Held {
			continue
		}
		own, propagated, fresh := rel.OwnBump, rel.PropagatedBump, rel.NewWork
		if mask != "" {
			own, propagated, fresh = cp.groupFresh(name, mask)
			if own != rel.OwnBump || propagated != rel.PropagatedBump || fresh != rel.NewWork {
				g.absorbed = true
			}
		}
		g.OwnBump = ccme.MaxBump(g.OwnBump, own)
		g.PropagatedBump = ccme.MaxBump(g.PropagatedBump, propagated)
		if fresh {
			g.NewWork = true
		}
		if rel.Channel != "" && rel.Channel != g.BaselineChannel && !seenChan[rel.Channel] {
			seenChan[rel.Channel] = true
			channelCands = append(channelCands, rel.Channel)
		}
	}
	g.Bump = ccme.MaxBump(g.OwnBump, g.PropagatedBump)
	if len(channelCands) > 0 {
		g.Channel = channelCands[0]
	}
	return g, channelCands
}

// groupFresh re-measures one member's pending contributions against the
// group's published baseline commit rather than the member's own tag: the
// bumps of its direct tuples and stale propagation sources whose commits the
// mask does not contain, and whether anything at all survives. A member whose
// whole window sits at or behind the mask contributes nothing — its work is
// published, just not under its own tag yet — which is what lets the group
// stay put and the member catch up at the version that already carries its
// work, instead of the whole group burning the next prefix on a re-count.
func (cp *computation) groupFresh(name, mask string) (own, propagated ccme.Bump, fresh bool) {
	for _, c := range cp.ownContribs[name] {
		if cp.ancestorOrSelf(c.key, mask) {
			continue
		}
		own = ccme.MaxBump(own, c.bump)
	}
	for _, s := range cp.rel[name].Sources {
		if cp.ancestorOrSelf(s.Commit, mask) {
			continue
		}
		propagated = ccme.MaxBump(propagated, s.Bump)
	}
	return own, propagated, own != ccme.BumpNone || propagated != ccme.BumpNone
}

// fixedGroupPin selects the pin that applies to the group, if any: the newest
// exact pin naming a shared part the group is not already on. With the whole
// version shared that is every pin, because there is nothing narrower for one
// to name. Under a partial mode a pin that stays inside the group's prefix
// asks for nothing of the group's, so it is not a candidate at all: it goes
// through its own package's applyPin, with its own guards (E153, E154, E157)
// measured against that package rather than against the aggregate, and it
// competes with nobody.
//
// The winner's scope breadth is deliberately reset to one package — the group
// holds one shared prefix, so E154's "an exact version can name only one" is
// satisfied by construction here; packages outside the group that the same pin
// scoped still go through their own applyPin and its guards. Competing group
// pins warn (W235) and the newest wins.
func (cp *computation) fixedGroupPin(g *Release, groupName string, members []string, depth int) (pin, bool) {
	var groupPin pin
	pinnedVersions := make(map[string]bool)
	hasPin := false
	for _, name := range members {
		p, ok := cp.pinned[name]
		if !ok {
			continue
		}
		if depth < model.SharedVersioningDepth && samePrefix(p.version, g.Baseline, depth) {
			continue // the member's own business, not the group's
		}
		pinnedVersions[p.version.String()] = true
		if !hasPin || cp.newerCommit(p.commit, groupPin.commit) {
			groupPin = p
			hasPin = true
		}
	}
	if !hasPin {
		return pin{}, false
	}
	groupPin.packages = 1
	if len(pinnedVersions) > 1 {
		cp.warn(CodeFixedPinConflict, g.Pkg.Name, groupPin.commit,
			fmt.Sprintf("%d exact Release-As pins compete for versioning group %q; the newest (%s) wins",
				len(pinnedVersions), groupName, groupPin.version.String()))
	}
	return groupPin, true
}

// alignFixedGroup restores the shared-prefix invariant on the per-member path,
// where each member has just been versioned on its own.
//
// A member releasing below the group's prefix adopts it, which is how a sparse
// member's first change lands it on the shared part rather than on its own old
// line. A member with nothing pending whose baseline sits below the prefix is
// released at it — its ride failed in an earlier run, or the group formed with
// unequal versions — exactly as a W193 catch-up discharges an earlier run's
// unfinished propagation. Sparse members are exempt from that second case:
// staying behind until they change is the point of a sparse mode.
//
// Raising an already-computed version is confined to the partial modes, with
// one exception. Under the full depth this path is a rare transitional state
// rather than the ordinary one, and moving a member's computed version there
// would rewrite settled behaviour for a case the prefix rule does not need —
// except when the aggregate absorbed pending work the group baseline already
// published: then a member releasing that work computes its version from its
// own smaller baseline, may land below the version the group already holds,
// and the full sharing demands the raise.
func (cp *computation) alignFixedGroup(groupName string, g *Release, members []string, depth int) {
	if !g.HasBaseline {
		return // the group has never published: nothing to align to
	}
	target := groupTarget(g.Baseline, depth)
	channel := channelOf(target, true)
	for _, name := range members {
		rel := cp.rel[name]
		if rel.Held {
			continue
		}
		if rel.Releasing() {
			if (depth < model.SharedVersioningDepth || g.absorbed) && versionLess(rel.Next, target) {
				rel.Next, rel.Channel = target, channel
			}
			continue
		}
		if rel.Pkg.Space.Versioning.Sparse() {
			continue
		}
		if rel.HasBaseline && !versionLess(rel.Baseline, target) {
			continue // already at (or somehow past) the group's shared prefix
		}
		rel.FixedRide = true
		rel.Next = target
		rel.Channel = channel
		cp.pkgWarn(rel, CodeFixedAlign, "", fmt.Sprintf(
			"released at %s with no changes of its own, catching up to versioning group %q's published version",
			target.String(), groupName))
	}
}
