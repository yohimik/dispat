// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// syntheticControlHistory renders what `git log --raw -z` answers for a
// control repository of n commits, each moving one gitlink and touching two
// ordinary files. The shape is the parser's input exactly, so a measurement
// against it is a measurement of the real path.
func syntheticControlHistory(n int) string {
	var b strings.Builder
	b.Grow(n * 400)
	for i := range n {
		sha := fmt.Sprintf("%040x", i+1)
		parent := ""
		if i+1 < n {
			parent = fmt.Sprintf("%040x", i+2)
		}
		from := fmt.Sprintf("%040x", 1_000_000+i)
		to := fmt.Sprintf("%040x", 2_000_000+i)
		b.WriteString("\x00" + controlHistoryMarker + "\x00" + sha + "\x00" + parent + "\x00")
		b.WriteString("Release Bot\x00bot@example.test\x00")
		b.WriteString(fmt.Sprintf("chore(release): advance the sdk pointer to %s\n\nA body long enough to be worth keeping, as a real control commit's is.\x00", to[:12]))
		b.WriteString(fmt.Sprintf("\n:160000 160000 %s %s M\x00sources/sdk\x00", from, to))
		b.WriteString(fmt.Sprintf(":100644 100644 %s %s M\x00packages/app/manifest.json\x00", from, to))
		b.WriteString(fmt.Sprintf(":100644 100644 %s %s M\x00packages/app/CHANGELOG.md\x00", from, to))
	}
	return b.String()
}

// TestParseControlGitlinkHistoryDoesNotRetainTheLogBuffer measures what the
// parsed history holds on to after the log output itself is unreachable.
//
// strings.Split returns slices of its input, so a parser that kept them would
// pin the whole answer — one retained object id is enough — for as long as the
// plan lives. The bound is a multiple of the input rather than an absolute
// number of bytes, because what matters is that the result is proportional to
// what it keeps rather than to what it read.
func TestParseControlGitlinkHistoryDoesNotRetainTheLogBuffer(t *testing.T) {
	const commits = 20_000
	out := syntheticControlHistory(commits)
	input := len(out)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	history, err := parseControlGitlinkHistory(out)
	require.NoError(t, err)
	require.Len(t, history, commits)
	out = "" // the only reference to the log answer, gone

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := int(after.HeapAlloc) - int(before.HeapAlloc)
	runtime.KeepAlive(history)

	t.Logf("input %d bytes, retained %d bytes (%.2fx)", input, retained, float64(retained)/float64(input))
	require.Greater(t, retained, 0, "the parsed history is not free")
	// The commits this input renders keep a little over half of it; pinning
	// the answer as well puts the total above it. The line sits between the
	// two measurements with room on either side: 0.54x of the input with the
	// copies, 1.09x without them, on the machine this was written on.
	require.Less(t, retained, input*4/5,
		"the parsed history must not pin the log answer it was read from")
}

// BenchmarkParseControlGitlinkHistory reports the allocation cost of reading a
// 20k-commit control history. Run with -benchmem; the counter that matters
// beside ns/op is B/op, which is what the copies trade for the buffer.
func BenchmarkParseControlGitlinkHistory(b *testing.B) {
	out := syntheticControlHistory(20_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(out)))
	for b.Loop() {
		history, err := parseControlGitlinkHistory(out)
		if err != nil {
			b.Fatal(err)
		}
		if len(history) != 20_000 {
			b.Fatalf("history = %d commits", len(history))
		}
	}
}
