// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fastImportHistory writes a linear history of n commits into a new
// repository under dir in one git process and returns the repository root and
// its oldest commit.
func fastImportHistory(tb testing.TB, dir string, n int) (string, string) {
	tb.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		tb.Skip("git not available")
	}
	root := filepath.Join(dir, "history")
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		tb.Fatalf("git init: %v: %s", err, out)
	}
	var stream strings.Builder
	for i := range n {
		fmt.Fprintf(&stream, "commit refs/heads/main\nmark :%d\ncommitter Test <test@example.com> %d +0000\ndata 8\ncommit %d\n", i+1, 1_700_000_000+i, i%10)
		if i > 0 {
			fmt.Fprintf(&stream, "from :%d\n", i)
		}
		stream.WriteString("\n")
	}
	importer := exec.Command("git", "-C", root, "fast-import", "--quiet")
	importer.Stdin = strings.NewReader(stream.String())
	if out, err := importer.CombinedOutput(); err != nil {
		tb.Fatalf("git fast-import: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	oldest, err := exec.Command("git", "-C", root, "rev-list", "--max-parents=0", "HEAD").Output()
	if err != nil {
		tb.Fatalf("git rev-list: %v", err)
	}
	return root, strings.TrimSpace(string(oldest))
}

// BenchmarkAncestryAcrossHandles is one process asking one ancestry question
// through three handles on the same repository after a commit, the way a
// workspace run opens a planner handle, an owed-check handle and a record
// handle per repository. The counters are the git processes and the bytes of
// git output each round costs.
func BenchmarkAncestryAcrossHandles(b *testing.B) {
	root, oldest := fastImportHistory(b, b.TempDir(), 5_000)
	ctx := context.Background()
	writer := &LocalGitx{Dir: root}
	mid, err := writer.ResolveCommit(ctx, "HEAD~2500")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	invocations, outBytes := GitInvocations(), GitOutputBytes()
	for b.Loop() {
		if _, err := writer.run(ctx, "commit", "--allow-empty", "-qm", "chore: round"); err != nil {
			b.Fatal(err)
		}
		for range 3 {
			isAncestor, err := (&LocalGitx{Dir: root}).IsAncestor(ctx, oldest, mid)
			if err != nil || !isAncestor {
				b.Fatalf("IsAncestor = %v, %v", isAncestor, err)
			}
		}
	}
	b.ReportMetric(float64(GitInvocations()-invocations)/float64(b.N), "git-invocations/op")
	b.ReportMetric(float64(GitOutputBytes()-outBytes)/float64(b.N), "git-out-bytes/op")
}
