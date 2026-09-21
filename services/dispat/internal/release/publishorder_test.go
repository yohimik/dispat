// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The publication order and the skip cascade, through a package this run does
// not release (§19.2, §19.3, vector 80a).
//
// The shape is always `app -> ui -> core` with `ui` carrying nothing, which is
// the commonest shape an incremental release has: `app` and `core` name no
// edge between them inside the plan, and the only thing that can order them is
// the graph they both sit in. What is claimed here is that a relation cannot
// declare that order away, because `app` resolves `core` through `ui` at
// install time whatever either build read.

// mkPublishOrderPlan builds that shape as a plan a run can execute: each
// package in its own space, because a relation belongs to the provider side
// and every hop here states its own, and `ui` present in the graph with
// nothing to release. toUI is the relation of the `app -> ui` hop and toCore
// the one of `ui -> core`.
func mkPublishOrderPlan(toUI, toCore models.StageWait) *plan.Plan {
	spaceOf := func(name string, wait models.StageWait) *model.Space {
		return &model.Space{Name: name,
			ProviderRelation: model.NewStageRelation(&models.StageRelation{Build: wait}),
			BuildScript:      []string{"build"}, PublishScript: []string{"publish"}}
	}
	p := &plan.Plan{
		Releases:  map[string]*plan.Release{},
		Providers: map[string][]string{"app": {"ui"}, "ui": {"core"}},
		Order:     []string{"core", "ui", "app"},
	}
	for name, wait := range map[string]models.StageWait{
		"app": models.StageWaitBuild, "ui": toUI, "core": toCore,
	} {
		p.Releases[name] = &plan.Release{
			Pkg:     &model.Package{Name: name, Dir: name, Space: spaceOf(name, wait)},
			Current: ccme.Version{Major: 1},
		}
	}
	// Only the two ends have work. `ui` keeps its relation and its place in the
	// graph and stays out of the plan, which is exactly what makes the two ends
	// mutually unordered until the graph is asked.
	for _, name := range []string{"app", "core"} {
		rel := p.Releases[name]
		units := []*ccme.Unit{{Header: ccme.Header{Type: "fix", Description: "own change"},
			Bump: ccme.BumpPatch, Valid: true}}
		rel.OwnBump, rel.Bump, rel.NewWork = ccme.BumpPatch, ccme.BumpPatch, true
		rel.Units, rel.FreshUnits = units, units
		rel.Next = rel.Current.Bumped(rel.Bump)
	}
	return p
}

// TestPublishOrderReachesThroughAPackageThatIsNotInThePlan: the walk answers
// the same under every relation, which is the whole difference between the two
// orders. A `none` hop ends a build path because nothing the provider builds
// is read beyond it; it ends no publication path, because §19.2a puts the
// provider's publication before its consumer's under all three relations.
func TestPublishOrderReachesThroughAPackageThatIsNotInThePlan(t *testing.T) {
	for name, c := range map[string]struct {
		toUI, toCore models.StageWait
	}{
		"a readable chain":               {models.StageWaitBuild, models.StageWaitBuild},
		"a none hop at the gap":          {models.StageWaitNone, models.StageWaitBuild},
		"a none hop behind the gap":      {models.StageWaitNone, models.StageWaitNone},
		"a publish hop is a hop as well": {models.StageWaitPublish, models.StageWaitPublish},
	} {
		t.Run(name, func(t *testing.T) {
			p, changed := mkReachPlan(
				map[string]models.StageWait{"app": models.StageWaitBuild, "ui": c.toUI, "core": c.toCore},
				map[string][]string{"app": {"ui"}, "ui": {"core"}},
				"app", "core")
			assert.Equal(t, []string{"core"}, newPublishReach(p, changed).Indirect("app"),
				"no relation declares the publication order away")
		})
	}
}

// TestPublishOrderConvergesThroughTwoUnreleasingPackages: a diamond whose two
// arms are both packages with nothing to release. The far providers are
// ordered once however many routes arrive at them, the answer is sorted so the
// derived edges enter the scheduler the same way on every run (§17.2), and a
// changed provider the consumer names itself is left to the loop that reads
// its own relation rather than being stated twice.
func TestPublishOrderConvergesThroughTwoUnreleasingPackages(t *testing.T) {
	p, changed := mkReachPlan(
		map[string]models.StageWait{
			"app": models.StageWaitBuild, "left": models.StageWaitNone, "right": models.StageWaitNone,
			"core": models.StageWaitNone, "util": models.StageWaitBuild, "direct": models.StageWaitBuild,
		},
		map[string][]string{
			"app":   {"left", "right", "direct"},
			"left":  {"core"},
			"right": {"util", "core"},
		},
		"app", "core", "util", "direct")

	reach := newPublishReach(p, changed)
	assert.Equal(t, []string{"core", "util"}, reach.Indirect("app"),
		"one edge per far provider, sorted, and the direct provider is not restated")
	assert.Empty(t, reach.Indirect("core"), "a package with no providers reaches nothing")

	// A second reading answers from the memo rather than walking again.
	assert.Equal(t, []string{"core", "util"}, reach.Indirect("app"))
}

