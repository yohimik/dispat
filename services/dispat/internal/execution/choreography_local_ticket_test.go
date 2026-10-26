// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestFirstNodeNativeTicketClaimsBeforeEffectAndSettlesAfter(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Package: "core", ExecutorOwned: true,
			EligibleNodes: []string{"node-a"}},
		ChoreographyTaskSpec{Task: "consumer", Package: "consumer", Predecessors: []string{"build"}})
	brokerOptions := choreographyFixtureBrokerOptions(persist)
	brokerOptions.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), brokerOptions)
	require.NoError(t, err)
	c := &Coordinator{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, GraphDigest: binding.GraphDigest,
		BootstrapOID:            persist.opts.BootstrapOID,
		GraphRootOID:            persist.opts.GraphRootOID,
		Local:                   LocalNode{Name: "node-a"},
		Pool:                    NewPool(nil, nil, LocalNode{Name: "node-a", Capacity: 1}, zerolog.Nop()),
		Links:                   []Link{{Name: "node-a", Endpoint: fixture.endpoint}},
		mailboxes:               map[string]*GitMailbox{"node-a": fixture.mailbox},
		canonicalMailbox:        fixture.mailbox,
		Limits:                  TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20},
		choreographyInitialized: true, choreographyPersist: persist,
		choreographyBroker: broker}
	releasePool, err := c.ReserveFirstLocalPool(t.Context(), spec.Task)
	require.NoError(t, err)
	locator, err := c.NativeNodeSlotLocator(t.Context(), spec.Task)
	require.NoError(t, err)
	ordinary, err := c.Pool.AcquireNear(t.Context(), nil, PlacementOrchestrator, "")
	require.NoError(t, err, "the broker budget wait must not take a second physical pool lease")
	ordinary.Release()
	intent := NodeSlotIntent{Run: binding.Run, Task: spec.Task, Attempt: 1, Weight: 1}
	ticket, err := c.BeginChoreographyLocalTask(t.Context(), spec.Task,
		NodeSlotLease{ID: nodeSlotLeaseID(intent), Intent: intent})
	require.NoError(t, err)
	require.NotEmpty(t, ticket.claim)
	require.Equal(t, 1, broker.nodeUsed["node-a"], "a durable Claim holds broker capacity during native effects")
	require.NoError(t, ticket.Finish(t.Context(), true, ""))
	releasePool()
	ordinary, err = c.Pool.AcquireNear(t.Context(), nil, PlacementOrchestrator, "")
	require.NoError(t, err)
	ordinary.Release()
	require.Equal(t, 0, broker.nodeUsed["node-a"])
	require.True(t, broker.tasks[spec.Task].terminal)
	require.False(t, broker.tasks[spec.Task].pendingPackageSettlement)
	require.Zero(t, broker.tasks["consumer"].remaining,
		"an executor-owned terminal follows its completed package barrier")
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 8)
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
	require.Equal(t, ChoreographyResultAccepted, page.Events[1].Kind)
	require.Equal(t, ChoreographyTerminal, page.Events[2].Kind)
	feed := &RetainedChoreographyFeed{reader: persist, maxDocumentBytes: 1 << 20}
	require.NoError(t, feed.VerifyNativeTaskFinality(t.Context(), spec.Task,
		locator, page.Events[2]), "cold release must verify the exact native Claim/Result/Decision chain")
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	brokerOptions.Persist = coldPersist
	cold, err := RecoverFirstNodeBroker(t.Context(), brokerOptions)
	require.NoError(t, err)
	require.False(t, cold.tasks[spec.Task].pendingPackageSettlement)
	require.Zero(t, cold.tasks["consumer"].remaining,
		"cold replay must discharge the executor-owned package gate")
}

func TestLegacyFirstLocalPoolReservesPhysicalWeight(t *testing.T) {
	pool := NewPool(nil, nil, LocalNode{Name: "first", Capacity: 2}, zerolog.Nop())
	spec := ChoreographyTaskSpec{Task: "api:publish", Kind: "publish", Package: "api",
		LocalAuthority: true, Budget: ChoreographyBudgetPublish, Weight: 2,
		Placement:     PlacementOrchestrator,
		EligibleNodes: []string{"first"}}
	c := &Coordinator{Local: LocalNode{Name: "first"}, Pool: pool,
		choreographyBroker: &FirstNodeBroker{tasks: map[string]*indexedChoreographyTask{
			spec.Task: {spec: spec},
		}}}
	release, err := c.ReserveFirstLocalPool(t.Context(), spec.Task)
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = pool.AcquireNear(waitCtx, nil, PlacementOrchestrator, "")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	release()
	ordinary, err := pool.AcquireNear(t.Context(), nil, PlacementOrchestrator, "")
	require.NoError(t, err)
	ordinary.Release()
}
