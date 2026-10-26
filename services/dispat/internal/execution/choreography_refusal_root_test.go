// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyRefusalReceiptKeepsExactGraphRoot(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: f.binding, Mailbox: f.endpoint.mailbox,
		Ledger:       ChoreographyLedgerReservation{Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
	})
	require.NoError(t, err)
	p := ChoreographyClaimProposal{ChoreographyRunBinding: f.binding,
		Task: f.spec.Task, Attempt: 1, Node: "peer", Branch: f.spec.Branch,
		ExpectedOID: f.taskRoot, ExpectedLedgerOID: f.complete, GraphRootOID: f.root,
		MembershipEpochOID: f.complete, CapacityAuthorityID: "11111111111111111111111111111111",
		LeaseIntentID: nodeSlotLeaseID(NodeSlotIntent{Run: f.binding.Run, Task: f.spec.Task, Attempt: 1}),
		LeaseWeight:   1, InputsDigest: "signed-ready-input", ProposalID: "refusal-root"}
	first, err := persist.PersistRefusal(t.Context(), p, ChoreographyRefusalBudgetCapacity)
	require.NoError(t, err)
	require.True(t, first.Refused)
	require.Equal(t, f.root, first.GraphRootOID)
	query, err := FormatClaimReceiptQuery(p)
	require.NoError(t, err)
	replayed, found, err := persist.LookupReceipt(t.Context(), query)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, f.root, replayed.GraphRootOID)
	require.Equal(t, first.FenceOID, replayed.FenceOID)
}
