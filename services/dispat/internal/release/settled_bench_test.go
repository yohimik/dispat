// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"testing"
)

// BenchmarkSettledEnv renders the settled environment of every package of a
// closing phase that re-runs syncLock for all of them, which is how the
// closing phase asks for it: one package after another over one settled run.
func BenchmarkSettledEnv(b *testing.B) {
	const packages = 300
	names := make([]string, packages)
	results := make(map[string]*Result, packages)
	for i := range names {
		names[i] = fmt.Sprintf("p%04d", i)
		results[names[i]] = &Result{Name: names[i], Status: StatusPublished}
	}
	p := mkPlan(planSpec{Names: names})
	b.ReportAllocs()
	for b.Loop() {
		settled := NewSettledEnvs(p, results)
		for _, name := range names {
			settled.Render(name, "syncLock")
		}
	}
}
