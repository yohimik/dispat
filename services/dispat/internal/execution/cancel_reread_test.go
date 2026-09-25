// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cancellation placed directly on the assignment fences the only claim a
// worker could use to start commands. A worker that never saw the assignment
// cannot acknowledge the cancellation, so the run must settle immediately.
func TestWithdrawalOfUnclaimedAssignmentNeedsNoWorkerAcknowledgement(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	fixture.coordinator.Timeouts.Cancel = time.Second
	branch := FormatBranch("build-a", KindBuild, time.Now())
	assignment := probeAssignment("build-a", branch)
	assignment.Kind, assignment.Task = KindBuild, "app:build"
	offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
	require.NoError(t, err)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: offered,
	})

	settled := fixture.coordinator.withdrawAttempt(t.Context(), "build-a", "app:build", 1,
		KindBuild, taskOffer{branch: branch, kind: KindBuild, offered: offered}, offered)

	assert.True(t, settled.isAcknowledged, "the exact lease fenced any worker claim")
	assert.False(t, settled.isCommandStarted)
	assert.Equal(t, "queued", settled.phase)
	require.NoError(t, fixture.coordinator.Close(t.Context()))
	assert.Empty(t, fixture.orchestrator.remoteBranches(t))
}

// The real Git lease can lose twice: first to a Claim, then to a Result
// written while the coordinator is retrying on the Claim. The transport
// wrapper only schedules the second worker push; both leases and all messages
// still pass through the real bare repository and signature checks.
func TestWithdrawalSettlesAResultAfterTwoLeaseLosses(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	branch := FormatBranch("build-a", KindBuild, time.Now())
	assignment := probeAssignment("build-a", branch)
	assignment.Kind, assignment.Task = KindBuild, "app:build"
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

	var terminal string
	transport := &retryRaceTransport{transportx: fixture.orchestrator.mailbox.remote}
	transport.onRetry = func() error {
		var pushErr error
		terminal, pushErr = fixture.node.mailbox.Advance(t.Context(), branch, claimed, MessageResult,
			mustMarshalValue(Result{Header: replyHeader(*assignment), Assignment: offered,
				Status: StatusSucceeded}), nil)
		return pushErr
	}
	fixture.orchestrator.mailbox.remote = transport

	withdrawn, err := fixture.coordinator.writeWithdrawal(t.Context(), "build-a", "app:build", 1,
		KindBuild, taskOffer{branch: branch, kind: KindBuild, offered: offered}, offered)

	require.NoError(t, err, "the second reread must see the worker's signed terminal result")
	assert.Empty(t, withdrawn.oid, "no cancellation follows a completed task")
	assert.Equal(t, 2, transport.calls, "the coordinator attempted one original and one retried lease")
	assert.Equal(t, terminal, fixture.coordinator.owned["build-a"][0].ExpectedOld)
	require.NoError(t, fixture.coordinator.Close(t.Context()))
	assert.Empty(t, fixture.orchestrator.remoteBranches(t))
}

type retryRaceTransport struct {
	transportx
	onRetry func() error
	calls   int
}

func (transport *retryRaceTransport) PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error {
	transport.calls++
	if transport.calls == 2 {
		if err := transport.onRetry(); err != nil {
			return err
		}
	}
	return transport.transportx.PushAdvance(ctx, remote, oid, branch, expectedOld)
}

