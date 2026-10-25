// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestCachedWalkIsTheLiteralWalk checks every route walk can take against
// reach() as §9.2 writes it, target for target and in order: the prefix of a
// single source's unbounded walk, the composition of several sources' walks,
// the outright walk of a wide source set, and each of them served again from
// its cache. The order and the credited source are both output (they decide
// provenance and what a plan reports first), so equality is on the whole list.
//
// Workspaces are dense on purpose. Diamonds, sources that reach one another,
// targets equidistant from two sources and edges of kinds a unit does not
// cross are where a composition goes wrong, and a sparse graph has none.
func TestCachedWalkIsTheLiteralWalk(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	kindChoices := []map[model.DepKind]bool{
		nil,
		{model.KindDependencies: true},
		{model.KindDependencies: true, model.KindPeerDependencies: true},
		{},
	}
	allKinds := []model.DepKind{model.KindDependencies, model.KindPeerDependencies, model.KindDevDependencies}
	for round := 0; round < 150; round++ {
		n := 2 + rng.Intn(14)
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("p%02d", i)
		}
		// Edges run from a provider to a consumer later in a random order, so
		// the graph is acyclic and name order says nothing about topology.
		order := rng.Perm(n)
		cp := &computation{log: zerolog.Nop(), edges: make(map[string][]edge)}
		for i, from := range order {
			for _, to := range order[i+1:] {
				if rng.Intn(3) == 0 {
					cp.edges[names[from]] = append(cp.edges[names[from]],
						edge{to: names[to], kind: allKinds[rng.Intn(len(allKinds))]})
				}
			}
		}
		for ask := 0; ask < 40; ask++ {
			sources := make(map[string]bool)
			for k := 1 + rng.Intn(min(n, walkComposeLimit+3)); k > 0; k-- {
				sources[names[rng.Intn(n)]] = true
			}
			depth := []int{1, 2, 3, depthUnbounded}[rng.Intn(4)]
			kinds := kindChoices[rng.Intn(len(kindChoices))]

			want := cp.walkLiteral(sources, depth, kinds)
			for again := 0; again < 2; again++ {
				got := cp.walk(sources, depth, kinds)
				require.Equalf(t, len(want), len(got), "round %d: sources %v depth %d kinds %v", round, sources, depth, kinds)
				for i := range want {
					require.Equalf(t, want[i], got[i], "round %d: sources %v depth %d kinds %v, target %d",
						round, sources, depth, kinds, i)
				}
			}
		}
	}
}

// TestWalkResultCannotReachItsSharedTail pins the clipped capacity: a bounded
// walk is a prefix of a shared unbounded one, and a caller appending to it
// must get a copy rather than overwrite the next level for everyone else.
func TestWalkResultCannotReachItsSharedTail(t *testing.T) {
	cp := &computation{log: zerolog.Nop(), edges: map[string][]edge{
		"a": {{to: "b", kind: model.KindDependencies}},
		"b": {{to: "c", kind: model.KindDependencies}},
	}}
	source := map[string]bool{"a": true}
	near := cp.walk(source, 1, nil)
	require.Len(t, near, 1)
	_ = append(near, target{name: "intruder"})
	all := cp.walk(source, depthUnbounded, nil)
	require.Len(t, all, 2)
	require.Equal(t, "c", all[1].name)
}

// TestWalkCacheChargesBothTiersToOneBudget: the single-source walks are the
// cache's larger tier, a quadratic number of targets over a dependency chain,
// so they are retained only while the one budget both tiers share allows. A
// walk past it is still answered in full, and simply not kept.
func TestWalkCacheChargesBothTiersToOneBudget(t *testing.T) {
	cp := &computation{log: zerolog.Nop(), edges: map[string][]edge{
		"a": {{to: "b", kind: model.KindDependencies}},
		"b": {{to: "c", kind: model.KindDependencies}},
		"c": {{to: "d", kind: model.KindDependencies}},
	}}
	cp.walks = &walkCache{single: make(map[walkKey][]target), exact: make(map[walkKey][]target),
		held: walkCacheBudget - 2}

	fromA := cp.walk(map[string]bool{"a": true}, depthUnbounded, nil)
	require.Equal(t, cp.walkLiteral(map[string]bool{"a": true}, depthUnbounded, nil), fromA)
	require.Empty(t, cp.walks.single, "three targets do not fit in the two left")
	require.Equal(t, walkCacheBudget-2, cp.walks.held)

	near := cp.walk(map[string]bool{"a": true}, 1, nil)
	require.Equal(t, cp.walkLiteral(map[string]bool{"a": true}, 1, nil), near)
	require.Len(t, cp.walks.single, 1, "one target does")
	require.Equal(t, walkCacheBudget-1, cp.walks.held)

	fromB := cp.walk(map[string]bool{"b": true}, depthUnbounded, nil)
	require.Len(t, fromB, 2)
	require.Len(t, cp.walks.single, 1, "two targets no longer do")
}

