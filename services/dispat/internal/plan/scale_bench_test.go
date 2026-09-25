// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// Scale benchmarks for the shapes a large workspace actually takes: many
// packages over one history, a deep dependency graph, a tag inventory of
// thousands of tags, many versioning groups, and a composed fleet. Each one
// reports the Git query count beside the timing, because the number that
// decides a real run's latency is how many git subprocesses planning spawns
// rather than how fast the in-process work is.
//
// Every fixture is built from the deterministic fakes in plan_test.go, so the
// measurements are repeatable and the benchmarks run in CI without a
// repository.

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// gitOps totals one fixture's Git queries: the tag listings, the history
// listings, and the bulk inventories that replace a listing per package. In
// the CLI each one is a git subprocess.
type gitOps struct{ logs, bulk int }

func (o gitOps) add(other gitOps) gitOps {
	return gitOps{logs: o.logs + other.logs, bulk: o.bulk + other.bulk}
}

func (o gitOps) sub(other gitOps) gitOps {
	return gitOps{logs: o.logs - other.logs, bulk: o.bulk - other.bulk}
}

// ops totals the queries recorded so far.
func (c *countingGit) ops() gitOps {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := gitOps{bulk: c.bulkQueries}
	for _, n := range c.logQueries {
		out.logs += n
	}
	return out
}

// reportGitOps turns the fixture's totals into per-operation metrics beside
// ns/op, so a change that trades in-process work for an extra subprocess per
// package is visible in the benchmark stream rather than only in a profile.
func reportGitOps(b *testing.B, iterations int, delta gitOps, stats *HistoryStats) {
	b.Helper()
	if iterations <= 0 {
		return
	}
	per := float64(iterations)
	b.ReportMetric(float64(delta.logs+delta.bulk)/per, "gitcalls/op")
	b.ReportMetric(float64(delta.logs)/per, "gitlogs/op")
	if stats != nil {
		b.ReportMetric(float64(stats.CommitWindows.Load())/per, "windows/op")
		b.ReportMetric(float64(stats.WindowCommitRefs.Load())/per, "windowrefs/op")
	}
}

// reportRetainedHeap measures what a finished plan keeps live, once and
// outside the timed loop. An allocation total cannot answer whether a fleet
// fits in memory; the heap still reachable from the returned plan can.
func reportRetainedHeap(b *testing.B, build func() any) {
	b.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	kept := build()
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained < 0 {
		retained = 0
	}
	b.ReportMetric(float64(retained)/(1<<20), "retained_MiB")
	runtime.KeepAlive(kept)
}

// linearHistory is a workspace-wide history whose commits address packages in
// turn, the way a monorepo's log reads.
func linearHistory(commits, packages int) []commit {
	history := make([]commit, 0, commits+1)
	history = append(history, commit{sha: "base", message: "chore: base"})
	for i := range commits {
		name := fmt.Sprintf("pkg-%04d", i%packages)
		history = append(history, commit{
			sha:     fmt.Sprintf("c%06d", i),
			message: fmt.Sprintf("feat(%s): change %d", name, i),
			files:   []string{"pkgs/" + name + "/main.go"},
		})
	}
	return history
}

// sharedHistoryWorkspace releases every package at one commit, so every
// package's pending window starts at the same boundary. This is the shape the
// shared-window cache exists for: one history listing for the whole
// workspace rather than one per package.
func sharedHistoryWorkspace(packages, commits int) ([]*model.Package, *countingGit) {
	git := newFakeGit(linearHistory(commits, packages)...)
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%04d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		git = git.tag(name, "1.0.0", "base")
	}
	return pkgs, counted(git)
}

// BenchmarkComputeSharedHistory measures many packages over one history. The
// Git call count must stay flat as the workspace grows: one bulk tag
// inventory and one history listing, whatever the package count.
func BenchmarkComputeSharedHistory(b *testing.B) {
	for _, packages := range []int{128, 512} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			pkgs, git := sharedHistoryWorkspace(packages, 512)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Root: "/r", HistoryStats: stats}
			before := git.ops()
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			reportGitOps(b, iterations, git.ops().sub(before), stats)
			reportRetainedHeap(b, func() any {
				pl, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
				if err != nil {
					b.Fatal(err)
				}
				return pl
			})
		})
	}
}

// dependencyGraphWorkspace builds a balanced provider tree: package i provides
// for 2i+1 and 2i+2, so the closure directive at the root has to expand every
// package exactly once through log2(n) levels of edges.
func dependencyGraphWorkspace(packages int) ([]*model.Package, []model.Dependency, *countingGit) {
	git := newFakeGit(
		commit{sha: "base", message: "chore: base"},
		commit{sha: "root", message: "feat(pkg-0000)^^: the closure walks the whole graph",
			files: []string{"pkgs/pkg-0000/main.go"}},
	)
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	deps := make([]model.Dependency, 0, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%04d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		git = git.tag(name, "1.0.0", "base")
		if i > 0 {
			deps = append(deps, model.Dependency{
				Consumer: name, Provider: fmt.Sprintf("pkg-%04d", (i-1)/2)})
		}
	}
	return pkgs, deps, counted(git)
}

