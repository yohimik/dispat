// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: who wrote this line, and who sent this event.
//
// A release spread over several machines is read from several logs at once
// and reported to receivers that hear from all of them. Every claim here is
// about attribution: a line names the process that wrote it, a line about
// another node names that node separately, an event carries the same two
// names as the log, and a repository that never asked for any of this sees
// none of it.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

const (
	// executionOrchestratorNode is what the orchestrator of these scenarios
	// calls itself, so that "this line was written here" and "this line is
	// about build-a" are two different strings.
	executionOrchestratorNode = "ci-1"
	// executionRelayedMark is what the fixture's build script prints, so a
	// scenario can find one relayed output line among a node's own.
	executionRelayedMark = "the build script ran here"
	// executionNestedMark is what the declared script the build script runs
	// dispat for prints, which is how the nested invocation's own log is
	// recognised inside the worker's.
	executionNestedMark = "the nested command ran"
	// The webhook variables of the trigger scenario. They are the suite's own
	// namespace, which is what the harness lets through, and they are given
	// to the node alone: an endpoint's secret and header values resolve where
	// the delivery is made from.
	executionWebhookSecretEnv = "DISPAT_IT_WEBHOOK_SECRET"
	executionWebhookSecret    = "wh-s3cr3t"
	executionNodeHeaderEnv    = "DISPAT_IT_NODE_HEADER"
)

// executionIdentityBuild is the build script of the log scenario: it records
// where it ran, prints a line of its own, and runs a nested dispat, so that a
// node's log carries its own lines, a relayed script line and a whole other
// dispat process's output.
func executionIdentityBuild(repo *harness.Repo) string {
	return executionRecordingScript +
		" && echo '" + executionRelayedMark + "'" +
		" && " + repo.DispatCommand("exec", "nested")
}

// executionLinesAfterTheConfig are the run's own lines: everything from the
// line that reports the configuration onwards.
//
// What comes before it is written by the bootstrap logger, which cannot know
// this node's identity because the file that states it has not been read yet.
// That is the documented boundary, so the scenario reads from the same place.
func executionLinesAfterTheConfig(t *testing.T, res harness.RunResult) []harness.Event {
	t.Helper()
	events := harness.ParseEvents(res.Stdout)
	for index, event := range events {
		if event.Str("message") == "configuration loaded" {
			return events[index:]
		}
	}
	t.Fatalf("the run never reported reading its configuration\nstdout:\n%s", res.Stdout)
	return nil
}

// executionRelayedLines are the JSON documents a node relayed from the output
// of a process it started: one dispat's log as it reaches another's.
func executionRelayedLines(res harness.RunResult) []harness.Event {
	var relayed []harness.Event
	for _, event := range harness.ParseEvents(res.Stdout) {
		message := event.Str("message")
		if !strings.HasPrefix(message, "{") {
			continue
		}
		var inner map[string]any
		if json.Unmarshal([]byte(message), &inner) != nil {
			continue
		}
		relayed = append(relayed, harness.Event(inner))
	}
	return relayed
}

