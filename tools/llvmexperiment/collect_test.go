package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestCampaignCollectionPreservesAllSixExactCells(t *testing.T) {
	settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
	for _, cell := range campaignCells() {
		writeCampaignFixture(t, settings, cell)
	}
	if err := collectCampaignFiles(settings); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(settings.Output, "cells"))
	if err != nil || len(entries) != 6 {
		t.Fatalf("six-cell campaign was incomplete: %v", err)
	}
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(settings.Output, "cells", entry.Name(), "fixture.json")); err != nil {
			t.Fatal("aggregation lost raw per-trial evidence")
		}
	}
	if err := collectCampaignFiles(settings); err == nil {
		t.Fatal("collection overwrote a prior aggregate")
	}
}

func TestCampaignCollectionRetainsMissingAndRejectedEvidence(t *testing.T) {
	settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
	cell := campaignCells()[0]
	path := writeCampaignFixture(t, settings, cell)
	if err := os.WriteFile(filepath.Join(path, "fixture.json"), []byte("corrupted"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := collectCampaignFiles(settings); err == nil {
		t.Fatal("corrupt and missing cells passed collection")
	}
	if _, err := os.Stat(filepath.Join(settings.Output, "environment", "dispat-compilation", filepath.Base(path), "verdict.json")); err != nil {
		t.Fatal("invalid result was discarded")
	}
	content, err := os.ReadFile(filepath.Join(settings.Output, "campaign.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cells []campaignCell `json:"cells"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cells) != 6 {
		t.Fatal("missing cells disappeared from campaign diagnostics")
	}
	for _, entry := range manifest.Cells {
		if entry.Status != failedStatus || entry.Reason == "" {
			t.Fatal("missing/corrupt evidence became a successful observation")
		}
	}
}

func TestCampaignRejectsLocalTopologyAndMismatchedWorkload(t *testing.T) {
	settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
	cell := campaignCells()[0]
	path := writeCampaignFixture(t, settings, cell)
	content, err := os.ReadFile(filepath.Join(path, "verdict.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record verdict
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	record.LLVM.Topology = "two process names on one host"
	if err := validateCampaignCell(settings, cell, record); err == nil {
		t.Fatal("local processes substituted for real VMs")
	}
	record.LLVM.Topology = remoteTopology
	record.LLVM.Profile = smokeProfile
	if err := validateCampaignCell(settings, cell, record); err == nil {
		t.Fatal("smoke substituted for full workload")
	}
}

func writeCampaignFixture(t *testing.T, settings campaignOptions, cell campaignCell) string {
	t.Helper()
	// Reuse the shared synthetic model only inside this temporary collection test.
	content, err := os.ReadFile("../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	var record measurement.Measurement
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	record.Trial.ID = "synthetic-" + cell.Tool + "-" + cell.Mode
	record.Trial.Tool, record.Trial.Mode, record.Trial.Profile, record.Trial.SourceRevision = cell.Tool, cell.Mode, settings.Profile, llvmRevision
	record.Trial.Toolchain["dispat-source-revision"] = settings.Source
	record.Resources.Nodes[0]["hostId"], record.Resources.Nodes[1]["hostId"] = "gce:1", "gce:2"
	fixture := []byte(`{"description":"synthetic collection regression, not LLVM experiment evidence"}`)
	digest := sha256.Sum256(fixture)
	record.Trial.Evidence[0].SHA256 = hex.EncodeToString(digest[:])
	build := initialResult(options{Tool: cell.Tool, Mode: cell.Mode, Profile: settings.Profile, Jobs: defaultJobsCount, Timeout: defaultTimeout})
	build.Topology, build.BinarySHA256, build.ToolVersions = remoteTopology, record.Trial.BinarySHA256, record.Trial.Toolchain
	seconds := 8.0
	build.Status, build.Seconds = completedStatus, &seconds
	passed := true
	if cell.Tool == changesetsTool {
		build.Status, build.Seconds, build.Reason, passed = unsupportedStatus, nil, "no build scheduler", false
		record.Trial.Status = unsupportedStatus
		record.TaskIntervals = []measurement.TaskInterval{}
		for index := range record.Graph.Vertices {
			record.Graph.Vertices[index].DurationInputs = []measurement.Reference{}
		}
	}
	if err := measurement.Derive(&record); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(settings.Input, cell.Tool+"-"+cell.Mode, record.Trial.ID)
	if err := os.MkdirAll(root, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.json"), fixture, fileMode); err != nil {
		t.Fatal(err)
	}
	if cell.Tool != changesetsTool {
		writeFixtureObservations(t, root, &record)
	}
	writeFixtureHardware(t, root, &record, &build, defaultFixtureHardware())
	if err := measurement.Derive(&record); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "verdict.json"), verdict{Experiment: experimentName, Tool: cell.Tool, LLVM: &build, Passed: passed, ExecutionMeasurement: &record}); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFixtureObservations(t *testing.T, root string, record *measurement.Measurement) {
	t.Helper()
	for index := range record.TaskIntervals {
		interval := record.TaskIntervals[index]
		interval.Evidence = []measurement.Reference{}
		manifest := taskManifest{Task: task{Name: interval.TaskID, Run: record.Trial.ID}, Node: interval.NodeID, AttemptID: interval.AttemptID, Status: completedStatus, Interval: interval}
		reference, err := retainJSON(root, record, "terminal-"+interval.TaskID, filepath.Join("attempts", interval.TaskID, taskFinishedFile), manifest)
		if err != nil {
			t.Fatal(err)
		}
		record.TaskIntervals[index].Evidence = []measurement.Reference{reference}
	}
	window := &record.TimeWindows[0]
	window.ID = coordinatorWindowID
	record.Premises["enclosingWindowId"] = coordinatorWindowID
	raw := map[string]any{"seconds": window.Seconds, "clockKind": window.ClockKind, "clockDomain": window.ClockDomain,
		"boundary": window.Boundary, "containsTasks": window.ContainsTasks, "startedBeforePeerLaunch": true, "allArtifactsVerifiedBeforeEnd": true}
	reference, err := retainJSON(root, record, "coordinator-window", "coordinator-window.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	window.Evidence, window.ContainmentProof = []measurement.Reference{reference}, []measurement.Reference{reference}
}
