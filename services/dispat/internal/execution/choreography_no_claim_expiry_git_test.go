// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A settlement can be durably accepted even when its response is lost. The
// first node must then reconcile that exact event without adding another
// package outcome or discharging the consumer edge twice.
func TestChoreographyNoClaimPackageSettlementReconcilesLostReplyAndColdReplay(t *testing.T) {
	fixture, persist, _, expired := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{
			AuthorityID:      "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1,
			Tasks: []ChoreographyTaskSpec{
				{Task: "core:worker", Package: "core", Placement: PlacementWorker},
				{Task: "consumer:build", Package: "consumer", Placement: PlacementOrchestrator,
					Predecessors: []string{"core:worker"}},
			},
		})
	consumer := persist.opts.Tasks[1]
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.False(t, broker.IsEligibleNodeAdmitted(expired.Task))
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, broker.admissionDeadlineUTC)
	require.NoError(t, err)
	if remaining := time.Until(deadline); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), expired.Task))
	noClaimOID := broker.tasks[expired.Task].tip
	require.Equal(t, noClaimOID, remoteChoreographyOID(t, fixture, expired.Branch))
	require.True(t, broker.IsNoClaimExpired(expired.Task))
	require.Equal(t, 1, broker.tasks[consumer.Task].remaining)
	require.Empty(t, broker.packageState[expired.Package])

	// This exact Git CAS lands, but the broker does not receive its response.
	require.NoError(t, persist.PersistNoClaimPackageSettlement(t.Context(),
		expired.Task, noClaimOID, "failed"))
	settledHead, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.True(t, broker.IsNoClaimExpired(expired.Task))
	require.NoError(t, broker.SettleNoClaimFailure(t.Context(), expired.Task, "failed"))
	require.False(t, broker.IsNoClaimExpired(expired.Task))
	require.Equal(t, "failed", broker.packageState[expired.Package])
	require.Zero(t, broker.tasks[consumer.Task].remaining)
	require.NoError(t, broker.SettleNoClaimFailure(t.Context(), expired.Task, "failed"))
	require.ErrorContains(t, broker.SettleNoClaimFailure(t.Context(), expired.Task, "cancelled"),
		choreographyNoClaimSettlementOutcomeMismatchError)
	headAfterRetry, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, settledHead, headAfterRetry)
	require.Equal(t, noClaimOID, remoteChoreographyOID(t, fixture, expired.Branch))

	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	cold, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.False(t, cold.IsNoClaimExpired(expired.Task))
	require.Equal(t, "failed", cold.packageState[expired.Package])
	require.Zero(t, cold.tasks[consumer.Task].remaining)
	require.NoError(t, cold.SettleNoClaimFailure(t.Context(), expired.Task, "failed"))
	require.ErrorContains(t, cold.SettleNoClaimFailure(t.Context(), expired.Task, "cancelled"),
		choreographyNoClaimSettlementOutcomeMismatchError)

	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, ChoreographyNoClaimExpired, page.Events[0].Kind)
	require.Empty(t, page.Events[0].OutputOID)
	require.Empty(t, page.Events[0].PackageOutcome)
	require.Equal(t, ChoreographyNoClaimPackageSettled, page.Events[1].Kind)
	require.Equal(t, noClaimOID, page.Events[1].TaskOID)
	require.Equal(t, "failed", page.Events[1].PackageOutcome)
}
