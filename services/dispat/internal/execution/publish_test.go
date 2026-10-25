// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Worker stage inputs, deadlines and rejection of legacy publication tasks.

import (
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestRemoteStageReceivesOnlyItsGraphAncestorOutputSets(t *testing.T) {
	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q")
	runGitIn(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "--allow-empty", "-m", "admitted")
	store := &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}
	commit := strings.TrimSpace(runGitIn(t, dir, "rev-parse", "HEAD"))
	c := &Coordinator{outputs: newOutputRegistry(), Links: []Link{{Name: "worker-b", Endpoint: "mailbox"}},
		dispatch: Dispatch{Inputs: func(string) []InputPackage { return nil }, Store: store}}
	for _, stage := range []string{"generate", "transform", "unrelated"} {
		c.outputs.rememberStage("core", stage, &admittedOutputs{
			node:  "worker-a",
			store: store, commit: commit, manifest: admittedManifest{
				task: "core:" + stage, packageName: "core", digest: "digest-" + stage,
			},
		})
	}
	request := release.StageRequest{Release: &plan.Release{Pkg: &model.Package{
		Name: "core", Repository: "repo", Space: &model.Space{},
	}}, Descriptor: release.Stage{OwnOutputStages: []string{"generate", "transform"}}}
	inputs, err := c.resolvePublishInputs(t.Context(), "worker-b", request,
		[]Source{{Name: "repo", Path: "repo"}}, "pkg")
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	assert.Equal(t, []string{"core:generate", "core:transform"}, []string{inputs[0].Task, inputs[1].Task})
	assert.Equal(t, "repo/pkg", inputs[0].Path)
	assert.Equal(t, commit, inputs[1].Commit)
	assert.Empty(t, inputs[1].Branch, "the signed assignment carries the result commit directly")
}

// TestEveryAssignmentBoundsItself: the node enforces the run's own wait,
// whatever the work is. A publication that outlived it would be acting on an
// authorization the run has written off, and a build that outlived it would be
// holding a machine for a run that is no longer listening: an orchestrator
// whose network went away can end neither, so the bound travels and the node
// applies it to every kind (§28.6).
func TestEveryAssignmentBoundsItself(t *testing.T) {
	assert.Equal(t, 900, resolveTaskDeadlineSeconds(15*time.Minute))
	assert.Equal(t, 0, resolveTaskDeadlineSeconds(0))
}

// TestAWorkerRefusesPublication: even signed legacy messages have no effect.
func TestAWorkerRefusesPublication(t *testing.T) {
	worker := &Worker{Node: "build-a", Seen: newSeenSetForTest(t)}
	branch := "dispat-worker-build-a-20260922-publish-abc"
	header := Header{Protocol: ProtocolVersion, Kind: KindPublish, Run: "run-1",
		Node: "build-a", Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)}

	assert.Equal(t, ReasonPermit,
		worker.checkAssignment(Assignment{Header: header}, ChainTip{Branch: branch}))
	assert.Equal(t, ReasonPermit, worker.checkAssignment(
		Assignment{Header: header, Permits: AssignmentPermits{Publish: true}},
		ChainTip{Branch: branch}), "even a legacy publication permit is refused")
	header.Kind = KindBuild
	assert.Empty(t, worker.checkAssignment(Assignment{Header: header}, ChainTip{Branch: branch}),
		"a build needs no permit: running its commands is the whole of what it does")
	assert.Equal(t, ReasonPermit, worker.checkAssignment(
		Assignment{Header: header, Permits: AssignmentPermits{NativeRefs: true}}, ChainTip{Branch: branch}))
	header.Kind = KindStage
	assert.Equal(t, ReasonPermit, worker.checkAssignment(
		Assignment{Header: header, Stage: StagePublish}, ChainTip{Branch: branch}),
		"a generic stage cannot name the built-in publication stage")
}

// newSeenSetForTest opens a seen-set in a folder of the test's own.
func newSeenSetForTest(t *testing.T) *SeenSet {
	t.Helper()
	seen, err := LoadSeenSet(t.TempDir()+"/seen.json", time.Now())
	require.NoError(t, err)
	return seen
}

// TestAnOutputConsumerPrefersTheProducerNode: the preference is a preference. The
// named node wins while it has room, and a busy one is passed over rather than
// waited for, because waiting trades a certain delay for a possible copy.
func TestAnOutputConsumerPrefersTheProducerNode(t *testing.T) {
	pool := newTestPool(linuxNode(1), linuxNode(1))

	first, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "b-node")
	require.NoError(t, err)
	assert.Equal(t, "b-node", first.Node, "the node the package was built on took it")

	second, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "b-node")
	require.NoError(t, err)
	assert.Equal(t, "a-node", second.Node, "and a busy preference is passed over, not waited for")
}
