// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func releaseChoreographyPackagePolicies(compiled *release.CompiledTaskGraph) []execution.ChoreographyPackagePolicy {
	if compiled == nil {
		return nil
	}
	source := compiled.PackagePolicies()
	policies := make([]execution.ChoreographyPackagePolicy, choreographyNoEntriesCount, len(source))
	for _, p := range source {
		policies = append(policies, execution.ChoreographyPackagePolicy{
			Package: p.Package, Providers: p.Providers, Reached: p.Reached,
			DueTo: p.DueTo, Updates: p.Updates, ChannelFrom: p.ChannelFrom,
			Direct: p.Direct, Blocking: p.Blocking, OwnCause: p.OwnCause,
			ChannelChanged: p.ChannelChanged,
		})
	}
	return policies
}

func sweepChoreographyPackagePolicies(pl *plan.Plan, covered []string,
	onError string) []execution.ChoreographyPackagePolicy {
	coveredSet := make(map[string]bool, len(covered))
	for _, name := range covered {
		coveredSet[name] = choreographyKnownMemberState
	}
	policies := make([]execution.ChoreographyPackagePolicy, choreographyNoEntriesCount, len(covered))
	for _, name := range covered {
		policy := execution.ChoreographyPackagePolicy{Package: name,
			Mode: execution.KindRun, OnError: onError}
		for _, provider := range pl.Providers[name] {
			if coveredSet[provider] {
				policy.Direct = append(policy.Direct, provider)
			}
		}
		policies = append(policies, policy)
	}
	slices.SortFunc(policies, func(a, b execution.ChoreographyPackagePolicy) int {
		if a.Package < b.Package {
			return choreographySortBeforeValue
		}
		if a.Package > b.Package {
			return choreographySortAfterValue
		}
		return choreographySortEqualValue
	})
	return policies
}
