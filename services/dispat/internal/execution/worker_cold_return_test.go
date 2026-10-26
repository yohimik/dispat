// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestWorkerRestoresSignedReturnAfterAnsweredProbeRefIsGone(t *testing.T) {
	first := newMailboxFixture(t)
	plumbing := gitx.NewPlumbing(first.git)
	tree := plumbing.MakeTree(t.Context(), nil)
	planned := plumbing.CommitTree(t.Context(), tree, nil, "planned source")
	snapshot := plumbing.CommitTree(t.Context(), tree, []string{planned}, "snapshot")
	require.NoError(t, plumbing.Err())
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	const peerName = "peer"
	coordinator := NewCoordinator(binding.Run, binding.PlanDigest, binding.Generation,
		LocalNode{Name: "first", Capacity: 1},
		[]Link{{Name: peerName, Endpoint: first.endpoint}},
		map[string]*GitMailbox{peerName: first.mailbox}, first.signer,
		Timeouts{}, TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}, zerolog.Nop())
	coordinator.GraphDigest, coordinator.Mode = binding.GraphDigest, "compilation"
	coordinator.AlgorithmVersion = ChoreographyGraphAlgorithmVersion
	coordinator.CanonicalEndpoint, coordinator.canonicalMailbox = first.endpoint, first.mailbox
	coordinator.JoinPolicy = ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
		Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: first.endpoint},
			{Name: peerName, CoordinationEndpoint: first.endpoint}}}
	coordinator.ReturnEndpoint = "https://localhost:43881"
	coordinator.ReturnCertSHA256 = strings.Repeat("0", 64)
	coordinator.PinnedSources = &PinnedRunSources{Repositories: []AssignmentRepository{{
		Snapshot: snapshot, PlannedHead: planned}}}
	coordinator.PinnedInputs = &PinnedRunInputs{Repositories: coordinator.PinnedSources.Repositories,
		Selection: []byte(`{"command":"release"}`), Options: []byte(`{"strict":false}`)}
	require.NoError(t, coordinator.PrepareChoreographyBootstrap(t.Context(), CoordinatorBootstrapSources{}))
	probeBranch := FormatBranch(peerName, KindProbe, time.Now())
	probe := &Assignment{Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindProbe,
		Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
		Task: PreflightTask, Attempt: 1, Node: peerName, Branch: probeBranch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		GraphDigest: binding.GraphDigest, DistributionMode: coordinator.Mode,
		ReturnEndpoint: coordinator.ReturnEndpoint, ReturnCertSHA256: coordinator.ReturnCertSHA256,
		BootstrapOID: coordinator.BootstrapOID, PinnedInputs: coordinator.PinnedInputs}
	probeOID, err := first.mailbox.PreparePinnedProbe(t.Context(), probe,
		[]string{snapshot, coordinator.BootstrapOID})
	require.NoError(t, err)
	require.NoError(t, first.git.PushCreateAtomic(t.Context(), first.endpoint,
		[]gitx.CreateRef{{Branch: probeBranch, OID: probeOID}}))
	aliasWorker := &Worker{Node: peerName, Endpoint: "file://" + first.endpoint,
		Mailbox: first.mailbox, ReturnSigner: first.signer,
		Report: NodeReport{Limits: TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}}
	err = aliasWorker.answerProbeByReturn(t.Context(), ChainTip{OID: probeOID}, *probe)
	require.ErrorContains(t, err, workerProbeMailboxEndpointMismatchError)
	require.Empty(t, aliasWorker.returnChannels,
		"a mailbox endpoint alias must be refused before a successful preflight reply")
	branch := FormatChoreographyTaskBranch(binding.Run, "core:build")
	reservation := &Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: "core:build", Attempt: 1,
		Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)},
		BootstrapOID: coordinator.BootstrapOID, MaxAttempts: maxPlacementAttempts}
	reservationOID, err := first.mailbox.PrepareChoreographyReservation(t.Context(), reservation)
	require.NoError(t, err)
	offer := &Assignment{Header: reservation.Header, GraphDigest: binding.GraphDigest,
		BootstrapOID: coordinator.BootstrapOID, Weight: 1}
	offer.Node = peerName
	offerOID, err := first.mailbox.PrepareAssignmentOnReservation(t.Context(), offer, reservationOID)
	require.NoError(t, err)
	require.NoError(t, first.git.PushCreateAtomic(t.Context(), first.endpoint,
		[]gitx.CreateRef{{Branch: branch, OID: offerOID}}))
	// The restarted process has no in-memory return channel and the answered
	// probe ref has been retired. Only the offered task ancestry remains.
	runGitIn(t, first.endpoint, "update-ref", "-d", "refs/heads/"+probeBranch)
	peer := first.second(t)
	heads, err := peer.mailbox.Observe(t.Context(), "refs/heads/"+branch, nil)
	require.NoError(t, err)
	require.Len(t, heads, 1)
	tip, err := peer.mailbox.Inspect(t.Context(), heads[0])
	require.NoError(t, err)
	document, err := peer.mailbox.Read(t.Context(), tip, 1<<20)
	require.NoError(t, err)
	var observed Assignment
	require.NoError(t, json.Unmarshal(document, &observed))
	seen, err := LoadSeenSet(filepath.Join(t.TempDir(), "seen.json"), time.Now())
	require.NoError(t, err)
	require.NoError(t, seen.Record(binding.Run, PreflightTask, 1, time.Now()))
	worker := &Worker{Node: peerName, Endpoint: first.endpoint, Mailbox: peer.mailbox,
		ReturnSigner: first.signer, Seen: seen,
		Report: NodeReport{Limits: TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}}
	require.True(t, seen.IsSeen(binding.Run, PreflightTask, 1))
	channel, err := worker.returnChannelForTask(t.Context(), tip, observed)
	require.NoError(t, err)
	require.Equal(t, binding, channel.binding)
	require.Equal(t, coordinator.BootstrapOID, channel.bootstrapOID)
	require.Equal(t, coordinator.ReturnEndpoint, channel.endpoint)
	require.Equal(t, coordinator.ReturnCertSHA256, channel.certSHA256)
	worker.closeReturnChannels()
	worker.Endpoint = "file:///different-mailbox"
	_, err = worker.returnChannelForTask(t.Context(), tip, observed)
	require.ErrorContains(t, err, "different signed return identity")
}
