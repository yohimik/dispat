// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// BenchmarkComputeShallowChain is appendix B.7a's cost workload: a chain with
// one depth-one unit per package and no release yet, so no owed pair asks for
// an unbounded walk and the units' own walks are all the propagation does.
// The literal reading reaches P - 1 targets in total; walkTargets/op is how
// many the planner computed to answer it.
func BenchmarkComputeShallowChain(b *testing.B) {
	for _, packages := range []int{1024, 4096} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			pkgs, deps, git := chainWorkspace(packages)
			git.tags, git.tagsFor = map[string]string{}, map[string][]string{}
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r", HistoryStats: stats}
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			b.ReportMetric(float64(stats.WalkTargets.Load())/float64(iterations), "walkTargets/op")
			reportPeakHeap(b, func() any {
				pl, err := Compute(context.Background(), git, Options{Packages: pkgs, Dependencies: deps, Root: "/r"})
				if err != nil {
					b.Fatal(err)
				}
				return pl
			})
		})
	}
}

// BenchmarkComputeMixedDepthChain is BenchmarkComputeShallowChain with two
// units per package, one written "^^" and one "^", and no release yet. Both
// ask a bounded walk of the same source, and walkTargets/op is how many
// targets the planner computed to answer the two.
func BenchmarkComputeMixedDepthChain(b *testing.B) {
	for _, packages := range []int{1024, 4096} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			pkgs, deps, git := mixedDepthChainWorkspace(packages)
			stats := &HistoryStats{}
			opts := Options{Packages: pkgs, Dependencies: deps, Root: "/r", HistoryStats: stats}
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
				iterations++
			}
			b.StopTimer()
			b.ReportMetric(float64(stats.WalkTargets.Load())/float64(iterations), "walkTargets/op")
		})
	}
}

// mixedDepthChainWorkspace is a chain whose every package has a "^" unit
// and, newer, a "^patch+2" one, and no release tag.
func mixedDepthChainWorkspace(packages int) ([]*model.Package, []model.Dependency, *fakeGit) {
	history := []commit{{sha: "base", message: "chore: base"}}
	space := &model.Space{Name: "workspace"}
	pkgs := make([]*model.Package, packages)
	deps := make([]model.Dependency, 0, packages)
	for i := range pkgs {
		name := fmt.Sprintf("pkg-%04d", i)
		pkgs[i] = &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space}
		files := []string{"pkgs/" + name + "/main.go"}
		history = append(history,
			commit{sha: fmt.Sprintf("c%06d", 2*i), message: fmt.Sprintf("fix(%s)^: one level", name), files: files},
			commit{sha: fmt.Sprintf("c%06d", 2*i+1), message: fmt.Sprintf("fix(%s)^patch+2: two levels", name), files: files})
		if i > 0 {
			deps = append(deps, model.Dependency{Consumer: name, Provider: fmt.Sprintf("pkg-%04d", i-1)})
		}
	}
	return pkgs, deps, newFakeGit(history...)
}

// BenchmarkCanonicalDiagnostics sorts a plan's diagnostics into the digest's
// canonical order, at the size a lenient parse of a long window reaches.
func BenchmarkCanonicalDiagnostics(b *testing.B) {
	for _, count := range []int{5_000, 50_000} {
		b.Run(fmt.Sprintf("diagnostics=%d", count), func(b *testing.B) {
			rng := rand.New(rand.NewPCG(1, uint64(count)))
			diagnostics := make([]Diagnostic, count)
			for i := range diagnostics {
				diagnostics[i] = Diagnostic{Code: fmt.Sprintf("W%03d", 100+rng.IntN(100)),
					Pkg: fmt.Sprintf("pkg-%03d", rng.IntN(64)), Commit: fmt.Sprintf("%016x%016x", rng.Uint64(), rng.Uint64())}
			}
			b.ReportAllocs()
			for b.Loop() {
				canonicalDiagnostics(diagnostics)
			}
		})
	}
}

// linkPathHistories is a fleet linked as one path, r0 - r1 - ... - rN, the
// topology whose routes are longest.
func linkPathHistories(repositories int) map[string]RepositoryHistory {
	histories := make(map[string]RepositoryHistory, repositories)
	for i := 0; i < repositories; i++ {
		links := map[string]string{}
		if i > 0 {
			links["r"+itoa(i-1)] = ".links/r" + itoa(i-1)
		}
		if i+1 < repositories {
			links["r"+itoa(i+1)] = ".links/r" + itoa(i+1)
		}
		histories["r"+itoa(i)] = RepositoryHistory{Name: "r" + itoa(i), Links: links}
	}
	return histories
}

// BenchmarkLinkRoute routes from a path fleet's entry to every repository of
// it, which is what a projection of one revision across the fleet asks.
func BenchmarkLinkRoute(b *testing.B) {
	for _, repositories := range []int{16, 64} {
		b.Run(fmt.Sprintf("repositories=%d", repositories), func(b *testing.B) {
			histories := linkPathHistories(repositories)
			b.ReportAllocs()
			for b.Loop() {
				cp := &computation{histories: histories}
				for i := 1; i < repositories; i++ {
					if route := cp.linkRoute("r0", "r"+itoa(i)); len(route) != i+1 {
						b.Fatalf("route to r%d has %d hops", i, len(route))
					}
				}
			}
		})
	}
}

// BenchmarkLinkEvidenceIndex indexes the release subjects of a repository
// whose packages carry long release lines; revisions/op is how many commits
// the subject read asks Git about.
func BenchmarkLinkEvidenceIndex(b *testing.B) {
	const packages, releases = 64, 256
	git := newLinkedGit("c0")
	cp := &computation{ctx: context.Background(), tags: map[string]gitx.Tags{},
		stableTags: map[string]gitx.Tag{}, latestTags: map[string]gitx.Tag{},
		histories: map[string]RepositoryHistory{"source": {Name: "source", Git: git}}}
	for p := 0; p < packages; p++ {
		name := fmt.Sprintf("pkg-%03d", p)
		cp.pkgs = append(cp.pkgs, &model.Package{Name: name, Repository: "source"})
		var tags gitx.Tags
		for r := releases - 1; r >= 0; r-- {
			commit := fmt.Sprintf("%020x%020x", p, r)
			tag := fmt.Sprintf("%s@1.%d.0", name, r)
			git.records(commit, "chore(release): "+tag)
			tags = append(tags, gitx.Tag{Name: tag, Commit: commit, Parsed: true})
		}
		cp.tags[name] = tags
		cp.stableTags[name], cp.latestTags[name] = tags[0], tags[0]
	}
	before := git.revisions
	iterations := 0
	b.ReportAllocs()
	for b.Loop() {
		if err := newLinkEvidence(cp).index(); err != nil {
			b.Fatal(err)
		}
		iterations++
	}
	b.StopTimer()
	b.ReportMetric(float64(git.revisions-before)/float64(iterations), "revisions/op")
}
