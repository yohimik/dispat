// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: what a serving node does with the provider outputs its assignment
// tells it to install.
//
// A reference is not a transfer (§28.5). The assignment carries an exact
// result commit and the digest of the manifest the run admitted, and both
// have to turn into bytes this node fetched, read and verified before a
// single command of the frame starts. So the claims here are all the same
// claim in different words: the frame did not run, the node said which rule
// the prerequisite broke, and the run heard it as a failed prerequisite rather
// than as a failed build.
//
// The references are written by hand because a real orchestrator never writes
// a bad one: what is being fenced is a mailbox somebody else can push to, and
// an assignment that names an object of their choosing.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionProvidedOutputs is one provider's answer written into a mailbox by
// hand: the exact object carried by a consumer assignment and its digest.
type executionProvidedOutputs struct {
	commit string
	digest string
}

// executionProvidedBody is what the crafted provider's one file holds.
const executionProvidedBody = "provided\n"

// offerOutputs writes one detached provider result commit with its declared
// output bytes. The consumer assignment carries this exact commit as a direct
// parent; v2 creates no runtime relay ref.
func (o *executionFakeOrchestrator) offerOutputs(label, task, pkg string,
	craft func(*executionCraftedOutputs), changes ...func(*executionMessageOptions)) executionProvidedOutputs {
	o.t.Helper()
	branch := o.inputTaskBranch("provider:" + label)
	crafted := &executionCraftedOutputs{
		entries: map[string]executionTreeFile{
			"dist/lib.js": {mode: "100644", content: executionProvidedBody}},
	}
	crafted.manifest = executionOrderedJSON{}.with(
		executionField{"protocol", executionProtocolVersion},
		executionField{"run", o.run},
		executionField{"planDigest", executionCraftedDigest},
		executionField{"task", task},
		executionField{"attempt", 1},
		executionField{"generation", executionCraftedGeneration},
		executionField{"node", executionNode},
		executionField{"package", pkg},
		executionField{"platform", executionPlatform()},
		executionField{"roots", []any{"dist"}},
		executionField{"entries", []any{executionCraftedEntry("dist/lib.js", "file", "0644",
			len(executionProvidedBody), executionDigestOf(executionProvidedBody))}},
		executionField{"files", 1},
		executionField{"bytes", len(executionProvidedBody)},
		executionField{"outputTree", ""},
		executionField{"manifestDigest", ""},
	)
	if craft != nil {
		craft(crafted)
	}
	outputs := executionWriteTree(o.t, o.mailbox, crafted.entries)
	crafted.manifest = crafted.manifest.set("outputTree", outputs)
	if !crafted.isDigestStale {
		crafted.manifest = crafted.manifest.set("manifestDigest",
			executionDigestOfBytes(executionMustMarshal(o.t, crafted.manifest.set("manifestDigest", ""))))
	}
	result := executionOrderedJSON{}.with(
		executionField{"protocol", executionProtocolVersion},
		executionField{"kind", "build"},
		executionField{"run", o.run},
		executionField{"planDigest", executionCraftedDigest},
		executionField{"task", task},
		executionField{"attempt", 1},
		executionField{"generation", executionCraftedGeneration},
		executionField{"node", executionNode},
		executionField{"branch", branch},
		executionField{"issuedAt", time.Now().UTC().Format(time.RFC3339)},
		executionField{"assignment", strings.Repeat("0", 40)},
		executionField{"status", "succeeded"},
		executionField{"platform", executionPlatform()},
	)
	if !crafted.isOutputsOmitted {
		result = result.with(executionField{"outputs", crafted.manifest})
	}
	options := executionMessageOptions{secret: o.secret, kind: "result", isSigned: true}
	for _, change := range changes {
		change(&options)
	}
	commit := o.carryOutputs(options, executionMustMarshal(o.t, result), outputs)
	return executionProvidedOutputs{commit: commit,
		digest: executionDigestOfBytes(executionMustMarshal(o.t, crafted.manifest.set("manifestDigest", "")))}
}

// inputTaskBranch gives crafted v2 attempts the same node-neutral branch
// shape as an orchestrator reservation, without changing shared fixtures.
func (o *executionFakeOrchestrator) inputTaskBranch(task string) string {
	sum := sha256.Sum256([]byte(task))
	return "dispat-task-" + o.run + "-" + hex.EncodeToString(sum[:16]) + "-1"
}

