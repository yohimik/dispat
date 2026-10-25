// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// buildChainWindowRepo gives each package a distinct stable boundary, then
// changes every package. The windows are nested prefixes of one linear walk.
func buildChainWindowRepo(t testing.TB, packages int) scaleRepo {
	changed := make([]int, packages)
	for pkg := range changed {
		changed[pkg] = pkg
	}
	return buildChainWindowRepoWithChanges(t, packages, changed)
}

func buildChainWindowRepoWithChanges(t testing.TB, packages int, changed []int) scaleRepo {
	t.Helper()
	dir := t.TempDir()
	var stream bytes.Buffer
	w := bufio.NewWriter(&stream)
	for commit := 1; commit <= packages+len(changed); commit++ {
		pkg := commit - 1
		if commit > packages {
			pkg = changed[commit-packages-1]
		}
		name := fmt.Sprintf("pkg-%04d", pkg)
		message := fmt.Sprintf("fix(%s): change %d", name, commit)
		fmt.Fprintf(w, "commit refs/heads/main\nmark :%d\n", commit)
		fmt.Fprintf(w, "author Same Author <same@example.com> %d +0000\n", 1_600_000_000+commit)
		fmt.Fprintf(w, "committer Same Author <same@example.com> %d +0000\n", 1_600_000_000+commit)
		fmt.Fprintf(w, "data %d\n%s\n", len(message), message)
		path := fmt.Sprintf("pkgs/%s/f%d.txt", name, commit)
		content := fmt.Sprintf("change %d\n", commit)
		fmt.Fprintf(w, "M 100644 inline %s\ndata %d\n%s\n", path, len(content), content)
		if commit <= packages {
			fmt.Fprintf(w, "reset refs/tags/%s@1.0.0\nfrom :%d\n\n", name, commit)
		}
	}
	fmt.Fprintln(w, "done")
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	run := func(input []byte, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if input != nil {
			cmd.Stdin = bytes.NewReader(input)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q", "-b", "main")
	run(stream.Bytes(), "fast-import", "--quiet", "--done")
	repo := scaleRepo{dir: dir, tags: packages}
	space := &model.Space{Name: "workspace"}
	for pkg := 0; pkg < packages; pkg++ {
		name := fmt.Sprintf("pkg-%04d", pkg)
		repo.pkgs = append(repo.pkgs, &model.Package{Name: name, Dir: filepath.Join(dir, "pkgs", name), Space: space})
	}
	return repo
}

func BenchmarkComputeChainWindows(b *testing.B) {
	for _, packages := range []int{64, 128, 256} {
		b.Run(fmt.Sprintf("packages=%d", packages), func(b *testing.B) {
			repo := buildChainWindowRepo(b, packages)
			stats := &HistoryStats{}
			compute := func(record *HistoryStats) *Plan {
				b.Helper()
				git := &gitx.LocalGitx{Dir: repo.dir}
				plan, err := Compute(context.Background(), git, Options{Packages: repo.pkgs, Root: repo.dir, HistoryStats: record})
				if err != nil {
					b.Fatal(err)
				}
				return plan
			}
			b.ReportAllocs()
			for b.Loop() {
				compute(stats)
			}
			b.StopTimer()
			b.ReportMetric(float64(stats.AuthorScans.Load())/float64(b.N), "AuthorScans/op")
			b.ReportMetric(float64(stats.WindowCommitRefs.Load())/float64(b.N), "WindowCommitRefs/op")
			reportRetainedHeap(b, func() any { return compute(nil) })
			reportPeakHeap(b, func() any { return compute(nil) })
		})
	}
}
