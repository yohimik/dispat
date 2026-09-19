// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"strings"
	"testing"

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