// BenchmarkComputeDependencyGraph measures the graph work: the topological
// sort, the three propagation phases and the ordering, over hundreds to
// thousands of packages reached by one closure directive.
func BenchmarkComputeDependencyGraph(b *testing.B) {
	for _, packages := range []int{256, 1024, 4096} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			pkgs, deps, git := dependencyGraphWorkspace(packages)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r", HistoryStats: stats}
			pl, err := Compute(context.Background(), git, opts)
			if err != nil {
				b.Fatal(err)
			}
			if len(pl.Releasing()) != packages {
				b.Fatalf("closure reached %d of %d packages", len(pl.Releasing()), packages)
			}
			before := git.ops()
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			reportGitOps(b, iterations, git.ops().sub(before), stats)
		})
	}
}

// tagInventoryWorkspace gives every package a long release line, so baseline
// selection and the alias filter run over thousands of tags.
func tagInventoryWorkspace(packages, versions int) ([]*model.Package, *countingGit) {
	git := newFakeGit(linearHistory(64, packages)...)
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%04d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		for v := range versions {
			git = git.tag(name, fmt.Sprintf("1.%d.0", v), "base")
		}
	}
	return pkgs, counted(git)
}

// BenchmarkComputeTagInventory measures planning over a large tag inventory:
// hundreds of packages carrying thousands of tags between them.
func BenchmarkComputeTagInventory(b *testing.B) {
	for _, size := range []struct{ packages, versions int }{{256, 16}, {512, 8}} {
		b.Run(fmt.Sprintf("packages=%d/versions=%d", size.packages, size.versions), func(b *testing.B) {
			pkgs, git := tagInventoryWorkspace(size.packages, size.versions)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Root: "/r", HistoryStats: stats}
			before := git.ops()
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			reportGitOps(b, iterations, git.ops().sub(before), stats)
		})
	}
}

// versionGroupWorkspace spreads the packages over several versioning groups,
// which is the shape a fleet of independently versioned product lines takes.
func versionGroupWorkspace(groups, members int) ([]*model.Package, *countingGit) {
	git := newFakeGit(
		commit{sha: "base", message: "chore: base"},
		commit{sha: "move", message: "feat(pkg-0000-000)!: moves one group",
			files: []string{"pkgs/pkg-0000-000/main.go"}},
	)
	pkgs := make([]*model.Package, 0, groups*members)
	for g := range groups {
		space := &model.Space{Name: fmt.Sprintf("group-%03d", g), Versioning: model.VersioningFixed}
		for m := range members {
			name := fmt.Sprintf("pkg-%04d-%03d", g, m)
			pkgs = append(pkgs, &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space})
			git = git.tag(name, fmt.Sprintf("1.%d.0", m%8), "base")
		}
	}
	return pkgs, counted(git)
}

// BenchmarkComputeVersionGroups measures the group aggregate over many groups
// rather than one large one: the per-group pass must not become quadratic in
// the workspace.
func BenchmarkComputeVersionGroups(b *testing.B) {
	for _, size := range []struct{ groups, members int }{{16, 16}, {64, 16}} {
		b.Run(fmt.Sprintf("groups=%d/members=%d", size.groups, size.members), func(b *testing.B) {
			pkgs, git := versionGroupWorkspace(size.groups, size.members)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Root: "/r", HistoryStats: stats}
			before := git.ops()
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			reportGitOps(b, iterations, git.ops().sub(before), stats)
		})
	}
}

