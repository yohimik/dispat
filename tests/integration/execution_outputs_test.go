// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, third part: the build outputs of one package reaching the machine
// that builds the next one (CCME §28.5, §28.7).
//
// The fixture is the specification's own example. `assets` builds a `dist`
// folder that Git ignores; `ui` and `docs` refuse to build without the exact
// bytes `assets` produced and put a copy of them in their own `dist`; `app`
// refuses to build without both. Every package declares `buildOutputs`, so
// what is being asserted is not that the scripts ran but that the files
// travelled: `assets` is built once, on one machine, and three other builds on
// other machines read what it wrote.
//
// The refusals are asserted from the other side. A node that reports an output
// set nobody may use is played by hand, because a correct dispat will not
// produce a manifest naming `../`, a digest nothing hashes to or a submodule,
// and those are exactly the sets that must never reach a consumer.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionOutputBuild is the build script of the §28.7 example: every package
// records where it ran, every package writes a `dist`, and every consumer
// refuses to build without the exact bytes its providers wrote.
//
// The consumers read their providers through the workspace layout rather than
// through a registry, which is the whole point: `../assets/dist/bundle.js`
// exists on the consumer's machine only if something carried it there.
const executionOutputBuild = executionRecordingScript + ` &&
mkdir -p dist &&
case "$DISPAT_PACKAGE" in
  assets)
    printf 'assets %s %s\n' "$DISPAT_NEW_VERSION" "${DISPAT_EXECUTION_NODE:-orchestrator}" > dist/bundle.js ;;
  ui|docs)
    test -f ../assets/dist/bundle.js &&
    cp ../assets/dist/bundle.js dist/from-assets.txt ;;
  app)
    test -f ../ui/dist/from-assets.txt && test -f ../docs/dist/from-assets.txt &&
    cat ../ui/dist/from-assets.txt ../docs/dist/from-assets.txt > dist/combined.txt ;;
esac &&
printf '%s %s %s\n' probe-inputs "$DISPAT_PACKAGE" \
  "$(cat dist/* | tr ' ' '_' | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG"`

// executionOutputPublish is the publish script, which runs on the
// orchestrator: it asserts that the bytes a worker produced are here, because
// every stage that stays at home reads the working tree.
const executionOutputPublish = `test -d dist &&
printf '%s %s %s\n' probe-publish "$DISPAT_PACKAGE" \
  "$(cat dist/* | tr ' ' '_' | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG"`

// newExecutionOutputWorkspace seeds the §28.7 fixture: four packages, the
// dependency edges between them, `dist` declared and ignored.
func newExecutionOutputWorkspace(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	return newExecutionOutputWorkspaceWith(t,
		func(*harness.Repo) string { return executionOutputBuild }, adjust...)
}

// newExecutionOutputWorkspaceWith is the same fixture with a build script of
// the scenario's own, for the claims that need the builds to be timed.
func newExecutionOutputWorkspaceWith(t *testing.T, script func(*harness.Repo) string,
	adjust ...func(*models.File)) *executionRig {
	t.Helper()
	rig := newExecutionWorkspace(t, script,
		append([]func(*models.File){func(cfg *models.File) {
			cfg.BuildOutputs = []string{"dist"}
			cfg.Scripts["publish"] = models.Script{executionOutputPublish}
		}}, adjust...)...)
	rig.repo.WriteFile(".gitignore", "dist/\n")
	rig.repo.Commit("chore(assets,ui,docs,app): ignore the build output folders")
	return rig
}

// executionProbeValues is what one probe recorded for every package, keyed by
// package name.
func executionProbeValues(rig *executionRig, probe string) map[string]string {
	recorded := map[string]string{}
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+probe {
			recorded[run.Package] = run.Dir
		}
	}
	return recorded
}

// executionBuildCount is how many times one package's build script ran
// anywhere, which is the claim §28.7 makes about `assets`.
func executionBuildCount(rig *executionRig, packageName string) int {
	ran := 0
	for _, run := range rig.runs() {
		if run.Package == packageName && !strings.HasPrefix(run.Node, executionProbePrefix) {
			ran++
		}
	}
	return ran
}

// TestExecutionIgnoredOutputsReachConsumersOnOtherWorkers is the example
// §28.7 requires: `assets` is built once, on one machine, and `ui`, `docs` and
// `app` build on other machines from the bytes it produced, without rebuilding
// anything of their closures.
func TestExecutionIgnoredOutputsReachConsumersOnOtherWorkers(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "consumers")
	rig := newExecutionOutputWorkspaceWith(t, func(repo *harness.Repo) string {
		return executionOutputBuild + ` &&
case "$DISPAT_PACKAGE" in
  ui) ` + stageRelationRendezvous(`"$DISPAT_IT_EXECUTION_GATE.ui"`,
			`"$DISPAT_IT_EXECUTION_GATE.docs"`) + ` ;;
  docs) ` + stageRelationRendezvous(`"$DISPAT_IT_EXECUTION_GATE.docs"`,
			`"$DISPAT_IT_EXECUTION_GATE.ui"`) + ` ;;
esac &&
` + repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE", 0)
	}, func(cfg *models.File) {
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
		cfg.Execution.Workers = append(cfg.Execution.Workers,
			models.ExecutionWorkerConfig{Name: executionThirdNode, Endpoint: cfg.Execution.Workers[0].Endpoint})
	})
	workers := rig.startWorkersHolding([]string{executionNode, executionSecondNode, executionThirdNode}, 1,
		executionGateEnv+"="+gate)

	res := rig.release(executionGateEnv + "=" + gate)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, 1, executionBuildCount(rig, name),
			"%s was built exactly once in the whole run: %v", name, rig.runs())
	}
	placed := rig.nodesByPackage()
	assert.NotEqual(t, placed["ui"], placed["docs"],
		"the two consumers of assets built on two machines: %v", rig.runs())
	timeline := rig.repo.Timeline("timeline.log")
	// Each consumer waits for the other inside its build command. Completing
	// the release proves both commands were live together, independent of
	// scheduling delays between their claims.
	harness.AssertSequential(t, harness.Find(t, timeline, "assets"), harness.Find(t, timeline, "ui"))

	produced := executionProbeValues(rig, "inputs")
	require.Contains(t, produced, "assets")
	bundle := produced["assets"]
	assert.Contains(t, bundle, "0.1.0", "the bytes carry the version this run wrote")
	assert.Equal(t, bundle, produced["ui"], "ui read exactly the bytes assets produced")
	assert.Equal(t, bundle, produced["docs"], "and so did docs")
	assert.Equal(t, bundle+bundle, produced["app"], "app read both consumers' copies")

	published := executionProbeValues(rig, "publish")
	assert.Equal(t, bundle, published["assets"],
		"the orchestrator's own checkout holds what the node produced")
	for _, name := range []string{"ui", "docs"} {
		assert.Equal(t, bundle, published[name], "and %s's too", name)
	}
	assert.Equal(t, bundle+bundle, published["app"])

	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
	assert.Empty(t, executionWorkerRefs(t, rig.origin), "and put none of them on the release remote")
	stopAll(t, workers)
}

// executionTestsCheck is the test stage of the scenario below: it records the
// bytes its package's folder holds and the node it ran on, and exports that
// node for the publish that follows it.
const executionTestsCheck = `test -d dist &&
printf '%s %s %s\n' probe-tests "$DISPAT_PACKAGE" \
  "$(cat dist/* | tr ' ' '_' | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG" &&
printf '%s %s %s\n' probe-tests-node "$DISPAT_PACKAGE" "${DISPAT_EXECUTION_NODE:-orchestrator}" \
  >> "$DISPAT_IT_EXECUTION_LOG" &&
echo "TESTED_ON=${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_OUTPUT"`

