package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

// This is synthetic importer evidence: no compiler or GCE executor is started.
func writeKernelImportFixture(t *testing.T, campaign campaignOptions, cell campaignCell, trialID string) string {
	t.Helper()
	root := filepath.Join(campaign.Input, cell.Tool+"-"+cell.Mode, trialID)
	if err := os.MkdirAll(root, directoryMode); err != nil {
		t.Fatal(err)
	}
	settings := options{Workload: kernelWorkload, Tool: cell.Tool, Mode: cell.Mode, Profile: fullProfile, TrialID: trialID,
		Repetition: 1, Source: kernelSourcePath, Output: root, Jobs: defaultJobsCount, Timeout: defaultTimeout}
	build := initialResult(settings)
	version := "dispat 1.11.0-rc.13-candidate." + campaign.Source
	build.Status, build.Topology, build.BinarySHA256, build.BinaryID = completedStatus, remoteTopology, strings.Repeat("a", 64), "candidate."+campaign.Source
	build.ToolVersions = map[string]string{dispatTool: version, "dispat-source-revision": campaign.Source}
	for _, tool := range []string{"make", "gcc-12", "x86_64-linux-gnu-gcc-12", "aarch64-linux-gnu-gcc-12", "x86_64-linux-gnu-ld", "aarch64-linux-gnu-ld", "pahole"} {
		build.ToolVersions[tool] = "synthetic-import-test"
	}
	packages := []byte("synthetic-import-test-package\t1.0\n")
	digest := sha256.Sum256(packages)
	build.ToolVersions["kernel-package-manifest-sha256"] = hex.EncodeToString(digest[:])
	record, err := initializeMeasurement(settings, &build)
	if err != nil {
		t.Fatal(err)
	}
	packageSource := filepath.Join(t.TempDir(), "packages.tsv")
	if err := os.WriteFile(packageSource, packages, fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := retainFile(root, record, "kernel-toolchain", "kernel-toolchain.tsv", packageSource); err != nil {
		t.Fatal(err)
	}
	passed := cell.Tool != changesetsTool
	if passed {
		populateKernelImportObservations(t, root, record, &build)
	} else {
		build.Status, build.Reason = unsupportedStatus, "synthetic fixture: Changesets has no distributed build scheduler"
	}
	record.Trial.Status = build.Status
	attachDurationInputs(record)
	if err := measurement.Derive(record); err != nil {
		t.Fatal(err)
	}
	result := verdict{Experiment: kernelWorkload, Tool: cell.Tool, Scenario: fullProfile + "-" + cell.Mode, Dispat: version,
		Passed: passed, Steps: []step{}, Checks: []check{}, LinuxKernel: &build, ExecutionMeasurement: record}
	if err := writeJSON(filepath.Join(root, "verdict.json"), result); err != nil {
		t.Fatal(err)
	}
	if err := readHardwareJSON(filepath.Join(root, "verdict.json"), &result); err != nil {
		t.Fatal(err)
	}
	record = result.ExecutionMeasurement
	if err := validateCampaignCell(campaign, cell, result); err != nil {
		t.Fatal(err)
	}
	if err := verifyRetainedEvidence(root, record); err != nil {
		t.Fatal(err)
	}
	if passed {
		if err := verifyObservedMeasurement(root, record); err != nil {
			t.Fatal(err)
		}
		if _, err := readCampaignHardware(filepath.Dir(root), result); err != nil {
			t.Fatal(err)
		}
		if _, err := readKernelCampaignInputs(filepath.Dir(root), root, result); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func populateKernelImportObservations(t *testing.T, root string, record *measurement.Measurement, build *buildResult) {
	t.Helper()
	rawRoot, raw := kernelRawEvidenceFixture(t)
	build.ConfigDigests, build.ModuleCounts, build.Artifacts = raw.LinuxKernel.ConfigDigests, raw.LinuxKernel.ModuleCounts, raw.LinuxKernel.Artifacts
	record.TaskIntervals = raw.ExecutionMeasurement.TaskIntervals
	for _, entry := range raw.ExecutionMeasurement.Trial.Evidence {
		source := filepath.Join(rawRoot, entry.Path)
		if filepath.Base(entry.Path) == taskFinishedFile {
			var manifest taskManifest
			if err := readHardwareJSON(source, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.Task.Run = record.Trial.ID
			if err := writeJSON(source, manifest); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := retainFile(root, record, entry.ID, entry.Path, source); err != nil {
			t.Fatal(err)
		}
	}
	for index, backend := range build.Backends {
		identity := "gce:1"
		if index == 1 {
			identity = "gce:2"
		}
		record.Resources.Nodes = append(record.Resources.Nodes, map[string]any{"id": "node-" + backend, "hostId": identity})
		build.Nodes = append(build.Nodes, "node-"+backend)
		record.ClockCompatibility["domains"] = append(record.ClockCompatibility["domains"].([]string), "synthetic-"+backend)
	}
	seconds := 8.0
	build.Seconds = &seconds
	writeKernelImportWindow(t, root, record)
	record.Resources.TaskSlotLimit.Evidence = record.Graph.Evidence
	writeFixtureHardware(t, root, record, build, defaultFixtureHardware())
	for _, nested := range record.Resources.Nested {
		delete(nested, "ninjaJobs")
		nested["makeJobs"], nested["linkPool"], nested["linkPoolReason"] = 2, nil, "link recipes share the configured GNU make job budget; no independent link pool"
		nested["evidence"] = record.Graph.Evidence
	}
	provenancePath := filepath.Join(filepath.Dir(root), "cloud", "provenance.json")
	var provenance map[string]any
	if err := readHardwareJSON(provenancePath, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance["options"] = map[string]any{"image": "sha256:" + strings.Repeat("f", 64)}
	if err := writeJSON(provenancePath, provenance); err != nil {
		t.Fatal(err)
	}
}

func writeKernelImportWindow(t *testing.T, root string, record *measurement.Measurement) {
	t.Helper()
	seconds := 8.0
	tasks := []string{"kernel-x86_64", "kernel-arm64"}
	boundary := "synthetic importer test enclosing window; not a benchmark observation"
	domain := record.ClockCompatibility["referenceDomain"].(string)
	raw := map[string]any{"seconds": seconds, "clockKind": monotonicClockKind, "clockDomain": domain, "boundary": boundary,
		"containsTasks": tasks, "startedBeforePeerLaunch": true, "allArtifactsVerifiedBeforeEnd": true}
	reference, err := retainJSON(root, record, "coordinator-window", "coordinator-window.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	record.TimeWindows = append(record.TimeWindows, measurement.TimeWindow{ID: coordinatorWindowID, Seconds: &seconds, ClockKind: monotonicClockKind,
		ClockDomain: domain, Boundary: boundary, ContainsTasks: tasks, Evidence: []measurement.Reference{reference}, ContainmentProof: []measurement.Reference{reference}})
}

func TestKernelImportFixtureRetainsFullAndUnsupportedEvidence(t *testing.T) {
	settings := campaignOptions{Workload: kernelWorkload, Input: t.TempDir(), Source: strings.Repeat("c", 40), Profile: fullProfile}
	for _, cell := range campaignCells() {
		writeKernelImportFixture(t, settings, cell, "123-1-"+cell.Tool+"-"+cell.Mode)
	}
}
