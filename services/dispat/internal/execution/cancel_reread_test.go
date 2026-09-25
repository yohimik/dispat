// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/release"
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

// TestWithdrawalKeepsWhatAPublisherReported: a run that withdraws a
// publication it authorized reads what the node already wrote in its place.
// The node's own success is a publication, recorded as if it had arrived a
// moment earlier, and its own failure is a known failure; an acknowledgement
// of an earlier withdrawal says whether the command had started, and only a
// started command without a result leaves the outcome unknown.
func TestWithdrawalKeepsWhatAPublisherReported(t *testing.T) {
	for _, tc := range []struct {
		name       string
		terminal   func(*publishChain) (MessageKind, string, any)
		isWithheld bool
		isUnknown  bool
		isFailure  bool
	}{
		{name: "a success the node reported", terminal: func(c *publishChain) (MessageKind, string, any) {
			return MessageResult, c.authorized, Result{Header: c.reply(), Assignment: c.offered,
				Status: StatusSucceeded}
		}},
		{name: "a failure the node reported", isFailure: true,
			terminal: func(c *publishChain) (MessageKind, string, any) {
				return MessageResult, c.authorized, Result{Header: c.reply(), Assignment: c.offered,
					Status: StatusFailed, FailedPart: "commands"}
			}},
		{name: "an acknowledgement after the command started", isUnknown: true,
			terminal: func(c *publishChain) (MessageKind, string, any) {
				cancel := c.withdrawEarlier(c.authorized)
				return MessageAck, cancel, Ack{Header: c.reply(), Assignment: c.offered, Cancel: cancel,
					Phase: "commands", CommandStarted: true}
			}},
		{name: "an acknowledgement before the command started", isFailure: true,
			terminal: func(c *publishChain) (MessageKind, string, any) {
				cancel := c.withdrawEarlier(c.authorized)
				return MessageAck, cancel, Ack{Header: c.reply(), Assignment: c.offered, Cancel: cancel,
					Phase: PhaseAuthorizationWait}
			}},
		{name: "a withheld publisher whose command started", isWithheld: true, isUnknown: true,
			terminal: func(c *publishChain) (MessageKind, string, any) {
				cancel := c.withdrawEarlier(c.ready)
				return MessageAck, cancel, Ack{Header: c.reply(), Assignment: c.offered, Cancel: cancel,
					Phase: "commands", CommandStarted: true}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := newPublishChain(t, !tc.isWithheld)
			kind, previous, message := tc.terminal(chain)
			_, err := chain.fixture.node.mailbox.Reread(t.Context(), chain.branch)
			require.NoError(t, err)
			_, err = chain.fixture.node.mailbox.Advance(t.Context(), chain.branch, previous, kind,
				mustMarshalValue(message), nil)
			require.NoError(t, err)
			pub := publicationAttempt{lease: chain.lease, task: chain.assignment.Task, attempt: 1,
				repository: "web", offer: taskOffer{branch: chain.branch, kind: KindPublish, offered: chain.offered}}

			var outcome release.StageOutcome
			if tc.isWithheld {
				refused := errors.New("the inputs moved")
				outcome, err = chain.fixture.coordinator.withdrawPublication(t.Context(), pub,
					release.StageOutcome{}, taskReply{kind: MessageReady, commit: chain.ready}, refused)
			} else {
				outcome, err = chain.fixture.coordinator.resolveUnansweredPublication(t.Context(), pub,
					release.StageOutcome{}, chain.authorized)
			}

			unknown := chain.fixture.coordinator.UnknownPublications()
			if tc.isUnknown {
				require.Error(t, err)
				assert.Equal(t, CodePublicationUnknown, config.DiagnosticCode(err))
				require.Len(t, unknown, 1)
				assert.True(t, unknown[0].IsQuiesced, "the node answered, so it has stopped")
				return
			}
			assert.Empty(t, unknown, "the node said what became of it")
			assert.Empty(t, chain.fixture.coordinator.RetainedRepositories())
			if tc.isFailure {
				require.Error(t, err)
				assert.NotEqual(t, CodePublicationUnknown, config.DiagnosticCode(err))
				return
			}
			require.NoError(t, err, "a success the node reported is a publication")
			assert.Empty(t, outcome.FailedPart)
		})
	}
}

// publishChain is one delegated publication on a real mailbox, written up to
// the authorization: assignment, claim, ready and go.
type publishChain struct {
	t          *testing.T
	fixture    *coordinatorFixture
	assignment *Assignment
	lease      *Lease
	branch     string
	offered    string
	ready      string
	authorized string
}

func newPublishChain(t *testing.T, isAuthorized bool) *publishChain {
	t.Helper()
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	fixture.coordinator.Timeouts.Cancel = 500 * time.Millisecond
	pool := NewPool([]Link{{Name: "build-a", Endpoint: fixture.orchestrator.endpoint}},
		[]*NodeReport{linuxNode(1)}, LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())
	lease, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "")
	require.NoError(t, err)
	chain := &publishChain{t: t, fixture: fixture, lease: lease,
		branch: FormatBranch("build-a", KindPublish, time.Now())}
	chain.assignment = probeAssignment("build-a", chain.branch)
	chain.assignment.Kind, chain.assignment.Task = KindPublish, "core:publish"
	chain.offered, err = assign(t.Context(), fixture.orchestrator.mailbox, chain.assignment)
	require.NoError(t, err)
	_, err = fixture.node.mailbox.Reread(t.Context(), chain.branch)
	require.NoError(t, err)
	claimed, err := fixture.node.mailbox.Advance(t.Context(), chain.branch, chain.offered, MessageClaim,
		mustMarshalValue(Claim{Header: chain.reply(), Assignment: chain.offered}), nil)
	require.NoError(t, err)
	chain.ready, err = fixture.node.mailbox.Advance(t.Context(), chain.branch, claimed, MessageReady,
		mustMarshalValue(Ready{Header: chain.reply(), Assignment: chain.offered, Claim: claimed}), nil)
	require.NoError(t, err)
	_, err = fixture.orchestrator.mailbox.Reread(t.Context(), chain.branch)
	require.NoError(t, err)
	if !isAuthorized {
		return chain
	}
	chain.authorized, err = fixture.orchestrator.mailbox.Advance(t.Context(), chain.branch, chain.ready,
		MessageGo, mustMarshalValue(Go{Header: chain.orchestratorHeader(0), Assignment: chain.offered,
			Ready: chain.ready, NotAfter: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}), nil)
	require.NoError(t, err)
	return chain
}

// reply is the header the node writes back.
func (c *publishChain) reply() Header { return replyHeader(*c.assignment) }

// orchestratorHeader is the run's own header, issued the given time ago so
// that two withdrawals of one second are two objects.
func (c *publishChain) orchestratorHeader(ago time.Duration) Header {
	header := c.fixture.coordinator.formatOrchestratorHeader(KindPublish, c.assignment.Task, 1,
		"build-a", c.branch)
	header.IssuedAt = time.Now().Add(-ago).UTC().Format(time.RFC3339)
	return header
}

// withdrawEarlier writes an earlier withdrawal of this run's on top of tip, as
// a run whose response to it was lost would have left it.
func (c *publishChain) withdrawEarlier(tip string) string {
	c.t.Helper()
	cancel, err := c.fixture.orchestrator.mailbox.Advance(c.t.Context(), c.branch, tip, MessageCancel,
		mustMarshalValue(Withdrawal{Header: c.orchestratorHeader(time.Minute), Assignment: c.offered,
			Tip: tip}), nil)
	require.NoError(c.t, err)
	return cancel
}