// TestExecutionTestFramesCheckTheBuildsOutputsOnWorkers: a test stage is
// placed the way the build is, so with the builds pinned to workers every
// test frame runs on a worker, reads exactly the bytes its own build
// produced, and what it exports reaches the publication on the orchestrator.
// Nothing is built twice for it.
func TestExecutionTestFramesCheckTheBuildsOutputsOnWorkers(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
		cfg.Scripts["check"] = models.Script{executionTestsCheck}
		cfg.Scripts["publish"] = models.Script{executionOutputPublish + ` &&
printf '%s %s %s\n' probe-tested "$DISPAT_PACKAGE" "$DISPAT_OUTPUT_TESTED_ON" >> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Test = []string{"check"}
		cfg.Spaces["libs"] = space
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)
	defer stopAll(t, workers)

	started := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL)
	res := started.WaitForDiagnostic(executionOutputFlowDiagnosticTimeout, func() {
		t.Logf("build markers at timeout: %v", rig.runs())
		for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
			t.Logf("signed ledger at timeout: kind=%s task=%s node=%s outcome=%s taskOID=%s",
				event.Kind, event.Task, event.Node, event.PackageOutcome, event.TaskOID)
		}
		for _, branch := range rig.branches() {
			if !strings.HasPrefix(branch, executionTaskBranchRefPrefix) {
				continue
			}
			t.Logf("task branch at timeout: %s %v", branch,
				executionReadChain(rig.mailbox, strings.TrimPrefix(branch, "refs/heads/")))
		}
	})

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	events := executionLateJoinLedgerEvents(t, rig.mailbox)
	for _, name := range []string{"ui", "docs", "app"} {
		versionTerminal, buildClaim := -1, -1
		for index, event := range events {
			if event.Task == name+":0:" && event.Kind == "terminal" {
				versionTerminal = index
			}
			if event.Task == name+":1:" && event.Kind == "claim-accepted" {
				buildClaim = index
			}
		}
		require.Greater(t, versionTerminal, -1, "%s version has signed first-only finality", name)
		require.Greater(t, buildClaim, versionTerminal,
			"%s build claims after its signed first-only version terminal", name)
	}
	produced := executionProbeValues(rig, "inputs")
	tested := executionProbeValues(rig, "tests")
	nodes := executionProbeValues(rig, "tests-node")
	published := executionProbeValues(rig, "tested")
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, 1, executionBuildCount(rig, name), "%s was built once: %v", name, rig.runs())
		require.Contains(t, tested, name, "%s ran its test stage", name)
		assert.Equal(t, produced[name], tested[name], "%s's tests read the bytes its build produced", name)
		assert.Contains(t, []string{executionNode, executionSecondNode}, nodes[name],
			"%s's tests ran on a worker, as its build was pinned to", name)
		assert.Equal(t, nodes[name], published[name], "%s's tests export reached its publication", name)
	}
	var testTask string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "task outcome" && event.Str("package") == "assets" &&
			event.Str("stage") == "test" {
			testTask = event.Str("task")
			break
		}
	}
	assert.NotEmpty(t, testTask, "the test frame has its own canonical task identity")
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
}

const executionOutputFlowDiagnosticTimeout = 120 * time.Second

// executionThirdNode is the third machine the §28.7 example needs: `assets`
// on one, `ui` and `docs` on two others.
const executionThirdNode = "build-c"

// executionWorkerRefs is every coordination branch a repository holds, which
// for the release remote is always none.
func executionWorkerRefs(t *testing.T, bare string) []string {
	t.Helper()
	return executionCoordinationBranches(t, bare)
}

// V3 keeps every task, receipt, and input carrier in one canonical mailbox.
// Two configured endpoints must be refused before any build; the cross-node
// carrier path is exercised by TestExecutionIgnoredOutputsReachConsumersOnOtherWorkers.
func TestExecutionRejectsDifferentCoordinationEndpointsBeforeBuild(t *testing.T) {
	second := executionMailbox(t)
	rig := newExecutionOutputWorkspaceWith(t, func(repo *harness.Repo) string {
		return executionOutputBuild + " && " +
			repo.TsmarkScript("timeline.log", "$DISPAT_PACKAGE", executionStageWindow)
	}, func(cfg *models.File) {
		cfg.Execution.Workers[1].Endpoint = "file://" + second
	})
	res := rig.release()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, diagnosticText(res), "one canonical Git mailbox")
	assert.Equal(t, 0, buildRuns(rig.repo), "no build ran with split coordination")
	assert.Empty(t, rig.repo.TagList(), "nothing was published")
	assert.False(t, remoteHoldsLock(t, rig.origin), "the release lock was given back")
	assert.Empty(t, rig.branches(), "no run refs were initialized")
	assert.Empty(t, executionMailboxBranches(t, second), "the second mailbox was untouched")
}

// executionCarrierWatch records direct result parents while a run is active.
type executionCarrierWatch struct {
	done  chan struct{}
	found chan map[string][]string
}

func newExecutionCarrierWatch(t *testing.T, mailboxes ...string) *executionCarrierWatch {
	t.Helper()
	watch := &executionCarrierWatch{done: make(chan struct{}), found: make(chan map[string][]string, 1)}
	go func() {
		seen := map[string][]string{}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.done:
				watch.found <- seen
				return
			case <-ticker.C:
				for _, mailbox := range mailboxes {
					for _, ref := range executionMailboxBranches(t, mailbox) {
						name := strings.TrimPrefix(ref, "refs/heads/")
						if strings.Contains(name, "-relay-") {
							seen["legacy-relay"] = append(seen["legacy-relay"], name)
						}
						task, sourceTasks := executionObservedAssignmentCarriers(mailbox, ref)
						if task == "" || seen[task] != nil {
							continue
						}
						seen[task] = sourceTasks
					}
				}
			}
		}
	}()
	return watch
}

// executionObservedAssignmentCarriers tolerates a ref disappearing while the
// run closes. It reads only an active assignment and its direct secondary
// result parents, then returns the producer task names found there.
func executionObservedAssignmentCarriers(mailbox, ref string) (string, []string) {
	if !strings.HasPrefix(ref, "refs/heads/dispat-task-") {
		return "", nil
	}
	listed, err := exec.Command("git", "-C", mailbox, "rev-list", "--first-parent", "--reverse", ref).Output()
	if err != nil {
		return "", nil
	}
	// A prepared frame can sign a ready-frame transition before Claim, so the
	// Assignment is not necessarily the second first-parent commit.
	var assignmentOID string
	var document []byte
	for _, oid := range strings.Fields(string(listed)) {
		candidate, readErr := exec.Command("git", "-C", mailbox, "cat-file", "blob",
			oid+":dispat/assignment.json").Output()
		if readErr == nil {
			assignmentOID, document = oid, candidate
			break
		}
	}
	if assignmentOID == "" {
		return "", nil
	}
	signature, err := exec.Command("git", "-C", mailbox, "cat-file", "blob",
		assignmentOID+":dispat/assignment.sig").Output()
	if err != nil || strings.TrimSpace(string(signature)) != executionSign("assignment", executionSecret, document) {
		return "", nil
	}
	var assignment struct {
		Kind    string `json:"kind"`
		Package struct {
			Name string `json:"name"`
		} `json:"package"`
	}
	if json.Unmarshal(document, &assignment) != nil || assignment.Package.Name == "" || assignment.Kind == "" {
		return "", nil
	}
	task := assignment.Package.Name + ":" + assignment.Kind
	commit, err := exec.Command("git", "-C", mailbox, "cat-file", "-p", assignmentOID).Output()
	if err != nil {
		return "", nil
	}
	var sources []string
	firstParent := true
	for _, line := range strings.Split(string(commit), "\n") {
		if !strings.HasPrefix(line, "parent ") {
			continue
		}
		if firstParent {
			firstParent = false
			continue
		}
		oid := strings.TrimPrefix(line, "parent ")
		blob, err := exec.Command("git", "-C", mailbox, "cat-file", "blob", oid+":dispat/result.json").Output()
		if err != nil {
			continue
		}
		signature, err := exec.Command("git", "-C", mailbox, "cat-file", "blob", oid+":dispat/result.sig").Output()
		if err != nil || strings.TrimSpace(string(signature)) != executionSign("result", executionSecret, blob) {
			continue
		}
		var result map[string]any
		if json.Unmarshal(blob, &result) != nil {
			continue
		}
		if producer, ok := result["task"].(string); ok {
			sources = append(sources, producer)
		}
	}
	return task, sources
}

func (w *executionCarrierWatch) seen() map[string][]string {
	close(w.done)
	return <-w.found
}

// TestExecutionBranchTransportCarriesEveryDeclaredOutput: no bundle service is
// configured, so the Git transport alone has to carry every kind of file a
// build really produces, byte for byte and mode for mode, out of two sibling
// roots.
func TestExecutionBranchTransportCarriesEveryDeclaredOutput(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.BuildOutputs = []string{"dist", "assets-out"}
		build := executionEveryOutputBuild
		if runtime.GOOS != "windows" {
			build += ` && if [ "$DISPAT_PACKAGE" = assets ]; then
  printf 'newline\n' > "dist/line
break.txt"
fi`
		}
		cfg.Scripts["build"] = models.Script{build}
		cfg.Scripts["publish"] = models.Script{executionEveryOutputCheck}
		executionOneWorker(cfg)
	})
	rig.repo.WriteFile(".gitignore", "dist/\nassets-out/\n")
	// A file the repository tracks inside a declared root: every checkout
	// starts with it, so the transfer has to replace a folder that is already
	// there rather than create one, the file travels as part of the root, and
	// a build that leaves it alone is not reported as writing outside what it
	// declared.
	rig.repo.WriteFile(filepath.Join("packages", "assets", "dist", "stale.txt"), "previous\n")
	rig.repo.Git("add", "-f", "--", filepath.Join("packages", "assets", "dist", "stale.txt"))
	rig.repo.Commit("chore(assets,ui,docs,app): ignore the second output root")
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	root := filepath.Join(rig.repo.Root, "packages", "assets")
	assert.Equal(t, "plain\n", readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "dist", "plain.txt")))
	assert.Equal(t, "", readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "dist", "empty.txt")),
		"an empty file is a file")
	assert.Equal(t, "wide\n",
		readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "dist", "ünï name.txt")))
	if runtime.GOOS != "windows" {
		assert.Equal(t, "newline\n",
			readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "dist", "line\nbreak.txt")),
			"a path that cannot go through git's newline-delimited hash list still carries exact bytes")
	}
	runnable, err := os.Stat(filepath.Join(root, "dist", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), runnable.Mode().Perm(), "the executable bit survived the journey")
	target, err := os.Readlink(filepath.Join(root, "dist", "latest.txt"))
	require.NoError(t, err)
	assert.Equal(t, "plain.txt", target, "a link inside its root arrived as a link")
	big, err := os.Stat(filepath.Join(root, "dist", "big.bin"))
	require.NoError(t, err)
	assert.Equal(t, int64(8<<20), big.Size(), "eight mebibytes travelled through the branch")
	assert.Equal(t, "second\n",
		readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "assets-out", "logo.svg")),
		"and so did the second declared root")
	assert.Equal(t, "previous\n",
		readRepoFile(t, rig.repo, filepath.Join("packages", "assets", "dist", "stale.txt")),
		"a tracked file inside a declared root travels with it, and the folder that was already "+
			"there was replaced by the one that came back")
	assert.Empty(t, asideLeftoverNames(t, root), "and nothing was moved aside and left")
	_, isStray := executionLine(res,
		"the task wrote tracked files outside what it declared, and they are not admitted")
	assert.False(t, isStray,
		"a tracked file inside a declared root is carried on purpose, not a stray write\nstdout:\n%s",
		res.Stdout)
	stopAll(t, []*executionWorker{worker})
}

// A declared root can itself be one file. The receiver must stage that file
// without first making a directory at its name, replace an older tracked file
// of the same name, and carry the new bytes into later workers' checkouts.
func TestExecutionFileOutputRootReplacesAFileAndFeedsConsumers(t *testing.T) {
	rig := newExecutionWorkspace(t, func(*harness.Repo) string {
		return executionRecordingScript + ` &&
case "$DISPAT_PACKAGE" in
  assets) printf 'bundle:%s\n' "$DISPAT_NEW_VERSION" > bundle.txt ;;
  ui|docs) test -f ../assets/bundle.txt && cp ../assets/bundle.txt bundle.txt ;;
  app) test -f ../ui/bundle.txt && test -f ../docs/bundle.txt &&
       cat ../ui/bundle.txt ../docs/bundle.txt > bundle.txt ;;
