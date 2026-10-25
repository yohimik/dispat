// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOneUnitCommitAllocatesItsRecordsOnce: a commit's scope-set, bump axis
// and channel axis records are one allocation for the one-unit commit that
// nearly every commit is, and their lengths always match the units.
func TestOneUnitCommitAllocatesItsRecordsOnce(t *testing.T) {
	for _, units := range []int{0, 1, 3} {
		scope, propagations, channels := newUnitRecords(units)
		require.Len(t, scope, units)
		require.Len(t, propagations, units)
		require.Len(t, channels, units)
	}
	var kept []map[string]bool
	assert.Equal(t, 1.0, testing.AllocsPerRun(100, func() { kept, _, _ = newUnitRecords(1) }))
	assert.Zero(t, testing.AllocsPerRun(100, func() { kept, _, _ = newUnitRecords(0) }))
	_ = kept
}

// TestUnwrittenChannelAxisIsLeftInert: at depth 0 with no scope footer the
// channel axis is inert whatever its value, so it is not resolved; a scope
// footer still is.
func TestUnwrittenChannelAxisIsLeftInert(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: reaches app on the bump axis only"},
		commit{sha: "c2", message: "fix(core)^: scoped\n\nPropagate-Scope: nothing-*"},
	).tag("core", "1.0.0", "").tag("app", "1.0.0", "")
	p := compute(t, git, nil)
	assert.True(t, p.Releases["app"].IsChanged(), "%v", codes(p))
	assert.Equal(t, "stable", p.Releases["app"].Channel)
	emptyGlobs := 0
	for _, code := range codes(p) {
		if code == CodeEmptyGlob {
			emptyGlobs++
		}
	}
	assert.Equal(t, 1, emptyGlobs, "the Propagate-Scope footer is still resolved, and reported once: %v", codes(p))
}
