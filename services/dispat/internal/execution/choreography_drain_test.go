// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyDrainPersistsSignedMembershipAndReplays(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 3600,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: f.binding, Mailbox: f.endpoint.mailbox, Ledger: ChoreographyLedgerReservation{
			Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
	})
	require.NoError(t, err)
	first := ChoreographyRuntimeNode{Name: "first", Platform: "linux/amd64", Capacity: 1,
		First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}
	peer := ChoreographyRuntimeNode{Name: "peer", Platform: "linux/amd64", Capacity: 1,
		AuthorityID: "11111111111111111111111111111111"}
	opts := FirstNodeBrokerOptions{Binding: f.binding, GraphRootOID: f.root,
		InitialMembershipEpochOID: f.complete, FirstNode: "first", Tasks: f.carrier.Tasks,
		Templates: f.carrier.Templates, Members: []ChoreographyRuntimeNode{first, peer},
		NodeCapacity: map[string]int{"first": 1, "peer": 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy, Persist: persist}
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	proposal := ChoreographyDrainProposal{ChoreographyRunBinding: f.binding,
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, Node: peer.Name,
		CapacityAuthorityID: peer.AuthorityID, MembershipEpochOID: f.complete,
		ExpectedLedgerOID: f.complete, RequestID: "peer-normal-stop"}
	receipt, err := broker.DrainNode(t.Context(), proposal)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.False(t, receipt.Refused)
	require.NotEmpty(t, receipt.Cursor.OID)
	require.True(t, broker.draining[peer.Name])
	replayed, err := broker.DrainNode(t.Context(), proposal)
	require.NoError(t, err)
	require.Equal(t, receipt.Cursor, replayed.Cursor)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.draining[peer.Name])
	require.Equal(t, receipt.Cursor.OID, recovered.memberEpoch[peer.Name])
}