esac &&
printf 'probe-file-inputs %s %s\n' "$DISPAT_PACKAGE" "$(cat bundle.txt | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG"`
	}, func(cfg *models.File) {
		cfg.BuildOutputs = []string{"bundle.txt"}
		cfg.Scripts["publish"] = models.Script{
			`test -f bundle.txt && printf 'probe-file-publish %s %s\n' "$DISPAT_PACKAGE" "$(cat bundle.txt | tr '\n' '+')" >> "$DISPAT_IT_EXECUTION_LOG"`,
		}
		executionOneWorker(cfg)
	})
	rig.repo.WriteFile(".gitignore", "bundle.txt\n")
	rig.repo.WriteFile("packages/assets/bundle.txt", "previous\n")
	rig.repo.Git("add", "-f", "--", "packages/assets/bundle.txt")
	rig.repo.Commit("chore(assets,ui,docs,app): track the previous file output")
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0)

	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assets := "bundle:0.1.0+"
	for _, packageName := range []string{"assets", "ui", "docs"} {
		assert.Equal(t, assets, executionProbeValues(rig, "file-inputs")[packageName])
		assert.Equal(t, assets, executionProbeValues(rig, "file-publish")[packageName],
			"the orchestrator published the file installed from the worker")
	}
	assert.Equal(t, assets+assets, executionProbeValues(rig, "file-inputs")["app"])
	assert.Equal(t, assets+assets, executionProbeValues(rig, "file-publish")["app"])
	assert.Equal(t, "bundle:0.1.0\n", readRepoFile(t, rig.repo, "packages/assets/bundle.txt"),
		"the single-file root replaced the tracked previous file")
	assert.Empty(t, asideLeftoverNames(t, rig.repo.Path("packages", "assets")))
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionOutputInstallRollsBackEarlierRoots: a node produced a complete
// two-root set, but the orchestrator's destination gained an ignored file or
// a link at the parent of the second root. Installing that root must fail
// after the first was replaced, restore the first root, write nothing through
// a link outside the checkout, and publish nothing.
func TestExecutionOutputInstallRollsBackEarlierRoots(t *testing.T) {
	for _, linkedParent := range []bool{false, true} {
		name := "file parent"
		if linkedParent {
			name = "linked parent"
		}
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "published")
			var outside string
			if linkedParent {
				outside = t.TempDir()
			}
			rig := newExecutionOutputWorkspaceWith(t, func(repo *harness.Repo) string {
				assetDir := repo.Path("packages", "assets")
				block := fmt.Sprintf("printf 'blocking parent\\n' > %q", filepath.Join(assetDir, "z-assets"))
				if linkedParent {
					block = fmt.Sprintf("ln -s %q %q", outside, filepath.Join(assetDir, "z-assets"))
				}
				return "set -eu; mkdir -p a-dist z-assets/nested; " +
					"printf new > a-dist/new.txt; printf new > z-assets/nested/new.txt; " +
					fmt.Sprintf("if [ \"$DISPAT_PACKAGE\" = assets ]; then mkdir -p %q; "+
						"printf 'old\\n' > %q; rm -rf %q; %s; fi",
						filepath.Join(assetDir, "a-dist"), filepath.Join(assetDir, "a-dist", "old.txt"),
						filepath.Join(assetDir, "z-assets"), block)
			}, func(cfg *models.File) {
				cfg.BuildOutputs = []string{"a-dist", "z-assets/nested"}
				cfg.Scripts["publish"] = models.Script{
					`[ "$DISPAT_PACKAGE" != assets ] || ` + fmt.Sprintf("printf published > %q", marker),
				}
				executionOneWorker(cfg)
			})
			rig.repo.WriteFile(".gitignore", "a-dist/\npackages/assets/z-assets\n")
			rig.repo.Commit("chore(assets,ui,docs,app): ignore the output roots")
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "assets"),
				"the output installation failed as an integrity prerequisite")
			assert.Contains(t, res.Stdout+res.Stderr, "destination-component",
				"the second root was refused at its destination, after the worker produced both roots")
			assert.Equal(t, "old\n", readRepoFile(t, rig.repo, "packages/assets/a-dist/old.txt"))
			assert.NoFileExists(t, rig.repo.Path("packages", "assets", "a-dist", "new.txt"))
			if linkedParent {
				assert.NoDirExists(t, filepath.Join(outside, "nested"), "the output stayed within the checkout")
			} else {
				assert.Equal(t, "blocking parent\n", readRepoFile(t, rig.repo, "packages/assets/z-assets"))
			}
			assert.Empty(t, asideLeftoverNames(t, rig.repo.Path("packages", "assets")))
			assert.NoFileExists(t, marker, "assets cannot publish a partially installed output set")
			assert.NotContains(t, executionReleaseTags(rig), "assets@0.1.0", "assets was not recorded")
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// TestExecutionOutputInstallPermissionFailureCanRetry: denying a move of an
// existing root or the final rename of a new root leaves the old output in
// place. Independent work can release, and repairing the destination permits
// one publication of the failed package on the next run.
func TestExecutionOutputInstallPermissionFailureCanRetry(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		isExistingRootMoveBlocked bool
	}{
		{name: "existing root cannot move aside", isExistingRootMoveBlocked: true},
		{name: "new root cannot be installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "published.log")
			injected := filepath.Join(t.TempDir(), "fault-injected")
			rig := newExecutionPlacementRig(t, []string{"assets", "spare"},
				func(repo *harness.Repo) string {
					assetDir := repo.Path("packages", "assets")
					blocked := filepath.Join(assetDir, "z-assets")
					if tc.isExistingRootMoveBlocked {
						blocked = assetDir
					}
					removeNewRoot := ""
					if !tc.isExistingRootMoveBlocked {
						removeNewRoot = fmt.Sprintf("rm -rf %q; mkdir -p %q; ",
							filepath.Join(assetDir, "z-assets", "nested"), filepath.Join(assetDir, "z-assets"))
					}
					return "set -eu; mkdir -p a-dist z-assets/nested; " +
						"printf new > a-dist/new.txt; printf new > z-assets/nested/new.txt; " +
						fmt.Sprintf("if [ \"$DISPAT_PACKAGE\" = assets ] && [ ! -f %q ]; then "+
							"mkdir -p %q; printf 'old\\n' > %q; %schmod 555 %q; touch %q; fi",
							injected, filepath.Join(assetDir, "a-dist"), filepath.Join(assetDir, "a-dist", "old.txt"),
							removeNewRoot, blocked, injected)
				}, func(cfg *models.File) {
					cfg.BuildOutputs = []string{"a-dist", "z-assets/nested"}
					cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
					cfg.Scripts["publish"] = models.Script{fmt.Sprintf(
						"printf '%%s\\n' \"$DISPAT_PACKAGE\" >> %q", marker)}
					cfg.Execution.Concurrency = models.Int(2)
				})
			rig.repo.WriteFile(".gitignore", "a-dist/\nz-assets/\n")
			rig.repo.Commit("chore(assets,spare): ignore build outputs")
			blocked := rig.repo.Path("packages", "assets", "z-assets")
			if tc.isExistingRootMoveBlocked {
				blocked = rig.repo.Path("packages", "assets")
			}
			t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
			probeDir := t.TempDir()
			require.NoError(t, os.Chmod(probeDir, 0o555))
			t.Cleanup(func() { _ = os.Chmod(probeDir, 0o755) })
			probeErr := os.WriteFile(filepath.Join(probeDir, "permission-probe"), []byte("x"), 0o600)
			if probeErr == nil {
				t.Skip("this user can write into a 0555 directory")
			}
			require.True(t, os.IsPermission(probeErr), "fixture must fail due to directory permissions: %v", probeErr)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(2) }), 0)

			failed := rig.release()

			require.Equal(t, 1, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.True(t, harness.IsCodePresentForPackage(executionEvents(failed), executionIntegrityCode, "assets"),
				"the write-denied root fails its own prerequisite")
			assert.Contains(t, failed.Stdout+failed.Stderr, "permission denied",
				"the refused install reached the destination filesystem")
			if tc.isExistingRootMoveBlocked {
				assert.Contains(t, failed.Stdout+failed.Stderr, "moving the previous build output root aside")
			} else {
				assert.Contains(t, failed.Stdout+failed.Stderr, "installing the build output root z-assets/nested")
			}
			assert.Equal(t, "old\n", readRepoFile(t, rig.repo, "packages/assets/a-dist/old.txt"))
			assert.NoFileExists(t, rig.repo.Path("packages", "assets", "a-dist", "new.txt"))
			assert.NoFileExists(t, rig.repo.Path("packages", "assets", "z-assets", "nested", "new.txt"),
				"the second root never exposes the worker's new bytes")
			if !tc.isExistingRootMoveBlocked {
				assert.NoDirExists(t, rig.repo.Path("packages", "assets", "z-assets", "nested"))
			}
			assert.Empty(t, asideLeftoverNames(t, rig.repo.Path("packages", "assets")))
			assert.False(t, rig.repo.IsTagged("assets@0.1.0"), "no partial installation can publish")
			assert.True(t, rig.repo.IsTagged("spare@0.1.0"),
				"independent work still releases\nstdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			firstPublications, err := os.ReadFile(marker)
			require.NoError(t, err, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			assert.Equal(t, "spare\n", string(firstPublications))

			require.NoError(t, os.Chmod(blocked, 0o755))
			retried := rig.release()

			require.Equal(t, 0, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
			assert.Equal(t, "new", readRepoFile(t, rig.repo, "packages/assets/a-dist/new.txt"))
			assert.Equal(t, "new", readRepoFile(t, rig.repo, "packages/assets/z-assets/nested/new.txt"))
			assert.ElementsMatch(t, []string{"assets@0.1.0", "spare@0.1.0"}, rig.repo.TagList())
			content, err := os.ReadFile(marker)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"spare", "assets"}, strings.Fields(string(content)),
				"each package published exactly once across the failed run and retry")
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// asideLeftoverNames is every folder an interrupted installation would have
// left beside a declared root.
func asideLeftoverNames(t *testing.T, dir string) []string {
	t.Helper()
	var left []string
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".dispat-old-") {
			left = append(left, entry.Name())
		}
	}
	return left
}

// executionEveryOutputBuild writes one of every kind of file a build really
// produces, into two declared roots.
const executionEveryOutputBuild = executionRecordingScript + ` &&
mkdir -p dist assets-out &&
if [ "$DISPAT_PACKAGE" = assets ]; then
  printf 'plain\n' > dist/plain.txt &&
  printf '#!/bin/sh\necho run\n' > dist/run.sh && chmod 755 dist/run.sh &&
  : > dist/empty.txt &&
  printf 'wide\n' > "dist/ünï name.txt" &&
  ln -sf plain.txt dist/latest.txt &&
  dd if=/dev/zero of=dist/big.bin bs=1048576 count=8 2>/dev/null &&
  printf 'second\n' > assets-out/logo.svg
