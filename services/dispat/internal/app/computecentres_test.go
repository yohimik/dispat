// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// What the minimal topology chooses, held to the number that makes the choice
// worth making: the longest route between two repositories of the joined
// fleet, which section 27.9 of the specification charges link evidence and
// settlement by.

import (
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// linkedFleet is a fleet this run composed every member of, joined by the
// links given, both ends of each declaring it.
func linkedFleet(t *testing.T, names []string, links [][2]string) *App {
	t.Helper()
	peers := make([]config.Repository, 0, len(names))
	for _, name := range names {
		others := make([]string, 0, len(names)-1)
		for _, other := range names {
			if other != name {
				others = append(others, other)
			}
		}
		peers = append(peers, fleetPeer(name, others, heldLinksOf(name, links)))
	}
	return computeFleet(t, peers...)
}

// heldLinksOf is the half of every link one repository declares, which is
// where composition reads a fleet's shape from.
func heldLinksOf(name string, links [][2]string) map[string]string {
	held := map[string]string{}
	for _, link := range links {
		if link[0] == name {
			held[link[1]] = ".links/" + link[1]
		}
		if link[1] == name {
			held[link[0]] = ".links/" + link[0]
		}
	}
	if len(held) == 0 {
		return nil
	}
	return held
}

// chainLinks joins the named repositories one after another, which is the
// shape whose centre is farthest from its ends and the one a fleet grows into
// when every new peer is linked to the last one added.
func chainLinks(names ...string) [][2]string {
	links := make([][2]string, 0, len(names)-1)
	for i := 0; i+1 < len(names); i++ {
		links = append(links, [2]string{names[i], names[i+1]})
	}
	return links
}

// proposedPairs is the links a computation proposed, as ordered pairs of
// owner and peer, which is what the listing prints.
func proposedPairs(changes []linkSuggestion) [][2]string {
	pairs := make([][2]string, 0, len(changes))
	for _, change := range kinds(changes, linkChangeLink) {
		pairs = append(pairs, [2]string{change.repository, change.peer})
	}
	return pairs
}

// longestFleetRoute is the number of links on the longest route between two
// repositories once the proposals are applied. It walks the links here rather
// than reusing the computation under test, and fails when the proposals leave
// anything but one tree, so a claim about the shape compute chose is asserted
// and not assumed.
func longestFleetRoute(t *testing.T, names []string, existing [][2]string, proposals [][2]string) int {
	t.Helper()
	edges := append(append([][2]string(nil), existing...), proposals...)
	require.Len(t, edges, len(names)-1,
		"a fleet of %d repositories is one tree only with %d links", len(names), len(names)-1)
	longest := measureLongestRoute(names, edges)
	require.GreaterOrEqual(t, longest, 0, "the links leave the fleet in more than one piece")
	return longest
}

// measureLongestRoute is the same measurement without the assertions, which is
// what the enumeration of every rival proposal calls once per candidate. It
// answers -1 when the links do not join every repository.
func measureLongestRoute(names []string, edges [][2]string) int {
	adjacency := map[string][]string{}
	for _, edge := range edges {
		adjacency[edge[0]] = append(adjacency[edge[0]], edge[1])
		adjacency[edge[1]] = append(adjacency[edge[1]], edge[0])
	}
	longest := 0
	for _, start := range names {
		reach, reached := walkLinksFrom(adjacency, start)
		if reached != len(names) {
			return -1
		}
		if reach > longest {
			longest = reach
		}
	}
	return longest
}

// walkLinksFrom answers how far the farthest repository is from one start and
// how many repositories that start reaches at all.
func walkLinksFrom(adjacency map[string][]string, start string) (int, int) {
	distances := map[string]int{start: 0}
	queue := []string{start}
	longest := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, peer := range adjacency[current] {
			if _, isReached := distances[peer]; isReached {
				continue
			}
			distances[peer] = distances[current] + 1
			if distances[peer] > longest {
				longest = distances[peer]
			}
			queue = append(queue, peer)
		}
	}
	return longest, len(distances)
}

