// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// mergedGit serves a history with merges from memory, union walk included
// (gitx.UnionHistoryx), so that what a benchmark over it measures is the
// planner's own work on a large union: the windows recovered from one walk,
// the owed windows, the marker passes. The real-history benchmarks pay a git
// process for every one of those reads, which hides it.
type mergedGit struct {
	*fakeGit
	parents [][]int // by index into history, oldest first
	at      map[string]int
	behind  map[int][]bool // ancestor-or-self sets IsAncestor has built
}

// listAfter is the union of the windows after the boundaries, newest first:
// the history without the commits every boundary is an ancestor-or-self of,
// which is what `git log` lists for HEAD --not their merge bases.
func (g *mergedGit) listAfter(boundaries []int) []gitx.Commit {
	excluded := g.behindAll(boundaries)
	var out []gitx.Commit
	for i := len(g.history) - 1; i >= 0; i-- {
		if excluded[i] {
			continue
		}
		c := g.history[i]
		parents := make([]string, len(g.parents[i]))
		for j, p := range g.parents[i] {
			parents[j] = g.history[p].sha
		}
		out = append(out, gitx.Commit{SHA: c.sha, Message: c.message, Files: c.files, Parents: parents})
	}
	return out
}

// behindAll marks the commits that are an ancestor-or-self of every
// boundary, one bit per boundary pushed from child to parent.
func (g *mergedGit) behindAll(boundaries []int) []bool {
	words := (len(boundaries) + 63) / 64
	masks := make([]uint64, len(g.history)*words)
	for j, b := range boundaries {
		masks[b*words+j>>6] |= 1 << (uint(j) & 63)
	}
	behind := make([]bool, len(g.history))
	for i := len(g.history) - 1; i >= 0; i-- {
		mask := masks[i*words : i*words+words]
		count := 0
		for _, word := range mask {
			count += bits.OnesCount64(word)
		}
		behind[i] = len(boundaries) > 0 && count == len(boundaries)
		for _, p := range g.parents[i] {
			for w, word := range mask {
				masks[p*words+w] |= word
			}
		}
	}
	return behind
}

func (g *mergedGit) resolve(rev string) (int, bool) {
	if sha, ok := g.tags[rev]; ok {
		rev = sha
	}
	i, ok := g.at[rev]
	return i, ok
}

func (g *mergedGit) Commits(_ context.Context, sinceTag string) ([]gitx.Commit, error) {
	if i, ok := g.resolve(sinceTag); ok {
		return g.listAfter([]int{i}), nil
	}
	return g.listAfter(nil), nil
}

func (g *mergedGit) CommitsSinceAny(_ context.Context, boundaries []string) ([]gitx.Commit, error) {
	var at []int
	for _, b := range boundaries {
		if b == "" {
			return g.listAfter(nil), nil
		}
		i, ok := g.at[b]
		if !ok {
			return nil, gitx.ErrBoundaryNotBehindHead
		}
		at = append(at, i)
	}
	return g.listAfter(at), nil
}

func (g *mergedGit) IsAncestor(_ context.Context, a, b string) (bool, error) {
	ia, okA := g.resolve(a)
	ib, okB := g.resolve(b)
	if !okA || !okB {
		return false, nil
	}
	if g.behind[ib] == nil {
		g.behind[ib] = g.behindAll([]int{ib})
	}
	return g.behind[ib][ia], nil
}

func (g *mergedGit) HeadSHA(context.Context) (string, error) {
	return g.history[len(g.history)-1].sha, nil
}

