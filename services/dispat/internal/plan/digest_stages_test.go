// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestPlanDigestFollowsTheConfiguredStages: a stage the flow defines, its
// commands and the order the stages run in each change what a release runs,
// so each changes the digest; a label changes how the run is read and leaves
// it alone.
func TestPlanDigestFollowsTheConfiguredStages(t *testing.T) {
	want := digestOfFixture(t)
	heads := map[string]string{"": "0000000000000000000000000000000000000000"}
	libsSpace := func(options *Options) *model.Space { return options.Packages[0].Space }
	withStage := func(stage model.UserStage) func(*Options) {
		return func(options *Options) { libsSpace(options).UserStages = []model.UserStage{stage} }
	}
	for name, adjust := range map[string]func(*Options){
		"a defined stage": withStage(model.UserStage{Name: "lint", Commands: []string{"make lint"}}),
		"the stage's hook": withStage(model.UserStage{Name: "lint", Commands: []string{"make lint"},
			PostHook: []string{"make report"}}),
		"the order of the stages": func(options *Options) {
			libsSpace(options).StageGraph = model.DefaultStageGraph([]string{"version", "build", "tests"})
		},
		"a stage moved before the publication": func(options *Options) {
			graph, refusals := model.ResolveStageGraph([]model.StageSpec{{Name: "version"}, {Name: "build"},
				{Name: "publish", After: []string{"build"}}, {Name: "announce", After: []string{"build"}}})
			if len(refusals) > 0 {
				t.Fatal(refusals[0].Message)
			}
			libsSpace(options).StageGraph = graph
		},
	} {
		assert.NotEqual(t, want, digestOf(t, "/r", adjust, heads), name)
	}
	assert.Equal(t, want, digestOf(t, "/r", func(options *Options) {
		libsSpace(options).StageLabels = map[string]string{"build": "Compile"}
	}, heads), "a label only changes how the run is read")
}
