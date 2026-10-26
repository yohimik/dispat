// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestPublishReadinessDependencyUsesPlannedProviderClosure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		relations  map[string]models.StageWait
		providers  map[string][]string
		planned    map[string]bool
		wantSource string
		wantReader string
	}{
		{name: "direct publish", relations: map[string]models.StageWait{
			"core": models.StageWaitPublish}, providers: map[string][]string{
			"app": {"core"}}, planned: map[string]bool{"core": true, "app": true},
			wantSource: "core", wantReader: "app"},
		{name: "publish through an unplanned package", relations: map[string]models.StageWait{
			"core": models.StageWaitPublish, "ui": models.StageWaitPublish},
			providers:  map[string][]string{"app": {"ui"}, "ui": {"core"}},
			planned:    map[string]bool{"core": true, "app": true},
			wantSource: "core", wantReader: "app"},
		{name: "build relation does not require native publication", relations: map[string]models.StageWait{
			"core": models.StageWaitBuild}, providers: map[string][]string{
			"app": {"core"}}, planned: map[string]bool{"core": true, "app": true}},
		{name: "a build hop ends publication reach", relations: map[string]models.StageWait{
			"core": models.StageWaitPublish, "ui": models.StageWaitBuild},
			providers: map[string][]string{"app": {"ui"}, "ui": {"core"}},
			planned:   map[string]bool{"core": true, "app": true}},
		{name: "already recorded unplanned provider", relations: map[string]models.StageWait{
			"core": models.StageWaitPublish}, providers: map[string][]string{
			"app": {"core"}}, planned: map[string]bool{"app": true}},
		{name: "independent planned packages", relations: map[string]models.StageWait{
			"core": models.StageWaitPublish}, planned: map[string]bool{"core": true, "app": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &plan.Plan{Releases: make(map[string]*plan.Release), Providers: tc.providers}
			for _, name := range []string{"app", "core", "ui"} {
				wait := tc.relations[name]
				rel := &plan.Release{Pkg: &model.Package{Name: name, Space: &model.Space{
					ProviderRelation: model.NewStageRelation(&models.StageRelation{Build: wait})}}}
				if tc.planned[name] {
					rel.Bump, rel.NewWork = ccme.BumpMinor, true
				}
				p.Releases[name] = rel
				p.Order = append(p.Order, name)
			}
			provider, consumer, found := PublishReadinessDependency(p)
			require.Equal(t, tc.wantSource != "", found)
			require.Equal(t, tc.wantSource, provider)
			require.Equal(t, tc.wantReader, consumer)
		})
	}
}
