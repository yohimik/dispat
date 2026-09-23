// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The decisions cancellation rests on, each as the pure question it is: which
// steps of a branch are legal, what an acknowledgement means for a
// publication, which locks a run may not give back, and when a lost lock is
// asked about again.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnAuthorizedPublisherMayBeWithdrawn: the state machine gains one step,
// and it is the one §28.6 needs. A withdrawal of an authorized publisher does
// not fence it, because the command may already have run; what it does is ask
// for the acknowledgement that says whether it had.
func TestAnAuthorizedPublisherMayBeWithdrawn(t *testing.T) {
	assert.True(t, IsTransitionLegal(MessageGo, MessageCancel, PartyOrchestrator))
	assert.True(t, IsTransitionLegal(MessageCancel, MessageAck, PartyWorker))
	assert.False(t, IsTransitionLegal(MessageGo, MessageCancel, PartyWorker),
		"only the party that owns the run withdraws an attempt")
	assert.False(t, IsTransitionLegal(MessageAck, MessageResult, PartyWorker),
		"an acknowledgement is terminal: no result follows one")
	assert.False(t, IsTransitionLegal(MessageAck, MessageCancel, PartyOrchestrator),
		"and an attempt both parties have settled is not withdrawn again")
}

// A worker may finish just before a withdrawal's expected-old push. Re-reading
// its signed terminal result or acknowledgement settles the attempt and also
// supplies Close's exact lease. A terminal-looking message with a bad signature
// or binding is neither evidence of stopped work nor a ref this run may delete.
func TestWithdrawalRereadOnlyOwnsItsTerminalMessage(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           MessageKind
		resultOf       string
		badSignature   bool
		badCancelBound bool
		isOwn          bool
	}{
		{name: "own result", kind: MessageResult, resultOf: PreflightTask, isOwn: true},
		{name: "another task result", kind: MessageResult, resultOf: "other:build"},
		{name: "wrong signature result", kind: MessageResult, resultOf: PreflightTask, badSignature: true},
		{name: "own acknowledgement", kind: MessageAck, isOwn: true},
		{name: "another withdrawal acknowledgement", kind: MessageAck, badCancelBound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := TransferLimits{MaxManifestBytes: 1 << 20}
			fixture := newCoordinatorFixture(t, limits, answeredPreflight)
			fixture.coordinator.Timeouts.Cancel = 5 * time.Second
			branch := FormatBranch("build-a", KindProbe, time.Now())
			assignment := probeAssignment("build-a", branch)
			offered, err := fixture.orchestrator.mailbox.Assign(t.Context(), assignment)
			require.NoError(t, err)
			heads, err := fixture.node.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
			require.NoError(t, err)
			require.Len(t, heads, 1)
			claimed, err := fixture.node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
				mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
			require.NoError(t, err)
			observed, err := fixture.orchestrator.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			require.Equal(t, claimed, observed.OID)
			fixture.coordinator.recordOwnedRef("build-a", branch, claimed)
			writer := fixture.node.mailbox
			if tc.badSignature {
				otherSigner, err := NewSigner("another-secret")
				require.NoError(t, err)
				writer = NewGitMailbox(fixture.node.endpoint, fixture.node.git, otherSigner, zerolog.Nop())
			}
			previous := claimed
			var document []byte
			if tc.kind == MessageAck {
				cancel, err := fixture.orchestrator.mailbox.Advance(t.Context(), branch, claimed,
					MessageCancel, mustMarshalValue(Withdrawal{
						Header: assignment.Header, Assignment: offered, Tip: claimed,
					}), nil)
				require.NoError(t, err)
				_, err = fixture.node.mailbox.Reread(t.Context(), branch)
				require.NoError(t, err)
				previous = cancel
				if tc.badCancelBound {
					cancel = offered
				}
				document = mustMarshalValue(Ack{Header: replyHeader(*assignment),
					Assignment: offered, Cancel: cancel})
			} else {
				result := Result{Header: replyHeader(*assignment), Assignment: offered, Status: StatusSucceeded}
				result.Task = tc.resultOf
				document = mustMarshalValue(result)
			}
			terminal, err := writer.Advance(t.Context(), branch, previous, tc.kind, document, nil)
			require.NoError(t, err)

			settled := fixture.coordinator.withdrawAttempt(t.Context(), "build-a", PreflightTask, 1,
				KindProbe, taskOffer{branch: branch, offered: offered}, claimed)

			if tc.isOwn {
				assert.True(t, settled.isAcknowledged, "the node's own terminal result proves it stopped")
				assert.Equal(t, terminal, fixture.coordinator.owned["build-a"][0].ExpectedOld)
				require.NoError(t, fixture.coordinator.Close(t.Context()))
				assert.Empty(t, fixture.orchestrator.remoteBranches(t))
				return
			}
			assert.False(t, settled.isAcknowledged, "an unbound terminal message does not settle this attempt")
			assert.False(t, isPublicationOutcomeKnown(settled),
				"a publisher with this answer would retain its unknown-outcome lock")
			assert.Equal(t, claimed, fixture.coordinator.owned["build-a"][0].ExpectedOld,
				"a foreign terminal tip never becomes this run's cleanup lease")
			require.Error(t, fixture.coordinator.Close(t.Context()),
				"the foreign tip must remain available for investigation")
			assert.Equal(t, []string{branch}, fixture.orchestrator.remoteBranches(t))
		})
	}
}

