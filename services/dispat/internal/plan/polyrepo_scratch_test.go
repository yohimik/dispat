// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func TestReleaseWorkspaceScratchPreservesPlanAncestry(t *testing.T) {
	source := newFakeGit(commit{sha: "source1"}, commit{sha: "source2"})
	links := updatePersistentLink(nil, 0, 1, 0, "source2")
	cp := &computation{
		ctx: context.Background(), controlRepo: "control",
		histories: map[string]RepositoryHistory{
			"control": {Name: "control", Git: newFakeGit(), Control: true},
			"source":  {Name: "source", Path: "source", Git: source},
		},
		controlStates: map[string]*controlGitlinkState{
			"control1": {links: links},
		},
		controlPathIndex: map[string]int{"source": 0}, controlPathCount: 1,
		ancNoGit: make(map[string]bool),
		stableBoundaries: map[string]map[string]string{
			"app": {"source": historyKey("source", "source2")},
		},

		repositoryReach:         map[string][]string{"app": {"source"}},
		controlInputs:           map[string]bool{"app": true},
		windowRefs:              map[string][]map[string]bool{"app": {{historyKey("source", "source2"): true}}},
		publishedBoundaries:     map[string]map[string]string{"app": {"source": historyKey("source", "source2")}},
		stableTags:              map[string]gitx.Tag{"app": {Name: "app@1.0.0"}},
		latestTags:              map[string]gitx.Tag{"app": {Name: "app@1.0.0"}},
		controlSnapshots:        map[string]controlSnapshot{"app": {commit: "control1"}},
		controlAmbiguous:        map[string]bool{"app": true},
		baselines:               map[baselineKey]string{{consumer: "app", tag: "app@1.0.0", repository: "source"}: "source2"},
		baselineSpecs:           []RepositoryBaseline{{Consumer: "app", ReleaseTag: "app@1.0.0", Repository: "source", Revision: "source2"}},
		parsers:                 map[string]*ccme.Parser{"source": nil},
		nonPackageByRepo:        map[string]map[string]bool{"source": {"release": true}},
		byFold:                  map[string]string{"app": "app"},
		proposedAll:             map[string][]channelPick{"app": {{channel: "beta", commit: historyKey("source", "source2")}}},
		ignoredTagsByRepository: map[string]map[string]bool{"source": {"v1.1.0": true}},
	}
	pl := &Plan{
		Releases: map[string]*Release{
			"lib": {
				Pkg:          &model.Package{Name: "lib", Repository: "source"},
				StableCommit: "source1", stableCommitKey: historyKey("source", "source1"),
			},
			"app": {Pkg: &model.Package{Name: "app", Repository: "app-source"}},
		},
		ancestor: cp.ancestorOrSelf, stableBoundaries: cp.stableBoundaries,
	}

	cp.releaseWorkspaceScratch()

	assert.Nil(t, cp.repositoryReach)
	assert.Nil(t, cp.controlInputs)
	assert.Nil(t, cp.windowRefs)
	assert.Nil(t, cp.publishedBoundaries)
	assert.Nil(t, cp.stableTags)
	assert.Nil(t, cp.latestTags)
	assert.Nil(t, cp.controlSnapshots)
	assert.Nil(t, cp.controlAmbiguous)
	assert.Nil(t, cp.baselines)
	assert.Nil(t, cp.baselineSpecs)
	assert.Nil(t, cp.parsers)
	assert.Nil(t, cp.nonPackageByRepo)
	assert.Nil(t, cp.byFold)
	assert.Nil(t, cp.proposedAll)
	assert.Nil(t, cp.ignoredTagsByRepository)

	assert.True(t, cp.ancestorOrSelf(historyKey("source", "source1"), historyKey("source", "source2")),
		"same-owner ancestry remains available")
	assert.True(t, cp.ancestorOrSelf(historyKey("source", "source1"), historyKey("control", "control1")),
		"the retained control snapshot still observes its source pin")
	assert.False(t, pl.PossiblyBehind("app", "lib"),
		"the public plan helper retains its stable boundary and ancestry callback")
}
