// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompiledPackageOutcomePolicyPreservesBlockerPriorityAndOwnCause(t *testing.T) {
	policies := []ChoreographyPackagePolicy{
		{Package: "core"}, {Package: "api"}, {Package: "app",
			Providers: []string{"core", "api"}, Reached: []string{"core"},
			Direct: []string{"core", "api"}, Blocking: map[string]bool{"api": true}},
		{Package: "independent", Providers: []string{"core"},
			Direct: []string{"core"}, OwnCause: true},
	}
	e, err := NewChoreographyPackageOutcomeEvaluator(policies)
	require.NoError(t, err)
	require.NoError(t, e.ApplyOutcome("core", "failed"))
	skip, by, err := e.ShouldSkip("app")
	require.NoError(t, err)
	require.True(t, skip)
	require.Equal(t, "core", by)
	skip, _, err = e.ShouldSkip("independent")
	require.NoError(t, err)
	require.False(t, skip)
	require.NoError(t, e.ApplyOutcome("api", "record-blocked"))
	skip, by, err = e.ShouldSkip("app")
	require.NoError(t, err)
	require.True(t, skip)
	require.Equal(t, "api", by)
	require.Error(t, e.ApplyOutcome("api", "published"))
}