fi`

// executionEveryOutputCheck is the publish stage of that scenario: it runs on
// the orchestrator and is what proves the files are here rather than only on
// the node that made them.
const executionEveryOutputCheck = `[ "$DISPAT_PACKAGE" != assets ] || {
  test -x dist/run.sh && test -L dist/latest.txt && test -s dist/big.bin
}`

// TestExecutionDefectiveOutputFailsThePrerequisite: a build that did not
// produce what it declared fails its own package, and nothing is substituted
// for the bytes it owed: every consumer that needed them fails too, on the
// node it was placed on, and the run publishes nothing at all.
//
// The consumers are still placed, because each of them has changes of its own
// and dispat releases a package that has its own work even when a provider
// failed. What §28.5 requires is the other half: the missing outputs are never
// replaced by a registry, by a rebuild or by whatever the checkout held, so a
// consumer whose build reads its provider's `dist` finds nothing there.
func TestExecutionDefectiveOutputFailsThePrerequisite(t *testing.T) {
	rig := newExecutionOutputWorkspaceWith(t, func(*harness.Repo) string {
		// The build of `assets` writes its declared root and then takes it
		// away again, which is the shape of a build that promised more than
		// it produced.
		return executionOutputBuild + ` && { [ "$DISPAT_PACKAGE" != assets ] || rm -rf dist; }`
	}, executionOneWorker)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "assets"),
		"the package whose outputs are missing fails with the integrity code\nstdout:\n%s", res.Stdout)
	assert.Equal(t, 1, executionBuildCount(rig, "assets"), "and was not tried again")
	produced := executionProbeValues(rig, "inputs")
	for _, name := range []string{"ui", "docs", "app"} {
		assert.NotContains(t, produced, name,
			"%s never got as far as reading a provider's outputs: %v", name, rig.runs())
	}
	assert.ElementsMatch(t, []string{"build", "build", "build", "build"}, executionFailedStages(res),
		"every package failed at its build stage\nstdout:\n%s", res.Stdout)
	assert.Empty(t, rig.repo.TagList(), "nothing was published")

	// The release obligation is preserved: the next plan still names them.
	status := rig.repo.CommandEnv(rig.env(), "status", "--log-format", "json")
	require.Equal(t, 0, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	planned := map[string]bool{}
	for _, event := range executionEvents(status) {
		if strings.Contains(event.Str("message"), "changed") {
			planned[event.Package()] = true
		}
	}
	for _, name := range executionWorkspacePackages {
		assert.True(t, planned[name], "%s is still planned after the failed run: %s", name, status.Stdout)
	}
	stopAll(t, []*executionWorker{worker})
}

// TestExecutionMovedBranchTipIsNotFollowed: an admitted output set is read at
// the object it was admitted at. A garbage commit pushed on top of the
// producer's branch after its result was admitted changes nothing for the
// consumer, which still builds from the bytes the run accepted.
//
// The consumer is held at a version hook until the branch has been moved, so
// the scenario is about what the consumer reads rather than about which of two
// machines was quicker.
func TestExecutionMovedBranchTipIsNotFollowed(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "let-the-consumer-go")
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		space := cfg.Spaces["libs"]
		space.Flow.PostVersion = []string{"gate"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["gate"] = models.Script{executionGateScript}
		cfg.Concurrency = []int{1, 1}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0)
	vandal := newExecutionTipVandal(t, rig, gate)

	res := rig.release(executionGateEnv + "=" + gate)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Positive(t, vandal.moved(), "a branch carrying an admitted result was moved under the run")
	produced := executionProbeValues(rig, "inputs")
	assert.Equal(t, produced["assets"], produced["ui"],
		"the consumer read the admitted bytes rather than whatever the branch holds now")
	stopAll(t, []*executionWorker{worker})
}

// executionGateEnv names the file a held package waits for, and
// executionGateScript is the hook that waits: only the consumer waits, and it
// waits at the version stage, which is the last thing that happens on the
// orchestrator before its build is placed anywhere.
const executionGateEnv = "DISPAT_IT_EXECUTION_GATE"

const executionGateScript = `[ "$DISPAT_PACKAGE" != ui ] || ` +
	`while [ ! -f "$DISPAT_IT_EXECUTION_GATE" ]; do sleep 0.2; done`

// executionTipVandal waits until the producer's outputs have been admitted,
// pushes a garbage commit on top of the branch they were admitted from, and
// only then lets the consumer go. Moving a branch is the one thing a party
// with write access to a mailbox can do without the signing secret.
//
// Admission is observed through the producer's own tag: a package is tagged
// after its outputs have been admitted and published, so a tag is proof that
// the run has already read what it was going to read.
type executionTipVandal struct {
	done   chan struct{}
	counts chan int
}

func newExecutionTipVandal(t *testing.T, rig *executionRig, gate string) *executionTipVandal {
	t.Helper()
	vandal := &executionTipVandal{done: make(chan struct{}), counts: make(chan int, 1)}
	go func() {
		moved := 0
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-vandal.done:
				vandal.counts <- moved
				return
			case <-ticker.C:
				if moved > 0 || !rig.repo.IsTagged("assets@0.1.0") {
					continue
				}
				for _, ref := range executionMailboxBranches(t, rig.mailbox) {
					if !executionCompletedAssetsBuild(rig.mailbox, ref) {
						continue
					}
					tip := strings.TrimSpace(bareGit(t, rig.mailbox, "rev-parse", ref))
					garbage := strings.TrimSpace(bareGit(t, rig.mailbox, "commit-tree",
						tip+"^{tree}", "-p", tip, "-m", "dispat transport vandal"))
					bareGit(t, rig.mailbox, "update-ref", ref, garbage, tip)
					moved++
				}
				if moved > 0 {
					require.NoError(t, os.WriteFile(gate, []byte("go\n"), 0o644))
				}
			}
		}
	}()
	return vandal
}

func (v *executionTipVandal) moved() int {
	close(v.done)
	return <-v.counts
}

const (
	executionAssignmentDocumentPath = "dispat/assignment.json"
	executionDecisionDocumentPath   = "dispat/choreography-decision.json"
	executionTaskBranchRefPrefix    = "refs/heads/dispat-task-"
	executionAssetsTaskPrefix       = "assets:"
	executionBuildAssignmentKind    = "build"
)

// executionCompletedAssetsBuild observes the exact v3 build attempt after its
// signed terminal decision. The mutator never moves a reservation or an
// in-flight claim, whose effects could still be running.
func executionCompletedAssetsBuild(mailbox, ref string) bool {
	if !strings.HasPrefix(ref, executionTaskBranchRefPrefix) {
		return false
	}
	listed, err := exec.Command("git", "-C", mailbox, "rev-list", "--first-parent", "--reverse", ref).Output()
	if err != nil {
		return false
	}
	build, decided := false, false
	for _, commit := range strings.Fields(string(listed)) {
		entries, err := exec.Command("git", "-C", mailbox, "ls-tree", "-r", "--name-only", commit).Output()
		if err != nil {
			return false
		}
		paths := "\n" + string(entries)
		if strings.Contains(paths, "\n"+executionDecisionDocumentPath+"\n") {
			decided = true
		}
		if !strings.Contains(paths, "\n"+executionAssignmentDocumentPath+"\n") {
			continue
		}
		document, err := exec.Command("git", "-C", mailbox, "cat-file", "blob",
			commit+":"+executionAssignmentDocumentPath).Output()
		if err != nil {
			return false
		}
		var assignment map[string]any
		if json.Unmarshal(document, &assignment) != nil {
			return false
		}
		task, _ := assignment["task"].(string)
		kind, _ := assignment["kind"].(string)
		build = build || strings.HasPrefix(task, executionAssetsTaskPrefix) && kind == executionBuildAssignmentKind
	}
	return build && decided
}

// executionCarries reports whether one branch has carried a message of this
// kind, without failing the test for a branch that has not.
func executionCarries(t *testing.T, mailbox, branch, kind string) bool {
	t.Helper()
	for _, carried := range executionReadChain(mailbox, branch) {
		if carried == kind {
			return true
		}
	}
	return false
}

// TestExecutionOutputGitFaults: the git invocations an output transfer is made
// of, each failed in turn: staging the declared roots, writing the tree,
// reading the objects to digest them on the node, and reading them again to
// install them here. Each fails the package it belongs to with the integrity
// code, the run publishes nothing, and the node carries on serving.
const executionOutputGitFaultWait = 60 * time.Second
const executionOutputGitFaultWorkerDiagnosticWait = 5 * time.Second
const executionOutputBatchPattern = "*cat-file --batch"

func TestExecutionOutputGitFaults(t *testing.T) {
	for name, tc := range map[string]struct {
		pattern  string
		onWorker bool
	}{
		"the outputs cannot be hashed":         {pattern: "*hash-object -w --no-filters --stdin-paths*", onWorker: true},
		"the outputs cannot be staged":         {pattern: "*update-index -z --add --index-info*", onWorker: true},
		"the output tree cannot be written":    {pattern: "*write-tree*", onWorker: true},
		"the outputs cannot be read to digest": {pattern: executionOutputBatchPattern, onWorker: true},
		"the outputs cannot be installed here": {pattern: executionOutputBatchPattern, onWorker: false},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionOutputWorkspace(t, executionOneWorker)
			selected := harness.GitFault{Pattern: tc.pattern, Onward: true, Nth: 1}
			if tc.pattern == executionOutputBatchPattern {
				// The first producer's exact output distinguishes its reader
				// from concurrent signed-message readers in the same Git store.
				selected.FirstInputLine = gitIn(t, rig.repo.Root,
					"assets 0.1.0 "+executionNode+"\n", "hash-object", "--stdin")
			}
			fault := harness.NewGitFault(t, selected)
			workerEnv := []string{}
			releaseEnv := []string{}
			if tc.onWorker {
				workerEnv = fault.Env()
			} else {
				releaseEnv = fault.Env()
			}
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0, workerEnv...)

			started := rig.repo.StartReleaseEnv(rig.env(releaseEnv...), "release",
				"--return-endpoint", rig.returnURL)
			res := started.WaitForDiagnostic(executionOutputGitFaultWait, func() {
				t.Logf("signed output-fault ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
				stopped := worker.proc.KillForDiagnostic(executionOutputGitFaultWorkerDiagnosticWait)
				t.Logf("worker stdout:\n%s\nstderr:\n%s", stopped.Stdout, stopped.Stderr)
			})

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Equal(t, 1, executionBuildCount(rig, "assets"),
				"the producer's build completed before its output operation failed")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
				"the affected package is refused with the integrity code\nstdout:\n%s", res.Stdout)
			assert.Empty(t, executionReleaseTags(rig), "nothing was published")
			reply := stopAll(t, []*executionWorker{worker})[0]
			assert.Equal(t, 0, reply.Code, "the node carries on serving after its git failed")
			assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
		})
	}
}

// A named pipe in a declared output root cannot be copied as a regular file:
// reading it would hang until another process opened the other end. Refuse the
// producer's result before a consumer or publish stage can use that output.
func TestExecutionProducerRefusesNamedPipeOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the build fixture uses the POSIX mkfifo command")
	}
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{`mkdir -p dist && if [ "$DISPAT_PACKAGE" = assets ]; then mkfifo dist/pipe; else printf 'ordinary\n' > dist/file; fi`}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()
	reply := stopAll(t, []*executionWorker{worker})[0]
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 0, reply.Code, "the worker remains available after refusing the output")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode))
	assert.Contains(t, reply.Stdout+reply.Stderr, "neither a file, a link nor a folder")
	assert.NotContains(t, executionReleasedPackages(res), "assets",
		"the rejected producer output cannot be published")
	assert.NotContains(t, rig.repo.TagList(), "assets@0.1.0")
	assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
}

// A build output can contain a complete nested Git repository. Git records
// that folder as a gitlink rather than its files, so accepting it as a build
// artifact would silently omit the bytes the consumer expects. The producer
// must refuse the capture before publication.
func TestExecutionProducerRefusesNestedRepositoryOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the nested repository build fixture uses a POSIX shell script")
	}
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.Scripts["build"] = models.Script{`mkdir -p dist && if [ "$DISPAT_PACKAGE" = assets ]; then
  git -C dist init -q nested
  git -C dist/nested -c user.name=build -c user.email=build@example.test commit --allow-empty -q -m nested
