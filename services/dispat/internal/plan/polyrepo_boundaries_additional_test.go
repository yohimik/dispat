// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type failingControlHistoryGit struct {
	*fakeGit
	err error
}

func (g *failingControlHistoryGit) ControlGitlinkHistory(context.Context) ([]gitx.ControlHistoryCommit, error) {
	return nil, g.err
}

func TestControlCheckpointIndexRejectsUnavailableOrDisconnectedHistory(t *testing.T) {
	t.Run("reader failure", func(t *testing.T) {
		cp := &computation{
			ctx: context.Background(), controlRepo: "control",
			histories: map[string]RepositoryHistory{
				"control": {Name: "control", Control: true, Git: &failingControlHistoryGit{
					fakeGit: newFakeGit(), err: errors.New("history unavailable"),
				}},
			},
		}
		_, _, err := cp.controlCheckpoints()
		require.Error(t, err)
		assert.ErrorContains(t, err, "indexing control checkpoints: history unavailable")
	})

	t.Run("missing first parent", func(t *testing.T) {
		control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{{
			SHA: "cccccccccccccccccccccccccccccccccccccccc", Parents: []string{"missing"},
		}}}
		cp := &computation{
			ctx: context.Background(), controlRepo: "control",
			histories: map[string]RepositoryHistory{
				"control": {Name: "control", Control: true, Git: control},
			},
		}
		_, _, err := cp.controlCheckpoints()
		require.Error(t, err)
		assert.ErrorContains(t, err, "is missing first parent missing")
	})

	t.Run("reader capability absent", func(t *testing.T) {
		cp := &computation{
			ctx: context.Background(), controlRepo: "control",
			histories: map[string]RepositoryHistory{
				"control": {Name: "control", Control: true, Git: newFakeGit()},
			},
		}
		snapshots, ambiguous, err := cp.controlCheckpoints()
		require.NoError(t, err)
		assert.Empty(t, snapshots)
		assert.Empty(t, ambiguous)
		assert.False(t, cp.controlIndexed)
	})
}

func TestControlCheckpointAssociationRejectsARepinnedTag(t *testing.T) {
	const (
		sourceCommit = "1111111111111111111111111111111111111111"
		otherCommit  = "2222222222222222222222222222222222222222"
		c1           = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		c2           = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		c3           = "cccccccccccccccccccccccccccccccccccccccc"
		zero         = "0000000000000000000000000000000000000000"
	)
	control := &composedControlGit{fakeGit: newFakeGit(), control: []gitx.ControlHistoryCommit{
		{SHA: c3, Parents: []string{c2}, Message: "chore(release): app@1.0.0", Gitlinks: map[string]gitx.GitlinkTransition{
			"source": {From: otherCommit, To: sourceCommit},
		}},
		{SHA: c2, Parents: []string{c1}, Message: "chore: move away", Gitlinks: map[string]gitx.GitlinkTransition{
			"source": {From: sourceCommit, To: otherCommit},
		}},
		{SHA: c1, Message: "chore(release): app@1.0.0", Gitlinks: map[string]gitx.GitlinkTransition{
			"source": {From: zero, To: sourceCommit},
		}},
	}}
	cp := &computation{
		ctx: context.Background(), controlRepo: "control",
		pkgs: []*model.Package{{Name: "app", Repository: "source"}},
		histories: map[string]RepositoryHistory{
			"control": {Name: "control", Control: true, Git: control},
			"source":  {Name: "source", Path: "source", Git: newFakeGit()},
		},
		tags: map[string]gitx.Tags{"app": {{Name: "app@1.0.0", Commit: sourceCommit}}},
	}

	snapshots, ambiguous, err := cp.controlCheckpoints()
	require.NoError(t, err)
	key := checkpointTagKey("source", "app@1.0.0")
	assert.True(t, ambiguous[key], "two ordinary checkpoints imply different causal snapshots")
	assert.NotContains(t, snapshots, key, "an ambiguous association cannot supply an automatic boundary")
}

