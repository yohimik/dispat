// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// TestLocalCommitsAreKeptAsListed pins the allocations of indexing one window
// of a real repository: LocalGitx's commits own their strings already, so a
// window's canonical commit copies only its text fields, the ones a plan
// keeps, and keeps its parents and paths as listed. A copy of every field
// costs two allocations more per commit of one parent and no paths, the
// shape here, and more with every path. Any other implementation's commits
// are still copied whole.
func TestLocalCommitsAreKeptAsListed(t *testing.T) {
	const commits = 200
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "dev")
	run("config", "user.email", "dev@example.com")
	for i := 0; i < commits; i++ {
		run("commit", "-q", "--allow-empty", "-m", fmt.Sprintf("fix(core): change %d", i))
	}
	local := &gitx.LocalGitx{Dir: dir}
	listing, err := local.Commits(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, listing, commits)

	allocations := func(git gitx.Gitx) float64 {
		cp := &computation{ctx: context.Background(), log: zerolog.Nop()}
		history := RepositoryHistory{Name: "source", Root: dir, Git: git.(TagInventoryGitx)}
		return testing.AllocsPerRun(5, func() {
			idx := newWindowIndex()
			idx.unions[historyKey("source", "")] = unionWindow{all: listing}
			_, _, err := cp.load(idx, history, historyKey("source", ""), "core")
			require.NoError(t, err)
		})
	}
	kept, copied := allocations(local), allocations(perBoundaryGit{inner: local})
	require.LessOrEqual(t, kept, float64(7*commits), "a listed commit's parents are kept, not copied")
	require.GreaterOrEqual(t, copied, kept+float64(commits), "another implementation's commits are copied")
}

// TestOverlappingWindowsShareTheirKeys pins what a window costs whose
// commits another window of its repository indexed already: their keys are
// the ones interned then, looked up without being built again, and the
// window is read off the union walk in place, so it allocates its own key
// list and membership set and nothing per commit.
func TestOverlappingWindowsShareTheirKeys(t *testing.T) {
	const commits = 400
	git := linearMergedGit(commits)
	listing, err := git.Commits(context.Background(), "")
	require.NoError(t, err)
	cp := &computation{ctx: context.Background(), log: zerolog.Nop()}
	history := RepositoryHistory{Name: "source", Root: "/r", Git: git}
	load := func(boundaries ...string) {
		idx := newWindowIndex()
		for _, boundary := range boundaries {
			idx.unions[historyKey("source", boundary)] = unionWindow{all: listing}
			_, _, err := cp.load(idx, history, historyKey("source", boundary), "core")
			require.NoError(t, err)
		}
	}
	one := testing.AllocsPerRun(5, func() { load("") })
	two := testing.AllocsPerRun(5, func() { load("", "c00") })
	require.LessOrEqual(t, two-one, float64(commits/8), "a second window over the same commits allocates nothing per commit")
}
