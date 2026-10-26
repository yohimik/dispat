// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func acceptPackageFixtureResult(t *testing.T, fixture *mailboxFixture,
	broker *FirstNodeBroker, binding ChoreographyRunBinding, spec ChoreographyTaskSpec,
	claim ChoreographyReceipt, inputsDigest, proposalID, status string) ChoreographyReceipt {
	t.Helper()
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch}, Assignment: claim.OID, Status: status}
	document, err := json.Marshal(result)
	require.NoError(t, err)
	rawOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, document, nil, nil)
	require.NoError(t, err)
	pack, err := os.CreateTemp(t.TempDir(), "package-result-*.pack")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), rawOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ClaimOID: claim.OID, InputsDigest: inputsDigest,
		ProposalID: proposalID, ResultDigest: rawOID}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack, 1<<20)
	require.NoError(t, err)
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
		ProposalID: "first-claim"}
	completeChoreographyFixtureClaim(t, persist, &firstClaim)
	acceptedFirst, err := broker.AcceptClaim(t.Context(), firstClaim)
	require.NoError(t, err)
	acceptedResult := acceptPackageFixtureResult(t, fixture, broker, binding, first,
		acceptedFirst, firstClaim.InputsDigest, "first-result", StatusFailed)
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
		ProposalID: "second-claim"}
	completeChoreographyFixtureClaim(t, persist, &secondClaim)
	acceptedSecond, err := broker.AcceptClaim(t.Context(), secondClaim)
	require.NoError(t, err, "the sibling can use the one physical slot before package settlement")
	require.True(t, acceptedSecond.Accepted)
	secondResult := acceptPackageFixtureResult(t, fixture, broker, binding, second,
		acceptedSecond, secondClaim.InputsDigest, "second-result", StatusFailed)
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
	require.ErrorContains(t, cold.SettleTaskPackage(t.Context(), second.Task, ""),
		choreographyTaskPackageSettlementCausalOrderError,
		"a noncausal failed sibling cannot discharge edges before signed package failure")
	require.NoError(t, cold.SettleTaskPackage(t.Context(), first.Task, "failed"))
	require.NoError(t, cold.SettleTaskPackage(t.Context(), second.Task, ""))
	require.Equal(t, "failed", cold.packageState[first.Package])
	require.Zero(t, cold.tasks[consumer.Task].remaining)
	require.False(t, cold.tasks[first.Task].pendingPackageSettlement)
	require.NoError(t, cold.SettleTaskPackage(t.Context(), first.Task, "failed"),
		"the exact signed package settlement is idempotent after a lost response")
	require.Error(t, cold.SettleTaskPackage(t.Context(), first.Task, "succeeded"),
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
