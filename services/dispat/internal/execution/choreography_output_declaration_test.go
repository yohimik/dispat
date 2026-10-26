// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestChoreographyDeclaresOutputsOnlyForProducingStage(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		stage    release.Stage
		produces bool
	}{
		{name: "version", stage: release.Stage{Name: "version"}},
		{name: "sign", stage: release.Stage{Name: "sign"}},
		{name: "sync lock", stage: release.Stage{Name: "syncLock"}},
		{name: "before all", stage: release.Stage{Name: "beforeAll"}},
		{name: "test", stage: release.Stage{Name: "test"}},
		{name: "publish", stage: release.Stage{Name: "publish"}},
		{name: "record", stage: release.Stage{Name: "record"}},
		{name: "build", stage: release.BuildStage(), produces: true},
		{name: "producing custom stage", stage: release.Stage{
			Name: "generate", IsProducingOutputs: true}, produces: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := release.StageRequest{Descriptor: scenario.stage,
				Release: &plan.Release{Pkg: &model.Package{Space: &model.Space{
					BuildOutputs: []string{"dist"}}}}}
			roots := resolveDeclaredOutputs(request)
			if scenario.produces {
				require.Equal(t, []string{"dist"}, roots)
			} else {
				require.Nil(t, roots)
			}
		})
	}
}
