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

// isHeadReachedIn answers ancestry against the fake history's last commit,
// as the release command answers it against each repository's head.
func isHeadReachedIn(git *fakeGit) func(string, string) bool {
	head, _ := git.HeadSHA(context.Background())
	return isHeadReachedAt(git, head)
}

// isHeadReachedAt answers ancestry against a head of the test's choosing.
func isHeadReachedAt(git *fakeGit, head string) func(string, string) bool {
	return func(_, commit string) bool {
		isReached, _ := git.IsAncestor(context.Background(), head, commit)
		return isReached
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
	assert.Empty(t, p.OwedAtHead(isHeadReachedIn(git)), "with both in the run, app is released after core")

	p.Narrow([]string{"core"})
	require.True(t, p.Releases["core"].IsReleasing())
	require.False(t, p.Releases["app"].IsReleasing())
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c2"}}, p.OwedAtHead(isHeadReachedIn(git)))
}

func TestOwedAtHeadIsEmptyWhenBothRelease(t *testing.T) {
	git := proceededAtHead()
	p := compute(t, git, nil)
	p.Narrow([]string{"core", "app"})
	assert.Empty(t, p.OwedAtHead(isHeadReachedIn(git)))
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
	assert.Empty(t, p.OwedAtHead(isHeadReachedIn(git)))
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
	assert.Empty(t, p.OwedAtHead(isHeadReachedIn(git)))
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
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c3"}}, p.OwedAtHead(isHeadReachedIn(git)))
}

// TestOwedAtHeadReportsABoundaryAheadOfTheHead: in a composed workspace a
// consumer's boundary in its provider's repository is the pin its release
// recorded, which can be ahead of the head the provider would be tagged at.
// A tag at the head is then behind the boundary, ancestry reads the consumer
// as served, and the pair is refused exactly as at an equal commit.
func TestOwedAtHeadReportsABoundaryAheadOfTheHead(t *testing.T) {
	git := proceededAtHead()
	p := compute(t, git, nil)
	p.Narrow([]string{"core"})
	require.Equal(t, "c2", p.Releases["app"].OwedBoundary("core"))
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c2"}}, p.OwedAtHead(isHeadReachedAt(git, "c1")))
}

// TestOwedAtHeadAmongReadsTheInvocationsSelection: `dispat commit --tag`
// releases only the packages it covers, so a consumer the plan releases but
// the invocation leaves out is owed exactly as one the plan leaves out.
func TestOwedAtHeadAmongReadsTheInvocationsSelection(t *testing.T) {
	git := proceededAtHead()
	p := compute(t, git, nil)
	require.True(t, p.Releases["app"].IsReleasing())
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c2"}},
		p.OwedAtHeadAmong([]string{"core"}, isHeadReachedIn(git)))
	assert.Empty(t, p.OwedAtHeadAmong([]string{"core", "app"}, isHeadReachedIn(git)), "app is tagged after core")
	assert.Empty(t, p.OwedAtHeadAmong([]string{"app"}, isHeadReachedIn(git)), "core is not tagged at all")
}
