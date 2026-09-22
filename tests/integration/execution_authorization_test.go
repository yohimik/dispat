// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a publishing node does with the message that would start the
// one command nobody can take back.
//
// Every scenario here writes that message by hand, because the interesting
// ones are exactly the messages no orchestrator would produce: an
// authorization signed with another secret, one that expired before it
// arrived, one addressed to another node, one answering another state of the
// branch. A node offered any of them must do the same thing it does when
// nothing arrives at all, which is nothing.
//
// The assignment is crafted too, for the same reason the refusals are: a run
// that authorizes correctly never leaves a node waiting at the gate long
// enough for a second party to push anything, so the only way to hold a real
// publisher there is to be the party that answers it.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionCraftedDigest is the plan digest every crafted message of this file
// states. Nothing parses it: what it has to be is the same in the assignment
// and in every message that answers it, because an authorization naming
// another plan is one of the refusals below.
const executionCraftedDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// executionCraftedGeneration is the ownership every crafted message names, in
// the same spirit.
const executionCraftedGeneration = "generation"

// The two scripts a crafted publication's frame runs, each recording itself in
// the file the fixture reads afterwards. The second one is the claim: a
// publication that was not authorized is a publication whose own command never
// ran, and the absence of its line is what says so.
const (
	executionCraftedHookScript = `printf '%s %s %s\n' hook "$DISPAT_PACKAGE" "$PWD" ` +
		`>> "$DISPAT_IT_EXECUTION_LOG"`
	executionCraftedPublishScript = `printf '%s %s %s\n' published "$DISPAT_PACKAGE" "$PWD" ` +
		`>> "$DISPAT_IT_EXECUTION_LOG"`
)

// executionInputState is one prepared input state a crafted assignment points
// at: the branch a node fetches it from and the exact commit it materializes.
type executionInputState struct {
	branch string
	commit string
}

// prepareInputState writes one commit holding one file into the mailbox, on a
// branch whose name says it carries a prepared input state rather than work.
//
// A node skips such a branch without reading it and fetches it by name when an
// assignment points at it, which is exactly what a real run's snapshot branch
// is for: this is that branch, made by hand.
func (o *executionFakeOrchestrator) prepareInputState(label string) executionInputState {
	o.t.Helper()
	branch := executionCraftedBranchName("snapshot", label)
	tree := gitIn(o.t, o.mailbox, fmt.Sprintf("100644 blob %s\tREADME.md\x00",
		o.hashObject("a package a crafted assignment publishes\n")), "mktree", "-z")
	commit := strings.TrimSpace(bareGit(o.t, o.mailbox, "commit-tree", tree, "-m", "input state"))
	bareGit(o.t, o.mailbox, "update-ref", "refs/heads/"+branch, commit)
	return executionInputState{branch: branch, commit: commit}
}

// publication is the publish assignment every scenario of this file varies
// from: addressed to this node, on this branch, permitting the publication and
// naming the input state the node is to run in.
func (o *executionFakeOrchestrator) publication(branch, task string, state executionInputState,
	changes ...func(map[string]any)) map[string]any {
	message := map[string]any{
		"protocol":   executionProtocolVersion,
		"kind":       "publish",
		"run":        o.run,
		"planDigest": executionCraftedDigest,
		"task":       task,
		"attempt":    1,
		"generation": executionCraftedGeneration,
		"node":       executionNode,
		"branch":     branch,
		"issuedAt":   time.Now().UTC().Format(time.RFC3339),
		"repositories": []any{map[string]any{
			"name": "", "path": "", "snapshot": state.commit, "branch": state.branch}},
		"package": map[string]any{"name": task, "version": "1.0.0", "dir": "."},
		"frame": map[string]any{
			"before":   []any{executionCraftedHookScript},
			"commands": []any{executionCraftedPublishScript},
		},
		"env":             []any{"DISPAT_PACKAGE=" + task},
		"permits":         map[string]any{"publish": true},
		"limits":          executionCraftedLimits(),
		"deadlineSeconds": 120,
	}
	for _, change := range changes {
		change(message)
	}
	return message
}

