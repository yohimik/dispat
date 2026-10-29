package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestKernelCollectionBindsReportedConfigurationAndCompleteInventoryToRawEvidence(t *testing.T) {
	root, record := kernelRawEvidenceFixture(t)
	if err := verifyRetainedEvidence(root, record.ExecutionMeasurement); err != nil {
		t.Fatal(err)
	}
	if err := bindKernelRawEvidence(root, record); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"config", "modules", "missing-config", "missing-inventory", "missing-order", "compiler", "partial-inventory", "artifact-digest", "artifact-bytes", "artifact-duplicate", "artifact-missing", "duration", "diagnostic-duration"} {
		t.Run(field, func(t *testing.T) {
			root, record := kernelRawEvidenceFixture(t)
			if field == "config" {
				record.LinuxKernel.ConfigDigests["x86_64"] = strings.Repeat("b", 64)
			}
			if field == "artifact-digest" {
				record.LinuxKernel.Artifacts[0].SHA256 = strings.Repeat("f", 64)
			}
			if field == "artifact-duplicate" {
				record.LinuxKernel.Artifacts[0] = record.LinuxKernel.Artifacts[1]
			}
			if field == "artifact-missing" {
				record.LinuxKernel.Artifacts = record.LinuxKernel.Artifacts[1:]
			}
			if field == "artifact-bytes" {
				record.LinuxKernel.Artifacts[0].Bytes++
			}
			if field == "duration" || field == "diagnostic-duration" {
				index := 0
				if field == "diagnostic-duration" {
					index = 1
				}
				changed := 999.0
				record.ExecutionMeasurement.TaskIntervals[index].DurationSeconds = &changed
			}
			if field == "modules" {
				record.LinuxKernel.ModuleCounts["x86_64"]++
			}
			for index := range record.ExecutionMeasurement.Trial.Evidence {
				entry := &record.ExecutionMeasurement.Trial.Evidence[index]
				name := filepath.Base(entry.Path)
				if field == "missing-config" && name == kernelResolvedConfig || field == "missing-inventory" && name == kernelModulesManifest || field == "missing-order" && name == kernelModulesOrder {
					entry.Path += ".missing"
				}
				if field == "compiler" && name == taskFinishedFile {
					var manifest taskManifest
					if err := readHardwareJSON(filepath.Join(root, entry.Path), &manifest); err != nil {
						t.Fatal(err)
					}
					manifest.BuildArguments = []string{"unapproved-compiler"}
					if err := writeJSON(filepath.Join(root, entry.Path), manifest); err != nil {
						t.Fatal(err)
					}
				}
				if field == "partial-inventory" && name == kernelModulesManifest {
					manifest, err := readKernelModuleManifest(filepath.Join(root, entry.Path))
					if err != nil {
						t.Fatal(err)
					}
					manifest.Modules = nil
					if err := writeJSON(filepath.Join(root, entry.Path), manifest); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := bindKernelRawEvidence(root, record); err == nil {
				t.Fatalf("%s mismatch was accepted", field)
			}
		})
	}
}

func kernelRawEvidenceFixture(t *testing.T) (string, verdict) {
	t.Helper()
	settings, _ := parseOptions([]string{"--workload", kernelWorkload, "--trial-id", "synthetic-raw-kernel"})
	build := initialResult(settings)
	build.Status = completedStatus
	record := verdict{Experiment: kernelWorkload, LinuxKernel: &build, ExecutionMeasurement: &measurement.Measurement{Trial: measurement.Trial{ID: settings.TrialID}}}
	output := t.TempDir()
	for _, backend := range build.Backends {
		item := taskForBackend(settings, settings.TrialID, backend)
		root, verification := kernelArtifactFixture(t, item)
		build.ConfigDigests[backend], build.ModuleCounts[backend] = verification.ConfigSHA256, verification.ModuleCount
		manifest := taskManifest{Task: item, Node: "node-" + backend, AttemptID: "attempt-" + backend, Status: completedStatus,
			Kernel: verification, WorkingDirectory: root, BuildEnvironment: kernelBuildEnvironment(),
			ConfigureArguments: kernelCommand(item, root, []string{kernelConfiguration(item.Profile)}).Args, BuildArguments: kernelCommand(item, root, kernelMakeTargets(item)).Args}
		for _, name := range workloadOutputs(kernelWorkload, fullProfile) {
			entry, err := describeKernelArtifact(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			entry.Package, entry.Path = item.Name, name
			build.Artifacts = append(build.Artifacts, entry)
			manifest.Artifacts = append(manifest.Artifacts, entry)
		}
		zero, duration := 0.0, 3.0
		manifest.Interval = measurement.TaskInterval{TaskID: item.Name, AttemptID: manifest.AttemptID, NodeID: manifest.Node, Operation: kernelOperation, Status: completedStatus, Boundary: kernelOperationBoundary, DurationSeconds: &duration, StartSeconds: &zero, EndSeconds: &duration, ClockKind: monotonicClockKind, ClockDomain: "synthetic-" + backend}
		diagnostic := manifest.Interval
		diagnostic.Operation = "synthetic-artifact-staging"
		manifest.Diagnostics = []measurement.TaskInterval{diagnostic}
		if err := writeJSON(filepath.Join(root, taskFinishedFile), manifest); err != nil {
			t.Fatal(err)
		}
		var terminal measurement.Reference
		for _, name := range []string{taskFinishedFile, ".config", kernelModulesManifest, kernelModulesOrder} {
			destination := name
			if name == ".config" {
				destination = kernelResolvedConfig
			}
			reference, err := retainFile(output, record.ExecutionMeasurement, backend+"-"+destination, filepath.Join(backend, destination), filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if name == taskFinishedFile {
				terminal = reference
			}
		}
		manifest.Interval.Evidence, diagnostic.Evidence = []measurement.Reference{terminal}, []measurement.Reference{terminal}
		record.ExecutionMeasurement.TaskIntervals = append(record.ExecutionMeasurement.TaskIntervals, manifest.Interval, diagnostic)
	}
	return output, record
}

func TestKernelCampaignRejectsUnequalInputs(t *testing.T) {
	expected := kernelCampaignInputs{Image: "sha256:" + strings.Repeat("a", 64), Toolchain: map[string]string{"gcc-12": "12.2"}, Configuration: "allmodconfig",
		ConfigDigests: map[string]string{"x86_64": strings.Repeat("b", 64), "arm64": strings.Repeat("c", 64)}, ModuleCounts: map[string]int{"x86_64": 2, "arm64": 3},
		Omissions: kernelOmissions(fullProfile), Targets: workloadTargets(kernelWorkload, fullProfile), Profile: fullProfile}
	if err := compareKernelCampaignInputs(expected, expected); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*kernelCampaignInputs){
		"image":            func(value *kernelCampaignInputs) { value.Image = "sha256:" + strings.Repeat("d", 64) },
		"compiler":         func(value *kernelCampaignInputs) { value.Toolchain["gcc-12"] = "different" },
		"missing-compiler": func(value *kernelCampaignInputs) { delete(value.Toolchain, "gcc-12") },
		"configuration":    func(value *kernelCampaignInputs) { value.Configuration = "defconfig" },
		"resolved-config":  func(value *kernelCampaignInputs) { value.ConfigDigests["arm64"] = strings.Repeat("d", 64) },
		"module-count":     func(value *kernelCampaignInputs) { value.ModuleCounts["arm64"]++ },
		"omissions":        func(value *kernelCampaignInputs) { value.Omissions = []string{"extra omission"} },
		"targets":          func(value *kernelCampaignInputs) { value.Targets = []string{"vmlinux"} },
		"profile":          func(value *kernelCampaignInputs) { value.Profile = smokeProfile },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(expected)
			var actual kernelCampaignInputs
			if err := json.Unmarshal(body, &actual); err != nil {
				t.Fatal(err)
			}
			mutate(&actual)
			if err := compareKernelCampaignInputs(expected, actual); err == nil {
				t.Fatalf("unequal %s was accepted", name)
			}
		})
	}
}

