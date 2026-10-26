// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const choreographyReturnHTTPTestWrongSecret = "different-return-authentication-secret"
const choreographyReturnHTTPTestClaimID = "http-exact-claim"
const choreographyReturnHTTPTestResultID = "http-exact-result"
const choreographyReturnHTTPTestOldResultID = "http-old-run-result"
const choreographyReturnHTTPTestOldRun = "retired-run"
const choreographyReturnHTTPTestAckID = "http-exact-ack"
const choreographyReturnHTTPTestMaximumDocumentBytes = 1 << 20

func TestChoreographyHTTPSClaimAndResultRejectWrongSignatureAndOldRun(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	handler, err := NewChoreographyReturnHandler(ReturnHandlerOptions{
		Broker: broker, Signer: fixture.signer, Binding: binding,
		MaxClaimBytes:  choreographyReturnHTTPTestMaximumDocumentBytes,
		MaxResultBytes: choreographyReturnHTTPTestMaximumDocumentBytes,
		TempDir:        t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pinned := sha256.Sum256(server.Certificate().Raw)
	options := ReturnClientOptions{BaseURL: server.URL,
		PinnedCertSHA256: hex.EncodeToString(pinned[:]), Signer: fixture.signer,
		Binding: binding, MaxResultBytes: choreographyReturnHTTPTestMaximumDocumentBytes}
	client, err := NewChoreographyReturnClient(options)
	require.NoError(t, err)
	defer client.Close()
	wrongSigner, err := NewSigner(choreographyReturnHTTPTestWrongSecret)
	require.NoError(t, err)
	options.Signer = wrongSigner
	forged, err := NewChoreographyReturnClient(options)
	require.NoError(t, err)
	defer forged.Close()
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, InputsDigest: spec.InputsDigest,
		ProposalID: choreographyReturnHTTPTestClaimID}
	completeChoreographyFixtureClaim(t, persist, &claim)
	ledgerBefore, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	_, err = forged.SubmitClaim(t.Context(), claim)
	require.Error(t, err)
	require.Equal(t, offered.AssignmentOID, remoteChoreographyOID(t, fixture, spec.Branch))
	ledgerAfter, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerBefore, ledgerAfter)
	accepted, err := client.SubmitClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	ledgerAccepted, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	replayed, err := client.SubmitClaim(t.Context(), claim)
	require.NoError(t, err)
	require.Equal(t, accepted, replayed)
	ledgerReplay, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerAccepted, ledgerReplay)
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: claim.Node, Branch: spec.Branch}, Assignment: accepted.OID,
		Status: StatusSucceeded}
	document, err := json.Marshal(result)
	require.NoError(t, err)
	rawOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, document, nil, nil)
	require.NoError(t, err)
	pack, err := os.CreateTemp(t.TempDir(), "return-result-*.pack")
	require.NoError(t, err)
	defer pack.Close()
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), rawOID, pack))
	packBytes, err := os.ReadFile(pack.Name())
	require.NoError(t, err)
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: claim.Node, Branch: spec.Branch,
		ClaimOID: accepted.OID, InputsDigest: claim.InputsDigest,
		ProposalID: choreographyReturnHTTPTestResultID, ResultDigest: rawOID}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, bytes.NewReader(packBytes),
		choreographyReturnHTTPTestMaximumDocumentBytes)
	require.NoError(t, err)
	_, err = forged.SubmitResult(t.Context(), proposal, bytes.NewReader(packBytes))
	require.Error(t, err)
	ledgerAfterForgedResult, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerAccepted, ledgerAfterForgedResult)
	require.Equal(t, accepted.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	resultReceipt, err := client.SubmitResult(t.Context(), proposal, bytes.NewReader(packBytes))
	require.NoError(t, err)
	require.True(t, resultReceipt.Accepted)
	require.Equal(t, resultReceipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
	ledgerAfterResult, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	resultReplay, err := client.SubmitResult(t.Context(), proposal, bytes.NewReader(packBytes))
	require.NoError(t, err)
	require.Equal(t, resultReceipt, resultReplay)
	ledgerAfterResultReplay, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerAfterResult, ledgerAfterResultReplay)
	oldBinding := binding
	oldBinding.Run = choreographyReturnHTTPTestOldRun
	oldResult := result
	oldResult.Header.Run = oldBinding.Run
	oldDocument, err := json.Marshal(oldResult)
	require.NoError(t, err)
	oldOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, oldDocument, nil, nil)
	require.NoError(t, err)
	oldPack, err := os.CreateTemp(t.TempDir(), "return-old-result-*.pack")
	require.NoError(t, err)
	defer oldPack.Close()
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), oldOID, oldPack))
	oldPackBytes, err := os.ReadFile(oldPack.Name())
	require.NoError(t, err)
	oldProposal := proposal
	oldProposal.ChoreographyRunBinding = oldBinding
	oldProposal.ProposalID = choreographyReturnHTTPTestOldResultID
	oldProposal.ResultDigest = oldOID
	oldProposal, err = PrepareChoreographyResult(t.Context(), oldProposal, bytes.NewReader(oldPackBytes),
		choreographyReturnHTTPTestMaximumDocumentBytes)
	require.NoError(t, err)
	options.Binding = oldBinding
	oldClient, err := NewChoreographyReturnClient(options)
	require.NoError(t, err)
	defer oldClient.Close()
	_, err = oldClient.SubmitResult(t.Context(), oldProposal, bytes.NewReader(oldPackBytes))
	require.Error(t, err, "a signed old-run Result cannot enter this run's HTTPS handler")
	ledgerAfterOldRun, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerAfterResult, ledgerAfterOldRun)
	require.Equal(t, resultReceipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
}

