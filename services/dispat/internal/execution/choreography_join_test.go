// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyLateJoinRequiresNodeKeyAndFencesReplacementAndStopping(t *testing.T) {
	f := newCompleteGraphCarrierFixture(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	public2, private2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: 3600,
		Authorized: []ChoreographyAuthorizedNode{
			{Name: "late", CoordinationEndpoint: f.endpoint.endpoint, IdentityPublicKey: base64.RawURLEncoding.EncodeToString(public), LateJoin: true},
			{Name: "later", CoordinationEndpoint: f.endpoint.endpoint, IdentityPublicKey: base64.RawURLEncoding.EncodeToString(public2), LateJoin: true}}}
	require.NoError(t, policy.Validate(f.endpoint.endpoint))
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
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: f.binding, GraphRootOID: f.root,
		InitialMembershipEpochOID: f.complete, FirstNode: "first", Tasks: f.carrier.Tasks,
		Templates: f.carrier.Templates, Members: []ChoreographyRuntimeNode{first, peer},
		NodeCapacity: map[string]int{"first": 1, "peer": 1}, Budgets: map[string]int{ChoreographyBudgetBuild: 1},
		JoinPolicy: policy, Persist: persist})
	require.NoError(t, err)
	join := ChoreographyJoinProposal{ChoreographyRunBinding: f.binding,
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		Node: "late", Endpoint: f.endpoint.endpoint, RequestID: "join-late-1", ExpectedLedgerOID: f.complete,
		Report: NodeReport{Protocol: ChoreographyProtocolVersion, Dispat: "same-build", OS: "linux", Arch: "amd64",
			Capacity: 1, AuthorityID: "22222222222222222222222222222222", GitVersion: "git"}}
	unsigned := join
	unsigned.IdentitySignature = "forged-shared-token"
	denied, err := broker.AdmitNode(t.Context(), unsigned)
	require.NoError(t, err)
	require.True(t, denied.Refused)
	require.Equal(t, choreographyJoinRefusalIdentity, denied.Reason)
	require.Equal(t, f.complete, remoteChoreographyOID(t, f.endpoint, FormatChoreographyLedgerBranch(f.binding.Run)))
	require.NoError(t, SignChoreographyJoinProposal(&join, private))
	forgedCapacity := join
	forgedCapacity.Report.Capacity = 2
	denied, err = broker.AdmitNode(t.Context(), forgedCapacity)
	require.NoError(t, err)
	require.True(t, denied.Refused)
	require.Equal(t, choreographyJoinRefusalIdentity, denied.Reason)
	receipt, err := broker.AdmitNode(t.Context(), join)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.Equal(t, receipt.Cursor.OID, broker.memberEpoch[join.Node])
	replayed, err := broker.AdmitNode(t.Context(), join)
	require.NoError(t, err)
	require.Equal(t, receipt.Cursor, replayed.Cursor)
	replacement := join
	replacement.RequestID = "join-late-replacement"
	replacement.ExpectedLedgerOID = receipt.Cursor.OID
	replacement.Report.AuthorityID = "33333333333333333333333333333333"
	replacement.IdentitySignature = ""
	require.NoError(t, SignChoreographyJoinProposal(&replacement, private))
	refused, err := broker.AdmitNode(t.Context(), replacement)
	require.NoError(t, err)
	require.True(t, refused.Refused)
	require.Equal(t, choreographyJoinRefusalAlreadyAdmitted, refused.Reason)
	stop, err := broker.StopClaims(t.Context())
	require.NoError(t, err)
	later := join
	later.Node = "later"
	later.RequestID = "join-after-stop"
	later.ExpectedLedgerOID = stop.OID
	later.Report.AuthorityID = "44444444444444444444444444444444"
	later.IdentitySignature = ""
	require.NoError(t, SignChoreographyJoinProposal(&later, private2))
	refused, err = broker.AdmitNode(t.Context(), later)
	require.NoError(t, err)
	require.True(t, refused.Refused)
	require.Equal(t, choreographyJoinRefusalStopping, refused.Reason)
}
