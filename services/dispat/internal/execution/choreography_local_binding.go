// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// ResolveLocalChoreographyBindings binds a first-node owned command to its
// signed ready state under the broker's claim/effect read lane. The accepted
// Assignment freezes captured source and input carriers before the Executor
// begins an effect, so this path needs no peer preparation handshake.
func (c *Coordinator) ResolveLocalChoreographyBindings(ctx context.Context, node string,
	request release.StageRequest, state ChoreographyVerifiedBindingState) (ChoreographyTaskBindings, error) {
	return c.resolveChoreographyBindingsFresh(ctx, node, request, state)
}