// Losing the cancellation push lease to a foreign live step cannot give the
// coordinator authority to sign a Cancel on top of that untrusted step.
func TestWithdrawalRetryRefusesForeignLiveTip(t *testing.T) {
	for _, foreignKind := range []MessageKind{MessageClaim, MessageReady, MessageGo} {
		t.Run(string(foreignKind), func(t *testing.T) {
			fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
			branch := FormatBranch("build-a", KindPublish, time.Now())
			assignment := probeAssignment("build-a", branch)
			assignment.Kind, assignment.Task = KindPublish, "core:publish"
			offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
			require.NoError(t, err)
			_, err = fixture.node.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			foreignSigner, err := NewSigner("another-secret")
			require.NoError(t, err)
			foreign := NewGitMailbox(fixture.node.endpoint, fixture.node.git, foreignSigner, zerolog.Nop())
			prior := offered
			if foreignKind != MessageClaim {
				prior, err = fixture.node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
					mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
				require.NoError(t, err)
			}
			if foreignKind == MessageGo {
				claimed := prior
				prior, err = fixture.node.mailbox.Advance(t.Context(), branch, claimed, MessageReady,
					mustMarshalValue(Ready{Header: replyHeader(*assignment),
						Assignment: offered, Claim: claimed}), nil)
				require.NoError(t, err)
			}
			var document []byte
			switch foreignKind {
			case MessageClaim:
				document = mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered})
			case MessageReady:
				document = mustMarshalValue(Ready{Header: replyHeader(*assignment),
					Assignment: offered, Claim: prior})
			case MessageGo:
				document = mustMarshalValue(Go{Header: replyHeader(*assignment),
					Assignment: offered, Ready: prior,
					NotAfter: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)})
			}
			foreignOID, err := foreign.Advance(t.Context(), branch, prior, foreignKind, document, nil)
			require.NoError(t, err)
			// The coordinator's known tip predates the foreign push. Its first
			// cancellation CAS loses; the reread must refuse the new tip.
			_, err = fixture.coordinator.writeWithdrawal(t.Context(), "build-a",
				"core:publish", 1, KindPublish,
				taskOffer{branch: branch, kind: KindPublish, offered: offered}, prior)
			require.Error(t, err)
			head, err := fixture.orchestrator.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			assert.Equal(t, foreignOID, head.OID, "foreign data must stay visible for investigation")
		})
	}
}

// The same lease race must still settle authentic progress. A Go that reached
// the remote before cancellation may have started an effect; an already
// pushed Cancel needs its acknowledgement, never another Cancel on top.
func TestWithdrawalRetryAcceptsOwnLiveTip(t *testing.T) {
	for _, movedKind := range []MessageKind{MessageClaim, MessageReady, MessageGo, MessageCancel} {
		t.Run(string(movedKind), func(t *testing.T) {
			fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
			branch := FormatBranch("build-a", KindPublish, time.Now())
			assignment := probeAssignment("build-a", branch)
			assignment.Kind, assignment.Task = KindPublish, "core:publish"
			offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
			require.NoError(t, err)
			_, err = fixture.node.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			claimed, err := fixture.node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
				mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
			require.NoError(t, err)
			prior, moved := offered, claimed
			if movedKind == MessageReady || movedKind == MessageGo {
				prior = claimed
				moved, err = fixture.node.mailbox.Advance(t.Context(), branch, claimed, MessageReady,
					mustMarshalValue(Ready{Header: replyHeader(*assignment),
						Assignment: offered, Claim: claimed}), nil)
				require.NoError(t, err)
			}
			if movedKind == MessageGo {
				prior = moved
				_, err = fixture.orchestrator.mailbox.Reread(t.Context(), branch)
				require.NoError(t, err)
				moved, err = fixture.orchestrator.mailbox.Advance(t.Context(), branch, prior, MessageGo,
					mustMarshalValue(Go{Header: fixture.coordinator.formatOrchestratorHeader(
						KindPublish, "core:publish", 1, "build-a", branch),
						Assignment: offered, Ready: prior,
						NotAfter: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}), nil)
				require.NoError(t, err)
			}
			if movedKind == MessageCancel {
				prior = claimed
				_, err = fixture.orchestrator.mailbox.Reread(t.Context(), branch)
				require.NoError(t, err)
				header := fixture.coordinator.formatOrchestratorHeader(
					KindPublish, "core:publish", 1, "build-a", branch)
				header.IssuedAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
				moved, err = fixture.orchestrator.mailbox.Advance(t.Context(), branch, prior, MessageCancel,
					mustMarshalValue(Withdrawal{Header: header, Assignment: offered, Tip: prior}), nil)
				require.NoError(t, err)
			}
			_, err = fixture.orchestrator.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			result, err := fixture.coordinator.writeWithdrawal(t.Context(), "build-a",
				"core:publish", 1, KindPublish,
				taskOffer{branch: branch, kind: KindPublish, offered: offered}, prior)
			require.NoError(t, err)
			if movedKind == MessageCancel {
				assert.Equal(t, moved, result.oid, "the first cancellation is already on the branch")
				assert.Equal(t, prior, result.parent)
			} else {
				assert.NotEmpty(t, result.oid)
				assert.Equal(t, moved, result.parent,
					"the new cancellation must answer the verified moved tip")
			}
		})
	}
}
