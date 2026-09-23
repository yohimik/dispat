// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, fifth part: publishing from a worker node.
//
// Publication is the one stage where being somewhere else changes what has to
// be true. A build that ran on the wrong inputs wastes a machine's time; a
// publication that ran on the wrong inputs is on a registry and cannot be
// taken back. So the claims here are about the decision rather than about the
// placement: the node does everything up to the irreversible command and then
// stops, the run revalidates what only it can revalidate, and the command
// starts if and only if the run said so, once.
//
// The second half is where the credentials are. A space that logs in publishes
// on the machine the release was started on and nothing about its
// authentication ever enters a mailbox; a space that does not may have its
// publish delegated, and what that publish reads from the environment is read
// on the node, from the node's own environment.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The probes a publication leaves: which node ran the hook, the command and
// the orchestrator-side tail. The third field is the node, so that
// executionProbeValues answers "where did this stage run" by package.
const (
	executionPublishProbe = `printf '%s %s %s\n' probe-publish "$DISPAT_PACKAGE" ` +
		`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`
	executionPrePublishProbe = `printf '%s %s %s\n' probe-prepublish "$DISPAT_PACKAGE" ` +
		`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`
	executionPostPublishProbe = `printf '%s %s %s\n' probe-postpublish "$DISPAT_PACKAGE" ` +
		`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`
)

// executionRemotePublishScript is the publish script of every delegated
// scenario: it proves the package's own build outputs are in the folder it
// runs in before it records itself. A publisher that cannot see its own dist
// is a publisher that would upload nothing.
const executionRemotePublishScript = `test -d dist && test -n "$(ls -A dist)" && ` +
	executionPublishProbe

// newExecutionPublishRig seeds the output workspace with the publish stage
// delegated: `dist` declared and ignored, a beforePublish hook and a
// postPublish hook that each record where they ran.
func newExecutionPublishRig(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	return newExecutionOutputWorkspace(t, append([]func(*models.File){
		executionDelegatedPublish}, adjust...)...)
}

// executionDelegatedPublish is the configuration that moves the publish stage
// to a node: the explicit `worker` value for the publish half of the pair, and
// the scripts that say where each part of the frame ran.
func executionDelegatedPublish(cfg *models.File) {
	cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
	cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
	cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe}
	cfg.Scripts["postpublish"] = models.Script{executionPostPublishProbe}
	space := cfg.Spaces["libs"]
	space.Flow.BeforePublish = []string{"prepublish"}
	space.Flow.PostPublish = []string{"postpublish"}
	cfg.Spaces["libs"] = space
}

// startWorkersWithEnv starts one node per name with something extra in its
// environment, which is how a scenario gives a node a credential the machine
// that starts the release does not have.
func (r *executionRig) startWorkersWithEnv(names []string, capacity int,
	extraEnv ...string) []*executionWorker {
	r.t.Helper()
	workers := make([]*executionWorker, 0, len(names))
	for _, name := range names {
		workers = append(workers, r.startWorker(executionWorkerConfig(r.mailbox,
			func(settings *models.ExecutionConfig) {
				settings.Name = name
				settings.Concurrency = models.Int(capacity)
			}), 0, extraEnv...))
	}
	return workers
}