// TestMinimalTopologyJoinsTwoChainsAtTheirCentres is the fleet of section
// 27.12 vector 30: two chains of five, one link to join them, and two answers
// that cost the same to write and very different amounts to release from.
func TestMinimalTopologyJoinsTwoChainsAtTheirCentres(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "v", "w", "x", "y", "z"}
	existing := append(chainLinks("a", "b", "c", "d", "e"), chainLinks("v", "w", "x", "y", "z")...)
	a := linkedFleet(t, names, existing)

	proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
	require.Equal(t, [][2]string{{"c", "x"}}, proposals, "two groups are joined by one link, at their centres")
	assert.Equal(t, 5, longestFleetRoute(t, names, existing, proposals))

	// Joining the chains end to end writes the same one link and leaves every
	// route across the fleet four hops longer.
	assert.Equal(t, 9, longestFleetRoute(t, names, existing, [][2]string{{"a", "v"}}))
}

// TestMinimalTopologyPaysOnlyTheTwoSmallestRadiiForAThirdGroup: three groups
// of equal radius cost r2 + r3 + 2, which is what hanging every other centre
// off one hub centre gives and what a chain of groups would beat only by
// making some other route longer.
func TestMinimalTopologyPaysOnlyTheTwoSmallestRadiiForAThirdGroup(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	existing := append(chainLinks("a", "b", "c"), chainLinks("d", "e", "f")...)
	existing = append(existing, chainLinks("g", "h", "i")...)
	a := linkedFleet(t, names, existing)

	proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
	require.Equal(t, [][2]string{{"b", "e"}, {"b", "h"}}, proposals,
		"every other centre links to the centre of the hub group")
	// max(d, r1+r2+1, r2+r3+2) with d = 2 and every radius 1.
	assert.Equal(t, 4, longestFleetRoute(t, names, existing, proposals))
	assert.Equal(t, 8, longestFleetRoute(t, names, existing, [][2]string{{"c", "d"}, {"f", "g"}}),
		"joining the groups end to end makes one chain of every repository instead")
}

// TestMinimalTopologyBreaksEveryTieByFoldedIdentity: a group of an even
// number of repositories has two centres and two groups can have one radius,
// and a proposal that depended on which was found first would differ between
// two runs over one fleet.
func TestMinimalTopologyBreaksEveryTieByFoldedIdentity(t *testing.T) {
	t.Run("the two centres of one group", func(t *testing.T) {
		names := []string{"a", "b", "c", "d", "z"}
		existing := chainLinks("a", "b", "c", "d")
		a := linkedFleet(t, names, existing)

		proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
		require.Equal(t, [][2]string{{"b", "z"}}, proposals,
			"b and c are both centres of the chain, and b sorts first")
		assert.Equal(t, 3, longestFleetRoute(t, names, existing, proposals))
	})

	t.Run("two groups of one radius", func(t *testing.T) {
		names := []string{"a", "b", "c", "d", "e", "f"}
		existing := append(chainLinks("a", "b", "c"), chainLinks("d", "e", "f")...)
		a := linkedFleet(t, names, existing)

		proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
		require.Equal(t, [][2]string{{"b", "e"}}, proposals,
			"both groups have radius 1, and the centre b sorts before the centre e")
		assert.Equal(t, 3, longestFleetRoute(t, names, existing, proposals))
	})

	t.Run("identities spelled in different cases", func(t *testing.T) {
		names := []string{"API", "Sdk", "web"}
		a := linkedFleet(t, names, chainLinks("API", "Sdk"))

		require.Equal(t, [][2]string{{"API", "web"}}, proposedPairs(requireLinkSuggestions(t, a, "minimal")),
			"the fold orders the identities and the written spelling is kept")
	})
}

