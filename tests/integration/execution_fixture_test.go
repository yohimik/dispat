// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the fixture a distributed run is driven through.
//
// Two machines are two processes here: a release started in one repository,
// and a `dispat worker` started against a configuration of its own, talking
// through a bare repository that stands in for the mailbox. Nothing is
// mocked, because the claims are about what one process leaves on a git
// remote and what the other makes of it.
//
// The mailbox is also written to by hand. A worker's acceptance rules are
// about messages no dispat would produce, so the scenarios craft them with
// git plumbing and sign them here: that is the only way to offer a node a
// forged assignment, and it is the same way an attacker would.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const (
	// executionProtocolVersion is the version every message states and every
	// reader requires exactly. It is spelled out rather than imported,
	// because this suite knows the binary from outside.
	executionProtocolVersion = 1
	// executionNode is the node name every scenario addresses.
	executionNode = "build-a"
	// executionSecret is what the fixtures sign with, in the variable the
	// harness lets through.
	executionSecret = "s3cr3t-execution"
	// executionRetainedCode is the warning a run that could not close its own
	// coordination branches reports.
	executionRetainedCode = "W244"
)

// executionRig is one distributed run's world: a repository to release, the
// remote its release lock lives on, and the mailbox a worker is reached at.
type executionRig struct {
	t       *testing.T
	repo    *harness.Repo
	origin  string
	mailbox string
	// builds is the file every build script of the fixture appends a line to,
	// outside every checkout, so that what ran where survives the folder a
	// node built in being removed.
	builds string
}

// newExecutionRig builds the repository every distributed scenario starts
// from: one package, a remote to take the release lock on, a mailbox, and a
// configuration the scenario may adjust before it is written.
//
// The build script leaves a marker, because half of these claims are that no
// stage ran: a refusal before dispatch has to be a refusal before the first
// command of the first package.
func newExecutionRig(t *testing.T, adjust ...func(*models.File)) *executionRig {
	t.Helper()
	repo := harness.New(t)
	repo.SeedPackage("packages", "core")
	mailbox := executionMailbox(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Execution = &models.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []models.ExecutionWorkerConfig{{Name: executionNode, Endpoint: "file://" + mailbox}},
		Timeouts:  &models.ExecutionTimeoutsConfig{Preflight: 30},
	}
	for _, change := range adjust {
		change(&cfg)
	}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(core): bootstrap")
	return newExecutionRigOver(t, repo, mailbox)
}

// newExecutionRigOver wraps an already-seeded repository as a rig: a remote to
// take the release lock on, the mailbox, and the file the build scripts of the
// fixture record themselves in.
func newExecutionRigOver(t *testing.T, repo *harness.Repo, mailbox string) *executionRig {
	t.Helper()
	return &executionRig{t: t, repo: repo, origin: repo.AddBareRemote(), mailbox: mailbox,
		builds: filepath.Join(t.TempDir(), "builds.log")}
}

// env is what every invocation of a distributed scenario needs: the release
// lock back on, the signing secret, and the file a build script records itself
// in wherever it runs.
func (r *executionRig) env(extra ...string) []string {
	return append(append(append([]string{}, harness.LockEnabled...),
		executionSecretEnv+"="+executionSecret, executionBuildLogEnv+"="+r.builds), extra...)
}

// executionBuildLogEnv names the file the fixture's build scripts append to.
// It is in the suite's own namespace, which is the only one the harness lets
// through to a dispat process, and it is what lets a script that ran inside a
// node's temporary checkout leave a record outside it.
const executionBuildLogEnv = "DISPAT_IT_EXECUTION_LOG"

// executionRecordingScript is the build script every delegated scenario uses:
// one line naming the node it ran on, the package it built and the folder it
// ran in. A build on the orchestrator names no node, which is exactly what the
// scenarios that assert "this did not go to a worker" read.
const executionRecordingScript = `printf '%s %s %s\n' ` +
	`"${DISPAT_EXECUTION_NODE:-orchestrator}" "$DISPAT_PACKAGE" "$PWD" >> "$DISPAT_IT_EXECUTION_LOG"`