// TestWhatAnAcknowledgementSaysAboutAPublication: the decision table of
// §28.6, as the one sentence it is. An authorized publisher leaves a knowable
// outcome under exactly one condition, and every other row is a package that
// fails at publish with nothing inferred about the registry.
func TestWhatAnAcknowledgementSaysAboutAPublication(t *testing.T) {
	for name, tc := range map[string]struct {
		settled cancellation
		isKnown bool
	}{
		"acknowledged before its own command": {
			settled: cancellation{isAcknowledged: true, phase: PhaseAuthorizationWait},
			isKnown: true},
		"acknowledged before the command of a frame it had started": {
			settled: cancellation{isAcknowledged: true, phase: "before"},
			isKnown: true},
		"acknowledged after its command started": {
			settled: cancellation{isAcknowledged: true, phase: "commands", isCommandStarted: true}},
		"never acknowledged": {
			settled: cancellation{}},
		"never acknowledged, whatever it had reached before": {
			settled: cancellation{phase: "commands", isCommandStarted: true}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.isKnown, isPublicationOutcomeKnown(tc.settled))
		})
	}
}

// TestOnlyAnUnfencedPublisherRetainsItsLock: the selection is per repository
// and only for a publisher nobody heard from. A run that retained every lock
// it held because one package went wrong would be taking repositories out of
// service for a fact about a different one.
func TestOnlyAnUnfencedPublisherRetainsItsLock(t *testing.T) {
	coordinator := &Coordinator{Run: "run-1", owned: nil, Log: zerolog.Nop()}

	assert.Empty(t, coordinator.RetainedRepositories(), "a run with nothing unknown retains nothing")

	coordinator.rememberUnknownPublication(unknownPublication{
		Task: "ui:publish", Attempt: 1, Node: "build-a", Repository: "web", IsQuiesced: true})
	assert.Empty(t, coordinator.RetainedRepositories(),
		"an acknowledged publisher has provably stopped, so its repository is released")

	coordinator.rememberUnknownPublication(unknownPublication{
		Task: "app:publish", Attempt: 1, Node: "build-b", Repository: "api"})
	assert.Equal(t, []string{"api"}, coordinator.RetainedRepositories())

	coordinator.rememberUnknownPublication(unknownPublication{
		Task: "docs:publish", Attempt: 1, Node: "build-b", Repository: "api"})
	assert.Equal(t, []string{"api"}, coordinator.RetainedRepositories(),
		"one repository is named once however many of its publications are unknown")
	require.Len(t, coordinator.UnknownPublications(), 3,
		"every unknown outcome is still reported, whatever it does to a lock")
}

