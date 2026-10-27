// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

const (
	pinnedPlanningNoEffectiveConcurrency      = 0
	pinnedPlanningConcurrencyComponents       = 2
	pinnedPlanningSingleConcurrencyValueCount = 1
	pinnedPlanningBuildComponent              = 0
	pinnedPlanningPublishComponent            = 1
	pinnedPlanningConcurrencyMismatch         = "execution: captured effective concurrency differs from explicit planning configuration"
)

// pinPeerEffectiveConcurrency preserves the first node's resolved defaults
// when a peer has a different CPU count. Explicit source or CLI values must
// still agree with the signed effective policy.
func pinPeerEffectiveConcurrency(cfg *config.File, invocation pinnedSemanticInvocation) error {
	if len(invocation.EffectiveConcurrency) == pinnedPlanningNoEffectiveConcurrency {
		return nil // Earlier signed envelopes have no resolved-default field.
	}
	if cfg == nil || len(invocation.EffectiveConcurrency) != pinnedPlanningConcurrencyComponents ||
		invocation.EffectiveConcurrency[pinnedPlanningBuildComponent] <= pinnedPlanningNoEffectiveConcurrency ||
		invocation.EffectiveConcurrency[pinnedPlanningPublishComponent] <= pinnedPlanningNoEffectiveConcurrency {
		return fmt.Errorf(pinnedPlanningConcurrencyMismatch)
	}
	configured := [pinnedPlanningConcurrencyComponents]int{}
	switch len(cfg.Concurrency) {
	case pinnedPlanningNoEffectiveConcurrency:
	case pinnedPlanningSingleConcurrencyValueCount:
		configured[pinnedPlanningBuildComponent] = cfg.Concurrency[pinnedPlanningBuildComponent]
		configured[pinnedPlanningPublishComponent] = cfg.Concurrency[pinnedPlanningBuildComponent]
	case pinnedPlanningConcurrencyComponents:
		copy(configured[:], cfg.Concurrency)
	default:
		return fmt.Errorf(pinnedPlanningConcurrencyMismatch)
	}
	for index, value := range configured {
		if value > pinnedPlanningNoEffectiveConcurrency && value != invocation.EffectiveConcurrency[index] {
			return fmt.Errorf(pinnedPlanningConcurrencyMismatch)
		}
	}
	cfg.BuildConcurrency = invocation.EffectiveConcurrency[pinnedPlanningBuildComponent]
	cfg.PublishConcurrency = invocation.EffectiveConcurrency[pinnedPlanningPublishComponent]
	return nil
}
