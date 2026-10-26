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
		retainedChoreographyPolicyOptions{
			RecomputedTasks:    []execution.ChoreographyTaskSpec{task},
			SignedTasks:        []execution.ChoreographyTaskSpec{task},
			RecomputedPackages: []execution.ChoreographyPackagePolicy{policy},
			SignedPackages:     []execution.ChoreographyPackagePolicy{policy},
		}))
	emptySerializedPolicy := policy
	emptySerializedPolicy.Providers = []string{}
	emptySerializedPolicy.Blocking = map[string]bool{}
	require.NoError(t, verifyRetainedChoreographyPolicy(
		retainedChoreographyPolicyOptions{
			RecomputedTasks:    []execution.ChoreographyTaskSpec{task},
			SignedTasks:        []execution.ChoreographyTaskSpec{task},
			RecomputedPackages: []execution.ChoreographyPackagePolicy{emptySerializedPolicy},
			SignedPackages:     []execution.ChoreographyPackagePolicy{policy},
		}),
		"omitted empty JSON fields and allocated empty collections have the same signed policy")
	changedTask := task
	changedTask.Predecessors = []string{"other:build"}
	require.ErrorContains(t, verifyRetainedChoreographyPolicy(
		retainedChoreographyPolicyOptions{
			RecomputedTasks:    []execution.ChoreographyTaskSpec{task},
			SignedTasks:        []execution.ChoreographyTaskSpec{changedTask},
			RecomputedPackages: []execution.ChoreographyPackagePolicy{policy},
			SignedPackages:     []execution.ChoreographyPackagePolicy{policy},
		}),
		"task \"beta:build\" differs")
	changedPolicy := policy
	changedPolicy.Direct = []string{"other"}
	require.ErrorContains(t, verifyRetainedChoreographyPolicy(
		retainedChoreographyPolicyOptions{
			RecomputedTasks:    []execution.ChoreographyTaskSpec{task},
			SignedTasks:        []execution.ChoreographyTaskSpec{task},
			RecomputedPackages: []execution.ChoreographyPackagePolicy{policy},
			SignedPackages:     []execution.ChoreographyPackagePolicy{changedPolicy},
		}),
		"package policy differs")
}
