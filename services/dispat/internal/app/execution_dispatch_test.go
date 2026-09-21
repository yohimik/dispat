// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// closurePlan is a plan reduced to what the output closure reads: who provides
// for whom, and each provider's relation.
func closurePlan(providers map[string][]string, waits map[string]models.StageWait) *plan.Plan {
	releases := map[string]*plan.Release{}
	for name, wait := range waits {
		releases[name] = &plan.Release{Pkg: &model.Package{Name: name,
			Space: &model.Space{ProviderRelation: model.StageRelation{Build: wait}}}}
	}
	return &plan.Plan{Providers: providers, Releases: releases}
}

// TestOutputClosureEndsAtAProviderItsConsumersDoNotRead: a build's inputs are
// the outputs of the providers it reads, through whatever sits between them,
// and a `none` relation is the declaration that nothing is read (§19.2a). The
// path ends there; a provider another path still reaches stays an input.
func TestOutputClosureEndsAtAProviderItsConsumersDoNotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		providers map[string][]string
		waits     map[string]models.StageWait
		want      []string
	}{
		"every relation but none carries outputs, transitively": {
			providers: map[string][]string{"app": {"ui"}, "ui": {"core"}},
			waits:     map[string]models.StageWait{"ui": models.StageWaitBuild, "core": models.StageWaitPublish},
			want:      []string{"core", "ui"},
		},
		"a provider nobody reads contributes nothing": {
			providers: map[string][]string{"front": {"infra"}},
			waits:     map[string]models.StageWait{"infra": models.StageWaitNone},
			want:      nil,
		},
		"nothing behind a provider nobody reads is reached through it": {
			providers: map[string][]string{"front": {"infra"}, "infra": {"modules"}},
			waits:     map[string]models.StageWait{"infra": models.StageWaitNone, "modules": models.StageWaitBuild},
			want:      nil,
		},
		"another path with no none hop still reaches it": {
			providers: map[string][]string{"app": {"infra", "ui"}, "infra": {"core"}, "ui": {"core"}},
			waits: map[string]models.StageWait{"infra": models.StageWaitNone,
				"ui": models.StageWaitBuild, "core": models.StageWaitBuild},
			want: []string{"core", "ui"},
		},
		"a name the plan holds no release for reads as the default": {
			providers: map[string][]string{"app": {"vendored"}},
			waits:     map[string]models.StageWait{},
			want:      []string{"vendored"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			closure := map[string]bool{}

			collectProviderClosure(closurePlan(tc.providers, tc.waits), "app", closure)
			collectProviderClosure(closurePlan(tc.providers, tc.waits), "front", closure)

			var got []string
			for provider := range closure {
				got = append(got, provider)
			}
			sort.Strings(got)
			assert.Equal(t, tc.want, got)
		})
	}
}

// outputPlan is a plan reduced to what the input resolution reads: who
// provides for whom, which packages this run releases, and which of them
// declare build outputs.
func outputPlan(order []string, providers map[string][]string,
	releasing map[string]bool, outputs map[string][]string) *plan.Plan {
	releases := map[string]*plan.Release{}
	for _, name := range order {
		rel := &plan.Release{
			Pkg: &model.Package{Name: name, Dir: filepath.Join("/work/packages", name),
				Space: &model.Space{Name: "libs", BuildOutputs: outputs[name]}},
		}
		if releasing[name] {
			// What makes a package this run's: work of its own carrying a bump.
			rel.Bump, rel.NewWork = ccme.BumpMinor, true
		}
		releases[name] = rel
	}
	return &plan.Plan{Order: order, Providers: providers, Releases: releases}
}

// TestInputsNameEveryProviderWhetherOrNotTheRunReleasesIt: §28.5 requires a
// task's inputs to include every build dependency it reads, including the
// dependencies whose packages need no new release. A provider the run leaves
// alone therefore stays in the closure and is marked as one the run has to
// build on purpose; a provider that declares no outputs supplies nothing
// either way.
func TestInputsNameEveryProviderWhetherOrNotTheRunReleasesIt(t *testing.T) {
	app := &App{root: filepath.FromSlash("/work"), log: zerolog.Nop()}
	pl := outputPlan(
		[]string{"assets", "tools", "types", "ui"},
		map[string][]string{"ui": {"assets", "tools", "types"}},
		map[string]bool{"assets": true, "tools": false, "types": false},
		map[string][]string{"assets": {"dist"}, "tools": {"dist"}},
	)

	inputs := app.resolveOutputProviders(pl)("ui")

	assert.Equal(t, []execution.InputPackage{
		{Package: "assets", Path: "packages/assets"},
		{Package: "tools", Path: "packages/tools", IsPrepared: true},
	}, inputs, "types declares no outputs and supplies nothing")
}

// TestAPreparedFrameIsTheProvidersOwnBuildFrame: what the run executes for a
// provider it does not release is that package's build stage under the
// environment `dispat run` would give it, with the configuration's own pairs
// left unresolved so that a value naming a secret never enters a mailbox.
func TestAPreparedFrameIsTheProvidersOwnBuildFrame(t *testing.T) {
	app := &App{root: filepath.FromSlash("/work"), log: zerolog.Nop()}
	pl := outputPlan([]string{"tools"}, nil, map[string]bool{}, map[string][]string{"tools": {"dist"}})
	space := pl.Releases["tools"].Pkg.Space
	space.BeforeBuildScript = []string{"echo before"}
	space.BuildScript = []string{"echo building"}
	space.PostBuildScript = []string{"echo after"}
	space.Env = []string{"TOKEN=$NPM_TOKEN"}

	prepared, err := app.resolvePreparedProviders(pl, &script.ShellRunner{Log: zerolog.Nop()})("tools")

	require.NoError(t, err)
	assert.Equal(t, execution.StageBuild, prepared.Request.Stage)
	assert.Equal(t, release.StageFrame{Before: []string{"echo before"},
		Commands: []string{"echo building"}, After: []string{"echo after"}}, prepared.Request.Frame)
	assert.Equal(t, []string{"TOKEN=$NPM_TOKEN"}, prepared.Request.StaticEnv,
		"the declared pairs travel exactly as the configuration wrote them")
	assert.Contains(t, prepared.Request.Env, "DISPAT_STAGE=build")
	assert.Contains(t, prepared.Request.Env, "DISPAT_BUMP=none",
		"a preparation is not a release and bumps nothing")
	for _, pair := range prepared.Request.Env {
		assert.NotContains(t, pair, "$NPM_TOKEN", "no declared pair was resolved here")
	}
	require.NotNil(t, prepared.Here)
}

// TestAPlanWithoutTheProviderCannotPrepareIt: a name the plan holds no entry
// for has no folder, no space and no build frame, so the run says so instead
// of building something it cannot describe.
func TestAPlanWithoutTheProviderCannotPrepareIt(t *testing.T) {
	app := &App{root: filepath.FromSlash("/work"), log: zerolog.Nop()}
	pl := outputPlan(nil, nil, nil, nil)

	prepared, err := app.resolvePreparedProviders(pl, &script.ShellRunner{Log: zerolog.Nop()})("vendored")

	require.Error(t, err)
	assert.Nil(t, prepared)
	assert.Contains(t, err.Error(), "vendored")
}
