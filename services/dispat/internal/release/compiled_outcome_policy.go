// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	compiledPackageInitialPolicyCount          = 0
	compiledPackageProviderHasBlockingRelation = true
	compiledPackageProviderHasDeadOutcome      = true
	compiledPackageNoProviderIdentity          = ""
	compiledPackageOutcomeAllowsTask           = false
	compiledPackageOutcomeBlocksTask           = true
)

// CompiledPackagePolicy is the immutable §19.3 predicate input for one
// package. It is exported from the same plan and reach index as SkipPolicy.
type CompiledPackagePolicy struct {
	Package        string
	Providers      []string
	Reached        []string
	DueTo          []string
	Updates        []string
	ChannelFrom    string
	Direct         []string
	Blocking       map[string]bool
	OwnCause       bool
	ChannelChanged bool
}

func (c *CompiledTaskGraph) PackagePolicies() []CompiledPackagePolicy {
	if c == nil || c.plan == nil {
		return nil
	}
	policies := make([]CompiledPackagePolicy, compiledPackageInitialPolicyCount, len(c.plan.Releases))
	for name, rel := range c.plan.Releases {
		if rel == nil {
			continue
		}
		policies = append(policies, compilePackagePolicy(name, c.plan, c.blocking.Indirect(name)))
	}
	slices.SortFunc(policies, func(a, b CompiledPackagePolicy) int { return compareString(a.Package, b.Package) })
	return policies
}

func compilePackagePolicy(name string, pl *plan.Plan, reached []string) CompiledPackagePolicy {
	rel := pl.Releases[name]
	providers := pl.BlockingProviders[name]
	if pl.BlockingProviders == nil {
		providers = pl.Providers[name]
	}
	p := CompiledPackagePolicy{Package: name, Providers: slices.Clone(pl.Providers[name]),
		Reached: slices.Clone(reached), DueTo: slices.Clone(rel.DueTo),
		ChannelFrom: rel.ChannelFrom, Direct: slices.Clone(providers),
		Blocking: make(map[string]bool), OwnCause: rel.IsFreshOwnBump() || rel.Pinned,
		ChannelChanged: rel.IsChannelChanged()}
	for _, update := range rel.Updates {
		p.Updates = append(p.Updates, update.Name)
	}
	for _, provider := range append(slices.Clone(p.Direct), p.Reached...) {
		if pr := pl.Releases[provider]; pr != nil && pr.Pkg != nil && pr.Pkg.Space != nil &&
			pr.Pkg.Space.ProviderRelation.IsBlocking {
			p.Blocking[provider] = compiledPackageProviderHasBlockingRelation
		}
	}
	return p
}

// EvaluateCompiledPackagePolicy is the shared §19.3 decision kernel for the
// compiled executor and a peer consuming the signed descriptor.
func EvaluateCompiledPackagePolicy(p CompiledPackagePolicy,
	outcome func(string) PackageOutcome) (bool, string) {
	badProvider, blockingProvider := compiledPackageNoProviderIdentity, compiledPackageNoProviderIdentity
	bad := make(map[string]bool)
	markDead := func(provider string) {
		if !outcome(provider).dead() {
			return
		}
		bad[provider], badProvider = compiledPackageProviderHasDeadOutcome, provider
	}
	for _, providers := range [][]string{p.Providers, p.Reached, p.DueTo, p.Updates} {
		for _, provider := range providers {
			markDead(provider)
		}
	}
	if p.ChannelFrom != compiledPackageNoProviderIdentity {
		markDead(p.ChannelFrom)
	}
	if badProvider == compiledPackageNoProviderIdentity {
		return compiledPackageOutcomeAllowsTask, compiledPackageNoProviderIdentity
	}
	for _, providers := range [][]string{p.Direct, p.Reached} {
		for _, provider := range providers {
			if !bad[provider] {
				continue
			}
			if outcome(provider) == OutcomeRecordBlocked {
				return compiledPackageOutcomeBlocksTask, provider
			}
			if p.Blocking[provider] {
				blockingProvider = provider
			}
		}
	}
	if blockingProvider != compiledPackageNoProviderIdentity {
		return compiledPackageOutcomeBlocksTask, blockingProvider
	}
	if p.OwnCause || p.ChannelChanged && (p.ChannelFrom == compiledPackageNoProviderIdentity || !bad[p.ChannelFrom]) {
		return compiledPackageOutcomeAllowsTask, compiledPackageNoProviderIdentity
	}
	for _, update := range p.Updates {
		if !bad[update] {
			return compiledPackageOutcomeAllowsTask, compiledPackageNoProviderIdentity
		}
	}
	return compiledPackageOutcomeBlocksTask, badProvider
}
