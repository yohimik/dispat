// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	inertPendingFirstTaskName      = "inert-pending-first"
	inertPendingSecondTaskName     = "inert-pending-second"
	inertPendingEventPageLimit     = 16
	inertPendingExpectedEventCount = 1
	inertPendingProofReadCount     = 2
	inertPendingFirstEventIndex    = 0
	inertPendingDirectReadName     = "direct-confirmation"
	inertPendingHeadReadName       = "ledger-head"
	inertPendingEventsReadName     = "event-page"
	inertPendingAlwaysUnknown      = true
)

func inertPendingOutcome(persist *GitChoreographyPersist, spec ChoreographyTaskSpec) ChoreographyInertCancellation {
	return ChoreographyInertCancellation{ChoreographyRunBinding: persist.opts.Binding,
		GraphRootOID: persist.opts.GraphRootOID, Task: spec.Task,
		Attempt: choreographyInertCancelMinimumAttempt, Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, Package: spec.Package,
		Cause: choreographyInertCancelCause}
}

func TestChoreographyInertPendingAppliedCASReconcilesAfterAuthenticatedRead(t *testing.T) {
	for _, read := range []string{inertPendingDirectReadName, inertPendingHeadReadName, inertPendingEventsReadName} {
		t.Run(read, func(t *testing.T) {
			fixture, persist, _, spec := newChoreographyLedgerFixture(t,
				ChoreographyTaskSpec{Task: inertPendingFirstTaskName})
			broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
			require.NoError(t, err)
			_, err = broker.StopClaims(t.Context())
			require.NoError(t, err)
			before, err := persist.LedgerHead(t.Context())
			require.NoError(t, err)
			beforeEvents := len(persist.events)
			advances := make(map[string][]ChoreographyRefAdvance)
			persist.opts.OnRefAdvance = func(_ context.Context, advance ChoreographyRefAdvance) {
				advances[advance.Branch] = append(advances[advance.Branch], advance)
			}
			outcome := inertPendingOutcome(persist, spec)
			transport := &frameReadyReadbackTransport{transportx: fixture.mailbox.remote}
			fixture.mailbox.remote = transport
			_, err = persist.PersistInertCancellation(t.Context(), outcome)
			require.ErrorIs(t, err, errChoreographyAtomicUnresolved)
			require.ErrorContains(t, err, frameReadyReadbackError)
			require.True(t, transport.pushed)
			require.NotNil(t, persist.pending)
			pending := *persist.pending
			require.Equal(t, before.OID, pending.oldLedger)
			require.Equal(t, before.OID, pending.mutation.ExpectedLedgerOID)
			fixture.mailbox.remote = transport.transportx
			require.Equal(t, pending.newLedger, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch),
				"the real remote accepted the signed ledger before its readback failed")
			require.Equal(t, pending.mutation.NewTaskOID, remoteChoreographyOID(t, fixture, spec.Branch),
				"the same atomic push accepted the exact signed task terminal")
			if read == inertPendingHeadReadName {
				_, err = persist.LedgerHead(t.Context())
			} else if read == inertPendingEventsReadName {
				_, err = persist.ReadEvents(t.Context(), before, inertPendingEventPageLimit)
			}
			if read != inertPendingDirectReadName {
				require.NoError(t, err)
				require.Equal(t, beforeEvents+inertPendingExpectedEventCount, len(persist.events))
			} else {
				require.Equal(t, beforeEvents, len(persist.events))
			}
			require.Equal(t, pending, *persist.pending, "an authenticated read cannot rebase the pending mutation")
			require.Empty(t, advances, "indexing the signed event is not remote-mutation confirmation")
			mismatch := outcome
			mismatch.Attempt++
			_, err = persist.PersistInertCancellation(t.Context(), mismatch)
			require.ErrorContains(t, err, choreographyLedgerPendingCASErr)
			require.Equal(t, pending, *persist.pending)
			require.Empty(t, advances)
			oid, err := persist.PersistInertCancellation(t.Context(), outcome)
			require.NoError(t, err)
			require.Equal(t, pending.mutation.NewTaskOID, oid)
			require.Nil(t, persist.pending)
			require.Equal(t, []ChoreographyRefAdvance{{Branch: spec.Branch,
				OldOID: spec.ReservationOID, NewOID: oid}}, advances[spec.Branch])
			require.Equal(t, []ChoreographyRefAdvance{{Branch: persist.opts.Ledger.Branch,
				OldOID: before.OID, NewOID: pending.newLedger}}, advances[persist.opts.Ledger.Branch],
				"first exact confirmation notifies cleanup tracking once even after prior indexing")
			require.Equal(t, beforeEvents+inertPendingExpectedEventCount, len(persist.events),
				"reconciling an already-indexed signed mutation must not append it twice")
			head, err := persist.LedgerHead(t.Context())
			require.NoError(t, err)
			require.Equal(t, pending.newLedger, head.OID)
			require.Equal(t, pending.mutation.Event.Sequence, head.Sequence)
			page, err := persist.ReadEvents(t.Context(), before, inertPendingEventPageLimit)
			require.NoError(t, err)
			require.Len(t, page.Events, inertPendingExpectedEventCount)
			require.Equal(t, ChoreographyInertCancelled, page.Events[inertPendingFirstEventIndex].Kind)
			cold, err := NewGitChoreographyPersist(persist.opts)
			require.NoError(t, err)
			coldPage, err := cold.ReadEvents(t.Context(), before, inertPendingEventPageLimit)
			require.NoError(t, err)
			require.Equal(t, coldPage, page, "hot reconciliation and cold authenticated replay agree exactly")
			sealed, err := persist.SealRun(t.Context(), choreographyCloseAbortedSeal)
			require.NoError(t, err, "the next legal transition must use the exact reconciled sequence")
			require.Equal(t, head.Sequence+inertPendingExpectedEventCount, sealed.Sequence)
			require.NotEqual(t, head.OID, sealed.OID)
			following, err := persist.ReadEvents(t.Context(), head, inertPendingEventPageLimit)
			require.NoError(t, err)
			require.Len(t, following.Events, inertPendingExpectedEventCount)
			require.Equal(t, ChoreographyRunSealed, following.Events[inertPendingFirstEventIndex].Kind)
			coldFollowing, err := cold.ReadEvents(t.Context(), head, inertPendingEventPageLimit)
			require.NoError(t, err)
			require.Equal(t, following, coldFollowing)
			require.Equal(t, oid, remoteChoreographyOID(t, fixture, spec.Branch))
			require.Len(t, advances[spec.Branch], inertPendingExpectedEventCount)
		})
	}
}

