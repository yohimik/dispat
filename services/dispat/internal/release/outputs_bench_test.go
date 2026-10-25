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
