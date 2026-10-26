// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A local input-failure proof observes an inert complete-root task, but it
// cannot authorize Blocked after an exact direct Claim wins the Git CAS.
func TestChoreographyNoClaimProofLosesToExactClaim(t *testing.T) {
	fixture, initialPersist, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "build", Package: "package"})
	options := initialPersist.opts
	blockingPersist, err := NewGitChoreographyPersist(options)
	require.NoError(t, err)
	claimingPersist, err := NewGitChoreographyPersist(options)
	require.NoError(t, err)
	blockingOptions := choreographyFixtureBrokerOptions(blockingPersist)
	blockingOptions.OutcomePolicy = preparedBrokerPolicy{}
	blockingBroker, err := RecoverFirstNodeBroker(t.Context(), blockingOptions)
	require.NoError(t, err)
	coordinator := &Coordinator{Run: binding.Run, Links: []Link{{Name: "node-a"}},
		mailboxes:        map[string]*GitMailbox{"node-a": fixture.mailbox},
		canonicalMailbox: fixture.mailbox, GraphRootOID: options.GraphRootOID,
		choreographyInitialized: true, choreographyBroker: blockingBroker}
	blocker, proven, err := coordinator.ProveChoreographyNoClaim(t.Context(), spec.Task)
	require.NoError(t, err)
	require.True(t, proven)
	require.Equal(t, spec.Package, blocker)

	claimingOptions := choreographyFixtureBrokerOptions(claimingPersist)
	claimingOptions.OutcomePolicy = preparedBrokerPolicy{}
	claimingBroker, err := RecoverFirstNodeBroker(t.Context(), claimingOptions)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		ProposalID: "claim-after-stale-local-proof"}
	completeChoreographyFixtureClaim(t, claimingPersist, &claim)
	accepted, err := claimingBroker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)

	err = blockingBroker.AcceptBlocked(t.Context(), ChoreographyBlockedOutcome{
		ChoreographyRunBinding: binding, Task: spec.Task, Package: spec.Package,
		BlockedBy: spec.Package, PackageOutcome: "failed",
		LocalFailure: &ChoreographyLocalFailureEvidence{
			Part: choreographyFailurePartInputs, ErrorSHA256: strings.Repeat("a", choreographyFailureDigestHexSize),
			ReservationOID: spec.ReservationOID,
		},
	})
	require.Error(t, err, "a stale proof cannot turn a claimed task into a local failure")
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Equal(t, accepted.Cursor.OID, remoteChoreographyOID(t, fixture, options.Ledger.Branch))
	page, err := claimingPersist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, claimingPersist), 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
	require.Zero(t, blockingBroker.used[spec.Budget], "a rejected local block charges no capacity")
	recovered, err := RecoverFirstNodeBroker(t.Context(), claimingOptions)
	require.NoError(t, err)
	require.Equal(t, accepted.OID, recovered.tasks[spec.Task].tip)
	require.Equal(t, "node-a", recovered.tasks[spec.Task].owner)
	require.False(t, recovered.tasks[spec.Task].terminal)
}
