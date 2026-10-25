// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelIndexNamesTheFirstCancelTheLoopNames: the cancel the index names
// for a commit and a package is the first one, in cancel order, whose scope
// holds the package and whose barrier the commit is behind, the one asking
// every cancel in turn names, over random DAGs with up to 150 cancels, so
// more than one word of them, and scopes that overlap.
func TestCancelIndexNamesTheFirstCancelTheLoopNames(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	packages := []string{"core", "app", "ui", "cli", "docs"}
	for round := 0; round < 40; round++ {
		n := 1 + rng.Intn(300)
		cp := &computation{byKey: make(map[string]*commitRec), ancTrusted: map[string]bool{"": true}}
		parents := make([][]int32, n)
		for rank := 0; rank < n; rank++ {
			key := fmt.Sprintf("c%03d", rank)
			rec := &commitRec{key: key, rank: rank}
			cp.byKey[key] = rec
			cp.commits = append(cp.commits, rec)
			for k := rng.Intn(3); k > 0 && rank+1 < n; k-- {
				parents[rank] = append(parents[rank], int32(rank+1+rng.Intn(n-rank-1)))
			}
		}
		cp.anc = newAncestryIndex(n, func(rank int) []int32 { return parents[rank] })
		var barriers []int32
		for j := rng.Intn(150); j >= 0; j-- {
			rank := rng.Intn(n)
			scope := make(map[string]bool)
			for _, pkg := range packages {
				if rng.Intn(3) == 0 {
					scope[pkg] = rng.Intn(5) != 0 // a false entry names nothing
				}
			}
			key := cp.commits[rank].key
			barriers = append(barriers, int32(rank))
			cp.cancels = append(cp.cancels, &cancelRec{key: key, scope: scope})
		}
		cp.anc.mark(barriers)
		for _, c := range cp.cancels {
			c.closure = cp.ancestorClosure(c.key)
		}
		index := cp.newCancelIndex()
		require.NotNil(t, index)
		for _, rec := range cp.commits {
			for _, pkg := range append(packages, "absent") {
				cp.cancelIndex = nil
				want := cp.firstCancel(rec.key, pkg)
				cp.cancelIndex = index
				require.Samef(t, want, cp.firstCancel(rec.key, pkg), "round %d: %s for %s", round, rec.key, pkg)
			}
		}
		require.Nil(t, cp.firstCancel("unknown", "core"), "a commit outside the union is behind no barrier")
	}
}
