// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// Cancellation is a planning outcome, not an afterthought. The planner reads
// one tag listing and one history listing per boundary, and a Git
// implementation is free to serve those from a cache, a fixture or an API
// that has no context to honour. Planning therefore has to stop scheduling
// work itself rather than rely on every backend to refuse it, which is what
// these tests hold it to.

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// interruptingGitx serves history exactly as fakeGit does and ignores the
// context it is handed, which is what a lightweight backend looks like. It
// cancels the run itself on its nth query, so the count of queries that
// follow the cancellation is the thing under test rather than a race.
type interruptingGitx struct {
	*fakeGit
	cancel    func()
	tagsAt    int
	commitsAt int
	// The legacy tag fallback queries packages concurrently, so the counters
	// take a lock — which also makes "the nth call cancels" exact rather than
	// a race between two callers reading the same value.
	mu       sync.Mutex
	tagCalls int
	logCalls int
}

func (g *interruptingGitx) Tags(_ context.Context, pkg string, format gitx.TagFormat) (gitx.Tags, error) {
	g.mu.Lock()
	g.tagCalls++
	reached := g.tagCalls == g.tagsAt
	g.mu.Unlock()
	if reached {
		g.cancel()
	}
	return g.fakeGit.Tags(context.Background(), pkg, format)
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

// TestCancellationStopsSchedulingRepositoryTagQueries: the composed loader's
// per-package tag fallback is the same story on the tag axis. A repository
// whose backend offers no bulk inventory gets one query per package, and an
// interrupted run must not walk the whole workspace issuing them.
func TestCancellationStopsSchedulingRepositoryTagQueries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pkgs, history := distinctBoundaryWorkspace(64)
	for _, p := range pkgs {
		p.Repository = "control"
		p.RepoRoot = "/r"
	}
	git := &interruptingGitx{fakeGit: history, cancel: cancel, tagsAt: 3}

	_, err := Compute(ctx, git, Options{Packages: pkgs, Root: "/r",
		Repositories: map[string]RepositoryHistory{
			"control": {Name: "control", Root: "/r", Git: git, Control: true},
		}})

	require.ErrorIs(t, err, context.Canceled)
	tags, _ := git.calls()
	assert.Equal(t, 3, tags,
		"no tag query is started after the run was cancelled")
}

// TestRepeatedCancelledPlansLeaveNoGoroutines: the bounded concurrent tag
// fallback starts goroutines per package. A run cancelled mid-flight joins
// them before returning, so repeating it — which is what a retrying CI job
// does — accumulates nothing.
func TestRepeatedCancelledPlansLeaveNoGoroutines(t *testing.T) {
	pkgs, history := groupWorkspace(256, model.VersioningFixed)
	settle := func() int {
		// Goroutines from an earlier test may still be exiting. Read the
		// count once it has been stable for a moment rather than at an
		// arbitrary instant, which is what makes this assertion reliable.
		last := runtime.NumGoroutine()
		for range 100 {
			time.Sleep(5 * time.Millisecond)
			now := runtime.NumGoroutine()
			if now == last {
				return now
			}
			last = now
		}
		return last
	}
	before := settle()
	for range 8 {
		ctx, cancel := context.WithCancel(context.Background())
		git := &interruptingGitx{fakeGit: history, cancel: cancel, tagsAt: 5}
		_, err := Compute(ctx, git, Options{Packages: pkgs, Root: "/r"})
		require.ErrorIs(t, err, context.Canceled)
		cancel()
	}
	after := settle()
	assert.LessOrEqual(t, after, before+2,
		"cancelled planning runs leaked goroutines: %d before, %d after", before, after)
}