func TestChoreographyInertPendingUnknownOutcomeRemainsFenced(t *testing.T) {
	fixture, persist, _, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: inertPendingFirstTaskName})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	before, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	beforeEvents := len(persist.events)
	var advances []ChoreographyRefAdvance
	persist.opts.OnRefAdvance = func(_ context.Context, advance ChoreographyRefAdvance) {
		advances = append(advances, advance)
	}
	transport := &choreographyAtomicUnknownTransport{transportx: fixture.mailbox.remote,
		always: inertPendingAlwaysUnknown}
	fixture.mailbox.remote = transport
	outcome := inertPendingOutcome(persist, spec)
	_, err = persist.PersistInertCancellation(t.Context(), outcome)
	require.ErrorIs(t, err, gitx.ErrPushUnknown)
	require.NotNil(t, persist.pending)
	pending := *persist.pending
	head, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, head)
	_, err = persist.PersistInertCancellation(t.Context(), outcome)
	require.ErrorIs(t, err, gitx.ErrPushUnknown)
	require.Equal(t, pending, *persist.pending, "neither local presence nor unchanged remote refs proves acceptance")
	mismatch := outcome
	mismatch.Attempt++
	_, err = persist.PersistInertCancellation(t.Context(), mismatch)
	require.ErrorContains(t, err, choreographyLedgerPendingCASErr)
	require.Equal(t, pending, *persist.pending)
	require.Equal(t, beforeEvents, len(persist.events))
	require.Empty(t, advances)
	fixture.mailbox.remote = transport.transportx
	require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, before.OID, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
	page, err := persist.ReadEvents(t.Context(), before, inertPendingEventPageLimit)
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.NotNil(t, persist.pending)
}

type inertPendingAdvanceTransport struct {
	transportx
	ledgerBranch string
	ledgerReads  int
	advance      func(context.Context) error
}

func (t *inertPendingAdvanceTransport) ListRemoteHeads(ctx context.Context,
	endpoint, pattern string) ([]gitx.RemoteHead, error) {
	if strings.HasSuffix(pattern, t.ledgerBranch) {
		t.ledgerReads++
		if t.ledgerReads == inertPendingProofReadCount {
			if err := t.advance(ctx); err != nil {
				return nil, err
			}
		}
	}
	return t.transportx.ListRemoteHeads(ctx, endpoint, pattern)
}

func TestChoreographyInertCancellationRefusesLedgerAdvanceAfterPrefixProof(t *testing.T) {
	fixture, persist, _, first := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: inertPendingFirstTaskName},
		ChoreographyTaskSpec{Task: inertPendingSecondTaskName})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	other, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	second := persist.tasks[inertPendingSecondTaskName]
	secondOutcome := inertPendingOutcome(persist, second)
	transport := &inertPendingAdvanceTransport{transportx: fixture.mailbox.remote,
		ledgerBranch: persist.opts.Ledger.Branch,
		advance: func(ctx context.Context) error {
			_, advanceErr := other.PersistInertCancellation(ctx, secondOutcome)
			return advanceErr
		}}
	fixture.mailbox.remote = transport
	_, err = persist.PersistInertCancellation(t.Context(), inertPendingOutcome(persist, first))
	require.ErrorContains(t, err, choreographyLedgerAdvancedErr)
	require.Nil(t, persist.pending, "a changed proof epoch cannot mint a replacement pending mutation")
	fixture.mailbox.remote = transport.transportx
	require.Equal(t, first.ReservationOID, remoteChoreographyOID(t, fixture, first.Branch))
	require.NotEqual(t, second.ReservationOID, remoteChoreographyOID(t, fixture, second.Branch))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, inertPendingEventPageLimit)
	require.NoError(t, err)
	for _, event := range page.Events {
		require.False(t, event.Task == first.Task && event.Kind == ChoreographyInertCancelled,
			"the stale proof did not sign the first task's cancellation")
	}
}
