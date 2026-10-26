// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyRetryPolicyIsExactSignedTaskPolicy(t *testing.T) {
	policy := ChoreographyJoinPolicy{RetryAfterUncertainAttempt: true}
	specs := []ChoreographyTaskSpec{
		{Task: "worker-build", RetryAfterUncertainAttempt: true},
		{Task: "native-publish", ExecutorOwned: true},
		{Task: "virtual-gate", Virtual: true},
	}
	require.NoError(t, validateChoreographyRetryPolicy(policy, specs))
	specs[0].RetryAfterUncertainAttempt = false
	require.ErrorContains(t, validateChoreographyRetryPolicy(policy, specs), choreographyRetryPolicyMismatchError)
	specs[0].RetryAfterUncertainAttempt = true
	specs[1].RetryAfterUncertainAttempt = true
	require.ErrorContains(t, validateChoreographyRetryPolicy(policy, specs), choreographyRetryPolicyMismatchError)
	specs[1].RetryAfterUncertainAttempt = false
	require.ErrorContains(t, validateChoreographyRetryPolicy(ChoreographyJoinPolicy{}, specs), choreographyRetryPolicyMismatchError)
}

func TestChoreographyContactLossWithoutRetrySafetyRetainsClaim(t *testing.T) {
	_, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
		choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
			AdmissionSeconds: choreographyTestAdmissionSeconds})
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: choreographyGitFirstAttempt, Node: "node-a",
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "unsafe-contact-claim"}
	completeChoreographyFixtureClaim(t, persist, &proposal)
	receipt, err := broker.AcceptClaim(t.Context(), proposal)
	require.NoError(t, err)
	_, err = broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: proposal.Attempt, Node: proposal.Node, ClaimOID: receipt.OID,
		AssignmentOID: receipt.AssignmentOID})
	require.NoError(t, err)
	require.ErrorContains(t, broker.MarkPickupContactLost(t.Context(), spec.Task, proposal.PickupID),
		choreographyPickupContactRetryUnsafeError)
	require.Equal(t, receipt.OID, broker.tasks[spec.Task].claim)
	require.False(t, broker.tasks[spec.Task].pickups[proposal.PickupID].contactLost)
}

func TestChoreographyColdConstructorsRejectRetryPolicyCarrierMismatch(t *testing.T) {
	_, persist, _, spec := newChoreographyLedgerFixture(t)
	opts := persist.opts
	opts.Tasks = []ChoreographyTaskSpec{spec}
	opts.Tasks[0].RetryAfterUncertainAttempt = true
	_, err := NewGitChoreographyPersist(opts)
	require.ErrorContains(t, err, choreographyRetryPolicyMismatchError)
	brokerOptions := choreographyFixtureBrokerOptions(persist)
	brokerOptions.Tasks = opts.Tasks
	_, err = NewFirstNodeBroker(brokerOptions)
	require.ErrorContains(t, err, choreographyRetryPolicyMismatchError)
}
