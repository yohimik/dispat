// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
)

// A consumer on a prerelease train can release past its provider's commit the
// way a stable consumer can: the provider's publish fails and the consumer,
// with work of its own, proceeds onto the train (§19.3). Its train window is
// measured from its stable tag, so the commit stays in W(app) and leaves only
// Wfresh(app). Admission for a dependent is Wfresh and delivery (§13.3,
// §13.4a): the train published the commit, not the provider's version, so the
// provider still owes the consumer and the pair is an ordinary owed pair.

// overtakenOnATrain is the prerelease form of vector 80d: c2 carries core's
// caret and app's own step onto beta; core's publish failed and app released
// 1.1.0-beta.0 there.
func overtakenOnATrain(extra ...commit) *fakeGit {
	return newFakeGit(append([]commit{
		{sha: "c1", message: "chore: base"},
		{sha: "c2", message: "feat(core)^: streaming\n\n---\n\nfeat(app)%beta: own flag"},
	}, extra...)...).
		tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "c1").tag("app", "1.0.0", "c1").
		tag("app", "1.1.0-beta.0", "c2")
}

func TestTrainConsumerIsStillOwedTheProviderItOvertook(t *testing.T) {
	git := overtakenOnATrain()
	p := compute(t, git, nil)
	core, app := p.Releases["core"], p.Releases["app"]
	require.True(t, core.IsReleasing(), "core never published c2")
	assertVersion(t, v(1, 1, 0), core.Next)
	require.True(t, app.IsReleasing(), "app's train carries c2 but not core's version: %v", codes(p))
	assertVersion(t, pre(1, 1, 0, "beta", "1"), app.Next, "the train's target is unchanged, its counter moves")
	assert.Equal(t, []string{"core"}, app.DueTo)
	require.Len(t, app.Sources, 1)
	assert.Equal(t, StaleSource{Provider: "core", Commit: "c2", commitKey: "c2", Level: 1, Bump: ccme.BumpPatch},
		app.Sources[0])
	assert.False(t, app.CatchUp, "core releases in this plan, so app's release is an ordinary propagation")
	assert.Equal(t, "c2", app.OwedBoundary("core"))
	assert.Empty(t, p.OwedAtHead(headOf(git)), "with both in the run, app is released after core")

	p.Narrow([]string{"core"})
	assert.Equal(t, []OwedPair{{Provider: "core", Consumer: "app", Commit: "c2"}}, p.OwedAtHead(headOf(git)),
		"core alone would land on app's release commit and read as delivered")
}

// TestTrainConsumerCatchesUpAfterTheProviderPublishesAlone: core publishes c2
// at a later commit in a run app sat out. app's next prerelease is the
// catch-up, and W193 explains it.
func TestTrainConsumerCatchesUpAfterTheProviderPublishesAlone(t *testing.T) {
	git := overtakenOnATrain(commit{sha: "c3", message: "chore(core): retry the provider"}).
		tag("core", "1.1.0", "c3")
	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.True(t, app.IsReleasing(), "core's c2 is still owed to app: %v", codes(p))
	assertVersion(t, pre(1, 1, 0, "beta", "1"), app.Next)
	assert.True(t, app.CatchUp)
	assert.True(t, hasCode(p, CodeCatchUp), "W193 explains the release: %v", codes(p))
	assert.Equal(t, []string{"core"}, app.DueTo)

	settled := compute(t, git.tag("app", "1.1.0-beta.1", "c3"), nil)
	for _, name := range []string{"core", "utils", "app"} {
		assert.False(t, settled.Releases[name].IsReleasing(), "%s: nothing is owed twice", name)
	}
}

// TestTrainDeliveredContributionStillOnlyCounts is the control: core released
// c2 and app's prerelease came after it on the same commit, so the train
// carries core's version. The bump keeps counting toward the train's target
// and is no reason to release again.
func TestTrainDeliveredContributionStillOnlyCounts(t *testing.T) {
	git := overtakenOnATrain(commit{sha: "c3", message: "fix(app)%beta: follow-up"}).
		tag("core", "1.1.0", "c2")
	p := compute(t, git, nil)
	app := p.Releases["app"]
	require.True(t, app.IsReleasing(), "app has a fresh fix of its own")
	assertVersion(t, pre(1, 1, 0, "beta", "1"), app.Next)
	assert.Empty(t, app.Sources, "the train delivered core's contribution")
	assert.Empty(t, app.DueTo)
	assert.Equal(t, ccme.BumpPatch, app.PropagatedBump, "the delivered bump still counts toward the train")
	assert.False(t, hasCode(p, CodeCatchUp))
}

// TestTrainConsumerCancelDiscardsOnlyTheDebt: cancel(app) after the train
// shipped c2 discards the owed catch-up (§13.7d) but not what the train
// published, so the target keeps the train's core and nothing raises E195.
func TestTrainConsumerCancelDiscardsOnlyTheDebt(t *testing.T) {
	git := overtakenOnATrain(commit{sha: "c3", message: "cancel(app): drop the owed release"}).
		tag("core", "1.1.0", "c3")
	p := compute(t, git, nil)
	app := p.Releases["app"]
	assert.False(t, app.IsReleasing(), "the owed contribution is cancelled: %v", codes(p))
	assert.Empty(t, app.Sources)
	assert.Equal(t, ccme.BumpPatch, app.PropagatedBump, "the train still counts what it published")
	assert.False(t, hasCode(p, CodeEmptyCancel), "the cancel discarded the debt: %v", codes(p))
}
