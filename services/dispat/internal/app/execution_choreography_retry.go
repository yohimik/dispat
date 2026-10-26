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