func TestRepositoryBoundaryRequiresCompleteCheckpointEvidence(t *testing.T) {
	const (
		consumerCommit = "1111111111111111111111111111111111111111"
		providerCommit = "2222222222222222222222222222222222222222"
		controlCommit  = "cccccccccccccccccccccccccccccccccccccccc"
	)
	pkg := &model.Package{Name: "app", Repository: "consumer"}
	tag := gitx.Tag{Name: "app@1.0.0", Commit: consumerCommit}
	checkpointKey := checkpointTagKey(pkg.Repository, tag.Name)
	newComputation := func() *computation {
		return &computation{
			controlRepo: "control",
			baselines:   make(map[baselineKey]string),
			histories: map[string]RepositoryHistory{
				"control":  {Name: "control", Control: true},
				"consumer": {Name: "consumer", Path: "consumer"},
				"provider": {Name: "provider", Path: "provider"},
			},
			controlPathIndex: map[string]int{"consumer": 0, "provider": 1},
			controlPathCount: 2,
		}
	}

	t.Run("owner tag is its own boundary", func(t *testing.T) {
		cp := newComputation()
		got, err := cp.repositoryBoundary(pkg, tag, "consumer", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, historyKey("consumer", consumerCommit), got)
	})

	t.Run("ambiguous checkpoint fails", func(t *testing.T) {
		cp := newComputation()
		_, err := cp.repositoryBoundary(pkg, tag, "provider", nil, map[string]bool{checkpointKey: true})
		require.ErrorIs(t, err, errFatalPlan)
		require.Len(t, cp.diags, 1)
		assert.Equal(t, CodeRepositoryBoundary, cp.diags[0].Code)
		assert.Contains(t, cp.diags[0].Message, "ambiguous control checkpoint")
	})

	t.Run("missing association fails", func(t *testing.T) {
		cp := newComputation()
		_, err := cp.repositoryBoundary(pkg, tag, "provider", nil, nil)
		require.ErrorIs(t, err, errFatalPlan)
		assert.Contains(t, cp.diags[0].Message, "no verifiable control checkpoint")
	})

	t.Run("checkpoint without provider pin fails", func(t *testing.T) {
		cp := newComputation()
		snapshot := controlSnapshot{commit: controlCommit, state: &controlGitlinkState{}}
		_, err := cp.repositoryBoundary(pkg, tag, "provider", map[string]controlSnapshot{checkpointKey: snapshot}, nil)
		require.ErrorIs(t, err, errFatalPlan)
		assert.Contains(t, cp.diags[0].Message, "has no gitlink for repository provider")
	})

	t.Run("checkpoint supplies control and provider positions", func(t *testing.T) {
		cp := newComputation()
		links := updatePersistentLink(nil, 0, 2, 0, consumerCommit)
		links = updatePersistentLink(links, 0, 2, 1, providerCommit)
		snapshot := controlSnapshot{commit: controlCommit, state: &controlGitlinkState{links: links}}
		snapshots := map[string]controlSnapshot{checkpointKey: snapshot}

		controlBoundary, err := cp.repositoryBoundary(pkg, tag, "control", snapshots, nil)
		require.NoError(t, err)
		assert.Equal(t, historyKey("control", controlCommit), controlBoundary)
		providerBoundary, err := cp.repositoryBoundary(pkg, tag, "provider", snapshots, nil)
		require.NoError(t, err)
		assert.Equal(t, historyKey("provider", providerCommit), providerBoundary)
	})

	t.Run("explicit baseline resolves otherwise ambiguous evidence", func(t *testing.T) {
		cp := newComputation()
		key := baselineKey{consumer: "app", tag: tag.Name, repository: "provider"}
		cp.baselines[key] = historyKey("provider", providerCommit)
		got, err := cp.repositoryBoundary(pkg, tag, "provider", nil, map[string]bool{checkpointKey: true})
		require.NoError(t, err)
		assert.Equal(t, historyKey("provider", providerCommit), got)
		assert.Empty(t, cp.diags)
	})
}
