// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestPinnedEffectiveConcurrencyPreservesDefaultsAndChecksExplicitValues(t *testing.T) {
	const buildCapacity = 7
	const publishCapacity = 5
	invocation := pinnedSemanticInvocation{EffectiveConcurrency: []int{buildCapacity, publishCapacity}}
	for _, test := range []struct {
		name        string
		configured  []int
		wantFailure bool
	}{
		{name: "omitted"},
		{name: "both default", configured: []int{0, 0}},
		{name: "one default", configured: []int{0}},
		{name: "partial default", configured: []int{buildCapacity, 0}},
		{name: "explicit match", configured: []int{buildCapacity, publishCapacity}},
		{name: "explicit mismatch", configured: []int{buildCapacity, 6}, wantFailure: true},
		{name: "single explicit mismatch", configured: []int{buildCapacity}, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.File{Concurrency: append([]int(nil), test.configured...),
				BuildConcurrency: 99, PublishConcurrency: 99} // Stand-in for another host's CPU default.
			err := pinPeerEffectiveConcurrency(cfg, invocation)
			if test.wantFailure {
				require.ErrorContains(t, err, pinnedPlanningConcurrencyMismatch)
				return
			}
			require.NoError(t, err)
			require.Equal(t, buildCapacity, cfg.BuildConcurrency)
			require.Equal(t, publishCapacity, cfg.PublishConcurrency)
			require.Equal(t, test.configured, cfg.Concurrency)
		})
	}
	old := &config.File{BuildConcurrency: 99, PublishConcurrency: 99}
	require.NoError(t, pinPeerEffectiveConcurrency(old, pinnedSemanticInvocation{}))
	require.Equal(t, 99, old.BuildConcurrency)
}

func TestPinnedInvocationSignsResolvedConcurrency(t *testing.T) {
	a := &App{cfg: &config.File{BuildConcurrency: 7, PublishConcurrency: 5}}
	document, err := a.pinnedInvocation()
	require.NoError(t, err)
	decoded, err := parsePinnedInvocation(document)
	require.NoError(t, err)
	require.Equal(t, []int{7, 5}, decoded.EffectiveConcurrency)
}
