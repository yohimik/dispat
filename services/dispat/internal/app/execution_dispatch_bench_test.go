package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

// BenchmarkResolveOutputProviders asks every package of a 2,000-package plan
// for its output providers once, as a distributed run's tasks do. The plan is
// chains of ten, so each closure is small next to the plan.
func BenchmarkResolveOutputProviders(b *testing.B) {
	const packages = 2000
	order := make([]string, packages)
	providers := make(map[string][]string, packages)
	outputs := make(map[string][]string, packages)
	for i := range packages {
		order[i] = fmt.Sprintf("pkg%04d", i)
		outputs[order[i]] = []string{"dist"}
		if i%10 != 0 {
			providers[order[i]] = []string{order[i-1]}
		}
	}
	pl := outputPlan(order, providers, map[string]bool{}, outputs)
	app := &App{root: filepath.FromSlash("/work"), log: zerolog.Nop()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inputs := app.resolveOutputProviders(pl)
		for _, name := range order {
			inputs(name)
		}
	}
}