// TestExecutionPublishRunsOnWorkersUnderAuthorization: the whole handshake,
// end to end. Every package's beforePublish hook and publish command run on a
// node and never here; the node is authorized once, before its command and
// after its hook; and everything a release is made of — the tags, the
// changelog entries, the postPublish hook — is still written here, at the head
// the plan was computed against.
func TestExecutionPublishRunsOnWorkersUnderAuthorization(t *testing.T) {
	rig := newExecutionPublishRig(t)
	plannedHead := strings.TrimSpace(rig.repo.Git("rev-parse", "HEAD"))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	published := executionProbeValues(rig, "publish")
	prepared := executionProbeValues(rig, "prepublish")
	tailed := executionProbeValues(rig, "postpublish")
	for _, name := range executionWorkspacePackages {
		assert.Contains(t, []string{executionNode, executionSecondNode}, published[name],
			"%s published on a node rather than here: %v", name, rig.runs())
		assert.Equal(t, published[name], prepared[name],
			"%s ran its beforePublish hook on the node that published it", name)
		assert.Equal(t, executionOrchestratorLabel, tailed[name],
			"%s ran its postPublish hook here, where the records are", name)
	}

	authorized := executionAuthorizations(res)
	assert.Len(t, authorized, len(executionWorkspacePackages),
		"one authorization per publication, and no publication twice\nstdout:\n%s", res.Stdout)
	for _, event := range authorized {
		assert.Contains(t, []string{executionNode, executionSecondNode}, event.Str("worker"),
			"the authorization names the node it was issued to")
	}

	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	assert.Equal(t, plannedHead, strings.TrimSpace(rig.repo.Git("rev-parse", "assets@0.1.0^{}")),
		"the orchestrator tagged the head the plan was computed against")
	for _, name := range executionWorkspacePackages {
		assert.FileExists(t, filepath.Join(rig.repo.Root, "packages", name, "CHANGELOG.md"),
			"%s's changelog was written here", name)
	}
	assert.Empty(t, rig.branches(), "the run closed every coordination branch it created")

	for _, reply := range stopAll(t, workers) {
		assert.Less(t, executionEventIndex(reply, "publication authorized, starting the publish command"),
			executionPublishStageIndex(reply),
			"no publish command started before the node had been authorized\nstdout:\n%s", reply.Stdout)
	}
}

// executionAuthorizations is every publication this run authorized, in order.
func executionAuthorizations(res harness.RunResult) []harness.Event {
	var issued []harness.Event
	for _, event := range executionEvents(res) {
		if event.Str("message") == "publication authorized" {
			issued = append(issued, event)
		}
	}
	return issued
}

// executionEventIndex is where one message first appears in a process's log,
// and the count of lines when it never does, so that an ordering assertion
// against a missing line fails rather than passing by accident.
func executionEventIndex(res harness.RunResult, message string) int {
	events := executionEvents(res)
	for index, event := range events {
		if event.Str("message") == message {
			return index
		}
	}
	return len(events)
}

// executionPublishStageIndex is where a node first started a publish stage.
func executionPublishStageIndex(res harness.RunResult) int {
	events := executionEvents(res)
	for index, event := range events {
		if event.Str("message") == "stage started" && event.Str("stage") == "publish" {
			return index
		}
	}
	return len(events)
}

// TestExecutionPublishStaysOnTheOrchestratorByDefault: `both` is not `worker`
// for a publication. Publication is serialized per repository whatever
// happens, so delegating it buys the run nothing and spreads the registry
// credentials over one more machine: the default keeps it here while the
// builds still go to the nodes, and no publish assignment is ever written into
// a mailbox.
func TestExecutionPublishStaysOnTheOrchestratorByDefault(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
	})
	watch := newExecutionBranchWatch(t, rig)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	published := executionProbeValues(rig, "publish")
	placed := rig.nodesByPackage()
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionOrchestratorLabel, published[name],
			"%s published here under the default: %v", name, rig.runs())
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s still built on a node", name)
	}
	assigned := watch.seen()
	assert.Empty(t, executionPublishBranches(assigned),
		"no publish assignment was ever offered to a mailbox: %v", assigned)
	assert.Empty(t, executionAuthorizations(res), "and nothing had to be authorized")
	assert.Len(t, rig.repo.TagList(), len(executionWorkspacePackages))
	stopAll(t, workers)
}

// executionPublishBranches is the coordination branches of a run that carry a
// publication, told apart by the kind label every branch name embeds.
func executionPublishBranches(branches []string) []string {
	var publishing []string
	for _, branch := range branches {
		if strings.Contains(branch, "-"+executionPublishKind+"-") {
			publishing = append(publishing, branch)
		}
	}
	return publishing
}

