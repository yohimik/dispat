// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestPeerReleaseRecomputesFromCapturedBytesAndRefusesDifferentGraph(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"spaces":{"libs":{"path":["packages"]}}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	head := recordGit(t, root, "rev-parse", "HEAD")
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	first := New(root, loaded, zerolog.Nop())
	// Simulate a first node whose CPU-derived defaults differ from this peer's.
	// The raw captured config still has no explicit concurrency values.
	first.cfg.BuildConcurrency, first.cfg.PublishConcurrency = 7, 5
	selected, err := first.selectedPlan(t.Context(), ReleaseOptions{})
	require.NoError(t, err)
	planDigest, err := first.calculatePlanDigest(t.Context(), selected, ReleaseOptions{})
	require.NoError(t, err)
	graph, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(selected)
	require.NoError(t, err)
	selection, policy, err := first.pinnedReleaseDocuments(ReleaseOptions{})
	require.NoError(t, err)
	configFiles, err := first.pinnedConfigFiles()
	require.NoError(t, err)
	invocation, err := first.pinnedInvocation()
	require.NoError(t, err)
	git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: head}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	inputs := execution.PinnedRunInputs{Repositories: pinned.Repositories, ConfigFiles: configFiles,
		Invocation: invocation, Selection: selection, Options: policy}
	binding := execution.ChoreographyRunBinding{Run: "run", Generation: "one",
		PlanDigest: planDigest, GraphDigest: graph.Digest()}

	fixtureGit(t, root, "commit", "-qm", "feat(core): later", "--allow-empty")
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte(`{"spaces":{}}`), 0o644))
	peer, err := recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: graph.Digest(),
		SourceDirs: map[string]string{"": root}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, graph.Digest(), peer.compiled.Digest())
	require.Equal(t, head, recordGit(t, peer.app.root, "rev-parse", "HEAD"))
	budgets, err := releaseChoreographyBudgets(graph, first.cfg.BuildConcurrency, first.cfg.PublishConcurrency)
	require.NoError(t, err)
	roster := execution.ChoreographyRuntimeRoster{Nodes: []execution.ChoreographyRuntimeNode{
		{Name: "first", Platform: "linux/amd64", Capacity: 2, First: true},
		{Name: "peer", Platform: "linux/amd64", Capacity: 2},
	}, Budgets: budgets}
	specs, err := peer.interpretedReleaseSpecs(roster)
	require.NoError(t, err)
	require.Len(t, specs, len(graph.TaskShapes()))
	roster.Budgets[0].Capacity++
	_, err = peer.interpretedReleaseSpecs(roster)
	require.ErrorContains(t, err, "run budgets differ")
	require.NoError(t, peer.close())

	_, err = recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: "different",
		SourceDirs: map[string]string{"": root}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.ErrorContains(t, err, "task graph differs")

	wrongPlan := binding
	wrongPlan.PlanDigest = "different"
	_, err = recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: wrongPlan, SemanticGraphDigest: graph.Digest(),
		SourceDirs: map[string]string{"": root}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.ErrorContains(t, err, pinnedPlanningPeerPlanMismatchErrorText)

	wrongPolicy := inputs
	wrongPolicy.Options = []byte("[]")
	_, err = recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: wrongPolicy, Binding: binding, SemanticGraphDigest: graph.Digest(),
		SourceDirs: map[string]string{"": root}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.ErrorContains(t, err, pinnedPlanningPeerPolicyMismatchErrorText)
}

func TestPeerReleaseRecomputesFleetFromCapturedOwners(t *testing.T) {
	control, source, configPath := compositionReleaseFixture(t)
	loaded, err := config.Load(configPath, nil)
	require.NoError(t, err)
	workspace, err := config.ComposeWorkspaceWithPinResolver(t.Context(), loaded, configPath, control, nil, nil, nil)
	require.NoError(t, err)
	first := NewWorkspace(control, loaded, workspace, zerolog.Nop())
	first.planningRemotes = map[string]planningRemoteCoordinate{"source": {}}
	selected, err := first.selectedPlan(t.Context(), ReleaseOptions{})
	require.NoError(t, err)
	planDigest, err := first.calculatePlanDigest(t.Context(), selected, ReleaseOptions{})
	require.NoError(t, err)
	graph, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(selected)
	require.NoError(t, err)
	selection, policy, err := first.pinnedReleaseDocuments(ReleaseOptions{})
	require.NoError(t, err)
	files, err := first.pinnedConfigFiles()
	require.NoError(t, err)
	invocation, err := first.pinnedInvocation()
	require.NoError(t, err)
	open := func(dir string) *gitx.LocalGitx { return &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()} }
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources: []execution.Source{
			{Name: config.ControlRepository, Path: ".", Dir: control,
				Head: recordGit(t, control, "rev-parse", "HEAD")},
			{Name: "source", Path: "source", Dir: source,
				Head: recordGit(t, source, "rev-parse", "HEAD")},
		},
		OpenRepository: func(dir string) *gitx.LocalGitx { return open(dir) },
		Store:          open(control), Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	inputs := execution.PinnedRunInputs{Repositories: pinned.Repositories, ConfigFiles: files,
		RepositoryCoordinates: first.pinnedRepositoryCoordinates(), Invocation: invocation,
		Selection: selection, Options: policy, RootRepository: config.ControlRepository}
	binding := execution.ChoreographyRunBinding{Run: "fleet-run", Generation: "one",
		PlanDigest: planDigest, GraphDigest: graph.Digest()}
	peer, err := recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: graph.Digest(),
		SourceDirs: map[string]string{config.ControlRepository: control, "source": source}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, graph.Digest(), peer.compiled.Digest())
	require.Equal(t, selected.Releases["lib"].Next.String(), peer.plan.Releases["lib"].Next.String())
	require.NoError(t, peer.close())

	inputs.RepositoryCoordinates = nil
	_, err = recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: graph.Digest(),
		SourceDirs: map[string]string{config.ControlRepository: control, "source": source}, Log: zerolog.Nop(),
		MaxPackBytes: 1 << 20,
	})
	require.ErrorContains(t, err, "coordinate")
}
