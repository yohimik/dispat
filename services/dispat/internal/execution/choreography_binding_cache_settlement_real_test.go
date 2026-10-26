// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestSignedBindingCacheWaitsForRealGitPackageSettlementAfterColdReplay(t *testing.T) {
	fixture, persist, binding, provider := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "provider:build", Package: "provider", Kind: string(KindBuild)},
		ChoreographyTaskSpec{Task: "consumer:build", Package: "consumer", Kind: string(KindBuild),
			Predecessors: []string{"provider:build"}})
	consumer := persist.opts.Tasks[1]
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: 1, Node: "node-a", Branch: provider.Branch,
		ExpectedOID: provider.ReservationOID, InputsDigest: provider.InputsDigest,
		ProposalID: "provider-settlement-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	claimed, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	accepted := acceptPackageFixtureResult(t, fixture, broker, binding, provider,
		claimed, claim.InputsDigest, "provider-settlement-result", StatusSucceeded)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: provider.Task, Branch: provider.Branch,
		Attempt: 1, Node: "node-a", ResultOID: accepted.OID, Succeeded: true}))
	cacheOptions := ChoreographyBindingCacheOptions{Binding: binding,
		LedgerRootOID: persist.opts.Ledger.OID, Reader: persist,
		Mailbox: fixture.mailbox, MaxDocumentBytes: 1 << 20, Tasks: persist.opts.Tasks,
		OutputContexts: map[string]ChoreographyBindingOutputContext{provider.Task: {}},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	}
	cache, err := NewChoreographyBindingCache(cacheOptions)
	require.NoError(t, err)
	_, err = cache.State(t.Context(), consumer.Task)
	require.ErrorContains(t, err, "no signed logical finality",
		"the immediate signed task terminal does not discharge package inputs")
	require.Empty(t, cache.providers)
	require.NoError(t, broker.SettleTaskPackage(t.Context(), provider.Task, ""))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{OID: persist.opts.Ledger.OID}, 16)
	require.NoError(t, err)
	var rawResultOID string
	var settled bool
	for _, event := range page.Events {
		if event.Task != provider.Task {
			continue
		}
		if event.Kind == ChoreographyTerminal {
			rawResultOID = event.OutputOID
		}
		if event.Kind == ChoreographyTaskPackageSettled {
			settled = true
			require.Equal(t, cache.terminal[provider.Task].TaskOID, event.TaskOID)
		}
	}
	require.True(t, settled)
	require.NotEmpty(t, rawResultOID)
	state, err := cache.State(t.Context(), consumer.Task)
	require.NoError(t, err)
	require.Equal(t, []string{rawResultOID}, state.PredecessorTaskOIDs)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	cacheOptions.Reader = restarted
	cold, err := NewChoreographyBindingCache(cacheOptions)
	require.NoError(t, err)
	replayed, err := cold.State(t.Context(), consumer.Task)
	require.NoError(t, err)
	require.Equal(t, state, replayed, "verified signed feed retains the package outcome gate after restart")
}
