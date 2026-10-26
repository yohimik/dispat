// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestProbeReplyReturnsOverTLSAndOnlyFirstPushesBothSteps(t *testing.T) {
	fixture := newMailboxFixture(t)
	binding := ChoreographyRunBinding{Run: "run-1", Generation: "generation", PlanDigest: "plan",
		GraphDigest: "graph"}
	limits := TransferLimits{MaxFiles: 10, MaxBytes: 1024, MaxManifestBytes: 1 << 20}
	coordinator := NewCoordinator(binding.Run, binding.PlanDigest, binding.Generation,
		LocalNode{Name: "first", Capacity: 1}, []Link{{Name: "build-a", Endpoint: fixture.endpoint}},
		map[string]*GitMailbox{"build-a": fixture.mailbox}, fixture.signer, Timeouts{}, limits, zerolog.Nop())
	coordinator.GraphDigest = binding.GraphDigest
	handler, err := NewChoreographyProbeHandler(ProbeHandlerOptions{Coordinator: coordinator,
		Signer: fixture.signer, Binding: binding, MaxBytes: 1 << 20})
	require.NoError(t, err)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pinned := sha256.Sum256(server.Certificate().Raw)
	coordinator.ReturnEndpoint = server.URL
	coordinator.ReturnCertSHA256 = hex.EncodeToString(pinned[:])
	client, err := NewChoreographyReturnClient(ReturnClientOptions{BaseURL: server.URL,
		PinnedCertSHA256: coordinator.ReturnCertSHA256, Signer: fixture.signer, Binding: binding,
		MaxResultBytes: 1 << 20})
	require.NoError(t, err)
	defer client.Close()
	branch := FormatBranch("build-a", KindProbe, time.Now())
	assignment := &Assignment{Header: Header{Protocol: ProtocolVersion, Kind: KindProbe, Run: binding.Run,
		PlanDigest: binding.PlanDigest, Task: PreflightTask, Attempt: 1, Generation: binding.Generation,
		Node: "build-a", Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		ReturnEndpoint: server.URL, ReturnCertSHA256: coordinator.ReturnCertSHA256,
		GraphDigest: binding.GraphDigest, Limits: limits}
	oid, err := fixture.mailbox.PrepareAssignment(t.Context(), assignment)
	require.NoError(t, err)
	coordinator.recordOwnedRef(t.Context(), ownedRefStep{node: "build-a", branch: branch, oid: oid})
	_, err = fixture.mailbox.Offer(t.Context(), branch, oid)
	require.NoError(t, err)
	reply := ChoreographyProbeReply{ChoreographyRunBinding: binding, Node: "build-a", Branch: branch,
		AssignmentOID: oid, Report: NodeReport{Protocol: ProtocolVersion, Dispat: "1.11.0", OS: "linux",
			Arch: "amd64", Capacity: 2, Limits: limits}}
	receipt, err := client.SubmitProbe(t.Context(), reply)
	require.NoError(t, err)
	require.Equal(t, MessageResult, receipt.Kind)
	head, err := fixture.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	require.Equal(t, receipt.OID, head.OID)
	tip, err := fixture.mailbox.Inspect(t.Context(), head)
	require.NoError(t, err)
	require.Equal(t, MessageClaim, tip.Previous)
	again, err := client.SubmitProbe(t.Context(), reply)
	require.NoError(t, err)
	require.Equal(t, receipt.OID, again.OID, "repeated HTTP answer does not push another result")
	reply.Node = "foreign"
	_, err = client.SubmitProbe(t.Context(), reply)
	require.Error(t, err)
}
