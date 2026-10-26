// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

type choreographyFixturePickupResult struct {
	Claim      ChoreographyClaimProposal
	Accepted   ChoreographyReceipt
	Result     Result
	ProposalID string
	Outputs    []gitx.TreeEntry
}

func prepareChoreographyFixturePickupResult(t *testing.T, fixture *mailboxFixture,
	opts choreographyFixturePickupResult) (ChoreographyResultProposal, *os.File) {
	t.Helper()
	claim, accepted, result := opts.Claim, opts.Accepted, opts.Result
	identity := ChoreographyPickupIdentity{Run: claim.Run, Task: claim.Task,
		Node: claim.Node, PickupID: claim.PickupID}
	pendingBranch, err := FormatChoreographyPendingBranch(identity)
	require.NoError(t, err)
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, result.Status)
	require.NoError(t, err)
	pending := Pending{Header: result.Header, TaskBranch: claim.Branch,
		PickupID: claim.PickupID, ClaimOID: accepted.OID,
		AssignmentOID: accepted.AssignmentOID, GraphDigest: claim.GraphDigest,
		GraphRootOID: claim.GraphRootOID, InputsDigest: claim.InputsDigest,
		MembershipEpochOID:  claim.MembershipEpochOID,
		CapacityAuthorityID: claim.CapacityAuthorityID,
		LeaseIntentID:       claim.LeaseIntentID, LeaseWeight: claim.LeaseWeight}
	pending.Branch = pendingBranch
	pendingDocument, err := json.Marshal(pending)
	require.NoError(t, err)
	pendingOID, err := fixture.mailbox.commitMessage(t.Context(), MessagePending,
		pendingDocument, []string{accepted.OID}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, pendingOID, pendingBranch))
	result.PickupID, result.Assignment = claim.PickupID, accepted.OID
	result.PendingBranch, result.PendingOID, result.OutcomeBranch = pendingBranch, pendingOID, outcomeBranch
	resultDocument, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult,
		resultDocument, []string{pendingOID}, opts.Outputs)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushCreate(t.Context(), fixture.endpoint, resultOID, outcomeBranch))
	pack, err := os.CreateTemp(t.TempDir(), "pickup-fixture-result-*.pack")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), resultOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: claim.ChoreographyRunBinding,
		Task: claim.Task, Attempt: claim.Attempt, Node: claim.Node,
		Branch: claim.Branch, ClaimOID: accepted.OID,
		InputsDigest: claim.InputsDigest, PickupID: claim.PickupID,
		PendingBranch: pendingBranch, PendingOID: pendingOID,
		OutcomeBranch: outcomeBranch, ResultDigest: resultOID, ProposalID: opts.ProposalID}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack, choreographyPickupProofDocumentLimit)
	require.NoError(t, err)
	return proposal, pack
}
