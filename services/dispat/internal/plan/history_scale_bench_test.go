// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// Real-history benchmarks: planning over a repository git itself writes, so
// that what is measured is what a run pays. The fakes in plan_test.go answer
// every history question in-process, which is right for the planner's logic
// and blind to its cost: the git processes it forks, the bytes they write and
// the commits git diffs to list changed paths. Only a real repository shows
// those, and they are what decides how long `dispat status` takes.
//
// The fixture is written by `git fast-import` into the benchmark's own
// temporary folder, once per sub-benchmark, and nothing of it is kept in the
// repository. A plain `go test` never builds one: the benchmarks run only
// under -bench.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// scaleShape is one fixture: how many commits, how many packages, and whether
// a `versioning: none` package is present. A none package never has a stable
// tag, so its pending window, and with it the union every package's window is
// read from, is the whole history (§13.3). Without one, the union starts at the
// oldest current release, and the older releases are what the owed windows of
// §13.3 reach back to.
type scaleShape struct {
	name      string
	commits   int
	packages  int
	isNoneSet bool
	// isComposed plans the repository as the one source of a composed
	// workspace (§27), whose windows are read per repository.
	isComposed bool
}

// scaleRepo is a built fixture: the repository folder and the workspace that
// plans over it.
type scaleRepo struct {
	dir  string
	pkgs []*model.Package
	deps []model.Dependency
	tags int
}

// scaleMergeEvery is how often a main-line commit merges a side branch, and
// scaleSideLength how many commits that branch carries.
const (
	scaleMergeEvery = 32
	scaleSideLength = 2
	scaleReleases   = 8
)

