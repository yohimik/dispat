// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

const (
	retainedTaskPolicyCountError   = "execution: retained complete graph has another task policy count"
	retainedTaskPolicyErrorFormat  = "execution: retained complete graph task %q differs from independently compiled policy"
	retainedPackagePolicyErrorText = "execution: retained complete graph package policy differs from independently compiled plan"
)

type retainedChoreographyPolicyOptions struct {
	RecomputedTasks    []execution.ChoreographyTaskSpec
	SignedTasks        []execution.ChoreographyTaskSpec
	RecomputedPackages []execution.ChoreographyPackagePolicy
	SignedPackages     []execution.ChoreographyPackagePolicy
}

// verifyRetainedChoreographyPolicy compares the semantic parts independently
// reconstructed from pinned planning inputs with the authenticated carrier.
// Ref OIDs, templates, and live eligible-node sets have separate exact checks.
func verifyRetainedChoreographyPolicy(options retainedChoreographyPolicyOptions) error {
	recomputed, signed := options.RecomputedTasks, options.SignedTasks
	packagePolicies, signedPackagePolicies := options.RecomputedPackages, options.SignedPackages
	if len(recomputed) != len(signed) {
		return fmt.Errorf(retainedTaskPolicyCountError)
	}
	byTask := make(map[string]execution.ChoreographyTaskSpec, len(recomputed))
	for _, task := range recomputed {
		if task.Task == retainedRunAbsentValueText || byTask[task.Task].Task != retainedRunAbsentValueText {
			return fmt.Errorf(retainedTaskPolicyErrorFormat, task.Task)
		}
		byTask[task.Task] = task
	}
	for _, task := range signed {
		want, found := byTask[task.Task]
		if !found || task.Package != want.Package || task.Kind != want.Kind ||
			task.Stage != want.Stage || task.Recording != want.Recording ||
			task.Virtual != want.Virtual || task.LocalAuthority != want.LocalAuthority ||
			task.ExecutorOwned != want.ExecutorOwned ||
			task.RetryAfterUncertainAttempt != want.RetryAfterUncertainAttempt ||
			task.NeedsPreparedInput != want.NeedsPreparedInput ||
			task.Budget != want.Budget || task.Weight != want.Weight ||
			task.Placement != want.Placement ||
			!slices.Equal(task.Platforms, want.Platforms) ||
			!slices.Equal(task.Predecessors, want.Predecessors) ||
			!slices.Equal(task.EligibleNodes, want.EligibleNodes) {
			return fmt.Errorf(retainedTaskPolicyErrorFormat, task.Task)
		}
	}
	ordered := slices.Clone(packagePolicies)
	slices.SortFunc(ordered, func(a, b execution.ChoreographyPackagePolicy) int {
		return cmp.Compare(a.Package, b.Package)
	})
	if len(ordered) != len(signedPackagePolicies) {
		return fmt.Errorf(retainedPackagePolicyErrorText)
	}
	for index, want := range ordered {
		got := signedPackagePolicies[index]
		if got.Package != want.Package || got.Mode != want.Mode || got.OnError != want.OnError ||
			got.ChannelFrom != want.ChannelFrom || got.OwnCause != want.OwnCause ||
			got.ChannelChanged != want.ChannelChanged ||
			!slices.Equal(got.Providers, want.Providers) ||
			!slices.Equal(got.Reached, want.Reached) || !slices.Equal(got.DueTo, want.DueTo) ||
			!slices.Equal(got.Updates, want.Updates) || !slices.Equal(got.Direct, want.Direct) ||
			!maps.Equal(got.Blocking, want.Blocking) {
			return fmt.Errorf(retainedPackagePolicyErrorText)
		}
	}
	return nil
}
