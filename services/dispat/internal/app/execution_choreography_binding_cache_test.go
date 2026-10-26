// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSweepSignedVisibleReleaseKeepsProviderNameOrderAndOwnOverride(t *testing.T) {
	baseline := &plan.Release{Pkg: &model.Package{Name: "consumer"},
		Outputs: []plan.Output{{Name: "TOKEN", Value: "baseline"}}}
	pl := &plan.Plan{Releases: map[string]*plan.Release{"consumer": baseline}}
	frozen := freezeChoreographySweepPlan(pl)
	baseline.Outputs[0].Value = "mutated after compilation"
	spec := execution.ChoreographyTaskSpec{Task: "consumer:run", Package: "consumer",
		Predecessors: []string{"zeta:run", "alpha:run"}}
	parents := map[string][]plan.Output{
		"zeta:run":  {{Name: "TOKEN", Value: "zeta", Source: "zeta:run"}},
		"alpha:run": {{Name: "TOKEN", Value: "alpha", Source: "alpha:run"}},
	}
	visible, err := sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
		Plan: frozen, Task: spec, Predecessors: parents})
	require.NoError(t, err)
	require.Equal(t, []plan.Output{{Name: "TOKEN", Value: "zeta", Source: "zeta:run"}}, visible.Outputs)
	require.Equal(t, "mutated after compilation", baseline.Outputs[0].Value)
	require.Equal(t, "baseline", frozen.Releases["consumer"].Outputs[0].Value)
	visible, err = sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
		Plan: frozen, Task: spec, Predecessors: parents,
		OwnOutputs: []plan.Output{{Name: "TOKEN", Value: "own", Source: "consumer:run"}}})
	require.NoError(t, err)
	require.Equal(t, "own", visible.Outputs[0].Value)
	delete(parents, "alpha:run")
	_, err = sweepSignedVisibleRelease(sweepSignedVisibleReleaseOptions{
		Plan: frozen, Task: spec, Predecessors: parents})
	require.ErrorContains(t, err, "lacks signed predecessor")
}
