// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The shape half of the minimal topology: where the links that join the
// fleet's groups are attached.
//
// Every completion of the fleet's forest adds the same number of links, one
// fewer than the number of groups the existing links leave it in, so the count
// cannot choose between two proposals. The longest route does. Section 27.9 of
// the specification charges link evidence by that route, because a boundary is
// resolved hop by hop, and settles a package with one commit in every
// repository that has a next hop on it. A fleet joined end to end therefore
// pays for every hop its shape has: two chains of five repositories joined at
// their ends leave a longest route of 9, and joined at their centres leave 5.
//
// So the groups are joined at their centres, as section 27.11 states. A
// group's centre is the repository whose farthest group member is nearest, and
// its radius is how far that is. The group with the greatest radius is the
// hub, and the centre of every other group links to the hub's centre. With
// radii r1 >= r2 >= r3 and d the longest route inside any group, the result
// has a longest route of max(d, r1 + r2 + 1, r2 + r3 + 2), which no proposal
// keeping the existing links can beat. Ties, both between the two centres a
// group can have and between groups of equal radius, go to the identity that
// sorts first under case folding, so one fleet is computed the same way twice.

import (
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// centredGroup is one group of the fleet as its existing links leave it: the
// members those links join, and how far the centre's farthest member is.
type centredGroup struct {
	// members are ordered by their distance from the centre and then by
	// folded identity, so the first of them is the centre and the first
	// composed one is the nearest end a link can be written in when the
	// centre itself cannot hold one.
	members []rosterEntry
	radius  int
}

// centre is the member every other group's link is proposed against: the
// repository whose farthest group member is nearest, which is what makes the
// joined fleet's longest route as short as the existing links allow.
func (g centredGroup) centre() rosterEntry {
	return g.members[0]
}

// nearestComposedMember answers the member closest to the centre that this run
// actually walked into, and reports whether the group has one at all. A link
// is written inside a checkout, so a group none of whose members were composed
// cannot hold one.
func (g centredGroup) nearestComposedMember() (rosterEntry, bool) {
	for _, member := range g.members {
		if member.composed {
			return member, true
		}
	}
	return rosterEntry{}, false
}

// fleetLinkAdjacency maps every fleet identity, folded, to the identities its
// existing links join it to, in folded order.
//
// Both ends of a link may declare it, so each pair is recorded once from
// whichever end named it first, and the order is fixed so that a walk over it
// answers the same member whatever order the fleet was composed in. A link
// naming a repository the roster does not hold, which is what a disabled
// participant leaves behind, joins nothing: this is the one reading of the
// fleet's shape, and what it leaves out has no route through it either.
func fleetLinkAdjacency(fleet []rosterEntry, repositories []config.Repository) map[string][]string {
	isMember := make(map[string]bool, len(fleet))
	for _, entry := range fleet {
		isMember[globx.Fold(entry.name)] = true
	}
	composed := make(map[string]*config.Repository, len(repositories))
	for i := range repositories {
		composed[globx.Fold(repositories[i].Name)] = &repositories[i]
	}
	adjacency := make(map[string][]string, len(fleet))
	recorded := make(map[string]bool)
	for _, entry := range fleet {
		linker := globx.Fold(entry.name)
		// A fleet member this run never walked into declares nothing, and
		// LinkPeers answers nothing for it.
		for _, name := range composed[linker].LinkPeers() {
			peer := globx.Fold(name)
			if peer == linker || !isMember[peer] || recorded[linker+"\x00"+peer] {
				continue
			}
			recorded[linker+"\x00"+peer], recorded[peer+"\x00"+linker] = true, true
			adjacency[linker] = append(adjacency[linker], peer)
			adjacency[peer] = append(adjacency[peer], linker)
		}
	}
	for _, peers := range adjacency {
		sort.Strings(peers)
	}
	return adjacency
}

// splitFleetIntoGroups partitions the fleet into the groups its existing links
// leave it in, each one already centred, in the folded order of its centre.
func splitFleetIntoGroups(fleet []rosterEntry, adjacency map[string][]string) []centredGroup {
	byFold := make(map[string]rosterEntry, len(fleet))
	for _, entry := range fleet {
		byFold[globx.Fold(entry.name)] = entry
	}
	isVisited := make(map[string]bool, len(fleet))
	var groups []centredGroup
	for _, entry := range fleet {
		start := globx.Fold(entry.name)
		if isVisited[start] {
			continue
		}
		for member := range walkGroupFrom(start, adjacency) {
			isVisited[member] = true
		}
		groups = append(groups, resolveCentredGroup(start, byFold, adjacency))
	}
	sort.Slice(groups, func(i, j int) bool {
		return globx.Fold(groups[i].centre().name) < globx.Fold(groups[j].centre().name)
	})
	return groups
}

// resolveCentredGroup answers the group one member belongs to, centred and
// ordered outward from that centre, which is the order a fallback end is
// picked in.
func resolveCentredGroup(member string, byFold map[string]rosterEntry, adjacency map[string][]string) centredGroup {
	centre, radius := chooseGroupCentre(member, adjacency)
	distances := walkGroupFrom(centre, adjacency)
	ordered := make([]string, 0, len(distances))
	for name := range distances {
		ordered = append(ordered, name)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if distances[ordered[i]] != distances[ordered[j]] {
			return distances[ordered[i]] < distances[ordered[j]]
		}
		return ordered[i] < ordered[j]
	})
	group := centredGroup{members: make([]rosterEntry, 0, len(ordered)), radius: radius}
	for _, name := range ordered {
		group.members = append(group.members, byFold[name])
	}
	return group
}

