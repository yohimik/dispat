// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// A signed v3 success cannot omit the declared output set. A failed frame
// may omit it because its partial exports are separately validated before
// any consumer receives them.
func TestChoreographySuccessfulProducerCannotOmitDeclaredOutputs(t *testing.T) {
	c := &Coordinator{Run: "run", Generation: "generation", PlanDigest: "plan"}
	c.registerChoreographyResult("core:build", []string{"dist"}, nil, true)
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: ChoreographyRunBinding{
		Run: "run", Generation: "generation", PlanDigest: "plan"}, Task: "core:build"}
	result := Result{Status: StatusSucceeded}
	require.ErrorContains(t, c.VerifyChoreographyResult(t.Context(), proposal, result, &gitx.LocalGitx{}),
		"successful producer has no declared output set")
}
