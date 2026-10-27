// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestChoreographyAttemptLimitIsSignedOnlyForInterpretation(t *testing.T) {
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
