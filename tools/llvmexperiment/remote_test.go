package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestManualGCPSplitUsesOneExplicitPackagePerActualExecutor(t *testing.T) {
	t.Setenv(cloudTokenEnvironmentName, "")
	t.Setenv(cloudIDEnvironmentName, "")
	for _, tool := range []string{nxTool, lernaTool} {
		settings, err := parseOptions([]string{"--tool", tool, "--mode", manualGCPMode})
		if err != nil {
			t.Fatal(err)
		}
		if status, _ := availability(settings); status != blockedStatus {
			t.Fatal("manual GCP mode accepted a local substitute")
		}
		settings.Remote = &remotePlan{}
		if status, reason := availability(settings); status != "" {
			t.Fatalf("manual orchestration incorrectly requires Nx Cloud: %s %s", status, reason)
		}
		for index, backend := range []string{"X86", "AArch64"} {
			node := nodeFixture{Name: []string{"vm-a", "vm-b"}[index], Root: "/cloud/work/repo", EvidenceDir: "/cloud/work/evidence"}
			item := taskForBackend(settings, "trial", backend)
			command := manualNodeCommand(settings, node, item, nil, io.Discard)
			if command.Name != tool || command.Dir != node.Root || !slices.Contains(command.Env, "NX_NO_CLOUD=true") || !slices.Contains(command.Env, "NX_AGENT_NAME="+node.Name) {
				t.Fatalf("manual executor uses wrong identity or Cloud mode: %+v", command)
			}
			if tool == nxTool && !slices.Contains(command.Args, item.Name+":build") || tool == lernaTool && !slices.Contains(command.Args, item.Name) {
				t.Fatalf("node %s did not receive exactly its selected backend: %+v", node.Name, command.Args)
			}
		}
	}
}

func TestPhysicalVMIdentityRequiresMatchingIndependentDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := os.WriteFile(path, []byte(`{"id":"123","status":"RUNNING"}`), fileMode); err != nil {
		t.Fatal(err)
	}
	node := remoteNode{Name: "vm-a", HostID: "gce:123", EvidencePath: path}
	if err := verifyRemoteHostEvidence(node); err != nil {
		t.Fatal(err)
	}
	node.HostID = "gce:456"
	if err := verifyRemoteHostEvidence(node); err == nil {
		t.Fatal("process label substituted for actual VM identity")
	}
	if err := os.WriteFile(path, []byte(`{"id":"456","status":"TERMINATED"}`), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := verifyRemoteHostEvidence(node); err == nil {
		t.Fatal("terminated VM was admitted")
	}
}

func TestExpectedHostEnvironmentCannotReplaceObservedPhysicalIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(taskEvidenceEnvironment, root)
	t.Setenv(requireGCEEnvironment, "true")
	t.Setenv(expectedHostEnvironment, "gce:123")
	t.Setenv("DISPAT_EXECUTION_NODE", "node-without-metadata")
	settings, _ := parseOptions(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := beginTaskRecording(ctx, taskForBackend(settings, "identity-trial", "X86"), t.TempDir()); err == nil {
		t.Fatal("caller-supplied expected identity replaced independently observed metadata")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("identity failure lost its raw attempt: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, entries[0].Name(), taskFinishedFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest taskManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != failedStatus || manifest.Host.ID == "gce:123" || manifest.Interval.DurationSeconds != nil {
		t.Fatal("identity failure invented physical evidence or execution time")
	}
}

func TestRemoteAdapterUsesStructuredInputWithoutSecretArguments(t *testing.T) {
	root := t.TempDir()
	requestPath := filepath.Join(root, "request.json")
	adapter := filepath.Join(root, "adapter")
	script := "#!/bin/sh\ncat > \"$LLVM_TEST_REQUEST\"\nprintf '{\"exitCode\":0}\\n'\n"
	if err := os.WriteFile(adapter, []byte(script), executableFileMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLVM_TEST_REQUEST", requestPath)
	request := remoteRequest{Action: remoteStartAction, Node: "vm-a", TrialID: "trial", Command: &remoteCommand{Dir: "/cloud/work/repo", Name: dispatTool, Args: []string{"choreographer"}, Env: []string{secretEnvironmentName + "=test-secret"}}}
	if _, err := callRemote(context.Background(), &remotePlan{Adapter: remoteAdapter{Executable: adapter}}, request, io.Discard); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var actual remoteRequest
	if err := json.Unmarshal(content, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, request) {
		t.Fatal("remote command changed over the adapter boundary")
	}
}
