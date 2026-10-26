// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/hex"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestCoordinatorPreparesRetainedBootstrapWithoutCreatingRemoteRef(t *testing.T) {
	fixture := newMailboxFixture(t)
	plumbing := gitx.NewPlumbing(fixture.git)
	tree := plumbing.MakeTree(t.Context(), nil)
	planned := plumbing.CommitTree(t.Context(), tree, nil, "planned source")
	source := plumbing.CommitTree(t.Context(), tree, []string{planned}, "captured source")
	require.NoError(t, plumbing.Err())
	coordinator := NewCoordinator("run", "plan", "generation",
		LocalNode{Name: "first", Capacity: 1},
		[]Link{{Name: "peer", Endpoint: fixture.endpoint}},
		map[string]*GitMailbox{"peer": fixture.mailbox}, fixture.signer,
		Timeouts{}, TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}, zerolog.Nop())
	coordinator.GraphDigest = "graph"
	coordinator.Mode = "compilation"
	coordinator.AlgorithmVersion = ChoreographyGraphAlgorithmVersion
	coordinator.CanonicalEndpoint = fixture.endpoint
	coordinator.canonicalMailbox = fixture.mailbox
	coordinator.JoinPolicy = ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
		Authorized: []ChoreographyAuthorizedNode{
			{Name: "first", CoordinationEndpoint: fixture.endpoint},
			{Name: "peer", CoordinationEndpoint: fixture.endpoint}}}
	coordinator.ReturnEndpoint = "https://localhost:43881"
	coordinator.ReturnCertSHA256 = hex.EncodeToString(make([]byte, 32))
	coordinator.PinnedSources = &PinnedRunSources{Repositories: []AssignmentRepository{{
		Name: "", Snapshot: source, PlannedHead: planned}}}
	coordinator.PinnedInputs = &PinnedRunInputs{Repositories: coordinator.PinnedSources.Repositories,
		Selection: []byte(`{"command":"release"}`), Options: []byte(`{"strict":false}`)}
	require.NoError(t, coordinator.PrepareChoreographyBootstrap(t.Context(), CoordinatorBootstrapSources{}))
	require.NotEmpty(t, coordinator.BootstrapOID)
	retained, err := fixture.mailbox.ReadChoreographyBootstrap(t.Context(),
		coordinator.BootstrapOID, coordinator.Limits.MaxManifestBytes, coordinator.Limits.MaxPackBytes)
	require.NoError(t, err)
	require.Equal(t, coordinator.Mode, retained.Mode)
	parents, err := fixture.git.CommitParents(t.Context(), coordinator.BootstrapOID)
	require.NoError(t, err)
	require.Equal(t, []string{source}, parents)
	require.Empty(t, fixture.remoteBranches(t), "preparing the bootstrap does not push a ref")
	probe := &Assignment{Header: Header{Protocol: ProtocolVersion,
		Kind: KindProbe, Run: coordinator.Run, Generation: coordinator.Generation,
		PlanDigest: coordinator.PlanDigest, Task: PreflightTask, Attempt: 1,
		Node: "peer", Branch: "dispat-peer-probe"},
		PinnedInputs: coordinator.PinnedInputs, BootstrapOID: coordinator.BootstrapOID,
		DistributionMode: coordinator.Mode}
	probeOID, err := fixture.mailbox.PreparePinnedProbe(t.Context(), probe,
		[]string{source, coordinator.BootstrapOID})
	require.NoError(t, err)
	parents, err = fixture.git.CommitParents(t.Context(), probeOID)
	require.NoError(t, err)
	require.Equal(t, []string{source, coordinator.BootstrapOID}, parents)
	_, err = fixture.mailbox.PreparePinnedProbe(t.Context(), probe,
		[]string{coordinator.BootstrapOID, source})
	require.ErrorContains(t, err, "out of order")
	require.Error(t, coordinator.PrepareChoreographyBootstrap(t.Context(), CoordinatorBootstrapSources{}),
		"a generation does not get a second bootstrap identity")
}