// TestPublishOrderHoldsThroughAPackageThatIsNotInThePlan runs the shape rather
// than indexing it. The build budget is one, so the scheduler hands the two
// builds out one at a time and the recorded order is evidence of an edge
// rather than of a timer: where no build edge holds `app` back it builds
// first, finishes while `core` is still building, and its publication has to
// wait for `core`'s all the same.
func TestPublishOrderHoldsThroughAPackageThatIsNotInThePlan(t *testing.T) {
	for name, c := range map[string]struct {
		toUI, toCore   models.StageWait
		isBuildOrdered bool
	}{
		"a readable chain orders both stages": {
			models.StageWaitBuild, models.StageWaitBuild, true},
		"a none hop at the gap frees the build alone": {
			models.StageWaitNone, models.StageWaitBuild, false},
		"a none hop behind the gap frees it too": {
			models.StageWaitBuild, models.StageWaitNone, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := &fakeRunner{delay: 20 * time.Millisecond}
			res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 1, Publish: 4}).
				Run(context.Background(), mkPublishOrderPlan(c.toUI, c.toCore))

			require.Len(t, res, 2, "only the two ends are in the plan")
			for _, name := range []string{"app", "core"} {
				require.Equal(t, StatusPublished, res[name].Status, "%s: %v", name, res[name].Err)
			}
			assert.Less(t, r.indexOf("publish core"), r.indexOf("publish app"),
				"the provider publishes first, reached through a package that is not releasing")
			if c.isBuildOrdered {
				assert.Less(t, r.indexOf("build core"), r.indexOf("build app"))
				return
			}
			assert.Less(t, r.indexOf("build app"), r.indexOf("build core"),
				"a none hop ends the build path, so nothing held the consumer's build back")
		})
	}
}

// TestPublishOrderBlocksAConsumerBehindAReachedProvider: the closure §19.3
// blocks over is the same one §19.2 orders over, so a provider the consumer
// never names is treated exactly as one it does. The relation is read from the
// provider that failed, because a relation is what a space says about the
// consumers of its packages wherever in the graph they sit.
func TestPublishOrderBlocksAConsumerBehindAReachedProvider(t *testing.T) {
	for name, c := range map[string]struct {
		relation     models.StageWait
		result       *Result
		hasOwnReason bool
		isSkipped    bool
	}{
		"a failed provider skips a consumer with nothing of its own": {
			models.StageWaitBuild, &Result{Status: StatusFailed}, false, true},
		"a build relation leaves the consumer's own reason standing": {
			models.StageWaitBuild, &Result{Status: StatusFailed}, true, false},
		"a blocking relation outranks that reason": {
			models.StageWaitNone, &Result{Status: StatusFailed}, true, true},
		"a skipped provider blocks as a failed one does": {
			models.StageWaitBuild, &Result{Status: StatusSkipped}, false, true},
		"incomplete records outrank every relation": {
			models.StageWaitBuild, &Result{Status: StatusPublished, RecordBlocked: true}, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			p := mkPublishOrderPlan(models.StageWaitBuild, c.relation)
			app := p.Releases["app"]
			if !c.hasOwnReason {
				app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
			}
			results := map[string]*Result{"app": {Name: "app"}, "core": c.result}

			skip, blocker := shouldSkip("app", p, results, []string{"core"})
			assert.Equal(t, c.isSkipped, skip)
			if !c.isSkipped {
				return
			}
			assert.Equal(t, "core", blocker, "the sentence names the provider that actually failed")
			assert.Contains(t, formatSkipReason(blocker,
				p.Releases[blocker].Pkg.Space.ProviderRelation, c.result.RecordBlocked),
				"provider core")
		})
	}
}

// TestPublishOrderCountsOnlyDeclaredProvidersAsAReleaseReason: a provider that
// published is a reason to release a consumer because the consumer is picking
// that version up, and a provider reached through a package this run does not
// release hands it nothing to pick up. Counting one would let a consumer
// publish past a provider of its own that failed, naming a version that never
// went out, which is the mistake the cascade exists to prevent.
func TestPublishOrderCountsOnlyDeclaredProvidersAsAReleaseReason(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	app := p.Releases["app"]
	app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
	// `lib` is the provider `app` declares and that failed; `core` sits behind
	// the unreleasing `ui` and published.
	p.Providers["app"] = []string{"ui", "lib"}
	p.Releases["lib"] = &plan.Release{Pkg: &model.Package{Name: "lib", Dir: "lib",
		Space: &model.Space{Name: "lib"}}}
	results := map[string]*Result{
		"app":  {Name: "app"},
		"lib":  {Status: StatusFailed},
		"core": {Status: StatusPublished},
	}

	skip, blocker := shouldSkip("app", p, results, []string{"core"})
	assert.True(t, skip, "a publication behind the gap is no substitute for the failed provider")
	assert.Equal(t, "lib", blocker)

	// A declared provider that published is the reason it always was.
	p.Providers["app"] = []string{"ui", "lib", "core"}
	skip, _ = shouldSkip("app", p, results, nil)
	assert.False(t, skip, "a version the package does pick up is a reason to release it")
}

// TestPublishOrderSkipsAConsumerItReachesOnlyThroughAnUnreleasingPackage is
// the same claim through the executor: nothing about `app` names `core`, and a
// `core` that failed still leaves `app` skipped rather than published against
// a version that was never published (W194).
func TestPublishOrderSkipsAConsumerItReachesOnlyThroughAnUnreleasingPackage(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitBuild, models.StageWaitBuild)
	app := p.Releases["app"]
	app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
	r := &fakeRunner{fail: map[string]bool{"publish core": true}}
	res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 2, Publish: 2}).
		Run(context.Background(), p)

	require.Equal(t, StatusFailed, res["core"].Status)
	assert.Equal(t, StatusSkipped, res["app"].Status, "err: %v", res["app"].Err)
	assert.True(t, res["app"].Blocked)
	assert.Equal(t, "core", res["app"].BlockedBy,
		"the consumer is blocked by the package that failed, not by the one it names")
	assert.Equal(t, -1, r.indexOf("publish app"), "nothing published behind the failure")
}
