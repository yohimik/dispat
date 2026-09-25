// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseCommitsRefusesMalformedNonemptyRecords: a successful Git process
// with a truncated framed record is not an empty history window. Treating it
// as empty would erase pending work from a release plan.
func TestParseCommitsRefusesMalformedNonemptyRecords(t *testing.T) {
	for _, raw := range []string{
		"nonempty output without the framed fields",
		logRecordSep + "not-an-object-id" + strings.Repeat(logFieldSep, logCommitFields),
		logRecordSep + strings.Repeat("1", 40) + logFieldSep + "not-a-parent" +
			strings.Repeat(logFieldSep, logCommitFields-1),
	} {
		commits, err := parseCommits(raw)
		require.Error(t, err)
		assert.Nil(t, commits)
		assert.ErrorContains(t, err, "malformed commit log")
	}
}

// TestCommitLogParsesAStreamAsItParsesTheWhole: git writes its output in
// pieces of whatever size the pipe hands over, so a record, a separator or a
// multi-byte character can be cut anywhere. The streamed parse must read the
// same commits whatever the cuts, and keep none of the buffer it read them
// from: every string a commit holds is its own.
func TestCommitLogParsesAStreamAsItParsesTheWhole(t *testing.T) {
	out := syntheticCommitLog(40) + logRecordSep + strings.Repeat("a", 40) + logFieldSep + logFieldSep +
		"Zoé" + logFieldSep + "zoe@example.com" + logFieldSep + "fix: ünïcode\n\nbody" + logFieldSep
	want, err := parseCommits(out)
	require.NoError(t, err)
	require.Len(t, want, 41)

	for _, size := range []int{1, 2, 3, 7, 64, 4096} {
		var log commitLog
		input := []byte(out)
		for chunk := range slices.Chunk(input, size) {
			n, err := log.Write(chunk)
			require.NoError(t, err)
			require.Equal(t, len(chunk), n)
		}
		got, err := log.close()
		require.NoError(t, err)
		assert.Equal(t, want, got, "chunks of %d bytes", size)

		// Nothing kept points into the bytes the parse was handed.
		first, last := uintptr(unsafe.Pointer(&input[0])), uintptr(unsafe.Pointer(&input[len(input)-1]))
		for _, c := range got {
			for _, field := range append([]string{c.SHA, c.AuthorName, c.AuthorEmail, c.Message}, c.Files...) {
				if field == "" {
					continue
				}
				at := uintptr(unsafe.Pointer(unsafe.StringData(field)))
				assert.False(t, at >= first && at <= last, "a kept field aliases the stream")
			}
		}
	}
}

// TestParseCommitsKeepsNoneOfItsInput: the in-memory parse copies what a
// commit keeps too, so a caller holding commits does not hold the log.
func TestParseCommitsKeepsNoneOfItsInput(t *testing.T) {
	out := syntheticCommitLog(3)
	commits, err := parseCommits(out)
	require.NoError(t, err)
	first := uintptr(unsafe.Pointer(unsafe.StringData(out)))
	last := first + uintptr(len(out))
	for _, c := range commits {
		for _, field := range append(append([]string{c.SHA, c.Message}, c.Parents...), c.Files...) {
			at := uintptr(unsafe.Pointer(unsafe.StringData(field)))
			assert.False(t, at >= first && at < last, "a kept field aliases the log output")
		}
	}
}
