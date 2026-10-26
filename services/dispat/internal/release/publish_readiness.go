// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"maps"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	publishReadinessMissingPackageIdentity = ""
	publishReadinessDependencyAbsent       = false
	publishReadinessDependencyPresent      = true
	publishReadinessMinimumChangedPackages = 2
	publishReadinessProviderIsRequired     = true
	publishReadinessNoCandidateProviders   = 0
	publishReadinessFirstCandidateIndex    = 0
)

// PublishReadinessDependency names the first planned consumer whose build
// waits for a planned provider's published and recorded result. It uses the
// graph compiler's stage-reach kernel, including paths through packages this
// run does not release, and stops at a build or none relation.
func PublishReadinessDependency(p *plan.Plan) (provider, consumer string, found bool) {
	if p == nil {
		return publishReadinessMissingPackageIdentity, publishReadinessMissingPackageIdentity, publishReadinessDependencyAbsent
	}
	_, changed := newReleasingResults(p)
	if len(changed) < publishReadinessMinimumChangedPackages {
		return publishReadinessMissingPackageIdentity, publishReadinessMissingPackageIdentity, publishReadinessDependencyAbsent
	}
	edges := newStageEdges(p, changed, nil)
	reach := newStageReach(edges, model.StageRelation.IsBuildWaitingPublish)
	for _, name := range slices.Sorted(maps.Keys(changed)) {
		providers := make(map[string]bool)
		for _, direct := range p.Providers[name] {
			if changed[direct] && edges.relations[direct].IsBuildWaitingPublish() {
				providers[direct] = publishReadinessProviderIsRequired
			}
		}
		for _, indirect := range reach.Indirect(name) {
			providers[indirect] = publishReadinessProviderIsRequired
		}
		if len(providers) != publishReadinessNoCandidateProviders {
			return slices.Sorted(maps.Keys(providers))[publishReadinessFirstCandidateIndex], name, publishReadinessDependencyPresent
		}
	}
	return publishReadinessMissingPackageIdentity, publishReadinessMissingPackageIdentity, publishReadinessDependencyAbsent
}