// buildScaleRepo writes the fixture. Every choice is drawn from a fixed seed,
// so two runs, and two revisions, plan over byte-identical histories.
//
// The history mixes what a monorepo's log holds: units scoped to a package,
// scopeless units whose packages come from the changed files (§6.2), units
// that propagate, housekeeping, co-authored commits and merges of short side
// branches. Every package is released several times at its own commits, the
// latest release of each at a distinct commit, so the plan has one window per
// package; every eighth package is on a prerelease train, so its baseline is a
// prerelease tag after its stable one. Package i consumes package (i-1)/2.
func buildScaleRepo(b *testing.B, shape scaleShape) scaleRepo {
	b.Helper()
	dir := b.TempDir()
	rng := rand.New(rand.NewPCG(uint64(shape.commits), uint64(shape.packages)))
	names := make([]string, shape.packages)
	for i := range names {
		names[i] = fmt.Sprintf("pkg-%02d", i)
	}

	var stream bytes.Buffer
	w := bufio.NewWriter(&stream)
	mark, stamp := 0, int64(1_600_000_000)
	commitAt := func(branch string, from, merge int, message, path string) int {
		mark++
		stamp += 60
		author := fmt.Sprintf("dev-%02d", rng.IntN(24))
		fmt.Fprintf(w, "commit refs/heads/%s\nmark :%d\n", branch, mark)
		fmt.Fprintf(w, "author %s <%s@example.com> %d +0000\n", author, author, stamp)
		fmt.Fprintf(w, "committer %s <%s@example.com> %d +0000\n", author, author, stamp)
		fmt.Fprintf(w, "data %d\n%s\n", len(message), message)
		if from > 0 {
			fmt.Fprintf(w, "from :%d\n", from)
		}
		if merge > 0 {
			fmt.Fprintf(w, "merge :%d\n", merge)
		}
		content := fmt.Sprintf("change %d\n", mark)
		fmt.Fprintf(w, "M 100644 inline %s\ndata %d\n%s\n", path, len(content), content)
		return mark
	}
	change := func(i int) (message, path string) {
		p := rng.IntN(shape.packages)
		path = fmt.Sprintf("pkgs/%s/f%d.txt", names[p], rng.IntN(4))
		if shape.isNoneSet && rng.IntN(20) == 0 {
			path = fmt.Sprintf("tools/t%d.txt", rng.IntN(4))
		}
		switch r := rng.IntN(100); {
		case r < 55:
			message = fmt.Sprintf("fix(%s): change %d", names[p], i)
		case r < 80:
			message = fmt.Sprintf("fix: change %d from the files", i)
		case r < 88:
			message = fmt.Sprintf("feat(%s)^: change %d for the consumers", names[p], i)
		case r < 94:
			message = fmt.Sprintf("chore: housekeeping %d", i)
		default:
			message = fmt.Sprintf("docs(%s): change %d", names[p], i)
		}
		if rng.IntN(10) == 0 {
			pair := fmt.Sprintf("dev-%02d", rng.IntN(24))
			message += fmt.Sprintf("\n\nA body line for change %d.\n\nCo-authored-by: %s <%s@example.com>", i, pair, pair)
		}
		return message, path
	}

	var line []int
	for written := 0; written < shape.commits; {
		i := len(line)
		if i >= scaleMergeEvery && i%scaleMergeEvery == 0 && written+scaleSideLength+1 <= shape.commits {
			side := line[i-4]
			for range scaleSideLength {
				message, path := change(written)
				side = commitAt("side", side, 0, message, path)
				written++
			}
			message, path := change(written)
			line = append(line, commitAt("main", line[i-1], side, "merge: "+message, path))
			written++
			continue
		}
		message, path := change(written)
		from := 0
		if i > 0 {
			from = line[i-1]
		}
		line = append(line, commitAt("main", from, 0, message, path))
		written++
	}

	// Releases: scaleReleases per package, a step apart, each package at an
	// offset of its own so no two packages share a release commit.
	tags := 0
	step := len(line) / scaleReleases
	for p, name := range names {
		offset := rng.IntN(step - 2)
		last := 0
		for k := 0; k < scaleReleases; k++ {
			pos := offset + k*step
			if pos >= len(line)-1 {
				break
			}
			fmt.Fprintf(w, "reset refs/tags/%s@1.%d.0\nfrom :%d\n\n", name, k, line[pos])
			tags++
			last = k
		}
		if p%8 == 3 {
			pos := min(offset+(scaleReleases-1)*step+step/3, len(line)-1)
			fmt.Fprintf(w, "reset refs/tags/%s@1.%d.0-rc.0\nfrom :%d\n\n", name, last+1, line[pos])
			tags++
		}
	}
	fmt.Fprintf(w, "done\n")
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}

	run := func(stdin []byte, args ...string) {
		b.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q", "-b", "main")
	run(stream.Bytes(), "fast-import", "--quiet", "--done")

	repo := scaleRepo{dir: dir, tags: tags}
	space := &model.Space{Name: "workspace"}
	for i, name := range names {
		repo.pkgs = append(repo.pkgs, &model.Package{
			Name: name, Dir: filepath.Join(dir, "pkgs", name), Space: space})
		if i > 0 {
			repo.deps = append(repo.deps, model.Dependency{Consumer: name, Provider: names[(i-1)/2]})
		}
	}
	if shape.isNoneSet {
		scripts := &model.Space{Name: "scripts", Versioning: model.VersioningNone}
		repo.pkgs = append(repo.pkgs, &model.Package{Name: "tools", Dir: filepath.Join(dir, "tools"), Space: scripts})
	}
	return repo
}

// gitCounters is a snapshot of the process-wide git counters gitx keeps.
type gitCounters struct{ calls, bytes, diffed uint64 }

func readGitCounters() gitCounters {
	return gitCounters{calls: gitx.GitInvocations(), bytes: gitx.GitOutputBytes(), diffed: gitx.CommitsDiffed()}
}

func (c gitCounters) sub(o gitCounters) gitCounters {
	return gitCounters{calls: c.calls - o.calls, bytes: c.bytes - o.bytes, diffed: c.diffed - o.diffed}
}

