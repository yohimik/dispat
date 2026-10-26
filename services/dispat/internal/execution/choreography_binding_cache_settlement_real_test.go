// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const choreographyBindingSettlementClaimProposal = "provider-settlement-claim"
const choreographyBindingSettlementResultProposal = "provider-settlement-result"
const choreographyBindingSettlementPackPattern = "provider-settlement-*.pack"
const choreographyBindingSettlementDocumentLimit = 1 << 20

func acceptSettlementPickupResult(t *testing.T, fixture *mailboxFixture,
	broker *FirstNodeBroker, binding ChoreographyRunBinding, provider ChoreographyTaskSpec,
	claim ChoreographyClaimProposal, accepted ChoreographyReceipt) ChoreographyReceipt {
	t.Helper()
	identity := ChoreographyPickupIdentity{Run: binding.Run, Task: provider.Task,
		Node: claim.Node, PickupID: claim.PickupID}
	pendingBranch, err := FormatChoreographyPendingBranch(identity)
	require.NoError(t, err)
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, StatusSucceeded)
	require.NoError(t, err)
	pending := Pending{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: provider.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: provider.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: pendingBranch},
		TaskBranch: provider.Branch, PickupID: claim.PickupID,
		ClaimOID: accepted.OID, AssignmentOID: accepted.AssignmentOID,
		GraphDigest: binding.GraphDigest, GraphRootOID: claim.GraphRootOID,
		InputsDigest: claim.InputsDigest, MembershipEpochOID: claim.MembershipEpochOID,
		CapacityAuthorityID: claim.CapacityAuthorityID,
		LeaseIntentID:       claim.LeaseIntentID, LeaseWeight: claim.LeaseWeight}
	pendingDocument, err := json.Marshal(pending)
	require.NoError(t, err)
	pendingOID, err := fixture.mailbox.commitMessage(t.Context(), MessagePending,
		pendingDocument, []string{accepted.OID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, pendingOID, pendingBranch))
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: provider.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: provider.Task, Attempt: claim.Attempt,
		Node: claim.Node, Branch: provider.Branch}, Assignment: accepted.OID,
		Status: StatusSucceeded, PickupID: claim.PickupID,
		PendingBranch: pendingBranch, PendingOID: pendingOID, OutcomeBranch: outcomeBranch}
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult,
		resultDocument, []string{pendingOID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, resultOID, outcomeBranch))
	pack, err := os.CreateTemp(t.TempDir(), choreographyBindingSettlementPackPattern)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: provider.Task, Attempt: claim.Attempt, Node: claim.Node,
		Branch: provider.Branch, ClaimOID: accepted.OID, InputsDigest: claim.InputsDigest,
		PickupID: claim.PickupID, PendingBranch: pendingBranch, PendingOID: pendingOID,
		OutcomeBranch: outcomeBranch, ResultDigest: resultOID,
		ProposalID: choreographyBindingSettlementResultProposal}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack,
		choreographyBindingSettlementDocumentLimit)
	require.NoError(t, err)
	acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	return acceptedResult
}

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
		PickupID:   NewChoreographyPickupID(),
		ProposalID: choreographyBindingSettlementClaimProposal}
	completeChoreographyFixtureClaim(t, persist, &claim)
	claimed, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	accepted := acceptSettlementPickupResult(t, fixture, broker, binding, provider,
		claim, claimed)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: provider.Task, Branch: provider.Branch,
		Attempt: 1, Node: "node-a", ResultOID: accepted.OID, Succeeded: true}))
	cacheOptions := ChoreographyBindingCacheOptions{Binding: binding,
		LedgerRootOID: persist.opts.Ledger.OID, Reader: persist,
		Mailbox: fixture.mailbox, MaxDocumentBytes: choreographyBindingSettlementDocumentLimit,
		Tasks:          persist.opts.Tasks,
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
