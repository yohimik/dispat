// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// §19.3 blocks a dependent only when EVERY admitted cause of its release comes
// from a package that failed or was blocked, and §19.5 says what a dependent
// that proceeds may publish: each provider's version as published, never the
// planned one its version stage wrote before the provider died.

// TestAdmissionBlocksOnlyWhenEveryCauseComesFromTheFailure is the cause table.
// The package declares two providers, `core` fails, and each row states one
// other cause the release has, or, in the last rows, states that it has none.
func TestAdmissionBlocksOnlyWhenEveryCauseComesFromTheFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		build     func(*plan.Release)
		results   map[string]*Result
		isSkipped bool
		why       string
	}{
		"a fresh direct bump": {
			build: func(rel *plan.Release) {
				rel.OwnBump = ccme.BumpMinor
				rel.Units = []*ccme.Unit{{Header: ccme.Header{Type: "feat"}, Bump: ccme.BumpMinor, Valid: true}}
				rel.FreshUnits = rel.Units
			},
			isSkipped: false,
			why:       "work of its own is not invalidated by a provider that failed",
		},
		"a channel change": {
			build:     func(rel *plan.Release) { rel.Channel, rel.BaselineChannel = "beta", "stable" },
			isSkipped: false,
			why:       "the package is moving between channels for its own reasons",
		},
		"a provider that published in this run": {
			build: func(rel *plan.Release) {
				rel.Updates = append(rel.Updates, plan.ProviderUpdate{Name: "utils",
					From: ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}})
			},
			results:   map[string]*Result{"utils": {Status: StatusPublished}},
			isSkipped: false,
			why:       "a version it picks up is as real a cause as its own work",
		},
		"a provider that published in an earlier run": {
			build: func(rel *plan.Release) {
				// The catch-up shape (§13.7a): the provider is not in this run
				// at all, so there is no result for it, and the version this
				// release picks up is the one an earlier run published.
				rel.Updates = append(rel.Updates, plan.ProviderUpdate{Name: "utils",
					From: ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}})
			},
			isSkipped: false,
			why:       "discharging an earlier run's publication is a cause of its own",
		},
		"every cause is the failure": {
			build: func(rel *plan.Release) {
				rel.Updates = append(rel.Updates, plan.ProviderUpdate{Name: "core",
					From: ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}})
			},
			isSkipped: true,
			why:       "releasing would record a provider movement that never published",
		},
		"no cause at all": {
			build:     func(*plan.Release) {},
			isSkipped: true,
			why:       "a package whose only reason was the propagation cannot proceed without it",
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := mkAdmissionPlan(models.StageWaitBuild)
			tc.build(p.Releases["app"])
			results := map[string]*Result{"app": {Name: "app"}, "core": {Status: StatusFailed}}
			for provider, res := range tc.results {
				results[provider] = res
			}

			skip, blocker := shouldSkip("app", p, results, nil)
			assert.Equal(t, tc.isSkipped, skip, tc.why)
			if tc.isSkipped {
				assert.Equal(t, "core", blocker)
			}
		})
	}
}

// TestAdmissionKeepsABlockingRelationAboveEveryCause: `isBlocking: true` is
// dispat's stricter opt-in and outranks the cause rule entirely, because under
// it the consumer's build took the provider's publish as its input and no work
// of the consumer's own substitutes for an input that never existed.
func TestAdmissionKeepsABlockingRelationAboveEveryCause(t *testing.T) {
	p := mkAdmissionPlan(models.StageWaitPublish)
	app := p.Releases["app"]
	app.OwnBump = ccme.BumpMinor
	app.Units = []*ccme.Unit{{Header: ccme.Header{Type: "feat"}, Bump: ccme.BumpMinor, Valid: true}}
	app.FreshUnits = app.Units

	skip, blocker := shouldSkip("app", p,
		map[string]*Result{"app": {Name: "app"}, "core": {Status: StatusFailed}}, nil)
	require.True(t, skip)
	assert.Equal(t, "core", blocker)
}

