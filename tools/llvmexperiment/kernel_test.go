package main

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestKernelSelectorRetainsSeparateSchemaAndExactFullWorkload(t *testing.T) {
	settings, err := parseOptions([]string{"--workload", kernelWorkload})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Source != kernelSourcePath || !reflect.DeepEqual(workloadTargets(settings.Workload, settings.Profile), []string{"vmlinux", "modules", "architecture-boot-image"}) {
		t.Fatal("kernel selector changed its pinned full workload")
	}
	result := initialResult(settings)
	body, err := json.Marshal(verdict{Experiment: kernelWorkload, LinuxKernel: &result})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if _, exists := value["llvm"]; exists {
		t.Fatal("kernel result was mislabeled LLVM")
	}
	kernel := value["linuxKernel"].(map[string]any)
	if _, exists := kernel["llvmVersion"]; exists {
		t.Fatal("kernel result included llvmVersion")
	}
	for _, field := range []string{"configDigests", "moduleCounts"} {
		if values, ok := kernel[field].(map[string]any); !ok || len(values) != 0 {
			t.Fatalf("unknown %s must begin as an empty map", field)
		}
	}
	for _, backend := range []string{"x86_64", "arm64"} {
		item := taskForBackend(settings, "kernel-test", backend)
		if err := validateTask(item); err != nil {
			t.Fatal(err)
		}
		if item.Revision != kernelRevision || item.Name != "kernel-"+backend || len(workloadOutputs(item.Workload, item.Profile)) != 7 {
			t.Fatal("kernel task provenance or artifact set drifted")
		}
	}
	content, err := os.ReadFile("../../tests/experiments/Dockerfile.kernel")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "origin "+kernelRevision) || !strings.Contains(string(content), "= "+kernelRevision) {
		t.Fatal("kernel image and runner source pins differ")
	}
}

func TestKernelFailureStopsModeledTimerBeforeConfigurationRetention(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "make")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nmkdir -p build\nprintf 'CONFIG_X86_64=y\\n' > build/.config\nexit 17\n"), executableFileMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(taskEvidenceEnvironment, filepath.Join(root, "evidence"))
	t.Setenv("DISPAT_EXECUTION_NODE", "failure-node")
	settings, _ := parseOptions([]string{"--workload", kernelWorkload})
	item := taskForBackend(settings, "kernel-failure", "x86_64")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recording, err := beginTaskRecording(ctx, item, root)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	request := kernelBuild{Task: item, Root: root, Recording: recording, RetainConfiguration: func(root string, recording *taskRecording) error {
		if recording.Manifest.Interval.DurationSeconds == nil || recording.Manifest.Interval.EndSeconds == nil {
			t.Fatal("failed operation timer still running when excluded configuration retention began")
		}
		retained = true
		return retainKernelConfiguration(root, recording)
	}}
	err = runKernelBuild(context.Background(), request)
	if err == nil || !retained {
		t.Fatal("failed make did not preserve failure and configuration")
	}
	seconds := *recording.Manifest.Interval.DurationSeconds
	if err := recording.finish(err); err != nil {
		t.Fatal(err)
	}
	if *recording.Manifest.Interval.DurationSeconds != seconds || recording.Manifest.Status != failedStatus {
		t.Fatal("retention changed the failed modeled duration")
	}
	if _, err := os.Stat(filepath.Join(recording.Directory, kernelResolvedConfig)); err != nil {
		t.Fatal(err)
	}
}

func TestKernelArtifactsVerifyArchitectureAndCompleteModuleInventory(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64"} {
		t.Run(backend, func(t *testing.T) {
			settings, _ := parseOptions([]string{"--workload", kernelWorkload})
			item := taskForBackend(settings, "artifact-fixture", backend)
			root, verification := kernelArtifactFixture(t, item)
			if err := verifyKernelArtifacts(root, item, verification); err != nil {
				t.Fatal(err)
			}
			verification.ModuleCount++
			if err := verifyKernelArtifacts(root, item, verification); err == nil {
				t.Fatal("partial module manifest passed as full")
			}
			verification.ModuleCount--
			other := "arm64"
			if backend == other {
				other = "x86_64"
			}
			writeFixtureELF(t, filepath.Join(root, "vmlinux.stripped"), other, false)
			if err := verifyKernelArtifacts(root, item, verification); err == nil {
				t.Fatal("wrong kernel architecture was accepted")
			}
		})
	}
}

