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

type settlementWaitProbe struct {
	*GitChoreographyPersist
	task    string
	waiting chan struct{}
}

func (p *settlementWaitProbe) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	if task == p.task && ctx.Err() == nil {
		select {
		case p.waiting <- struct{}{}:
		default:
		}
	}
	return p.GitChoreographyPersist.WaitTaskEvent(ctx, task, after)
}

func TestCompiledAdmissionWakesAfterPackageSettlementMakesSuccessorReady(t *testing.T) {
	fixture, persist, binding, parent := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: 2, Tasks: []ChoreographyTaskSpec{
				{Task: "parent:build", Package: "parent", Kind: string(KindBuild),
					Placement: PlacementOrchestrator},
				{Task: "child:build", Package: "child", Kind: string(KindBuild),
					Placement: PlacementOrchestrator, Platforms: []string{"plan9/mips"},
					Predecessors: []string{"parent:build"}},
			}})
	child := persist.opts.Tasks[1]
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	probe := &settlementWaitProbe{GitChoreographyPersist: persist,
		task: child.Task, waiting: make(chan struct{}, 1)}
	opts.Persist = probe
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: parent.Task, Attempt: 1, Node: "node-a", Branch: parent.Branch,
		ExpectedOID: parent.ReservationOID, InputsDigest: parent.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "parent-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	acceptedResult := acceptPackageFixtureResult(t, fixture, broker, binding, parent,
		acceptedClaim, claim, "parent-result", StatusSucceeded)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: parent.Task, Branch: parent.Branch,
		Attempt: claim.Attempt, Node: claim.Node, ResultOID: acceptedResult.OID, Succeeded: true}))
	require.True(t, broker.tasks[parent.Task].pendingPackageSettlement)
	require.Equal(t, 1, broker.tasks[child.Task].remaining)
	require.False(t, broker.IsEligibleNodeAdmitted(child.Task))
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
			TaskID: child.Task, Request: release.StageRequest{TaskID: child.Task, Stage: "build",
				Release: &plan.Release{Pkg: &model.Package{Name: child.Package}}}})
		result <- answer{outcome: outcome, err: waitErr}
	}()
	select {
	case <-probe.waiting:
	case got := <-result:
		t.Fatalf("successor returned before signed parent package settlement: %v", got.err)
	case <-time.After(2 * time.Second):
		t.Fatal("successor did not enter its post-deadline indexed event wait")
	}
	select {
	case got := <-result:
		t.Fatalf("successor returned before signed parent package settlement: %v", got.err)
	default:
	}
	require.NoError(t, broker.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{
		TaskID: parent.Task}))
	require.Zero(t, broker.tasks[child.Task].remaining)
	select {
	case got := <-result:
		require.Error(t, got.err)
		require.NotNil(t, got.outcome.NoClaim)
		require.True(t, got.outcome.NoClaim.SignedTerminal)
	case <-time.After(2 * time.Second):
		t.Fatal("ready successor did not wake and expire after signed parent package settlement")
	}
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{OID: persist.opts.Ledger.OID}, 16)
	require.NoError(t, err)
	var isChildExpired bool
	for _, event := range page.Events {
		if event.Task == child.Task && event.Kind == ChoreographyNoClaimExpired {
			isChildExpired = true
			require.Equal(t, child.ReservationOID, event.PriorTaskOID)
			require.Equal(t, string(child.Placement), event.MissingPlacement)
		}
	}
	require.True(t, isChildExpired, "the awakened successor signs its own exact no-claim terminal")
}