// TestAdmissionFindsTheProvidersThatDiedAfterTheVersionStage is the §19.5
// question: which providers' planned versions are in the manifests and will
// never be published. Only a stage that actually reconciled counts, and only a
// provider that has since died.
func TestAdmissionFindsTheProvidersThatDiedAfterTheVersionStage(t *testing.T) {
	p := mkAdmissionPlan(models.StageWaitBuild)
	r := &run{plan: p, results: map[string]*Result{
		"app":   {Name: "app"},
		"core":  {Status: StatusFailed},
		"utils": {Status: StatusPublished},
	},
		reconciledProviders: map[string][]string{},
		builtPackages:       map[string]bool{},
	}

	assert.Empty(t, r.deadPickups("app"), "a stage that reconciled nothing leaves nothing to undo")

	r.reconciledProviders["app"] = []string{"utils"}
	assert.Empty(t, r.deadPickups("app"), "the provider it reconciled to published")

	r.reconciledProviders["app"] = []string{"utils", "core"}
	assert.Equal(t, []string{"core"}, r.deadPickups("app"))

	r.results["core"] = &Result{Status: StatusPublished, RecordBlocked: true}
	assert.Equal(t, []string{"core"}, r.deadPickups("app"),
		"a publication whose records are blocked is no version to name either")
}

// TestAdmissionRecordsWhatEachStageDid pins the two moments the §19.5 decision
// reads: which providers were still alive when the manifests were written, and
// whether a build command has since run over them. A stage with nothing to run
// records neither.
func TestAdmissionRecordsWhatEachStageDid(t *testing.T) {
	p := mkAdmissionPlan(models.StageWaitBuild)
	r := &run{plan: p, results: map[string]*Result{"app": {Name: "app"}},
		reconciledProviders: map[string][]string{}, builtPackages: map[string]bool{}}
	ctxFor := func(kind taskKind) *taskCtx {
		return &taskCtx{run: r, t: task{"app", kind}, rel: p.Releases["app"], log: zerolog.Nop(),
			updates: []providerUpdate{{Package: "core"}, {Package: "utils"}}}
	}

	ctxFor(taskVersion).recordReconciliation(stage{})
	assert.Empty(t, r.reconciledProviders["app"], "a version stage with neither script nor native step wrote nothing")

	ctxFor(taskVersion).recordReconciliation(stage{commands: []string{"version"}})
	assert.Equal(t, []string{"core", "utils"}, r.reconciledProviders["app"])

	ctxFor(taskBuild).recordReconciliation(stage{})
	assert.False(t, r.builtPackages["app"], "a package with no build command produced nothing")

	ctxFor(taskBuild).recordReconciliation(stage{commands: []string{"build"}})
	assert.True(t, r.builtPackages["app"])
}

// TestAdmissionReRunsTheVersionScriptsAgainstTheLiveProviders: the remedy for
// a space that reconciles through scripts is to run them again with the dead
// provider already out of DISPAT_UPDATED_*, so the script can write back what
// it wrote optimistically.
func TestAdmissionReRunsTheVersionScriptsAgainstTheLiveProviders(t *testing.T) {
	p := mkAdmissionPlan(models.StageWaitBuild)
	p.Releases["app"].Pkg.Space.VersionScript = []string{"version"}
	runner := &fakeRunner{}
	r := &run{Executor: &Executor{Runner: runner, Log: zerolog.Nop()}, plan: p,
		results:             map[string]*Result{"app": {Name: "app"}, "core": {Status: StatusFailed}},
		reconciledProviders: map[string][]string{}, builtPackages: map[string]bool{}}
	tc := &taskCtx{run: r, t: task{"app", taskPublish}, rel: p.Releases["app"], log: zerolog.Nop(),
		deadPickups: []string{"core"},
		updates:     []providerUpdate{{Package: "utils", NewVersion: "1.1.0"}}}

	require.NoError(t, tc.reconcileToPublished(context.Background()))

	joined := strings.Join(runner.envPrefix(t, "version"), "\n")
	assert.Contains(t, joined, "DISPAT_STAGE=version", "the frame is the version stage's, run again")
	assert.Contains(t, joined, "DISPAT_UPDATED_PACKAGES=UTILS")
	assert.NotContains(t, joined, "DISPAT_UPDATED_CORE",
		"the provider that died must not be in the set the scripts reconcile to")
}

// mkAdmissionPlan is one consumer with two declared providers and no cause of
// its own, which is the state every row of the cause table modifies. The
// packages carry no scripts: what is being decided here is admission, and a
// fixture with scripts would be asserting about stages nothing ran.
func mkAdmissionPlan(wait models.StageWait) *plan.Plan {
	space := &model.Space{Name: "libs",
		ProviderRelation: model.NewStageRelation(&models.StageRelation{Build: wait})}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{"app": {"core", "utils"}},
		Order:     []string{"core", "utils", "app"},
	}
	for _, name := range p.Order {
		p.Releases[name] = &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: name, Space: space},
			Current: ccme.Version{Major: 1},
			Next:    ccme.Version{Major: 1, Patch: 1},
			Bump:    ccme.BumpPatch,
			NewWork: true,
		}
	}
	return p
}
