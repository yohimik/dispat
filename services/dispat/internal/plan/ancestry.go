// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"math/bits"
)

// commitSet is an immutable set of the union's commits, one bit per rank. A
// pending window is one of these, shared by every package released at the
// same boundary: k+1 bitsets of C bits where a map per boundary keyed by
// commit id was the larger part of a wide plan's memory (CCME §13.11).
type commitSet struct {
	bits []uint64
	size int
}

func (s *commitSet) has(rank int) bool {
	return s != nil && rank>>6 < len(s.bits) && s.bits[rank>>6]&(1<<(uint(rank)&63)) != 0
}

func (s *commitSet) len() int {
	if s == nil {
		return 0
	}
	return s.size
}

// commitSetOf builds the set holding exactly the given ranks, of n commits.
func commitSetOf(n int, ranks func(yield func(int))) *commitSet {
	s := &commitSet{bits: make([]uint64, (n+63)/64)}
	ranks(func(r int) {
		if s.bits[r>>6]&(1<<(uint(r)&63)) == 0 {
			s.bits[r>>6] |= 1 << (uint(r) & 63)
			s.size++
		}
	})
	return s
}

// ancestryIndex answers "is a an ancestor-or-self of marker b" among the
// commits of the union, by the marker pass of CCME §13.11.
//
// Every ancestry question planning asks has that shape, and its b is one of a
// few commits: a window boundary, a cancel barrier, the commit of a
// correction. Each marker takes one bit; the union is visited once, children
// before parents, a marker's own bit is set when it is visited and the visited
// commit's mask is ORed into each parent. c is an ancestor-or-self of x
// exactly when c is x or some child of c is, so when the pass ends bit x of
// mask(c) says whether c is in reach(x). One pass costs O((C+A)·ceil(m/64))
// where a walk per marker costs O(m·(C+A)).
//
// The index holds the union only, and that is enough. A window is closed
// under descendants, so every commit on a path from a marker down to a commit
// of the union is itself in the union: no path leaves it and comes back.
type ancestryIndex struct {
	parents [][]int32 // by rank, parents inside the union only
	order   []int32   // every rank, children before parents

	// reach is, per marker rank, its ancestors-or-self inside the union.
	reach map[int32]*commitSet
	// held counts the bits retained in reach, against ancestryBudget.
	held int
}

// ancestryBudget bounds the retained marker sets, in bits. A set is a bit per
// union commit, so the budget holds two thousand markers over a union of a
// million commits; a marker past it is simply not indexed, and its questions
// go to the Git implementation as they always did.
const ancestryBudget = 2 << 30

// ancestryBatch is how many markers one pass carries: eight words per commit.
const ancestryBatch = 512

// newAncestryIndex indexes n commits whose parents parentsOf lists by rank.
// It returns nil when the parent pointers do not describe a DAG, which no
// history does; the caller then has no index and asks Git.
func newAncestryIndex(n int, parentsOf func(rank int) []int32) *ancestryIndex {
	ai := &ancestryIndex{parents: make([][]int32, n), reach: make(map[int32]*commitSet)}
	children := make([]int32, n)
	for r := 0; r < n; r++ {
		ai.parents[r] = parentsOf(r)
		for _, p := range ai.parents[r] {
			children[p]++
		}
	}
	// Kahn's algorithm from the commits nothing in the union descends from.
	// Any topological order gives the same masks, so rank order among the
	// ready commits is only the cheapest one to produce.
	ai.order = make([]int32, 0, n)
	for r := 0; r < n; r++ {
		if children[r] == 0 {
			ai.order = append(ai.order, int32(r))
		}
	}
	for i := 0; i < len(ai.order); i++ {
		for _, p := range ai.parents[ai.order[i]] {
			if children[p]--; children[p] == 0 {
				ai.order = append(ai.order, p)
			}
		}
	}
	if len(ai.order) != n {
		return nil
	}
	return ai
}

// mark computes the ancestor set of every marker not yet indexed, a batch of
// them per pass over the union.
func (ai *ancestryIndex) mark(markers []int32) {
	var fresh []int32
	queued := make(map[int32]bool, len(markers))
	for _, m := range markers {
		if _, done := ai.reach[m]; !done && !queued[m] {
			queued[m] = true
			fresh = append(fresh, m)
		}
	}
	n := len(ai.parents)
	for len(fresh) > 0 && ai.held+n <= ancestryBudget {
		batch := fresh[:min(len(fresh), ancestryBatch, (ancestryBudget-ai.held)/max(n, 1))]
		fresh = fresh[len(batch):]
		ai.pass(batch)
	}
}

// pass is one marker pass: batch[j] owns bit j.
func (ai *ancestryIndex) pass(batch []int32) {
	n := len(ai.parents)
	words := (len(batch) + 63) / 64
	masks := make([]uint64, n*words)
	for j, m := range batch {
		masks[int(m)*words+j>>6] |= 1 << (uint(j) & 63)
	}
	for _, c := range ai.order {
		mask := masks[int(c)*words : int(c)*words+words]
		for _, p := range ai.parents[c] {
			into := masks[int(p)*words : int(p)*words+words]
			for w, bitsOfC := range mask {
				into[w] |= bitsOfC
			}
		}
	}
	// Transposed into one set per marker: a window is read by rank in order,
	// and a closure is handed around whole, so both want the marker's column.
	sets := make([]*commitSet, len(batch))
	for j := range batch {
		sets[j] = &commitSet{bits: make([]uint64, (n+63)/64)}
	}
	for r := 0; r < n; r++ {
		for w, word := range masks[r*words : r*words+words] {
			for ; word != 0; word &= word - 1 {
				s := sets[w<<6+bits.TrailingZeros64(word)]
				s.bits[r>>6] |= 1 << (uint(r) & 63)
				s.size++
			}
		}
	}
	for j, m := range batch {
		ai.reach[m] = sets[j]
		ai.held += n
	}
}

// ancestors is the ancestor-or-self set of an indexed marker, or nil.
func (ai *ancestryIndex) ancestors(marker int32) *commitSet {
	if ai == nil {
		return nil
	}
	return ai.reach[marker]
}