// prepareCarriedInputState writes a detached source snapshot. Its object is
// transported by the assignment parent list rather than a snapshot ref.
func (o *executionFakeOrchestrator) prepareCarriedInputState() executionInputState {
	o.t.Helper()
	tree := gitIn(o.t, o.mailbox, fmt.Sprintf("100644 blob %s\tREADME.md\x00",
		o.hashObject("a package a crafted assignment reads\n")), "mktree", "-z")
	commit := strings.TrimSpace(bareGit(o.t, o.mailbox, "commit-tree", tree, "-m", "input state"))
	return executionInputState{commit: commit}
}

// offerCarriedInput builds the signed Reservation -> Assignment first-parent
// chain and adds every distinct declared source/result as a direct carrier
// parent. Omitting a result lets one test prove that an object merely present
// in the mailbox cannot become a task input.
func (o *executionFakeOrchestrator) offerCarriedInput(branch string, message map[string]any,
	state executionInputState, inputs []any, omitResult bool) string {
	o.t.Helper()
	reservation := make(map[string]any, len(message))
	for field, value := range message {
		reservation[field] = value
	}
	reservation["node"] = ""
	document, err := json.Marshal(reservation)
	require.NoError(o.t, err)
	reserved := o.commit(executionMessageOptions{secret: o.secret, kind: "reservation", isSigned: true}, document)
	parents := []string{reserved, state.commit}
	seen := map[string]bool{state.commit: true}
	if !omitResult {
		for _, raw := range inputs {
			input := raw.(map[string]any)
			commit, _ := input["commit"].(string)
			if commit != "" && !seen[commit] {
				parents = append(parents, commit)
				seen[commit] = true
			}
		}
	}
	document, err = json.Marshal(message)
	require.NoError(o.t, err)
	assigned := o.commit(executionMessageOptions{secret: o.secret, kind: "assignment", isSigned: true},
		document, parents...)
	bareGit(o.t, o.mailbox, "update-ref", "refs/heads/"+branch, assigned)
	o.offerSlot(branch, assigned, message)
	return assigned
}

// carryOutputs builds one transport commit carrying both a message and the
// tree of files it describes, which is how a result and its bytes travel in
// one push.
func (o *executionFakeOrchestrator) carryOutputs(options executionMessageOptions,
	document []byte, outputs string) string {
	o.t.Helper()
	if len(options.document) > 0 {
		document = options.document
	}
	entries := fmt.Sprintf("100644 blob %s\t%s.json\x00", o.hashObject(string(document)), options.kind)
	if options.isSigned {
		entries += fmt.Sprintf("100644 blob %s\t%s.sig\x00",
			o.hashObject(executionSign(options.kind, options.secret, document)), options.kind)
	}
	inner := gitIn(o.t, o.mailbox, entries, "mktree", "-z")
	tree := gitIn(o.t, o.mailbox, fmt.Sprintf("040000 tree %s\tdispat\x00040000 tree %s\toutputs\x00",
		inner, outputs), "mktree", "-z")
	return strings.TrimSpace(bareGit(o.t, o.mailbox, "commit-tree", tree, "-m", "dispat transport result"))
}

// consuming is the build assignment the input scenarios vary: one package
// whose frame records that it ran, and the provider sets it is told to install
// first.
func (o *executionFakeOrchestrator) consuming(branch, task string, state executionInputState,
	inputs []any, changes ...func(map[string]any)) map[string]any {
	return o.publication(branch, task, state, append([]func(map[string]any){
		func(message map[string]any) {
			message["kind"] = "build"
			message["permits"] = map[string]any{"publish": false}
			message["frame"] = map[string]any{"commands": []any{executionCraftedPublishScript}}
			message["inputs"] = inputs
		}}, changes...)...)
}

// executionProvidedInput is one entry of a crafted assignment's input list.
func executionProvidedInput(provided executionProvidedOutputs, pkg, path string,
	changes ...func(map[string]any)) map[string]any {
	input := map[string]any{
		"task": pkg + ":build", "package": pkg, "branch": "",
		"commit": provided.commit, "manifestDigest": provided.digest, "path": path,
	}
	for _, change := range changes {
		change(input)
	}
	return input
}

