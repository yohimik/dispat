// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func TestMissingApplicableControlBoundariesReportInPlanOrder(t *testing.T) {
	for range 128 {
		control := &composedControlGit{
			fakeGit: newFakeGit(),
			control: []gitx.ControlHistoryCommit{{
				SHA: "control1", Message: "release(*)%rc: move the fleet",
			}},
		}
		pl, err := Compute(context.Background(), control, Options{
			Packages: []*model.Package{
				{Name: "b", Dir: "/workspace/b/packages/b", RepoRoot: "/workspace/b", Repository: "b-source", Space: &model.Space{Name: "b"}},
				{Name: "a", Dir: "/workspace/a/packages/a", RepoRoot: "/workspace/a", Repository: "a-source", Space: &model.Space{Name: "a"}},
			},
			Repositories: map[string]RepositoryHistory{
				"control": {Name: "control", Root: "/workspace", Git: control, Control: true},
				"a-source": {
					Name: "a-source", Root: "/workspace/a", Path: "a",
					Git: newFakeGit(commit{sha: "a0", message: "feat(a): initial"}).tag("a", "1.0.0", "a0"),
				},
				"b-source": {
					Name: "b-source", Root: "/workspace/b", Path: "b",
					Git: newFakeGit(commit{sha: "b0", message: "feat(b): initial"}).tag("b", "1.0.0", "b0"),
				},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, pl.Diagnostics)
		assert.Equal(t, CodeRepositoryBoundary, pl.Diagnostics[0].Code)
		assert.Equal(t, "a", pl.Diagnostics[0].Pkg,
			"the first missing boundary follows deterministic package order")
	}
}
