// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestWorkerRetainsAcceptedProbeWhenReplayRecordFailsAfterAdmission(t *testing.T) {
	fixture := newMailboxFixture(t)
	assignment := validReturnProbe(FormatBranch("build-a", KindProbe, time.Now()))
	binding := ChoreographyRunBinding{Run: assignment.Run, Generation: assignment.Generation,
		PlanDigest: assignment.PlanDigest, GraphDigest: assignment.GraphDigest}
	limits := TransferLimits{MaxFiles: 10, MaxBytes: 1024, MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}
	coordinator := NewCoordinator(binding.Run, binding.PlanDigest, binding.Generation,
		LocalNode{Name: "first", Capacity: 1}, []Link{{Name: "build-a", Endpoint: fixture.endpoint}},
		map[string]*GitMailbox{"build-a": fixture.mailbox}, fixture.signer, Timeouts{}, limits, zerolog.Nop())
	coordinator.GraphDigest = binding.GraphDigest
	handler, err := NewChoreographyProbeHandler(ProbeHandlerOptions{Coordinator: coordinator,
		Signer: fixture.signer, Binding: binding, MaxBytes: 1 << 20})
	require.NoError(t, err)
	seen := newWorkerReplayRecord(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		buffered := httptest.NewRecorder()
		handler.ServeHTTP(buffered, request)
		require.Equal(t, http.StatusOK, buffered.Code)
		// Admission has committed, but the peer has not received its receipt.
		require.NoError(t, os.Remove(seen.path))
		require.NoError(t, os.Mkdir(seen.path, 0o755))
		for key, values := range buffered.Header() {
			response.Header()[key] = values
		}
		response.WriteHeader(buffered.Code)
		_, err := response.Write(buffered.Body.Bytes())
		require.NoError(t, err)
	}))
	defer server.Close()
	pin := sha256.Sum256(server.Certificate().Raw)
	assignment.ReturnEndpoint, assignment.ReturnCertSHA256 = server.URL, hex.EncodeToString(pin[:])
	assignment.Limits = limits
	coordinator.ReturnEndpoint, coordinator.ReturnCertSHA256 = assignment.ReturnEndpoint, assignment.ReturnCertSHA256
	oid, err := fixture.mailbox.PrepareAssignment(t.Context(), &assignment)
	require.NoError(t, err)
	coordinator.recordOwnedRef(t.Context(), ownedRefStep{node: "build-a", branch: assignment.Branch, oid: oid})
	_, err = fixture.mailbox.Offer(t.Context(), assignment.Branch, oid)
	require.NoError(t, err)
	worker := &Worker{Node: "build-a", Seen: seen, Mailbox: fixture.mailbox, ReturnSigner: fixture.signer,
		Log: zerolog.Nop(), slots: make(chan struct{}, 1),
		Report: NodeReport{Protocol: ProtocolVersion, Dispat: "1.11.0", OS: "linux",
			Arch: "amd64", Capacity: 2, Limits: limits}}
	err = worker.answerProbe(t.Context(), ChainTip{Branch: assignment.Branch, OID: oid}, assignment)
	require.ErrorContains(t, err, "replacing the answered-work record")
	channel := worker.returnChannels[binding.Run]
	require.NotNil(t, channel, "the accepted roster must retain its authenticated return path")
	defer channel.client.Close()
	require.Equal(t, binding, channel.binding)
	require.True(t, seen.IsSeen(binding.Run, PreflightTask, 1))
	require.Error(t, seen.Persist(), "accepted admission does not bypass the claim persistence gate")
	require.NoError(t, os.Remove(seen.path))
	require.NoError(t, seen.Persist(), "the same accepted run can resume after repair")
	require.Same(t, channel, worker.returnChannels[binding.Run])
	reloaded, err := LoadSeenSet(seen.path, time.Now())
	require.NoError(t, err)
	require.True(t, reloaded.IsSeen(binding.Run, PreflightTask, 1))
}

func newWorkerReplayRecord(t *testing.T) *SeenSet {
	t.Helper()
	seen, err := LoadSeenSet(filepath.Join(t.TempDir(), "seen.json"), time.Now())
	require.NoError(t, err)
	return seen
}

func TestSeenSetPersistenceReadinessPreservesOnlyRecordedAttempts(t *testing.T) {
	seen := newWorkerReplayRecord(t)
	require.NoError(t, seen.Record("old-run", "core:build", 1, time.Now()))
	before, err := os.ReadFile(seen.path)
	require.NoError(t, err)
	require.NoError(t, seen.Persist())
	after, err := os.ReadFile(seen.path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, os.Remove(seen.path))
	require.NoError(t, os.Mkdir(seen.path, 0o755))
	require.Error(t, seen.Persist(), "a cached file identity is not evidence of a writable record")
	require.NoError(t, os.Remove(seen.path))
	require.NoError(t, seen.Persist())
	reloaded, err := LoadSeenSet(seen.path, time.Now())
	require.NoError(t, err)
	require.True(t, reloaded.IsSeen("old-run", "core:build", 1))
	require.Len(t, reloaded.entries, 1)
}

func TestWorkerRefusesProbeBeforeAdmissionWhenReplayStateCannotBeSaved(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-probe-abc"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	assignment := validReturnProbe(branch)
	worker, mailbox := newWorkerFixture(t, branch, tip, assignment)
	attachFakeProbeReturn(t, worker, assignment)
	fake := worker.returnChannels[assignment.Run].client.(*fakeWorkerReturn)
	accepted := fake.probe
	proposals := 0
	fake.probe = func(reply ChoreographyProbeReply) (ChoreographyReceipt, error) {
		proposals++
		if proposals == 1 {
			return ChoreographyReceipt{}, errors.New("reply lost")
		}
		return accepted(reply)
	}
	require.NoError(t, os.Mkdir(worker.Seen.path, 0o755))
	require.Error(t, worker.answerProbe(t.Context(), tip, assignment))
	require.Zero(t, proposals, "no remote admission precedes the replay-state gate")
	require.Contains(t, mailbox.reconsidered, branch)
	require.False(t, worker.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt))
	require.NoError(t, os.Remove(worker.Seen.path))
	require.Error(t, worker.answerProbe(t.Context(), tip, assignment))
	require.False(t, worker.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt),
		"an unknown probe reply must leave the exact probe retryable")
	require.NoError(t, worker.answerProbe(t.Context(), tip, assignment))
	require.Equal(t, 2, proposals)
	require.True(t, worker.Seen.IsSeen(assignment.Run, assignment.Task, assignment.Attempt))
}