// BenchmarkComputeRealHistory plans over a real repository of 10,000 and
// 50,000 commits, with and without a package whose window is the whole
// history, and as the source of a composed workspace. Beside the timing it reports what a run pays git for: every git
// process planning starts (gitcalls/op, ancestry and resolution included), the
// bytes those processes write (gitOutputBytes/op), and the commits git diffs to
// list their changed paths (commitsDiffed/op); and what the planner does with
// the answer: the distinct windows it reads (windows/op), the commits the
// window attribution examines (AuthorScans/op), and the heap a finished plan
// keeps and needs at its peak (retained_MiB, peakHeap_MiB).
func BenchmarkComputeRealHistory(b *testing.B) {
	shapes := []scaleShape{
		{name: "whole", commits: 10_000, packages: 64, isNoneSet: true},
		{name: "whole", commits: 50_000, packages: 64, isNoneSet: true},
		{name: "bounded", commits: 10_000, packages: 64},
		{name: "bounded", commits: 50_000, packages: 64},
		{name: "composed", commits: 10_000, packages: 64, isComposed: true},
		{name: "composed", commits: 50_000, packages: 64, isComposed: true},
	}
	for _, shape := range shapes {
		b.Run(fmt.Sprintf("shape=%s/commits=%d", shape.name, shape.commits), func(b *testing.B) {
			repo := buildScaleRepo(b, shape)
			if shape.isComposed {
				for _, p := range repo.pkgs {
					p.Repository, p.RepoRoot = "source", repo.dir
				}
			}
			compute := func(stats *HistoryStats) *Plan {
				b.Helper()
				// A fresh repository handle per plan, as every run makes one:
				// LocalGitx keeps the ancestry DAG it loads for its lifetime.
				git := &gitx.LocalGitx{Dir: repo.dir}
				opts := Options{Packages: repo.pkgs, Dependencies: repo.deps, Root: repo.dir, HistoryStats: stats}
				if shape.isComposed {
					opts.Repositories = map[string]RepositoryHistory{
						"source": {Name: "source", Root: repo.dir, Git: git}}
				}
				p, err := Compute(context.Background(), git, opts)
				if err != nil {
					b.Fatal(err)
				}
				return p
			}
			stats := &HistoryStats{}
			before := readGitCounters()
			iterations := 0
			b.ReportAllocs()
			for b.Loop() {
				compute(stats)
				iterations++
			}
			b.StopTimer()
			per := float64(iterations)
			delta := readGitCounters().sub(before)
			b.ReportMetric(float64(delta.calls)/per, "gitcalls/op")
			b.ReportMetric(float64(delta.bytes)/per, "gitOutputBytes/op")
			b.ReportMetric(float64(delta.diffed)/per, "commitsDiffed/op")
			b.ReportMetric(float64(stats.CommitWindows.Load())/per, "windows/op")
			b.ReportMetric(float64(stats.AuthorScans.Load())/per, "AuthorScans/op")
			reportRetainedHeap(b, func() any { return compute(nil) })
			reportPeakHeap(b, func() any { return compute(nil) })
		})
	}
}

// reportPeakHeap measures the most heap a build holds live at any one moment,
// once and outside the timed loop. A cache that is dropped when the plan is
// returned never shows in retained_MiB, and it is still what decides whether a
// large workspace fits: the peak is the other half of the memory question.
//
// The live heap is sampled from runtime/metrics, which reads it without
// stopping the world, every quarter millisecond while the build runs. A
// sampler can miss a peak shorter than its interval, so the figure is a lower
// bound; the allocations that decide it here live for most of the build.
func reportPeakHeap(b *testing.B, build func() any) {
	b.Helper()
	const heapObjects = "/memory/classes/heap/objects:bytes"
	sample := []metrics.Sample{{Name: heapObjects}}
	live := func() uint64 {
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	runtime.GC()
	base := live()
	var peak atomic.Uint64
	done := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Go(func() {
		own := []metrics.Sample{{Name: heapObjects}}
		ticker := time.NewTicker(250 * time.Microsecond)
		defer ticker.Stop()
		for {
			metrics.Read(own)
			if v := own[0].Value.Uint64(); v > peak.Load() {
				peak.Store(v)
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	})
	kept := build()
	close(done)
	sampler.Wait()
	runtime.KeepAlive(kept)
	grown := int64(peak.Load()) - int64(base)
	if grown < 0 {
		grown = 0
	}
	b.ReportMetric(float64(grown)/(1<<20), "peakHeap_MiB")
}
