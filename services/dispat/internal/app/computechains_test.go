// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// linkedFleet gives the planner an already composed checkout for every member.
// Integration tests exercise the stricter case where a new checkout must be
// created before it can own the next link.
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
	return held
}

func chainLinks(names ...string) [][2]string {
	links := make([][2]string, 0, len(names)-1)
	for i := 0; i+1 < len(names); i++ {
		links = append(links, [2]string{names[i], names[i+1]})
	}
	return links
}

func proposedPairs(changes []linkSuggestion) [][2]string {
	pairs := make([][2]string, 0, len(changes))
	for _, change := range kinds(changes, linkChangeLink) {
		pairs = append(pairs, [2]string{change.repository, change.peer})
	}
	return pairs
}

// completeMockChain models one repair round at a time. Both ends of each
// suggested pair become available for the next round, just as recomposition
// observes a newly materialized checkout. The independent shape check below
// checks the result, rather than reusing the planner's component calculation.
func completeMockChain(t *testing.T, a *App, names []string, existing [][2]string) [][2]string {
	t.Helper()
	all := append([][2]string(nil), existing...)
	for round := 0; round <= len(names); round++ {
		changes := requireLinkSuggestions(t, a, "minimal")
		joins := kinds(changes, linkChangeLink)
		require.LessOrEqual(t, len(joins), 1, "one joining edge per repair round")
		if len(joins) == 0 {
			requirePath(t, names, all)
			return all
		}
		join := joins[0]
		owner := a.workspace.RepositoryByName(join.repository)
		peer := a.workspace.RepositoryByName(join.peer)
		require.NotNil(t, owner)
		require.NotNil(t, peer)
		if owner.Links == nil {
			owner.Links = map[string]string{}
		}
		if peer.Links == nil {
			peer.Links = map[string]string{}
		}
		owner.Links[join.peer] = ".links/" + join.peer
		peer.Links[join.repository] = ".links/" + join.repository
		all = append(all, [2]string{join.repository, join.peer})
	}
	t.Fatal("chain planner did not converge after one round per missing edge")
	return nil
}

func requirePath(t *testing.T, names []string, links [][2]string) {
	t.Helper()
	require.Len(t, links, len(names)-1, "a complete chain on Q members has Q-1 links")
	adjacency := make(map[string][]string, len(names))
	for _, name := range names {
		adjacency[strings.ToLower(name)] = nil
	}
	for _, link := range links {
		a, b := strings.ToLower(link[0]), strings.ToLower(link[1])
		_, knownA := adjacency[a]
		_, knownB := adjacency[b]
		require.True(t, knownA && knownB && a != b, "invalid link %v", link)
		adjacency[a] = append(adjacency[a], b)
		adjacency[b] = append(adjacency[b], a)
	}
	for name, neighbors := range adjacency {
		require.LessOrEqual(t, len(neighbors), 2, "%s has a branch", name)
	}
	visited := map[string]bool{}
	queue := []string{strings.ToLower(names[0])}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if visited[next] {
			continue
		}
		visited[next] = true
		queue = append(queue, adjacency[next]...)
	}
	require.Len(t, visited, len(names), "the links must join every known member")
}

func TestMinimalTopologyJoinsPathComponentsOnlyAtEndpoints(t *testing.T) {
	names := []string{"a", "b", "c", "v", "w", "x"}
	existing := append(chainLinks("a", "b", "c"), chainLinks("v", "w", "x")...)
	a := linkedFleet(t, names, existing)
	joins := proposedPairs(requireLinkSuggestions(t, a, "minimal"))
	require.Equal(t, [][2]string{{"a", "v"}}, joins)
	completeMockChain(t, a, names, existing)
}

func TestMinimalTopologyGrowsUnlinkedFleetAsAChain(t *testing.T) {
	names := []string{"api", "sdk", "shop", "web"}
	a := linkedFleet(t, names, nil)
	require.Equal(t, [][2]string{{"api", "sdk"}}, proposedPairs(requireLinkSuggestions(t, a, "minimal")))
	completeMockChain(t, a, names, nil)
}

