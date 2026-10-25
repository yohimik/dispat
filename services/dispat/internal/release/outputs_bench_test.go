// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkMergeOutputs folds exports onto releases the way `dispat run`
// carries them down a provider chain: every package takes its provider's
// accumulated outputs and then exports k names of its own, so the list a merge
// scans grows with the chain. The one-package case is the common merge of a
// single stage's few exports onto a release that has few.
func BenchmarkMergeOutputs(b *testing.B) {
	for _, shape := range []struct{ packages, exports int }{{1, 5}, {300, 5}, {300, 20}} {
		b.Run(fmt.Sprintf("chain-%d-exports-%d", shape.packages, shape.exports), func(b *testing.B) {
			own := mkOwnExports(shape.packages, shape.exports)
			b.ReportAllocs()
			for b.Loop() {
				mergeDownChain(own)
			}
		})
	}
}

// BenchmarkMergeOutputsCrossover runs both ways of merging, whatever the
// limit picks, onto lists of 100 and 1000 outputs, so the measurement that
// sets outputScanLimit can be repeated. Half of each merge's exports re-export
// a name the list already carries and half are new.
func BenchmarkMergeOutputsCrossover(b *testing.B) {
	for _, outputs := range []int{100, 1000} {
		for _, exports := range []int{4, 6, 8, 12, 16, 24} {
			for _, merge := range []struct {
				name string
				fold func(*plan.Release, []plan.Output)
			}{{"scan", mergeOutputsByScan}, {"index", mergeOutputsByIndex}} {
				name := fmt.Sprintf("outputs-%d-exports-%d-%s", outputs, exports, merge.name)
				b.Run(name, func(b *testing.B) {
					current, incoming := mkCrossoverOutputs(outputs, exports)
					rel := &plan.Release{}
					b.ReportAllocs()
					for b.Loop() {
						rel.Outputs = current
						merge.fold(rel, incoming)
					}
				})
			}
		}
	}
}

// mkCrossoverOutputs answers a list of n outputs with room for k more, and k
// exports of which every second re-exports a name spread across the list.
func mkCrossoverOutputs(n, k int) (current, incoming []plan.Output) {
	current = make([]plan.Output, n, n+k)
	for i := range current {
		current[i] = plan.Output{Name: fmt.Sprintf("OUT%04d", i), Value: "v", Source: "p:build"}
	}
	for i := range k {
		name := fmt.Sprintf("NEW%04d", i)
		if i%2 == 0 {
			name = current[(i*n)/k].Name
		}
		incoming = append(incoming, plan.Output{Name: name, Value: "w", Source: "c:build"})
	}
	return current, incoming
}

// mergeDownChain merges each package's provider's outputs and then its own
// exports onto a fresh release per package, and answers the last release.
func mergeDownChain(own [][]plan.Output) *plan.Release {
	var provider *plan.Release
	for _, exports := range own {
		rel := &plan.Release{Pkg: &model.Package{}}
		if provider != nil {
			MergeOutputs(rel, provider.Outputs)
		}
		MergeOutputs(rel, exports)
		provider = rel
	}
	return provider
}

// mkOwnExports names k distinct exports per package.
func mkOwnExports(packages, exports int) [][]plan.Output {
	own := make([][]plan.Output, packages)
	for pkg := range own {
		for export := range exports {
			own[pkg] = append(own[pkg], plan.Output{Name: fmt.Sprintf("P%d_OUT%d", pkg, export),
				Value: "v", Source: fmt.Sprintf("p%d:run", pkg)})
		}
	}
	return own
}
