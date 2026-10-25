// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"math/bits"
)

// commitSet is an immutable set of the union's commits. A chain's ancestry
// and pending windows are intervals; other histories use one bit per rank.
// A pending window is shared by packages released at the same boundary.
type commitSet struct {
	bits     []uint64
	size     int
	start    int
	end      int
	interval bool
}

func (s *commitSet) has(rank int) bool {
	if s != nil && s.interval {
		return rank >= s.start && rank < s.end
	}
	return s != nil && rank>>6 < len(s.bits) && s.bits[rank>>6]&(1<<(uint(rank)&63)) != 0
}

func (s *commitSet) len() int {
	if s == nil {
		return 0
	}
	return s.size
}

// each calls yield with every rank of the set, lowest first: for a pending
// window, its commits newest first.
func (s *commitSet) each(yield func(rank int)) {
	if s == nil {
		return
	}
	if s.interval {
		for rank := s.start; rank < s.end; rank++ {
			yield(rank)
		}
		return
	}
	for w, word := range s.bits {
		for ; word != 0; word &= word - 1 {
			yield(w<<6 + bits.TrailingZeros64(word))
		}
	}
}

// word is the set's w-th word, zero past its end.
func (s *commitSet) word(w int) uint64 {
	if s == nil || w < 0 {
		return 0
	}
	if s.interval {
		wordStart := w << 6
		if wordStart >= s.end || wordStart+64 <= s.start {
			return 0
		}
		lo := max(s.start-wordStart, 0)
		hi := min(s.end-wordStart, 64)
		belowHi := ^uint64(0)
		if hi < 64 {
			belowHi = 1<<uint(hi) - 1
		}
		return belowHi & (^uint64(0) << uint(lo))
	}
	if w >= len(s.bits) {
		return 0
	}
	return s.bits[w]
}

func commitInterval(start, end int) *commitSet {
	return &commitSet{start: start, end: end, size: end - start, interval: true}
}

// commitSetWithout is every one of n commits but the excluded ones. A chain
// suffix has a prefix complement; a general set is complemented word by word.
func commitSetWithout(n int, excluded *commitSet) *commitSet {
	if excluded == nil {
		return commitInterval(0, n)
	}
	if excluded.interval && excluded.end == n {
		return commitInterval(0, excluded.start)
	}
	s := &commitSet{bits: make([]uint64, (n+63)/64)}
	for w := range s.bits {
		s.bits[w] = ^excluded.word(w) & wordBelow(n, w)
		s.size += bits.OnesCount64(s.bits[w])
	}
	return s
}

// wordBelow is the bits of word w that stand for ranks below n.
func wordBelow(n, w int) uint64 {
	if rest := n - w<<6; rest < 64 {
		return 1<<uint(rest) - 1
	}
	return ^uint64(0)
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
// commits of the union, by a rank interval on a verified chain or the marker
// pass of CCME §13.11 on a general DAG.
//
// Every ancestry question planning asks has that shape, and its b is one of a
// few commits: a window boundary, a cancel barrier, the commit of a
// correction. On a general DAG each marker takes one bit; the union is visited
// once, children before parents. A marker's bit is set at its commit, then the
// visited commit's mask is ORed into each parent. c is an ancestor-or-self of x
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
	chain   bool      // rank r has parent r+1, except the oldest rank

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
	ai.chain = n > 0
	for rank, parents := range ai.parents {
		if rank == n-1 {
			ai.chain = ai.chain && len(parents) == 0
		} else {
			ai.chain = ai.chain && len(parents) == 1 && parents[0] == int32(rank+1)
		}
	}
	return ai
}

// mark computes the ancestor set of every marker not yet indexed. A chain
// stores intervals directly; a general DAG batches markers per union pass.
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
	if ai.chain {
		for _, marker := range fresh {
			ai.reach[marker] = commitInterval(int(marker), n)
		}
		return
	}
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
	// A block of 64 rows by 64 markers at a time, since enumerating every set
	// bit costs a step per (commit, marker) ancestor pair (CCME §13.11).
	sets := make([]*commitSet, len(batch))
	for j := range batch {
		sets[j] = &commitSet{bits: make([]uint64, (n+63)/64)}
	}
	var block [64]uint64
	for rows := 0; rows < n; rows += 64 {
		for w := 0; w < words; w++ {
			if !loadBlock(&block, masks, rows, w, words) {
				continue
			}
			transposeBlock(&block)
			for i, column := range block[:min(64, len(batch)-w<<6)] {
				s := sets[w<<6+i]
				s.bits[rows>>6] = column
				s.size += bits.OnesCount64(column)
			}
		}
	}
	for j, m := range batch {
		ai.reach[m] = sets[j]
		ai.held += n
	}
}

// loadBlock copies word w of the 64 rows from the given one into block,
// rows past the masks being zero, and reports whether any bit is set.
func loadBlock(block *[64]uint64, masks []uint64, rows, w, words int) bool {
	isSet := false
	for i := range block {
		block[i] = 0
		if at := (rows+i)*words + w; at < len(masks) {
			block[i] = masks[at]
		}
		isSet = isSet || block[i] != 0
	}
	return isSet
}

// transposeBlock transposes a 64×64 bit matrix in place: bit j of word i
// becomes bit i of word j. Six rounds swap ever smaller off-diagonal blocks.
func transposeBlock(block *[64]uint64) {
	mask := uint64(0x00000000ffffffff)
	for width := 32; width != 0; width, mask = width>>1, mask^mask<<(width>>1) {
		for i := 0; i < 64; i = (i + width + 1) &^ width {
			swap := (block[i]>>width ^ block[i+width]) & mask
			block[i] ^= swap << width
			block[i+width] ^= swap
		}
	}
}

// ancestors is the ancestor-or-self set of an indexed marker, or nil.
func (ai *ancestryIndex) ancestors(marker int32) *commitSet {
	if ai == nil {
		return nil
	}
	return ai.reach[marker]
}