// TestExecutionEveryLogLineNamesItsNode: both processes of a distributed
// release say who they are on every line they write, and they say it of
// themselves: the orchestrator names the worker it is reporting on in a field
// of its own and never in the field that names the writer.
func TestExecutionEveryLogLineNamesItsNode(t *testing.T) {
	rig := newExecutionWorkspace(t, executionIdentityBuild, executionOneWorker,
		func(cfg *models.File) {
			cfg.LogLevel = "trace"
			cfg.Execution.Name = executionOrchestratorNode
			cfg.Scripts["nested"] = models.Script{"echo " + executionNestedMark}
		})
	// A task's command that was handed the authority marker without the node
	// beside it is still on a worker, and still names a machine rather than
	// writing an empty field.
	unnamed := rig.repo.CommandEnv([]string{executionAuthorityEnv + "=worker"}, "status")
	require.Equal(t, 0, unnamed.Code, "stdout:\n%s\nstderr:\n%s", unnamed.Stdout, unnamed.Stderr)
	for _, event := range executionLinesAfterTheConfig(t, unnamed) {
		assert.Equal(t, "worker", event.Str("role"), "line: %v", event)
		assert.NotEmpty(t, event.Str("node"), "line: %v", event)
	}

	// And a node that was told nothing at all still says which machine
	// refused: the identity is settled before the settings are checked, so
	// the first line an operator reads off a new machine names that machine.
	untold := runWorker(t, rig, []string{executionSecretEnv + "=" + executionSecret},
		"worker", "--root", writeNodeConfig(t, harness.BaseFile()),
		"--state-dir", t.TempDir(), "--idle-timeout", "1")
	require.Equal(t, 1, untold.Code, "stdout:\n%s\nstderr:\n%s", untold.Stdout, untold.Stderr)
	refusal, isRefused := executionLine(untold, "cannot serve tasks")
	require.True(t, isRefused, "stdout:\n%s", untold.Stdout)
	assert.Equal(t, "worker", refusal.Str("role"))
	assert.NotEmpty(t, refusal.Str("node"))

	nodeConfig := executionWorkerConfig(rig.mailbox)
	nodeConfig.LogLevel = "trace"
	worker := rig.startWorker(nodeConfig, 0)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	isReportingTheWorker := false
	for _, event := range executionLinesAfterTheConfig(t, res) {
		assert.Equal(t, "orchestrator", event.Str("role"), "line: %v", event)
		assert.Equal(t, executionOrchestratorNode, event.Str("node"),
			"every line names the machine that wrote it: %v", event)
		isReportingTheWorker = isReportingTheWorker || event.Str("worker") == executionNode
	}
	assert.True(t, isReportingTheWorker,
		"a run that placed work on a node says so, in the field that names another node")

	reply := stopAll(t, []*executionWorker{worker})[0]
	lines := harness.ParseEvents(reply.Stdout)
	require.NotEmpty(t, lines)
	for _, event := range lines {
		assert.Equal(t, "worker", event.Str("role"), "line: %v", event)
		assert.Equal(t, executionNode, event.Str("node"),
			"a node names itself in node, which is what the orchestrator calls worker: %v", event)
	}
	for _, line := range strings.Split(strings.TrimSpace(reply.Stdout), "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		assert.Equal(t, 1, strings.Count(line, `"node":`),
			"the logger names the node once, so nothing writes the field a second time: %s", line)
	}
	relayed, isRelayed := executionLine(reply, executionRelayedMark)
	require.True(t, isRelayed, "the script's own output reaches the node's log\nstdout:\n%s", reply.Stdout)
	assert.Equal(t, "worker", relayed.Str("role"))
	assert.Equal(t, executionNode, relayed.Str("node"))
	assert.Equal(t, "build", relayed.Str("stage"), "with the task fields the stage gave it")
	assert.NotEmpty(t, relayed.Str("package"))

	nested := executionRelayedLines(reply)
	require.NotEmpty(t, nested, "the nested dispat's own log reaches this one\nstdout:\n%s", reply.Stdout)
	for _, event := range nested {
		assert.Equal(t, "worker", event.Str("role"),
			"a dispat a task started is on the node, whatever the checkout's configuration says: %v", event)
		assert.Equal(t, executionNode, event.Str("node"), "line: %v", event)
	}
	_, isNestedReported := executionLine(reply, executionNestedMark)
	assert.True(t, isNestedReported,
		"and the declared script that invocation ran reached this log too\nstdout:\n%s", reply.Stdout)
}

// TestExecutionAbsentAddsNoIdentityFields: the containment. With no execution
// object the same fixture writes the lines it always wrote and delivers the
// payloads it always delivered, with none of the three keys anywhere.
func TestExecutionAbsentAddsNoIdentityFields(t *testing.T) {
	sink := newWebhookSink(t)
	rig := newExecutionWorkspace(t, recordingBuild, func(cfg *models.File) {
		cfg.Execution = nil
		cfg.LogLevel = "trace"
		cfg.Webhooks = []models.WebhookConfig{{URL: sink.srv.URL}}
	})

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	events := harness.ParseEvents(res.Stdout)
	require.NotEmpty(t, events)
	for _, event := range events {
		for _, field := range []string{"role", "node", "worker"} {
			assert.NotContains(t, event, field, "line: %v", event)
		}
	}
	payloads := sink.payloads(t)
	require.NotEmpty(t, payloads)
	for _, payload := range payloads {
		for _, field := range []string{"role", "node", "worker"} {
			assert.NotContains(t, payload, field, "payload: %v", payload)
		}
	}
	placed := rig.nodesByPackage()
	require.Len(t, placed, len(executionWorkspacePackages))
	for name, node := range placed {
		assert.Equal(t, "orchestrator", node, "%s was built here, as it always was", name)
	}
}

// TestExecutionWebhooksNameTheirSender: every delivery of a distributed run
// names the process that sent it, the deliveries about a delegated stage name
// the node it ran on, the deliveries about a stage the orchestrator kept name
// nobody, and a format template may render all three.
func TestExecutionWebhooksNameTheirSender(t *testing.T) {
	sink, formatted := newWebhookSink(t), newWebhookSink(t)
	rig := newExecutionWorkspace(t, recordingBuild, executionOneWorker, func(cfg *models.File) {
		cfg.Webhooks = []models.WebhookConfig{
			{URL: sink.srv.URL},
			{URL: formatted.srv.URL, Events: []string{"stage.succeeded"},
				Format: `{"sentBy": "{role} {node}", "ranOn": "{worker}"}`},
		}
	})
	workers := rig.startWorkers([]string{executionNode}, 4)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	stopAll(t, workers)
	payloads := sink.payloads(t)
	require.NotEmpty(t, payloads)
	sendingNode := str(payloads[0]["node"])
	assert.NotEmpty(t, sendingNode,
		"an orchestrator that states no name of its own still says which machine it is")
	assert.NotEqual(t, executionNode, sendingNode, "and it is not the node it delegated to")
	for _, payload := range payloads {
		assert.Equal(t, "orchestrator", payload["role"], "payload: %v", payload)
		assert.Equal(t, sendingNode, payload["node"], "one run is one sender: %v", payload)
	}

	built := executionPayloadOf(t, payloads, "stage.succeeded", "build", "assets")
	assert.Equal(t, executionNode, built["worker"], "the delegated stage names the node that ran it")
	published := executionPayloadOf(t, payloads, "package.published", "", "assets")
	assert.Equal(t, executionNode, published["worker"],
		"and so does the package's own outcome, which is what a receiver files the version under")
	kept := executionPayloadOf(t, payloads, "stage.succeeded", "publish", "assets")
	assert.NotContains(t, kept, "worker", "a stage this run kept for itself names no node")
	started := executionPayloadOf(t, payloads, "stage.started", "build", "assets")
	assert.NotContains(t, started, "worker",
		"and the event that opens a delegated stage cannot, because nothing is placed yet")

	rendered := formatted.payloads(t)
	require.NotEmpty(t, rendered)
	isNodeRendered := false
	for _, payload := range rendered {
		assert.Equal(t, "orchestrator "+sendingNode, payload["sentBy"],
			"a template may name the sender: %v", payload)
		isNodeRendered = isNodeRendered || payload["ranOn"] == executionNode
	}
	assert.True(t, isNodeRendered, "and the node a stage ran on, which renders empty when there is none")
}

