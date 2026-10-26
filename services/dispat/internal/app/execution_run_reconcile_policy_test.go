// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestRetainedCompleteGraphPolicyRequiresRecomputedTaskAndPackageCausality(t *testing.T) {
	task := execution.ChoreographyTaskSpec{Task: "beta:build", Package: "beta",
		Kind: string(execution.KindBuild), Budget: execution.ChoreographyBudgetBuild,
		Weight: 1, Placement: execution.PlacementWorker,
		Predecessors: []string{"alpha:build"}, Platforms: []string{"linux/amd64"}}
	policy := execution.ChoreographyPackagePolicy{Package: "beta", Direct: []string{"alpha"}}
	require.NoError(t, verifyRetainedChoreographyPolicy(
		[]execution.ChoreographyTaskSpec{task}, []execution.ChoreographyTaskSpec{task},
		[]execution.ChoreographyPackagePolicy{policy}, []execution.ChoreographyPackagePolicy{policy}))
	emptySerializedPolicy := policy
	emptySerializedPolicy.Providers = []string{}
	emptySerializedPolicy.Blocking = map[string]bool{}
	require.NoError(t, verifyRetainedChoreographyPolicy(
		[]execution.ChoreographyTaskSpec{task}, []execution.ChoreographyTaskSpec{task},
		[]execution.ChoreographyPackagePolicy{emptySerializedPolicy},
		[]execution.ChoreographyPackagePolicy{policy}),
		"omitted empty JSON fields and allocated empty collections have the same signed policy")
	changedTask := task
	changedTask.Predecessors = []string{"other:build"}
	require.ErrorContains(t, verifyRetainedChoreographyPolicy(
		[]execution.ChoreographyTaskSpec{task}, []execution.ChoreographyTaskSpec{changedTask},
		[]execution.ChoreographyPackagePolicy{policy}, []execution.ChoreographyPackagePolicy{policy}),
		"task \"beta:build\" differs")
	changedPolicy := policy
	changedPolicy.Direct = []string{"other"}
	require.ErrorContains(t, verifyRetainedChoreographyPolicy(
		[]execution.ChoreographyTaskSpec{task}, []execution.ChoreographyTaskSpec{task},
		[]execution.ChoreographyPackagePolicy{policy}, []execution.ChoreographyPackagePolicy{changedPolicy}),
		"package policy differs")
}
