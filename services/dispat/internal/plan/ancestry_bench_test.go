// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"testing"
)

// BenchmarkMarkerPass measures one marker pass over a 50,000-commit chain,
// the shape whose ancestor sets are largest: a marker's set is every commit
// behind it, so with markers spread along the chain the rows hold about half
// a batch of bits each. 72 markers is a wide workspace's boundaries, 512 a
// full batch of cancel and correction commits.
func BenchmarkMarkerPass(b *testing.B) {
	const commits = 50_000
	index := newAncestryIndex(commits, func(rank int) []int32 {
		if rank+1 == commits {
			return nil
		}
		return []int32{int32(rank + 1)}
	})
	for _, count := range []int{72, 512} {
		b.Run(fmt.Sprintf("markers=%d", count), func(b *testing.B) {
			markers := make([]int32, count)
			for i := range markers {
				markers[i] = int32(i * commits / count)
			}
			b.ReportAllocs()
			for b.Loop() {
				index.reach, index.held = make(map[int32]*commitSet), 0
				index.mark(markers)
			}
		})
	}
}