// executionRun is one recorded execution of a fixture script.
type executionRun struct {
	Node    string
	Package string
	Dir     string
}

// runs are the script executions this rig recorded, in order.
func (r *executionRig) runs() []executionRun {
	r.t.Helper()
	content, err := os.ReadFile(r.builds)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(r.t, err)
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

// executionProbePrefix marks a recorded line as one of the fixture's probes
// rather than as one of its builds, so that a scenario reading "where did this
// package build" is not answered by a probe that ran in the same script.
const executionProbePrefix = "probe-"

// nodesByPackage is which node built each package, for the scenarios whose
// claim is where the work happened rather than in which order.
func (r *executionRig) nodesByPackage() map[string]string {
	placed := map[string]string{}
	for _, run := range r.runs() {
		if strings.HasPrefix(run.Node, executionProbePrefix) {
			continue
		}
		placed[run.Package] = run.Node
	}
	return placed
}

// startWorker starts a node against this rig's mailbox, with the build log
// the fixture's scripts write to already in its environment.
func (r *executionRig) startWorker(cfg models.File, idleSeconds int, extraEnv ...string) *executionWorker {
	r.t.Helper()
	return startWorker(r.t, r.repo, cfg, idleSeconds,
		append([]string{executionBuildLogEnv + "=" + r.builds}, extraEnv...)...)
}

// release runs the release of this rig, with whatever else the scenario
// wants in the environment.
func (r *executionRig) release(extra ...string) harness.RunResult {
	r.t.Helper()
	return r.repo.CommandEnv(r.env(extra...))
}

// branches lists the coordination branches the mailbox holds.
func (r *executionRig) branches() []string {
	r.t.Helper()
	return executionMailboxBranches(r.t, r.mailbox)
}

// executionWorker is one `dispat worker` process: its own configuration
// folder, its own state folder, and the process itself.
type executionWorker struct {
	t        *testing.T
	proc     *harness.Proc
	stateDir string
	root     string
}

// executionSecondNode is the second node the placement scenarios add, so that
// "which node ran this" is a question with more than one answer.
const executionSecondNode = "build-b"

// executionWorkerConfig is the node configuration a worker is started with:
// what it is called, where its mailbox is, and what it may take on.
func executionWorkerConfig(mailbox string, adjust ...func(*models.ExecutionConfig)) models.File {
	settings := &models.ExecutionConfig{
		Name:      executionNode,
		Endpoint:  "file://" + mailbox,
		SecretEnv: executionSecretEnv,
	}
	for _, change := range adjust {
		change(settings)
	}
	cfg := harness.BaseFile()
	cfg.LogLevel = "debug"
	cfg.Execution = settings
	return cfg
}

// writeNodeConfig writes one node's configuration into a folder of its own,
// which is what a serving machine has: no packages, no spaces, and not even
// a git repository.
func writeNodeConfig(t *testing.T, cfg models.File) string {
	t.Helper()
	root := t.TempDir()
	document, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), document, 0o644))
	return root
}

// startWorker launches a serving node against a mailbox and returns it.
//
// The repository is only where the binary under test comes from: a serving
// node is pointed at a configuration folder of its own and never at the one
// being released. The idle timeout is what stops every node this suite
// starts: a worker killed outright would flush no coverage counters, so
// every scenario ends its node by letting it go idle or by signalling it.
func startWorker(t *testing.T, repo *harness.Repo, cfg models.File, idleSeconds int, extraEnv ...string) *executionWorker {
	t.Helper()
	root := writeNodeConfig(t, cfg)
	state := t.TempDir()
	env := append([]string{executionSecretEnv + "=" + executionSecret}, extraEnv...)
	proc := repo.StartCommandEnv(env, "worker",
		"--root", root, "--state-dir", state,
		"--idle-timeout", fmt.Sprint(idleSeconds))
	return &executionWorker{t: t, proc: proc, stateDir: state, root: root}
}

