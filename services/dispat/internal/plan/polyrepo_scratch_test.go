// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestReleaseWorkspaceScratchDropsTransientIndexes(t *testing.T) {
	cp := &computation{
		repositoryReach:         map[string][]string{"app": {"source"}},
		controlInputs:           map[string]bool{"app": true},
		windowRefs:              map[string][]map[string]bool{"app": {{historyKey("source", "source2"): true}}},
		windowKeys:              map[string][]string{"app": {historyKey("source", "source2")}},
		windowKey:               map[string]string{"app": "commit:source2"},
		windowAuthors:           map[windowIdentity]windowAuthorSet{{window: "commit:source2"}: {}},
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
	cp.releaseWorkspaceScratch()

	assert.Nil(t, cp.repositoryReach)
	assert.Nil(t, cp.controlInputs)
	assert.Nil(t, cp.windowRefs)
	assert.Nil(t, cp.windowAuthors)
	assert.Nil(t, cp.windowKeys)
	assert.Nil(t, cp.windowKey)
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

}
