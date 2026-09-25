// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// syntheticCommitLog is what the planner's `git log` writes for a history of n
// commits, each changing two paths: ONE string, exactly as a git process's
// output reaches the parser.
func syntheticCommitLog(n int) string {
	var b strings.Builder
	for i := range n {
		sha := fmt.Sprintf("%040x", i+1)
		parent := fmt.Sprintf("%040x", i+2)
		b.WriteString(logRecordSep + sha + logFieldSep + parent + logFieldSep)
		fmt.Fprintf(&b, "dev-%02d%sdev-%02d@example.com%s", i%24, logFieldSep, i%24, logFieldSep)
		fmt.Fprintf(&b, "fix(pkg-%02d): change %d\n\nA body line explaining change %d.\n", i%64, i, i)
		b.WriteString(logFieldSep)
		fmt.Fprintf(&b, "\n\npkgs/pkg-%02d/f%d.txt\npkgs/pkg-%02d/g%d.txt\n", i%64, i%4, i%64, i%4)
	}
	return b.String()
}

// BenchmarkParseCommits measures reading a 50,000-commit log, and what the
// parsed commits keep alive afterwards (retained_MiB). A commit whose fields
// are sub-slices of the output keeps the whole output reachable, file lists
// and separators included, for as long as any one of its strings is: a plan
// keeps its commits' messages for the whole run, so that is how long the
// buffer stays.
func BenchmarkParseCommits(b *testing.B) {
	const commits = 50_000
	out := syntheticCommitLog(commits)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		parsed, err := parseCommitLog(out)
		if err != nil || len(parsed) != commits {
			b.Fatalf("parsed %d commits: %v", len(parsed), err)
		}
	}
	b.StopTimer()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	kept := func() []Commit {
		parsed, err := parseCommitLog(syntheticCommitLog(commits))
		if err != nil {
			b.Fatal(err)
		}
		return parsed
	}()
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := max(int64(after.HeapAlloc)-int64(before.HeapAlloc), 0)
	b.ReportMetric(float64(retained)/(1<<20), "retained_MiB")
	runtime.KeepAlive(kept)
}
