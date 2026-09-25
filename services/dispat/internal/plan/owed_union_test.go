// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// withTrains puts some packages of an owed-window repository on a prerelease
// train: a prerelease tag on a commit after the package's stable release, so
// its baseline is a tag that is no window boundary of its own.
func withTrains(t *testing.T, git *gitx.LocalGitx, pkgs []*model.Package, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", git.Dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	reachable := strings.Fields(run("rev-list", "--reverse", "--topo-order", "HEAD"))
	for _, p := range pkgs {
		if rng.Intn(2) == 0 {
			continue
		}
		tags, err := git.Tags(context.Background(), p.Name, gitx.DefaultTagFormat)
		require.NoError(t, err)
		stable, ok := tags.StableBaseline()
		require.True(t, ok)
		at := 0
		for i, sha := range reachable {
			if sha == stable.Commit {
				at = i
			}
		}
		if at+1 >= len(reachable) {
			continue
		}
		run("tag", fmt.Sprintf("%s@9.0.0-rc.0", p.Name), reachable[at+1+rng.Intn(len(reachable)-at-1)])
	}
}

// TestOwedWindowsReadAsOneWalkPlanAsReadOneByOne is the byte-identity claim
// for reading every owed window in one union walk, and for answering a train
// consumer's delivery question from the marker index: over histories with
// merges, where consumers got ahead of their providers and some sit on
// prerelease trains, the plan equals the one read a window per boundary with
// git answering every ancestry question, for a single history and a composed
// one. The single walk starts no more git processes than the reads it
// replaces.
func TestOwedWindowsReadAsOneWalkPlanAsReadOneByOne(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real repositories")
	}
	ctx := context.Background()
	fewer := 0
	for seed := int64(1); seed <= 10; seed++ {
		git, pkgs, deps := randomOwedRepository(t, seed)
		withTrains(t, git, pkgs, seed)
		for _, isComposed := range []bool{false, true} {
			plan := func(history TagInventoryGitx) (*Plan, uint64) {
				opts := Options{Packages: pkgs, Dependencies: deps, Root: git.Dir}
				if isComposed {
					opts.Repositories = map[string]RepositoryHistory{
						"source": {Name: "source", Root: git.Dir, Git: history}}
				}
				before := gitx.GitInvocations()
				p, err := Compute(ctx, history, opts)
				require.NoError(t, err)
				return p, gitx.GitInvocations() - before
			}
			if isComposed {
				for _, p := range pkgs {
					p.Repository, p.RepoRoot = "source", git.Dir
				}
			}
			want, perBoundary := plan(perBoundaryGit{inner: git})
			got, single := plan(git)
			assert.Equalf(t, want.Order, got.Order, "seed %d composed %v", seed, isComposed)
			assert.Equalf(t, want.Diagnostics, got.Diagnostics, "seed %d composed %v", seed, isComposed)
			require.Equalf(t, len(want.Releases), len(got.Releases), "seed %d composed %v", seed, isComposed)
			for name, w := range want.Releases {
				assert.Equalf(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])),
					"seed %d composed %v: release of %s", seed, isComposed, name)
			}
			assert.LessOrEqualf(t, single, perBoundary, "seed %d composed %v", seed, isComposed)
			if single < perBoundary {
				fewer++
			}
			for _, p := range pkgs {
				p.Repository, p.RepoRoot = "", ""
			}
		}
	}
	assert.Positive(t, fewer)
}

// TestOwedWindowsStopAtTheWholeHistory: once an owed window is the whole
// history, it holds every other one, and no further window is read.
func TestOwedWindowsStopAtTheWholeHistory(t *testing.T) {
	// app released at c2, after utils's first release and before core ever
	// released: its owed window from core is the whole history, and the one
	// from utils starts at utils@1.0.0, which is nobody's ordinary window.
	counting := counted(newFakeGit(
		commit{sha: "c1", message: "feat(utils): first utils"},
		commit{sha: "c2", message: "feat(app): first app"},
		commit{sha: "c3", message: "fix(utils)^: second utils"},
		commit{sha: "c4", message: "feat(core)^: first core"},
		commit{sha: "c5", message: "chore: tail"},
	).tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c2").tag("utils", "1.0.1", "c3").
		tag("core", "1.0.0", "c4"))
	p := compute(t, counting, nil)
	require.False(t, p.IsInvalid(), "%v", codes(p))
	require.Equal(t, 1, counting.logQueries[""], "the whole history is an owed window here")
	assert.Zero(t, counting.logQueries["utils@1.0.0"], "a window the whole history holds is not read after it")
}
