// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the same distributed release, driven through all three ways dispat
// can hold a history (CCME §28.1, "Workers MAY be added to every history
// mode").
//
// One workspace shape is released three times: in a single repository, in a
// control repository whose sources are submodules, and in a fleet of linked
// peers. The packages, the dependency edges, the scripts and the worker links
// are the same in all three; what differs is which repository owns which
// package, and therefore which repository a node has to materialize, which
// repository a tag belongs in, and which repository records the run.
//
// Every claim here is a comparison. A release that delegates work is read
// against a twin of the same fixture released with no worker links at all, in
// the same mode: same packages, same versions, same tags in the same
// repositories, same files in the release commits. An execution link is not a
// fleet link (vector 3), and the way to prove that is to show that adding one
// changes nothing a reader of the history can see.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The packages every mode is built from: one provider that writes a declared
// build output, and two consumers of it that read those exact bytes. Two
// consumers rather than one because the pool prefers the least loaded node and
// then the first by name, so a single consumer would always land where the
// provider did: two consumers placed together on two nodes of one slot each
// are how a run is made to carry bytes between machines rather than between
// folders.
const (
	executionModeProvider  = "prov"
	executionModeConsumerA = "conza"
	executionModeConsumerB = "conzb"
)

// executionModePackages is the fixture's whole package set, in a stable order.
var executionModePackages = []string{executionModeProvider, executionModeConsumerA, executionModeConsumerB}

// executionModeFleet is one history mode's world: the checkout a release is
// started in, where each repository's checkout sits inside it, and the mailbox
// the nodes are reached at.
//
// The repository paths are what every assertion about a distributed run in a
// multi-repository mode is made against: a tag belongs in the repository that
// owns the package, and a node has to reproduce the same relative layout, so
// the test has to know that layout as well as the run does.
type executionModeFleet struct {
	t       *testing.T
	entry   *harness.Repo
	mailbox string
	// builds is the file every build script appends a line to, outside every
	// checkout, because the folder a node built in is removed when the task
	// ends.
	builds string
	// checkouts is where each repository sits relative to the entry, "." for
	// the entry itself, keyed by the name the fixture calls the repository.
	checkouts map[string]string
	// owners names the repository each package belongs to.
	owners map[string]string
	// remotes is each repository's own bare remote, which is where its
	// release lock lives, keyed as the checkouts are.
	remotes map[string]string
	// gate is the rendezvous point the fixture's build scripts meet at. It is
	// the fleet's own rather than one invocation's, because the scripts run in
	// the node processes as well as in the release, and a meeting only half
	// the participants know the address of is no meeting.
	gate string
	// extra is what every invocation of this mode needs beyond the release
	// lock and the secret: the file-transport permission a fleet of submodule
	// links cannot be assembled without.
	extra []string
}

// executionModeNames are the three modes, in the order the tables run them.
var executionModeNames = []string{"single history", "control repository", "linked peers"}

// newExecutionModeFleet builds one mode's fixture. isDistributed decides
// whether the entry configuration carries worker links at all, which is the
// only difference between a run under test and the twin it is read against.
func newExecutionModeFleet(t *testing.T, mode string, isDistributed bool) *executionModeFleet {
	t.Helper()
	switch mode {
	case "single history":
		return newExecutionSingleFleet(t, isDistributed)
	case "control repository":
		return newExecutionControlFleet(t, isDistributed)
	case "linked peers":
		return newExecutionLinkedFleet(t, isDistributed)
	}
	t.Fatalf("no such history mode %q", mode)
	return nil
}

// executionModeExecution is the execution object a mode's entry configuration
// carries, or nil for the twin that delegates nothing.
func executionModeExecution(mailbox string, isDistributed bool, names ...string) *models.ExecutionConfig {
	if !isDistributed {
		return nil
	}
	if len(names) == 0 {
		names = []string{executionNode, executionSecondNode}
	}
	workers := make([]models.ExecutionWorkerConfig, 0, len(names))
	for _, name := range names {
		workers = append(workers, models.ExecutionWorkerConfig{Name: name, Endpoint: "file://" + mailbox})
	}
	return &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   workers,
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
}

// executionModeScripts is the build and publish scripts every mode shares.
func executionModeScripts(provider string) map[string]models.Script {
	return map[string]models.Script{
		"prov-build": {executionModeProviderBuild},
		"cons-build": {executionModeConsumerBuild(provider)},
		"publish":    {executionModePublish},
	}
}

// executionModeProviderBuild writes the declared output the consumers read,
// naming the node it ran on so that a copy of those bytes can be traced back
// to the machine that made them.
const executionModeProviderBuild = executionRecordingScript + ` &&
mkdir -p dist && printf '%s %s %s\n' "$DISPAT_PACKAGE" "$DISPAT_NEW_VERSION" \
  "${DISPAT_EXECUTION_NODE:-orchestrator}" > dist/bundle.txt`