// executionCraftedLimits are the transfer ceilings a crafted assignment holds
// its node to. They are stated because the node reads the authorization under
// the ceiling its own assignment named, so an assignment that stated none
// would refuse every answer as oversize.
func executionCraftedLimits() map[string]any {
	return map[string]any{"maxFiles": 1000, "maxBytes": 10 << 20, "maxManifestBytes": 1 << 20}
}

// authorization is the `go` message a waiting publisher is answered with: the
// work it belongs to, the exact objects it answers, and the instant it stops
// meaning anything.
func (o *executionFakeOrchestrator) authorization(branch, task, assignment,
	ready string, changes ...func(map[string]any)) map[string]any {
	message := map[string]any{
		"protocol":   executionProtocolVersion,
		"kind":       "publish",
		"run":        o.run,
		"planDigest": executionCraftedDigest,
		"task":       task,
		"attempt":    1,
		"generation": executionCraftedGeneration,
		"node":       executionNode,
		"branch":     branch,
		"issuedAt":   time.Now().UTC().Format(time.RFC3339),
		"assignment": assignment,
		"ready":      ready,
		"notAfter":   time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339),
	}
	for _, change := range changes {
		change(message)
	}
	return message
}

// withdrawal is the `cancel` message that takes a waiting publisher's work
// away again. It names the tip it withdraws, which is what keeps a
// cancellation of an earlier state of the branch from stopping this one.
func (o *executionFakeOrchestrator) withdrawal(branch, task, assignment,
	tip string, changes ...func(map[string]any)) map[string]any {
	message := map[string]any{
		"protocol":   executionProtocolVersion,
		"kind":       "publish",
		"run":        o.run,
		"planDigest": executionCraftedDigest,
		"task":       task,
		"attempt":    1,
		"generation": executionCraftedGeneration,
		"node":       executionNode,
		"branch":     branch,
		"issuedAt":   time.Now().UTC().Format(time.RFC3339),
		"assignment": assignment,
		"tip":        tip,
	}
	for _, change := range changes {
		change(message)
	}
	return message
}

// answer writes one of the orchestrator's later messages onto a branch, on top
// of the object it answers, exactly as a compare-and-swap push would leave it.
func (o *executionFakeOrchestrator) answer(branch, kind, parent string, message map[string]any,
	changes ...func(*executionMessageOptions)) string {
	o.t.Helper()
	return o.offer(branch, message, append([]func(*executionMessageOptions){
		func(options *executionMessageOptions) {
			options.kind = kind
			options.parents = []string{parent}
		}}, changes...)...)
}

// executionCraftedBranchName names one crafted branch: addressed to this node,
// labelled with the kind a reader of the mailbox would expect, and with a
// label telling the scenarios apart.
func executionCraftedBranchName(kind, label string) string {
	return "dispat-worker-" + executionNode + "-20260921-" + kind + "-" + label
}

// executionTipOID is where one coordination branch is right now, which is what
// a crafted answer has to be written on top of.
func executionTipOID(t *testing.T, mailbox, branch string) string {
	t.Helper()
	return strings.TrimSpace(bareGit(t, mailbox, "rev-parse", "refs/heads/"+branch))
}

// executionRecordedTasks is the set of tasks one of the fixture's scripts
// recorded itself for, which is how a scenario asks whether a command ran at
// all.
func executionRecordedTasks(rig *executionRig, marker string) []string {
	var recorded []string
	for _, run := range rig.runs() {
		if run.Node == marker {
			recorded = append(recorded, run.Package)
		}
	}
	return recorded
}