else printf 'ordinary\n' > dist/file; fi`}
		executionOneWorker(cfg)
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()
	reply := stopAll(t, []*executionWorker{worker})[0]
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 0, reply.Code)
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode))
	assert.Contains(t, reply.Stdout+reply.Stderr, "gitlink")
	assert.NotContains(t, executionReleasedPackages(res), "assets")
	assert.NotContains(t, rig.repo.TagList(), "assets@0.1.0")
	assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
}

// A successful hash-object exit is not enough to describe every captured
// output. If Git answers fewer object IDs than the worker sent paths, no
// incomplete tree may be offered to the release orchestrator.
func TestExecutionIncompleteHashReplyCannotPublish(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, executionOneWorker)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*hash-object -w --no-filters --stdin-paths*", Output: "\n",
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)

	res := rig.release()
	reply := stopAll(t, []*executionWorker{worker})[0]
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 0, reply.Code)
	assert.Positive(t, fault.Matches())
	assert.Contains(t, reply.Stdout+reply.Stderr, "answered 0 objects")
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode))
	assert.NotContains(t, executionReleasedPackages(res), "assets")
	assert.NotContains(t, rig.repo.TagList(), "assets@0.1.0")
	assert.NotEmpty(t, rig.branches(), "signed task and ledger refs remain for recovery")
}

// executionFakeWorker plays a node: it answers the probe a preflight sends and
// then pushes whatever result a scenario wants for the build it is offered.
//
// It exists because a correct dispat never produces the results these
// scenarios are about. A manifest naming `../`, a digest nothing hashes to, a
// tree holding a submodule: each is what somebody who had taken over a machine
// would report, and the only way to put one on a mailbox is to write it.
type executionFakeWorker struct {
	t       *testing.T
	mailbox string
	node    string
	// craft turns the manifest of a correct capture into the one this
	// scenario is about, and may also change the tree it describes.
	craft func(*executionCraftedOutputs)
	// isSigned is false for the one scenario whose point is a result nobody
	// signed.
	isSigned bool
	// mangle rebinds the identities of an otherwise correct result, which is
	// how a receipt bound to another run, another attempt or another node is
	// offered. Nil leaves the result bound to the attempt it answers.
	mangle func(executionOrderedJSON) executionOrderedJSON
	// resultSecret signs the result of a build, and nothing else. It is
	// separate from the secret every other message is signed with because a
	// scenario about a receipt nobody could have written still needs this node
	// to pass preflight: a probe answered with the wrong secret is a run that
	// never dispatches anything, which is a scenario about preflight instead.
	resultSecret string
	// Claim-only scenarios vary the first worker reply and stop before a
	// result, so the coordinator must decide that claim on its own merits.
	claimMangle func(executionOrderedJSON) executionOrderedJSON
	claimSecret string
	isClaimOnly bool
	// Ready-only scenarios report a publisher's proposed irreversible work,
	// then wait to see whether the coordinator authorizes that exact proposal.
	readyMangle func(executionOrderedJSON) executionOrderedJSON
	readySecret string
	isReadyOnly bool
	// answered is closed once a build has been answered, so a scenario can
	// wait for the thing it is about.
	answered chan struct{}
	once     sync.Once
	stop     chan struct{}
	waiting  sync.WaitGroup
}

// executionCraftedOutputs is the result one fake build reports: the ordered
// manifest fields the digest is taken over, and the tree entries the outputs
// folder holds.
type executionCraftedOutputs struct {
	manifest executionOrderedJSON
	// entries are the files of the crafted output tree, as
	// "<mode> <blob|commit> <content>" keyed by the path inside the package.
	entries map[string]executionTreeFile
	// isDigestStale leaves the manifest digest as it was rather than
	// recomputing it after craft ran, which is how a digest nobody could have
	// produced is offered.
	isDigestStale bool
	// isOutputsOmitted reports a result that describes nothing at all, which
	// is what a node answering for a package that declares outputs must never
	// be believed about.
	isOutputsOmitted bool
}

// executionTreeFile is one entry of a crafted output tree.
type executionTreeFile struct {
	mode    string
	content string
	// oid names an object directly, for the entries whose content is not a
	// blob this scenario wrote (a submodule names a commit).
	oid string
}

// newExecutionFakeWorker starts a node that answers probes and one build.
func newExecutionFakeWorker(t *testing.T, mailbox, node string, craft func(*executionCraftedOutputs)) *executionFakeWorker {
	t.Helper()
	worker := &executionFakeWorker{t: t, mailbox: mailbox, node: node, craft: craft,
		isSigned: true, resultSecret: executionSecret,
		answered: make(chan struct{}), stop: make(chan struct{})}
	return worker
}

// serve runs the loop until the scenario stops it.
func (w *executionFakeWorker) serve() {
	w.waiting.Add(1)
	go func() {
		defer w.waiting.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		claimed := map[string]bool{}
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
				w.tick(claimed)
			}
		}
	}()
}

// close stops the loop and waits for it.
func (w *executionFakeWorker) close() {
	close(w.stop)
	w.waiting.Wait()
}

// tick answers preflight probes and activated v2 slot notices for this node.
// A reservation without an assignment is inert and never reaches answer.
func (w *executionFakeWorker) tick(claimed map[string]bool) {
	for _, ref := range executionMailboxBranches(w.t, w.mailbox) {
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if strings.HasPrefix(branch, "dispat-slot-") {
			document, err := exec.Command("git", "-C", w.mailbox, "cat-file", "blob",
				"refs/heads/"+branch+":dispat/slot.json").Output()
			if err != nil {
				continue
			}
			var notice map[string]any
			if json.Unmarshal(document, &notice) != nil {
				continue
			}
			if notice["node"] != w.node {
				continue
			}
			taskBranch, _ := notice["taskBranch"].(string)
			if taskBranch == "" || claimed[taskBranch] {
				continue
			}
			chain := executionReadChain(w.mailbox, taskBranch)
			if len(chain) != 2 || chain[0] != "reservation" || chain[1] != "assignment" {
				continue
			}
			claimed[taskBranch] = true
			w.answer(taskBranch)
			continue
		}
		if claimed[branch] || !strings.HasPrefix(branch, "dispat-worker-"+w.node+"-") ||
			executionBranchKind(branch) != "probe" {
			continue
		}
		// The run closes its branches while this loop is going, so a name
		// that is gone between the listing and the read is not work.
		chain := executionReadChain(w.mailbox, branch)
		if len(chain) != 1 || chain[0] != "assignment" {
			continue
		}
		claimed[branch] = true
		w.answer(branch)
	}
}

// answer claims one assignment and reports a result for it.
func (w *executionFakeWorker) answer(branch string) {
	assignment := executionMessage(w.t, w.mailbox, branch, "assignment")
	tip := strings.TrimSpace(bareGit(w.t, w.mailbox, "rev-parse", "refs/heads/"+branch))
	header := executionReplyHeader(assignment, w.node)
	claimDocument := executionOrderedJSON{}.with(header...).with(executionField{"assignment", tip})
	claimSecret := executionSecret
	if assignment["kind"] != "probe" {
		if w.claimMangle != nil {
			claimDocument = w.claimMangle(claimDocument)
		}
		if w.claimSecret != "" {
			claimSecret = w.claimSecret
		}
	}
	claim := w.pushSigned(branch, tip, "claim", claimDocument, "", true, claimSecret)
	if assignment["kind"] == "probe" {
		w.push(branch, claim, "result", executionOrderedJSON{}.with(header...).with(
			executionField{"assignment", tip},
			executionField{"status", "succeeded"},
			executionField{"platform", executionPlatform()},
			executionField{"report", executionNodeReport(assignment)},
		), "", true)
		return
	}
	if w.isClaimOnly {
		w.once.Do(func() { close(w.answered) })
		return
	}
	if w.isReadyOnly {
		ready := executionOrderedJSON{}.with(header...).with(
			executionField{"assignment", tip}, executionField{"claim", claim})
		if w.readyMangle != nil {
			ready = w.readyMangle(ready)
		}
		secret := executionSecret
		if w.readySecret != "" {
			secret = w.readySecret
		}
		w.pushSigned(branch, claim, "ready", ready, "", true, secret)
		w.once.Do(func() { close(w.answered) })
		return
	}
	crafted := w.buildOutputs(assignment)
	report := executionOrderedJSON{}.with(header...).with(
		executionField{"assignment", tip},
		executionField{"status", "succeeded"},
		executionField{"platform", executionPlatform()},
	)
	if !crafted.isOmitted {
		report = report.with(executionField{"outputs", crafted.manifest})
	}
	if w.mangle != nil {
		report = w.mangle(report)
	}
	w.pushSigned(branch, claim, "result", report, crafted.tree, w.isSigned, w.resultSecret)
	w.once.Do(func() { close(w.answered) })
}

// buildOutputs assembles the output tree and the manifest this scenario wants
// reported, starting from what a correct capture of one file would be.
func (w *executionFakeWorker) buildOutputs(assignment map[string]any) struct {
	manifest  executionOrderedJSON
	tree      string
	isOmitted bool
} {
	body := "one\n"
	crafted := &executionCraftedOutputs{
		entries: map[string]executionTreeFile{"dist/app.js": {mode: "100644", content: body}},
	}
	crafted.manifest = executionOrderedJSON{}.with(
		executionField{"protocol", executionProtocolVersion},
		executionField{"run", assignment["run"]},
		executionField{"planDigest", assignment["planDigest"]},
		executionField{"task", assignment["task"]},
		executionField{"attempt", assignment["attempt"]},
		executionField{"generation", assignment["generation"]},
		executionField{"node", w.node},
		executionField{"package", executionAssignmentPackage(assignment)["name"]},
		executionField{"platform", executionPlatform()},
		executionField{"roots", []any{"dist"}},
		executionField{"entries", []any{executionOrderedJSON{}.with(
			executionField{"path", "dist/app.js"},
			executionField{"type", "file"},
			executionField{"mode", "0644"},
			executionField{"size", len(body)},
			executionField{"sha256", executionDigestOf(body)},
		)}},
		executionField{"files", 1},
		executionField{"bytes", len(body)},
		executionField{"outputTree", ""},
		executionField{"manifestDigest", ""},
	)
	if w.craft != nil {
		w.craft(crafted)
	}
	tree := executionWriteTree(w.t, w.mailbox, crafted.entries)
	crafted.manifest = crafted.manifest.set("outputTree", tree)
	if !crafted.isDigestStale {
		crafted.manifest = crafted.manifest.set("manifestDigest",
			executionDigestOfBytes(executionMustMarshal(w.t, crafted.manifest.set("manifestDigest", ""))))
	}
	return struct {
		manifest  executionOrderedJSON
		tree      string
		isOmitted bool
	}{manifest: crafted.manifest, tree: tree, isOmitted: crafted.isOutputsOmitted}
}

// push writes one message onto a branch, with the crafted output tree beside
// it when there is one.
func (w *executionFakeWorker) push(branch, parent, kind string, document executionOrderedJSON,
	outputs string, isSigned bool) string {
	w.t.Helper()
	return w.pushSigned(branch, parent, kind, document, outputs, isSigned, executionSecret)
}

// pushSigned is push with the secret stated, for the one scenario whose point
// is a signature that does not verify.
func (w *executionFakeWorker) pushSigned(branch, parent, kind string, document executionOrderedJSON,
	outputs string, isSigned bool, secret string) string {
	w.t.Helper()
	body := executionMustMarshal(w.t, document)
	entries := fmt.Sprintf("100644 blob %s\t%s.json\x00",
		gitIn(w.t, w.mailbox, string(body), "hash-object", "-w", "--stdin"), kind)
	if isSigned {
		entries += fmt.Sprintf("100644 blob %s\t%s.sig\x00",
			gitIn(w.t, w.mailbox, executionSign(kind, secret, body), "hash-object", "-w", "--stdin"), kind)
	}
	inner := gitIn(w.t, w.mailbox, entries, "mktree", "-z")
	listing := fmt.Sprintf("040000 tree %s\tdispat\x00", inner)
	if outputs != "" {
		listing += fmt.Sprintf("040000 tree %s\toutputs\x00", outputs)
	}
	tree := gitIn(w.t, w.mailbox, listing, "mktree", "-z")
	commit := strings.TrimSpace(bareGit(w.t, w.mailbox, "commit-tree", tree, "-p", parent,
		"-m", "dispat transport "+kind))
	bareGit(w.t, w.mailbox, "update-ref", "refs/heads/"+branch, commit, parent)
	return commit
}

// executionReadChain is executionChain for a branch that may vanish under the
// reader: a run closes its own coordination branches while a node is still
// looking at the mailbox, and a name that is gone is not a test failure.
func executionReadChain(mailbox, branch string) []string {
	listed, err := exec.Command("git", "-C", mailbox, "rev-list",
		"--first-parent", "--reverse", "refs/heads/"+branch).Output()
	if err != nil {
		return nil
	}
	var kinds []string
	for _, commit := range strings.Fields(string(listed)) {
		entries, err := exec.Command("git", "-C", mailbox, "ls-tree", "-r", "--name-only", commit).Output()
		if err != nil {
			return nil
		}
		for _, path := range strings.Fields(string(entries)) {
			if name, isDocument := strings.CutSuffix(strings.TrimPrefix(path, "dispat/"), ".json"); isDocument {
				kinds = append(kinds, name)
			}
		}
	}
	return kinds
}

// executionReplyHeader is the header a node writes back: the work's identity
// exactly as the assignment stated it, and this node's name.
func executionReplyHeader(assignment map[string]any, node string) []executionField {
	return []executionField{
		{"protocol", executionProtocolVersion},
		{"kind", assignment["kind"]},
		{"run", assignment["run"]},
		{"planDigest", assignment["planDigest"]},
		{"task", assignment["task"]},
		{"attempt", assignment["attempt"]},
		{"generation", assignment["generation"]},
		{"node", node},
		{"branch", assignment["branch"]},
		{"issuedAt", time.Now().UTC().Format(time.RFC3339)},
	}
}

// executionPlatform is what a node says it ran on. It is this machine's, so
// that a correctly built set is not refused for the wrong reason.
func executionPlatform() executionOrderedJSON {
	return executionOrderedJSON{}.with(
		executionField{"os", runtime.GOOS},
		executionField{"arch", runtime.GOARCH},
		executionField{"dispat", "fake"},
	)
}

// executionNodeReport is what a probe is answered with: enough for preflight
// to admit the node, with the run's own ceilings echoed back.
func executionNodeReport(assignment map[string]any) executionOrderedJSON {
	limits, _ := assignment["limits"].(map[string]any)
	return executionOrderedJSON{}.with(
		executionField{"protocol", executionProtocolVersion},
		executionField{"dispat", "fake"},
		executionField{"os", runtime.GOOS},
		executionField{"arch", runtime.GOARCH},
		executionField{"capacity", 4},
		executionField{"limits", executionOrderedJSON{}.with(
			executionField{"maxFiles", limits["maxFiles"]},
			executionField{"maxBytes", limits["maxBytes"]},
			executionField{"maxManifestBytes", limits["maxManifestBytes"]},
		)},
	)
}

// executionAssignmentPackage is the package object of an assignment.
func executionAssignmentPackage(assignment map[string]any) map[string]any {
	carried, _ := assignment["package"].(map[string]any)
	return carried
}

// executionWriteTree builds the crafted output tree, one level of folders
// deep, which is as much as these scenarios need.
func executionWriteTree(t *testing.T, mailbox string, entries map[string]executionTreeFile) string {
	t.Helper()
	folders := map[string]string{}
	var top string
	for path, file := range entries {
		oid := file.oid
		if oid == "" {
			oid = gitIn(t, mailbox, file.content, "hash-object", "-w", "--stdin")
		}
		kind := "blob"
		if file.mode == "160000" {
			kind = "commit"
		}
		if file.mode == "040000" {
			kind = "tree"
		}
		folder, name, isNested := strings.Cut(path, "/")
		if !isNested {
			top += fmt.Sprintf("%s %s %s\t%s\x00", file.mode, kind, oid, folder)
			continue
		}
		folders[folder] += fmt.Sprintf("%s %s %s\t%s\x00", file.mode, kind, oid, name)
	}
	for folder, listing := range folders {
		top += fmt.Sprintf("040000 tree %s\t%s\x00", gitIn(t, mailbox, listing, "mktree", "-z"), folder)
	}
	return gitIn(t, mailbox, top, "mktree", "-z")
}

// executionOrderedJSON is a JSON object whose fields keep the order they were
// written in, which is what a manifest digest is taken over: the digest covers
// the document the producing engine marshalled, so a test that reorders the
// fields is a test computing a different digest.
type executionOrderedJSON []executionField

// executionField is one field of such an object.
type executionField struct {
	key   string
	value any
}

func (o executionOrderedJSON) with(fields ...executionField) executionOrderedJSON {
	return append(append(executionOrderedJSON{}, o...), fields...)
}

// set replaces one field's value, keeping its position.
func (o executionOrderedJSON) set(key string, value any) executionOrderedJSON {
	out := append(executionOrderedJSON{}, o...)
	for index := range out {
		if out[index].key == key {
			out[index].value = value
		}
	}
	return out
}

// remove drops one field, which is how a scenario offers a manifest missing
// something the reader requires.
func (o executionOrderedJSON) remove(key string) executionOrderedJSON {
	out := executionOrderedJSON{}
	for _, field := range o {
		if field.key != key {
			out = append(out, field)
		}
	}
	return out
}

func (o executionOrderedJSON) MarshalJSON() ([]byte, error) {
	var out strings.Builder
	out.WriteByte('{')
	for index, field := range o {
		if index > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return []byte(out.String()), nil
}

func executionMustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	document, err := json.Marshal(value)
	require.NoError(t, err)
	return document
}

// executionDigestOf is the digest a manifest entry has to carry for one
// content, and executionDigestOfBytes the same for a document.
func executionDigestOf(body string) string { return executionDigestOfBytes([]byte(body)) }

func executionDigestOfBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// The retired fake writer pushes a v2 Claim and Result directly to Git. V3
// takes probe replies over its authenticated return channel, so these bytes
// cannot make it past preflight. The distinct manifest invalidity cases moved
// to the real-Git execution manifest validator tests, which the v3 result gate
// invokes before accepting a Result.
func TestExecutionRetiredFakeOutputWriterCannotAnswerV3Probe(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		executionOneWorker(cfg)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
	})
	writer := newExecutionFakeWorker(t, rig.mailbox, executionNode, func(*executionCraftedOutputs) {})
	writer.serve()
	defer writer.close()

	res := rig.release()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	assert.Zero(t, buildRuns(rig.repo), "a retired Git-push reply cannot start a command")
	assert.Empty(t, rig.repo.TagList(), "no old-transport output can be published")
}

// A retired Git-push reply without a signature cannot answer a v3 probe.
// The signed v3 input reader separately refuses an unsigned Result carrier.
func TestExecutionRetiredUnsignedOutputReplyCannotPassV3Probe(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		executionOneWorker(cfg)
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
	})
	worker := newExecutionFakeWorker(t, rig.mailbox, executionNode, func(*executionCraftedOutputs) {})
	worker.isSigned = false
	worker.serve()
	defer worker.close()

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireExecutionRefusal(t, res, executionRefusalCode, executionConfigurationCategory)
	_, isAdmitted := executionLine(res, "outputs admitted")
	assert.False(t, isAdmitted, "nothing the unsigned answer described was admitted")
	assert.Zero(t, buildRuns(rig.repo), "no command ran")
	assert.Empty(t, rig.repo.TagList(), "and nothing was published")
}

// executionCraftedEntry is one manifest entry, in the order a manifest states
// its fields.
func executionCraftedEntry(path, kind, mode string, size int, digest string) executionOrderedJSON {
	return executionOrderedJSON{}.with(
		executionField{"path", path},
		executionField{"type", kind},
		executionField{"mode", mode},
		executionField{"size", size},
		executionField{"sha256", digest},
	)
}

// executionCraftedPath is the scenario shape shared by every row whose point
// is a path: the tree holds one ordinary file, and the manifest calls it
// something a consumer must never write.
func executionCraftedPath(path string) func(*executionCraftedOutputs) {
	return func(c *executionCraftedOutputs) {
		c.manifest = c.manifest.set("entries", []any{
			executionCraftedEntry(path, "file", "0644", 4, executionDigestOf("one\n")),
		})
	}
}

// TestExecutionAdmittedOutputSurvivesBranchChange: a verified result object
// stays the consumer's input after the producer task ref is removed or moved.
// The assignment carries the exact admitted object as a parent, so neither
// later ref state can substitute bytes or make the consumer refetch a tip.
func TestExecutionAdmittedOutputSurvivesBranchChange(t *testing.T) {
	for name, isReplaced := range map[string]bool{
		"a branch that is no longer there":    false,
		"an object that is not on the branch": true,
	} {
		t.Run(name, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "let-the-consumer-go")
			rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
				space := cfg.Spaces["libs"]
				space.Flow.PostVersion = []string{"gate"}
				cfg.Spaces["libs"] = space
				cfg.Scripts["gate"] = models.Script{executionGateScript}
				cfg.Concurrency = []int{1, 1}
				executionOneWorker(cfg)
			})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(1) }), 0)
			thief := newExecutionAnswerThief(t, rig, gate, isReplaced)

			res := rig.release(executionGateEnv + "=" + gate)

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, thief.taken(), "the provider's task ref changed before consumer dispatch")
			assert.Equal(t, 1, executionBuildCount(rig, "assets"), "the provider built once")
			assert.Equal(t, 1, executionBuildCount(rig, "ui"), "the consumer used the admitted object: %v", rig.runs())
			produced := executionProbeValues(rig, "inputs")
			assert.Equal(t, produced["assets"], produced["ui"], "the consumer read exact original bytes")
			assert.True(t, rig.repo.IsTagged("ui@0.1.0"), "the consumer published its verified output")
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// executionAnswerThief removes the bytes behind an admitted answer once the
// run has admitted it, and only then lets the consumer go: it either deletes
// the branch a consumer would fetch, or replaces it with a history the
// admitted object is not on.
type executionAnswerThief struct {
	done   chan struct{}
	counts chan int
}

func newExecutionAnswerThief(t *testing.T, rig *executionRig, gate string, isReplaced bool) *executionAnswerThief {
	t.Helper()
	thief := &executionAnswerThief{done: make(chan struct{}), counts: make(chan int, 1)}
	go func() {
		taken := 0
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-thief.done:
				thief.counts <- taken
				return
			case <-ticker.C:
				if taken > 0 || !rig.repo.IsTagged("assets@0.1.0") {
					continue
				}
				for _, ref := range executionMailboxBranches(t, rig.mailbox) {
					if !executionCompletedAssetsBuild(rig.mailbox, ref) {
						continue
					}
					taken++
					if !isReplaced {
						bareGit(t, rig.mailbox, "update-ref", "-d", ref)
						continue
					}
					unrelated := strings.TrimSpace(bareGit(t, rig.mailbox, "commit-tree",
						gitIn(t, rig.mailbox, "", "mktree", "-z"), "-m", "dispat transport elsewhere"))
					bareGit(t, rig.mailbox, "update-ref", ref, unrelated)
				}
				if taken > 0 {
					require.NoError(t, os.WriteFile(gate, []byte("go\n"), 0o644))
				}
			}
		}
	}()
	return thief
}

func (a *executionAnswerThief) taken() int {
	close(a.done)
	return <-a.counts
}

// TestExecutionOversizedManifestIsRefusedWhereItIsWritten: a description
// larger than the ceiling the run holds it to is refused on the node that
// wrote it, so the run reads one sentence naming the rule rather than waiting
// out a task deadline for a document nobody could read.
//
// The ceiling bounds every document a mailbox carries, so it is set above what
// an assignment and a result weigh and below what a manifest of a thousand
// files does: the scenario is about the manifest rather than about the
// protocol being unusable.
func TestExecutionOversizedManifestIsRefusedWhereItIsWritten(t *testing.T) {
	rig := newExecutionOutputWorkspaceWith(t, func(*harness.Repo) string {
		return executionRecordingScript + ` && mkdir -p dist && ` +
			`{ [ "$DISPAT_PACKAGE" != assets ] || ` +
			`for i in $(seq 1 1000); do printf 'x' > "dist/file$i.txt"; done; }`
	}, func(cfg *models.File) {
		executionOneWorker(cfg)
		cfg.Scripts["publish"] = models.Script{"echo publishing"}
		cfg.Execution.Transfer = &models.ExecutionTransferConfig{MaxManifestBytes: 65536}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "assets"),
		"the package is refused with the integrity code\nstdout:\n%s", res.Stdout)
	assert.False(t, rig.repo.IsTagged("assets@0.1.0"), "nothing of it was published")
	reply := stopAll(t, []*executionWorker{worker})[0]
	assert.Contains(t, reply.Stdout, "manifest-oversize",
		"the node names the rule it broke\nstdout:\n%s", reply.Stdout)
}

// TestExecutionOutputBytesIgnoreTheCheckoutAttributes: a checkout's
// attributes describe its sources and must not touch a build output. A
// package whose tree is marked `text` builds a library holding CRLF pairs;
// the consumer reads exactly the bytes the build wrote, on another machine and
// in the orchestrator's own checkout, and the capture records an executable
// as executable.
func TestExecutionOutputBytesIgnoreTheCheckoutAttributes(t *testing.T) {
	rig := newExecutionOutputWorkspaceWith(t, func(repo *harness.Repo) string {
		return executionRecordingScript + ` && mkdir -p dist && case "$DISPAT_PACKAGE" in
  assets) printf 'ELF\000head\r\nbody\r\n' > dist/lib.bin && printf '#!/bin/sh\r\n' > dist/tool && chmod 755 dist/tool ;;
  ui|docs) od -An -c ../assets/dist/lib.bin | tr -d ' \n' > dist/from-assets.txt && test -x ../assets/dist/tool ;;
  app) cat ../ui/dist/from-assets.txt ../docs/dist/from-assets.txt > dist/combined.txt ;;
esac`
	})
	rig.repo.WriteFile("packages/assets/.gitattributes", "* text eol=lf\n")
	rig.repo.Commit("chore(assets): store the sources as text")
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 1)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	// The publish probe prints what each package's dist holds, in the
	// orchestrator's own checkout: ui and docs wrote the character dump of the
	// library they read on their node, and app the two dumps.
	published := executionProbeValues(rig, "publish")
	for _, name := range []string{"ui", "docs", "app"} {
		assert.Contains(t, published[name], `\r\n`,
			"%s read the CRLF pairs the build wrote, on its node and in the orchestrator's copy: %q", name, published[name])
	}
	stopAll(t, workers)
}
