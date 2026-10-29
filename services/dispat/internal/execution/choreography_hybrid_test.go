// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func hybridPolicyFixture(t *testing.T) ChoreographyGraphCarrier {
	t.Helper()
	carrier := ChoreographyGraphCarrier{
		ChoreographyRunBinding: ChoreographyRunBinding{Run: "run", Generation: "generation", PlanDigest: "pinned-plan"},
		SemanticGraphDigest:    "typed-graph", AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		Tasks: []ChoreographyTaskSpec{
			{Task: "provider:build", Kind: KindBuild, Weight: 1, Placement: PlacementAnyNode},
			{Task: "provider:publish", Kind: "publish", Weight: 1, ExecutorOwned: true, LocalAuthority: true,
				Placement: PlacementOrchestrator, Predecessors: []string{"provider:build"}},
			{Task: "consumer:build", Kind: KindBuild, Weight: 1, Placement: PlacementAnyNode,
				Predecessors: []string{"provider:publish"}},
			{Task: "relay", Virtual: true, Predecessors: []string{"consumer:build"}},
		},
	}
	require.NoError(t, BindChoreographyHybridPolicy(ChoreographyLogicalGraphInventory{
		Binding: carrier.ChoreographyRunBinding, SemanticGraphDigest: carrier.SemanticGraphDigest, Tasks: carrier.Tasks,
	}, hybridPinnedInputsFixture()))
	for index := range carrier.Tasks {
		task := &carrier.Tasks[index]
		if task.Virtual {
			continue
		}
		template := ChoreographyCompiledTemplate{Task: task.Task, Kind: task.Kind, Weight: task.Weight}
		if task.HybridSegment == HybridCompiledSegmentName {
			template.Frame = &AssignmentFrame{Commands: []string{"build"}}
		}
		digest, err := DigestChoreographyCompiledTemplate(template)
		require.NoError(t, err)
		task.TemplateDigest = digest
		carrier.Templates = append(carrier.Templates, template)
	}
	return carrier
}

func TestHybridPolicyPreservesCrossPartitionEdgesAndBindsPinnedOperations(t *testing.T) {
	carrier := hybridPolicyFixture(t)
	require.NoError(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, hybridPinnedInputsFixture(), carrier))
	require.Equal(t, HybridCompiledSegmentName, carrier.Tasks[0].HybridSegment)
	require.Equal(t, HybridInterpretedSegmentName, carrier.Tasks[1].HybridSegment)
	require.Equal(t, []string{"provider:build"}, carrier.Tasks[1].Predecessors)
	require.Equal(t, []string{"provider:publish"}, carrier.Tasks[2].Predecessors)
	require.Empty(t, carrier.Tasks[3].HybridSegment)
	require.NotEqual(t, carrier.Tasks[0].OperationPolicyDigest, carrier.Tasks[2].OperationPolicyDigest)
	carrier.PlanDigest = "different-native-and-command-policy"
	require.ErrorContains(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, hybridPinnedInputsFixture(), carrier), "pinned operation policy")
}

func TestHybridPartitionCannotBeDowngradedOrGiveAnEffectToPeers(t *testing.T) {
	for name, mutate := range map[string]func(*ChoreographyGraphCarrier){
		"missing segment": func(carrier *ChoreographyGraphCarrier) { carrier.Tasks[1].HybridSegment = "" },
		"compiled effect": func(carrier *ChoreographyGraphCarrier) { carrier.Tasks[1].HybridSegment = HybridCompiledSegmentName },
		"peer effect":     func(carrier *ChoreographyGraphCarrier) { carrier.Tasks[1].Placement = PlacementWorker },
		"missing policy":  func(carrier *ChoreographyGraphCarrier) { carrier.Tasks[1].OperationPolicyDigest = "" },
		"executable effect": func(carrier *ChoreographyGraphCarrier) {
			carrier.Templates[1].Frame = &AssignmentFrame{Commands: []string{"publish"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			carrier := hybridPolicyFixture(t)
			mutate(&carrier)
			require.Error(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, hybridPinnedInputsFixture(), carrier))
		})
	}
	carrier := hybridPolicyFixture(t)
	require.Error(t, validateChoreographyHybridPolicy(bootstrapCompilationModeName, hybridPinnedInputsFixture(), carrier))
	carrier.Tasks = slices.Clone(carrier.Tasks)
	for index := range carrier.Tasks {
		carrier.Tasks[index].HybridSegment, carrier.Tasks[index].OperationPolicyDigest = "", ""
	}
	require.Error(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, hybridPinnedInputsFixture(), carrier),
		"a hybrid bootstrap cannot consume an ordinary compilation graph")
}