// mergedHistoryWorkspace is the real-history fixture's shape in memory: a main
// line that merges a short side branch every 32 commits, packages consuming
// package (i-1)/2, each released 8 times at its own commits, so the older
// releases are what the owed windows reach back to. The two oldest current
// releases sit on either side of one merge, neither behind the other, so no
// window is the whole union and the union is ranked from the windows rather
// than as read.
func mergedHistoryWorkspace(commits, packages int) ([]*model.Package, []model.Dependency, *mergedGit) {
	rng := rand.New(rand.NewPCG(uint64(commits), uint64(packages)))
	names := make([]string, packages)
	for i := range names {
		names[i] = fmt.Sprintf("pkg-%03d", i)
	}
	git := &mergedGit{fakeGit: newFakeGit(), at: make(map[string]int), behind: make(map[int][]bool)}
	add := func(message, path string, parents ...int) int {
		i := len(git.history)
		sha := fmt.Sprintf("%040x", i+1)
		git.history = append(git.history, commit{sha: sha, message: message, files: []string{path}})
		git.parents = append(git.parents, parents)
		git.at[sha] = i
		return i
	}
	change := func(i int) (string, string) {
		p := rng.IntN(packages)
		path := fmt.Sprintf("pkgs/%s/f%d.txt", names[p], rng.IntN(4))
		switch r := rng.IntN(100); {
		case r < 60:
			return fmt.Sprintf("fix(%s): change %d", names[p], i), path
		case r < 85:
			return fmt.Sprintf("fix: change %d from the files", i), path
		case r < 93:
			return fmt.Sprintf("feat(%s)^: change %d for the consumers", names[p], i), path
		default:
			return fmt.Sprintf("chore: housekeeping %d", i), path
		}
	}
	var line []int
	sideOf := make(map[int]int) // a merge's position on the main line -> its branch's first commit
	for len(git.history) < commits {
		i := len(line)
		if i >= 32 && i%32 == 0 {
			side := line[i-4]
			for k := range 2 {
				message, path := change(len(git.history))
				side = add(message, path, side)
				if k == 0 {
					sideOf[i] = side
				}
			}
			message, path := change(len(git.history))
			line = append(line, add("merge: "+message, path, line[i-1], side))
			continue
		}
		message, path := change(len(git.history))
		if i == 0 {
			line = append(line, add(message, path))
			continue
		}
		line = append(line, add(message, path, line[i-1]))
	}

	step := len(line) / 8
	merge := (7*step/32 + 1) * 32
	// Two packages nothing consumes, so neither release is owed to the other.
	git.tag(names[packages-2], "1.0.0", git.history[sideOf[merge]].sha)
	git.tag(names[packages-1], "1.0.0", git.history[line[merge-2]].sha)
	for _, name := range names[:packages-2] {
		offset := merge - 7*step + 8 + rng.IntN(step-(merge-7*step)-10)
		for k := 0; k < 8 && offset+k*step < len(line)-1; k++ {
			git.tag(name, fmt.Sprintf("1.%d.0", k), git.history[line[offset+k*step]].sha)
		}
	}

	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	var deps []model.Dependency
	for i, name := range names {
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		if i > 0 {
			deps = append(deps, model.Dependency{Consumer: name, Provider: names[(i-1)/2]})
		}
	}
	return pkgs, deps, git
}

// BenchmarkComputeMergedHistory plans over a 20,000-commit history with merges
// held in memory, through the union walk, with and without the composed
// workspace's per-repository windows. No git process runs, so the timing and
// allocations are the planner's own: the windows recovered from the walk, the
// owed windows read past the union, the marker passes that decide both. It
// reports the window memberships the plan counts (windowrefs/op) beside the
// heap.
func BenchmarkComputeMergedHistory(b *testing.B) {
	for _, isComposed := range []bool{false, true} {
		b.Run(fmt.Sprintf("composed=%v", isComposed), func(b *testing.B) {
			pkgs, deps, git := mergedHistoryWorkspace(20_000, 128)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r", HistoryStats: stats}
			if isComposed {
				for _, p := range pkgs {
					p.Repository, p.RepoRoot = "source", "/r"
				}
				opts.Repositories = map[string]RepositoryHistory{"source": {Name: "source", Root: "/r", Git: git}}
			}
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			b.ReportMetric(float64(stats.CommitWindows.Load())/float64(iterations), "windows/op")
			b.ReportMetric(float64(stats.WindowCommitRefs.Load())/float64(iterations), "windowrefs/op")
			reportPeakHeap(b, func() any {
				pl, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps,
					Root: "/r", Repositories: opts.Repositories})
				if err != nil {
					b.Fatal(err)
				}
				return pl
			})
		})
	}
}
