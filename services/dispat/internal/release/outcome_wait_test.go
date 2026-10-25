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
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestOutcomeWaitsWhenPublicationKindsOmitCausalEdges(t *testing.T) {
	for _, row := range []struct {
		name           string
		providers      map[string][]string
		blocking       map[string][]string
		cause          bool
		pickup         bool
		versionWork    bool
		withoutOwn     bool
		waitForOutcome bool
	}{
		{name: "direct propagated cause", providers: map[string][]string{"app": {"core"}},
			cause: true, withoutOwn: true, waitForOutcome: true},
		{name: "direct version pickup without own work", providers: map[string][]string{"app": {"core"}},
			pickup: true, withoutOwn: true, waitForOutcome: true},
		{name: "cause through an unreleasing relay", providers: map[string][]string{"app": {"ui"}, "ui": {"core"}},
			cause: true, withoutOwn: true, waitForOutcome: true},
		{name: "blocking edge through an unreleasing relay", providers: map[string][]string{"app": {"ui"}, "ui": {"core"}},
			blocking: map[string][]string{"app": {"ui"}, "ui": {"core"}}, waitForOutcome: true},
		{name: "own work survives an optional pickup failure", providers: map[string][]string{"app": {"core"}},
			pickup: true},
		{name: "own work still reconciles a changed provider before versioning",
			providers: map[string][]string{"app": {"core"}}, pickup: true, versionWork: true, waitForOutcome: true},
		{name: "independent own work behind a nonblocking relay", providers: map[string][]string{"app": {"ui"}, "ui": {"core"}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			p := mkPublishOrderPlan(models.StageWaitNone, models.StageWaitNone)
			p.Providers = row.providers
			p.PublishProviders = map[string][]string{}
			p.BlockingProviders = row.blocking
			if p.BlockingProviders == nil {
				p.BlockingProviders = map[string][]string{}
			}
			if row.cause {
				p.Releases["app"].DueTo = []string{"core"}
			}
			if row.pickup {
				p.Releases["app"].Updates = []plan.ProviderUpdate{{Name: "core"}}
			}
			if row.versionWork {
				p.Releases["app"].Pkg.Space.VersionScript = []string{"version"}
			}
			if row.withoutOwn {
				app := p.Releases["app"]
				app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
			}
			changed := map[string]bool{"app": true, "core": true}
			plans := newStagePlans(p, changed)
			sched, _ := (&Executor{}).buildTaskGraph(p, changed, plans)
			assert.Equal(t, !row.waitForOutcome,
				isRecordReadyBeforeProviderOutcome(t, sched, plans["core"].recording, plans["app"].recording))
		})
	}
}

func TestBlockingOnlyUnreleasingCycleCarriesOnlyChangedProviderOutcomes(t *testing.T) {
	for _, row := range []struct {
		name        string
		behindCycle bool
	}{
		{name: "cycle with no changed provider"},
		{name: "cycle with a changed provider", behindCycle: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			p := mkPublishOrderPlan(models.StageWaitNone, models.StageWaitNone)
			p.Releases["relay"] = &plan.Release{Pkg: &model.Package{Name: "relay", Space: &model.Space{
				ProviderRelation: model.NewStageRelation(&models.StageRelation{Build: models.StageWaitNone})}}}
			p.Providers = map[string][]string{"app": {"ui"}, "ui": {"relay"}, "relay": {"ui"}}
			if row.behindCycle {
				p.Providers["ui"] = append(p.Providers["ui"], "core")
			}
			p.PublishProviders = map[string][]string{}
			p.BlockingProviders = p.Providers
			changed := map[string]bool{"app": true, "core": true}
			require.NoError(t, ValidateExecutionGraph(p), "an unreleasing relay cycle is not a task cycle")
			plans := newStagePlans(p, changed)
			sched, reach := (&Executor{}).buildTaskGraph(p, changed, plans)
			assert.Equal(t, row.behindCycle,
				!isRecordReadyBeforeProviderOutcome(t, sched, plans["core"].recording, plans["app"].recording))
			if row.behindCycle {
				assert.Equal(t, []string{"core"}, reach.Indirect("app"))
			} else {
				assert.Empty(t, reach.Indirect("app"))
			}
		})
	}
}

func TestTransitivePropagatedCauseSettlesBeforePublicationWhenOrderKindsOmitIt(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitNone, models.StageWaitNone)
	p.PublishProviders = map[string][]string{}
	p.BlockingProviders = map[string][]string{}
	app := p.Releases["app"]
	app.OwnBump, app.Units, app.FreshUnits = ccme.BumpNone, nil, nil
	app.DueTo = []string{"core"}
	runner := &fakeRunner{fail: map[string]bool{"publish core": true}, delay: 15 * time.Millisecond}
	result := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 2, Publish: 2}).
		Run(context.Background(), p)
	require.Equal(t, StatusFailed, result["core"].Status)
	assert.Equal(t, StatusSkipped, result["app"].Status)
	assert.Equal(t, -1, runner.indexOf("publish app"), "the unreleasing relay cannot hide an unsettled cause")
}

func TestOwnCauseVersionStageReadsOnlySettledProviderPickups(t *testing.T) {
	p := mkPublishOrderPlan(models.StageWaitNone, models.StageWaitNone)
	p.Providers["app"] = []string{"core"}
	p.PublishProviders = map[string][]string{}
	p.BlockingProviders = map[string][]string{}
	app := p.Releases["app"]
	app.Updates = []plan.ProviderUpdate{{Name: "core"}}
	app.Pkg.Space.VersionScript = []string{"version"}
	runner := &fakeRunner{fail: map[string]bool{"publish core": true}, delay: 15 * time.Millisecond}
	result := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 2, Publish: 2}).
		Run(context.Background(), p)
	require.Equal(t, StatusFailed, result["core"].Status)
	require.Equal(t, StatusPublished, result["app"].Status, "%v", result["app"].Err)
	assert.Equal(t, -1, runner.indexOf("version app"),
		"a failed provider's planned version must not enter the consumer's version frame")

	runner = &fakeRunner{delay: 15 * time.Millisecond}
	result = newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Build: 2, Publish: 2}).
		Run(context.Background(), p)
	require.Equal(t, StatusPublished, result["core"].Status, "%v", result["core"].Err)
	require.Equal(t, StatusPublished, result["app"].Status, "%v", result["app"].Err)
	assert.Greater(t, runner.indexOf("version app"), runner.indexOf("publish core"),
		"versioning starts only after the provider's outcome settles")
	assert.Contains(t, runner.envPrefix(t, "version"), plan.UpdatedPackagesEnvVar+"=CORE",
		"a successful provider's published version is available to the consumer")
}

// isRecordReadyBeforeProviderOutcome completes every other ready task while
// withholding one provider's record, then asks whether its consumer could
// publish before that outcome is known.
func isRecordReadyBeforeProviderOutcome(t *testing.T, sched *graph.Scheduler[task],
	provider, consumer task) bool {
	t.Helper()
	frontier := sched.Ready()
	providerReady, consumerReady := false, false
	for len(frontier) > 0 {
		next := frontier[0]
		frontier = frontier[1:]
		switch next {
		case provider:
			providerReady = true
		case consumer:
			consumerReady = true
		default:
			frontier = append(frontier, sched.Done(next)...)
		}
	}
	require.True(t, providerReady, "the withheld provider must reach its record task")
	return consumerReady
}