// TestBoundedWalkWalksItsOwnDepth pins what a depth-one ask over a chain
// costs: one level, whether or not the source's unbounded walk is kept, and
// nothing at all the second time it is asked.
func TestBoundedWalkWalksItsOwnDepth(t *testing.T) {
	cp := &computation{log: zerolog.Nop(), stats: &HistoryStats{}, edges: map[string][]edge{
		"a": {{to: "b", kind: model.KindDependencies}},
		"b": {{to: "c", kind: model.KindDependencies}},
		"c": {{to: "d", kind: model.KindDependencies}},
	}}
	source := map[string]bool{"a": true}
	for again := 0; again < 2; again++ {
		require.Equal(t, []target{{name: "b", from: "a", level: 1}}, cp.walk(source, 1, nil))
	}
	require.EqualValues(t, 1, cp.stats.WalkTargets.Load(), "the unbounded reach of a is never walked")

	pair := map[string]bool{"a": true, "c": true}
	require.Equal(t, []target{{name: "b", from: "a", level: 1}, {name: "d", from: "c", level: 1}},
		cp.walk(pair, 1, nil))
	require.EqualValues(t, 2, cp.stats.WalkTargets.Load(), "a composed walk reads its sources' bounded walks")
}

// TestLiteralWalkAllocatesItsOutputAndSeenSet pins the walk's own heap: the
// targets it returns and the set of packages it has seen, grown as the walk
// grows, and no second queue or per-target origin beside them. A 63-target
// chain measured 16 allocations; the queue and origin map took it to 89.
func TestLiteralWalkAllocatesItsOutputAndSeenSet(t *testing.T) {
	cp := &computation{log: zerolog.Nop(), edges: map[string][]edge{}}
	for i := 0; i < 63; i++ {
		cp.edges[fmt.Sprintf("p%02d", i)] = []edge{{to: fmt.Sprintf("p%02d", i+1), kind: model.KindDependencies}}
	}
	source := map[string]bool{"p00": true}
	allocations := testing.AllocsPerRun(100, func() { cp.walkLiteral(source, depthUnbounded, nil) })
	require.LessOrEqual(t, allocations, 20.0)
}

// TestOneSourceReachesEveryTargetWithoutAnIndex: a unit over one package is
// nearly every unit, and every target it reaches is reached from that
// package, so the answer is one list and nothing per target.
func TestOneSourceReachesEveryTargetWithoutAnIndex(t *testing.T) {
	cp := &computation{log: zerolog.Nop(), edges: map[string][]edge{
		"a": {{to: "b", kind: model.KindDependencies}, {to: "c", kind: model.KindDependencies}},
		"b": {{to: "d", kind: model.KindDependencies}},
	}}
	source := map[string]bool{"a": true}
	reaching := cp.reachingSources(source, depthUnbounded, nil)
	for _, target := range cp.walk(source, depthUnbounded, nil) {
		require.Equal(t, []string{"a"}, reaching.of(target.name))
	}
	allocations := testing.AllocsPerRun(100, func() { cp.reachingSources(source, depthUnbounded, nil) })
	require.LessOrEqual(t, allocations, 1.0, "the source's name, and no map over the targets")

	pair := cp.reachingSources(map[string]bool{"b": true, "c": true}, 1, nil)
	require.Equal(t, []string{"b"}, pair.of("d"))
	require.Nil(t, pair.of("a"))
}

// TestGlobMatchesIsTheScan checks the indexed glob against the definition it
// replaced, a fold and a match of every package for every term: over the
// sorted-run shortcut for a trailing "*", the general matcher for an interior
// one, names differing only in case, names that are prefixes of one another,
// and every ownership class a commit can read a pattern from.
func TestGlobMatchesIsTheScan(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	pieces := []string{"@acme/", "@Acme/", "ui", "UI", "ui-kit", "core", "a", "", "/x"}
	repositories := []string{"", "source", "Source", "other", "control"}
	for round := 0; round < 80; round++ {
		cp := &computation{log: zerolog.Nop(), controlRepo: "control"}
		if rng.Intn(2) == 0 {
			cp.histories = map[string]RepositoryHistory{"source": {Name: "source"}}
		}
		for i := 0; i < 1+rng.Intn(12); i++ {
			cp.pkgs = append(cp.pkgs, &model.Package{
				Name:       pieces[rng.Intn(len(pieces))] + pieces[rng.Intn(len(pieces))] + fmt.Sprint(rng.Intn(3)),
				Repository: repositories[rng.Intn(len(repositories))],
			})
		}
		for ask := 0; ask < 60; ask++ {
			pattern := strings.ToLower(pieces[rng.Intn(len(pieces))])
			switch rng.Intn(4) {
			case 0:
				pattern += "*"
			case 1:
				pattern = "*" + pattern
			case 2:
				pattern += "*" + fmt.Sprint(rng.Intn(3))
			default:
				pattern += "*" + strings.ToLower(pieces[rng.Intn(len(pieces))]) + "*"
			}
			rec := &commitRec{repository: repositories[rng.Intn(len(repositories))]}
			var want []int
			for i, p := range cp.pkgs {
				if cp.commitCanScope(rec, p) && IsGlobMatch(pattern, strings.ToLower(p.Name)) {
					want = append(want, i)
				}
			}
			for again := 0; again < 2; again++ {
				require.Equalf(t, want, cp.globMatches(pattern, rec), "round %d: %q read from %q", round, pattern, rec.repository)
			}
		}
	}
}
