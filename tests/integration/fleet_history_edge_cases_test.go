// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFleetHistoryRefusesAControlCommitWhoseFirstParentIsMissing proves the
// checkpoint index is a complete DAG, rather than a bag of individually valid
// commits. A truncated Git reply can retain a well-formed child while omitting
// its first parent; planning must refuse the whole history and show no partial
// package plan.
func TestFleetHistoryRefusesAControlCommitWhoseFirstParentIsMissing(t *testing.T) {
	fleet := finalPolyrepo(t)
	child := fleet.control.Git("rev-parse", "HEAD")
	missing := strings.Repeat("1", 40)
	output := strings.Join([]string{
		"", "dispat-control-gitlink-history-v1", child, missing,
		"Release Planner", "planner@example.test", "chore: truncated history", "",
	}, "\x00")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --topo-order*",
		Output:  output,
	})

	res := fleet.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "is missing first parent "+missing)
	assert.NotContains(t, combined, "release plan ready")
	assert.Empty(t, plannedPackages(res))
	assert.Equal(t, 1, fault.Matches())
	assert.Empty(t, polyrepoTags(fleet.control, "sources/lib"))
}
