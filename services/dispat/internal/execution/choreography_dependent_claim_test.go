// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestChoreographyDependentClaimUsesAcceptedResultAfterColdReplay(t *testing.T) {
	testChoreographyDependentClaimUsesAcceptedResultAfterColdReplay(t, true)
}

func TestChoreographyDependentClaimUsesFailedResultAfterColdReplay(t *testing.T) {
	testChoreographyDependentClaimUsesAcceptedResultAfterColdReplay(t, false)
}

func testChoreographyDependentClaimUsesAcceptedResultAfterColdReplay(t *testing.T, succeeded bool) {
	fixture, persist, binding, provider := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "provider"},
		ChoreographyTaskSpec{Task: "consumer", Predecessors: []string{"provider"}, ExecutorOwned: true})
	consumer := persist.opts.Tasks[1]
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	providerClaim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: 1, Node: "node-a", Branch: provider.Branch,
		ExpectedOID: provider.ReservationOID, InputsDigest: provider.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "provider-claim"}
	completeChoreographyFixtureClaim(t, persist, &providerClaim)
	providerReceipt, err := broker.AcceptClaim(t.Context(), providerClaim)
	require.NoError(t, err)
	require.True(t, providerReceipt.Accepted)
	status := StatusFailed
	if succeeded {
		status = StatusSucceeded
	}
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: provider.Task, Attempt: 1,
		Node: "node-a", Branch: provider.Branch}, Assignment: providerReceipt.OID,
		Status: status}
	resultProposal, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: providerClaim, Accepted: providerReceipt,
			Result: result, ProposalID: "provider-result"})
	rawResultOID := resultProposal.ResultDigest
	accepted, err := broker.AcceptResult(t.Context(), resultProposal, pack)
	require.NoError(t, err)

	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: provider.Task, Branch: provider.Branch,
		Attempt: providerClaim.Attempt, Node: "node-a", ResultOID: accepted.OID, Succeeded: succeeded}))
	readyDigest, predecessorOIDs, err := broker.ExpectedInputIdentity(consumer.Task)
	require.NoError(t, err)
	require.Equal(t, []string{rawResultOID}, predecessorOIDs)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: binding, LedgerRootOID: persist.opts.Ledger.OID, Reader: restarted,
		Mailbox: fixture.mailbox, MaxDocumentBytes: 1 << 20, Tasks: persist.opts.Tasks,
		OutputContexts: map[string]ChoreographyBindingOutputContext{
			provider.Task: {},
		},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	state, err := cache.State(t.Context(), consumer.Task)
	require.NoError(t, err)
	require.Equal(t, predecessorOIDs, state.PredecessorTaskOIDs)
	cold, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(restarted))
	require.NoError(t, err)
	cold.resolveBindings = func(ctx context.Context, taskID, _ string) (ChoreographyTaskBindings, error) {
		state, err := cache.State(ctx, taskID)
		if err != nil {
			return ChoreographyTaskBindings{}, err
		}
		return ChoreographyTaskBindings{PredecessorResultOIDs: state.PredecessorTaskOIDs}, nil
	}
	coldDigest, coldOIDs, err := cold.ExpectedInputIdentity(consumer.Task)
	require.NoError(t, err)
	require.Equal(t, readyDigest, coldDigest)
	require.Equal(t, predecessorOIDs, coldOIDs)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: consumer.Task, Attempt: 1, Node: "node-a", Branch: consumer.Branch,
		ExpectedOID: consumer.ReservationOID, InputsDigest: coldDigest,
		ProposalID: "consumer-dependent-claim"}
	completeChoreographyFixtureClaim(t, restarted, &claim)
	claim.Bindings.PredecessorResultOIDs = coldOIDs
	claim.BindingsDigest, err = DigestChoreographyTaskBindings(claim.Bindings)
	require.NoError(t, err)
	acceptedConsumer, err := cold.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, acceptedConsumer.Accepted)
	require.NotEmpty(t, acceptedConsumer.AssignmentOID)
}
