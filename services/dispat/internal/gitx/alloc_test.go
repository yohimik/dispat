//go:build !race

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag.

import "testing"

// TestAllocationBudget pins the allocation counts of the parsers that read a
// whole git answer. It is a regression gate, not a target: each budget is the
// measured count with a little headroom.
func TestAllocationBudget(t *testing.T) {
	const controlCommits = 1_000
	controlHistory := syntheticControlHistory(controlCommits)

	cases := []struct {
		name   string
		budget float64
		run    func()
	}{
		{
			// Measured 9 per commit: the header copy, the parent list, the
			// file list, three paths, the gitlink map with its group and the
			// copy of the gitlink's two ids.
			name:   "parseControlGitlinkHistory/1000",
			budget: 9*controlCommits + 10,
			run: func() {
				if _, err := parseControlGitlinkHistory(controlHistory); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(5, tc.run)
			t.Logf("%s: %.0f allocs (budget %.0f)", tc.name, allocs, tc.budget)
			if allocs > tc.budget {
				t.Fatalf("%s allocated %.0f times, budget %.0f", tc.name, allocs, tc.budget)
			}
		})
	}
}