// executionPayloadOf is the first delivered payload of one event, stage and
// package, and fails the test when none arrived: a claim about a field of an
// event nobody sent would otherwise pass by accident.
func executionPayloadOf(t *testing.T, payloads []map[string]any, event, stage, pkg string) map[string]any {
	t.Helper()
	for _, payload := range payloads {
		if payload["event"] == event && str(payload["stage"]) == stage && payload["package"] == pkg {
			return payload
		}
	}
	t.Fatalf("no %s delivery of %s (stage %q) arrived", event, pkg, stage)
	return nil
}

// TestExecutionTriggerFromAWorkerNamesTheWorker: a build script reports its
// own progress from wherever the build was placed. The event names the node
// as its sender, carries the package and stage the task gave it, and is
// signed and headed with what that node's environment holds, because a
// delivery made from a machine resolves on that machine.
func TestExecutionTriggerFromAWorkerNamesTheWorker(t *testing.T) {
	sink := newWebhookSink(t)
	rig := newExecutionWorkspace(t, executionTriggerBuild, executionOneWorker, func(cfg *models.File) {
		cfg.Webhooks = []models.WebhookConfig{{URL: sink.srv.URL,
			SecretEnv: executionWebhookSecretEnv,
			Headers:   []models.WebhookHeader{{Name: "X-Dispat-It-Node", Value: "$" + executionNodeHeaderEnv}}}}
	})
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0,
		executionWebhookSecretEnv+"="+executionWebhookSecret,
		executionNodeHeaderEnv+"="+executionNode)

	res := rig.release()

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	stopAll(t, []*executionWorker{worker})
	raised, isRaised := executionDeliveryOf(sink, "script.progress")
	require.True(t, isRaised, "the script's own event reached the endpoint; got %v", sink.events())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raised.Body, &payload))
	assert.Equal(t, "worker", payload["role"], "the event names the machine that sent it")
	assert.Equal(t, executionNode, payload["node"])
	assert.NotContains(t, payload, "worker", "a node reporting about itself names no other node")
	assert.Equal(t, "build", payload["stage"], "with the task fields the stage gave it")
	assert.Contains(t, executionWorkspacePackages, str(payload["package"]))
	assert.Equal(t, float64(50), payload["progress"])
	assert.Equal(t, executionNode, raised.Header.Get("X-Dispat-It-Node"),
		"a header value resolves from the environment of the node that delivers it")
	assert.Equal(t, executionSignature(raised.Body), raised.Header.Get("X-Dispat-Signature"),
		"and so does the variable naming the signing secret")

	opened, isOpened := executionDeliveryOf(sink, "release.started")
	require.True(t, isOpened)
	require.NoError(t, json.Unmarshal(opened.Body, &payload))
	assert.Equal(t, "orchestrator", payload["role"], "the run's own brackets are still the orchestrator's")
	assert.Empty(t, opened.Header.Get("X-Dispat-Signature"),
		"and this machine was given no secret to sign with, which is what makes the node's own signature its own")
}

// executionTriggerBuild is the build script of the trigger scenario: it
// records where it ran and reports its progress through the binary under
// test, exactly as a real build script does.
func executionTriggerBuild(repo *harness.Repo) string {
	return executionRecordingScript + " && " +
		repo.DispatCommand("trigger", "progress", "50", "building")
}

// executionDeliveryOf is the first delivery of one event name, with its
// headers, which is what a claim about signing or a header value needs.
func executionDeliveryOf(sink *webhookSink, event string) (webhookDelivery, bool) {
	for _, delivery := range sink.all() {
		if delivery.Header.Get("X-Dispat-Event") == event {
			return delivery, true
		}
	}
	return webhookDelivery{}, false
}

// executionSignature is the header value a delivery signed with the node's
// secret carries, computed the way any receiver would.
func executionSignature(body []byte) string {
	mac := hmac.New(sha256.New, []byte(executionWebhookSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
