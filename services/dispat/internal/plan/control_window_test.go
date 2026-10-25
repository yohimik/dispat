// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// TestControlCommitsAfterIsBoundaryDotDotHead checks the indexed control window
// against its definition, the inventory minus the boundary's ancestors-or-self
// found by walking parent ids, over random control histories with merges, a
// parent the inventory does not hold, and boundaries inside it, outside it and
// empty. One index serves every window.
func TestControlCommitsAfterIsBoundaryDotDotHead(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 40; round++ {
		n := 2 + rng.Intn(60)
		history := make([]gitx.ControlHistoryCommit, n) // newest first
		for i := n - 1; i >= 0; i-- {
			history[i] = gitx.ControlHistoryCommit{SHA: fmt.Sprintf("c%03d", i), Message: fmt.Sprintf("m%d", i)}
			if i == n-1 {
				history[i].Parents = []string{"shallow"} // outside the inventory
				continue
			}
			history[i].Parents = []string{history[i+1+rng.Intn(n-1-i)].SHA}
			if rng.Intn(4) == 0 {
				history[i].Parents = append(history[i].Parents, history[i+1+rng.Intn(n-1-i)].SHA)
			}
		}
		cp := &computation{controlHistory: history}
		for _, boundary := range []string{"", "absent", history[rng.Intn(n)].SHA, history[rng.Intn(n)].SHA} {
			parents := make(map[string][]string, n)
			for _, c := range history {
				parents[c.SHA] = c.Parents
			}
			excluded := make(map[string]bool)
			for queue := []string{boundary}; boundary != "" && len(queue) > 0; {
				c := queue[len(queue)-1]
				queue = queue[:len(queue)-1]
				if !excluded[c] {
					excluded[c] = true
					queue = append(queue, parents[c]...)
				}
			}
			var want []string
			for _, c := range history {
				if !excluded[c.SHA] {
					want = append(want, c.SHA)
				}
			}
			var got []string
			for _, c := range controlCommitsAfter(cp, boundary) {
				got = append(got, c.SHA)
			}
			require.Equalf(t, want, got, "round %d boundary %q", round, boundary)
		}
	}
}

// controlCommitsAfter is the control window after a boundary as the commits
// it holds, in listing order.
func controlCommitsAfter(cp *computation, boundary string) []gitx.Commit {
	listing, excluded := cp.controlWindow(boundary)
	var window []gitx.Commit
	for pos, commit := range listing {
		if !excluded.has(pos) {
			window = append(window, commit)
		}
	}
	return window
}
