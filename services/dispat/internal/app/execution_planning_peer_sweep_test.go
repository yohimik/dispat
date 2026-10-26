// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestPeerSweepRecomputesCoverageAndTaskGraph(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"spaces":{"libs":{"path":["packages"]}},"scripts":{"test":"echo checked"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): first")
	anchorOID := recordGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "commit", "-qm", "feat(core): second", "--allow-empty")
	head := recordGit(t, root, "rev-parse", "HEAD")
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	first := New(root, loaded, zerolog.Nop())
	pl, err := first.computePlan(t.Context())
	require.NoError(t, err)
	opts := RunOptions{OnError: OnErrorContinue, Window: WindowOptions{Since: anchorOID}, Args: []string{"--check"}}
	_, covered, err := first.coveredSelection(t.Context(), pl, opts.Window)
	require.NoError(t, err)
	require.Equal(t, []string{"core"}, covered)
	planDigest, err := first.calculatePlanDigest(t.Context(), pl, ReleaseOptions{})
	require.NoError(t, err)
	selection, policy, err := first.pinnedSweepDocuments("test", opts, covered)
	require.NoError(t, err)
	files, err := first.pinnedConfigFiles()
	require.NoError(t, err)
	invocation, err := first.pinnedInvocation()
	require.NoError(t, err)
	tasks := []execution.ReservationTask{{Task: "core:" + execution.KindRun, Kind: execution.KindRun,
		Placement: execution.ResolveStagePlacement(execution.StageBuild,
			pl.Releases["core"].Pkg.Space.RunOnly.ResolveBuild(), false)}}
	graphDigest, err := execution.DigestSweepTasks(planDigest, "test", tasks)
	require.NoError(t, err)
	git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: head}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	inputs := execution.PinnedRunInputs{Repositories: pinned.Repositories, ConfigFiles: files,
		Invocation: invocation, Selection: selection, Options: policy,
		SinceAnchor: &execution.PinnedSinceAnchor{OID: anchorOID}}
	binding := execution.ChoreographyRunBinding{Run: "sweep", Generation: "one",
		PlanDigest: planDigest, GraphDigest: graphDigest}
	fixtureGit(t, root, "commit", "-qm", "feat(core): later", "--allow-empty")
	peer, err := recomputePeerSweep(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: graphDigest,
		SourceDirs:   map[string]string{"": root},
		MaxPackBytes: 1 << 20, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"core"}, peer.covered)
	require.Equal(t, OnErrorContinue, peer.onError)
	require.Equal(t, graphDigest, binding.GraphDigest)
	require.NoError(t, peer.close())

	var changed pinnedPlanningSelection
	require.NoError(t, json.Unmarshal(inputs.Selection, &changed))
	changed.Covered = []string{"other"}
	inputs.Selection, err = json.Marshal(changed)
	require.NoError(t, err)
	_, err = recomputePeerSweep(t.Context(), peerReleasePlanningOptions{
		Inputs: inputs, Binding: binding, SemanticGraphDigest: graphDigest,
		SourceDirs:   map[string]string{"": root},
		MaxPackBytes: 1 << 20, Log: zerolog.Nop(),
	})
	require.ErrorContains(t, err, "coverage differs")
}