// chooseGroupCentre answers the folded identity of one group's centre and its
// radius, from any member of it.
//
// The existing links form a forest, because composition refuses a second route
// between two repositories with E338 and compute refuses to run on one. A
// group is therefore a tree, where every member's farthest member is an end of
// a longest route, so three walks are the whole computation: one finds an end,
// one measures from that end and finds the other, one measures from the other,
// and each member's farthest member is the greater of its two distances. The
// least of those is the radius and the member holding it is the centre. A
// route of an odd number of links leaves two such members, and the identity
// that sorts first under case folding is taken.
func chooseGroupCentre(member string, adjacency map[string][]string) (string, int) {
	oneEnd, _ := farthestMember(walkGroupFrom(member, adjacency))
	fromOneEnd := walkGroupFrom(oneEnd, adjacency)
	otherEnd, diameter := farthestMember(fromOneEnd)
	fromOtherEnd := walkGroupFrom(otherEnd, adjacency)
	centre, radius := "", diameter+1
	for name, distance := range fromOneEnd {
		reach := max(distance, fromOtherEnd[name])
		if reach < radius || (reach == radius && name < centre) {
			centre, radius = name, reach
		}
	}
	return centre, radius
}

// walkGroupFrom walks the existing links outward from one member and answers
// how many links away every member of its group is.
func walkGroupFrom(start string, adjacency map[string][]string) map[string]int {
	distances := map[string]int{start: 0}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, peer := range adjacency[current] {
			if _, isReached := distances[peer]; isReached {
				continue
			}
			distances[peer] = distances[current] + 1
			queue = append(queue, peer)
		}
	}
	return distances
}

// farthestMember answers the member a walk reached last and how far that is.
// Equal distances go to the identity that sorts first under case folding, so
// the answer does not depend on the order a map happened to be read in.
func farthestMember(distances map[string]int) (string, int) {
	farthest, reach := "", -1
	for member, distance := range distances {
		if distance > reach || (distance == reach && member < farthest) {
			farthest, reach = member, distance
		}
	}
	return farthest, reach
}

// chooseFleetHub answers the group whose centre every other group's centre
// links to: the one with the greatest radius, because the joined fleet's
// longest route is then max(d, r1 + r2 + 1, r2 + r3 + 2) and hanging the
// widest group off another one would add its radius to a route instead.
func chooseFleetHub(groups []centredGroup) centredGroup {
	hub := groups[0]
	for _, group := range groups[1:] {
		if isHubPreferred(group, hub) {
			hub = group
		}
	}
	return hub
}

// isHubPreferred reports whether one group should hold the fleet's hub rather
// than the group currently holding it. Equal radii go to the identity that
// sorts first under case folding, so two computations over one fleet propose
// the same links.
func isHubPreferred(candidate, current centredGroup) bool {
	if candidate.radius != current.radius {
		return candidate.radius > current.radius
	}
	return globx.Fold(candidate.centre().name) < globx.Fold(current.centre().name)
}

// chooseGroupJoinEnds answers the two repositories the link joining one group
// to the hub is written between, and reports whether such a link can be
// written at all.
//
// The two centres are the ends that give the shortest longest route. A link is
// a checkout, though, so at least one end has to be a repository this run
// walked into: where neither centre is, the member of the hub nearest its
// centre stands in for it, and the longest-route optimum is then not
// guaranteed, because the route runs through that member's own distance from
// the centre as well. Where the hub has no composed member either, nothing
// here can be written and the caller falls back to whatever pair it can reach.
func chooseGroupJoinEnds(group, hub centredGroup) (rosterEntry, rosterEntry, bool) {
	if group.centre().composed || hub.centre().composed {
		return group.centre(), hub.centre(), true
	}
	if nearest, isComposed := hub.nearestComposedMember(); isComposed {
		return group.centre(), nearest, true
	}
	return rosterEntry{}, rosterEntry{}, false
}

// chooseLinkOwner answers which end of a proposed link writes it and which end
// it names. The link is created inside a checkout this run holds, so a
// composed end owns it; where both ends are composed the identity that sorts
// first under case folding owns it, so one fleet is computed the same way
// twice.
func chooseLinkOwner(first, second rosterEntry) (rosterEntry, rosterEntry) {
	if globx.Fold(second.name) < globx.Fold(first.name) {
		first, second = second, first
	}
	if first.composed {
		return first, second
	}
	return second, first
}
