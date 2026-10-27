// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import "github.com/yohimik/dispat/services/dispat/internal/execution"

// applyChoreographyRetrySafety binds the run's explicit operator assertion
// into every delegable descriptor before the complete logical graph is hashed.
func applyChoreographyRetrySafety(specs []execution.ChoreographyTaskSpec, enabled bool) {
	for index := range specs {
		if !specs[index].Virtual && !specs[index].ExecutorOwned {
			specs[index].RetryAfterUncertainAttempt = enabled
		}
	}
}

// The interpretation count is signed into every nonvirtual descriptor. A
// first-node native effect stays single-attempt even when delegated commands
// may be safely repeated. Compilation leaves the historical limit omitted.
func applyChoreographyAttemptLimit(specs []execution.ChoreographyTaskSpec, policy execution.ChoreographyJoinPolicy) {
	if policy.MaxAttempts == 0 {
		return
	}
	for index := range specs {
		if specs[index].Virtual {
			continue
		}
		specs[index].MaxAttempts = policy.MaxAttempts
		if specs[index].ExecutorOwned {
			specs[index].MaxAttempts = 1
		}
	}
}
