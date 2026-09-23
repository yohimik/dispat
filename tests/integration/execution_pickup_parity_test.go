// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a consumer's pick-up of a provider it overtook works the same way
// when every build and publication runs on a worker node as when nothing is
// delegated.
//
// The plan, its owed windows included, and the refusal of a provider released
// alone at its consumer's commit (E201) are computed once on the machine that
// starts the release and fixed by the plan digest before anything is placed.
// What a node receives is an assignment of that plan, and nothing it runs adds
// a record the next plan reads: a release tag says `release <tag>` wherever
// its package was built. So the whole sequence, from the failed provider to
// the settled catch-up, is released twice, once locally and once with both
// stages of both packages pinned to a worker, and the two histories must be
// the same history.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionParityGateEnv names the file whose presence lets the provider's
// publish succeed. It lives outside every checkout and reaches the release and
// the node alike, because the publish runs on whichever of them the twin
// places it on.
const executionParityGateEnv = "DISPAT_IT_PARITY_GATE"

// executionParityTwin is one of the two releases of the fixture: the
// repository, what its scripts record, the gate, and the node when the twin
// delegates.
type executionParityTwin struct {
	rig     *executionRig
	gate    string
	workers []*executionWorker
}

// newExecutionParityTwin seeds `core` and its consumer `cli`. Every script
// records the machine it ran on; core's publish fails while the gate is shut.
// The delegating twin pins build and publish of both packages to its one node,
// which is the only difference between the twins.
func newExecutionParityTwin(t *testing.T, isDistributed bool) *executionParityTwin {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := harness.BaseFile(2, 2)
	cfg.Scripts = map[string]models.Script{
		"build":        {executionRecordingScript},
		"core-publish": {`test -f "$` + executionParityGateEnv + `" && ` + executionPublishProbe},
		"cli-publish":  {executionPublishProbe},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"core-publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"cli-publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "cli", Provider: "core"}}
	if isDistributed {
		cfg.Execution = &models.ExecutionConfig{
			SecretEnv: executionSecretEnv,
			Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
			Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30},
		}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyWorker)
	}
	repo.WriteConfigModel(cfg)
	repo.SeedPackage("packages/libs", "core")
	repo.SeedPackage("packages/apps", "cli")
	repo.Commit("feat(core,cli): bootstrap")
	twin := &executionParityTwin{rig: newExecutionRigOver(t, repo, mailbox),
		gate: filepath.Join(t.TempDir(), "provider.gate")}
	if isDistributed {
		twin.workers = []*executionWorker{twin.rig.startWorker(executionWorkerConfig(mailbox), 0,
			executionParityGateEnv+"="+twin.gate)}
	}
	return twin
}

// setGate opens or shuts the provider's publication.
func (twin *executionParityTwin) setGate(t *testing.T, isOpen bool) {
	t.Helper()
	if isOpen {
		require.NoError(t, os.WriteFile(twin.gate, nil, 0o600))
		return
	}
	require.NoError(t, os.RemoveAll(twin.gate))
}

// run is one invocation against the twin, with the lock, the secret, the
// script log and the gate in its environment.
func (twin *executionParityTwin) run(args ...string) harness.RunResult {
	return twin.rig.repo.CommandEnv(twin.rig.env(executionParityGateEnv+"="+twin.gate), args...)
}