// TestMinimalTopologyWritesTheLinkInACheckoutItHolds: a link is a checkout,
// so a centre this run never walked into cannot hold one. The nearest
// composed member stands in for it, which is the one case the shortest
// longest route is not guaranteed.
func TestMinimalTopologyWritesTheLinkInACheckoutItHolds(t *testing.T) {
	// zed holds a link to a checkout that was never initialized, so the
	// group's centre m is a name and nothing else; p is roster-only too.
	a := computeFleet(t, fleetPeer("zed", []string{"m", "p"}, map[string]string{"m": ".links/m"}))

	links := kinds(requireLinkSuggestions(t, a, "minimal"), linkChangeLink)
	require.Len(t, links, 1, "p is joined once and m is already joined to zed")
	assert.Equal(t, "zed", links[0].repository, "the centre m is no checkout, so the nearest composed member writes it")
	assert.Equal(t, "p", links[0].peer)

	// With the centre composed the link is written at the centre itself.
	a = linkedFleet(t, []string{"m", "p", "zed"}, [][2]string{{"m", "zed"}})
	assert.Equal(t, [][2]string{{"m", "p"}}, proposedPairs(requireLinkSuggestions(t, a, "minimal")))
}

// TestMinimalTopologyStarsAnUnlinkedFleetOnItsFirstIdentity: every group of a
// fleet with no links is one repository of radius zero, so the tie goes to the
// first identity and every other repository links to it.
func TestMinimalTopologyStarsAnUnlinkedFleetOnItsFirstIdentity(t *testing.T) {
	names := []string{"api", "sdk", "web"}
	a := linkedFleet(t, names, nil)

	proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
	require.Equal(t, [][2]string{{"api", "sdk"}, {"api", "web"}}, proposals)
	assert.Equal(t, 2, longestFleetRoute(t, names, nil, proposals))
}

// TestMinimalTopologyProposesNothingForOneGroup: a fleet the existing links
// already join is a fleet with no group to join to another.
func TestMinimalTopologyProposesNothingForOneGroup(t *testing.T) {
	a := linkedFleet(t, []string{"a", "b", "c", "d"}, chainLinks("a", "b", "c", "d"))
	assert.Empty(t, kinds(requireLinkSuggestions(t, a, "minimal"), linkChangeLink))
}

// TestMinimalTopologyKeepsEveryExistingLink: a link is history's only record
// of what a release incorporated, so a shape that would be shorter without one
// is not a shape compute may propose.
func TestMinimalTopologyKeepsEveryExistingLink(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "z"}
	existing := chainLinks("a", "b", "c", "d", "e")
	a := linkedFleet(t, names, existing)

	proposals := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
	require.Equal(t, [][2]string{{"c", "z"}}, proposals, "the chain is kept and z joins it at its centre")
	assert.Equal(t, 4, longestFleetRoute(t, names, existing, proposals))
	// The whole chain is still declared at both ends of every hop.
	for _, link := range existing {
		assert.Equal(t, ".links/"+link[1], a.workspace.RepositoryByName(link[0]).Links[link[1]])
		assert.Equal(t, ".links/"+link[0], a.workspace.RepositoryByName(link[1]).Links[link[0]])
	}
}

