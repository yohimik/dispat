// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestChoreographyAttemptLimitKeepsLegacyReplayAndExplicitNativeLimit(t *testing.T) {
	specs := []execution.ChoreographyTaskSpec{
		{Task: "worker:build"},
		{Task: "native:publish", ExecutorOwned: true},
		{Task: "gate", Virtual: true},
	}
	applyChoreographyAttemptLimit(specs, execution.ChoreographyJoinPolicy{})
	for _, spec := range specs {
		require.Zero(t, spec.MaxAttempts)
	}
	applyChoreographyAttemptLimit(specs, execution.ChoreographyJoinPolicy{MaxAttempts: 2})
	require.Equal(t, 2, specs[0].MaxAttempts)
	require.Equal(t, 1, specs[1].MaxAttempts)
	require.Zero(t, specs[2].MaxAttempts)
	changed := append([]execution.ChoreographyTaskSpec(nil), specs...)
	changed[0].MaxAttempts = 1
	require.ErrorContains(t, verifyRetainedChoreographyPolicy(retainedChoreographyPolicyOptions{
		RecomputedTasks: specs, SignedTasks: changed,
	}), "retained complete graph task")
}

func TestEveryDistributionModeSignsResolvedAttemptLimit(t *testing.T) {
	for _, mode := range []models.DistributionMode{models.DistributionModeCompilation,
		models.DistributionModeInterpretation, models.DistributionModeHybrid} {
		for _, limit := range []*int{nil, models.Int(2)} {
			coordinator := &execution.Coordinator{
				CanonicalEndpoint: "file:///canonical",
				JoinPolicy: execution.ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
					Authorized: []execution.ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: "file:///canonical"}}},
			}
			settings := &models.ExecutionConfig{MaxAttempts: limit}
			require.NoError(t, setChoreographyCoordinatorMode(coordinator, string(mode), settings))
			require.Equal(t, string(mode), coordinator.JoinPolicy.DistributionMode)
			require.Equal(t, settings.ResolveMaxAttempts(), coordinator.JoinPolicy.MaxAttempts)
			specs := []execution.ChoreographyTaskSpec{{Task: "build"}, {Task: "publish", ExecutorOwned: true}}
			applyChoreographyAttemptLimit(specs, coordinator.JoinPolicy)
			require.Equal(t, settings.ResolveMaxAttempts(), specs[0].MaxAttempts)
			require.Equal(t, models.DefaultExecutionMaxAttempts, specs[1].MaxAttempts)
		}
	}
}
