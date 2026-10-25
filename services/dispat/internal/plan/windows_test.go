// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// randomCommitSet is a set over n ranks whose words may run past n, as a
// set indexed over a longer walk does.
func randomCommitSet(rng *rand.Rand, n int) *commitSet {
	if rng.Intn(5) == 0 {
		return nil
	}
	s := &commitSet{bits: make([]uint64, (n+63)/64+rng.Intn(2))}
	for w := range s.bits {
		s.bits[w] = rng.Uint64() & rng.Uint64()
	}
	for _, word := range s.bits {
		for ; word != 0; word &= word - 1 {
			s.size++
		}
	}
	return s
}

// TestCommitSetWithoutIsTheComplement: a window built a word at a time holds
// exactly the ranks below n the excluded set does not, with the same size, so
// every reader of it (has, len, each) sees the window a rank-by-rank build
// gives.
func TestCommitSetWithoutIsTheComplement(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for round := 0; round < 500; round++ {
		n := rng.Intn(300)
		excluded := randomCommitSet(rng, n)
		want := commitSetOf(n, func(yield func(int)) {
			for rank := 0; rank < n; rank++ {
				if !excluded.has(rank) {
					yield(rank)
				}
			}
		})
		got := commitSetWithout(n, excluded)
		require.Equalf(t, want.len(), got.len(), "round %d, n %d", round, n)
		for rank := 0; rank < n; rank++ {
			require.Equalf(t, want.has(rank), got.has(rank), "round %d, n %d, rank %d", round, n, rank)
		}
		for word := 0; word < (n+63)/64; word++ {
			require.Equalf(t, want.word(word), got.word(word), "round %d, n %d, word %d", round, n, word)
		}
		var wantRanks, gotRanks []int
		want.each(func(rank int) { wantRanks = append(wantRanks, rank) })
		got.each(func(rank int) { gotRanks = append(gotRanks, rank) })
		require.Equalf(t, wantRanks, gotRanks, "round %d, n %d", round, n)
	}
}

// TestRankByWindowsIsBuildUnionsRanking: ranking one walk from the windows'
// exclusion sets gives the union buildUnion builds from the windows' own
// listings, rank for rank, with windows of equal length, nested and not,
// held and not.
func TestRankByWindowsIsBuildUnionsRanking(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for round := 0; round < 500; round++ {
		n := 1 + rng.Intn(200)
		all := make([]gitx.Commit, n)
		for i := range all {
			all[i] = gitx.Commit{SHA: fmt.Sprintf("%040x", i)}
		}
		excluded := make([]*commitSet, 2+rng.Intn(6))
		sizes := make([]int, len(excluded))
		lists := make([][]gitx.Commit, len(excluded))
		for i := range excluded {
			switch r := rng.Intn(6); {
			case i > 0 && r == 0:
				excluded[i] = excluded[rng.Intn(i)] // an equal length, so ties decide
			case r == 1:
				excluded[i] = nil // a window that is the whole walk
			default:
				excluded[i] = commitSetOf(n, func(yield func(int)) {
					for rank := 0; rank < n; rank++ {
						if rng.Intn(3) == 0 {
							yield(rank)
						}
					}
				})
			}
			for pos, c := range all {
				if !excluded[i].has(pos) {
					lists[i] = append(lists[i], c)
				}
			}
			sizes[i] = n - excluded[i].len()
			require.Len(t, lists[i], sizes[i])
		}
		want := &computation{byKey: make(map[string]*commitRec), parents: make(map[string][]string)}
		want.buildUnion(lists)
		ranked, _ := rankByWindows(all, sizes, func(i int) *commitSet { return excluded[i] })
		got := &computation{byKey: make(map[string]*commitRec), parents: make(map[string][]string)}
		got.buildUnion([][]gitx.Commit{ranked})
		require.Equalf(t, len(want.commits), len(got.commits), "round %d", round)
		for i := range want.commits {
			require.Equalf(t, want.commits[i].key, got.commits[i].key, "round %d, rank %d", round, i)
		}
	}
}

// withoutUnionWalk hides the union walk of a Git implementation, so each
// window is read one by one, and keeps the head a composed workspace reads.
type withoutUnionWalk struct {
	TagInventoryGitx
	head interface {
		HeadSHA(context.Context) (string, error)
	}
}

func (g withoutUnionWalk) HeadSHA(ctx context.Context) (string, error) { return g.head.HeadSHA(ctx) }

// TestMergedHistoryPlansAsReadOneByOne is the byte-identity claim for the
// union walk over a history whose windows are not nested, so the union is
// ranked from the windows, and whose owed windows reach past it, so they are
// read in one walk of their own: the plan equals the one read a window per
// boundary, in one history and as a composed workspace's source.
func TestMergedHistoryPlansAsReadOneByOne(t *testing.T) {
	for _, isComposed := range []bool{false, true} {
		pkgs, deps, git := mergedHistoryWorkspace(3000, 40)
		plan := func(history TagInventoryGitx) *Plan {
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r", Log: zerolog.Nop()}
			if isComposed {
				for _, p := range pkgs {
					p.Repository, p.RepoRoot = "source", "/r"
				}
				opts.Repositories = map[string]RepositoryHistory{"source": {Name: "source", Root: "/r", Git: history}}
			}
			p, err := Compute(context.Background(), history, opts)
			require.NoError(t, err)
			return p
		}
		want, got := plan(withoutUnionWalk{TagInventoryGitx: git, head: git}), plan(git)
		require.Equalf(t, want.Order, got.Order, "composed %v", isComposed)
		require.Equalf(t, want.Diagnostics, got.Diagnostics, "composed %v", isComposed)
		require.Equalf(t, len(want.Releases), len(got.Releases), "composed %v", isComposed)
		for name, w := range want.Releases {
			require.Equalf(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])),
				"composed %v: release of %s", isComposed, name)
		}
	}
}