func TestChoreographyHTTPSAckRejectsForeignTaskNodeAndSignature(t *testing.T) {
	fixture, persist, broker, binding, spec, offered := newCompiledCancellationFixture(t)
	handler, err := NewChoreographyReturnHandler(ReturnHandlerOptions{
		Broker: broker, Signer: fixture.signer, Binding: binding,
		MaxClaimBytes:  choreographyReturnHTTPTestMaximumDocumentBytes,
		MaxResultBytes: choreographyReturnHTTPTestMaximumDocumentBytes,
		TempDir:        t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pinned := sha256.Sum256(server.Certificate().Raw)
	options := ReturnClientOptions{BaseURL: server.URL,
		PinnedCertSHA256: hex.EncodeToString(pinned[:]), Signer: fixture.signer,
		Binding: binding, MaxResultBytes: choreographyReturnHTTPTestMaximumDocumentBytes}
	client, err := NewChoreographyReturnClient(options)
	require.NoError(t, err)
	defer client.Close()
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: offered.AssignmentOID, InputsDigest: spec.InputsDigest,
		ProposalID: choreographyReturnHTTPTestClaimID}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := client.SubmitClaim(t.Context(), claim)
	require.NoError(t, err)
	cancelOID, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: claim.Node, AssignmentOID: accepted.AssignmentOID,
		ClaimOID: accepted.OID})
	require.NoError(t, err)
	ack := ChoreographyAckProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Branch: spec.Branch, Attempt: 1, Node: claim.Node,
		AssignmentOID: accepted.AssignmentOID, CancelOID: cancelOID,
		ProposalID: choreographyReturnHTTPTestAckID, Phase: PhaseAuthorizationWait}
	ledgerBefore, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	wrongSigner, err := NewSigner(choreographyReturnHTTPTestWrongSecret)
	require.NoError(t, err)
	options.Signer = wrongSigner
	forged, err := NewChoreographyReturnClient(options)
	require.NoError(t, err)
	defer forged.Close()
	_, err = forged.SubmitAck(t.Context(), ack)
	require.Error(t, err)
	foreignTask := ack
	foreignTask.Task = "foreign-task"
	_, err = client.SubmitAck(t.Context(), foreignTask)
	require.Error(t, err)
	foreignNode := ack
	foreignNode.Node = "foreign-node"
	_, err = client.SubmitAck(t.Context(), foreignNode)
	require.Error(t, err)
	ledgerAfter, err := persist.LedgerHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, ledgerBefore, ledgerAfter)
	require.Equal(t, cancelOID, remoteChoreographyOID(t, fixture, spec.Branch))
	acceptedAck, err := client.SubmitAck(t.Context(), ack)
	require.NoError(t, err)
	require.True(t, acceptedAck.Accepted)
	ackQuery, err := FormatAckReceiptQuery(ack)
	require.NoError(t, err)
	lookedUp, found, err := persist.LookupReceipt(t.Context(), ackQuery)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, acceptedAck, lookedUp)
	replayed, err := client.SubmitAck(t.Context(), ack)
	require.NoError(t, err)
	require.Equal(t, acceptedAck, replayed)
}