// runWorker runs one serving invocation to completion.
//
// It goes through the process starter rather than the ordinary command
// helper for one reason: the helper appends its own --root, and a serving
// node is pointed at a configuration folder of its own rather than at the
// repository being released.
func runWorker(t *testing.T, rig *executionRig, env []string, args ...string) harness.RunResult {
	t.Helper()
	return rig.repo.StartCommandEnv(env, args...).Wait()
}

// restart starts a second process on the same state folder, which is how a
// scenario proves that what a node remembers outlives it.
func (w *executionWorker) restart(t *testing.T, repo *harness.Repo, idleSeconds int, extraEnv ...string) *executionWorker {
	t.Helper()
	env := append([]string{executionSecretEnv + "=" + executionSecret}, extraEnv...)
	proc := repo.StartCommandEnv(env, "worker",
		"--root", w.root, "--state-dir", w.stateDir,
		"--idle-timeout", fmt.Sprint(idleSeconds))
	return &executionWorker{t: t, proc: proc, stateDir: w.stateDir, root: w.root}
}

// stop signals a node and waits for it, which is how a scenario that started
// one ends it without losing the coverage a killed process would never flush.
func (w *executionWorker) stop(t *testing.T) harness.RunResult {
	t.Helper()
	w.proc.Signal(os.Interrupt)
	res := w.proc.Wait()
	require.Equal(t, 0, res.Code, "a node asked to stop stops cleanly\nstdout:\n%s\nstderr:\n%s",
		res.Stdout, res.Stderr)
	return res
}

// executionFakeOrchestrator writes coordination branches by hand, so that a
// scenario can offer a node anything at all: a forged signature, another
// node's work, a chain no protocol produced.
type executionFakeOrchestrator struct {
	t       *testing.T
	mailbox string
	secret  string
	run     string
}

func newExecutionFakeOrchestrator(t *testing.T, mailbox string) *executionFakeOrchestrator {
	t.Helper()
	return &executionFakeOrchestrator{t: t, mailbox: mailbox, secret: executionSecret,
		run: "run" + hex.EncodeToString([]byte(t.Name()))[:16]}
}

// probe is the assignment document every scenario varies from: a valid probe
// addressed to this node on this branch.
func (o *executionFakeOrchestrator) probe(branch, task string, changes ...func(map[string]any)) map[string]any {
	message := map[string]any{
		"protocol":   executionProtocolVersion,
		"kind":       "probe",
		"run":        o.run,
		"planDigest": "0000000000000000000000000000000000000000000000000000000000000000",
		"task":       task,
		"attempt":    1,
		"generation": "generation",
		"node":       executionNode,
		"branch":     branch,
		"issuedAt":   time.Now().UTC().Format(time.RFC3339),
	}
	for _, change := range changes {
		change(message)
	}
	return message
}

// offer writes one assignment onto a branch of the mailbox, exactly as an
// orchestrator's push would leave it.
func (o *executionFakeOrchestrator) offer(branch string, message map[string]any, changes ...func(*executionMessageOptions)) string {
	o.t.Helper()
	options := executionMessageOptions{secret: o.secret, kind: "assignment", isSigned: true}
	for _, change := range changes {
		change(&options)
	}
	document, err := json.Marshal(message)
	require.NoError(o.t, err)
	commit := o.commit(options, document, options.parents...)
	bareGit(o.t, o.mailbox, "update-ref", "refs/heads/"+branch, commit)

	return commit
}

// executionMessageOptions is how a crafted message differs from a correct
// one: who signed it, what it is called, whether it is signed at all, and
// what it sits on.
type executionMessageOptions struct {
	secret string
	kind   string
	// signedAs is the kind the signature was computed over, when a scenario
	// wants a document signed as one message and published as another. Empty
	// signs it as what it is.
	signedAs string
	isSigned bool
	parents  []string
}

