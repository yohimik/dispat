// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	choreographyPreparedFailureProviderTask = "assets:prepare"
	choreographyPreparedFailureConsumerTask = "ui:build"
	choreographyPreparedFailurePackage      = "assets"
	choreographyPreparedFailureNode         = "node-a"
	choreographyPreparedFailureClaimID      = "prepared-failure-claim"
	choreographyPreparedFailureResultID     = "prepared-failure-result"
	choreographyPreparedFailurePackPattern  = "prepared-failure-*.pack"
	choreographyPreparedFailurePackBytes    = 1 << 20
	choreographyPreparedFailureInputDigest  = "prepared-provider-inputs"
)

type preparedBrokerPolicy struct{}

func (preparedBrokerPolicy) ValidateOutcome(string, string) error { return nil }
func (preparedBrokerPolicy) ApplyOutcome(string, string) error    { return nil }
func (preparedBrokerPolicy) ShouldSkip(string) (bool, string, error) {
	return false, "", nil
}

func TestBrokerPreparedFailureBlockerIsArrivalIndependent(t *testing.T) {
	for _, order := range [][]string{{"alpha", "zeta"}, {"zeta", "alpha"}} {
		specs := []ChoreographyTaskSpec{
			{Task: "alpha", Package: "alpha", Kind: string(KindPrepare),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"}},
			{Task: "zeta", Package: "zeta", Kind: string(KindPrepare),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"}},
			{Task: "relay", Package: "app", Virtual: true,
				PreparedProviders: []string{"alpha", "zeta"}, Predecessors: []string{"alpha", "zeta"}},
			{Task: "consumer", Package: "app", Kind: string(KindBuild),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"},
				PreparedProviders: []string{"alpha", "zeta"}, Predecessors: []string{"relay"}},
			{Task: "solo", Package: "solo", Kind: string(KindBuild),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"},
				Predecessors: []string{"relay"}},
		}
		_, persist, _, _ := newChoreographyLedgerFixture(t, specs...)
		opts := choreographyFixtureBrokerOptions(persist)
		opts.OutcomePolicy = preparedBrokerPolicy{}
		broker, err := RecoverFirstNodeBroker(t.Context(), opts)
		require.NoError(t, err)
		for _, name := range order {
			parent := broker.tasks[name]
			parent.terminal = true
			broker.discharge(parent)
		}
		require.True(t, broker.tasks["relay"].terminal)
		require.Equal(t, "alpha", broker.tasks["consumer"].preparedBlockedBy)
		require.Empty(t, broker.tasks["solo"].preparedBlockedBy)
		require.Zero(t, broker.tasks["solo"].remaining)
	}
}

func TestChoreographyFailedPrepareDischargesConsumerWithoutPackageSettlement(t *testing.T) {
	fixture, persist, binding, provider := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: choreographyPreparedFailureProviderTask,
			Package: choreographyPreparedFailurePackage, Kind: string(KindPrepare),
			ExecutorOwned: true, InputsDigest: choreographyPreparedFailureInputDigest},
		ChoreographyTaskSpec{Task: choreographyPreparedFailureConsumerTask,
			Package: choreographyPreparedFailureConsumerTask, Kind: string(KindBuild),
			PreparedProviders: []string{choreographyPreparedFailurePackage},
			Predecessors:      []string{choreographyPreparedFailureProviderTask}})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: choreographyGitFirstAttempt,
		Node: choreographyPreparedFailureNode, Branch: provider.Branch,
		ExpectedOID: provider.ReservationOID, InputsDigest: provider.InputsDigest,
		ProposalID: choreographyPreparedFailureClaimID}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindPrepare, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: provider.Task,
		Attempt: claim.Attempt, Node: claim.Node, Branch: provider.Branch},
		Assignment: acceptedClaim.OID, Status: StatusFailed}
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, resultDocument, nil, nil)
	require.NoError(t, err)
	pack, err := os.CreateTemp(t.TempDir(), choreographyPreparedFailurePackPattern)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: claim.Attempt, Node: claim.Node,
		Branch: provider.Branch, ClaimOID: acceptedClaim.OID,
		InputsDigest: claim.InputsDigest, ResultDigest: resultOID,
		ProposalID: choreographyPreparedFailureResultID}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack,
		choreographyPreparedFailurePackBytes)
	require.NoError(t, err)
	acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: provider.Task,
		Branch: provider.Branch, Attempt: claim.Attempt, Node: claim.Node,
		ResultOID: acceptedResult.OID}))
	consumer := broker.tasks[choreographyPreparedFailureConsumerTask]
	require.Zero(t, consumer.remaining)
	require.Equal(t, choreographyPreparedFailurePackage, consumer.preparedBlockedBy)
	require.False(t, broker.tasks[provider.Task].pendingPackageSettlement)

	coldPersist, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	coldOptions := choreographyFixtureBrokerOptions(coldPersist)
	coldOptions.OutcomePolicy = preparedBrokerPolicy{}
	cold, err := RecoverFirstNodeBroker(t.Context(), coldOptions)
	require.NoError(t, err)
	require.Zero(t, cold.tasks[choreographyPreparedFailureConsumerTask].remaining)
	require.Equal(t, choreographyPreparedFailurePackage,
		cold.tasks[choreographyPreparedFailureConsumerTask].preparedBlockedBy)
	require.False(t, cold.tasks[provider.Task].pendingPackageSettlement)
}
