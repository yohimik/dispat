// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitForFixtureAdmissionDeadline(t *testing.T, deadlineUTC string) {
	t.Helper()
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, deadlineUTC)
	require.NoError(t, err)
	if wait := time.Until(deadline) + 20*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
}

func TestChoreographyPlacementWithoutCompatibleAdmittedNodeExpiresOnSignedDeadline(t *testing.T) {
	for _, placement := range []Placement{PlacementOrchestrator, PlacementAnyNode} {
		t.Run(string(placement), func(t *testing.T) {
			_, persist, _, spec := newChoreographyLedgerFixtureWithSettings(t,
				choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
					AdmissionSeconds: 1,
					Tasks: []ChoreographyTaskSpec{{Task: "impossible:build", Package: "impossible",
						Kind: string(KindBuild), Placement: placement, Platforms: []string{"plan9/mips"}}}})
			opts := choreographyFixtureBrokerOptions(persist)
			opts.OutcomePolicy = preparedBrokerPolicy{}
			broker, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			require.False(t, broker.IsEligibleNodeAdmitted(spec.Task))
			waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
			require.NoError(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
			require.True(t, broker.IsNoClaimExpired(spec.Task))
			page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 4)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			event := page.Events[0]
			require.Equal(t, ChoreographyNoClaimExpired, event.Kind)
			require.Equal(t, string(placement), event.MissingPlacement)
			require.Equal(t, choreographyNoCompatibleAdmittedNodeCause, event.BlockedBy)
			require.Empty(t, event.OutputOID, "no command Result is fabricated")
			coldPersist, err := NewGitChoreographyPersist(persist.opts)
			require.NoError(t, err)
			opts.Persist = coldPersist
			cold, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			require.True(t, cold.IsNoClaimExpired(spec.Task))
			require.Equal(t, event.TaskOID, cold.tasks[spec.Task].tip)
			require.NoError(t, cold.SettleNoClaimFailure(t.Context(), spec.Task, "failed"))
			settledPersist, err := NewGitChoreographyPersist(persist.opts)
			require.NoError(t, err)
			opts.Persist = settledPersist
			settled, err := RecoverFirstNodeBroker(t.Context(), opts)
			require.NoError(t, err)
			require.False(t, settled.IsNoClaimExpired(spec.Task))
			require.Equal(t, "failed", settled.packageState[spec.Package])
		})
	}
}

func TestChoreographyPreparedPlacementExpiryImmediatelyBlocksConsumers(t *testing.T) {
	_, persist, _, provider := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1,
			Tasks: []ChoreographyTaskSpec{
				{Task: "assets:prepare", Package: "assets", Kind: string(KindPrepare),
					Placement: PlacementAnyNode, Platforms: []string{"plan9/mips"}},
				{Task: "ui:build", Package: "ui", Kind: string(KindBuild),
					PreparedProviders: []string{"assets"}, Predecessors: []string{"assets:prepare"}},
				{Task: "solo:build", Package: "solo", Kind: string(KindBuild),
					Predecessors: []string{"assets:prepare"}},
			}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), provider.Task))
	require.False(t, broker.IsNoClaimExpired(provider.Task), "preparation has no package hook barrier")
	require.Zero(t, broker.tasks["ui:build"].remaining)
	require.Equal(t, "assets", broker.tasks["ui:build"].preparedBlockedBy)
	require.Zero(t, broker.tasks["solo:build"].remaining)
	require.Empty(t, broker.tasks["solo:build"].preparedBlockedBy)
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = coldPersist
	cold, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, "assets", cold.tasks["ui:build"].preparedBlockedBy)
	require.Empty(t, cold.tasks["solo:build"].preparedBlockedBy)
}

func TestChoreographyBusyCompatibleNodeDoesNotExpireAfterAdmissionDeadline(t *testing.T) {
	_, persist, _, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1,
			Tasks: []ChoreographyTaskSpec{{Task: "queued:build", Package: "queued",
				Kind: string(KindBuild), Placement: PlacementAnyNode, Platforms: []string{"linux/amd64"}}}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	broker.nodeUsed["node-a"] = broker.members["node-a"].Capacity
	waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
	require.True(t, broker.IsEligibleNodeAdmitted(spec.Task), "occupied signed capacity remains admitted")
	require.Error(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
	require.False(t, broker.tasks[spec.Task].terminal)
	page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 4)
	require.NoError(t, err)
	require.Empty(t, page.Events)
}
