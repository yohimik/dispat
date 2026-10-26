// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57, fifth part: workers build and validate while the orchestrator
// publishes, records, and keeps release credentials outside worker mailboxes.

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

const (
	executionPublishRunStoppingEventKind    = "run-stopping"
	executionPublishInertCancelledEventKind = "inert-cancelled"
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

// The orchestrator must see the worker's admitted build outputs before it
// starts the publish script.
const executionRemotePublishScript = `test -d dist && test -n "$(ls -A dist)" && ` +
	executionPublishProbe

// newExecutionOrchestratedPublishRig delegates build while keeping all
// publish hooks and the publish command on the orchestrator.
func newExecutionOrchestratedPublishRig(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	return newExecutionOutputWorkspace(t, append([]func(*models.File){
		executionOrchestratedPublish}, adjust...)...)
}

func executionOrchestratedPublish(cfg *models.File) {
	cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
	cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
	cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe}
	cfg.Scripts["postpublish"] = models.Script{executionPostPublishProbe}
	space := cfg.Spaces["libs"]
	space.Flow.BeforePublish = []string{"prepublish"}
	space.Flow.PostPublish = []string{"postpublish"}
	cfg.Spaces["libs"] = space
}

// executionAuthorizations finds any obsolete worker publication authorizations.
func executionAuthorizations(res harness.RunResult) []harness.Event {
	var issued []harness.Event
	for _, event := range executionEvents(res) {
		if event.Str("message") == "publication authorized" {
			issued = append(issued, event)
		}
	}
	return issued
}