func TestMinimalTopologyOneAndTwoMemberBaseCases(t *testing.T) {
	t.Run("one member", func(t *testing.T) {
		a := linkedFleet(t, []string{"solo"}, nil)
		assert.Empty(t, proposedPairs(requireLinkSuggestions(t, a, "minimal")))
		requirePath(t, []string{"solo"}, nil)
	})
	t.Run("two members", func(t *testing.T) {
		names := []string{"alpha", "beta"}
		a := linkedFleet(t, names, nil)
		require.Equal(t, [][2]string{{"alpha", "beta"}}, proposedPairs(requireLinkSuggestions(t, a, "minimal")))
		completeMockChain(t, a, names, nil)
	})
}

func TestMinimalTopologyKeepsExistingPathAndAddsNoEdge(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	existing := chainLinks(names...)
	a := linkedFleet(t, names, existing)
	assert.Empty(t, proposedPairs(requireLinkSuggestions(t, a, "minimal")))
	requirePath(t, names, existing)
}

func TestMinimalTopologyRejectsExistingBranchEvenWhenConnected(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	a := linkedFleet(t, names, [][2]string{{"a", "b"}, {"a", "c"}, {"a", "d"}})
	changes, err := a.suggestLinks("minimal")
	require.Error(t, err)
	assert.Empty(t, changes)
	assert.Contains(t, err.Error(), "chain")
}

func TestMinimalTopologyDefersJoiningUntilMissingCheckoutIsInitialized(t *testing.T) {
	a := computeFleet(t, fleetPeer("zed", []string{"m", "p"}, map[string]string{"m": ".links/m"}))
	a.workspace.Findings = []config.LinkFinding{{
		Code: config.DiagnosticRepositoryInvalid, Repository: "zed", Peer: "m",
		Message: "the checkout for m is missing",
	}}
	changes := requireLinkSuggestions(t, a, "minimal")
	assert.Empty(t, kinds(changes, linkChangeLink))
	require.Len(t, kinds(changes, linkChangeInit), 1)
}

func TestMinimalTopologyRefusesToAttachAtAnUnwritableInterior(t *testing.T) {
	a := computeFleet(t, fleetPeer("mid", []string{"a", "z", "p"},
		map[string]string{"a": ".links/a", "z": ".links/z"}))
	changes, err := a.suggestLinks("minimal")
	require.Error(t, err)
	assert.Empty(t, changes)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestMinimalTopologyIsDeterministicAcrossRosterOrder(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	existing := append(chainLinks("a", "b", "c"), chainLinks("d", "e")...)
	expected := proposedPairs(requireLinkSuggestions(t, linkedFleet(t, names, existing), "minimal"))
	rng := rand.New(rand.NewSource(20261028))
	for attempt := 0; attempt < 20; attempt++ {
		shuffled := append([]string{names[0]}, names[1:]...)
		rng.Shuffle(len(shuffled)-1, func(i, j int) { shuffled[i+1], shuffled[j+1] = shuffled[j+1], shuffled[i+1] })
		assert.Equal(t, expected, proposedPairs(requireLinkSuggestions(t, linkedFleet(t, shuffled, existing), "minimal")))
	}
}

func TestMinimalTopologyCompletesSampledPathForests(t *testing.T) {
	rng := rand.New(rand.NewSource(20261028))
	for attempt := 0; attempt < 150; attempt++ {
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}[:2+rng.Intn(7)]
		groups := 2 + rng.Intn(3)
		if groups > len(names) {
			groups = len(names)
		}
		shuffled := append([]string(nil), names...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		parts := make([][]string, groups)
		for i, name := range shuffled {
			parts[i%groups] = append(parts[i%groups], name)
		}
		var existing [][2]string
		for _, part := range parts {
			existing = append(existing, chainLinks(part...)...)
		}
		ordered := append([]string{names[0]}, names[1:]...)
		sort.Strings(ordered[1:])
		completeMockChain(t, linkedFleet(t, ordered, existing), ordered, existing)
	}
}
