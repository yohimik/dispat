// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The old hand-written input messages document retired v2 transport. The v3
// refusal matrix and two-path installation use the real Git carrier and Worker
// reader in execution/choreography_input_migration_test.go.

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

func TestExecutionRetiredCraftedInputDoesNotClaim(t *testing.T) {
	retiredCraftedInputRefusal(t, false)
}

// The retired hand-written assignment also omits the direct result parent.
// It must remain unclaimed; the v3 parent-integrity refusal is checked by
// TestChoreographyWorkerRefusesMissingDirectInputCarrier in execution.
func TestExecutionRetiredCraftedInputMissingCarrierIsNotClaimed(t *testing.T) {
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

// The old hand-written assignment uses protocol v2. Current serving nodes
// refuse it before a Claim or command. The v3 input refusal matrix lives in
// execution/choreography_input_migration_test.go against real Git carriers.
func TestExecutionRetiredTwoPathInputDoesNotClaim(t *testing.T) {
	retiredCraftedInputRefusal(t, true)
}

func retiredCraftedInputRefusal(t *testing.T, duplicate bool) {
	t.Helper()
	rig := newExecutionRig(t)
	orchestrator := newExecutionFakeOrchestrator(t, rig.mailbox)
	state := orchestrator.prepareCarriedInputState()
	provided := orchestrator.offerOutputs("retired", "provider:build", "provider", nil)
	branch := orchestrator.inputTaskBranch("retired-input")
	inputs := []any{executionProvidedInput(provided, "provider", "providers/first")}
	if duplicate {
		inputs = append(inputs, executionProvidedInput(provided, "provider", "providers/second"))
	}
	orchestrator.offerCarriedInput(branch,
		orchestrator.consuming(branch, "retired-input", state, inputs), state, inputs, false)
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)
	time.Sleep(time.Second)
	worker.stop(t)
	assert.NotContains(t, executionChain(t, rig.mailbox, branch), "claim",
		"a retired protocol cannot authorize a command")
	assert.Empty(t, rig.runs(), "no command ran from the retired assignment")
}
