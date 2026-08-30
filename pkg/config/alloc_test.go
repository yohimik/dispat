//go:build !race

package config

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag. `go test` covers them; `go test -race`
// skips the file entirely.

import (
	"context"
	"testing"
)

// allocSink keeps the results live so the compiler cannot elide the work being
// measured.
var (
	allocTree     *Tree
	allocSettings map[string]any
	allocConfig   appConfig
	allocOv       Overrides
	allocString   string
)

// TestAllocationBudget pins the allocation counts the package documents. It is
// a regression gate, not a target: a change that reintroduces a second deep
// copy of the tree, a flat intermediate map in Settings, or a second sort of
// every decoded object will trip it.
//
// Budgets carry one allocation of headroom so a toolchain change does not fail
// the build for a rounding difference; a real regression is worth many.
func TestAllocationBudget(t *testing.T) {
	ctx := context.Background()

	small := benchLoader(map[string]string{"/bench/app.json": benchDoc("json", benchSmall)})
	files, chainPath := benchChain(8)
	chain := benchLoader(files)

	l := NewLoader(Options{})
	flat := &Tree{Root: benchTrees()["flat"]}
	hollow := &Tree{Root: benchTrees()["empty-objects"]}
	decodeSmall := benchDecodeSrc()["small"]
	decodeFull := benchDecodeSrc()["full"]

	binding := EnvBinding{
		Prefix:  "APP_",
		Keys:    []string{"name", "logLevel"},
		Environ: []string{"APP_NAME=n", "APP_LOGLEVEL=warn", "PATH=/usr/bin"},
	}

	cases := []struct {
		name   string
		budget float64
		run    func()
	}{
		{
			name:   "ReadTree/json/small",
			budget: 73, // measured 72: the parser's own, and one map per object
			run:    func() { allocTree, _ = small.ReadTree(ctx, "/bench/app.json") },
		},
		{
			name:   "ReadTree/refDepth8",
			budget: 233, // measured 232: nine files, each parsed and rebuilt
			run:    func() { allocTree, _ = chain.ReadTree(ctx, chainPath) },
		},
		{
			name:   "Settings/flat",
			budget: 6, // measured 5: the sorted keys, the path buffer, the output map
			run:    func() { allocSettings = flat.Settings(l, nil) },
		},
		{
			name:   "Settings/emptyObjects",
			budget: 6, // measured 5: a hundred pruned branches leave nothing behind
			run:    func() { allocSettings = hollow.Settings(l, nil) },
		},
		{
			name:   "Decode/small",
			budget: 22, // measured 21: the fields table and its closures
			run: func() {
				allocConfig = appConfig{}
				_ = DecodeObject(decodeSmall, "", appFields(&allocConfig))
			},
		},
		{
			name:   "Decode/full",
			budget: 514, // measured 513: every nested table and every filled slice
			run: func() {
				allocConfig = appConfig{}
				_ = DecodeObject(decodeFull, "", appFields(&allocConfig))
			},
		},
		{
			name:   "EnvBinding",
			budget: 9, // measured 8: the name table, the overrides, the derived names
			run:    func() { allocOv, _ = binding.Overrides(ctx) },
		},
		{
			name:   "Fold/alreadyLower",
			budget: 0, // the fast path returns the string that went in
			run:    func() { allocString = Fold("loglevel") },
		},
	}

	for _, tc := range cases {
		if got := testing.AllocsPerRun(100, tc.run); got > tc.budget {
			t.Errorf("%s: %.0f allocs/op, budget %.0f", tc.name, got, tc.budget)
		}
	}
}

// TestSettingsDoesNotAllocatePerLeaf is the copy-elimination gate stated as a
// shape rather than as a number: rendering used to build a flat map of every
// leaf's delimited path and then the nested result from it, which is one
// allocation per leaf twice over. One walk writes the leaves straight into the
// result, so the count is what the output map costs to grow and nothing else —
// a hundred leaves and ten thousand are within a few allocations of each other.
func TestSettingsDoesNotAllocatePerLeaf(t *testing.T) {
	l := NewLoader(Options{})
	count := func(n int) float64 {
		root := make(map[string]any, n)
		for i := 0; i < n; i++ {
			root[benchKey(i)] = "value"
		}
		tree := &Tree{Root: root}
		return testing.AllocsPerRun(50, func() { allocSettings = tree.Settings(l, nil) })
	}

	small, large := count(benchSmall), count(benchLarge)
	if large > small+8 {
		t.Errorf("%.0f allocs for %d leaves against %.0f for %d: the count grows with the leaves",
			large, benchLarge, small, benchSmall)
	}
}
