// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package graph

import (
	"fmt"
	"testing"
)

// BenchmarkTopoSort sorts a workspace-sized package graph, each package a
// consumer of the three before it: the planner's use of the scheduler, which
// a scheduler change must leave at its cost.
func BenchmarkTopoSort(b *testing.B) {
	g := New()
	const packages = 1000
	for i := range packages {
		g.AddNode(fmt.Sprintf("p%04d", i))
		for back := max(0, i-3); back < i; back++ {
			if err := g.AddEdge(fmt.Sprintf("p%04d", back), fmt.Sprintf("p%04d", i)); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := g.TopoSort(); err != nil {
			b.Fatal(err)
		}
	}
}
