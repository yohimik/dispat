// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// BenchmarkControlWindows measures the control history's pending windows over
// a long control history: the one inventory indexed, then a window after each
// of 64 distinct control boundaries, which is what a fleet whose repositories
// released at different control commits asks for. The control history is read
// once per plan; what each window costs on top of it is what grows with the
// fleet.
func BenchmarkControlWindows(b *testing.B) {
	const (
		controlCommits = 20_000
		boundaries     = 64
	)
	history := make([]gitx.ControlHistoryCommit, controlCommits)
	for i := range history {
		history[i] = gitx.ControlHistoryCommit{SHA: fmt.Sprintf("%040x", controlCommits-i),
			Message: "chore: control", Gitlinks: map[string]gitx.GitlinkTransition{}}
		if i+1 < len(history) {
			history[i].Parents = []string{fmt.Sprintf("%040x", controlCommits-i-1)}
		}
	}
	control := &composedControlGit{fakeGit: newFakeGit(), control: history}
	histories := map[string]RepositoryHistory{"control": {Name: "control", Git: control, Control: true}}
	packages := []*model.Package{{Name: "pkg", Repository: "control"}}
	windows, iterations := 0, 0
	b.ReportAllocs()
	for b.Loop() {
		cp := &computation{ctx: context.Background(), controlRepo: "control", pkgs: packages,
			histories: histories, tags: map[string]gitx.Tags{}}
		if _, _, err := cp.controlCheckpoints(); err != nil {
			b.Fatal(err)
		}
		for k := range boundaries {
			// Boundaries spread over the newest tenth of the history, where
			// a fleet's recent releases sit.
			boundary := history[(k+1)*controlCommits/10/boundaries].SHA
			if len(cp.controlCommitsAfter(boundary)) == 0 {
				b.Fatal("an empty control window")
			}
			windows++
		}
		iterations++
	}
	b.StopTimer()
	b.ReportMetric(float64(windows)/float64(iterations), "windows/op")
}