// executionPublishKind is the word a publish branch is labelled with.
const executionPublishKind = "publish"

// TestExecutionPublishWithALoginStaysOnTheOrchestrator: giving a space a login
// script is what pins its publishes to the machine the release was started on.
// The login runs here, once, the publish frame runs here in the checkout the
// verified build outputs were installed into, and nothing about the
// authentication travels: no login command, no login export, no publish
// assignment at all.
func TestExecutionPublishWithALoginStaysOnTheOrchestrator(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
		cfg.Scripts["login"] = models.Script{
			`printf '%s %s %s\n' probe-login space "${DISPAT_EXECUTION_NODE:-orchestrator}" ` +
				`>> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Login = []string{"login"}
		cfg.Spaces["libs"] = space
	})
	watch := newExecutionBranchWatch(t, rig)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, executionOrchestratorLabel,
		executionProbeValue(t, rig, "login", "space"), "the login ran here")
	assert.Equal(t, 1, executionProbeCount(rig, "login"), "and once for the whole space")
	published := executionProbeValues(rig, "publish")
	placed := rig.nodesByPackage()
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionOrchestratorLabel, published[name],
			"%s published here beside its login: %v", name, rig.runs())
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s was still built on a node", name)
	}
	assigned := watch.seen()
	assert.Empty(t, executionPublishBranches(assigned),
		"no publish branch was ever created: %v", assigned)
	assert.Len(t, rig.repo.TagList(), len(executionWorkspacePackages))
	stopAll(t, workers)
}

// executionProbeCount is how many lines one probe recorded in total.
func executionProbeCount(rig *executionRig, probe string) int {
	recorded := 0
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+probe {
			recorded++
		}
	}
	return recorded
}

// TestExecutionBuildsOverlapWhilePublicationSerializesPerOwner: the two halves
// of the profile's scheduling promise in one run. Builds of independent
// packages overlap on two machines, because that is what a pool is for; the
// publications of one repository never overlap, because a repository's
// publications and the records they produce are one lane whichever machine
// runs the commands.
func TestExecutionBuildsOverlapWhilePublicationSerializesPerOwner(t *testing.T) {
	var timed *harness.Repo
	rig := newExecutionOutputWorkspaceWith(t, func(repo *harness.Repo) string {
		timed = repo
		return executionOutputBuild + " && " +
			repo.TsmarkScript("builds.log", "$DISPAT_PACKAGE", executionStageWindow)
	}, executionDelegatedPublish, func(cfg *models.File) {
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript + " && " +
			timed.TsmarkScript("publications.log", "$DISPAT_PACKAGE", executionPublishWindow)}
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	builds := rig.repo.Timeline("builds.log")
	harness.AssertOverlaps(t, harness.Find(t, builds, "ui"), harness.Find(t, builds, "docs"))
	publications := rig.repo.Timeline("publications.log")
	require.Len(t, publications, len(executionWorkspacePackages), "every package published")
	harness.AssertConcurrencyBudget(t, publications, 1)
	stopAll(t, workers)
}

// executionPublishWindow is how long a delegated publication holds its lane.
// It is shorter than the build window because four of them are serialized by
// construction and the claim is that they do not overlap, not that they are
// slow.
const executionPublishWindow = 2 * time.Second

// TestExecutionRelevantNativeChangeWithholdsPublication: a commit made while
// the run is going, inside a folder this package's build read, withholds its
// publication. The artefact was built from something the repository no longer
// holds, so the run refuses to name it a release of that repository and asks
// for a new plan.
func TestExecutionRelevantNativeChangeWithholdsPublication(t *testing.T) {
	rig := newExecutionPublishRig(t, executionCommitDuringTheRun(
		filepath.Join("packages", "ui", "extra.txt")))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "ui"),
		"the package whose inputs moved is withheld with the integrity code\nstdout:\n%s", res.Stdout)
	assert.Contains(t, executionFailedStages(res), "publish")
	assert.NotContains(t, executionProbeValues(rig, "publish"), "ui",
		"and its publish command never ran anywhere: %v", rig.runs())
	assert.False(t, rig.repo.IsTagged("ui@0.1.0"), "nothing of it was recorded")
	assert.True(t, rig.repo.IsTagged("assets@0.1.0"), "the package that published first still did")
	stopAll(t, workers)
}

// A consumer's own folder can stay untouched while its provider changes.
// The late provider commit still invalidates the artefact the worker built.
// The hook waits until app's build finishes before committing, which also
// proves ui and docs finished: app's build waits for both of their builds.
func TestExecutionChangedProviderWithholdsDependentPublications(t *testing.T) {
	rig := newExecutionPublishRig(t, executionCommitDuringTheRun(
		filepath.Join("packages", "assets", "late-input.txt"), "app"))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, rig.repo.IsTagged("assets@0.1.0"), "the provider published before its hook changed the checkout")
	for _, name := range []string{"ui", "docs", "app"} {
		assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, name),
			"%s cannot publish an artefact built against the earlier provider inputs", name)
		assert.NotContains(t, executionProbeValues(rig, "publish"), name,
			"%s's publish command never ran", name)
		assert.False(t, rig.repo.IsTagged(name+"@0.1.0"))
	}
	stopAll(t, workers)
}

// TestExecutionUnrelatedNativeChangeKeepsResult: the same commit outside every
// folder the package was built from changes nothing. A release moves the
// repository constantly — every changelog it writes is a change to a package
// folder — and a check that withheld a publication for any movement at all
// would withhold every publication of every run.
func TestExecutionUnrelatedNativeChangeKeepsResult(t *testing.T) {
	rig := newExecutionPublishRig(t, executionCommitDuringTheRun(
		filepath.Join("elsewhere", "note.txt")))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	published := executionProbeValues(rig, "publish")
	for _, name := range executionWorkspacePackages {
		assert.Contains(t, []string{executionNode, executionSecondNode}, published[name],
			"%s published on a node although the repository moved under it", name)
	}
	stopAll(t, workers)
}

// executionCommitDuringTheRun makes the first package's postPublish hook
// commit a file, which is the one way a fixture can move the repository from
// inside the run: postPublish runs on the orchestrator, in its checkout,
// between one package's publication and the next one's.
//
// The path is stated from the repository root, so the hook leaves its own
// folder first: a stage script runs in its package's folder, and a relative
// path that meant one thing to the reader and another to the shell would make
// every claim here a claim about the wrong folder.
// A named build is awaited before the commit when the claim needs a definite
// old input snapshot; without that barrier it may legitimately build later.
func executionCommitDuringTheRun(path string, waitForBuild ...string) func(*models.File) {
	return func(cfg *models.File) {
		wait := ""
		for _, name := range waitForBuild {
			marker := "probe-inputs " + name + " "
			wait += fmt.Sprintf(`for attempt in $(seq 1 300); do
  grep -Fq %q "$DISPAT_IT_EXECUTION_LOG" && break
  sleep 0.1
done
grep -Fq %q "$DISPAT_IT_EXECUTION_LOG" || exit 9
`, marker, marker)
		}
		cfg.Scripts["postpublish"] = models.Script{
			executionPostPublishProbe,
			fmt.Sprintf(`[ "$DISPAT_PACKAGE" != assets ] || {
cd "$(git rev-parse --show-toplevel)" &&
%s
mkdir -p "$(dirname %q)" &&
printf 'changed\n' > %q &&
git add -- %q &&
git -c user.name=fixture -c user.email=fixture@example.com commit -q -m "chore: a change made during the run" -- %q
}`, wait, path, path, path, path),
		}
	}
}

// TestExecutionUnauthorizedPublisherNeverStarts: a run that has lost the lock
// it took authorizes nothing more. The loss is noticed at whichever check runs
// first after it: the ownership check before the next assignment, or the
// revalidation behind the authorization a node waiting at the gate asked
// for. Either way no command starts, the node is withdrawn and acknowledges,
// and the run exits non-zero having published nothing further.
func TestExecutionUnauthorizedPublisherNeverStarts(t *testing.T) {
	rig := newExecutionPublishRig(t, func(cfg *models.File) {
		cfg.Scripts["postpublish"] = models.Script{
			executionPostPublishProbe,
			`[ "$DISPAT_PACKAGE" != assets ] || ` +
				`git --git-dir="$DISPAT_IT_EXECUTION_ORIGIN" tag -d dispat-release-lock`,
		}
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release(executionOriginEnv + "=" + rig.origin)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	withheld, isWithheld := executionLine(res, "publication withheld")
	_, isHalted := executionLine(res, "the release lock was lost, so no new effect may start")
	require.True(t, isWithheld || isHalted,
		"the run withdrew or refused the attempt it would not authorize\nstdout:\n%s", res.Stdout)
	if isWithheld {
		assert.Contains(t, []string{executionNode, executionSecondNode}, withheld.Str("worker"))
	}
	assert.True(t, harness.IsCodePresent(executionEvents(res), executionLockCode),
		"and said so with the lock code\nstdout:\n%s", res.Stdout)

	assert.Equal(t, []string{"assets"}, executionPublishedPackages(rig),
		"only the package that published before the loss ever ran a publish command: %v", rig.runs())
	assert.Equal(t, []string{"assets@0.1.0"}, rig.repo.TagList())
	assert.Empty(t, rig.branches(), "and the run still closed the branches it created")
	stopAll(t, workers)
}

// executionOriginEnv names the bare remote a fixture hook writes to, in the
// suite's own namespace, which is the only one the harness lets through.
const executionOriginEnv = "DISPAT_IT_EXECUTION_ORIGIN"

// executionLockCode is what dispat reports a lost or unreadable release lock
// under, which §28.9 classes as native-recording-or-lock.
const executionLockCode = "E336"

// executionPublishedPackages is every package whose publish command ran
// anywhere, sorted, so that a claim about which ones did is a claim about a
// set.
func executionPublishedPackages(rig *executionRig) []string {
	published := executionProbeValues(rig, "publish")
	names := make([]string, 0, len(published))
	for name := range published {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestExecutionPublishCredentialsResolveOnTheWorker: what a publish command
// reads from the environment is read on the node that runs it. A static pair
// naming a variable travels as that reference and is expanded from the node's
// own environment, which is the whole reason a registry token never has to
// leave the machine it is configured on.
func TestExecutionPublishCredentialsResolveOnTheWorker(t *testing.T) {
	rig := newExecutionPublishRig(t, func(cfg *models.File) {
		cfg.Env = map[string]string{"REGISTRY_TOKEN": "$" + executionTokenEnv}
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript + ` && ` +
			`printf '%s %s %s\n' probe-token "$DISPAT_PACKAGE" "${REGISTRY_TOKEN:-none}" ` +
			`>> "$DISPAT_IT_EXECUTION_LOG"`}
	})
	workers := rig.startWorkersWithEnv([]string{executionNode, executionSecondNode}, 2,
		executionTokenEnv+"="+executionWorkerToken)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionWorkerToken, executionProbeValue(t, rig, "token", name),
			"%s's publish read the token out of the node's own environment", name)
	}
	stopAll(t, workers)
}

// executionTokenEnv is the variable the fixture's configuration refers to, and
// executionWorkerToken is the value only a node has.
const (
	executionTokenEnv    = "DISPAT_IT_EXECUTION_TOKEN"
	executionWorkerToken = "token-that-lives-on-the-node"
)

// TestExecutionSecretNeverReachesMailboxOrLogs: the negative half of the same
// claim. A value that exists only in the orchestrator's environment is not put
// into an assignment, is not written into a mailbox and is not printed by
// either process at trace level, because the pair that names it travels
// unresolved.
func TestExecutionSecretNeverReachesMailboxOrLogs(t *testing.T) {
	rig := newExecutionPublishRig(t, func(cfg *models.File) {
		cfg.LogLevel = "trace"
		cfg.Env = map[string]string{"REGISTRY_TOKEN": "$" + executionTokenEnv}
	})
	keep := newExecutionMailboxSnapshot(t, rig)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release(executionTokenEnv + "=" + executionOrchestratorSecret)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, executionOrchestratorSecret,
		"the orchestrator never printed the value it resolved nothing with")
	assert.NotContains(t, res.Stderr, executionOrchestratorSecret)
	bodies := keep.objects()
	assert.NotEmpty(t, bodies, "the run did write objects into the mailbox")
	for _, body := range bodies {
		assert.NotContains(t, body, executionOrchestratorSecret,
			"no object this run wrote into the mailbox carries the value")
	}
	for _, reply := range stopAll(t, workers) {
		assert.NotContains(t, reply.Stdout, executionOrchestratorSecret,
			"and no node printed it either")
		assert.NotContains(t, reply.Stderr, executionOrchestratorSecret)
	}
}

// executionOrchestratorSecret is a value only the machine that starts the
// release has, so finding it anywhere else is finding it somewhere it was
// carried to.
const executionOrchestratorSecret = "orchestrator-only-secret-value"

// executionMailboxSnapshot keeps every object a run writes into a mailbox,
// because a finished run deletes the branches that named them.
//
// seen belongs to the watch goroutine until done is closed, and to whoever
// reads it after that: objects stops the watch before it reads, so the two
// never touch the map at once.
type executionMailboxSnapshot struct {
	t       *testing.T
	mailbox string
	stop    chan struct{}
	halt    sync.Once
	done    chan struct{}
	seen    map[string]string
}

// newExecutionMailboxSnapshot starts watching a mailbox and keeps the body of
// every transport blob it ever advertises.
func newExecutionMailboxSnapshot(t *testing.T, rig *executionRig) *executionMailboxSnapshot {
	t.Helper()
	keep := &executionMailboxSnapshot{t: t, mailbox: rig.mailbox,
		stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]string{}}
	go keep.watch()
	t.Cleanup(keep.finish)
	return keep
}

// finish stops the watch after its last collection and waits for it. It is
// safe to call more than once: the scenario calls it through objects, and the
// cleanup calls it again for a scenario that never asked.
func (k *executionMailboxSnapshot) finish() {
	k.halt.Do(func() { close(k.stop) })
	<-k.done
}

// watch copies every blob reachable from a coordination branch until the
// scenario stops it.
func (k *executionMailboxSnapshot) watch() {
	defer close(k.done)
	for {
		select {
		case <-k.stop:
			k.collect()
			return
		case <-time.After(50 * time.Millisecond):
			k.collect()
		}
	}
}

// collect reads what the mailbox advertises right now.
//
// Every read is allowed to fail and is simply skipped: the run this is
// watching deletes its own branches as it finishes with them, so a ref listed
// one moment and gone the next is the ordinary case rather than a fault, and a
// watch that failed the test for it would be failing it for the thing it is
// there to observe.
func (k *executionMailboxSnapshot) collect() {
	listed := executionReadMailbox(k.mailbox, "for-each-ref", "--format=%(refname)", "refs/heads/")
	if listed == "" {
		return
	}
	for _, ref := range strings.Split(listed, "\n") {
		for _, line := range strings.Split(executionReadMailbox(k.mailbox, "ls-tree", "-r", ref), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 || k.seen[fields[2]] != "" {
				continue
			}
			k.seen[fields[2]] = executionReadMailbox(k.mailbox, "cat-file", "-p", fields[2])
		}
	}
}

// executionReadMailbox runs one read against a mailbox and answers nothing at
// all when it fails, which is what a watch of a repository somebody else is
// deleting branches in has to do with a failure.
func executionReadMailbox(mailbox string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", mailbox}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// objects is every blob body this watch kept. It ends the watch first, so the
// answer includes a last look at the mailbox and nothing writes to the map
// while it is read.
func (k *executionMailboxSnapshot) objects() []string {
	k.finish()
	bodies := make([]string, 0, len(k.seen))
	for _, body := range k.seen {
		bodies = append(bodies, body)
	}
	return bodies
}

// TestExecutionRemotePublishFailureKeepsLocalSemantics: a publication that
// failed on a node fails its package exactly as one that failed here always
// did. Nothing is tagged, the outcome script runs on the orchestrator, and the
// packages that do not depend on it still release. A hook that failed before
// the gate fails the same way, and nothing was ever authorized for it.
func TestExecutionRemotePublishFailureKeepsLocalSemantics(t *testing.T) {
	for name, row := range map[string]struct {
		scripts        func(*models.File)
		authorizations int
	}{
		"the publish command exits non-zero": {
			scripts: func(cfg *models.File) {
				cfg.Scripts["publish"] = models.Script{executionRemotePublishScript +
					` && { [ "$DISPAT_PACKAGE" != ui ] || exit 7; }`}
			},
			// The command ran, so it had been authorized: what failed is the
			// publication itself, after the decision to make it.
			authorizations: len(executionWorkspacePackages),
		},
		"the beforePublish hook fails on the node": {
			scripts: func(cfg *models.File) {
				cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe +
					` && { [ "$DISPAT_PACKAGE" != ui ] || exit 3; }`}
			},
			// A node that never reached the gate is a node nothing was ever
			// authorized for.
			authorizations: len(executionWorkspacePackages) - 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionPublishRig(t, func(cfg *models.File) {
				space := cfg.Spaces["libs"]
				space.Flow.OnFail = []string{"onfail"}
				cfg.Spaces["libs"] = space
				cfg.Scripts["onfail"] = models.Script{executionOnFailScript}
				row.scripts(cfg)
			})
			workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

			res := rig.release()

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, executionFailedStages(res), "publish",
				"the package failed at its publish stage\nstdout:\n%s", res.Stdout)
			assert.False(t, rig.repo.IsTagged("ui@0.1.0"), "nothing of the failed package was recorded")
			assert.True(t, rig.repo.IsTagged("docs@0.1.0"), "an unrelated package still released")
			assert.Equal(t, executionOrchestratorLabel,
				executionProbeValue(t, rig, "onfail", "ui"), "its outcome script ran here")
			assert.Len(t, executionAuthorizations(res), row.authorizations,
				"a publication nobody could have made was never authorized\nstdout:\n%s", res.Stdout)
			stopAll(t, workers)
		})
	}
}

// TestExecutionPublishExportsReachTheRecorders: what a delegated publication
// exports reaches the run exactly as a local one's does. The GitHub opt-in a
// node wrote is what makes the orchestrator's own recorder create the release,
// and the value it exported beside it is in the environment of the postPublish
// hook that runs here afterwards.
func TestExecutionPublishExportsReachTheRecorders(t *testing.T) {
	server, bodies := githubFake(t)
	defer server.Close()
	rig := newExecutionPublishRig(t, func(cfg *models.File) {
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), APIURL: server.URL,
			Owner: "acme", Repo: "fixture", TokenEnv: executionGitHubTokenEnv,
		}
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript + ` && ` +
			`printf 'DISPAT_EXPORT_GITHUB=\n' >> "$DISPAT_OUTPUT" && ` +
			`printf 'PUBLISHED_AT=%s\n' "$DISPAT_EXECUTION_NODE" >> "$DISPAT_OUTPUT"`}
		cfg.Scripts["postpublish"] = models.Script{
			`printf '%s %s %s\n' probe-postpublish "$DISPAT_PACKAGE" ` +
				`"${DISPAT_OUTPUT_PUBLISHED_AT:-none}" >> "$DISPAT_IT_EXECUTION_LOG"`}
	})
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release(executionGitHubTokenEnv + "=token")

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	published := executionProbeValues(rig, "publish")
	exported := executionProbeValues(rig, "postpublish")
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, published[name], exported[name],
			"%s's postPublish hook read the variable its publisher exported", name)
	}
	assert.Len(t, bodies(), len(executionWorkspacePackages),
		"the orchestrator's own recorder created one release per opted-in package")
	stopAll(t, workers)
}

// executionReleaseTags are the tags a run created as records of a release,
// which is every tag except the exclusion itself.
//
// The release lock is a tag and is deliberately not a record: a run that
// authorized a publication it cannot account for leaves that exclusion behind
// for an operator (§28.6), so a scenario asserting that nothing was recorded
// has to be asking about records rather than about refs.
func executionReleaseTags(rig *executionRig) []string {
	var records []string
	for _, tag := range rig.repo.TagList() {
		if strings.HasPrefix(tag, "dispat-release-lock") {
			continue
		}
		records = append(records, tag)
	}
	return records
}

// executionGitHubTokenEnv is the variable the fixture's GitHub recorder reads
// its token from, in the suite's own namespace.
const executionGitHubTokenEnv = "DISPAT_IT_EXECUTION_GH_TOKEN"

// TestExecutionPublishHandshakeGitFaults: each message of the handshake failed
// one at a time, on one package so that the ordinals mean what they say.
// Whichever of them fails, the package fails, nothing of it is recorded and no
// publish command runs on any node: an authorization that could not be written
// and a node that could not read one are the same thing to a release.
func TestExecutionPublishHandshakeGitFaults(t *testing.T) {
	for name, row := range map[string]struct {
		fault    harness.GitFault
		onWorker bool
	}{
		"the run cannot write the authorization": {
			// The second push onto a publish branch: the first is the
			// assignment that created it.
			fault: harness.GitFault{Pattern: "*push*-publish-*", Nth: 2, Onward: true}},
		"the node cannot report itself ready": {
			// The second push onto a publish branch from the node: the first is
			// its claim.
			fault: harness.GitFault{Pattern: "*push*-publish-*", Nth: 2, Onward: true}, onWorker: true},
		"the node cannot re-read its own branch": {
			fault: harness.GitFault{Pattern: "*ls-remote*-publish-*", Nth: 1, Onward: true}, onWorker: true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionSinglePublishRig(t)
			fault := harness.NewGitFault(t, row.fault)
			workerEnv, releaseEnv := []string{}, []string{}
			if row.onWorker {
				workerEnv = fault.Env()
			} else {
				releaseEnv = fault.Env()
			}
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) {
					settings.Concurrency = models.Int(2)
				}), 0, workerEnv...)

			res := rig.release(releaseEnv...)

			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Empty(t, executionReleaseTags(rig), "nothing was recorded")
			assert.Empty(t, executionProbeValues(rig, "publish"),
				"and no publish command ran anywhere: %v", rig.runs())
			stopAll(t, []*executionWorker{worker})
		})
	}
}

// newExecutionSinglePublishRig is one package whose publish is delegated, with
// a wait short enough that a node which is never told anything gives up inside
// a test rather than inside a CI timeout.
func newExecutionSinglePublishRig(t *testing.T) *executionRig {
	t.Helper()
	return newExecutionRig(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyBoth, models.RunOnlyWorker)
		cfg.Scripts["publish"] = models.Script{executionPublishProbe}
		cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 30, Task: 25}
	})
}
