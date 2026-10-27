// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/models"
)

const choreographyAttemptPolicyLegacyUnspecified = 0
const choreographyAttemptPolicyFirstAttempt = models.DefaultExecutionMaxAttempts
const choreographyAttemptPickupIdentityError = "execution: pickup identity differs from signed execution mode"

func (policy ChoreographyJoinPolicy) pickupMode() string {
	if policy.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified {
		return bootstrapInterpretationModeName
	}
	return bootstrapCompilationModeName
}

func (b *FirstNodeBroker) taskSpec(taskID string) ChoreographyTaskSpec {
	if b != nil && b.tasks[taskID] != nil {
		return b.tasks[taskID].spec
	}
	return ChoreographyTaskSpec{}
}

func validateChoreographySignedPickupID(policy ChoreographyJoinPolicy,
	spec ChoreographyTaskSpec, pickupID string) error {
	if spec.ExecutorOwned && pickupID == choreographyPickupMissingIdentity {
		return nil
	}
	if policy.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified &&
		pickupID == choreographyPickupMissingIdentity && !spec.Virtual {
		return fmt.Errorf(choreographyAttemptPickupIdentityError)
	}
	if pickupID != choreographyPickupMissingIdentity &&
		ValidateChoreographyPickupID(policy.pickupMode(), pickupID) != nil {
		return fmt.Errorf(choreographyAttemptPickupIdentityError)
	}
	return nil
}

func choreographyPickupAdmissionEvent(kind ChoreographyEventKind) bool {
	switch kind {
	case ChoreographyClaimAccepted, ChoreographyResultAccepted, ChoreographyAckAccepted:
		return true
	default:
		return false
	}
}

// EffectiveMaxAttempts returns the signed total accepted-pickup limit. An
// omitted value is the established compilation policy; interpretation writes
// an explicit value, including its one-attempt default, into every task.
func (spec ChoreographyTaskSpec) EffectiveMaxAttempts() int {
	if spec.MaxAttempts == choreographyAttemptPolicyLegacyUnspecified {
		return maxPlacementAttempts
	}
	return spec.MaxAttempts
}

func validChoreographyAttemptLimit(attempts int) bool {
	return attempts >= models.DefaultExecutionMaxAttempts &&
		attempts <= models.MaximumExecutionMaxAttempts
}

// A verified failed Result can be retried when the interpretation policy
// explicitly permits multiple pickups. Legacy compilation keeps its existing
// retry-safety declaration. A cancelled/unknown pickup still needs that
// separate declaration and a signed cancellation fence.
func (spec ChoreographyTaskSpec) IsSettledFailureRetryAllowed() bool {
	if spec.ExecutorOwned {
		return false
	}
	if spec.MaxAttempts != choreographyAttemptPolicyLegacyUnspecified {
		return spec.MaxAttempts > choreographyAttemptPolicyFirstAttempt
	}
	return spec.RetryAfterUncertainAttempt
}

func (spec ChoreographyTaskSpec) IsPickupRetryAllowed(resultAccepted bool) bool {
	if resultAccepted {
		return spec.IsSettledFailureRetryAllowed()
	}
	return spec.RetryAfterUncertainAttempt && !spec.ExecutorOwned
}
