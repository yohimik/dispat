// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

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

// TestContributionsOrderAsAStableSortByProvider checks the provider order
// against the stable sort it stands for, ties included: a provider's
// contributions keep the order they were made in, which is what decides the
// records' order.
func TestContributionsOrderAsAStableSortByProvider(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	providers := []string{"a", "b", "B", "core", "utils", ""}
	for round := 0; round < 500; round++ {
		sources := make([]StaleSource, rng.Intn(12))
		for i := range sources {
			sources[i] = StaleSource{Provider: providers[rng.Intn(len(providers))], Commit: fmt.Sprint(i), Level: i}
		}
		want := slices.Clone(sources)
		sort.SliceStable(want, func(i, j int) bool { return want[i].Provider < want[j].Provider })
		var wantProviders []string
		for _, source := range want {
			if !slices.Contains(wantProviders, source.Provider) {
				wantProviders = append(wantProviders, source.Provider)
			}
		}
		got, gotProviders := orderByProvider(slices.Clone(sources))
		require.Equalf(t, len(want), len(got), "round %d", round)
		for i := range want {
			require.Equalf(t, want[i], got[i], "round %d, contribution %d", round, i)
		}
		require.Equalf(t, wantProviders, gotProviders, "round %d", round)
	}
	one := []StaleSource{{Provider: "core", Commit: "c1"}, {Provider: "core", Commit: "c2"}}
	allocations := testing.AllocsPerRun(100, func() { orderByProvider(one) })
	require.LessOrEqual(t, allocations, 1.0, "contributions from one provider need only the provider list")
}
