//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestColdNativeNodeSlotNeedsExactSignedFinality(t *testing.T) {
	ctx := t.Context()
	task := "api:beforeAll"
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	authority, err := OpenNodeSlotAuthority(root, "node-a", 2)
	require.NoError(t, err)
	source, persist, binding, _ := newChoreographyLedgerFixtureWithAuthority(t,
		authority.AuthorityID(), ChoreographyTaskSpec{Task: task, Kind: KindBuild,
			Placement: PlacementOrchestrator, ExecutorOwned: true,
			EligibleNodes: []string{"node-a"}})
	first := source.second(t)
	reader := source.second(t)
	bootstrapOID := persist.opts.BootstrapOID
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding,
		GraphRootOID:              persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		JoinPolicy:                persist.opts.JoinPolicy,
		FirstNode:                 "node-a", Tasks: persist.opts.Tasks, Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", Platform: "linux/amd64",
			Capacity: 1, First: true, AuthorityID: authority.AuthorityID()}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		ResolveBindings: func(context.Context, string, string) (ChoreographyTaskBindings, error) {
			return ChoreographyTaskBindings{}, nil
		}, Persist: persist})
	require.NoError(t, err)
	_, err = broker.StartExecution(ctx)
	require.NoError(t, err)
	coordinator := &Coordinator{Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, GraphDigest: binding.GraphDigest,
		BootstrapOID: bootstrapOID, GraphRootOID: persist.opts.GraphRootOID,
		CanonicalEndpoint: source.endpoint, canonicalMailbox: source.mailbox,
		Local:                   LocalNode{Name: "node-a"},
		Pool:                    NewPool(nil, nil, LocalNode{Name: "node-a", Capacity: 1}, zerolog.Nop()),
		Links:                   []Link{{Name: "node-a", Endpoint: source.endpoint}},
		mailboxes:               map[string]*GitMailbox{"node-a": source.mailbox},
		Limits:                  TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20},
		choreographyInitialized: true, choreographyPersist: persist, choreographyBroker: broker}
	locator, err := coordinator.NativeNodeSlotLocator(ctx, task)
	require.NoError(t, err)
	lease, acquired, err := authority.TryReserve(ctx, NodeSlotIntent{Run: binding.Run,
		Task: task, Attempt: 1, Weight: 1, NativeRecovery: &locator})
	require.NoError(t, err)
	require.True(t, acquired)
	w := &Worker{Node: "node-a", Endpoint: source.endpoint, Mailbox: reader.mailbox,
		NodeSlots: authority, Report: NodeReport{Limits: TransferLimits{
			MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}, Log: zerolog.Nop()}
	_, _, err = ReadChoreographyLedgerRoot(ctx, reader.mailbox, binding, bootstrapOID)
	require.NoError(t, err, "the signed ledger root must be cold readable")
	_, err = reader.mailbox.ReadChoreographyBootstrap(ctx, bootstrapOID, 1<<20, 1<<20)
	require.NoError(t, err, "the retained bootstrap must be cold readable")
	require.NoError(t, w.RecoverHeldNodeSlots(ctx))
	held, err := authority.Held(ctx)
	require.NoError(t, err)
	require.Len(t, held, 1, "a native preclaim may still have a running denied-admission hook")
	ticket, err := coordinator.BeginChoreographyLocalTask(ctx, task, lease)
	require.NoError(t, err)
	_, err = broker.StopClaims(ctx)
	require.NoError(t, err)
	foreignBinding := ChoreographyRunBinding{Run: "other-run", Generation: "other-generation",
		PlanDigest: "other-plan", GraphDigest: "other-graph"}
	_, acquired, err = authority.TryReserve(ctx, NodeSlotIntent{Run: foreignBinding.Run,
		Task: "other:publish", Attempt: 1, Weight: 1,
		NativeRecovery: &NodeSlotNativeRecoveryLocator{Binding: foreignBinding,
			CanonicalEndpoint: "-untrusted-option", BootstrapOID: strings.Repeat("a", 40),
			LedgerRootOID: strings.Repeat("b", 40), Kind: KindPublish, Node: "node-a"}})
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, w.RecoverHeldNodeSlotsForRun(ctx, binding.Run),
		"retiring this run must not read another checkout with another key or endpoint")
	held, err = authority.Held(ctx)
	require.NoError(t, err)
	require.Len(t, held, 2, "a newer stopping epoch cannot prove native child processes stopped")
	require.NoError(t, ticket.Finish(ctx, true, ""))
	reopened, err := OpenNodeSlotAuthority(root, "node-a", 2)
	require.NoError(t, err)
	// A later invocation may use another canonical mailbox. The retained
	// locator, rather than today's configured endpoint, chooses the reader.
	other := newMailboxFixture(t)
	w.Endpoint = other.endpoint
	w.Mailbox = NewGitMailbox(other.endpoint, reader.git, first.mailbox.signer, zerolog.Nop())
	w.NodeSlots = reopened
	require.NoError(t, w.RecoverHeldNodeSlotsForRun(ctx, binding.Run))
	held, err = reopened.Held(ctx)
	require.NoError(t, err)
	require.Len(t, held, 1, "the exact signed native decision settles only its reopened lease")
	require.Equal(t, foreignBinding.Run, held[0].Intent.Run)
	_, acquired, err = reopened.TryReserve(ctx, NodeSlotIntent{Run: "later", Task: "other",
		Attempt: 1, Weight: lease.Intent.Weight})
	require.NoError(t, err)
	require.True(t, acquired)
}