// TestMinimalTopologyProposesTheSameLinksWhateverOrderTheFleetIsRead: the
// order repositories were composed in is an accident of which link was walked
// first, and two runs over one fleet must still agree on what to write.
func TestMinimalTopologyProposesTheSameLinksWhateverOrderTheFleetIsRead(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	existing := append(chainLinks("a", "b", "c", "d"), chainLinks("e", "f", "g")...)
	expected := proposedPairs(requireLinkSuggestions(t, linkedFleet(t, names, existing), "minimal"))
	require.NotEmpty(t, expected)

	rng := rand.New(rand.NewSource(20260921))
	for attempt := 0; attempt < 20; attempt++ {
		shuffled := append([]string(nil), names...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		assert.Equal(t, expected,
			proposedPairs(requireLinkSuggestions(t, linkedFleet(t, shuffled, existing), "minimal")),
			"composed in the order %v", shuffled)
	}
}

// TestCentreJoiningReachesTheShortestLongestRoutePossible checks the claim
// section 27.11 makes against the definition rather than against the formula:
// over random forests, every set of joining links that yields a tree is
// enumerated, and centre joining must reach the least longest route any of
// them has.
func TestCentreJoiningReachesTheShortestLongestRoutePossible(t *testing.T) {
	rng := rand.New(rand.NewSource(20260921))
	for attempt := 0; attempt < 300; attempt++ {
		names, existing := randomFleetForest(rng)
		proposals := proposedPairs(requireLinkSuggestions(t, linkedFleet(t, names, existing), "minimal"))
		reached := longestFleetRoute(t, names, existing, proposals)
		require.Equal(t, bestLongestRoute(t, names, existing), reached,
			"fleet %v joined by %v was joined at %v", names, existing, proposals)
	}
}

// randomFleetForest is a fleet of up to nine repositories whose existing links
// leave it in two to four groups, each of them a random tree.
func randomFleetForest(rng *rand.Rand) ([]string, [][2]string) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}[:2+rng.Intn(8)]
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	groups := 2 + rng.Intn(3)
	if groups > len(names) {
		groups = len(names)
	}
	// The first repositories seed one group each and the rest join a random
	// group by hanging off a random member of it, which grows a random tree.
	members := make([][]string, groups)
	var links [][2]string
	for index, name := range names {
		if index < groups {
			members[index] = []string{name}
			continue
		}
		group := rng.Intn(groups)
		links = append(links, [2]string{members[group][rng.Intn(len(members[group]))], name})
		members[group] = append(members[group], name)
	}
	sort.Strings(names)
	return names, links
}

// bestLongestRoute is the least longest route any completion of the forest
// has: every set of the right number of joining links is tried, and the ones
// that leave one tree are measured.
func bestLongestRoute(t *testing.T, names []string, existing [][2]string) int {
	t.Helper()
	groupOf := groupsOfForest(names, existing)
	var candidates [][2]string
	for first := 0; first < len(names); first++ {
		for second := first + 1; second < len(names); second++ {
			if groupOf[names[first]] != groupOf[names[second]] {
				candidates = append(candidates, [2]string{names[first], names[second]})
			}
		}
	}
	distinct := map[string]bool{}
	for _, group := range groupOf {
		distinct[group] = true
	}
	best := len(names)
	var choose func(from int, chosen [][2]string, joined map[string]string)
	choose = func(from int, chosen [][2]string, joined map[string]string) {
		if len(chosen) == len(distinct)-1 {
			best = min(best, measureLongestRoute(names, append(append([][2]string(nil), existing...), chosen...)))
			return
		}
		for index := from; index < len(candidates); index++ {
			keeps, absorbed := joined[candidates[index][0]], joined[candidates[index][1]]
			if keeps == absorbed {
				continue
			}
			next := make(map[string]string, len(joined))
			for name, group := range joined {
				next[name] = group
				if group == absorbed {
					next[name] = keeps
				}
			}
			choose(index+1, append(append([][2]string(nil), chosen...), candidates[index]), next)
		}
	}
	choose(0, nil, groupOf)
	require.Less(t, best, len(names), "the enumeration found no completion of this forest at all")
	return best
}

// groupsOfForest answers which group each repository's existing links leave it
// in, named by the folded identity of the member that seeded it.
func groupsOfForest(names []string, existing [][2]string) map[string]string {
	groupOf := map[string]string{}
	for _, name := range names {
		groupOf[name] = strings.ToLower(name)
	}
	for _, link := range existing {
		absorbed := groupOf[link[1]]
		for name, group := range groupOf {
			if group == absorbed {
				groupOf[name] = groupOf[link[0]]
			}
		}
	}
	return groupOf
}
