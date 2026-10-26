// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type noClaimTestPersist struct {
	ChoreographyPersistable
	expired     []ChoreographyNoClaimOutcome
	settled     []string
	settlements map[string]string
}

func (p *noClaimTestPersist) PersistNoClaim(_ context.Context, outcome ChoreographyNoClaimOutcome) (string, error) {
	p.expired = append(p.expired, outcome)
	return "signed-no-claim-oid", nil
}

func (p *noClaimTestPersist) PersistNoClaimPackageSettlement(_ context.Context,
	taskID, taskOID, outcome string) error {
	p.settled = append(p.settled, taskID+":"+taskOID+":"+outcome)
	if p.settlements == nil {
		p.settlements = make(map[string]string)
	}
	p.settlements[taskID+":"+taskOID] = outcome
	return nil
}

func (p *noClaimTestPersist) LookupNoClaimPackageSettlement(_ context.Context,
	taskID, taskOID string) (string, bool, error) {
	outcome, found := p.settlements[taskID+":"+taskOID]
	return outcome, found, nil
}

type noClaimTestPolicy struct{ outcomes map[string]string }

func (p *noClaimTestPolicy) ValidateOutcome(_ string, status string) error {
	if status != "failed" {
		return context.Canceled
	}
	return nil
}
func (p *noClaimTestPolicy) ApplyOutcome(pkg, status string) error {
	p.outcomes[pkg] = status
	return nil
}
func (p *noClaimTestPolicy) ShouldSkip(string) (bool, string, error) { return false, "", nil }

func TestChoreographyNoClaimWaitsForPackageSettlement(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "plan", GraphDigest: "graph"}
	missing := ChoreographyTaskSpec{Task: "core:test", Package: "core", Kind: KindBuild,
		Branch: FormatChoreographyTaskBranch(binding.Run, "core:test"), ReservationOID: "inert-root",
		Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementWorker}
	sibling := ChoreographyTaskSpec{Task: "core:build", Package: "core", Kind: KindBuild,
		Branch: FormatChoreographyTaskBranch(binding.Run, "core:build"), ReservationOID: "sibling-root",
		Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementOrchestrator}
	child := ChoreographyTaskSpec{Task: "consumer:test", Package: "consumer", Kind: KindBuild,
		Branch: FormatChoreographyTaskBranch(binding.Run, "consumer:test"), ReservationOID: "child-root",
		Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementWorker,
		Predecessors: []string{missing.Task}}
	persist := &noClaimTestPersist{}
	policy := &noClaimTestPolicy{outcomes: map[string]string{}}
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding,
		GraphRootOID: "complete-root", InitialMembershipEpochOID: "complete-event",
		FirstNode: "first", Tasks: []ChoreographyTaskSpec{missing, sibling, child},
		Members:      []ChoreographyRuntimeNode{{Name: "first", Capacity: 1, Platform: "linux/amd64", First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"first": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 2},
		JoinPolicy:    ChoreographyJoinPolicy{AdmissionDurationSeconds: 60},
		OutcomePolicy: policy, Persist: persist})
	require.NoError(t, err)
	broker.executionStarted = true
	broker.admissionDeadlineUTC = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	// A parallel same-package sibling is still active when admission expires.
	broker.tasks[sibling.Task].claim = "accepted-sibling-claim"
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), missing.Task))
	require.Len(t, persist.expired, 1)
	require.True(t, broker.IsNoClaimExpired(missing.Task))
	require.Equal(t, 1, broker.tasks[child.Task].remaining)
	require.Empty(t, policy.outcomes[missing.Package])
	require.Error(t, broker.SettleNoClaimFailure(t.Context(), missing.Task, ""),
		"a noncausal settlement needs a prior signed failed package")
	// The executor's sibling/onFail barrier is the only caller of settlement.
	broker.tasks[sibling.Task].claim = ""
	broker.tasks[sibling.Task].terminal = true
	require.NoError(t, broker.SettleNoClaimFailure(t.Context(), missing.Task, "failed"))
	require.Equal(t, []string{missing.Task + ":signed-no-claim-oid:failed"}, persist.settled)
	require.Equal(t, "failed", policy.outcomes[missing.Package])
	require.Zero(t, broker.tasks[child.Task].remaining)
	require.False(t, broker.IsNoClaimExpired(missing.Task))
	require.NoError(t, broker.SettleNoClaimFailure(t.Context(), missing.Task, "failed"))
	require.Len(t, persist.settled, 1, "an exact lost-response retry cannot append another settlement")
	require.Error(t, broker.SettleNoClaimFailure(t.Context(), missing.Task, "cancelled"))
}
