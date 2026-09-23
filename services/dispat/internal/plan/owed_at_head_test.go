// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E201 before publication (§19.3): a provider released at the baseline commit
// of a consumer it still owes, with the consumer not released after it in the
// same run. Both tags would sit on one commit and ancestry could no longer say
// that the consumer came first, so the consumer would read as served.

// headOf answers every repository's head as the fake history's last commit.
func headOf(git *fakeGit) func(string) string {
	return func(string) string {
		sha, _ := git.HeadSHA(context.Background())
		return sha
	}
}

// proceededAtHead is vector 80d's first run: core's publish of c2 failed and
// app proceeded there, so app's baseline is the head a retry starts from.
func proceededAtHead(extra ...commit) *fakeGit {
	return newFakeGit(append([]commit{
		{sha: "c1", message: "chore: base"},
		{sha: "c2", message: "feat(core)^: streaming\n\n---\n\nfeat(app): own flag"},
	}, extra...)...).
		tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
		tag("app", "1.1.0", "c2")
}

func TestOwedAtHeadReportsAProviderReleasedAloneAtItsConsumersCommit(t *testing.T) {
	git := proceededAtHead()
	p := compute(t, git, nil)
	require.True(t, p.Releases["app"].IsReleasing(), "app is owed core's c2 and releases after it")
	assert.Empty(t, p.OwedAtHead(headOf(git)), "with both in the run, app is released after core")

	p.Narrow([]string{"core"})
	require.True(t, p.Releases["core"].IsReleasing())
	require.False(t, p.Releases["app"].IsReleasing())
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c2"}}, p.OwedAtHead(headOf(git)))
}

func TestOwedAtHeadIsEmptyWhenBothRelease(t *testing.T) {
	git := proceededAtHead()
	p := compute(t, git, nil)
	p.Narrow([]string{"core", "app"})
	assert.Empty(t, p.OwedAtHead(headOf(git)))
}

// TestOwedAtHeadIsEmptyAtALaterCommit: the provider's tag lands on a commit the
// consumer's baseline does not reach, so the debt stays visible through the
// owed window and nothing needs refusing.
func TestOwedAtHeadIsEmptyAtALaterCommit(t *testing.T) {
	git := proceededAtHead(commit{sha: "c3", message: "chore(core): retry the provider"})
	p := compute(t, git, nil)
	p.Narrow([]string{"core"})
	require.True(t, p.Releases["core"].IsReleasing())
	assert.Equal(t, "c2", p.Releases["app"].OwedBoundary("core"))
	assert.Empty(t, p.OwedAtHead(headOf(git)))
}

// TestOwedAtHeadIsEmptyWithoutACaret: a provider unit that propagates nothing
// owes the consumer nothing, whatever commit the provider lands on.
func TestOwedAtHeadIsEmptyWithoutACaret(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "chore: base"},
		commit{sha: "c2", message: "feat(core): streaming\n\n---\n\nfeat(app): own flag"},
	).tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").tag("app", "1.1.0", "c2")
	p := compute(t, git, nil)
	p.Narrow([]string{"core"})
	require.True(t, p.Releases["core"].IsReleasing())
	assert.Empty(t, p.Releases["app"].OwedBoundary("core"))
	assert.Empty(t, p.OwedAtHead(headOf(git)))
}

// TestOwedAtHeadReportsAPrereleaseBaselineAtHead: the consumer's newest release
// is a prerelease at the head. Delivery is measured against the newest release
// of any channel, so the pair is the same pair.
func TestOwedAtHeadReportsAPrereleaseBaselineAtHead(t *testing.T) {
	git := proceededAtHead(commit{sha: "c3", message: "feat(app)%beta: onto the train"}).
		tag("app", "1.2.0-beta.0", "c3")
	p := compute(t, git, nil)
	require.True(t, p.Releases["app"].IsReleasing(), "app is still owed core's c2: %v", codes(p))
	p.Narrow([]string{"core"})
	require.True(t, p.Releases["core"].IsReleasing())
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c3"}}, p.OwedAtHead(headOf(git)))
}