func TestHybridPartitionChangesLogicalGraphIdentity(t *testing.T) {
	carrier := hybridPolicyFixture(t)
	inventory := ChoreographyLogicalGraphInventory{Binding: carrier.ChoreographyRunBinding,
		AlgorithmVersion: carrier.AlgorithmVersion, SemanticGraphDigest: carrier.SemanticGraphDigest,
		Tasks: carrier.Tasks, Templates: carrier.Templates}
	before, err := DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	inventory.Tasks = slices.Clone(inventory.Tasks)
	inventory.Tasks[1].HybridSegment = HybridCompiledSegmentName
	after, err := DigestChoreographyLogicalGraph(inventory)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestEveryModeVerifiesItsExplicitAttemptPolicyAndPickupIdentity(t *testing.T) {
	for _, mode := range []string{bootstrapCompilationModeName, bootstrapInterpretationModeName, bootstrapHybridModeName} {
		t.Run(mode, func(t *testing.T) {
			policy := ChoreographyJoinPolicy{DistributionMode: mode, MaxAttempts: 1}
			bootstrap := RunBootstrap{Mode: mode, JoinPolicy: policy}
			require.True(t, validChoreographyBootstrapAttemptPolicy(bootstrap))
			identity := newChoreographyPickupIDForMode(mode)
			require.NoError(t, ValidateChoreographyPickupID(mode, identity))
			require.NoError(t, validateChoreographySignedPickupID(policy,
				ChoreographyTaskSpec{MaxAttempts: 1}, identity))
			require.False(t, ChoreographyTaskSpec{MaxAttempts: 1}.IsSettledFailureRetryAllowed())
			require.True(t, ChoreographyTaskSpec{MaxAttempts: 2}.IsSettledFailureRetryAllowed())
			require.False(t, ChoreographyTaskSpec{MaxAttempts: 2}.IsPickupRetryAllowed(false))
			bootstrap.JoinPolicy.MaxAttempts = 0
			require.False(t, validChoreographyBootstrapAttemptPolicy(bootstrap))
		})
	}
}

// Both selected config blobs have the same native source HEAD. These native
// fields intentionally do not contribute to PlanDigest; snapshot closure must
// therefore distinguish their operation policy and complete signed graph.
func TestHybridNativeConfigClosureChangesIdentityAtSameHead(t *testing.T) {
	fixture := newMailboxFixture(t)
	plumbing := gitx.NewPlumbing(fixture.git)
	emptyTree := plumbing.MakeTree(t.Context(), nil)
	head := plumbing.CommitTree(t.Context(), emptyTree, nil, "source HEAD")
	var policies, graphs, snapshots []string
	for _, config := range []string{
		`{"publish":{"adoptPublished":{"verify":["verify-original"]}},"github":{"enabled":false}}`,
		`{"publish":{"adoptPublished":{"verify":["verify-alternate"]}},"github":{"enabled":true}}`,
	} {
		blob := plumbing.HashObject(t.Context(), strings.NewReader(config))
		tree := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeFile, Type: "blob", Name: "dispat.json", OID: blob}})
		snapshot := plumbing.CommitTree(t.Context(), tree, []string{head}, "config snapshot")
		require.NoError(t, plumbing.Err())
		inputs := hybridPinnedInputsFixture()
		inputs.Repositories[0].PlannedHead, inputs.Repositories[0].Snapshot = head, snapshot
		carrier := hybridPolicyFixture(t)
		inventory := ChoreographyLogicalGraphInventory{Binding: carrier.ChoreographyRunBinding,
			AlgorithmVersion: carrier.AlgorithmVersion, SemanticGraphDigest: carrier.SemanticGraphDigest,
			Tasks: carrier.Tasks, Templates: carrier.Templates}
		require.NoError(t, BindChoreographyHybridPolicy(inventory, inputs))
		graph, err := DigestChoreographyLogicalGraph(inventory)
		require.NoError(t, err)
		carrier.GraphDigest = graph
		bootstrap := RunBootstrap{Protocol: ChoreographyProtocolVersion, Mode: bootstrapHybridModeName,
			AlgorithmVersion: ChoreographyGraphAlgorithmVersion, ChoreographyRunBinding: carrier.ChoreographyRunBinding,
			PinnedInputs: *inputs, CoordinationEndpoint: fixture.endpoint, ReturnEndpoint: "https://localhost:8383",
			ReturnCertSHA256: hex.EncodeToString(make([]byte, 32)),
			JoinPolicy: ChoreographyJoinPolicy{DistributionMode: bootstrapHybridModeName, MaxAttempts: 1,
				AdmissionDurationSeconds: 60, Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: fixture.endpoint}}}}
		oid, err := fixture.mailbox.PrepareChoreographyBootstrap(t.Context(), ChoreographyBootstrapOptions{
			Bootstrap: &bootstrap, MaxDocumentBytes: 1 << 20, MaxTagBytes: 1 << 20})
		require.NoError(t, err)
		read, err := fixture.mailbox.ReadChoreographyBootstrap(t.Context(), oid, 1<<20, 1<<20)
		require.NoError(t, err)
		require.Equal(t, head, read.PinnedInputs.Repositories[0].PlannedHead)
		require.Equal(t, graph, read.GraphDigest)
		require.NoError(t, validateChoreographyHybridPolicy(read.Mode, &read.PinnedInputs, carrier))
		policies = append(policies, carrier.Tasks[1].OperationPolicyDigest)
		graphs, snapshots = append(graphs, graph), append(snapshots, snapshot)
	}
	require.NotEqual(t, snapshots[0], snapshots[1])
	require.NotEqual(t, policies[0], policies[1])
	require.NotEqual(t, graphs[0], graphs[1])
}

