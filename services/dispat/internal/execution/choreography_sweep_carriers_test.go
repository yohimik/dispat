// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSweepAssignmentCarriesAdmittedTypedProviderPath(t *testing.T) {
	carried := newSweepOutputs()
	ref := &ExportedDeclaredPath{Repository: "source", Package: "core",
		Root: "coverage", Relative: "core.asset"}
	carried.admit(&sweepSet{task: "core:run", packageName: "core", repositoryName: "source",
		resultOID: "producer-result", manifest: &OutputManifest{Digest: "manifest-digest"},
		signedExports:  []ExportedValue{{Name: "CORE_ASSET", DeclaredPath: ref, Source: "core:run"}},
		rehomedExports: []ExportedValue{{Name: "CORE_ASSET", Value: "/first/coverage/core.asset", Source: "core:run"}}})
	c := &Coordinator{sweepOutputs: carried}
	outputs := []plan.Output{
		{Name: "CORE_ASSET", Value: "/first/coverage/core.asset", Source: "core:run"},
		{Name: "LITERAL", Value: "dispat-output://not-a-path"},
	}
	exports, inputs, err := c.sweepAssignmentInputs(outputs, []Source{{Name: "source", Path: "src"}})
	require.NoError(t, err)
	require.Equal(t, []AssignmentInput{{Task: "core:run", Package: "core", Commit: "producer-result",
		Digest: "manifest-digest", Path: "src"}}, inputs)
	require.Equal(t, "", exports[0].Value)
	require.Equal(t, ref, exports[0].DeclaredPath)
	require.Equal(t, outputs[1].Value, exports[1].Value)
	require.Nil(t, exports[1].DeclaredPath)

	_, _, err = c.sweepAssignmentInputs(outputs, []Source{{Name: "unrelated"}})
	require.ErrorContains(t, err, "outside its input closure")
}