// TestExecutionCraftedPublicationAuthorizations: every rule a publishing node
// holds its authorization to, offered as a message by hand.
//
// One node serves them all at once, each on a branch of its own, so that the
// claim is about the message rather than about the process: a node that
// refused one authorization goes on to act on the correct one beside it. What
// every refusal has in common is the absence the file exists for, which is
// that the publish command left no line anywhere.
func TestExecutionCraftedPublicationAuthorizations(t *testing.T) {
	rows := map[string]struct {
		// change adjusts the authorization document; options adjusts how it is
		// signed and what it is called.
		change  func(map[string]any)
		options []func(*executionMessageOptions)
		reason  string
	}{
		"an authorization signed with another secret": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.secret = "forged-" + strings.Repeat("x", 16) }},
			reason: "signature"},
		"an authorization nobody signed at all": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.isSigned = false }},
			reason: "unreadable"},
		"an authorization signed as another kind of message": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.signedAs = "ready" }},
			reason: "signature"},
		"an authorization that expired before it arrived": {
			change: func(m map[string]any) {
				m["notAfter"] = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
			},
			reason: "issued-at"},
		"an authorization that states no expiry at all": {
			change: func(m map[string]any) { delete(m, "notAfter") },
			reason: "issued-at"},
		"an authorization addressed to another node": {
			change: func(m map[string]any) { m["node"] = executionSecondNode },
			reason: "node"},
		"an authorization bound to another branch": {
			change: func(m map[string]any) { m["branch"] = executionCraftedBranchName("publish", "elsewhere") },
			reason: "branch"},
		"an authorization of a protocol this node does not speak": {
			change: func(m map[string]any) { m["protocol"] = executionProtocolVersion + 1 },
			reason: "protocol"},
		"an authorization answering another ready": {
			change: func(m map[string]any) { m["ready"] = executionCraftedDigest[:40] },
			reason: "replay"},
		"an authorization naming another assignment": {
			change: func(m map[string]any) { m["assignment"] = executionCraftedDigest[:40] },
			reason: "replay"},
		"an authorization of another run": {
			change: func(m map[string]any) { m["run"] = "runsomebodyelse" },
			reason: "replay"},
		"an authorization of another attempt": {
			change: func(m map[string]any) { m["attempt"] = 2 },
			reason: "replay"},
		"an authorization of another ownership": {
			change: func(m map[string]any) { m["generation"] = "another-generation" },
			reason: "replay"},
		"an authorization of another plan": {
			change: func(m map[string]any) { m["planDigest"] = strings.Repeat("a", 64) },
			reason: "replay"},
		"an authorization that is not JSON at all": {
			options: []func(*executionMessageOptions){
				func(o *executionMessageOptions) { o.document = []byte("not a document") }},
			reason: "unreadable"},
	}

	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareInputState("authorizations")
	branches := map[string]string{}
	assignments := map[string]string{}
	for name := range rows {
		task := executionLabel(name)
		branch := executionCraftedBranchName("publish", task)
		branches[name] = branch
		assignments[name] = orchestrator.offer(branch, orchestrator.publication(branch, task, state))
	}
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) {
			settings.Concurrency = models.Int(len(rows) + 1)
		}), executionRefusalIdleSeconds)

	for name, row := range rows {
		branch := branches[name]
		executionAwaitMessage(t, rig.mailbox, branch, "ready")
		ready := executionTipOID(t, rig.mailbox, branch)
		document := orchestrator.authorization(branch, executionLabel(name), assignments[name], ready)
		if row.change != nil {
			row.change(document)
		}
		orchestrator.answer(branch, "go", ready, document, row.options...)
	}
	reply := executionServeUntilIdle(t, worker)

	assert.Empty(t, executionRecordedTasks(rig, "published"),
		"no refused authorization started a publish command anywhere: %v", rig.runs())
	assert.Len(t, executionRecordedTasks(rig, "hook"), len(rows),
		"although every one of them had run its beforePublish hook")
	refused := executionRefusedAuthorizations(reply)
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, refused, row.reason, "the node named the rule the message broke")
			assert.NotContains(t, executionChain(t, rig.mailbox, branches[name]), "result",
				"and answered nothing on a branch somebody else had moved")
		})
	}
	assert.NotContains(t, reply.Stdout, executionSecret, "and never echoed the secret")
}

// executionRefusalIdleSeconds is how long a node of these scenarios serves
// with nothing to do before it ends itself.
//
// Ending itself is what the scenarios wait on, because a refused
// authorization leaves nothing on the mailbox to watch for: the node answers
// nothing on a branch somebody else has moved. A node that has gone idle has
// finished every attempt it took on, which is exactly the condition the
// assertions need, and it is a statement the node makes rather than a
// duration a test guessed at.
const executionRefusalIdleSeconds = 10

