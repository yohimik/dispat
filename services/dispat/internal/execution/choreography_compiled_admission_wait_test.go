// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestCompiledAdmissionWaitsForPredecessorBeforeExpiringSuccessor(t *testing.T) {
	fixture, persist, _, predecessor := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{
				{Task: "first", Kind: string(KindBuild), Placement: PlacementOrchestrator,
					Platforms: []string{"plan9/mips"}},
				{Task: "second", Kind: string(KindBuild), Placement: PlacementOrchestrator,
					Platforms: []string{"plan9/mips"}, Predecessors: []string{"first"}},
			}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	require.False(t, broker.IsEligibleNodeAdmitted(predecessor.Task))
	require.False(t, broker.IsEligibleNodeAdmitted("second"))
	waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
	coordinator := &Coordinator{GraphRootOID: persist.opts.GraphRootOID,
		choreographyBroker: broker, choreographyPersist: persist,
		canonicalMailbox: fixture.mailbox, choreographyInitialized: true}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type answer struct {
		outcome release.StageOutcome
		err     error
	}
	result := make(chan answer, 1)
	go func() {
		outcome, waitErr := coordinator.waitCompiledTask(ctx, compiledTaskWaitOptions{
			TaskID: "second", Request: release.StageRequest{TaskID: "second", Stage: "build",
				Release: &plan.Release{Pkg: &model.Package{Name: "second"}}}})
		result <- answer{outcome: outcome, err: waitErr}
	}()
	select {
	case got := <-result:
		t.Fatalf("nonready successor returned before predecessor terminal: %v", got.err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), predecessor.Task))
	select {
	case got := <-result:
		require.Error(t, got.err)
		require.NotNil(t, got.outcome.NoClaim)
		require.True(t, got.outcome.NoClaim.SignedTerminal)
		page, readErr := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 8)
		require.NoError(t, readErr)
		var expired []string
		for _, event := range page.Events {
			if event.Kind == ChoreographyNoClaimExpired {
				expired = append(expired, event.Task)
			}
		}
		require.Equal(t, []string{"first", "second"}, expired,
			"the successor expires only after its predecessor has signed finality")
	case <-ctx.Done():
		t.Fatal("ready successor did not expire after predecessor terminal")
	}
}

func TestCompiledAdmissionExpiresReadySuccessorAfterPredecessorResult(t *testing.T) {
	fixture, persist, binding, predecessor := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 3, Tasks: []ChoreographyTaskSpec{
				{Task: "first", Kind: string(KindBuild), Placement: PlacementOrchestrator},
				{Task: "second", Kind: string(KindBuild), Placement: PlacementOrchestrator,
					Platforms: []string{"plan9/mips"}, Predecessors: []string{"first"}},
			}})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: predecessor.Task, Attempt: 1, Node: "node-a", Branch: predecessor.Branch,
		ExpectedOID: predecessor.ReservationOID, InputsDigest: predecessor.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "first-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, acceptedClaim.Accepted)
	waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
	coordinator := &Coordinator{GraphRootOID: persist.opts.GraphRootOID,
		choreographyBroker: broker, choreographyPersist: persist,
		canonicalMailbox: fixture.mailbox, choreographyInitialized: true}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type answer struct {
		outcome release.StageOutcome
		err     error
	}
	result := make(chan answer, 1)
	go func() {
		outcome, waitErr := coordinator.waitCompiledTask(ctx, compiledTaskWaitOptions{
			TaskID: "second", Request: release.StageRequest{TaskID: "second", Stage: "build",
				Release: &plan.Release{Pkg: &model.Package{Name: "second"}}}})
		result <- answer{outcome: outcome, err: waitErr}
	}()
	select {
	case got := <-result:
		t.Fatalf("successor returned while predecessor claim remained live: %v", got.err)
	case <-time.After(100 * time.Millisecond):
	}
	workerResult := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: predecessor.Task, Attempt: 1,
		Node: "node-a", Branch: predecessor.Branch}, Assignment: acceptedClaim.OID,
		Status: StatusSucceeded}
	proposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: claim, Accepted: acceptedClaim,
			Result: workerResult, ProposalID: "first-result"})
	acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: predecessor.Task, Branch: predecessor.Branch,
		Attempt: claim.Attempt, Node: claim.Node, ResultOID: acceptedResult.OID, Succeeded: true}))
	select {
	case got := <-result:
		require.Error(t, got.err)
		require.NotNil(t, got.outcome.NoClaim)
		require.True(t, got.outcome.NoClaim.SignedTerminal)
	case <-ctx.Done():
		t.Fatal("successor did not expire after signed successful predecessor result")
	}
}

func TestCompiledAdmissionKeepsPreparedBlockerDistinctFromSuccessorExpiry(t *testing.T) {
	fixture, persist, binding, predecessor := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 1, Tasks: []ChoreographyTaskSpec{
				{Task: "assets:prepare", Package: "assets", Kind: string(KindPrepare),
					Placement: PlacementAnyNode, Platforms: []string{"plan9/mips"}},
				{Task: "ui:build", Package: "ui", Kind: string(KindBuild),
					Placement: PlacementAnyNode, Platforms: []string{"plan9/mips"},
					Predecessors: []string{"assets:prepare"}, PreparedProviders: []string{"assets"}},
			}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	waitForFixtureAdmissionDeadline(t, broker.admissionDeadlineUTC)
	coordinator := &Coordinator{GraphRootOID: persist.opts.GraphRootOID,
		choreographyBroker: broker, choreographyPersist: persist,
		canonicalMailbox: fixture.mailbox, choreographyInitialized: true}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type answer struct {
		outcome release.StageOutcome
		err     error
	}
	result := make(chan answer, 1)
	go func() {
		outcome, waitErr := coordinator.waitCompiledTask(ctx, compiledTaskWaitOptions{
			TaskID: "ui:build", Request: release.StageRequest{TaskID: "ui:build", Stage: "build",
				Release: &plan.Release{Pkg: &model.Package{Name: "ui"}}}})
		result <- answer{outcome: outcome, err: waitErr}
	}()
	select {
	case got := <-result:
		t.Fatalf("consumer returned before signed prepared-provider failure: %v", got.err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, broker.ExpireUnclaimed(t.Context(), predecessor.Task))
	select {
	case got := <-result:
		require.Error(t, got.err)
		require.NotNil(t, got.outcome.NoClaim)
		require.Equal(t, "assets", got.outcome.NoClaim.Blocker)
	case <-ctx.Done():
		t.Fatal("consumer did not observe signed prepared-provider blocker")
	}
	require.NoError(t, broker.AcceptBlocked(t.Context(), ChoreographyBlockedOutcome{
		ChoreographyRunBinding: binding, Task: "ui:build", Package: "ui", BlockedBy: "assets"}))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 8)
	require.NoError(t, err)
	blocked := false
	for _, event := range page.Events {
		if event.Kind == ChoreographyNoClaimExpired {
			require.Equal(t, predecessor.Task, event.Task,
				"the blocked consumer must not be recorded as independently unclaimed")
		}
		if event.Kind == ChoreographyBlocked && event.Task == "ui:build" {
			blocked = true
			require.Equal(t, "assets", event.BlockedBy)
		}
	}
	require.True(t, blocked, "the prepared blocker receives its own signed decision")
}
