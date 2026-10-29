package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	llvmProtocolRevision      = "llvm-two-backend-requests/v2-retained-monotonic-attempts"
	coordinatorWindowID       = "coordinator-startup-through-verification"
	coordinatorWindowBoundary = "before executor startup through coordinator verification of all returned executable digests, versions and demangling; excludes provisioning, fixture/source/tool/client preparation and peer shutdown"
	clockUncertaintyReason    = "No quantitative whole-trial relative clock-rate error bound was established."
	clockRateAssumption       = "One reported second is treated as a compatible elapsed-time unit across the listed domains for this trial."
	unalignedClockReason      = "Task clocks have independent origins and no cross-host mapping was measured."
	remoteEvidenceTimeout     = 2 * time.Minute
)

func initializeMeasurement(settings options, result *buildResult) (*measurement.Measurement, error) {
	domain := "coordinator:" + settings.TrialID + ":monotonic"
	descriptor := describeWorkload(settings.Workload)
	record := &measurement.Measurement{
		Schema: measurement.Schema,
		Trial: measurement.Trial{ID: settings.TrialID, Repetition: settings.Repetition, Status: failedStatus, Tool: settings.Tool,
			Mode: settings.Mode, Profile: settings.Profile, Workload: descriptor.Description,
			SourceRevision: descriptor.Revision, BinarySHA256: result.BinarySHA256, ProtocolRevision: workloadProtocol(settings.Workload),
			Toolchain: result.ToolVersions, Evidence: []measurement.Evidence{}},
		Graph: measurement.Graph{ID: descriptor.Name + "-outer:" + settings.TrialID, Scope: "analysis-projection",
			Provenance: "generated before execution from the retained two-request fixture; duration annotations attach afterward",
			Vertices:   []measurement.Vertex{}, Edges: []measurement.Edge{},
			OmittedWork: []string{"source/tool/VM preparation", "artifact staging, packing, copying and verification", "transport and coordination", "native executor and inner build graphs are not exported as this graph"}},
		Resources: measurement.Resources{Nodes: []map[string]any{}, Pools: []map[string]any{}, Nested: []map[string]any{},
			TaskSlotLimit:      measurement.Quantity{Reason: "executor admission and fixture configuration have not been retained"},
			UsableOnePoolSlots: measurement.Quantity{Reason: "fixed interchangeable zero-overhead slots are not established; placement, transport and setup remain binding"}},
		TaskIntervals: []measurement.TaskInterval{}, TimeWindows: []measurement.TimeWindow{{ID: "modeled-task-span", Boundary: descriptor.Boundary,
			ClockDomain: "unmapped-task-domains:" + settings.TrialID, ClockKind: monotonicClockKind, Reason: unalignedClockReason}},
		ObservedPeakOverlap: measurement.Quantity{Reason: unalignedClockReason}, Analysis: []measurement.Calculation{},
		ClockCompatibility: map[string]any{"referenceDomain": domain, "domains": []string{domain}, "method": "nominal-seconds",
			"assumptions": []string{clockRateAssumption}, "uncertainty": map[string]any{"relativeRateErrorBound": nil, "reason": clockUncertaintyReason}, "evidence": []measurement.Reference{}},
		Premises: map[string]any{"precedenceValid": true, "enclosingWindowId": coordinatorWindowID},
	}
	configuration := map[string]any{"tool": settings.Tool, "mode": settings.Mode, "profile": settings.Profile,
		"source": settings.Source, "sourceRevision": descriptor.Revision, "workload": descriptor.Name, "jobsPerExecutor": settings.Jobs,
		"timeoutSeconds": settings.Timeout.Seconds(), "timeoutBoundary": "run command entry before fixture/source/client preparation through completion; cleanup is separately bounded",
		"buildTargets": workloadTargets(settings.Workload, settings.Profile), "returnedTools": workloadOutputs(settings.Workload, settings.Profile), "remotePlan": settings.Remote}
	configuration["sourceIdentityMeaning"] = "trial.sourceRevision pins the selected build workload; tested Dispat source and binary identities are recorded separately"
	configuration["dispat"] = map[string]any{"version": result.ToolVersions[dispatTool], "sourceRevision": result.ToolVersions["dispat-source-revision"],
		"binaryId": result.BinaryID, "binarySha256": result.BinarySHA256}
	requests := []map[string]any{}
	for _, backend := range descriptor.Backends {
		item := taskForBackend(settings, settings.TrialID, backend)
		requests = append(requests, map[string]any{"task": item, "configureArguments": configureArguments(item), "buildArguments": buildArguments(item)})
	}
	if settings.Workload == kernelWorkload {
		configuration["configuration"], configuration["omissions"], configuration["buildEnvironment"] = kernelConfiguration(settings.Profile), kernelOmissions(settings.Profile), kernelBuildEnvironment()
	}
	configuration["requests"] = requests
	config, err := retainJSON(settings.Output, record, "configuration", "configuration.json", configuration)
	if err != nil {
		return nil, err
	}
	record.Trial.ConfigurationSHA256 = record.Trial.Evidence[len(record.Trial.Evidence)-1].SHA256
	if settings.Workload == kernelWorkload {
		if _, err := os.Stat("/etc/dispat-kernel-toolchain.tsv"); err == nil {
			if _, err := retainFile(settings.Output, record, "kernel-toolchain", "kernel-toolchain.tsv", "/etc/dispat-kernel-toolchain.tsv"); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	for _, backend := range descriptor.Backends {
		item := taskForBackend(settings, settings.TrialID, backend)
		record.Graph.Vertices = append(record.Graph.Vertices, measurement.Vertex{ID: item.Name, Operation: descriptor.Operation, DurationInputs: []measurement.Reference{}})
		nested := map[string]any{"taskId": item.Name, "backend": backend, "unit": "inner-build-job", "evidence": []measurement.Reference{config}}
		if settings.Workload == kernelWorkload {
			nested["makeJobs"], nested["linkPool"], nested["linkPoolReason"] = settings.Jobs, nil, "link recipes share the configured GNU make job budget; no independent link pool"
		} else {
			nested["ninjaJobs"], nested["linkPool"] = settings.Jobs, 1
		}
		record.Resources.Nested = append(record.Resources.Nested, nested)
	}
	record.Graph.Counts.Vertices = len(record.Graph.Vertices)
	graph, err := retainJSON(settings.Output, record, "graph-before", "graph-before.json", record.Graph)
	if err != nil {
		return nil, err
	}
	record.Graph.Evidence = []measurement.Reference{graph}
	record.Premises["evidence"] = []measurement.Reference{graph}
	return record, nil
}

func retainFixture(settings options, data fixture, record *measurement.Measurement) error {
	descriptor := describeWorkload(settings.Workload)
	files := map[string]string{}
	for _, name := range []string{"dispat.yaml", "nx.json", "lerna.json", "package.json"} {
		content, err := os.ReadFile(filepath.Join(data.Root, name))
		if err != nil {
			return err
		}
		files[name] = string(content)
	}
	for _, item := range data.Tasks {
		for _, name := range []string{taskFileName, "package.json"} {
			path := filepath.Join("packages", item.Name, name)
			content, err := os.ReadFile(filepath.Join(data.Root, path))
			if err != nil {
				return err
			}
			files[path] = string(content)
		}
	}
	reference, err := retainJSON(settings.Output, record, "fixture", "fixture.json", map[string]any{"tasks": data.Tasks, "files": files,
		"outerTaskLimit": executorCount, "perNodeTaskLimit": 1, "operation": descriptor.Operation, "boundary": descriptor.Boundary})
	if err != nil {
		return err
	}
	record.Graph.Evidence = append(record.Graph.Evidence, reference)
	capacity := float64(executorCount)
	record.Resources.TaskSlotLimit = measurement.Quantity{Value: &capacity, Evidence: []measurement.Reference{reference}, Reason: "two admitted executor processes, each capped at one outer build task; inner build jobs are separate"}
	record.Resources.Pools = append(record.Resources.Pools, map[string]any{"id": "outer-tasks", "unit": "outer-task-slot", "configuredCapacity": executorCount,
		"capacityUpperBound": executorCount, "tasks": []string{data.Tasks[0].Name, data.Tasks[1].Name}, "weights": []int{1, 1}, "evidence": []measurement.Reference{reference}})
	for index, node := range data.Nodes {
		entry := map[string]any{"id": node.Name, "hostId": nil, "reason": "physical identity has not yet been observed", "configuredTaskSlots": 1,
			"admittedTaskSlots": nil, "cpuQuota": nil, "memoryBytes": nil, "evidence": []measurement.Reference{reference},
			"eligibleTasks": []string{data.Tasks[0].Name, data.Tasks[1].Name}}
		if settings.Mode == manualGCPMode {
			entry["eligibleTasks"] = []string{data.Tasks[index].Name}
		}
		if node.Remote != nil {
			hardware, err := retainFile(settings.Output, record, "hardware-"+node.Name, filepath.Join("cloud", node.Name+".json"), node.Remote.EvidencePath)
			if err != nil {
				return err
			}
			entry["hostId"], entry["reason"], entry["admittedTaskSlots"] = node.Remote.HostID, "", 1
			entry["cpuQuota"], entry["memoryBytes"], entry["machineType"], entry["cpuPlatform"] = node.Remote.CPUQuota, node.Remote.MemoryBytes, node.Remote.MachineType, node.Remote.CPUPlatform
			entry["topology"] = "independently described GCE virtual machine; separate coordinator"
			entry["evidence"] = []measurement.Reference{reference, hardware}
		}
		record.Resources.Nodes = append(record.Resources.Nodes, entry)
	}
	return nil
}

func retainCoordinatorWindow(settings options, record *measurement.Measurement, started time.Time, completed bool) error {
	seconds := time.Since(started).Seconds()
	contains := []string{}
	if completed {
		for _, vertex := range record.Graph.Vertices {
			contains = append(contains, vertex.ID)
		}
	}
	boundary := coordinatorWindowBoundary
	if settings.Workload == kernelWorkload {
		boundary = "before executor startup through coordinator verification of returned kernel ELF, architecture boot image, resolved configuration and complete module manifest; excludes provisioning, fixture/source/tool preparation and peer shutdown"
	}
	raw := map[string]any{"seconds": seconds, "clockKind": monotonicClockKind, "clockDomain": record.ClockCompatibility["referenceDomain"],
		"boundary": boundary, "startedBeforePeerLaunch": true, "allArtifactsVerifiedBeforeEnd": completed, "containsTasks": contains}
	reference, err := retainJSON(settings.Output, record, "coordinator-window", "coordinator-window.json", raw)
	if err != nil {
		return err
	}
	window := measurement.TimeWindow{ID: coordinatorWindowID, Boundary: boundary, Seconds: &seconds,
		ClockDomain: record.ClockCompatibility["referenceDomain"].(string), ClockKind: monotonicClockKind, Evidence: []measurement.Reference{reference}, ContainsTasks: contains}
	if completed {
		window.ContainmentProof = []measurement.Reference{reference}
	} else {
		window.Reason = "failure elapsed diagnostic; this is not a completed-trial duration"
	}
	record.TimeWindows = append(record.TimeWindows, window)
	return nil
}

func retainTaskEvidence(settings options, data fixture, record *measurement.Measurement, output io.Writer) error {
	var failures []error
	for _, node := range data.Nodes {
		directory := node.EvidenceDir
		if settings.Remote != nil {
			destination := filepath.Join(settings.Output, "remote", node.Name)
			ctx, cancel := context.WithTimeout(context.Background(), remoteEvidenceTimeout)
			_, err := callRemote(ctx, settings.Remote, remoteRequest{Action: remoteFetchAction, Node: node.Name, TrialID: data.Run, Destination: destination}, output)
			cancel()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			directory = filepath.Join(destination, "evidence")
		}
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, attempt := range entries {
			if !attempt.IsDir() {
				continue
			}
			if err := retainAttempt(settings.Output, record, node, filepath.Join(directory, attempt.Name())); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func retainAttempt(output string, record *measurement.Measurement, node nodeFixture, directory string) error {
	var manifest taskManifest
	var terminal measurement.Reference
	isFinished := false
	for _, name := range []string{taskStartedFile, taskFinishedFile} {
		path := filepath.Join(directory, name)
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		manifest = taskManifest{}
		if err := json.Unmarshal(content, &manifest); err != nil {
			return err
		}
		if manifest.AttemptID != filepath.Base(directory) || manifest.Interval.AttemptID != manifest.AttemptID ||
			manifest.Interval.TaskID != manifest.Task.Name || manifest.Interval.NodeID != manifest.Node {
			return fmt.Errorf("LLVM evidence directory %s has inconsistent attempt, task or node identity", directory)
		}
		identity := "attempt-" + node.Name + "-" + manifest.AttemptID + "-" + name
		terminal, err = retainFile(output, record, identity, filepath.Join("attempts", node.Name, manifest.AttemptID, name), path)
		if err != nil {
			return err
		}
		isFinished = name == taskFinishedFile
	}
	if manifest.AttemptID == "" {
		return fmt.Errorf("LLVM evidence directory %s has no attempt identity", directory)
	}
	if manifest.Node != node.Name || manifest.Task.Run != record.Trial.ID || node.Remote != nil && manifest.Host.ID != node.Remote.HostID {
		return fmt.Errorf("LLVM attempt %s has inconsistent trial, node or independently evidenced host identity", manifest.AttemptID)
	}
	for _, name := range []string{kernelConfiguredConfig, kernelResolvedConfig, kernelModulesManifest, kernelModulesOrder} {
		path := filepath.Join(directory, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := retainFile(output, record, "attempt-"+node.Name+"-"+manifest.AttemptID+"-"+name, filepath.Join("attempts", node.Name, manifest.AttemptID, name), path); err != nil {
			return err
		}
	}
	interval := manifest.Interval
	if !isFinished {
		interval.Status, interval.Reason = failedStatus, "attempt has no terminal record; process may have been killed"
	}
	interval.Evidence = []measurement.Reference{terminal}
	record.TaskIntervals = append(record.TaskIntervals, interval)
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.TaskID != manifest.Task.Name || diagnostic.AttemptID != manifest.AttemptID || diagnostic.NodeID != node.Name || diagnostic.ClockDomain != interval.ClockDomain {
			return fmt.Errorf("task diagnostic has inconsistent identity or clock")
		}
		diagnostic.Evidence = []measurement.Reference{terminal}
		record.TaskIntervals = append(record.TaskIntervals, diagnostic)
	}
	domains := record.ClockCompatibility["domains"].([]string)
	if !slices.Contains(domains, interval.ClockDomain) {
		record.ClockCompatibility["domains"] = append(domains, interval.ClockDomain)
	}
	for _, resource := range record.Resources.Nodes {
		if resource["id"] == node.Name && manifest.Host.ID != "" {
			resource["hostId"], resource["reason"], resource["admittedTaskSlots"] = manifest.Host.ID, "", 1
		}
	}
	return nil
}

func attachDurationInputs(record *measurement.Measurement) {
	for vertex := range record.Graph.Vertices {
		inputs := []measurement.Reference{}
		for index, interval := range record.TaskIntervals {
			if interval.TaskID == record.Graph.Vertices[vertex].ID && interval.Operation == record.Graph.Vertices[vertex].Operation && interval.Status == completedStatus {
				inputs = append(inputs, measurement.Reference{Source: "measurement", Pointer: "/taskIntervals/" + strconv.Itoa(index) + "/durationSeconds"})
			}
		}
		if len(inputs) == 1 {
			record.Graph.Vertices[vertex].DurationInputs = inputs
		}
	}
}

func retainJSON(output string, record *measurement.Measurement, identity, relative string, value any) (measurement.Reference, error) {
	path := filepath.Join(output, relative)
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return measurement.Reference{}, err
	}
	if err := writeExclusiveJSON(path, value); err != nil {
		return measurement.Reference{}, err
	}
	return registerEvidence(record, identity, relative, path)
}

func retainFile(output string, record *measurement.Measurement, identity, relative, source string) (measurement.Reference, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return measurement.Reference{}, err
	}
	if !info.Mode().IsRegular() {
		return measurement.Reference{}, fmt.Errorf("LLVM retained evidence %s must be a regular file", source)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return measurement.Reference{}, err
	}
	path := filepath.Join(output, relative)
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return measurement.Reference{}, err
	}
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return measurement.Reference{}, err
	}
	_, writeError := destination.Write(content)
	if err := errors.Join(writeError, destination.Close()); err != nil {
		return measurement.Reference{}, err
	}
	return registerEvidence(record, identity, relative, path)
}

func registerEvidence(record *measurement.Measurement, identity, relative, path string) (measurement.Reference, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return measurement.Reference{}, err
	}
	digest := sha256.Sum256(content)
	record.Trial.Evidence = append(record.Trial.Evidence, measurement.Evidence{ID: identity, Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(digest[:])})
	return measurement.Reference{Source: "evidence", EvidenceID: identity, Pointer: ""}, nil
}
