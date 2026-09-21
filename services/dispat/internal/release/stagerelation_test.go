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

// The three relations, seen from the task graph and from the skip cascade.
//
// Two claims are checked separately because they fail separately. The
// orderings a relation imposes are read out of the fake runner's recorded
// order, which is evidence independent of any timing: an edge either kept a
// task back or it did not. The ordering a relation does NOT impose cannot be
// read that way at all — the absence of an edge shows up as work happening at
// once — so it is read out of the peak number of builds in flight, which is
// how every other budget claim in this file is checked.

// relationOf builds the resolved relation a space states, the way the ladder
// would have resolved it, so the fixtures below state configuration rather
// than the two values it folds down to.
func relationOf(t *testing.T, build models.StageWait, isBlocking *bool) *model.StageRelation {
	t.Helper()
	resolved := model.NewStageRelation(&models.StageRelation{Build: build, IsBlocking: isBlocking})
	return &resolved
}

// TestStageRelationChainLaunchOrder walks a three-deep chain (a <- b <- c)
// under each relation. The publish chain is the invariant: it holds under all
// three, because publishing against a version that was never published is
// invalid whatever the builds did.
func TestStageRelationChainLaunchOrder(t *testing.T) {
	chain := func(t *testing.T, build models.StageWait) *fakeRunner {
		t.Helper()
		p := mkPlan(planSpec{
			Relation: relationOf(t, build, nil),
			Deps:     map[string][]string{"b": {"a"}, "c": {"b"}},
			Names:    []string{"a", "b", "c"},
		})
		r := &fakeRunner{delay: 30 * time.Millisecond}
		res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 4, Publish: 4}).
			Run(context.Background(), p)
		for _, name := range []string{"a", "b", "c"} {
			require.Equal(t, StatusPublished, res[name].Status, "%s: %v", name, res[name].Err)
		}
		assert.Less(t, r.indexOf("publish a"), r.indexOf("publish b"), "the publish chain holds")
		assert.Less(t, r.indexOf("publish b"), r.indexOf("publish c"), "the publish chain holds")
		return r
	}

	t.Run("none lets the whole chain build at once", func(t *testing.T) {
		r := chain(t, models.StageWaitNone)
		assert.Equal(t, 3, r.maxCur["build"],
			"no build waits for a provider, so all three are in flight together")
	})

	t.Run("build serialises the builds", func(t *testing.T) {
		r := chain(t, models.StageWaitBuild)
		assert.Less(t, r.indexOf("build a"), r.indexOf("build b"))
		assert.Less(t, r.indexOf("build b"), r.indexOf("build c"))
		assert.Equal(t, 1, r.maxCur["build"], "each build waits for the one before it")
	})

	t.Run("publish holds each build behind the provider's publish", func(t *testing.T) {
		r := chain(t, models.StageWaitPublish)
		assert.Less(t, r.indexOf("publish a"), r.indexOf("build b"))
		assert.Less(t, r.indexOf("publish b"), r.indexOf("build c"))
	})
}

// TestStageRelationDiamondLaunchOrder walks a fan-out/fan-in shape
// (a <- b, a <- c, {b,c} <- d) under each relation. The two independent
// consumers are what makes the diamond worth its own fixture: under `build`
// they may overlap each other while both wait for a, and under `none` they
// overlap a as well.
func TestStageRelationDiamondLaunchOrder(t *testing.T) {
	diamond := func(t *testing.T, build models.StageWait) *fakeRunner {
		t.Helper()
		p := mkPlan(planSpec{
			Relation: relationOf(t, build, nil),
			Deps:     map[string][]string{"b": {"a"}, "c": {"a"}, "d": {"b", "c"}},
			Names:    []string{"a", "b", "c", "d"},
		})
		r := &fakeRunner{delay: 30 * time.Millisecond}
		res := newExecutor(execSpec{Runner: r, Tagger: &fakeTagger{}, Build: 4, Publish: 4}).
			Run(context.Background(), p)
		for _, name := range []string{"a", "b", "c", "d"} {
			require.Equal(t, StatusPublished, res[name].Status, "%s: %v", name, res[name].Err)
		}
		for _, after := range []string{"b", "c"} {
			assert.Less(t, r.indexOf("publish a"), r.indexOf("publish "+after), "the publish order holds")
			assert.Less(t, r.indexOf("publish "+after), r.indexOf("publish d"), "and converges")
		}
		return r
	}

	t.Run("none lets every build run at once", func(t *testing.T) {
		r := diamond(t, models.StageWaitNone)
		assert.Equal(t, 4, r.maxCur["build"], "no build waits for a provider")
	})

	t.Run("build waits for the providers' builds only", func(t *testing.T) {
		r := diamond(t, models.StageWaitBuild)
		for _, after := range []string{"b", "c"} {
			assert.Less(t, r.indexOf("build a"), r.indexOf("build "+after))
			assert.Less(t, r.indexOf("build "+after), r.indexOf("build d"))
		}
		assert.Equal(t, 2, r.maxCur["build"],
			"the two independent consumers overlap each other and nothing else")
	})

	t.Run("publish waits for the providers' publishes", func(t *testing.T) {
		r := diamond(t, models.StageWaitPublish)
		for _, after := range []string{"b", "c"} {
			assert.Less(t, r.indexOf("publish a"), r.indexOf("build "+after))
			assert.Less(t, r.indexOf("publish "+after), r.indexOf("build d"))
		}
	})
}