// executionServeUntilIdle waits for a node to end itself once every attempt it
// took on has finished, and answers what it reported on the way.
func executionServeUntilIdle(t *testing.T, worker *executionWorker) harness.RunResult {
	t.Helper()
	res := worker.proc.Wait()
	require.Equal(t, 0, res.Code, "a node that ran out of work ends by itself\nstdout:\n%s\nstderr:\n%s",
		res.Stdout, res.Stderr)
	stopped, isStopped := executionLine(res, "worker stopped")
	require.True(t, isStopped, "stdout:\n%s", res.Stdout)
	require.Equal(t, "idle", stopped.Str("reason"),
		"every attempt of the scenario had finished before the node stopped")
	return res
}

// executionRefusedAuthorizations are the rules a node reported an
// authorization or a withdrawal breaking, in the order it reported them.
func executionRefusedAuthorizations(res harness.RunResult) []string {
	var reasons []string
	for _, event := range executionEvents(res) {
		switch event.Str("message") {
		case "the publication authorization was refused",
			"the publication withdrawal was refused",
			"assignment rejected":
			reasons = append(reasons, event.Str("reason"))
		}
	}
	return reasons
}

// TestExecutionCraftedPublicationAuthorizationStartsTheCommand: the control
// the refusals are read against. A message that satisfies every one of those
// rules is acted on: the command runs, once, and the attempt reports success
// on top of the authorization it was let through by.
func TestExecutionCraftedPublicationAuthorizationStartsTheCommand(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareInputState("authorized")
	branch := executionCraftedBranchName("publish", "authorized")
	assignment := orchestrator.offer(branch, orchestrator.publication(branch, "authorized", state))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	executionAwaitMessage(t, rig.mailbox, branch, "ready")
	ready := executionTipOID(t, rig.mailbox, branch)
	orchestrator.answer(branch, "go", ready,
		orchestrator.authorization(branch, "authorized", assignment, ready))
	result := executionAwaitMessage(t, rig.mailbox, branch, "result")
	reply := worker.stop(t)

	assert.Equal(t, "succeeded", result["status"], "the authorized publication reported success")
	assert.Equal(t, []string{"assignment", "claim", "ready", "go", "result"},
		executionChain(t, rig.mailbox, branch), "one attempt, one chain, one message per step")
	assert.Equal(t, []string{"authorized"}, executionRecordedTasks(rig, "published"),
		"and the publish command ran exactly once: %v", rig.runs())
	assert.Less(t, executionEventIndex(reply, "publication authorized, starting the publish command"),
		executionPublishStageIndex(reply),
		"the node was authorized before it started the command\nstdout:\n%s", reply.Stdout)
}

