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

// TestOwnershipIsAskedAgainAndRememberedOnce: the check before every new
// assignment is cached for a few seconds, so a fan-out costs one query rather
// than one per task; a loss is remembered, because ownership is not something
// a run gets back; and a loss ends the attempts already in flight.
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
	assert.Equal(t, 1, asked, "the answer is cached, so a fan-out asks once")
	require.NoError(t, inFlight.Err())

	lost = true
	coordinator.ownership.checked = time.Now().Add(-2 * ownershipCacheLifetime)
	err := coordinator.checkOwnership(t.Context())

	require.Error(t, err)
	assert.Equal(t, CodeLockLost, err.(interface{ DiagnosticCode() string }).DiagnosticCode())
	assert.Equal(t, CategoryNativeRecordingOrLock, DiagnosticCategory(err))
	assert.Error(t, inFlight.Err(), "the attempts already in flight are ended by the loss")
	assert.Equal(t, 2, asked)

	assert.Equal(t, err, coordinator.checkOwnership(t.Context()),
		"a lost lock is not asked about again: ownership is not something a run gets back")
	assert.Equal(t, 2, asked)

	// An attempt that asks for a context after the loss gets one that is
	// already ended, so nothing is placed under an exclusion this run lost.
	after, done := coordinator.watchOwnership(t.Context())
	defer done()
	assert.Error(t, after.Err())
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