func TestHybridBootstrapRetainsLegacyCompilationRecoveryPolicy(t *testing.T) {
	// Pre-hybrid v4 readers reject unknown modes and nonzero compilation
	// MaxAttempts before accepting a run. Keeping the graph algorithm version
	// preserves old retained attempts; every new run signs positive MaxAttempts.
	legacy := RunBootstrap{Mode: bootstrapCompilationModeName}
	require.True(t, validChoreographyBootstrapAttemptPolicy(legacy))
	require.Equal(t, bootstrapCompilationModeName, legacy.JoinPolicy.pickupMode())
	for _, mode := range []string{bootstrapCompilationModeName, bootstrapHybridModeName} {
		current := RunBootstrap{Mode: mode, JoinPolicy: ChoreographyJoinPolicy{DistributionMode: mode, MaxAttempts: 1}}
		require.True(t, validChoreographyBootstrapAttemptPolicy(current))
		legacyAcceptedMode := current.Mode == bootstrapCompilationModeName || current.Mode == bootstrapInterpretationModeName
		legacyAcceptedAttempts := current.Mode == bootstrapInterpretationModeName && validChoreographyAttemptLimit(current.JoinPolicy.MaxAttempts) || current.JoinPolicy.MaxAttempts == choreographyAttemptPolicyLegacyUnspecified
		require.False(t, legacyAcceptedMode && legacyAcceptedAttempts)
	}
}

func hybridPinnedInputsFixture() *PinnedRunInputs {
	return &PinnedRunInputs{
		Repositories: []AssignmentRepository{{Snapshot: "config-and-source-snapshot", PlannedHead: "source-head", Path: ".", Branch: "main"}},
		ConfigFiles:  []PinnedConfigFile{{Path: "dispat.json"}},
		Invocation:   json.RawMessage(`{}`), Selection: json.RawMessage(`{"kind":"release"}`), Options: json.RawMessage(`{}`),
	}
}

func TestHybridOperationBindsCompletePinnedPolicyEnvelope(t *testing.T) {
	for name, mutate := range map[string]func(*PinnedRunInputs){
		"same head different config snapshot": func(inputs *PinnedRunInputs) { inputs.Repositories[0].Snapshot = "changed-native-policy-snapshot" },
		"selected config":                     func(inputs *PinnedRunInputs) { inputs.ConfigFiles[0].Path = "alternate.json" },
		"invocation":                          func(inputs *PinnedRunInputs) { inputs.Invocation = json.RawMessage(`{"quietParser":true}`) },
		"native refs": func(inputs *PinnedRunInputs) {
			inputs.NativeRefs = []PinnedNativeRef{{Name: "refs/tags/core@1.0.0", Object: "tag-object"}}
		},
		"coordinates": func(inputs *PinnedRunInputs) {
			inputs.RepositoryCoordinates = []PinnedRepositoryCoordinate{{Host: "github.com", Owner: "changed", Repo: "project"}}
		},
		"options":   func(inputs *PinnedRunInputs) { inputs.Options = json.RawMessage(`{"force":true}`) },
		"selection": func(inputs *PinnedRunInputs) { inputs.Selection = json.RawMessage(`{"kind":"other"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			carrier := hybridPolicyFixture(t)
			inputs := hybridPinnedInputsFixture()
			before := carrier.Tasks[1].OperationPolicyDigest
			inventory := ChoreographyLogicalGraphInventory{Binding: carrier.ChoreographyRunBinding, SemanticGraphDigest: carrier.SemanticGraphDigest, AlgorithmVersion: carrier.AlgorithmVersion, Tasks: carrier.Tasks, Templates: carrier.Templates}
			beforeGraph, err := DigestChoreographyLogicalGraph(inventory)
			require.NoError(t, err)
			mutate(inputs)
			require.Error(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, inputs, carrier), "a bootstrap with different policy must not match the signed graph")
			require.NoError(t, BindChoreographyHybridPolicy(inventory, inputs))
			require.NotEqual(t, before, carrier.Tasks[1].OperationPolicyDigest)
			afterGraph, err := DigestChoreographyLogicalGraph(inventory)
			require.NoError(t, err)
			require.NotEqual(t, beforeGraph, afterGraph)
			require.NoError(t, validateChoreographyHybridPolicy(bootstrapHybridModeName, inputs, carrier))
		})
	}
	require.Error(t, BindChoreographyHybridPolicy(ChoreographyLogicalGraphInventory{Tasks: []ChoreographyTaskSpec{{Task: "effect"}}}, nil))
}