// TestExecutionCraftedPublicationWithdrawals: the other message a waiting
// publisher may be answered with, and the rules it is held to.
//
// A withdrawal is checked exactly as an authorization is, and for the opposite
// reason: one nobody signed would let anybody who can write into a mailbox
// stop every publication of every run. So a valid one is acknowledged and
// terminal, and a forged one leaves the attempt where it was.
func TestExecutionCraftedPublicationWithdrawals(t *testing.T) {
	t.Run("a valid withdrawal is acknowledged and nothing is published", func(t *testing.T) {
		rig := newExecutionRig(t)
		orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
		state := orchestrator.prepareInputState("withdrawn")
		branch := executionCraftedBranchName("publish", "withdrawn")
		assignment := orchestrator.offer(branch, orchestrator.publication(branch, "withdrawn", state))
		worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

		executionAwaitMessage(t, rig.mailbox, branch, "ready")
		ready := executionTipOID(t, rig.mailbox, branch)
		orchestrator.answer(branch, "cancel", ready,
			orchestrator.withdrawal(branch, "withdrawn", assignment, ready))
		ack := executionAwaitMessage(t, rig.mailbox, branch, "ack")
		worker.stop(t)

		assert.Equal(t, []string{"assignment", "claim", "ready", "cancel", "ack"},
			executionChain(t, rig.mailbox, branch),
			"the acknowledgement is the attempt's terminal message and no result follows it")
		assert.Equal(t, "authorization-wait", ack["phase"],
			"it says the attempt was still waiting to be authorized")
		assert.NotEqual(t, true, ack["commandStarted"],
			"and that the publish command had not begun")
		assert.Empty(t, executionRecordedTasks(rig, "published"),
			"which is what the missing line proves: %v", rig.runs())
	})

	t.Run("a withdrawal nobody signed stops nothing", func(t *testing.T) {
		rig := newExecutionRig(t)
		orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
		state := orchestrator.prepareInputState("unsigned")
		branch := executionCraftedBranchName("publish", "unsigned")
		assignment := orchestrator.offer(branch, orchestrator.publication(branch, "unsigned", state))
		worker := rig.startWorker(executionWorkerConfig(rig.mailbox), executionRefusalIdleSeconds)

		executionAwaitMessage(t, rig.mailbox, branch, "ready")
		ready := executionTipOID(t, rig.mailbox, branch)
		orchestrator.answer(branch, "cancel", ready,
			orchestrator.withdrawal(branch, "unsigned", assignment, ready),
			func(o *executionMessageOptions) { o.secret = "forged-" + strings.Repeat("y", 16) })
		reply := executionServeUntilIdle(t, worker)

		assert.Contains(t, executionRefusedAuthorizations(reply), "signature",
			"the node said which rule the withdrawal broke\nstdout:\n%s", reply.Stdout)
		assert.NotContains(t, executionChain(t, rig.mailbox, branch), "ack",
			"a withdrawal nobody signed is acknowledged by nobody")
		assert.Empty(t, executionRecordedTasks(rig, "published"),
			"and the attempt is still waiting rather than publishing: %v", rig.runs())
	})

	t.Run("a withdrawal of an earlier state of the branch is refused", func(t *testing.T) {
		rig := newExecutionRig(t)
		orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
		state := orchestrator.prepareInputState("stale")
		branch := executionCraftedBranchName("publish", "stale")
		assignment := orchestrator.offer(branch, orchestrator.publication(branch, "stale", state))
		worker := rig.startWorker(executionWorkerConfig(rig.mailbox), executionRefusalIdleSeconds)

		executionAwaitMessage(t, rig.mailbox, branch, "ready")
		ready := executionTipOID(t, rig.mailbox, branch)
		// Authentically signed, correctly addressed, and about the assignment
		// commit rather than about the state the node is waiting at.
		orchestrator.answer(branch, "cancel", ready,
			orchestrator.withdrawal(branch, "stale", assignment, assignment))
		reply := executionServeUntilIdle(t, worker)

		assert.Contains(t, executionRefusedAuthorizations(reply), "replay",
			"a cancellation of an earlier tip is not this attempt's\nstdout:\n%s", reply.Stdout)
		assert.NotContains(t, executionChain(t, rig.mailbox, branch), "ack")
		assert.Empty(t, executionRecordedTasks(rig, "published"), "%v", rig.runs())
	})

	t.Run("a message that is not an authorization at all withholds the publication", func(t *testing.T) {
		rig := newExecutionRig(t)
		orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
		state := orchestrator.prepareInputState("notgo")
		branch := executionCraftedBranchName("publish", "notgo")
		assignment := orchestrator.offer(branch, orchestrator.publication(branch, "notgo", state))
		worker := rig.startWorker(executionWorkerConfig(rig.mailbox), executionRefusalIdleSeconds)

		executionAwaitMessage(t, rig.mailbox, branch, "ready")
		ready := executionTipOID(t, rig.mailbox, branch)
		// An authentic message of the wrong kind: the branch moved, and what it
		// moved to is not the one message that may start the command.
		orchestrator.answer(branch, "ready", ready,
			orchestrator.authorization(branch, "notgo", assignment, ready))
		reply := executionServeUntilIdle(t, worker)

		_, isWithheld := executionLine(reply,
			"the publication branch moved to something that is not an authorization")
		assert.True(t, isWithheld, "the node said so\nstdout:\n%s", reply.Stdout)
		assert.Empty(t, executionRecordedTasks(rig, "published"), "%v", rig.runs())
	})
}
