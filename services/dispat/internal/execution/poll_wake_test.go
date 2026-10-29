// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestTaskCompletionWakesMailboxWithoutChangingIdlePolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		worker, mailbox := newWorkerFixture(t, "", ChainTip{}, nil)
		mailbox.heads = nil
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		worker.beginTask()
		stopped := make(chan string, 1)
		go func() { stopped <- worker.poll(ctx) }()
		synctest.Wait()
		initialPolls := mailbox.polls
		require.Positive(t, initialPolls)

		time.Sleep(minimumPollInterval)
		synctest.Wait()
		require.Equal(t, initialPolls, mailbox.polls, "an unchanged mailbox keeps its idle backoff")
		worker.endTask()
		synctest.Wait()
		require.Greater(t, mailbox.polls, initialPolls, "the released slot is reconsidered without waiting for the next timer")
		completedPolls := mailbox.polls
		synctest.Wait()
		require.Equal(t, completedPolls, mailbox.polls, "a completion is a single wake, not a busy loop")
		cancel()
		require.Equal(t, StopSignal, <-stopped)
	})
}

type probeWakeTransport struct {
	transportx
	polls int
	err   error
}

func (p *probeWakeTransport) ListRemoteHeads(context.Context, string, string) ([]gitx.RemoteHead, error) {
	p.polls++
	return nil, p.err
}

func TestProbeNotificationImmediatelyRereadsCanonicalMailbox(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := &probeWakeTransport{}
		mailbox := NewGitMailbox("file:///mailbox", nil, nil, zerolog.Nop())
		mailbox.remote = transport
		coordinator := &Coordinator{mailboxes: map[string]*GitMailbox{"worker": mailbox}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		finished := make(chan error, 1)
		go func() {
			_, err := coordinator.awaitReport(ctx, Link{Name: "worker"}, "probe", "offered")
			finished <- err
		}()
		synctest.Wait()
		require.Equal(t, 1, transport.polls, "a reply already present is read before waiting")
		coordinator.notifyProbeReply("foreign", "probe")
		synctest.Wait()
		require.Equal(t, 1, transport.polls, "another worker cannot wake this probe")
		transport.err = errors.New("canonical mailbox unavailable")
		coordinator.notifyProbeReply("worker", "probe")
		synctest.Wait()
		require.Equal(t, 2, transport.polls)
		require.ErrorIs(t, <-finished, transport.err, "the wake never substitutes a callback for a verified Git result")
		_, remains := coordinator.probeWaiters.Load(probeWaiterKey{node: "worker", branch: "probe"})
		require.False(t, remains)
	})
}
