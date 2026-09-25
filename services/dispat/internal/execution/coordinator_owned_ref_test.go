// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A poll can hold an old, authentic claim while cancellation writes an
// acknowledgement. Accepting that delayed claim must not rewind Close's lease.
func TestDelayedObservedClaimCannotReplaceAcknowledgedCleanupLease(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	assignment := probeAssignment("build-a", branch)
	offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
	require.NoError(t, err)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: offered,
	})
	_, err = fixture.node.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	claimed, err := fixture.node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
	require.NoError(t, err)
	stale, err := fixture.orchestrator.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	require.Equal(t, claimed, stale.OID)
	withdrawn, err := fixture.orchestrator.mailbox.Advance(t.Context(), branch, claimed, MessageCancel,
		mustMarshalValue(Withdrawal{Header: assignment.Header, Assignment: offered, Tip: claimed}), nil)
	require.NoError(t, err)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: withdrawn, parent: claimed,
	})
	_, err = fixture.node.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	ackOID, err := fixture.node.mailbox.Advance(t.Context(), branch, withdrawn, MessageAck,
		mustMarshalValue(Ack{Header: replyHeader(*assignment), Assignment: offered, Cancel: withdrawn}), nil)
	require.NoError(t, err)
	head, err := fixture.orchestrator.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	require.Equal(t, ackOID, head.OID)
	tip, err := fixture.orchestrator.mailbox.Inspect(t.Context(), head)
	require.NoError(t, err)
	_, reason := fixture.coordinator.readAcknowledgement(t.Context(), "build-a", PreflightTask, 1,
		taskOffer{branch: branch, kind: KindProbe, offered: offered}, tip, withdrawn)
	require.Empty(t, reason)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: tip.OID, parent: tip.PreviousOID,
	})

	watcher := &watcher{
		coordinator: fixture.coordinator, link: fixture.coordinator.Links[0],
		mailbox: fixture.orchestrator.mailbox,
		attempts: map[string]*attemptState{branch: {
			offered: offered, assignment: assignment, replies: make(chan taskReply, 3),
		}},
	}
	require.True(t, watcher.inspect(t.Context(), stale), "the older claim is authentic")
	assert.Equal(t, ackOID, fixture.coordinator.owned["build-a"][0].ExpectedOld,
		"delayed observation cannot rewind the cleanup lease")
	require.NoError(t, fixture.coordinator.Close(t.Context()))
	assert.Empty(t, fixture.orchestrator.remoteBranches(t))
}

// A writer who can move the Git ref but cannot sign for this run must never
// cause this run to delete that writer's tip.
func TestForeignObservedTipCannotBecomeCleanupLease(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	assignment := probeAssignment("build-a", branch)
	offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
	require.NoError(t, err)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: offered,
	})
	_, err = fixture.node.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	foreignSigner, err := NewSigner("another-secret")
	require.NoError(t, err)
	foreign := NewGitMailbox(fixture.node.endpoint, fixture.node.git, foreignSigner, zerolog.Nop())
	_, err = foreign.Advance(t.Context(), branch, offered, MessageResult,
		mustMarshalValue(Result{Header: replyHeader(*assignment), Assignment: offered, Status: StatusSucceeded}), nil)
	require.NoError(t, err)
	head, err := fixture.orchestrator.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	watcher := &watcher{
		coordinator: fixture.coordinator, link: fixture.coordinator.Links[0],
		mailbox: fixture.orchestrator.mailbox,
		attempts: map[string]*attemptState{branch: {
			offered: offered, assignment: assignment, replies: make(chan taskReply, 3),
		}},
	}
	assert.False(t, watcher.inspect(t.Context(), head))
	assert.Equal(t, offered, fixture.coordinator.owned["build-a"][0].ExpectedOld)
	require.Error(t, fixture.coordinator.Close(t.Context()))
	assert.Equal(t, []string{branch}, fixture.orchestrator.remoteBranches(t))
}