func TestKernelCollectionRequiresPreparedImageAndActualToolchain(t *testing.T) {
	root, record := kernelRawEvidenceFixture(t)
	environment := t.TempDir()
	if err := os.Mkdir(filepath.Join(environment, "cloud"), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(environment, "cloud", "provenance.json"), map[string]any{"options": map[string]string{"image": "sha256:" + strings.Repeat("a", 64)}}); err != nil {
		t.Fatal(err)
	}
	record.LinuxKernel.ToolVersions = map[string]string{}
	record.ExecutionMeasurement.Trial.Toolchain = record.LinuxKernel.ToolVersions
	for _, tool := range []string{"make", "gcc-12", "x86_64-linux-gnu-gcc-12", "aarch64-linux-gnu-gcc-12", "x86_64-linux-gnu-ld", "aarch64-linux-gnu-ld", "pahole", "kernel-package-manifest-sha256"} {
		record.LinuxKernel.ToolVersions[tool] = "synthetic"
	}
	record.LinuxKernel.ToolVersions["kernel-package-manifest-sha256"] = strings.Repeat("e", 64)
	record.ExecutionMeasurement.Trial.Evidence = append(record.ExecutionMeasurement.Trial.Evidence, measurement.Evidence{ID: "kernel-toolchain", SHA256: strings.Repeat("e", 64)})
	if _, err := readKernelCampaignInputs(environment, root, record); err != nil {
		t.Fatal(err)
	}
	delete(record.LinuxKernel.ToolVersions, "aarch64-linux-gnu-gcc-12")
	if _, err := readKernelCampaignInputs(environment, root, record); err == nil {
		t.Fatal("missing cross compiler identity was accepted")
	}
}
