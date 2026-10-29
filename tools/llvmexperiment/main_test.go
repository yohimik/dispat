package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOptionsSeparateFullBuildsFromSmokeAndDistribution(t *testing.T) {
	settings, err := parseOptions(nil)
	if err != nil || settings.Profile != fullProfile || !reflect.DeepEqual(buildTargets(settings.Profile), []string{"all"}) {
		t.Fatalf("default workload must be full LLVM: %+v, %v", settings, err)
	}
	for _, args := range [][]string{
		{"--tool", "nx"}, {"--mode", "local"}, {"--profile", "quick"}, {"--jobs", "0"}, {"--timeout", "0s"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("accepted invalid or non-distributed settings %v", args)
		}
	}
	settings, err = parseOptions([]string{"--tool", nxTool, "--mode", distributedMode, "--profile", smokeProfile})
	if err != nil || len(buildTargets(settings.Profile)) != 2 || len(outputTools(fullProfile)) != 5 {
		t.Fatalf("smoke/full workload identity is wrong: %+v, %v", settings, err)
	}
}

func TestDispatVersionSkipsItsBanner(t *testing.T) {
	want := "dispat 1.11.0-rc.10 (linux_arm64)"
	if got := dispatVersionLine("████\n████\n\n" + want + "\n"); got != want {
		t.Fatalf("version %q is not %q", got, want)
	}
}

func TestLLVMImagePinsTheSameSourceAsItsRunner(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "tests", "experiments", "Dockerfile.llvm"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "origin "+llvmRevision) || !strings.Contains(string(content), "= "+llvmRevision) {
		t.Fatal("LLVM image and task runner source identities differ")
	}
}

func TestMissingCloudAndUnsupportedNeverAcquireTiming(t *testing.T) {
	t.Setenv(cloudTokenEnvironmentName, "")
	t.Setenv(cloudIDEnvironmentName, "")
	for _, tool := range []string{nxTool, lernaTool, changesetsTool} {
		t.Run(tool, func(t *testing.T) {
			settings, err := parseOptions([]string{"--tool", tool, "--mode", distributedMode, "--results", t.TempDir(), "--trial-id", "availability"})
			if err != nil {
				t.Fatal(err)
			}
			if err := runExperiment(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(settings.Results, "llvm-full-distributed-"+tool+"-availability-r1", "verdict.json"))
			if err != nil {
				t.Fatal(err)
			}
			var record verdict
			if err := json.Unmarshal(content, &record); err != nil {
				t.Fatal(err)
			}
			if record.Passed || record.LLVM.Seconds != nil || record.LLVM.Reason == "" || len(record.Steps) != 0 {
				t.Fatalf("unavailable tool fabricated a successful measurement: %s", content)
			}
			if tool == changesetsTool && record.LLVM.Status != unsupportedStatus || tool != changesetsTool && record.LLVM.Status != blockedStatus {
				t.Fatalf("wrong availability result: %s", content)
			}
		})
	}
}

func TestConfiguredExecutorFailuresFailTheCampaignAndRetainEvidence(t *testing.T) {
	t.Setenv(cloudTokenEnvironmentName, "test-token-not-used")
	t.Setenv(cloudIDEnvironmentName, "test-cloud-not-used")
	for _, tool := range []string{dispatTool, nxTool, lernaTool} {
		mode := distributedMode
		if tool == dispatTool {
			mode = hybridMode
		}
		settings, err := parseOptions([]string{"--tool", tool, "--mode", mode, "--source", t.TempDir(), "--results", t.TempDir(), "--trial-id", "failure"})
		if err != nil {
			t.Fatal(err)
		}
		if err := runExperiment(context.Background(), settings); err == nil {
			t.Fatalf("configured %s failure did not fail the campaign", tool)
		}
		content, err := os.ReadFile(filepath.Join(settings.Results, "llvm-full-"+mode+"-"+tool+"-failure-r1", "verdict.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record verdict
		if err := json.Unmarshal(content, &record); err != nil {
			t.Fatal(err)
		}
		if record.LLVM.Status != failedStatus || record.LLVM.Seconds != nil || record.LLVM.Reason == "" || record.LLVM.TimeoutSeconds != defaultTimeout.Seconds() {
			t.Fatalf("configured failure lost its evidence: %s", content)
		}
	}
}

func TestPreparedFixturePinsColdInputsAndPeerOnlyPlacement(t *testing.T) {
	settings, err := parseOptions([]string{"--source", t.TempDir(), "--timeout", "2h0.1s"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := prepareFixture(context.Background(), settings, t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(data.Root, "dispat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"choreographers:", "- choreographer", "concurrency: 2", "runOutputs:"} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("configuration lacks %s: %s", expected, config)
		}
	}
	var configuration dispatConfiguration
	if err := yaml.Unmarshal(config, &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.Execution.Timeouts == nil || configuration.Execution.Timeouts.Task != 7201 {
		t.Fatalf("task timeout must round up the entire campaign timeout: %+v", configuration.Execution.Timeouts)
	}
	if len(data.Tasks) != executorCount || data.Tasks[0].Run != data.Run || data.Tasks[1].Run != data.Run {
		t.Fatalf("task identities do not bind this unique run: %+v", data.Tasks)
	}
	for _, item := range data.Tasks {
		if err := validateTask(item); err != nil {
			t.Fatal(err)
		}
		if item.Profile != fullProfile || item.Revision != llvmRevision || item.Jobs != defaultJobsCount {
			t.Fatalf("task workload drifted: %+v", item)
		}
	}
}

func TestArtifactsRefuseCorruptionAndSymlinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "input")
	destination := filepath.Join(root, "output")
	if err := os.WriteFile(source, []byte("executable test payload"), executableFileMode); err != nil {
		t.Fatal(err)
	}
	entry, err := copyArtifact(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	entry.Path = "output"
	if err := verifyArtifact(root, entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("corrupted test payload!"), executableFileMode); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifact(root, entry); err == nil {
		t.Fatal("corrupted artifact was accepted")
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, destination); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifact(root, entry); err == nil {
		t.Fatal("symlink artifact was accepted")
	}
}

func TestArtifactManifestRejectsPreviousRun(t *testing.T) {
	root := t.TempDir()
	expected := task{Name: "llvm-x86", Backend: "X86", Profile: fullProfile, Source: defaultSourcePath, Revision: llvmRevision, Run: "current", Jobs: 2}
	stale := expected
	stale.Run = "old-cache-entry"
	out := filepath.Join(root, artifactsDirectory, expected.Name)
	if err := os.MkdirAll(out, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(out, manifestFileName), taskManifest{Task: stale, Node: "llvm-node-1"}); err != nil {
		t.Fatal(err)
	}
	err := collectArtifacts(context.Background(), fixture{Root: root, Tasks: []task{expected}}, &buildResult{})
	if err == nil || !strings.Contains(err.Error(), "cold run") {
		t.Fatalf("cached manifest was not rejected: %v", err)
	}
}
