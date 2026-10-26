// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// pinSweepSince resolves a user's revision once, before the first window
// selection. The first node and every peer then project the same entry commit
// through the fleet; a later branch or tag move cannot change the window.
func (a *App) pinSweepSince(ctx context.Context, stated string) (*execution.PinnedSinceAnchor, error) {
	if stated == pinnedPlanningAbsentText || stated == SinceAll {
		return nil, nil
	}
	commit, err := a.git.ResolveCommit(ctx, stated)
	if err != nil {
		return nil, fmt.Errorf(pinnedPlanningSinceRevisionResolutionErrorFormat, stated, err)
	}
	repository := pinnedPlanningAbsentText
	if a.workspace != nil {
		found := pinnedPlanningNotFoundState
		for _, candidate := range a.workspace.Repositories {
			if candidate.Root == a.workspace.ControlRoot {
				repository, found = candidate.Name, pinnedPlanningFoundState
				break
			}
		}
		if !found {
			return nil, fmt.Errorf(pinnedPlanningSweepEntryRepositoryAbsentErrorText)
		}
	}
	return &execution.PinnedSinceAnchor{Repository: repository, OID: commit}, nil
}