// executionModeConsumerBuild refuses to build without the provider's exact
// bytes, at the path the provider's folder has relative to this one, and
// records what it read. The two consumers meet at a rendezvous first, so that
// a release which completes at all is a release whose consumers were in flight
// together and therefore on two machines.
func executionModeConsumerBuild(providerPath string) string {
	return executionRecordingScript + " &&\n" +
		`{ touch "$DISPAT_IT_EXECUTION_GATE.$DISPAT_PACKAGE"; i=0; ` +
		`while [ "$(ls "$DISPAT_IT_EXECUTION_GATE".* 2>/dev/null | wc -l)" -lt 2 ]; do ` +
		`[ $i -lt 600 ] || exit 7; sleep 0.05; i=$((i+1)); done; } &&
test -f ` + providerPath + `/dist/bundle.txt &&
mkdir -p dist && cp ` + providerPath + `/dist/bundle.txt dist/from-provider.txt &&
printf '%s %s %s\n' probe-inputs "$DISPAT_PACKAGE" \
  "$(tr ' ' '_' < dist/from-provider.txt | tr -d '\n')" >> "$DISPAT_IT_EXECUTION_LOG"`
}

// executionModePublish records, on the orchestrator, what the package's folder
// holds when its publish stage runs: the stages that stay at home read the
// working tree, so the bytes a node produced have to be here.
const executionModePublish = `printf '%s %s %s\n' probe-publish "$DISPAT_PACKAGE" ` +
	`"$(cat dist/* 2>/dev/null | tr ' ' '_' | tr -d '\n')" >> "$DISPAT_IT_EXECUTION_LOG"`

