// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func acceptPackageFixtureResult(t *testing.T, fixture *mailboxFixture,
	broker *FirstNodeBroker, binding ChoreographyRunBinding, spec ChoreographyTaskSpec,
	claim ChoreographyReceipt, claimProposal ChoreographyClaimProposal, proposalID, status string) ChoreographyReceipt {
	t.Helper()
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch}, Assignment: claim.OID, Status: status}
	proposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: claimProposal, Accepted: claim,
			Result: result, ProposalID: proposalID})
	accepted, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	return accepted
}

func TestChoreographyCapacityOneParallelFailureDefersPackageEdgesAfterSignedTaskFinality(t *testing.T) {
	fixture, persist, binding, first := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "core:test", Package: "core"},
		ChoreographyTaskSpec{Task: "core:lint", Package: "core"},
		ChoreographyTaskSpec{Task: "consumer:build", Package: "consumer",
			Predecessors: []string{"core:test"}})
	second, consumer := persist.opts.Tasks[1], persist.opts.Tasks[2]
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	firstClaim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: first.Task, Attempt: 1, Node: "node-a", Branch: first.Branch,
		ExpectedOID: first.ReservationOID, InputsDigest: first.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "first-claim"}
	completeChoreographyFixtureClaim(t, persist, &firstClaim)
	acceptedFirst, err := broker.AcceptClaim(t.Context(), firstClaim)
	require.NoError(t, err)
	acceptedResult := acceptPackageFixtureResult(t, fixture, broker, binding, first,
		acceptedFirst, firstClaim, "first-result", StatusFailed)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: first.Task, Branch: first.Branch,
		Attempt: 1, Node: "node-a", ResultOID: acceptedResult.OID,
		Succeeded: false}))
	require.Zero(t, broker.nodeUsed["node-a"], "signed task finality releases physical capacity")
	require.True(t, broker.tasks[first.Task].pendingPackageSettlement)
	require.Equal(t, 1, broker.tasks[consumer.Task].remaining,
		"package outcome gate remains held before sibling and hooks settle")
	require.Empty(t, broker.packageState[first.Package])
	secondClaim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: second.Task, Attempt: 1, Node: "node-a", Branch: second.Branch,
		ExpectedOID: second.ReservationOID, InputsDigest: second.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "second-claim"}
	completeChoreographyFixtureClaim(t, persist, &secondClaim)
	acceptedSecond, err := broker.AcceptClaim(t.Context(), secondClaim)
	require.NoError(t, err, "the sibling can use the one physical slot before package settlement")
	require.True(t, acceptedSecond.Accepted)
	secondResult := acceptPackageFixtureResult(t, fixture, broker, binding, second,
		acceptedSecond, secondClaim, "second-result", StatusFailed)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: second.Task, Branch: second.Branch,
		Attempt: 1, Node: "node-a", ResultOID: secondResult.OID,
		Succeeded: false}))
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	cold, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Zero(t, cold.nodeUsed["node-a"])
	require.True(t, cold.tasks[first.Task].pendingPackageSettlement)
	require.True(t, cold.tasks[second.Task].pendingPackageSettlement)
	require.Equal(t, 1, cold.tasks[consumer.Task].remaining)
	require.Empty(t, cold.packageState[first.Package])
	_, err = cold.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = restarted.SealRun(t.Context(), false)
	require.ErrorContains(t, err, choreographyLedgerUnsettledTaskPackageSealErr)
	require.ErrorContains(t, cold.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{TaskID: second.Task}),
		choreographyTaskPackageSettlementCausalOrderError,
		"a noncausal failed sibling cannot discharge edges before signed package failure")
	require.NoError(t, cold.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{TaskID: first.Task, PackageOutcome: "failed"}))
	require.NoError(t, cold.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{TaskID: second.Task}))
	require.Equal(t, "failed", cold.packageState[first.Package])
	require.Zero(t, cold.tasks[consumer.Task].remaining)
	require.False(t, cold.tasks[first.Task].pendingPackageSettlement)
	require.NoError(t, cold.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{TaskID: first.Task, PackageOutcome: "failed"}),
		"the exact signed package settlement is idempotent after a lost response")
	require.Error(t, cold.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{TaskID: first.Task, PackageOutcome: "succeeded"}),
		"a different outcome cannot reuse the signed settlement")
	last, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = last
	verified, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, "failed", verified.packageState[first.Package])
	require.Zero(t, verified.tasks[consumer.Task].remaining)
	_, err = last.SealRun(t.Context(), true)
	require.NoError(t, err)
	leases, err := last.CanonicalTaskLeases(t.Context())
	require.NoError(t, err)
	require.Len(t, leases, 3)
}

func TestChoreographyPreparedOutputRejectionBlocksOnlyDeclaredConsumersAfterColdReplay(t *testing.T) {
	fixture, persist, binding, provider := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "assets:prepare", Package: "assets", Kind: string(KindPrepare)},
		ChoreographyTaskSpec{Task: "ui:build", Package: "ui", Kind: string(KindBuild),
			PreparedProviders: []string{"assets"}, Predecessors: []string{"assets:prepare"}},
		ChoreographyTaskSpec{Task: "solo:build", Package: "solo", Kind: string(KindBuild),
			Predecessors: []string{"assets:prepare"}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: 1, Node: "node-a", Branch: provider.Branch,
		ExpectedOID: provider.ReservationOID, InputsDigest: provider.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "prepared-admission-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	acceptedResult := acceptPackageFixtureResult(t, fixture, broker, binding, provider,
		acceptedClaim, claim, "prepared-admission-result", StatusSucceeded)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: provider.Task, Branch: provider.Branch,
		Attempt: 1, Node: "node-a", ResultOID: acceptedResult.OID, Succeeded: true}))
	require.True(t, broker.tasks[provider.Task].pendingPackageSettlement)
	require.Equal(t, 1, broker.tasks["ui:build"].remaining)
	require.Equal(t, 1, broker.tasks["solo:build"].remaining)
	require.NoError(t, broker.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{
		TaskID: provider.Task, OutputAdmissionRejected: true}))
	require.Zero(t, broker.tasks["ui:build"].remaining)
	require.Equal(t, "assets", broker.tasks["ui:build"].preparedBlockedBy)
	require.Zero(t, broker.tasks["solo:build"].remaining)
	require.Empty(t, broker.tasks["solo:build"].preparedBlockedBy)
	require.NoError(t, broker.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{
		TaskID: provider.Task, OutputAdmissionRejected: true}), "exact lost-response retry is idempotent")
	require.Error(t, broker.SettleTaskPackage(t.Context(), ChoreographyTaskPackageSettlement{
		TaskID: provider.Task}), "changed admission disposition cannot reuse the signed settlement")
	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = coldPersist
	cold, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, "assets", cold.tasks["ui:build"].preparedBlockedBy)
	require.Empty(t, cold.tasks["solo:build"].preparedBlockedBy)
	require.False(t, cold.tasks[provider.Task].pendingPackageSettlement)
}
