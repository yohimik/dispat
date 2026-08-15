//go:build !race

package ccme

import (
	"strings"
	"testing"
)

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag. `go test` covers them; `go test -race`
// skips the file entirely.

// allocSink keeps the parser's output live so the compiler cannot elide the
// work being measured.
var allocSink *Result

// TestAllocationBudget pins the allocation counts the package documents. It is
// a regression gate, not a target: a refactor that quietly reintroduces a copy
// of the message, a per-unit allocation or a temporary map will trip it.
//
// Budgets carry one allocation of headroom so a toolchain change does not fail
// the build for a rounding difference; a real regression is worth several.
func TestAllocationBudget(t *testing.T) {
	p := DefaultParser()

	cases := []struct {
		name   string
		budget float64
		run    func()
	}{
		{
			name:   "ParseSubject",
			budget: 3, // measured 2: the packed buffer and the scope slice
			run:    func() { allocSink, _ = p.ParseSubject(benchSubject) },
		},
		{
			name:   "Parse/simple",
			budget: 6, // measured 5: normalisation, lines, sources, scopes, buffer
			run:    func() { allocSink, _ = p.Parse(benchSimple) },
		},
		{
			name:   "Parse/directives",
			budget: 17, // measured 16 (go1.26, two-axis grammar with a footer)
			run:    func() { allocSink, _ = p.Parse(benchDirectives) },
		},
		{
			name:   "Parse/multiUnit",
			budget: 13, // measured 12
			run:    func() { allocSink, _ = p.Parse(benchMultiUnit) },
		},
	}

	for _, tc := range cases {
		if got := testing.AllocsPerRun(200, tc.run); got > tc.budget {
			t.Errorf("%s: %.0f allocs/op, budget %.0f", tc.name, got, tc.budget)
		}
	}
}

// TestAllocationByteBudget pins bytes/op alongside the counts: a change that
// keeps the allocation count but grows what each allocation holds (a copied
// message, a fatter buffer) passes the count gate and trips this one. The
// budgets are measured values rounded up ~30% so a toolchain change does not
// fail the build for a size-class difference.
func TestAllocationByteBudget(t *testing.T) {
	p := DefaultParser()

	cases := []struct {
		name   string
		budget int64 // bytes/op
		run    func()
	}{
		{"ParseSubject", 1000, func() { allocSink, _ = p.ParseSubject(benchSubject) }}, // measured 774
		{"Parse/simple", 1500, func() { allocSink, _ = p.Parse(benchSimple) }},         // measured 1142
		{"Parse/directives", 3200, func() { allocSink, _ = p.Parse(benchDirectives) }}, // measured 2454
		{"Parse/multiUnit", 5000, func() { allocSink, _ = p.Parse(benchMultiUnit) }},   // measured 3846
	}

	for _, tc := range cases {
		r := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tc.run()
			}
		})
		if got := r.AllocedBytesPerOp(); got > tc.budget {
			t.Errorf("%s: %d B/op, budget %d", tc.name, got, tc.budget)
		}
	}
}

// TestNormalizeFastPathIsAllocationFree is the load-bearing claim behind the
// throughput numbers: a message that arrives already normalised is not copied.
func TestNormalizeFastPathIsAllocationFree(t *testing.T) {
	msg := strings.TrimRight(benchSimple, "\n")
	if needsNormalizing(msg) {
		t.Fatalf("the fixture is not already normalised: %q", msg)
	}

	var sink string
	got := testing.AllocsPerRun(200, func() { sink = Normalize(msg) })
	if got != 0 {
		t.Errorf("Normalize on an already-normalised message: %.0f allocs, want 0", got)
	}
	if sink != msg {
		t.Errorf("Normalize altered an already-normalised message")
	}
}

// TestParseDoesNotScaleAllocationsWithBodySize is the substring contract stated
// as a measurement: a body a hundred times longer must not cost more
// allocations, because it is never copied.
func TestParseDoesNotScaleAllocationsWithBodySize(t *testing.T) {
	p := DefaultParser()

	small := "feat(core): a\n\n" + strings.Repeat("body line\n", 2)
	large := "feat(core): a\n\n" + strings.Repeat("body line\n", 200)

	smallAllocs := testing.AllocsPerRun(200, func() { allocSink, _ = p.Parse(small) })
	largeAllocs := testing.AllocsPerRun(200, func() { allocSink, _ = p.Parse(large) })

	// The larger message needs a bigger line slice, but that is one allocation
	// either way; nothing should scale with the number of body lines.
	if largeAllocs > smallAllocs+1 {
		t.Errorf("a 100x larger body cost %.0f allocs vs %.0f: the body is being copied",
			largeAllocs, smallAllocs)
	}
}