// TestExecutionCraftedInputsFailThePrerequisite: every way a named output set
// fails to become bytes this node may build against.
//
// One node serves them all, each on a branch of its own, and none of them runs
// a command: the node reports the rule the prerequisite broke, and the
// assignment's frame left no line anywhere.
func TestExecutionCraftedInputsFailThePrerequisite(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareCarriedInputState()
	sound := orchestrator.offerOutputs("sound", "provider:build", "provider", nil)

	rows := map[string]struct {
		input  func() map[string]any
		reason string
	}{
		"an input named without a manifest digest": {
			input: func() map[string]any {
				return executionProvidedInput(sound, "provider", "providers/lib",
					func(m map[string]any) { m["manifestDigest"] = "" })
			},
			reason: "manifest-digest"},
		"an input naming an object that carries no result": {
			input: func() map[string]any {
				return executionProvidedInput(sound, "provider", "providers/lib",
					func(m map[string]any) { m["commit"] = state.commit })
			},
			reason: "output-identity"},
		"an input whose result describes no outputs": {
			input: func() map[string]any {
				empty := orchestrator.offerOutputs("nooutputs", "provider:build", "provider",
					func(c *executionCraftedOutputs) { c.isOutputsOmitted = true })
				return executionProvidedInput(empty, "provider", "providers/lib")
			},
			reason: "bytes-missing"},
		"an input whose result has no signature beside it": {
			// A direct carrier parent without a signed result fails input
			// identity before the worker reads any output manifest.
			input: func() map[string]any {
				unsigned := orchestrator.offerOutputs("unsigned", "provider:build", "provider", nil,
					func(o *executionMessageOptions) { o.isSigned = false })
				return executionProvidedInput(unsigned, "provider", "providers/lib")
			},
			reason: "output-identity"},
		"an input whose result was signed with another secret": {
			input: func() map[string]any {
				forged := orchestrator.offerOutputs("forged", "provider:build", "provider", nil,
					func(o *executionMessageOptions) { o.secret = "forged-" + strings.Repeat("z", 16) })
				return executionProvidedInput(forged, "provider", "providers/lib")
			},
			reason: "output-identity"},
		"an input whose result is not the JSON it claims to be": {
			input: func() map[string]any {
				garbled := orchestrator.offerOutputs("garbled", "provider:build", "provider", nil,
					func(o *executionMessageOptions) { o.document = []byte("not a result") })
				return executionProvidedInput(garbled, "provider", "providers/lib")
			},
			reason: "output-identity"},
		"an input whose manifest is not the one the run admitted": {
			input: func() map[string]any {
				return executionProvidedInput(sound, "provider", "providers/lib",
					func(m map[string]any) { m["manifestDigest"] = strings.Repeat("b", 64) })
			},
			reason: "manifest-digest"},
		"an input built under another ownership": {
			input: func() map[string]any {
				foreign := orchestrator.offerOutputs("foreign", "provider:build", "provider",
					func(c *executionCraftedOutputs) {
						c.manifest = c.manifest.set("generation", "somebody-elses-lock")
					})
				return executionProvidedInput(foreign, "provider", "providers/lib")
			},
			reason: "output-identity"},
		"an input naming a file its tree does not hold": {
			input: func() map[string]any {
				absent := orchestrator.offerOutputs("absent", "provider:build", "provider",
					func(c *executionCraftedOutputs) {
						c.manifest = c.manifest.set("entries", []any{
							executionCraftedEntry("dist/lib.js", "file", "0644",
								len(executionProvidedBody), executionDigestOf(executionProvidedBody)),
							executionCraftedEntry("dist/missing.js", "file", "0644",
								len(executionProvidedBody), executionDigestOf(executionProvidedBody)),
						}).set("files", 2).set("bytes", 2*len(executionProvidedBody))
					})
				return executionProvidedInput(absent, "provider", "providers/lib")
			},
			reason: "tree-missing"},
	}

	branches := map[string]string{}
	for name, row := range rows {
		task := executionLabel(name)
		branch := orchestrator.inputTaskBranch(task)
		branches[name] = branch
		inputs := []any{row.input()}
		orchestrator.offerCarriedInput(branch, orchestrator.consuming(branch, task, state, inputs),
			state, inputs, false)
	}
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
		func(settings *models.ExecutionConfig) {
			settings.Concurrency = models.Int(len(rows) + 1)
		}), 0)
	for name := range rows {
		executionAwaitMessage(t, rig.mailbox, branches[name], "result")
	}
	reply := worker.stop(t)

	assert.Empty(t, executionRecordedTasks(rig, "published"),
		"no frame ran on a prerequisite that could not be installed: %v", rig.runs())
	refused := executionRefusedPrerequisites(reply)
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			result := executionMessage(t, rig.mailbox, branches[name], "result")
			assert.Equal(t, "failed", result["status"])
			assert.Equal(t, "inputs", result["failedPart"],
				"the run hears a prerequisite rather than a build that went wrong")
			assert.Equal(t, row.reason, result["reason"])
			assert.Contains(t, refused, row.reason, "and the node says so in its own log")
		})
	}
}

