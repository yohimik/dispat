// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// syntheticRevList renders `git rev-list --parents HEAD` for a history of n
// commits, newest first, where every seventh commit merges a side line.
func syntheticRevList(n int) []byte {
	var b bytes.Buffer
	for i := range n {
		fmt.Fprintf(&b, "%040x", i+1)
		if i+1 < n {
			fmt.Fprintf(&b, " %040x", i+2)
		}
		if i%7 == 0 && i+4 < n {
			fmt.Fprintf(&b, " %040x", i+4)
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// pipeReader hands its input over in the pieces a pipe from git delivers, and
// hides bytes.Reader's WriterTo so a copy cannot take it in one write.
type pipeReader struct{ rest []byte }

func (r *pipeReader) Read(p []byte) (int, error) {
	if len(r.rest) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), 64<<10)], r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// BenchmarkLoadCommitGraph measures turning the rev-list answer of a
// 50,000-commit history into the ancestry graph, from the bytes git writes to
// the graph a handle keeps: B/op and allocs/op cover the output buffer as well
// as the graph.
func BenchmarkLoadCommitGraph(b *testing.B) {
	revList := syntheticRevList(50_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(revList)))
	for b.Loop() {
		graph := loadCommitGraph(&pipeReader{rest: revList})
		if len(graph.index) != 50_000 {
			b.Fatalf("graph holds %d commits", len(graph.index))
		}
	}
}

func loadCommitGraph(r io.Reader) *commitGraph {
	builder := newCommitGraphBuilder()
	if _, err := io.Copy(builder, r); err != nil {
		panic(err)
	}
	return builder.graph()
}

// newCommitGraph is the graph of one whole rev-list answer.
func newCommitGraph(revList string) *commitGraph {
	builder := newCommitGraphBuilder()
	_, _ = builder.Write([]byte(revList))
	return builder.graph()
}
