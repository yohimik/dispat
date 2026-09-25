// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// hangingCoordinator is a coordinator whose mailbox never answers: its close
// waits for its context, and it counts how often it was asked.
type hangingCoordinator struct {
	calls         int
	isLiveAtStart bool
}

func (c *hangingCoordinator) Close(ctx context.Context) error {
	c.calls++
	c.isLiveAtStart = ctx.Err() == nil
	<-ctx.Done()
	return ctx.Err()
}

// TestCloseCoordinatorIsBoundedAndClosesOnce: the close runs before the release
// locks go back, so a mailbox that never answers must cost a bounded wait
// rather than every lock of the run. It is detached from the run's
// cancellation, because an interrupted run has the most reason to tidy its
// refs, and only the first call closes anything: the closing phase closes the
// coordinator explicitly and the deferred call behind it has nothing to do.
func TestCloseCoordinatorIsBoundedAndClosesOnce(t *testing.T) {
	previous := coordinationCloseTimeout
	coordinationCloseTimeout = 50 * time.Millisecond
	t.Cleanup(func() { coordinationCloseTimeout = previous })
	var logs bytes.Buffer
	a := &App{log: zerolog.New(&logs)}
	run, cancel := context.WithCancel(t.Context())
	cancel()
	coordinator := &hangingCoordinator{}

	started := time.Now()
	a.closeCoordinator(run, coordinator)

	assert.Less(t, time.Since(started), 5*time.Second, "the close is bounded")
	require.Equal(t, 1, coordinator.calls)
	assert.True(t, coordinator.isLiveAtStart, "the run's cancellation does not reach the close")
	assert.Contains(t, logs.String(), execution.CodeTransportRetained, "a close that did not finish says so")

	a.closeCoordinator(run, coordinator)
	assert.Equal(t, 1, coordinator.calls, "a second close does nothing")
}
