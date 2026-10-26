// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	packagePolicyMissingErrorFormat    = "execution: compiled package policy for %q is absent"
	packagePolicyOutcomeErrorFormat    = "execution: compiled package %q has invalid outcome %q"
	packagePolicyTransitionErrorFormat = "execution: compiled package %q changed settled outcome from %q to %q"
)

// ChoreographyPackagePolicy carries the complete immutable §19.3 skip inputs
// in the signed graph root. A worker applies outcomes from signed events.
type ChoreographyPackagePolicy struct {
	Package        string          `json:"package"`
	Mode           string          `json:"mode,omitempty"`
	OnError        string          `json:"onError,omitempty"`
	Providers      []string        `json:"providers,omitempty"`
	Reached        []string        `json:"reached,omitempty"`
	DueTo          []string        `json:"dueTo,omitempty"`
	Updates        []string        `json:"updates,omitempty"`
	ChannelFrom    string          `json:"channelFrom,omitempty"`
	Direct         []string        `json:"direct,omitempty"`
	Blocking       map[string]bool `json:"blocking,omitempty"`
	OwnCause       bool            `json:"ownCause,omitempty"`
	ChannelChanged bool            `json:"channelChanged,omitempty"`
}

type ChoreographyPackageOutcomeEvaluator struct {
	policies map[string]ChoreographyPackagePolicy
	outcomes map[string]string
}

func NewChoreographyPackageOutcomeEvaluator(policies []ChoreographyPackagePolicy) (*ChoreographyPackageOutcomeEvaluator, error) {
	e := &ChoreographyPackageOutcomeEvaluator{policies: make(map[string]ChoreographyPackagePolicy, len(policies)), outcomes: make(map[string]string, len(policies))}
	for _, policy := range policies {
		if policy.Package == "" || e.policies[policy.Package].Package != "" {
			return nil, fmt.Errorf(packagePolicyMissingErrorFormat, policy.Package)
		}
		e.policies[policy.Package] = policy
	}
	return e, nil
}

func (e *ChoreographyPackageOutcomeEvaluator) ValidateOutcome(pkg, status string) error {
	if e == nil || e.policies[pkg].Package == "" {
		return fmt.Errorf(packagePolicyMissingErrorFormat, pkg)
	}
	switch status {
	case "published", "failed", "skipped", "record-blocked", "cancelled":
	default:
		return fmt.Errorf(packagePolicyOutcomeErrorFormat, pkg, status)
	}
	prior := e.outcomes[pkg]
	if prior != "" && prior != status && !(prior == "published" && status == "record-blocked") {
		return fmt.Errorf(packagePolicyTransitionErrorFormat, pkg, prior, status)
	}
	return nil
}

func (e *ChoreographyPackageOutcomeEvaluator) ApplyOutcome(pkg, status string) error {
	if err := e.ValidateOutcome(pkg, status); err != nil {
		return err
	}
	e.outcomes[pkg] = status
	return nil
}

func (e *ChoreographyPackageOutcomeEvaluator) ShouldSkip(pkg string) (bool, string, error) {
	if e == nil || e.policies[pkg].Package == "" {
		return false, "", fmt.Errorf(packagePolicyMissingErrorFormat, pkg)
	}
	p := e.policies[pkg]
	if p.Mode == KindRun {
		if p.OnError == "continue" {
			return false, "", nil
		}
		if p.OnError != "skip" {
			return false, "", fmt.Errorf(packagePolicyMissingErrorFormat, pkg)
		}
		for _, provider := range p.Direct {
			if e.outcomes[provider] == "failed" || e.outcomes[provider] == "skipped" {
				return true, provider, nil
			}
		}
		return false, "", nil
	}
	skip, blocker := release.EvaluateCompiledPackagePolicy(release.CompiledPackagePolicy{
		Package: p.Package, Providers: p.Providers, Reached: p.Reached,
		DueTo: p.DueTo, Updates: p.Updates, ChannelFrom: p.ChannelFrom,
		Direct: p.Direct, Blocking: p.Blocking, OwnCause: p.OwnCause,
		ChannelChanged: p.ChannelChanged,
	}, func(provider string) release.PackageOutcome { return release.PackageOutcome(e.outcomes[provider]) })
	return skip, blocker, nil
}
