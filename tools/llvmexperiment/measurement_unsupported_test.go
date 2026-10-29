package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestUnsupportedChangesetsProducerKeepsSchedulePremisesUnavailable(t *testing.T) {
	toolsRoot := t.TempDir()
	commands := filepath.Join(t.TempDir(), "commands.log")
	versionOnly := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MEASUREMENT_TEST_COMMAND_LOG\"\ncase \"$*\" in --version) printf 'dispat 0.0.0-test-fixture\\n';; *) exit 91;; esac\n"
	if err := os.WriteFile(filepath.Join(toolsRoot, dispatTool), []byte(versionOnly), executableFileMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolsRoot+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MEASUREMENT_TEST_COMMAND_LOG", commands)
	settings, err := parseOptions([]string{"--tool", changesetsTool, "--mode", distributedMode, "--trial-id", "unsupported-premise", "--results", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := runExperiment(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(settings.Results, "llvm-full-distributed-changesets-unsupported-premise-r1")
	body, err := os.ReadFile(filepath.Join(output, "verdict.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record verdict
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	result := record.ExecutionMeasurement
	if result == nil || result.Trial.Status != unsupportedStatus || len(result.TaskIntervals) != 0 || len(record.Steps) != 0 || record.Passed || record.LLVM.Seconds != nil {
		t.Fatalf("unsupported producer invented execution evidence: %s", body)
	}
	if result.Graph.Counts.Vertices != 2 || result.Graph.Counts.TimedVertices != 0 {
		t.Fatal("unsupported producer discarded required graph coverage", result.Graph.Counts)
	}
	for _, calculation := range result.Analysis {
		if calculation.Value != nil || calculation.Status != "unavailable" || len(calculation.Reasons) == 0 {
			t.Fatalf("unsupported producer invented failure or numeric evidence: %+v", calculation)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := measurement.Validate(encoded); err != nil {
		t.Fatal(err)
	}
	if err := verifyRetainedEvidence(output, result); err != nil {
		t.Fatal(err)
	}
	invocations, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range strings.Fields(string(invocations)) {
		if command != "--version" {
			t.Fatalf("unsupported producer launched workload command %q", command)
		}
	}
}
