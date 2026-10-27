// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNoClaimRefusalNamesTheRejectedBrokerInvariant(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		reason string
		alter  func(*FirstNodeBroker, *indexedChoreographyTask)
	}{
		{name: "deadline", reason: choreographyNoClaimDeadlineReasonFormat,
			alter: func(b *FirstNodeBroker, _ *indexedChoreographyTask) {
				b.admissionDeadlineUTC = time.Now().Add(time.Minute).UTC().Format(choreographyAdmissionTimeFormat)
			}},
		{name: "predecessor", reason: "1 predecessor edges remain",
			alter: func(_ *FirstNodeBroker, task *indexedChoreographyTask) { task.remaining = 1 }},
		{name: "terminal", reason: choreographyNoClaimTerminalReason,
			alter: func(_ *FirstNodeBroker, task *indexedChoreographyTask) { task.terminal = true }},
		{name: "claim", reason: choreographyNoClaimClaimedReason,
			alter: func(_ *FirstNodeBroker, task *indexedChoreographyTask) { task.claim = "accepted-claim" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
			spec := ChoreographyTaskSpec{Task: "alpha:build", Package: "alpha", Kind: KindBuild,
				Branch: FormatChoreographyTaskBranch(binding.Run, "alpha:build"), ReservationOID: "inert-root",
				Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementWorker}
			persist := &noClaimTestPersist{}
			broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding,
				GraphRootOID: "complete-root", InitialMembershipEpochOID: "complete-event",
				FirstNode: "first", Tasks: []ChoreographyTaskSpec{spec},
				Members: []ChoreographyRuntimeNode{{Name: "first", Capacity: 1, Platform: "linux/arm64",
					First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}},
				NodeCapacity: map[string]int{"first": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 1},
				JoinPolicy:    ChoreographyJoinPolicy{AdmissionDurationSeconds: 60},
				OutcomePolicy: &noClaimTestPolicy{outcomes: map[string]string{}}, Persist: persist})
			require.NoError(t, err)
			broker.executionStarted = true
			broker.admissionDeadlineUTC = time.Now().Add(-time.Minute).UTC().Format(choreographyAdmissionTimeFormat)
			scenario.alter(broker, broker.tasks[spec.Task])
			err = broker.ExpireUnclaimed(t.Context(), spec.Task)
			require.ErrorContains(t, err, choreographyNoClaimStateError)
			require.ErrorContains(t, err, choreographyNoClaimBrokerPhase)
			require.ErrorContains(t, err, spec.Task)
			if scenario.name == "deadline" {
				require.ErrorContains(t, err, "signed admission deadline has not elapsed")
			} else {
				require.ErrorContains(t, err, scenario.reason)
			}
			require.Empty(t, persist.expired, "refusal must not mutate the canonical task")
			if scenario.name == "deadline" {
				broker.admissionDeadlineUTC = time.Now().Add(-time.Minute).UTC().Format(choreographyAdmissionTimeFormat)
				require.NoError(t, broker.ExpireUnclaimed(t.Context(), spec.Task))
				require.Len(t, persist.expired, 1, "the same inert task expires after the signed deadline")
			}
		})
	}
}

func TestNoClaimRefusalSeparatesPersistenceAndVerification(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{{Task: "alpha:build", Package: "alpha",
				Kind: string(KindBuild), Placement: PlacementWorker}}})
	outcome := ChoreographyNoClaimOutcome{ChoreographyRunBinding: binding,
		GraphRootOID: persist.opts.GraphRootOID, Task: spec.Task, Attempt: choreographyNoClaimFirstAttempt,
		Branch: spec.Branch, ExpectedOID: "another-tip", Package: spec.Package,
		MissingPlacement: string(spec.Placement), AdmissionDeadlineUTC: persist.admissionDeadlineUTC}
	_, err := persist.PersistNoClaim(t.Context(), outcome)
	require.ErrorContains(t, err, choreographyNoClaimStateError)
	require.ErrorContains(t, err, choreographyNoClaimPersistPhase)
	require.ErrorContains(t, err, choreographyNoClaimExpectedReason)

	err = persist.verifyNoClaimEvent(t.Context(), ChoreographyEvent{
		Kind: ChoreographyNoClaimExpired, Task: spec.Task, GraphRootOID: "another-root"})
	require.ErrorContains(t, err, choreographyNoClaimStateError)
	require.ErrorContains(t, err, choreographyNoClaimVerifyPhase)
	require.ErrorContains(t, err, choreographyNoClaimRootReason)
}
