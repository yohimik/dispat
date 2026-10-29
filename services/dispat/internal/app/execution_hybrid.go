// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import "github.com/yohimik/dispat/services/dispat/internal/execution"

const hybridModeName = "hybrid"

func bindHybridTaskPolicy(coordinator *execution.Coordinator,
	tasks []execution.ChoreographyTaskSpec, semanticGraphDigest string) error {
	if coordinator.Mode != hybridModeName {
		return nil
	}
	return execution.BindChoreographyHybridPolicy(execution.ChoreographyLogicalGraphInventory{
		Binding:             execution.ChoreographyRunBinding{PlanDigest: coordinator.PlanDigest},
		SemanticGraphDigest: semanticGraphDigest, Tasks: tasks,
	}, coordinator.PinnedInputs)
}