// TestStageRelationBlockingOutranksOwnWork is the skip cascade's half of the
// relation: a provider that failed skips its consumers unconditionally when
// its relation blocks, and leaves a consumer's own release reason standing
// when it does not. `none` blocks by default because the consumer's
// publication is the whole of what was supposed to follow the provider's;
// `build` does not, which is what the key's `false` has always done.
func TestStageRelationBlockingOutranksOwnWork(t *testing.T) {
	for name, c := range map[string]struct {
		build      models.StageWait
		isBlocking *bool
		isSkipped  bool
	}{
		"none blocks by default":            {models.StageWaitNone, nil, true},
		"none may be relaxed":               {models.StageWaitNone, models.Bool(false), false},
		"build leaves the reason standing":  {models.StageWaitBuild, nil, false},
		"build may be asked to block":       {models.StageWaitBuild, models.Bool(true), true},
		"publish blocks":                    {models.StageWaitPublish, nil, true},
		"publish blocks when written twice": {models.StageWaitPublish, models.Bool(true), true},
	} {
		t.Run(name, func(t *testing.T) {
			units := []*ccme.Unit{{Bump: ccme.BumpMinor, Valid: true}}
			consumer := &plan.Release{
				Pkg:        &model.Package{Name: "app", Space: &model.Space{Name: "apps"}},
				OwnBump:    ccme.BumpMinor,
				Units:      units,
				FreshUnits: units, // a release reason of the consumer's own
				Next:       ccme.Version{Minor: 3},
			}
			provider := &plan.Release{Pkg: &model.Package{Name: "core", Space: &model.Space{
				Name: "libs", ProviderRelation: *relationOf(t, c.build, c.isBlocking)}}}
			p := &plan.Plan{
				Releases:  map[string]*plan.Release{"app": consumer, "core": provider},
				Providers: map[string][]string{"app": {"core"}},
			}
			results := map[string]*Result{"core": {Status: StatusFailed}}

			skip, blocker := shouldSkip("app", p, results)
			assert.Equal(t, c.isSkipped, skip, "a fresh own bump beside a failed provider")
			if c.isSkipped {
				assert.Equal(t, "core", blocker)
			}

			// Without a reason of its own the consumer is skipped under every
			// relation, which is the rule a relation can only ever strengthen.
			consumer.FreshUnits = nil
			consumer.OwnBump = ccme.BumpNone
			consumer.Units = nil
			skip, blocker = shouldSkip("app", p, results)
			assert.True(t, skip, "nothing of its own to release")
			assert.Equal(t, "core", blocker)
		})
	}
}

// TestStageRelationSkipReasonNamesWhatFailed: the sentence an operator reads
// in place of the release has to stay true for each relation separately.
func TestStageRelationSkipReasonNamesWhatFailed(t *testing.T) {
	for name, c := range map[string]struct {
		relation        model.StageRelation
		isRecordBlocked bool
		want            string
	}{
		"a publish relation names the input": {
			model.NewStageRelation(models.StageRelationOf(true)), false,
			"this package's build takes its publish as input"},
		"a blocking relation names the publication order": {
			model.NewStageRelation(&models.StageRelation{Build: models.StageWaitNone}), false,
			"its consumers publish only after it published"},
		"a build relation names the missing reason": {
			model.NewStageRelation(models.StageRelationOf(false)), false,
			"the package has no changes of its own"},
		"an incomplete record outranks every relation": {
			model.NewStageRelation(models.StageRelationOf(true)), true,
			"repair its records before releasing dependents"},
	} {
		t.Run(name, func(t *testing.T) {
			reason := formatSkipReason("core", c.relation, c.isRecordBlocked)
			assert.Contains(t, reason, c.want)
			assert.Contains(t, reason, "provider core", "the sentence names what failed")
		})
	}
}