// commit builds one transport commit in the mailbox itself: the document,
// its signature, the folder holding both, and the commit that carries them.
func (o *executionFakeOrchestrator) commit(options executionMessageOptions, document []byte, parents ...string) string {
	o.t.Helper()
	signedAs := options.kind
	if options.signedAs != "" {
		// The one thing a party with push access to a mailbox can do without
		// the secret: publish an authentic document under another name.
		signedAs = options.signedAs
	}
	entries := fmt.Sprintf("100644 blob %s\t%s.json\x00",
		o.hashObject(string(document)), options.kind)
	if options.isSigned {
		entries += fmt.Sprintf("100644 blob %s\t%s.sig\x00",
			o.hashObject(executionSign(signedAs, options.secret, document)), options.kind)
	}
	inner := gitIn(o.t, o.mailbox, entries, "mktree", "-z")
	tree := gitIn(o.t, o.mailbox, fmt.Sprintf("040000 tree %s\tdispat\x00", inner), "mktree", "-z")
	args := []string{"commit-tree", tree}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	return strings.TrimSpace(bareGit(o.t, o.mailbox, append(args, "-m", "dispat transport "+options.kind)...))
}

func (o *executionFakeOrchestrator) hashObject(content string) string {
	o.t.Helper()
	return gitIn(o.t, o.mailbox, content, "hash-object", "-w", "--stdin")
}

// executionSign is the signature a message carries: the lowercase hex
// HMAC-SHA256 over the kind of message, one NUL byte and the exact document
// bytes.
//
// The kind is part of what is signed because which message a document is
// depends on the name of the file it was found in, and anybody able to push
// to a mailbox can change that name without knowing the secret.
func executionSign(kind, secret string, document []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write(document)
	return hex.EncodeToString(mac.Sum(nil))
}

// gitIn runs one git command in a repository with something on its standard
// input, which is what the plumbing that builds an object needs.
func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// executionChain is the messages one coordination branch carries, oldest
// first: what the two parties actually wrote to each other.
func executionChain(t *testing.T, mailbox, branch string) []string {
	t.Helper()
	listed := bareGit(t, mailbox, "rev-list", "--first-parent", "--reverse", "refs/heads/"+branch)
	var kinds []string
	for _, commit := range strings.Fields(listed) {
		entries := bareGit(t, mailbox, "ls-tree", "-r", "--name-only", commit)
		for _, path := range strings.Fields(entries) {
			if name, isDocument := strings.CutSuffix(strings.TrimPrefix(path, "dispat/"), ".json"); isDocument {
				kinds = append(kinds, name)
			}
		}
	}
	return kinds
}

// executionMessage reads one message off a branch: the document the party
// that wrote it signed, as a map, because this suite has no access to the
// types the binary marshalled it from.
func executionMessage(t *testing.T, mailbox, branch, kind string) map[string]any {
	t.Helper()
	document := bareGit(t, mailbox, "cat-file", "blob",
		"refs/heads/"+branch+":dispat/"+kind+".json")
	var message map[string]any
	require.NoError(t, json.Unmarshal([]byte(document), &message), "the %s document is not JSON", kind)
	return message
}

// executionAwaitMessage waits for one branch to carry a message of this kind,
// which is how a scenario synchronises with a node it started.
func executionAwaitMessage(t *testing.T, mailbox, branch, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, carried := range executionChain(t, mailbox, branch) {
			if carried == kind {
				return executionMessage(t, mailbox, branch, kind)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %s reached %s within the deadline", kind, branch)
	return nil
}

// executionRejections are the reasons a node refused something, as its log
// reported them.
func executionRejections(res harness.RunResult) []string {
	var reasons []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "assignment rejected" {
			reasons = append(reasons, event.Str("reason"))
		}
	}
	return reasons
}

// executionLine finds one line of a run by its message, and reports whether
// there was one.
func executionLine(res harness.RunResult, message string) (harness.Event, bool) {
	for _, event := range executionEvents(res) {
		if event.Str("message") == message {
			return event, true
		}
	}
	return nil, false
}

// executionBranchName is the name a crafted branch takes: addressed to this
// node, with a label a person reading the mailbox can tell apart.
func executionBranchName(label string) string {
	return "dispat-worker-" + executionNode + "-20260921-probe-" + label
}
