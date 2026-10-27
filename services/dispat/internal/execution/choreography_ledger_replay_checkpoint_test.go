// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyReplayCheckpointMalformedSequenceOffset = 2
	choreographyReplayCheckpointNextSequenceOffset      = 1
	choreographyReplayCheckpointMissingIndex            = false
	choreographyReplayCheckpointInvalidEventError       = "invalid signed choreography event"
	choreographyReplayCheckpointInvalidStartError       = "signed execution start differs"
	choreographyReplayCheckpointTreeCommand             = "ls-tree"
	choreographyReplayCheckpointAbortRun                = true
)

type choreographyReplayCancelOnObjectRead struct {
	oid    string
	cancel context.CancelFunc
	once   sync.Once
}

func (writer *choreographyReplayCancelOnObjectRead) Write(content []byte) (int, error) {
	if bytes.Contains(content, []byte(choreographyReplayCheckpointTreeCommand)) &&
		bytes.Contains(content, []byte(writer.oid)) {
		writer.once.Do(writer.cancel)
	}
	return len(content), nil
}

type choreographyReplayCheckpointAppend struct {
	Fixture *mailboxFixture
	Persist *GitChoreographyPersist
	Prior   ChoreographyEventCursor
	Event   ChoreographyEvent
}

func appendChoreographyReplayCheckpointEvent(t *testing.T, options choreographyReplayCheckpointAppend) string {
	t.Helper()
	record := choreographyLedgerRecord{ChoreographyRunBinding: options.Persist.opts.Binding,
		Sequence: options.Event.Sequence, Event: &options.Event}
	document, err := json.Marshal(record)
	require.NoError(t, err)
	oid, err := options.Fixture.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		document, []string{options.Prior.OID, options.Event.TaskOID}, nil)
	require.NoError(t, err)
	require.NoError(t, options.Fixture.git.PushAtomicAdvances(t.Context(), options.Fixture.endpoint,
		[]gitx.BranchAdvance{{Branch: options.Persist.opts.Ledger.Branch,
			ExpectedOld: options.Prior.OID, OID: oid}}))
	return oid
}

func TestChoreographyReplayCheckpointsVerifiedPrefixBeforeInvalidSuffix(t *testing.T) {
	fixture, persist, _, _ := newChoreographyLedgerFixture(t)
	stopped, err := persist.StopClaims(t.Context())
	require.NoError(t, err)
	malformed := ChoreographyEvent{Sequence: stopped.Sequence + choreographyReplayCheckpointMalformedSequenceOffset,
		Kind: ChoreographyRunStopping, Task: choreographyRunTaskID,
		TaskOID: persist.opts.BootstrapOID}
	invalidOID := appendChoreographyReplayCheckpointEvent(t, choreographyReplayCheckpointAppend{
		Fixture: fixture, Persist: persist, Prior: stopped, Event: malformed})
	replayed, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	_, err = replayed.LedgerHead(t.Context())
	require.ErrorContains(t, err, choreographyReplayCheckpointInvalidEventError)
	require.Equal(t, stopped.OID, replayed.ledgerTip)
	require.Equal(t, stopped.Sequence, replayed.sequence)
	require.Len(t, replayed.events, int(stopped.Sequence))
	_, found := replayed.eventIndex[invalidOID]
	require.Equal(t, choreographyReplayCheckpointMissingIndex, found)
	_, err = replayed.LedgerHead(t.Context())
	require.ErrorContains(t, err, choreographyReplayCheckpointInvalidEventError)
	require.Equal(t, stopped.OID, replayed.ledgerTip)
	require.Len(t, replayed.events, int(stopped.Sequence))
}

func TestChoreographyReplayRejectsInvalidExecutionStartBeforeMutation(t *testing.T) {
	fixture, persist, _, _ := newChoreographyLedgerFixture(t)
	started, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	invalid := persist.events[len(persist.events)-choreographyLedgerSingleItemCount]
	invalid.Sequence = started.Sequence + choreographyReplayCheckpointNextSequenceOffset
	invalid.LedgerOID = choreographyLedgerMissingIdentity
	invalidOID := appendChoreographyReplayCheckpointEvent(t, choreographyReplayCheckpointAppend{
		Fixture: fixture, Persist: persist, Prior: started, Event: invalid})
	replayed, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	_, err = replayed.LedgerHead(t.Context())
	require.ErrorContains(t, err, choreographyReplayCheckpointInvalidStartError)
	require.Equal(t, started.OID, replayed.ledgerTip)
	require.Equal(t, started.Sequence, replayed.sequence)
	require.Len(t, replayed.events, int(started.Sequence))
	_, found := replayed.eventIndex[invalidOID]
	require.Equal(t, choreographyReplayCheckpointMissingIndex, found)
}

func TestChoreographyReplayResumesAfterInterruptedSignedSuffixRead(t *testing.T) {
	fixture, persist, _, _ := newChoreographyLedgerFixture(t)
	stopped, err := persist.StopClaims(t.Context())
	require.NoError(t, err)
	sealed, err := persist.SealRun(t.Context(), choreographyReplayCheckpointAbortRun)
	require.NoError(t, err)
	replayed, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	interrupted, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.git.Log = zerolog.New(&choreographyReplayCancelOnObjectRead{
		oid: sealed.OID, cancel: cancel}).Level(zerolog.TraceLevel)
	_, err = replayed.LedgerHead(interrupted)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, stopped.OID, replayed.ledgerTip)
	require.Equal(t, stopped.Sequence, replayed.sequence)
	require.Len(t, replayed.events, int(stopped.Sequence))
	fixture.git.Log = zerolog.Nop()
	head, err := replayed.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, sealed, head)
	require.Len(t, replayed.events, int(sealed.Sequence))
	require.Equal(t, sealed.OID, replayed.eventOIDs[len(replayed.eventOIDs)-choreographyLedgerSingleItemCount])
	_, found := replayed.eventIndex[sealed.OID]
	require.True(t, found)
}
