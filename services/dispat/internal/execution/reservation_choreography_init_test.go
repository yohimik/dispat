// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestChoreographyInitializesOneAuthoritativeRefPerTaskAndLedger(t *testing.T) {
	fixture := newMailboxFixture(t)
	coordinator := NewCoordinator("run", "plan", "generation",
		LocalNode{Name: "first", Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
		[]Link{{Name: "peer", Endpoint: fixture.endpoint}},
		map[string]*GitMailbox{"peer": fixture.mailbox}, fixture.signer,
		Timeouts{}, TransferLimits{MaxManifestBytes: 1 << 20}, zerolog.Nop())
	coordinator.GraphDigest = "graph"
	coordinator.AlgorithmVersion = ChoreographyGraphAlgorithmVersion
	coordinator.CanonicalEndpoint, coordinator.canonicalMailbox = fixture.endpoint, fixture.mailbox
	coordinator.JoinPolicy = ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
		Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: fixture.endpoint},
			{Name: "peer", CoordinationEndpoint: fixture.endpoint}}}
	coordinator.Pool = NewPool(coordinator.Links, []*NodeReport{{OS: "linux", Arch: "amd64", Capacity: 1, AuthorityID: "11111111111111111111111111111111"}},
		coordinator.Local, zerolog.Nop())
	bootstrapOID, err := fixture.mailbox.commitMessage(t.Context(), choreographyBootstrapKind,
		[]byte(`{"run":"run"}`), nil, nil)
	require.NoError(t, err)
	coordinator.BootstrapOID = bootstrapOID
	templates := []ChoreographyCompiledTemplate{
		{Task: "core:build", Kind: KindBuild, Weight: 1},
		{Task: "core:publish", Kind: "publish", Weight: 1},
	}
	buildDigest, err := DigestChoreographyCompiledTemplate(templates[0])
	require.NoError(t, err)
	publishDigest, err := DigestChoreographyCompiledTemplate(templates[1])
	require.NoError(t, err)
	tasks := []ChoreographyTaskSpec{
		{Task: "core:build", Kind: KindBuild, Budget: ChoreographyBudgetBuild, Weight: 1,
			Placement: PlacementAnyNode, TemplateDigest: buildDigest,
			EligibleNodes: []string{"first", "peer"}},
		{Task: "core:publish", Kind: "publish", Budget: ChoreographyBudgetPublish,
			Weight: 1, Placement: PlacementOrchestrator, LocalAuthority: true,
			TemplateDigest: publishDigest, EligibleNodes: []string{"first"}},
		{Task: "core:relay", Virtual: true},
	}
	coordinator.GraphDigest, err = DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: ChoreographyRunBinding{Run: coordinator.Run, Generation: coordinator.Generation,
			PlanDigest: coordinator.PlanDigest},
		AlgorithmVersion: coordinator.AlgorithmVersion, SemanticGraphDigest: "semantic-stage-graph",
		Tasks: tasks, Templates: templates})
	require.NoError(t, err)
	_, err = coordinator.InitializeChoreographyTasks(t.Context(), ChoreographyInitialization{
		BootstrapOID: "foreign", Tasks: tasks,
		Budgets: []ChoreographyRuntimeBudget{{Name: "build", Capacity: 1}}})
	require.Error(t, err, "a caller cannot substitute another run bootstrap")
	initialized, err := coordinator.InitializeChoreographyTasks(t.Context(),
		ChoreographyInitialization{BootstrapOID: bootstrapOID, Tasks: tasks,
			Templates: templates, SemanticGraphDigest: "semantic-stage-graph",
			Budgets: []ChoreographyRuntimeBudget{{Name: ChoreographyBudgetBuild, Capacity: 1},
				{Name: ChoreographyBudgetPublish, Capacity: 1}}})
	require.NoError(t, err)
	require.Equal(t, FormatChoreographyLedgerBranch("run"), initialized.Ledger.Branch)
	_, roster, err := ReadChoreographyLedgerRoot(t.Context(), fixture.mailbox,
		ChoreographyRunBinding{Run: coordinator.Run, Generation: coordinator.Generation,
			PlanDigest: coordinator.PlanDigest, GraphDigest: coordinator.GraphDigest}, bootstrapOID)
	require.NoError(t, err)
	require.Equal(t, []ChoreographyRuntimeBudget{{Name: ChoreographyBudgetBuild, Capacity: 1},
		{Name: ChoreographyBudgetPublish, Capacity: 1}}, roster.Budgets)
	require.Len(t, roster.Nodes, 2)
	require.Equal(t, FormatChoreographyTaskBranch("run", "core:build"), initialized.Tasks[0].Branch)
	require.NotEmpty(t, initialized.Tasks[0].ReservationOID)
	require.Equal(t, FormatChoreographyTaskBranch("run", "core:publish"), initialized.Tasks[1].Branch)
	require.NotEmpty(t, initialized.Tasks[1].ReservationOID)
	require.Empty(t, initialized.Tasks[2].Branch)
	require.Len(t, fixture.remoteBranches(t), 6, "two command branches, ledger, graph and two participation indexes")
	expected := Reservation{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: "run", PlanDigest: "plan", Generation: "generation",
		Task: "core:build", Attempt: 1, Branch: initialized.Tasks[0].Branch},
		BootstrapOID: bootstrapOID, MaxAttempts: maxPlacementAttempts}
	require.NoError(t, fixture.mailbox.VerifyChoreographyReservation(t.Context(),
		initialized.Tasks[0].ReservationOID, expected, 1<<20))
	_, err = coordinator.InitializeChoreographyTasks(t.Context(),
		ChoreographyInitialization{BootstrapOID: bootstrapOID, Tasks: tasks,
			Budgets: []ChoreographyRuntimeBudget{{Name: "build", Capacity: 1}}})
	require.Error(t, err, "the same run cannot start another initial batch")
	require.ErrorContains(t, coordinator.Close(t.Context()), "no attached broker",
		"an initialized run without a replayed broker cannot certify cleanup")
	require.Len(t, fixture.remoteBranches(t), 6,
		"the canonical task and ledger refs remain readable for reconciliation")
}

