// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogicalGraphDigestBindsTemplatesEdgesAndPolicyButNotRuntimeOIDs(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan"}
	specs := []ChoreographyTaskSpec{{Task: "a", Kind: KindBuild, Weight: 1,
		Branch: FormatChoreographyTaskBranch("run", "a"), ReservationOID: "first-root",
		EligibleNodes: []string{"first"}},
		{Task: "b", Kind: KindStage, Weight: 1, Stage: "test",
			Predecessors: []string{"a"}, ReservationOID: "second-root"}}
	templates := []ChoreographyCompiledTemplate{{Task: "a", Kind: KindBuild, Weight: 1,
		Frame: &AssignmentFrame{Commands: []string{"build"}}},
		{Task: "b", Kind: KindStage, Stage: "test", Weight: 1,
			Frame: &AssignmentFrame{Commands: []string{"test"}}}}
	policies := []ChoreographyPackagePolicy{{Package: "pkg", Providers: []string{"base"}}}
	inventory := ChoreographyLogicalGraphInventory{Binding: binding,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, SemanticGraphDigest: "semantic-graph",
		Tasks: specs, Templates: templates, PackagePolicies: policies}
	base, err := DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	specs[0].ReservationOID = "later-root"
	specs[0].EligibleNodes = []string{"late-peer"}
	rebound, err := DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	require.Equal(t, base, rebound)
	templates[1].Frame.Commands[0] = "changed"
	changed, err := DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	require.NotEqual(t, base, changed)
	templates[1].Frame.Commands[0] = "test"
	policies[0].Providers[0] = "different"
	changed, err = DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	require.NotEqual(t, base, changed)
}
