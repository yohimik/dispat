// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A stage the flow defines, as the generic stage path places it: like the
// build, under the generic kind, carrying its own name to the node.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// userStage is a defined stage's descriptor placed after the build, as the
// executor hands it over.
func userStage() release.Stage {
	return release.Stage{Name: "scanImages", Title: "ScanImages", Placement: release.PlacementByBuild,
		Inputs: release.InputsOwnOutputs}
}

// TestAUserStageTravelsUnderTheGenericKind: the frame is placed by the build's
// runOnly value, travels under the kind `stage` naming the stage, asks for no
// outputs back, and a node runs it under the stage's own name and hook names.
func TestAUserStageTravelsUnderTheGenericKind(t *testing.T) {
	for _, value := range []string{public.RunOnlyBoth, public.RunOnlyWorker, public.RunOnlyOrchestrator} {
		space := &model.Space{RunOnly: &public.RunOnly{Build: value, Publish: public.RunOnlyOrchestrator},
			LoginScript: []string{"npm login"}}
		assert.Equal(t, ResolveStagePlacement(StageBuild, value, false),
			resolveDescriptorPlacement(userStage(), space), value)
	}
	request := release.StageRequest{Stage: "scanImages", Descriptor: userStage(), Release: &plan.Release{
		Pkg: &model.Package{Name: "core", Space: &model.Space{BuildOutputs: []string{"dist"}}},
	}}
	assert.Equal(t, KindStage, resolveStageKind(request.Descriptor))
	assert.Nil(t, resolveDeclaredOutputs(request))
	assert.Equal(t, "scanImages", resolveAssignmentStage(KindStage, request))

	assignment := Assignment{Header: Header{Kind: KindStage}, Stage: "scanImages"}
	assert.Equal(t, "scanImages", resolveFrameStage(assignment))
	assert.Equal(t, "ScanImages", formatStageTitle(resolveFrameStage(assignment)),
		"the node spells the hooks beforeScanImages and postScanImages, as the orchestrator does")
	assert.True(t, isStageName("scanImages"), "the name passes the acceptance rule a node applies")
}