func TestChoreographyRejectsDistinctAuthoritativeEndpointsBeforeAnyTaskRef(t *testing.T) {
	one := newMailboxFixture(t)
	two := newMailboxFixture(t)
	coordinator := NewCoordinator("run", "plan", "generation",
		LocalNode{Name: "first", Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
		[]Link{{Name: "a", Endpoint: one.endpoint}, {Name: "b", Endpoint: two.endpoint}},
		map[string]*GitMailbox{"a": one.mailbox, "b": two.mailbox}, one.signer,
		Timeouts{}, TransferLimits{MaxManifestBytes: 1 << 20}, zerolog.Nop())
	coordinator.GraphDigest = "graph"
	coordinator.AlgorithmVersion = ChoreographyGraphAlgorithmVersion
	coordinator.CanonicalEndpoint, coordinator.canonicalMailbox = one.endpoint, one.mailbox
	coordinator.Mode = "compilation"
	coordinator.ReturnEndpoint = "https://localhost:8383"
	coordinator.ReturnCertSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	coordinator.PinnedSources = &PinnedRunSources{}
	coordinator.PinnedInputs = &PinnedRunInputs{}
	coordinator.Limits.MaxPackBytes = 1 << 20
	coordinator.JoinPolicy = ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
		Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: one.endpoint},
			{Name: "a", CoordinationEndpoint: one.endpoint},
			{Name: "b", CoordinationEndpoint: one.endpoint}}}
	coordinator.Pool = NewPool(coordinator.Links, []*NodeReport{
		{OS: "linux", Arch: "amd64", Capacity: 1, AuthorityID: "11111111111111111111111111111111"},
		{OS: "linux", Arch: "amd64", Capacity: 1, AuthorityID: "22222222222222222222222222222222"},
	}, coordinator.Local, zerolog.Nop())
	err := coordinator.PrepareChoreographyBootstrap(t.Context(), CoordinatorBootstrapSources{})
	require.ErrorContains(t, err, "one canonical mailbox endpoint")
	require.Empty(t, one.remoteBranches(t))
	require.Empty(t, two.remoteBranches(t))
}
