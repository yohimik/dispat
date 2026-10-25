// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// literalOwedPairs is listOwedPairs as §13.3 reads it: one unbounded walk per
// released provider, keeping the first target of each (provider, baseline).
func literalOwedPairs(cp *computation) []owedPair {
	kinds := cp.resolveOwedKinds()
	isFound := make(map[[2]string]bool)
	var pairs []owedPair
	for _, provider := range cp.order {
		if !isAnyReleaseParsed(cp.tags[provider]) {
			continue
		}
		repository := ""
		if p := cp.byName[provider]; p != nil {
			repository = p.Repository
		}
		for _, t := range cp.walkLiteral(map[string]bool{provider: true}, depthUnbounded, kinds) {
			baseline := cp.baselineBoundary(t.name, historyKey(repository, ""))
			if key := [2]string{provider, baseline}; baseline != "" && !isFound[key] {
				isFound[key] = true
				pairs = append(pairs, owedPair{provider: provider, consumer: t.name, baseline: baseline})
			}
		}
	}
	return pairs
}

// randomOwedWorkspace is a dense random DAG whose plan order is a random
// topological order, with edges of every kind (duplicates included), shared,
// distinct and empty baselines and some providers never released. With
// repositories > 0 it is a fleet whose baselines differ per repository.
func randomOwedWorkspace(rng *rand.Rand, repositories int) *computation {
	allKinds := []model.DepKind{model.KindDependencies, model.KindPeerDependencies,
		model.KindDevDependencies, model.KindOptionalDependencies}
	n := 2 + rng.Intn(18)
	cp := &computation{log: zerolog.Nop(), edges: make(map[string][]edge), byName: make(map[string]*model.Package),
		tags: make(map[string]gitx.Tags), rel: make(map[string]*Release),
		publishedBoundaries: make(map[string]map[string]string)}
	if repositories > 0 {
		cp.histories = make(map[string]RepositoryHistory)
		cp.parsers = make(map[string]*ccme.Parser)
		for r := 0; r < repositories; r++ {
			name := fmt.Sprintf("Repo-%d", r)
			cp.histories[globx.Fold(name)] = RepositoryHistory{Name: name}
			cp.parsers[globx.Fold(name)] = nil // the specification's kinds
		}
	}
	baselines := []string{"", "", "b1", "b2", "b3", "b4"}
	for _, i := range rng.Perm(n) {
		name := fmt.Sprintf("p%02d", i)
		cp.order = append(cp.order, name)
		pkg := &model.Package{Name: name}
		if repositories > 0 {
			pkg.Repository = fmt.Sprintf("repo-%d", rng.Intn(repositories))
			boundaries := make(map[string]string)
			for r := 0; r < repositories; r++ {
				if b := baselines[rng.Intn(len(baselines))]; b != "" {
					boundaries[globx.Fold(fmt.Sprintf("repo-%d", r))] = historyKey(fmt.Sprintf("repo-%d", r), b)
				}
			}
			cp.publishedBoundaries[name] = boundaries
		}
		cp.byName[name] = pkg
		cp.rel[name] = &Release{BaselineCommit: baselines[rng.Intn(len(baselines))]}
		cp.tags[name] = gitx.Tags{{Name: name + "@1.0.0", Parsed: rng.Intn(4) != 0, Commit: "c"}}
	}
	for i, from := range cp.order {
		for _, to := range cp.order[i+1:] {
			for rng.Intn(3) == 0 {
				cp.edges[from] = append(cp.edges[from], edge{to: to, kind: allKinds[rng.Intn(len(allKinds))]})
			}
		}
		rng.Shuffle(len(cp.edges[from]), func(a, b int) {
			cp.edges[from][a], cp.edges[from][b] = cp.edges[from][b], cp.edges[from][a]
		})
	}
	return cp
}

// TestOwedPairsAreTheLiteralWalksPairs: the pairs decide which owed windows
// are read and in which order the union grows, so they must equal the
// literal reading's pair for pair, consumer and order included, in one
// history and in a fleet.
func TestOwedPairsAreTheLiteralWalksPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 3000; round++ {
		cp := randomOwedWorkspace(rng, round%3)
		require.Equalf(t, literalOwedPairs(cp), cp.listOwedPairs(), "round %d", round)
	}
}

// TestReachedBaselinesFollowTheWalk checks the merge against the literal walk
// under every kind filter, nil being every kind and the empty set none.
func TestReachedBaselinesFollowTheWalk(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	kindChoices := []map[model.DepKind]bool{nil, {}, {model.KindDependencies: true},
		{model.KindPeerDependencies: true, model.KindDevDependencies: true}}
	for round := 0; round < 1000; round++ {
		cp := randomOwedWorkspace(rng, 0)
		kinds := kindChoices[round%len(kindChoices)]
		baselineOf := func(name string) string { return cp.rel[name].BaselineCommit }
		reached := cp.listReachedBaselines(baselineOf, kinds)
		for _, name := range cp.order {
			var want []reachedBaseline
			isSeen := make(map[string]bool)
			for _, t := range cp.walkLiteral(map[string]bool{name: true}, depthUnbounded, kinds) {
				if b := baselineOf(t.name); b != "" && !isSeen[b] {
					isSeen[b] = true
					want = append(want, reachedBaseline{baseline: b, consumer: t.name, level: t.level})
				}
			}
			require.Equalf(t, want, reached[name], "round %d, from %s", round, name)
		}
	}
}

// TestOwedPairsAreLinearOnAChain pins the cost of the listing where one walk
// per provider is quadratic: a chain whose packages are all released at one
// commit has one pair per provider, and the listing allocates a few objects
// per package, not one walk's worth per provider.
func TestOwedPairsAreLinearOnAChain(t *testing.T) {
	const packages = 1024
	cp := &computation{log: zerolog.Nop(), edges: make(map[string][]edge), byName: make(map[string]*model.Package),
		tags: make(map[string]gitx.Tags), rel: make(map[string]*Release)}
	for i := 0; i < packages; i++ {
		name := fmt.Sprintf("pkg-%04d", i)
		cp.order = append(cp.order, name)
		cp.byName[name] = &model.Package{Name: name}
		cp.rel[name] = &Release{BaselineCommit: "base"}
		cp.tags[name] = gitx.Tags{{Name: name + "@1.0.0", Parsed: true, Commit: "base"}}
		if i > 0 {
			cp.edges[cp.order[i-1]] = []edge{{to: name}}
		}
	}
	var pairs []owedPair
	allocs := testing.AllocsPerRun(3, func() {
		cp.walks = nil
		pairs = cp.listOwedPairs()
	})
	require.Len(t, pairs, packages-1)
	require.LessOrEqual(t, allocs, float64(2*packages), "the listing must stay linear in the packages")
}
