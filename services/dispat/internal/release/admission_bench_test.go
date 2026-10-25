// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkAdmission measures one task's admission, which every task of every
// run pays: a consumer reading its providers through unreleased packages that
// all carry the same changed providers, every one of them published, so the
// skip cascade has nothing to act on. The after-a-death shape is the same
// admission once a package the consumer does not reach has failed.
func BenchmarkAdmission(b *testing.B) {
	for _, shape := range []struct {
		name       string
		gaps       int
		isAnyDying bool
	}{
		{"gaps-1", 1, false},
		{"gaps-3", 3, false},
		{"gaps-8", 8, false},
		{"gaps-8-after-a-death", 8, true},
	} {
		b.Run(shape.name, func(b *testing.B) {
			p, changed := mkOverlappingGapsPlan(shape.gaps, 200)
			r := newAdmissionRun(p, changed)
			if shape.isAnyDying {
				elsewhere := &taskCtx{run: r, t: task{"elsewhere", taskBuild, ""}}
				elsewhere.settleFailure(r.results["elsewhere"], errors.New("build failed"), false)
			}
			tc := &taskCtx{run: r, t: task{"app", taskBuild, ""}, rel: p.Releases["app"]}
			res := r.results["app"]
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				res.Status = StatusPending
				r.settleAdmission(ctx, tc, res)
			}
		})
	}
}

// mkOverlappingGapsPlan is a releasing `app` whose providers are k unreleased
// packages, each over the same m releasing providers, beside an unrelated
// releasing package `elsewhere`.
func mkOverlappingGapsPlan(k, m int) (*plan.Plan, map[string]bool) {
	relations := map[string]models.StageWait{"app": models.StageWaitBuild, "elsewhere": models.StageWaitBuild}
	providers := map[string][]string{}
	releasing := []string{"app", "elsewhere"}
	for i := range m {
		provider := fmt.Sprintf("p%04d", i)
		relations[provider] = models.StageWaitBuild
		releasing = append(releasing, provider)
	}
	for i := range k {
		gap := fmt.Sprintf("g%02d", i)
		relations[gap] = models.StageWaitBuild
		providers["app"] = append(providers["app"], gap)
		providers[gap] = releasing[2:]
	}
	return mkReachPlan(relations, providers, releasing...)
}

// newAdmissionRun is the run Executor.Run builds for the plan, with every
// releasing package but `app` published.
func newAdmissionRun(p *plan.Plan, changed map[string]bool) *run {
	e := &Executor{Log: zerolog.Nop()}
	_, publishReach := e.buildTaskGraph(p, changed, newStagePlans(p, changed))
	results := make(map[string]*Result, len(changed))
	for name := range changed {
		results[name] = &Result{Name: name, Status: StatusPublished}
	}
	results["app"].Status = StatusPending
	results["elsewhere"].Status = StatusPending
	return &run{Executor: e, plan: p, results: results, publishReach: publishReach,
		reachedProviders: map[string][]string{}, started: map[string]time.Time{},
		reconciledProviders: map[string][]string{}, builtPackages: map[string]bool{}}
}
