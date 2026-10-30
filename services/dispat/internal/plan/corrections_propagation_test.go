// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
)

func TestCorrectionConsumerWildcardDiscardsBothPropagationAxes(t *testing.T) {
	for name, message := range map[string]string{
		"bump":    "feat(core)^patch: provider change",
		"channel": "chore(core)%beta++1: provider train",
		"both":    "feat(core)^patch%beta++1: provider change",
	} {
		t.Run(name, func(t *testing.T) {
			history := []commit{
				{sha: shaA, message: message},
				{sha: shaB, message: "chore(app): discard incoming work\n\nDeletes: *"},
			}
			tags := func(g *fakeGit) *fakeGit {
				return g.tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
			}
			before := compute(t, tags(newFakeGit(history[0])), nil)
			require.True(t, before.Releases["app"].IsReleasing(), "the incoming contribution exists")
			p := compute(t, tags(newFakeGit(history...)), nil)
			assert.True(t, p.Releases["core"].IsReleasing(), "discarding app's contribution preserves its provider")
			assert.False(t, p.Releases["app"].IsReleasing())
			assert.Equal(t, ccme.ChannelStable, p.Releases["app"].Channel)
			assert.Empty(t, p.Releases["app"].Sources)
			assert.False(t, hasCode(p, CodeCorrectionNoop), "the wildcard discarded incoming work: %v", codes(p))
		})
	}
}

func TestCorrectionConsumerWildcardDiscardsOwedWorkOutsideItsFreshWindow(t *testing.T) {
	history := []commit{
		{sha: shaA, message: "feat(core)^patch: provider change"},
		{sha: shaB, message: "fix(app): own release"},
		{sha: shaC, message: "chore(core): provider retry"},
		{sha: shaD, message: "chore(app): discard catch-up\n\nDeletes: *"},
	}
	tags := func(g *fakeGit) *fakeGit {
		return g.tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "").
			tag("app", "1.0.1", shaB).tag("core", "1.1.0", shaC)
	}
	before := compute(t, tags(newFakeGit(history[:3]...)), nil)
	require.True(t, before.Releases["app"].IsReleasing(), "the consumer released before its provider delivered")
	assertVersion(t, v(1, 0, 2), before.Releases["app"].Next)
	named := append([]commit(nil), history[:3]...)
	named = append(named, commit{sha: shaD, message: "chore(app): cannot widen a named target\n\nDeletes: " + shaA})
	explicit := compute(t, tags(newFakeGit(named...)), nil)
	assert.True(t, hasCode(explicit, CodeCorrectionWidens), "only wildcard reach includes consumer contributions")
	assert.True(t, explicit.Releases["app"].IsReleasing(), "the invalid named correction leaves the debt intact")

	p := compute(t, tags(newFakeGit(history...)), nil)
	assert.False(t, p.Releases["app"].IsReleasing(), "the wildcard reaches owed work behind app's baseline")
	assert.Empty(t, p.Releases["app"].Sources)
	assert.False(t, hasCode(p, CodeCorrectionNoop), "%v", codes(p))

	undone := compute(t, tags(newFakeGit(append(history,
		commit{sha: shaE, message: "chore(app): restore catch-up\n\nDeletes: " + shaD})...)), nil)
	assertVersion(t, v(1, 0, 2), undone.Releases["app"].Next)
	assert.True(t, undone.Releases["app"].IsReleasing(), "discarding the correction restores its owed contribution")
	assert.True(t, hasCode(undone, CodeCorrectionVoid))
}

func TestCorrectionConsumerWildcardPreservesCarriedTrainBump(t *testing.T) {
	for name, providerRelease := range map[string]string{"owed": shaC, "delivered": shaA} {
		t.Run(name, func(t *testing.T) {
			history := []commit{
				{sha: shaA, message: "feat(core)^minor: provider change\n\n---\n\nfix(app)%rc: own fix"},
				{sha: shaB, message: "chore(app): consumer release"},
				{sha: shaC, message: "chore(core): provider retry"},
				{sha: shaD, message: "chore(app): discard catch-up\n\nDeletes: *"},
			}
			tags := func(g *fakeGit) *fakeGit {
				return g.tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "").
					tag("app", "1.1.0-rc.0", shaB).tag("core", "1.1.0", providerRelease)
			}
			p := compute(t, tags(newFakeGit(history...)), nil)
			app := p.Releases["app"]
			assert.False(t, app.IsReleasing(), "no owed contribution remains: %v", codes(p))
			assert.Empty(t, app.Sources)
			assert.Equal(t, ccme.BumpMinor, app.PropagatedBump, "published train work still contributes to the target")
			assert.Equal(t, name == "delivered", hasCode(p, CodeCorrectionNoop), "%v", codes(p))

			fresh := compute(t, tags(newFakeGit(append(history, commit{sha: shaE, message: "fix(app): fresh work"})...)), nil)
			assert.True(t, fresh.Releases["app"].IsReleasing())
			assertVersion(t, pre(1, 1, 0, "rc", "1"), fresh.Releases["app"].Next, "a fresh patch cannot shrink the published minor target")
			assert.Empty(t, fresh.Releases["app"].Sources, "fresh own work does not restore the discarded debt")
		})
	}
}

func TestCorrectionConsumerWildcardExcludesItsOwnCommit(t *testing.T) {
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)^patch: old incoming work"},
		commit{sha: shaB, message: "chore(app): discard old work\n\nDeletes: *\n\n---\n\nfeat(utils)^minor: new incoming work"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	p := compute(t, git, nil)
	assertVersion(t, v(1, 1, 0), p.Releases["app"].Next)
	assert.Equal(t, []string{"utils"}, p.Releases["app"].DueTo, "only the correction's strict ancestors are discarded")
}

func TestCorrectionConsumerWildcardOfDeliveredWorkIsANoop(t *testing.T) {
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)^patch: provider change"},
		commit{sha: shaB, message: "chore(app): consumer release"},
		commit{sha: shaC, message: "chore(app): discard old work\n\nDeletes: *"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "").
		tag("core", "1.1.0", shaA).tag("app", "1.0.1", shaB)
	p := compute(t, git, nil)
	assert.False(t, p.Releases["app"].IsReleasing())
	assert.True(t, hasCode(p, CodeCorrectionNoop), "already delivered work cannot be discarded")
}