// executionParityStep renders what one invocation came to: its exit code and
// which of the delivery diagnostics it raised, and for whom.
func executionParityStep(name string, res harness.RunResult) string {
	var raised []string
	for _, code := range []string{"E201", "W193", "W194"} {
		for _, pkg := range []string{"core", "cli"} {
			if harness.IsCodePresentForPackage(res.Events, code, pkg) {
				raised = append(raised, code+"("+pkg+")")
			}
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%s exit=%d %s", name, res.Code, strings.Join(raised, ",")))
}

// history is what a later run reads from this twin: every tag with the
// subject of its message, in name order.
func (twin *executionParityTwin) history(t *testing.T) []string {
	t.Helper()
	tags := twin.rig.repo.TagList()
	sort.Strings(tags)
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		subject := twin.rig.repo.Git("for-each-ref", "--format=%(contents:subject)", "refs/tags/"+tag)
		out = append(out, tag+" "+strings.TrimSpace(subject))
	}
	return out
}

// publishedOn is the machine each package's last publication ran on.
func (twin *executionParityTwin) publishedOn() map[string]string {
	return executionProbeValues(twin.rig, "publish")
}

// release drives the whole sequence and returns the twin's snapshot: every
// step, the plan the catch-up was planned from, and the history left behind.
func (twin *executionParityTwin) release(t *testing.T) []string {
	t.Helper()
	repo := twin.rig.repo
	var snapshot []string
	record := func(name string, res harness.RunResult) harness.RunResult {
		snapshot = append(snapshot, executionParityStep(name, res))
		return res
	}

	twin.setGate(t, true)
	record("bootstrap", twin.run())

	repo.Commit("feat(core)^: streaming\n\n---\n\nfeat(cli): own flag")
	twin.setGate(t, false)
	record("provider fails, consumer proceeds", twin.run())
	require.Equal(t, 1, repo.TagCount("cli@0.2.0"), "the consumer proceeded; tags: %v", repo.TagList())

	twin.setGate(t, true)
	scripted := len(twin.rig.runs())
	refused := record("provider alone at the consumer's commit", twin.run("--package", "core"))
	snapshot = append(snapshot, fmt.Sprintf("scripts run by the refused release: %d", len(twin.rig.runs())-scripted))
	assert.Empty(t, twin.rig.branches(), "nothing was dispatched; stdout:\n%s", refused.Stdout)

	repo.CommitEmpty("chore(core): retry the provider")
	record("provider alone after a new commit", twin.run("--package", "core"))

	planned := record("plan of the catch-up", twin.run("status"))
	for _, pkg := range []string{"core", "cli"} {
		line := harness.GraphLine(planned.Events, pkg)
		snapshot = append(snapshot, strings.TrimSpace(fmt.Sprintf("graph %s: %s %s",
			pkg, line.Str("version"), line.Str("reason"))))
	}

	record("catch-up", twin.run())
	settled := record("settled", twin.run("status", "--require-release"))
	snapshot = append(snapshot, fmt.Sprintf("settled plan releases nothing: %t",
		strings.Contains(settled.Stdout, `"releasing":0`)))
	return append(snapshot, twin.history(t)...)
}

// TestExecutionPickUpMatchesALocalRelease releases the owed pick-up twice, once
// on this machine and once with every stage on a worker, and compares what the
// two runs did and left behind.
func TestExecutionPickUpMatchesALocalRelease(t *testing.T) {
	local := newExecutionParityTwin(t, false)
	localSnapshot := local.release(t)

	distributed := newExecutionParityTwin(t, true)
	distributedSnapshot := distributed.release(t)
	replies := stopAll(t, distributed.workers)

	assert.Equal(t, localSnapshot, distributedSnapshot,
		"a delegating run picks up exactly what a local one does\nnode:\n%s", replies[0].Stdout)
	assert.Equal(t, []string{
		"bootstrap exit=0",
		"provider fails, consumer proceeds exit=1",
		"provider alone at the consumer's commit exit=1 E201(cli)",
		"scripts run by the refused release: 0",
		"provider alone after a new commit exit=0",
		"plan of the catch-up exit=0 W193(cli)",
		"graph core: 0.2.0",
		"graph cli: 0.2.0 -> 0.2.1 catch-up from core",
		"catch-up exit=0 W193(cli)",
		"settled exit=3",
		"settled plan releases nothing: true",
		"cli@0.1.0 release cli@0.1.0",
		"cli@0.2.0 release cli@0.2.0",
		"cli@0.2.1 release cli@0.2.1",
		"core@0.1.0 release core@0.1.0",
		"core@0.2.0 release core@0.2.0",
	}, localSnapshot)
	assert.Equal(t, map[string]string{"core": executionOrchestratorLabel, "cli": executionOrchestratorLabel},
		local.publishedOn(), "the local twin publishes here")
	assert.Equal(t, map[string]string{"core": executionNode, "cli": executionNode},
		distributed.publishedOn(), "the delegating twin publishes on its node, the catch-up included")
	assert.Empty(t, distributed.rig.branches(), "the run closed every coordination branch it created")
}
