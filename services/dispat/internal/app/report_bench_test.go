package app

import (
	"io"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkPrintErrorDiagnostics measures the allocation cost of reporting a
// history with many invalid commit units, including the final count summary.
func BenchmarkPrintErrorDiagnostics(b *testing.B) {
	a := New(b.TempDir(), &config.File{}, zerolog.New(io.Discard))
	pl := &plan.Plan{Diagnostics: make([]plan.Diagnostic, 100)}
	for i := range pl.Diagnostics {
		pl.Diagnostics[i] = plan.Diagnostic{
			Code: "E130", Level: plan.LevelError, Pkg: "legacy",
			Commit:  "0123456789abcdef0123456789abcdef01234567",
			Message: "scope names no package at HEAD",
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a.printDiagnostics(pl)
	}
	b.ReportMetric(float64(len(pl.Diagnostics)), "diagnostics/op")
}
