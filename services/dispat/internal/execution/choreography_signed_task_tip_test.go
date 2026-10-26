// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// A durable Result can be replayed after a legitimate successor Claim, but a
// foreign commit on the canonical task ref cannot become a winning Decision.
func TestChoreographySignedTaskTipPreservesResultReplayAndFencesForeignDecision(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds:           choreographyTestAdmissionSeconds,
			RetryAfterUncertainAttempt: true})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	first := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: "node-a",
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID,
		InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(),
		ProposalID: "signed-tip-first-claim"}
	completeChoreographyFixtureClaim(t, persist, &first)
	firstClaim, err := broker.AcceptClaim(t.Context(), first)
	require.NoError(t, err)
	failed := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: first.Attempt,
		Node: first.Node, Branch: spec.Branch}, Assignment: firstClaim.OID,
		Status: StatusFailed}
	firstResult, pack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: first, Accepted: firstClaim,
			Result: failed, ProposalID: "signed-tip-first-result"})
	accepted, err := broker.AcceptResult(t.Context(), firstResult, pack)
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: first.Attempt, Node: first.Node, ResultOID: accepted.OID, Retry: true}))
	second := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: broker.tasks[spec.Task].attempt, Node: first.Node,
		Branch: spec.Branch, ExpectedOID: broker.tasks[spec.Task].tip,
		InputsDigest: broker.tasks[spec.Task].inputsDigest,
		PickupID:     NewChoreographyPickupID(), ProposalID: "signed-tip-successor-claim"}
	completeChoreographyFixtureClaim(t, persist, &second)
	secondClaim, err := broker.AcceptClaim(t.Context(), second)
	require.NoError(t, err)
	beforeReplay, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	replayed, err := broker.AcceptResult(t.Context(), firstResult, pack)
	require.NoError(t, err)
	require.Equal(t, accepted.OID, replayed.OID)
	afterReplay, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, beforeReplay.OID, afterReplay.OID,
		"an exact prior Result replay creates no second signed receipt")
	require.Equal(t, secondClaim.OID, remoteChoreographyOID(t, fixture, spec.Branch))

	succeeded := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: second.Attempt,
		Node: second.Node, Branch: spec.Branch}, Assignment: secondClaim.OID,
		Status: StatusSucceeded}
	secondResult, secondPack := prepareChoreographyFixturePickupResult(t, fixture,
		choreographyFixturePickupResult{Claim: second, Accepted: secondClaim,
			Result: succeeded, ProposalID: "signed-tip-successor-result"})
	secondReceipt, err := broker.AcceptResult(t.Context(), secondResult, secondPack)
	require.NoError(t, err)
	foreign := Withdrawal{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: FormatRunID(), Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: second.Attempt,
		Node: second.Node, Branch: spec.Branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		Assignment: secondClaim.AssignmentOID, Tip: secondClaim.OID}
	foreignBytes, err := json.Marshal(foreign)
	require.NoError(t, err)
	foreignOID, err := fixture.mailbox.commitMessage(t.Context(), MessageCancel,
		foreignBytes, []string{secondClaim.OID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAtomicAdvances(t.Context(), fixture.endpoint,
		[]gitx.BranchAdvance{{Branch: spec.Branch, ExpectedOld: secondClaim.OID, OID: foreignOID}}))
	beforeDecision, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	err = broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: second.Attempt, Node: second.Node, ResultOID: secondReceipt.OID,
		Succeeded: true})
	require.ErrorContains(t, err, choreographyLedgerTaskTipDiffersFromSignedHistoryErr)
	afterDecision, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, beforeDecision.OID, afterDecision.OID,
		"a foreign task tip cannot become canonical Terminal")
	require.Equal(t, foreignOID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.False(t, broker.tasks[spec.Task].terminal)
}