// TestOwnershipIsAskedAgainAndRememberedOnce: every new effect checks the
// remote, even immediately after a success; a loss is remembered, because
// ownership is not something a run gets back; and it ends in-flight attempts.
func TestOwnershipIsAskedAgainAndRememberedOnce(t *testing.T) {
	asked := 0
	lost := false
	coordinator := &Coordinator{Run: "run-1", Log: zerolog.Nop()}
	coordinator.VerifyOwnershipWith(func(context.Context) error {
		asked++
		if lost {
			return errors.New("the release lock is no longer on the remote")
		}
		return nil
	})

	inFlight, settled := coordinator.watchOwnership(t.Context())
	defer settled()

	require.NoError(t, coordinator.checkOwnership(t.Context()))
	require.NoError(t, coordinator.checkOwnership(t.Context()))
	assert.Equal(t, 2, asked, "a successful answer cannot authorize a later effect")
	require.NoError(t, inFlight.Err())

	lost = true
	err := coordinator.checkOwnership(t.Context())

	require.Error(t, err)
	assert.Equal(t, CodeLockLost, err.(interface{ DiagnosticCode() string }).DiagnosticCode())
	assert.Equal(t, CategoryNativeRecordingOrLock, DiagnosticCategory(err))
	assert.Error(t, inFlight.Err(), "the attempts already in flight are ended by the loss")
	assert.Equal(t, 3, asked)

	assert.Equal(t, err, coordinator.checkOwnership(t.Context()),
		"a lost lock is not asked about again: ownership is not something a run gets back")
	assert.Equal(t, 3, asked)

	// An attempt that asks for a context after the loss gets one that is
	// already ended, so nothing is placed under an exclusion this run lost.
	after, done := coordinator.watchOwnership(t.Context())
	defer done()
	assert.Error(t, after.Err())
}

func TestCancelledOwnershipLookupDoesNotReportLockLoss(t *testing.T) {
	coordinator := &Coordinator{Run: "run-1", Log: zerolog.Nop()}
	coordinator.VerifyOwnershipWith(func(ctx context.Context) error { return ctx.Err() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, coordinator.checkOwnership(ctx), context.Canceled)
	require.NoError(t, coordinator.checkOwnership(t.Context()),
		"an interrupted lookup did not establish that the remote lock was lost")
}

func TestOwnershipVerificationWaitCanBeCancelled(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	checks := 0
	coordinator := &Coordinator{Run: "run-1", Log: zerolog.Nop()}
	coordinator.VerifyOwnershipWith(func(context.Context) error {
		checks++
		if checks == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- coordinator.checkOwnership(t.Context()) }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	second := make(chan error, 1)
	go func() { second <- coordinator.checkOwnership(ctx) }()
	select {
	case err := <-second:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Error("cancelled verification remained blocked behind a remote lookup")
	}
	close(release)
	require.NoError(t, <-first)
	assert.Equal(t, 1, checks, "the cancelled waiter never queried the remote")
}

// TestACancelledTaskReportsWhatItHadReached: the progress a node carries
// across the boundary between its poll and its work. The command flag is
// sticky, because a publication whose command has started has an unknown
// outcome whatever the node does afterwards.
func TestACancelledTaskReportsWhatItHadReached(t *testing.T) {
	task := &claimedTask{expectedTip: "claim-oid"}

	task.reportPhase("before", false)
	cancel, phase, isCommandStarted := task.readProgress()
	assert.Empty(t, cancel)
	assert.Equal(t, "before", phase)
	assert.False(t, isCommandStarted)

	task.reportPhase("commands", true)
	task.reportPhase("after", false)
	_, phase, isCommandStarted = task.readProgress()
	assert.Equal(t, "after", phase)
	assert.True(t, isCommandStarted, "a command that started cannot be unstarted by a later phase")

	assert.True(t, task.withdraw("cancel-oid"))
	assert.False(t, task.withdraw("cancel-oid-2"),
		"the first withdrawal is the one the acknowledgement answers")
	cancel, _, _ = task.readProgress()
	assert.Equal(t, "cancel-oid", cancel)

	assert.Equal(t, "claim-oid", task.readExpectedTip())
	task.advanceTip("go-oid")
	assert.Equal(t, "go-oid", task.readExpectedTip())
}

// TestACancelledTaskThatRanNothingReportsInputs: a node cancelled while it was
// still assembling its checkout has run no part of the frame, and the phase it
// reports says so rather than being empty.
func TestACancelledTaskThatRanNothingReportsInputs(t *testing.T) {
	assert.Equal(t, "inputs", resolveCancelledPhase(""))
	assert.Equal(t, "commands", resolveCancelledPhase("commands"))
}
