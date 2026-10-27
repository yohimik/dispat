// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "fmt"

const choreographyRetryPolicyMismatchError = "execution: signed retry safety policy differs from complete task graph"

func validateChoreographyRetryPolicy(policy ChoreographyJoinPolicy, specs []ChoreographyTaskSpec) error {
	for _, spec := range specs {
		if !spec.Virtual && !validChoreographyAttemptLimit(spec.EffectiveMaxAttempts()) {
			return fmt.Errorf(choreographyRetryPolicyMismatchError)
		}
		if policy.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified && !spec.Virtual {
			expected := policy.MaxAttempts
			if spec.ExecutorOwned {
				expected = choreographyAttemptPolicyFirstAttempt
			}
			if spec.MaxAttempts != expected {
				return fmt.Errorf(choreographyRetryPolicyMismatchError)
			}
		} else if policy.MaxAttempts == choreographyAttemptPolicyLegacyUnspecified &&
			spec.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified {
			return fmt.Errorf(choreographyRetryPolicyMismatchError)
		}
		allowed := policy.RetryAfterUncertainAttempt && !spec.Virtual && !spec.ExecutorOwned
		if spec.RetryAfterUncertainAttempt != allowed {
			return fmt.Errorf(choreographyRetryPolicyMismatchError)
		}
	}
	return nil
}