// Worker builds and tests complete before the orchestrator publishes.
func TestExecutionWorkerBuildAndTestPublishOnTheOrchestrator(t *testing.T) {
	rig := newExecutionOutputWorkspace(t, func(cfg *models.File) {
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["test"] = models.Script{`printf '%s %s %s\n' probe-test "$DISPAT_PACKAGE" ` +
			`"${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_IT_EXECUTION_LOG"`}
		space := cfg.Spaces["libs"]
		space.Flow.Test = []string{"test"}
		cfg.Spaces["libs"] = space
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript}
	})
	watch := newExecutionBranchWatch(t, rig)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	published := executionProbeValues(rig, "publish")
	tested := executionProbeValues(rig, "test")
	placed := rig.nodesByPackage()
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionOrchestratorLabel, published[name],
			"%s published here under the default: %v", name, rig.runs())
		assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
			"%s still built on a node", name)
		assert.Contains(t, []string{executionNode, executionSecondNode}, tested[name],
			"%s ran its test on a node", name)
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
	}, executionOrchestratedPublish, func(cfg *models.File) {
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

// executionPublishWindow is how long an orchestrator publication holds its lane.
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
	rig := newExecutionOrchestratedPublishRig(t, executionCommitDuringTheRun(
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
	rig := newExecutionOrchestratedPublishRig(t, executionCommitDuringTheRun(
		filepath.Join("packages", "assets", "late-input.txt"), "app"))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.repo.StartReleaseEnv(rig.env(), "release", "--return-endpoint", rig.returnURL).
		WaitForDiagnostic(90*time.Second, func() {
			t.Logf("changed-provider signed ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
		})

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, rig.repo.IsTagged("assets@0.1.0"), "the provider published before its hook changed the checkout")
	assert.True(t, harness.IsCodePresentForPackage(executionEvents(res), executionIntegrityCode, "docs"),
		"docs' pre-publish check must identify the changed provider input\nstdout:\n%s", res.Stdout)
	var stopping bool
	inertCancelled := make(map[string]bool)
	for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
		if event.Kind == executionPublishRunStoppingEventKind {
			stopping = true
		}
		if event.Kind == executionPublishInertCancelledEventKind {
			inertCancelled[event.Package] = true
		}
	}
	require.True(t, stopping, "the relevant native change fences later claims in the signed ledger")
	assert.True(t, inertCancelled["ui"], "ui's unclaimed native effect ends with signed inert cancellation")
	for _, name := range []string{"ui", "docs", "app"} {
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
	rig := newExecutionOrchestratedPublishRig(t, executionCommitDuringTheRun(
		filepath.Join("elsewhere", "note.txt")))
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.ElementsMatch(t,
		[]string{"assets@0.1.0", "ui@0.1.0", "docs@0.1.0", "app@0.1.0"}, rig.repo.TagList())
	published := executionProbeValues(rig, "publish")
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionOrchestratorLabel, published[name],
			"%s published on the orchestrator although unrelated content moved", name)
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

// executionOriginEnv identifies the fixture's bare remote to hook scripts.
const executionOriginEnv = "DISPAT_IT_EXECUTION_ORIGIN"

// executionLockCode reports a lost or unreadable release lock.
const executionLockCode = "E336"

func executionPublishedPackages(rig *executionRig) []string {
	published := executionProbeValues(rig, "publish")
	names := make([]string, 0, len(published))
	for name := range published {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

const executionTokenEnv = "DISPAT_IT_EXECUTION_TOKEN"

// The orchestrator keeps its publication secret out of worker mailboxes and logs.
func TestExecutionSecretNeverReachesMailboxOrLogs(t *testing.T) {
	rig := newExecutionOrchestratedPublishRig(t, func(cfg *models.File) {
		cfg.LogLevel = "trace"
		cfg.Env = map[string]string{"REGISTRY_TOKEN": "$" + executionTokenEnv}
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript +
			` && test "$REGISTRY_TOKEN" = "$DISPAT_IT_EXECUTION_TOKEN"`}
	})
	keep := newExecutionMailboxSnapshot(t, rig)
	workers := rig.startWorkers([]string{executionNode, executionSecondNode}, 2)

	res := rig.release(executionTokenEnv + "=" + executionOrchestratorSecret)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	for _, name := range executionWorkspacePackages {
		assert.Equal(t, executionOrchestratorLabel, executionProbeValues(rig, "publish")[name],
			"%s published using the orchestrator's token", name)
	}
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

// A local publication failure leaves the failed package unrecorded and lets
// independent packages continue after workers supplied their build outputs.
func TestExecutionOrchestratorPublishFailureKeepsLocalSemantics(t *testing.T) {
	for name, row := range map[string]struct {
		scripts func(*models.File)
	}{
		"the publish command exits non-zero": {
			scripts: func(cfg *models.File) {
				cfg.Scripts["publish"] = models.Script{executionRemotePublishScript +
					` && { [ "$DISPAT_PACKAGE" != ui ] || exit 7; }`}
			},
		},
		"the beforePublish hook fails locally": {
			scripts: func(cfg *models.File) {
				cfg.Scripts["prepublish"] = models.Script{executionPrePublishProbe +
					` && { [ "$DISPAT_PACKAGE" != ui ] || exit 3; }`}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newExecutionOrchestratedPublishRig(t, func(cfg *models.File) {
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
			assert.Empty(t, executionAuthorizations(res),
				"orchestrator publication needs no worker authorization\nstdout:\n%s", res.Stdout)
			stopAll(t, workers)
		})
	}
}

// The local publish command's exports reach the orchestrator's recorders and
// postPublish hook after the workers' build outputs have been admitted.
func TestExecutionPublishExportsReachTheRecorders(t *testing.T) {
	server, bodies := githubFake(t)
	defer server.Close()
	rig := newExecutionOrchestratedPublishRig(t, func(cfg *models.File) {
		cfg.GitHub = &models.GitHubConfig{
			Enabled: models.Bool(true), APIURL: server.URL,
			Owner: "acme", Repo: "fixture", TokenEnv: executionGitHubTokenEnv,
		}
		cfg.Scripts["publish"] = models.Script{executionRemotePublishScript + ` && ` +
			`printf 'DISPAT_EXPORT_GITHUB=\n' >> "$DISPAT_OUTPUT" && ` +
			`printf 'PUBLISHED_AT=%s\n' "${DISPAT_EXECUTION_NODE:-orchestrator}" >> "$DISPAT_OUTPUT"`}
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
// The release lock is a tag rather than a record, so a scenario asserting
// that nothing was recorded must exclude it from the tag list.
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

// executionProbedPackages is every package one of the fixture's probes fired
// for, sorted, so that a claim about which commands ran is a claim about a set.
func executionProbedPackages(rig *executionRig, probe string) []string {
	var names []string
	for _, run := range rig.runs() {
		if run.Node == executionProbePrefix+probe {
			names = append(names, run.Package)
		}
	}
	return names
}
