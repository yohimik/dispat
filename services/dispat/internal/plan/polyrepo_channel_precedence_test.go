// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func composedChannelPlan(t *testing.T, appMessage string) *Plan {
	t.Helper()
	control := &composedControlGit{fakeGit: newFakeGit()}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{
			{Name: "a", Dir: "/workspace/a/packages/a", RepoRoot: "/workspace/a", Repository: "a-source", Space: &model.Space{Name: "a"}},
			{Name: "b", Dir: "/workspace/b/packages/b", RepoRoot: "/workspace/b", Repository: "b-source", Space: &model.Space{Name: "b"}},
			{Name: "app", Dir: "/workspace/app/packages/app", RepoRoot: "/workspace/app", Repository: "app-source", Space: &model.Space{Name: "app"}},
		},
		Dependencies: []model.Dependency{
			{Consumer: "app", Provider: "a"},
			{Consumer: "app", Provider: "b"},
		},
		Initials: map[string]ccme.Version{
			"a": v(1, 0, 0), "b": v(1, 0, 0), "app": v(1, 0, 0),
		},
		Repositories: map[string]RepositoryHistory{
			"control":    {Name: "control", Root: "/workspace", Git: control, Control: true},
			"a-source":   {Name: "a-source", Root: "/workspace/a", Path: "a", Git: newFakeGit(commit{sha: "a1", message: "release(a)%beta%%beta++1: propose beta"})},
			"b-source":   {Name: "b-source", Root: "/workspace/b", Path: "b", Git: newFakeGit(commit{sha: "b1", message: "release(b)%rc%%rc++1: propose rc"})},
			"app-source": {Name: "app-source", Root: "/workspace/app", Path: "app", Git: newFakeGit(commit{sha: "app1", message: appMessage})},
		},
	})
	require.NoError(t, err)
	return pl
}

func TestOwnerDirectChannelBeatsIncomparablePropagatedChannels(t *testing.T) {
	pl := composedChannelPlan(t, "release(app)%canary: choose the app channel")

	assert.False(t, hasCode(pl, CodeRepositoryPrecedence), "%v", pl.Diagnostics)
	assert.False(t, pl.Fatal(), "%v", pl.Diagnostics)
	assert.Equal(t, "canary", pl.Releases["app"].Channel)
	assert.Empty(t, pl.Releases["app"].ChannelFrom)
}

func TestNoopOwnerDirectChannelLeavesPropagatedConflict(t *testing.T) {
	pl := composedChannelPlan(t, "release(app)%beta>canary: unmatched app transition")

	assert.True(t, hasCode(pl, CodeRepositoryPrecedence), "%v", pl.Diagnostics)
	assert.True(t, pl.Fatal(), "%v", pl.Diagnostics)
}

func TestIncomparableDirectChannelsRemainConflicting(t *testing.T) {
	control := &composedControlGit{
		fakeGit: newFakeGit(),
		control: []gitx.ControlHistoryCommit{{
			SHA: "control1", Message: "release(app)%rc: choose control channel",
		}},
	}
	pl, err := Compute(context.Background(), control, Options{
		Packages: []*model.Package{{
			Name: "app", Dir: "/workspace/app/packages/app", RepoRoot: "/workspace/app",
			Repository: "app-source", Space: &model.Space{Name: "app"},
		}},
		Initials: map[string]ccme.Version{"app": v(1, 0, 0)},
		Repositories: map[string]RepositoryHistory{
			"control":    {Name: "control", Root: "/workspace", Git: control, Control: true},
			"app-source": {Name: "app-source", Root: "/workspace/app", Path: "app", Git: newFakeGit(commit{sha: "app1", message: "release(app)%beta: choose owner channel"})},
		},
	})
	require.NoError(t, err)

	assert.True(t, hasCode(pl, CodeRepositoryPrecedence), "%v", pl.Diagnostics)
	assert.True(t, pl.Fatal(), "%v", pl.Diagnostics)
}
