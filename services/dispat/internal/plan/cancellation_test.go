// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// Cancellation is a planning outcome, not an afterthought. The planner reads
// one tag inventory per repository and one history listing per boundary. A
// backend may return after cancellation, so planning must not start another
// read from its answer.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// interruptingGitx ignores the context it is handed and cancels the run on
// its configured query. A planner must observe that cancellation itself.
type interruptingGitx struct {
	*fakeGit
	cancel    func()
	tagsAt    int
	commitsAt int
	mu        sync.Mutex
	tagCalls  int
	logCalls  int
}

func (g *interruptingGitx) TagsForPackages(_ context.Context, formats map[string]gitx.TagFormat) (map[string]gitx.Tags, error) {
	g.mu.Lock()
	g.tagCalls++
	reached := g.tagCalls == g.tagsAt
	g.mu.Unlock()
	if reached {
		g.cancel()
	}
	return g.fakeGit.TagsForPackages(context.Background(), formats)
}

func (g *interruptingGitx) Commits(_ context.Context, since string) ([]gitx.Commit, error) {
	g.mu.Lock()
	g.logCalls++
	reached := g.logCalls == g.commitsAt
	g.mu.Unlock()
	if reached {
		g.cancel()
	}
	return g.fakeGit.Commits(context.Background(), since)
}

func (g *interruptingGitx) calls() (tags, logs int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tagCalls, g.logCalls
}

// distinctBoundaryWorkspace gives every package its own stable tag on its own
// commit, so every package needs a history listing of its own and none of them
// share a window.
func distinctBoundaryWorkspace(packages int) ([]*model.Package, *fakeGit) {
	history := make([]commit, 0, packages)
	for i := range packages {
		history = append(history, commit{sha: fmt.Sprintf("c%03d", i),
			message: fmt.Sprintf("feat(pkg-%03d): change", i)})
	}
	git := newFakeGit(history...)
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%03d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		git = git.tag(name, "1.0.0", fmt.Sprintf("c%03d", i))
	}
	return pkgs, git
}

// TestCancellationStopsSchedulingHistoryWindows: the window loader reads one
// history listing per distinct boundary, sequentially. Once the caller has
// gone, it stops asking for the rest instead of walking a large workspace to
// the end on a backend that answers a dead context anyway.
func TestCancellationStopsSchedulingHistoryWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pkgs, history := distinctBoundaryWorkspace(64)
	git := &interruptingGitx{fakeGit: history, cancel: cancel, commitsAt: 4}

	_, err := Compute(ctx, git, Options{Packages: pkgs, Root: "/r"})

	require.ErrorIs(t, err, context.Canceled)
	_, logs := git.calls()
	assert.Equal(t, 4, logs,
		"no history listing is started after the run was cancelled")
}

// TestCancellationStopsRepositoryInventories: a backend returning an answer
// after cancellation cannot cause the next repository's refs to be read.
func TestCancellationStopsRepositoryInventories(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pkgs, history := distinctBoundaryWorkspace(64)
	for _, p := range pkgs {
		p.Repository = "control"
		p.RepoRoot = "/r"
	}
	pkgs = append(pkgs, &model.Package{Name: "remote", Dir: "/source/remote",
		RepoRoot: "/source", Repository: "source", Space: &model.Space{Name: "source"}})
	git := &interruptingGitx{fakeGit: history, cancel: cancel, tagsAt: 1}

	_, err := Compute(ctx, git, Options{Packages: pkgs, Root: "/r",
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/r", Git: git, Control: true},
			"source":  {Name: "source", Root: "/source", Git: git},
		}})

	require.ErrorIs(t, err, context.Canceled)
	tags, _ := git.calls()
	assert.Equal(t, 1, tags,
		"the planner reads the control inventory, then stops before the source inventory")
}