// newExecutionSingleFleet is mode S: one repository holding every package.
func newExecutionSingleFleet(t *testing.T, isDistributed bool) *executionModeFleet {
	t.Helper()
	repo := harness.New(t)
	mailbox := executionMailbox(t)
	cfg := harness.BaseFile(3, 3)
	cfg.Scripts = executionModeScripts("../../providers/" + executionModeProvider)
	cfg.Spaces = map[string]models.SpaceConfig{
		"providers": {Path: models.PathList{"packages/providers"}, BuildOutputs: []string{"dist"},
			Flow: &models.SpaceFlowConfig{Build: []string{"prov-build"}, Publish: []string{"publish"}}},
		"consumers": {Path: models.PathList{"packages/consumers"}, BuildOutputs: []string{"dist"},
			Flow: &models.SpaceFlowConfig{Build: []string{"cons-build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = executionModeDependencies()
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Branch: harness.DefaultBranch}
	cfg.Execution = executionModeExecution(mailbox, isDistributed)
	repo.SeedPackage("packages/providers", executionModeProvider)
	for _, name := range []string{executionModeConsumerA, executionModeConsumerB} {
		repo.SeedPackage("packages/consumers", name)
	}
	repo.WriteConfigModel(cfg)
	repo.WriteFile(".gitignore", "dist/\n")
	repo.Commit(fmt.Sprintf("feat(%s): bootstrap the workspace", strings.Join(executionModePackages, ",")))
	repo.AddBareRemote()
	repo.Git("push", "-q", "origin", harness.DefaultBranch)
	return &executionModeFleet{t: t, entry: repo, mailbox: mailbox,
		builds:    filepath.Join(t.TempDir(), "builds.log"),
		gate:      filepath.Join(t.TempDir(), "consumers.gate"),
		checkouts: map[string]string{"entry": "."},
		owners: map[string]string{executionModeProvider: "entry",
			executionModeConsumerA: "entry", executionModeConsumerB: "entry"},
	}
}

// executionModeDependencies is the edge set every mode declares: both
// consumers read the provider.
func executionModeDependencies() []models.DependencyConfig {
	return []models.DependencyConfig{
		{Consumer: executionModeConsumerA, Provider: executionModeProvider},
		{Consumer: executionModeConsumerB, Provider: executionModeProvider},
	}
}

// newExecutionControlFleet is mode C: a control repository whose two sources
// are submodules, the provider in one and both consumers in the other.
func newExecutionControlFleet(t *testing.T, isDistributed bool) *executionModeFleet {
	t.Helper()
	alpha := harness.New(t)
	alpha.SeedPackage("packages", executionModeProvider)
	alpha.Commit("feat(" + executionModeProvider + "): bootstrap the provider")
	alphaRemote := alpha.AddBareRemote()
	alpha.Git("push", "-q", "origin", harness.DefaultBranch)

	beta := harness.New(t)
	for _, name := range []string{executionModeConsumerA, executionModeConsumerB} {
		beta.SeedPackage("packages", name)
	}
	beta.Commit(fmt.Sprintf("feat(%s,%s): bootstrap the consumers",
		executionModeConsumerA, executionModeConsumerB))
	betaRemote := beta.AddBareRemote()
	beta.Git("push", "-q", "origin", harness.DefaultBranch)

	control := harness.New(t)
	mailbox := executionMailbox(t)
	addPolyrepoSource(t, control, "alpha", "sources/alpha", alpha)
	addPolyrepoSource(t, control, "beta", "sources/beta", beta)
	control.Git("-C", "sources/alpha", "remote", "set-url", "origin", alphaRemote)
	control.Git("-C", "sources/beta", "remote", "set-url", "origin", betaRemote)
	cfg := harness.BaseFile(3, 3)
	cfg.Polyrepo = true
	cfg.Scripts = executionModeScripts("../../../alpha/packages/" + executionModeProvider)
	cfg.Spaces = map[string]models.SpaceConfig{
		"providers": {Path: models.PathList{"sources/alpha/packages"}, BuildOutputs: []string{"dist"},
			Flow: &models.SpaceFlowConfig{Build: []string{"prov-build"}, Publish: []string{"publish"}}},
		"consumers": {Path: models.PathList{"sources/beta/packages"}, BuildOutputs: []string{"dist"},
			Flow: &models.SpaceFlowConfig{Build: []string{"cons-build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = executionModeDependencies()
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Branch: harness.DefaultBranch}
	cfg.Execution = executionModeExecution(mailbox, isDistributed)
	control.WriteConfigModel(cfg)
	control.WriteFile(".gitignore", "dist/\n")
	control.Commit("chore: assemble the control repository")
	control.AddBareRemote()
	control.Git("push", "-q", "origin", harness.DefaultBranch)
	return &executionModeFleet{t: t, entry: control, mailbox: mailbox,
		builds:    filepath.Join(t.TempDir(), "builds.log"),
		gate:      filepath.Join(t.TempDir(), "consumers.gate"),
		checkouts: map[string]string{"control": ".", "alpha": "sources/alpha", "beta": "sources/beta"},
		owners: map[string]string{executionModeProvider: "alpha",
			executionModeConsumerA: "beta", executionModeConsumerB: "beta"},
		extra: fileProtocolEnv(),
	}
}

// newExecutionLinkedFleet is mode L: two peers linked both ways, the provider
// owned by the peer the release is started in and both consumers by the other.
//
// The fleet is assembled by the choreography fixture's own helpers, so that
// what a distributed release is driven through here is exactly the fleet every
// other choreographed scenario is driven through. Its seeded packages are
// replaced by this fixture's own, under a subject that releases nothing, so
// the two peers hold the provider and the consumers rather than a package
// named after themselves.
func newExecutionLinkedFleet(t *testing.T, isDistributed bool) *executionModeFleet {
	t.Helper()
	mailbox := executionMailbox(t)
	fleet := newChoreographyFleet(t, "alpha", "beta")
	executionModeSeedPeer(fleet, "alpha", executionModeProvider)
	executionModeSeedPeer(fleet, "beta", executionModeConsumerA, executionModeConsumerB)
	fleet.writeConfig("alpha", func(cfg *models.File) {
		executionModeLinkedConfig(cfg, true)
		cfg.Execution = executionModeExecution(mailbox, isDistributed)
	})
	fleet.peer("alpha").WriteFile(".gitignore", "dist/\n")
	fleet.peer("alpha").Commit("feat(" + executionModeProvider + "): hold the provider of this fleet")
	fleet.push("alpha")
	fleet.writeConfig("beta", func(cfg *models.File) {
		executionModeLinkedConfig(cfg, false)
		cfg.Dependencies = executionModeDependencies()
	})
	fleet.peer("beta").WriteFile(".gitignore", "dist/\n")
	fleet.peer("beta").Commit(fmt.Sprintf("feat(%s,%s): hold the consumers of this fleet",
		executionModeConsumerA, executionModeConsumerB))
	fleet.push("beta")
	fleet.link("alpha", "beta")
	return &executionModeFleet{t: t, entry: fleet.peer("alpha").Repo, mailbox: mailbox,
		builds:    filepath.Join(t.TempDir(), "builds.log"),
		gate:      filepath.Join(t.TempDir(), "consumers.gate"),
		checkouts: map[string]string{"alpha": ".", "beta": ".links/beta"},
		owners: map[string]string{executionModeProvider: "alpha",
			executionModeConsumerA: "beta", executionModeConsumerB: "beta"},
		extra: fileProtocolEnv(),
	}
}

// executionModeSeedPeer replaces one peer's seeded package with the packages
// this fixture needs it to own.
func executionModeSeedPeer(fleet *choreographyFleet, name string, packages ...string) {
	peer := fleet.peer(name)
	peer.Git("rm", "-r", "-q", "--", filepath.ToSlash(filepath.Join("packages", peer.pkg)))
	for _, pkg := range packages {
		peer.SeedPackage("packages", pkg)
	}
}

// executionModeLinkedConfig is one peer's own configuration in mode L: the
// shared scripts, and the space holding whichever half of the fixture this
// peer owns.
func executionModeLinkedConfig(cfg *models.File, isProvider bool) {
	cfg.Concurrency = []int{3, 3}
	cfg.Scripts = executionModeScripts("../../../../packages/" + executionModeProvider)
	// The fixture replaced the packages the choreography fleet seeds, so the
	// subjects that bootstrapped this fleet name scopes no package answers to.
	// Declaring them keeps that assembly out of the diagnostics of every run.
	cfg.NonPackageScopes = []string{"release", "alpha-pkg", "beta-pkg"}
	if isProvider {
		cfg.Spaces = map[string]models.SpaceConfig{
			"providers": {Path: models.PathList{"packages"}, BuildOutputs: []string{"dist"},
				Flow: &models.SpaceFlowConfig{Build: []string{"prov-build"}, Publish: []string{"publish"}}},
		}
		return
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"consumers": {Path: models.PathList{"packages"}, BuildOutputs: []string{"dist"},
			Flow: &models.SpaceFlowConfig{Build: []string{"cons-build"}, Publish: []string{"publish"}}},
	}
}

// env is what every invocation of this mode needs: the release lock back on,
// the signing secret, the build log, the rendezvous point and whatever the
// mode itself requires.
func (f *executionModeFleet) env(extra ...string) []string {
	return append(append(append(append([]string{},
		harness.LockEnabled...), f.extra...), f.scriptEnv()...), extra...)
}

// scriptEnv is what the fixture's own scripts need wherever they run: the
// signing secret for the process that dispatches, the log every build records
// itself in, and the rendezvous point the consumer builds meet at.
func (f *executionModeFleet) scriptEnv() []string {
	return []string{
		executionSecretEnv + "=" + executionSecret,
		executionBuildLogEnv + "=" + f.builds,
		executionGateEnv + "=" + f.gate,
	}
}

// release runs this mode's release from its entry checkout.
func (f *executionModeFleet) release(extra ...string) harness.RunResult {
	f.t.Helper()
	return f.entry.CommandEnv(f.env(extra...), "--package", "*")
}

// status reads this mode's plan without executing anything.
func (f *executionModeFleet) status(extra ...string) harness.RunResult {
	f.t.Helper()
	return f.entry.CommandEnv(f.env(extra...), "status", "--package", "*", "--log-format", "json")
}

// startWorkers starts one node per name against this mode's mailbox, each with
// the build log and whatever else the scenario hands them.
func (f *executionModeFleet) startWorkers(names []string, capacity int, extraEnv ...string) []*executionWorker {
	f.t.Helper()
	workers := make([]*executionWorker, 0, len(names))
	for _, name := range names {
		cfg := executionWorkerConfig(f.mailbox, func(settings *models.ExecutionConfig) {
			settings.Name = name
			settings.Concurrency = models.Int(capacity)
		})
		workers = append(workers, startWorker(f.t, f.entry, cfg, 0,
			append(f.scriptEnv(), extraEnv...)...))
	}
	return workers
}

// runs are the script executions this fixture recorded, in order.
func (f *executionModeFleet) runs() []executionRun {
	f.t.Helper()
	content, err := os.ReadFile(f.builds)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(f.t, err)
	var recorded []executionRun
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		recorded = append(recorded, executionRun{Node: fields[0], Package: fields[1], Dir: fields[2]})
	}
	return recorded
}

// nodesByPackage is which node built each package.
func (f *executionModeFleet) nodesByPackage() map[string]string {
	placed := map[string]string{}
	for _, run := range f.runs() {
		if strings.HasPrefix(run.Node, executionProbePrefix) {
			continue
		}
		placed[run.Package] = run.Node
	}
	return placed
}

// probeValues is what one probe recorded for every package.
func (f *executionModeFleet) probeValues(probe string) map[string]string {
	recorded := map[string]string{}
	for _, run := range f.runs() {
		if run.Node == executionProbePrefix+probe {
			recorded[run.Package] = run.Dir
		}
	}
	return recorded
}

// buildCount is how many times one package's build script ran anywhere.
func (f *executionModeFleet) buildCount(packageName string) int {
	ran := 0
	for _, run := range f.runs() {
		if run.Package == packageName && !strings.HasPrefix(run.Node, executionProbePrefix) {
			ran++
		}
	}
	return ran
}

// tags are the release tags each repository of this fixture holds, keyed by
// the fixture's name for the repository. A tag belongs in the repository that
// owns the package whatever machine built it (§27.7), so this is the map every
// comparison between a delegating run and its twin is made over.
func (f *executionModeFleet) tags() map[string][]string {
	f.t.Helper()
	held := map[string][]string{}
	for name, path := range f.checkouts {
		listed := f.entry.Git("-C", path, "tag", "--list")
		if strings.TrimSpace(listed) == "" {
			held[name] = nil
			continue
		}
		found := strings.Split(strings.TrimSpace(listed), "\n")
		sort.Strings(found)
		held[name] = found
	}
	return held
}

// releaseCommitFiles is the set of paths every commit this run added to one
// repository touches, sorted. It is what a release leaves in a history:
// changelog entries, version edits and the settlement of a fleet link, with
// the commit ids and the ordering left out so that two independent fixtures
// can be compared at all.
func (f *executionModeFleet) releaseCommitFiles(repository, since string) []string {
	f.t.Helper()
	path := f.checkouts[repository]
	listed := f.entry.Git("-C", path, "diff", "--name-only", since, "HEAD")
	if strings.TrimSpace(listed) == "" {
		return nil
	}
	found := strings.Split(strings.TrimSpace(listed), "\n")
	sort.Strings(found)
	return found
}

// heads is each repository's current head, which is what a scenario records
// before a run so that it can ask what the run added afterwards.
func (f *executionModeFleet) heads() map[string]string {
	f.t.Helper()
	found := map[string]string{}
	for name, path := range f.checkouts {
		found[name] = strings.TrimSpace(f.entry.Git("-C", path, "rev-parse", "HEAD"))
	}
	return found
}

// planShape is the graph `dispat status` reports, one line per package, with
// everything that is not the plan left out.
func executionModePlanShape(t *testing.T, res harness.RunResult) []string {
	t.Helper()
	var shape []string
	for _, event := range executionEvents(res) {
		if event.Package() == "" {
			continue
		}
		shape = append(shape, strings.Join([]string{
			event.Package(), event.Str("message"), event.Str("version"), event.Str("reason"),
		}, "|"))
	}
	sort.Strings(shape)
	return shape
}

// TestExecutionModesPlanIsIndependentOfWorkers is conformance vector 3 in all
// three history modes: worker endpoint links are execution links, so adding
// them alters no source discovery, no repository identity and no release
// scope. The plan a reader is shown is the plan they were always shown, and
// the name the run fixes for it does not follow how many machines will
// execute it or what they are called.
func TestExecutionModesPlanIsIndependentOfWorkers(t *testing.T) {
	for _, mode := range executionModeNames {
		t.Run(mode, func(t *testing.T) {
			local := newExecutionModeFleet(t, mode, false)
			localPlan := local.status()
			require.Equal(t, 0, localPlan.Code, "stdout:\n%s\nstderr:\n%s", localPlan.Stdout, localPlan.Stderr)
			require.NotEmpty(t, executionModePlanShape(t, localPlan), "the fixture plans something")
			_, isFixed := executionLine(localPlan, "plan fixed")
			assert.False(t, isFixed, "a fleet with no worker links computes no digest and says nothing about one")

			distributed := newExecutionModeFleet(t, mode, true)
			withWorkers := distributed.status()
			require.Equal(t, 0, withWorkers.Code, "stdout:\n%s\nstderr:\n%s",
				withWorkers.Stdout, withWorkers.Stderr)
			assert.Equal(t, executionModePlanShape(t, localPlan), executionModePlanShape(t, withWorkers),
				"the same packages at the same versions for the same reasons")

			// The digest is compared within one fixture rather than between
			// two: two independently assembled fleets hold the same files at
			// different commits, and the heads are part of what the digest
			// names.
			want := planDigestOf(t, withWorkers)
			distributed.writeEntryExecution(executionModeExecution(distributed.mailbox, true,
				"zulu", executionNode, "mike"))
			assert.Equal(t, want, planDigestOf(t, distributed.status()),
				"three differently named links in another order name the same plan")
			distributed.writeEntryExecution(executionModeExecution(distributed.mailbox, true, executionNode))
			assert.Equal(t, want, planDigestOf(t, distributed.status()),
				"and so does one")
		})
	}
}

// TestExecutionModesReleaseAsTheirTwinWithoutWorkers is the other half of
// vector 3, made against a release rather than against a plan: the same
// fixture is released twice in each history mode, once across two machines and
// once with no worker links at all, and the two histories are compared.
//
// What is compared is what a later run reads: the tags each repository holds,
// the files every commit the release added touches, and the evidence the mode
// itself owns — the control repository's checkpoint of each source, and the
// settlement of a fleet link. None of it may mention a machine.
func TestExecutionModesReleaseAsTheirTwinWithoutWorkers(t *testing.T) {
	for _, mode := range executionModeNames {
		t.Run(mode, func(t *testing.T) {
			local := newExecutionModeFleet(t, mode, false)
			localBefore := local.heads()
			localRes := local.release()
			require.Equal(t, 0, localRes.Code, "stdout:\n%s\nstderr:\n%s", localRes.Stdout, localRes.Stderr)

			distributed := newExecutionModeFleet(t, mode, true)
			before := distributed.heads()
			workers := distributed.startWorkers([]string{executionNode, executionSecondNode}, 1)

			res := distributed.release()
			replies := stopAll(t, workers)

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s\nnode:\n%s", res.Stdout, res.Stderr, replies[0].Stdout+replies[1].Stdout)
			placed := distributed.nodesByPackage()
			for _, name := range executionModePackages {
				assert.Contains(t, []string{executionNode, executionSecondNode}, placed[name],
					"%s built on a node rather than here (runs: %v)", name, distributed.runs())
			}
			assert.Equal(t, local.tags(), distributed.tags(),
				"every tag is in the repository that owns its package, exactly as without workers")
			for repository := range distributed.checkouts {
				assert.Equal(t,
					local.releaseCommitFiles(repository, localBefore[repository]),
					distributed.releaseCommitFiles(repository, before[repository]),
					"the release wrote the same files in %s as it does without workers", repository)
				assert.Empty(t, distributed.transportSubjects(repository, before[repository]),
					"no transport commit entered %s's history", repository)
			}
			executionModeEvidence(t, mode, local, localRes, distributed, res)
			assert.Empty(t, executionMailboxBranches(t, distributed.mailbox),
				"the run closed every coordination branch it created")
		})
	}
}

// executionModeEvidence checks what the mode itself records about a release,
// against the same fixture released without workers.
//
// A control repository checkpoints each source it released, and a linked peer
// settles the revision of every peer it incorporated. Both are written by the
// orchestrator in the owning repository (§27.7), so a run that delegated every
// build has to leave exactly what a run that delegated none leaves.
func executionModeEvidence(t *testing.T, mode string, local *executionModeFleet,
	localRes harness.RunResult, distributed *executionModeFleet, res harness.RunResult) {
	t.Helper()
	switch mode {
	case "control repository":
		for _, source := range []string{"alpha", "beta"} {
			head := strings.TrimSpace(distributed.entry.Git("-C", distributed.checkouts[source], "rev-parse", "HEAD"))
			assert.Equal(t, head, gitlinkAt(distributed.entry, "HEAD", "sources/"+source),
				"the control repository checkpointed the head %s really has", source)
		}
	case "linked peers":
		require.NotEmpty(t, settledPins(localRes), "the twin settled what it incorporated")
		assert.Equal(t, executionModeSettledRepositories(localRes), executionModeSettledRepositories(res),
			"the delegating run settled the same links as the twin")
		pin := gitlinkAt(distributed.entry, "HEAD", ".links/beta")
		require.NotEmpty(t, pin, "the entry's head records the peer it incorporated")
		assert.Contains(t, distributed.entry.Git("-C", ".links/beta", "rev-list", "HEAD"), pin,
			"and what it recorded is a revision of that peer rather than anything a task wrote")
	default:
		assert.Empty(t, settledPins(res), "a single history settles no fleet link")
		_ = local
	}
}

// executionModeSettledRepositories are the repositories a run recorded fleet
// links in, sorted, which is the comparable part of a settlement: the
// revisions themselves belong to the fixture that produced them.
func executionModeSettledRepositories(res harness.RunResult) []string {
	var found []string
	for _, event := range settledPins(res) {
		found = append(found, event.Str("repository"))
	}
	sort.Strings(found)
	return found
}

// transportSubjects are the commits one repository gained that a coordination
// branch put there, which must always be none: a task's transport commits are
// objects in a mailbox and never release evidence (§27.7, vector 12).
func (f *executionModeFleet) transportSubjects(repository, since string) []string {
	f.t.Helper()
	listed := f.entry.Git("-C", f.checkouts[repository], "log", "--format=%s", since+"..HEAD")
	var found []string
	for _, subject := range strings.Split(listed, "\n") {
		if strings.HasPrefix(strings.TrimSpace(subject), "dispat transport") {
			found = append(found, subject)
		}
	}
	return found
}

// TestExecutionFleetBuildSeesProviderOutputsAcrossRepositories: in a fleet the
// provider and its consumers live in different repositories, so a node running
// a consumer's build has to be handed both of them, each at the path it has on
// the machine the release was started on.
//
// The consumer's build reads the provider through that relative path and
// nothing else, so a checkout laid out any other way fails the build rather
// than falling back on anything. The two consumers meet at a rendezvous, which
// puts them on two nodes of one slot each: whichever of them is not on the
// node that built the provider read bytes that crossed both a repository
// boundary and a machine boundary.
func TestExecutionFleetBuildSeesProviderOutputsAcrossRepositories(t *testing.T) {
	for _, mode := range []string{"control repository", "linked peers"} {
		t.Run(mode, func(t *testing.T) {
			fleet := newExecutionModeFleet(t, mode, true)
			workers := fleet.startWorkers([]string{executionNode, executionSecondNode}, 1)

			res := fleet.release()
			replies := stopAll(t, workers)

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			placed := fleet.nodesByPackage()
			assert.Equal(t, 1, fleet.buildCount(executionModeProvider),
				"the provider was built once for both consumers: %v", fleet.runs())
			assert.NotEqual(t, placed[executionModeConsumerA], placed[executionModeConsumerB],
				"the two consumers built on two machines: %v", fleet.runs())
			elsewhere := executionModeConsumerA
			if placed[executionModeConsumerA] == placed[executionModeProvider] {
				elsewhere = executionModeConsumerB
			}
			assert.NotEqual(t, placed[executionModeProvider], placed[elsewhere],
				"%s built on a machine that never built the provider", elsewhere)

			produced := fleet.probeValues("inputs")
			require.Contains(t, produced, elsewhere, "the consumer on the other machine read something")
			assert.Contains(t, produced[elsewhere], placed[executionModeProvider],
				"and what it read is the bytes the provider's own machine wrote")
			assert.Equal(t, produced[executionModeConsumerA], produced[executionModeConsumerB],
				"both consumers read the same bytes, whichever machine they were on")
			assert.Equal(t, produced[elsewhere], fleet.probeValues("publish")[elsewhere],
				"and the orchestrator's own checkout holds them when the publish stage reads it")

			for _, reply := range replies {
				assert.Equal(t, 0, reply.Code, "the nodes carried on serving")
			}
			assert.Empty(t, executionMailboxBranches(t, fleet.mailbox))
		})
	}
}

// The three peers of the contention fixture. The names are chosen for their
// order: locks are acquired in repository-name order, so a second entry has to
// reach the contended repository last if it is to have a partial set to give
// back at all.
const (
	executionEntryFirst     = "aaa"
	executionEntrySecond    = "mmm"
	executionEntryContested = "zzz"
)

// executionEntriesBuild records that a build started and then waits for the
// scenario to let it finish, which is how a run is held inside a remote build
// while it owns every lock it took.
const executionEntriesBuild = executionRecordingScript + ` &&
{ i=0; while [ ! -f "$DISPAT_IT_EXECUTION_GATE" ]; do ` +
	`[ $i -lt 1200 ] || exit 7; sleep 0.05; i=$((i+1)); done; }`

// TestExecutionConcurrentEntriesOneAcquiresAllLocks is conformance vector 4a
// under §28.3: two CI entry nodes address overlapping repository sets, and at
// most one of them acquires the complete set and dispatches effects.
//
// The fleet is three linked peers, two of which are entry points sharing the
// third. The first run is held inside a build on one of its nodes while it
// owns both of its locks; the second run enters from the other peer, takes the
// lock that is free, finds the shared one taken, and has to give back what it
// took without having dispatched anything at all.
func TestExecutionConcurrentEntriesOneAcquiresAllLocks(t *testing.T) {
	first, second := newExecutionEntriesFleet(t)
	workers := append(first.startWorkers([]string{executionNode}, 2),
		second.startWorkers([]string{executionNode}, 2)...)
	running := first.entry.StartCommandEnv(first.env(), "--package", "*")
	executionAwaitBuild(t, first)

	held := lockObject(t, first.remotes[executionEntryContested])
	blocked := second.release()

	require.Equal(t, 1, blocked.Code, "stdout:\n%s\nstderr:\n%s", blocked.Stdout, blocked.Stderr)
	assert.True(t, harness.IsCodePresent(blocked.Events, "E336"),
		"the second entry is refused by the lock it could not take\nstdout:\n%s\nstderr:\n%s",
		blocked.Stdout, blocked.Stderr)
	assert.Equal(t, held, lockObject(t, first.remotes[executionEntryContested]),
		"the run that holds the shared repository keeps its own lock")
	assert.False(t, remoteHoldsLock(t, second.remotes[executionEntrySecond]),
		"and the refused run gave back the lock it had already taken")
	assert.Empty(t, executionMailboxBranches(t, second.mailbox),
		"a run that never owned the complete set dispatched nothing")
	_, isFixed := executionLine(blocked, "plan fixed")
	assert.False(t, isFixed, "it never got as far as fixing a plan")

	require.NoError(t, os.WriteFile(first.gate, nil, 0o644))
	res := running.Wait()
	stopAll(t, workers)

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.False(t, remoteHoldsLock(t, first.remotes[executionEntryContested]),
		"the run that completed gave every lock back")
	assert.False(t, remoteHoldsLock(t, first.remotes[executionEntryFirst]))
	assert.Equal(t, []string{executionEntryFirst + "-pkg@0.1.0"}, first.entry.TagList())
	assert.Equal(t, []string{executionEntryContested + "-pkg@0.1.0"},
		tagsIn(first.entry, ".links/"+executionEntryContested))
	assert.Empty(t, second.entry.TagList(), "and the refused run released nothing")
}

// executionAwaitBuild waits until a build of the run under way has started on
// a node, which is the moment that run owns every lock it will take and has
// work in flight.
func executionAwaitBuild(t *testing.T, fleet *executionModeFleet) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		for _, run := range fleet.runs() {
			if !strings.HasPrefix(run.Node, executionProbePrefix) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no build of the first run started within the deadline: %v", fleet.runs())
}

// newExecutionEntriesFleet builds the contention fixture and answers the two
// entry points as fleets of their own: three linked peers, two of which may
// start a release and both of which reach the third.
//
// Each entry has a mailbox of its own, so "this run dispatched nothing" is a
// question about one mailbox rather than a question about whose branches those
// are.
func newExecutionEntriesFleet(t *testing.T) (first, second *executionModeFleet) {
	t.Helper()
	fleet := newChoreographyFleet(t, executionEntryFirst, executionEntrySecond, executionEntryContested)
	mailboxes := map[string]string{
		executionEntryFirst: executionMailbox(t), executionEntrySecond: executionMailbox(t),
	}
	for _, name := range []string{executionEntryFirst, executionEntrySecond, executionEntryContested} {
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.Scripts = map[string]models.Script{
				"build": {executionEntriesBuild}, "publish": {"echo publishing"},
			}
			cfg.Repositories = executionEntriesLinks(fleet, name)
			cfg.Execution = executionModeExecution(mailboxes[name], mailboxes[name] != "", executionNode)
		})
		fleet.peer(name).Commit("chore: declare what this peer reaches")
		fleet.push(name)
	}
	// One-sided links, because the contention is about the repository sets two
	// entries address rather than about what they record in each other: a
	// shared repository that linked both entries back would put each of them
	// in the other's set, and there would be no partial acquisition left to
	// give back.
	fleet.linkOneWay(executionEntryFirst, executionEntryContested)
	fleet.linkOneWay(executionEntrySecond, executionEntryContested)
	remotes := map[string]string{}
	for _, name := range fleet.names {
		remotes[name] = fleet.peer(name).remote
	}
	gate := filepath.Join(t.TempDir(), "builds.gate")
	return executionEntriesEntry(t, fleet, executionEntryFirst, gate, mailboxes, remotes),
		executionEntriesEntry(t, fleet, executionEntrySecond, gate, mailboxes, remotes)
}

// executionEntriesLinks is the roster one peer declares: the shared repository
// for the two entry points, and both of them for the shared one.
func executionEntriesLinks(fleet *choreographyFleet, name string) []models.RepositoryLinkConfig {
	reached := []string{executionEntryContested}
	if name == executionEntryContested {
		reached = []string{executionEntryFirst, executionEntrySecond}
	}
	links := make([]models.RepositoryLinkConfig, 0, len(reached))
	for _, other := range reached {
		links = append(links, models.RepositoryLinkConfig{
			Name: other, URL: fleet.peer(other).remote, Branch: harness.DefaultBranch,
		})
	}
	return links
}

// executionEntriesEntry wraps one peer of the contention fixture as the fleet
// a release is started from.
func executionEntriesEntry(t *testing.T, fleet *choreographyFleet, name, gate string,
	mailboxes, remotes map[string]string) *executionModeFleet {
	t.Helper()
	return &executionModeFleet{t: t, entry: fleet.peer(name).Repo, mailbox: mailboxes[name],
		builds:    filepath.Join(t.TempDir(), name+"-builds.log"),
		gate:      gate,
		checkouts: map[string]string{name: ".", executionEntryContested: ".links/" + executionEntryContested},
		remotes:   remotes,
		extra:     fileProtocolEnv(),
	}
}

// TestExecutionControlCheckpointFailureKeepsSourceSuccess is conformance
// vector 15 in the mode it is about: a source tag written by a run that
// delegated every build stays true when the control repository's checkpoint
// of that source then fails.
//
// The distinction the vector draws is between the effect and the record of
// where it landed. The provider really was published, so its tag is not taken
// back and nothing is published a second time; the checkpoint that could not
// be written is reported for what it is, and the consumers of that provider
// are left for the next run.
func TestExecutionControlCheckpointFailureKeepsSourceSuccess(t *testing.T) {
	fleet := newExecutionModeFleet(t, "control repository", true)
	workers := fleet.startWorkers([]string{executionNode, executionSecondNode}, 1)
	// The control repository's own commit is what a checkpoint is, and this is
	// how a commit is made to fail without failing the sources' own.
	require.NoError(t, os.WriteFile(fleet.entry.Path(".git", "hooks", "pre-commit"),
		[]byte("#!/bin/sh\nexit 37\n"), 0o755))

	res := fleet.release()
	stopAll(t, workers)

	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, executionCheckpointCode),
		"the run names the step that failed\nstdout:\n%s", res.Stdout)
	assert.Contains(t, fleet.tags()["alpha"], executionModeProvider+"@0.1.0",
		"the provider's publication remains truthful")
	assert.Equal(t, []string{executionModeProvider},
		executionReleasedPackages(res), "and nothing else was published")
	assert.Empty(t, fleet.tags()["beta"], "the consumers of an unrecorded source are left for the next run")
	assert.Empty(t, executionMailboxBranches(t, fleet.mailbox),
		"the run still closed the branches it created")
}

// executionCheckpointCode is the diagnostic a control repository reports for
// a checkpoint of a source it could not write.
const executionCheckpointCode = "E335"

// TestExecutionSecretNeverReachesMailboxOrLogs: the signing secret is named by
// an environment variable rather than written in a file so that it stays on
// the machines that need it. This is that claim, read from the two places a
// leak would end up in every mode: the mailbox, which anybody who can reach a
// node can read, and the logs of both processes, which a CI system keeps.
//
// Every object the mailbox holds is inspected rather than every branch,
// because a run deletes its branches at the end and a secret in an object
// nothing points at is a secret in the mailbox all the same.
func TestExecutionSecretNeverReachesMailboxOrLogs(t *testing.T) {
	for _, mode := range executionModeNames {
		t.Run(mode, func(t *testing.T) {
			fleet := newExecutionModeFleet(t, mode, true)
			workers := fleet.startWorkers([]string{executionNode, executionSecondNode}, 1)

			res := fleet.release()
			replies := stopAll(t, workers)

			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.NotContains(t, res.Stdout+res.Stderr, executionSecret,
				"the secret reached the orchestrator's log")
			for index, reply := range replies {
				assert.NotContains(t, reply.Stdout+reply.Stderr, executionSecret,
					"the secret reached node %d's log", index)
			}
			carried := executionModeMailboxObjects(t, fleet.mailbox)
			require.NotEmpty(t, carried, "the mailbox really carried this run's messages")
			assert.NotContains(t, carried, executionSecret, "the secret reached the mailbox")
			assert.NotContains(t, carried, executionSecretEnv+"="+executionSecret)
		})
	}
}

// executionModeMailboxObjects is every blob a mailbox holds, as one string.
//
// The objects are enumerated rather than walked from the refs: a finished run
// deletes the branches it created, so a walk would report nothing at all and
// pass a claim it never checked.
func executionModeMailboxObjects(t *testing.T, mailbox string) string {
	t.Helper()
	listed := bareGit(t, mailbox, "cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)")
	var carried strings.Builder
	for _, line := range strings.Split(listed, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != "blob" {
			continue
		}
		carried.WriteString(bareGit(t, mailbox, "cat-file", "blob", fields[0]))
	}
	return carried.String()
}

// writeEntryExecution restates the entry configuration's execution object
// without committing it.
//
// Nothing is committed on purpose: the head is part of what a plan digest
// names, so a scenario asking whether the digest follows the worker list has
// to change the worker list and nothing else.
func (f *executionModeFleet) writeEntryExecution(execution *models.ExecutionConfig) {
	f.t.Helper()
	document, err := os.ReadFile(f.entry.Path("dispat.json"))
	require.NoError(f.t, err)
	var cfg models.File
	require.NoError(f.t, json.Unmarshal(document, &cfg))
	cfg.Execution = execution
	f.entry.WriteConfigModel(cfg)
}
