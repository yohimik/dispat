// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestCompilationInitializesBootstrapBoundPeerIndex(t *testing.T) {
	fixture := newMailboxFixture(t)
	c := NewCoordinator("run", "plan", "generation", LocalNode{Name: "first", Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
		[]Link{{Name: "peer", Endpoint: fixture.endpoint}},
		map[string]*GitMailbox{"peer": fixture.mailbox}, fixture.signer,
		Timeouts{}, TransferLimits{MaxManifestBytes: 1 << 20}, zerolog.Nop())
	c.Mode, c.AlgorithmVersion = "compilation", ChoreographyGraphAlgorithmVersion
	c.CanonicalEndpoint, c.canonicalMailbox = fixture.endpoint, fixture.mailbox
	c.JoinPolicy = ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
		Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: fixture.endpoint},
			{Name: "peer", CoordinationEndpoint: fixture.endpoint}}}
	c.Pool = NewPool(c.Links, []*NodeReport{{OS: "linux", Arch: "amd64", Capacity: 1, AuthorityID: "11111111111111111111111111111111"}}, c.Local, zerolog.Nop())
	bootstrapOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyBootstrapKind,
		[]byte(`{"run":"run"}`), nil, nil)
	require.NoError(t, err)
	c.BootstrapOID = bootstrapOID
	template := ChoreographyCompiledTemplate{Task: "core:build", Kind: KindBuild, Weight: 1}
	digest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	spec := ChoreographyTaskSpec{Task: template.Task, Kind: template.Kind,
		Budget: ChoreographyBudgetBuild, Weight: 1, Placement: PlacementAnyNode,
		TemplateDigest: digest, EligibleNodes: []string{"first", "peer"}}
	c.GraphDigest, err = DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding:          ChoreographyRunBinding{Run: c.Run, Generation: c.Generation, PlanDigest: c.PlanDigest},
		AlgorithmVersion: c.AlgorithmVersion, SemanticGraphDigest: "semantic-stage-graph",
		Tasks: []ChoreographyTaskSpec{spec}, Templates: []ChoreographyCompiledTemplate{template}})
	require.NoError(t, err)
	_, err = c.InitializeChoreographyTasks(t.Context(), ChoreographyInitialization{
		BootstrapOID: bootstrapOID, Tasks: []ChoreographyTaskSpec{spec},
		Templates: []ChoreographyCompiledTemplate{template}, SemanticGraphDigest: "semantic-stage-graph",
		Budgets: []ChoreographyRuntimeBudget{{Name: ChoreographyBudgetBuild, Capacity: 1}},
	})
	require.NoError(t, err)
	require.Len(t, fixture.remoteBranches(t), 5, "task, ledger, complete root and two participation indexes")
	require.Len(t, c.choreographySlots.free["peer"], 1)
	slot := c.choreographySlots.free["peer"][0]
	require.Equal(t, FormatSlotBranch("peer", "run", 0), slot.branch)
	parents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), slot.oid)
	require.NoError(t, err)
	require.Len(t, parents, 1)
	inertParents, err := fixture.mailbox.plumbing.CommitParents(t.Context(), parents[0])
	require.NoError(t, err)
	require.Equal(t, []string{bootstrapOID}, inertParents)
	tree, err := fixture.mailbox.readTree(t.Context(), slot.oid)
	require.NoError(t, err)
	data, err := fixture.mailbox.Read(t.Context(), ChainTip{Kind: tree.kind,
		document: tree.document, signature: tree.signature}, 1<<20)
	require.NoError(t, err)
	require.Contains(t, string(data), `"bootstrapOid":"`+bootstrapOID+`"`)
	require.Contains(t, string(data), `"graphRootOid":"`+c.GraphRootOID+`"`)
	peer := fixture.second(t)
	observed, err := peer.mailbox.Observe(t.Context(), FormatSlotPattern("peer"), nil)
	require.NoError(t, err)
	require.Len(t, observed, 1, "a preauthorized absent node discovers its activated index")
	require.Equal(t, slot.branch, observed[0].Name)
	require.Equal(t, slot.oid, observed[0].OID)
}
