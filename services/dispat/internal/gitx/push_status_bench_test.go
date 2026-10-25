// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"fmt"
	"strings"
	"testing"
)

// syntheticPushPorcelain is what `git push --porcelain` answers for a release
// push of n tags: a destination line, one status line per ref in refspec order
// and the closing line.
func syntheticPushPorcelain(n int) (string, []string) {
	var b strings.Builder
	refs := make([]string, 0, n)
	b.WriteString("To https://git.example/acme.git\n")
	for i := range n {
		ref := fmt.Sprintf("refs/tags/pkg-%04d@1.%d.0", i, i%7)
		refs = append(refs, ref)
		fmt.Fprintf(&b, "*\t%s:%s\t[new tag]\n", ref, ref)
	}
	b.WriteString("Done\n")
	return b.String(), refs
}

// BenchmarkPushStatusLookup answers every ref of a 1,000-ref release push out
// of one porcelain answer, the way PushReleaseRefs reads it.
func BenchmarkPushStatusLookup(b *testing.B) {
	out, refs := syntheticPushPorcelain(1_000)
	b.ReportAllocs()
	for b.Loop() {
		if reported := lookupPushStatuses(out, refs); reported != len(refs) {
			b.Fatalf("reported %d of %d refs", reported, len(refs))
		}
	}
}

func lookupPushStatuses(out string, refs []string) int {
	reported := 0
	report := readPushReport(out)
	for _, ref := range refs {
		if _, isReported := report[ref]; isReported {
			reported++
		}
	}
	return reported
}