// fleetWorkspace composes a control repository over several source
// repositories, each with its own packages, history and tag inventory.
func fleetWorkspace(repositories, perRepository, commits int) (
	[]*model.Package, []model.Dependency, map[string]RepositoryHistory,
	*composedControlGit, []*countingGit, map[string]ccme.Version) {

	controlCommits := make([]gitx.ControlHistoryCommit, 0, 256)
	for i := range 256 {
		sha := fmt.Sprintf("%040x", 256-i)
		entry := gitx.ControlHistoryCommit{SHA: sha, Message: "chore: control",
			Gitlinks: map[string]gitx.GitlinkTransition{}}
		if i+1 < 256 {
			entry.Parents = []string{fmt.Sprintf("%040x", 255-i)}
		}
		controlCommits = append(controlCommits, entry)
	}
	control := &composedControlGit{fakeGit: newFakeGit(), control: controlCommits}

	histories := map[string]RepositoryHistory{
		"control": {Name: "control", Root: "/fleet", Git: control, Control: true},
	}
	var (
		pkgs     []*model.Package
		deps     []model.Dependency
		sources  []*countingGit
		initials = map[string]ccme.Version{}
	)
	space := &model.Space{Name: "fleet"}
	for r := range repositories {
		repository := fmt.Sprintf("source-%02d", r)
		history := make([]commit, 0, commits)
		for i := range commits {
			history = append(history, commit{
				sha:     fmt.Sprintf("%s-c%05d", repository, i),
				message: fmt.Sprintf("feat(%s-pkg-%03d)^: change %d", repository, i%perRepository, i),
				files:   []string{fmt.Sprintf("pkg-%03d/main.go", i%perRepository)},
			})
		}
		git := counted(newFakeGit(history...))
		sources = append(sources, git)
		histories[repository] = RepositoryHistory{
			Name: repository, Root: "/fleet/" + repository, Path: repository, Git: git}
		for p := range perRepository {
			name := fmt.Sprintf("%s-pkg-%03d", repository, p)
			pkgs = append(pkgs, &model.Package{Name: name, Dir: "/fleet/" + repository + "/pkg",
				RepoRoot: "/fleet/" + repository, Repository: repository, Space: space})
			initials[name] = ccme.Version{Major: 1}
			if r > 0 {
				deps = append(deps, model.Dependency{Consumer: name,
					Provider: fmt.Sprintf("source-%02d-pkg-%03d", r-1, p)})
			}
		}
	}
	return pkgs, deps, histories, control, sources, initials
}

// BenchmarkComputeFleetSnapshot measures a composed polyrepository plan: the
// control gitlink history, the per-repository checkpoints, the cross-repository
// windows, and the repository input closures the release validation reads.
func BenchmarkComputeFleetSnapshot(b *testing.B) {
	for _, size := range []struct{ repositories, perRepository, commits int }{
		{4, 16, 128},
		{16, 16, 128},
	} {
		name := fmt.Sprintf("repositories=%d/packages=%d", size.repositories, size.repositories*size.perRepository)
		b.Run(name, func(b *testing.B) {
			pkgs, deps, histories, control, sources, initials :=
				fleetWorkspace(size.repositories, size.perRepository, size.commits)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Dependencies: deps, Initials: initials,
				Repositories: histories, HistoryStats: stats}
			var before gitOps
			for _, source := range sources {
				before = before.add(source.ops())
			}
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), control, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			var after gitOps
			for _, source := range sources {
				after = after.add(source.ops())
			}
			reportGitOps(b, iterations, after.sub(before), stats)
			reportRetainedHeap(b, func() any {
				pl, err := Compute(context.Background(), control, Options{Packages: pkgs,
					Dependencies: deps, Initials: initials, Repositories: histories})
				if err != nil {
					b.Fatal(err)
				}
				return pl
			})
		})
	}
}

// chainWorkspace is the topology that makes graph traversals largest: package
// i consumes package i-1, so the walk from package i reaches every package
// after it, and one unit per package asks for a walk from every package. Every
// package is released at one base commit, so the owed windows are examined
// for every (provider, consumer) pair the chain holds.
func chainWorkspace(packages int) ([]*model.Package, []model.Dependency, *fakeGit) {
	history := []commit{{sha: "base", message: "chore: base"}}
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	deps := make([]model.Dependency, 0, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%04d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		history = append(history, commit{sha: fmt.Sprintf("c%06d", i),
			message: fmt.Sprintf("fix(%s)^: change for the next package", name),
			files:   []string{"pkgs/" + name + "/main.go"}})
		if i > 0 {
			deps = append(deps, model.Dependency{Consumer: name, Provider: fmt.Sprintf("pkg-%04d", i-1)})
		}
	}
	git := newFakeGit(history...)
	for _, p := range pkgs {
		git = git.tag(p.Name, "1.0.0", "base")
	}
	return pkgs, deps, git
}

// BenchmarkComputeChainTopology measures the graph caches of §13.11 where they
// are largest: a 4,096-package chain, where the unbounded walks the owed pairs
// and the units share hold a quadratic number of targets between them. What a
// cache keeps is dropped with the computation, so it never shows in
// retained_MiB; the peak heap is what the cache bound is about.
func BenchmarkComputeChainTopology(b *testing.B) {
	for _, packages := range []int{1024, 4096} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			pkgs, deps, git := chainWorkspace(packages)
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r"}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			reportPeakHeap(b, func() any {
				pl, err := Compute(context.Background(), git, opts)
				if err != nil {
					b.Fatal(err)
				}
				return pl
			})
		})
	}
}
