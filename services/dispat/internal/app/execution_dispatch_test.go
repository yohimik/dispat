// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
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