func kernelArtifactFixture(t *testing.T, item task) (string, *kernelVerification) {
	t.Helper()
	root := t.TempDir()
	writeFixtureELF(t, filepath.Join(root, "vmlinux.stripped"), item.Backend, false)
	writeFixtureELF(t, filepath.Join(root, "sample-module.ko.stripped"), item.Backend, true)
	header := make([]byte, 0x206)
	header[0x1fe], header[0x1ff] = 0x55, 0xaa
	copy(header[0x202:], "HdrS")
	if item.Backend == "arm64" {
		copy(header[0x38:], []byte{'A', 'R', 'M', 0x64})
	}
	configuration := "CONFIG_X86_64=y\n"
	if item.Backend == "arm64" {
		configuration = "CONFIG_ARM64=y\n"
	}
	for name, body := range map[string][]byte{"boot-image": header, ".config": []byte(configuration), "Module.symvers": []byte("synthetic symbol fixture\n"), "modules.order": []byte("drivers/example.o\n")} {
		if err := os.WriteFile(filepath.Join(root, name), body, fileMode); err != nil {
			t.Fatal(err)
		}
	}
	config, err := describeKernelArtifact(filepath.Join(root, ".config"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := describeKernelArtifact(filepath.Join(root, "sample-module.ko.stripped"))
	if err != nil {
		t.Fatal(err)
	}
	module.Path, module.Package = "drivers/example.ko", item.Name
	original, err := describeKernelArtifact(filepath.Join(root, "vmlinux.stripped"))
	if err != nil {
		t.Fatal(err)
	}
	original.Path = "vmlinux"
	manifest := kernelModuleManifest{SourceRevision: kernelRevision, Backend: item.Backend, ConfigSHA256: config.SHA256, Modules: []artifact{module}, SampleModule: module.Path, OriginalVmlinux: original}
	if err := writeJSON(filepath.Join(root, "modules-manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	return root, &kernelVerification{ConfigSHA256: config.SHA256, KernelRelease: kernelVersion, ModuleCount: 1, AreModulesBuilt: true, SampleModule: module.Path}
}

func writeFixtureELF(t *testing.T, path, backend string, isModule bool) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	kind, machine := elf.ET_EXEC, elf.EM_X86_64
	if isModule {
		kind = elf.ET_REL
	}
	if backend == "arm64" {
		machine = elf.EM_AARCH64
	}
	binary.LittleEndian.PutUint16(header[16:], uint16(kind))
	binary.LittleEndian.PutUint16(header[18:], uint16(machine))
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint16(header[52:], 64)
	if err := os.WriteFile(path, header, fileMode); err != nil {
		t.Fatal(err)
	}
}

func TestKernelMeasurementKeepsMakeLimitsAndDiagnosticsOutsideProjection(t *testing.T) {
	settings, _ := parseOptions([]string{"--workload", kernelWorkload, "--trial-id", "kernel-measurement"})
	settings.Output = t.TempDir()
	result := initialResult(settings)
	result.BinarySHA256 = strings.Repeat("a", 64)
	result.ToolVersions = map[string]string{"fixture": "synthetic"}
	record, err := initializeMeasurement(settings, &result)
	if err != nil {
		t.Fatal(err)
	}
	for _, vertex := range record.Graph.Vertices {
		if vertex.Operation != kernelOperation {
			t.Fatal("kernel operation was mislabeled")
		}
	}
	for _, nested := range record.Resources.Nested {
		if nested["makeJobs"] != 2 || nested["linkPool"] != nil || nested["ninjaJobs"] != nil {
			t.Fatal("make limits became invented Ninja/link capacity")
		}
	}
	duration := 1.0
	for _, vertex := range record.Graph.Vertices {
		record.TaskIntervals = append(record.TaskIntervals, measurement.TaskInterval{TaskID: vertex.ID, Operation: kernelOperation, Status: completedStatus, DurationSeconds: &duration}, measurement.TaskInterval{TaskID: vertex.ID, Operation: "artifact-staging", Status: completedStatus, DurationSeconds: &duration})
	}
	attachDurationInputs(record)
	for _, vertex := range record.Graph.Vertices {
		if len(vertex.DurationInputs) != 1 {
			t.Fatal("omitted staging phase contaminated modeled build duration")
		}
	}
}

func TestKernelSuccessfulSyntheticBuildRetainsFullEvidenceAndSeparatePhases(t *testing.T) {
	// This exercises orchestration with synthetic ELF headers and no compiler;
	// its durations are test diagnostics, never benchmark observations.
	toolsRoot := t.TempDir()
	for name, body := range map[string]string{"make": "#!/bin/sh\nexit 0\n", "x86_64-linux-gnu-objcopy": "#!/bin/sh\ncp \"$2\" \"$3\"\n", "aarch64-linux-gnu-objcopy": "#!/bin/sh\ncp \"$2\" \"$3\"\n"} {
		if err := os.WriteFile(filepath.Join(toolsRoot, name), []byte(body), executableFileMode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", toolsRoot+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(taskEvidenceEnvironment, filepath.Join(t.TempDir(), "evidence"))
	settings, _ := parseOptions([]string{"--workload", kernelWorkload, "--trial-id", "synthetic-kernel-build"})
	settings.Output = t.TempDir()
	build := initialResult(settings)
	build.Status, build.BinarySHA256 = completedStatus, strings.Repeat("a", 64)
	build.ToolVersions = map[string]string{"fixture": "synthetic"}
	record, err := initializeMeasurement(settings, &build)
	if err != nil {
		t.Fatal(err)
	}
	data, err := prepareFixture(context.Background(), settings, t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := retainFixture(settings, data, record); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for index, backend := range build.Backends {
		item := taskForBackend(settings, settings.TrialID, backend)
		node := data.Nodes[index]
		root := filepath.Join(t.TempDir(), "packages", item.Name)
		prepareSyntheticKernelBuild(t, root, item)
		t.Setenv("DISPAT_EXECUTION_NODE", node.Name)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		recording, err := beginTaskRecording(ctx, item, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := runKernelBuild(context.Background(), kernelBuild{Task: item, Root: root, Recording: recording}); err != nil {
			t.Fatal(err)
		}
		if err := recording.finish(nil); err != nil {
			t.Fatal(err)
		}
		if len(recording.Manifest.Diagnostics) != 2 || *recording.Manifest.Diagnostics[0].StartSeconds < *recording.Manifest.Interval.EndSeconds || *recording.Manifest.Diagnostics[1].StartSeconds < *recording.Manifest.Diagnostics[0].EndSeconds {
			t.Fatal("staging and validation were not separately recorded after modeled build")
		}
		build.ConfigDigests[backend], build.ModuleCounts[backend] = recording.Manifest.Kernel.ConfigSHA256, recording.Manifest.Kernel.ModuleCount
		build.Artifacts = append(build.Artifacts, recording.Manifest.Artifacts...)
		if err := retainAttempt(settings.Output, record, node, recording.Directory); err != nil {
			t.Fatal(err)
		}
	}
	attachDurationInputs(record)
	for _, vertex := range record.Graph.Vertices {
		if len(vertex.DurationInputs) != 1 {
			t.Fatal("post-build diagnostic phases entered the modeled build duration")
		}
	}
	if err := verifyRetainedEvidence(settings.Output, record); err != nil {
		t.Fatal(err)
	}
	if err := validateKernelCampaignResult(&build); err != nil {
		t.Fatal(err)
	}
	if err := bindKernelRawEvidence(settings.Output, verdict{LinuxKernel: &build, ExecutionMeasurement: record}); err != nil {
		t.Fatal(err)
	}
	if err := retainCoordinatorWindow(settings, record, started, true); err != nil {
		t.Fatal(err)
	}
	if err := verifyObservedMeasurement(settings.Output, record); err != nil {
		t.Fatal(err)
	}
	record.Trial.Status = completedStatus
	if err := measurement.Derive(record); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := measurement.Validate(encoded); err != nil {
		t.Fatal(err)
	}
}

func prepareSyntheticKernelBuild(t *testing.T, root string, item task) {
	t.Helper()
	fixture, _ := kernelArtifactFixture(t, item)
	for source, destination := range map[string]string{"vmlinux.stripped": "vmlinux", "sample-module.ko.stripped": "drivers/example.ko", ".config": ".config", "boot-image": kernelBootPath(item), "Module.symvers": "Module.symvers", "modules.order": "modules.order"} {
		path := filepath.Join(root, buildDirectory, destination)
		if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
			t.Fatal(err)
		}
		if _, err := copyArtifact(filepath.Join(fixture, source), path); err != nil {
			t.Fatal(err)
		}
	}
	release := filepath.Join(root, buildDirectory, "include", "config", "kernel.release")
	if err := os.MkdirAll(filepath.Dir(release), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte(kernelVersion+"\n"), fileMode); err != nil {
		t.Fatal(err)
	}
}