// An exact object present in the mailbox is not enough to authorize an input:
// it must be a direct secondary parent of the signed assignment. The worker
// rejects this malformed envelope before claiming or running its frame.
func TestExecutionCraftedInputMissingCarrierIsNotClaimed(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareCarriedInputState()
	provided := orchestrator.offerOutputs("unattached", "provider:build", "provider", nil)
	branch := orchestrator.inputTaskBranch("missing-carrier")
	inputs := []any{executionProvidedInput(provided, "provider", "providers/lib")}
	orchestrator.offerCarriedInput(branch,
		orchestrator.consuming(branch, "missing-carrier", state, inputs), state, inputs, true)

	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 2)
	reply := worker.proc.Wait()
	require.Equal(t, 0, reply.Code, "worker exits after refusing malformed task\nstdout:\n%s", reply.Stdout)
	assert.Equal(t, []string{"reservation", "assignment"}, executionChain(t, rig.mailbox, branch),
		"the worker must not claim an assignment missing its declared result carrier")
	assert.Empty(t, executionRecordedTasks(rig, "published"), "no command ran")
	assert.Contains(t, reply.Stdout+reply.Stderr, "rejected (chain)",
		"the direct-parent integrity gate, rather than another refusal, stopped the worker")
}

// executionRefusedPrerequisites are the rules a node reported an input set
// breaking.
func executionRefusedPrerequisites(res harness.RunResult) []string {
	var reasons []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "the task's inputs could not be installed" {
			reasons = append(reasons, event.Str("reason"))
		}
	}
	return reasons
}

// TestExecutionCraftedInputsInstallOnce: the control. Two inputs naming one
// exact result are carried once and installed into the two folders the
// assignment names; the frame runs after both are present.
func TestExecutionCraftedInputsInstallOnce(t *testing.T) {
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareCarriedInputState()
	provided := orchestrator.offerOutputs("shared", "provider:build", "provider", nil)
	branch := orchestrator.inputTaskBranch("installed")
	// The same object named twice: one carrier parent, two installations.
	inputs := []any{
		executionProvidedInput(provided, "provider", "providers/first"),
		executionProvidedInput(provided, "provider", "providers/second"),
	}
	message := orchestrator.consuming(branch, "installed", state, inputs, func(message map[string]any) {
		message["frame"] = map[string]any{"commands": []any{
			`test -f providers/first/dist/lib.js && test -f providers/second/dist/lib.js && ` +
				executionCraftedPublishScript}}
	})
	assigned := orchestrator.offerCarriedInput(branch, message, state, inputs, false)
	parents := strings.Fields(bareGit(t, rig.mailbox, "rev-list", "--parents", "--max-count=1", assigned))
	assert.Equal(t, 4, len(parents), "assignment, reservation, source and one deduplicated provider result")
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	result := executionAwaitMessage(t, rig.mailbox, branch, "result")
	reply := worker.stop(t)

	assert.Equal(t, "succeeded", result["status"],
		"the frame ran against the bytes it was promised\nstdout:\n%s", reply.Stdout)
	assert.Equal(t, []string{"installed"}, executionRecordedTasks(rig, "published"),
		"and ran once: %v", rig.runs())
	installed := 0
	for _, event := range executionEvents(reply) {
		if event.Str("message") == "inputs installed" {
			installed++
		}
	}
	assert.Equal(t, 2, installed, "both folders were filled from the one answer")
}
