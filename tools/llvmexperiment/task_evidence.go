package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	taskOperation          = "configure-build-create-output-directory"
	taskOperationBoundary  = "before CMake configuration through successful Ninja targets and output-directory creation; excludes source verification, artifact copy, executable checks and transport"
	taskStartedFile        = "started.json"
	taskFinishedFile       = "finished.json"
	taskRunningStatus      = "running"
	timeoutStatus          = "timeout"
	monotonicClockKind     = "monotonic"
	wallClockKind          = "wall"
	metadataInstanceURL    = "http://169.254.169.254/computeMetadata/v1/instance/id"
	metadataFlavorHeader   = "Metadata-Flavor"
	metadataGoogleFlavor   = "Google"
	metadataRequestTimeout = 750 * time.Millisecond
	metadataMaximumBytes   = 256
	linuxBootIdentityPath  = "/proc/sys/kernel/random/boot_id"
)

type hostIdentity struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	ObservedValue string `json:"observedValue"`
	BootID        string `json:"bootId"`
	Hostname      string `json:"hostname"`
	Platform      string `json:"platform"`
	Reason        string `json:"reason,omitempty"`
}

type taskRecording struct {
	Manifest   taskManifest
	Directory  string
	OutputPath string
	Started    time.Time
}

func beginTaskRecording(ctx context.Context, item task, root string) (*taskRecording, error) {
	identity, err := newIdentity()
	if err != nil {
		return nil, err
	}
	directory := os.Getenv(taskEvidenceEnvironment)
	if directory == "" {
		directory = filepath.Join(root, "..", "..", "task-evidence")
	}
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return nil, err
	}
	directory = filepath.Join(directory, identity)
	if err := os.Mkdir(directory, directoryMode); err != nil {
		return nil, err
	}
	host := readHostIdentity(ctx)
	descriptor := describeWorkload(item.Workload)
	recording := &taskRecording{Directory: directory, Manifest: taskManifest{
		Task: item, Node: taskNode(), Status: taskRunningStatus, AttemptID: identity,
		WorkingDirectory:   root,
		ConfigureArguments: configureArguments(item), BuildArguments: buildArguments(item),
		Host: host, Artifacts: []artifact{}, StartedClockKind: wallClockKind,
		Interval: measurement.TaskInterval{TaskID: item.Name, AttemptID: identity, NodeID: taskNode(),
			Status: taskRunningStatus, Operation: descriptor.Operation, Boundary: descriptor.Boundary,
			ClockDomain: host.ID + ":process-attempt:" + identity, ClockKind: monotonicClockKind,
			Evidence: []measurement.Reference{}, Reason: "modeled operation has not started"},
	}}
	if item.Workload == kernelWorkload {
		recording.Manifest.BuildEnvironment = kernelBuildEnvironment()
		recording.Manifest.ConfigureArguments = kernelCommand(item, root, []string{kernelConfiguration(item.Profile)}).Args
		recording.Manifest.BuildArguments = kernelCommand(item, root, kernelMakeTargets(item)).Args
	}
	if err := writeExclusiveJSON(filepath.Join(directory, taskStartedFile), recording.Manifest); err != nil {
		return nil, err
	}
	if os.Getenv(requireGCEEnvironment) == "true" && (!strings.HasPrefix(host.ID, "gce:") || host.ID != os.Getenv(expectedHostEnvironment)) {
		err := fmt.Errorf("LLVM task host %q differs from its independently described GCE executor", host.ID)
		return nil, errors.Join(err, recording.finish(err))
	}
	return recording, nil
}

func (recording *taskRecording) beginOperation() {
	recording.Started = time.Now()
	recording.Manifest.Started = recording.Started
	recording.Manifest.Interval.Reason = "modeled operation has not finished"
}

func (recording *taskRecording) endOperation() {
	seconds := time.Since(recording.Started).Seconds()
	zero := float64(0)
	recording.Manifest.Seconds = seconds
	recording.Manifest.Interval.DurationSeconds = &seconds
	recording.Manifest.Interval.StartSeconds = &zero
	recording.Manifest.Interval.EndSeconds = &seconds
	recording.Manifest.Interval.Reason = ""
}

func (recording *taskRecording) measurePhase(operation, boundary string, execute func() error) error {
	started := time.Now()
	err := execute()
	seconds := time.Since(started).Seconds()
	start := started.Sub(recording.Started).Seconds()
	end := start + seconds
	interval := measurement.TaskInterval{TaskID: recording.Manifest.Task.Name, AttemptID: recording.Manifest.AttemptID, NodeID: recording.Manifest.Node,
		Status: completedStatus, Operation: operation, Boundary: boundary, DurationSeconds: &seconds, StartSeconds: &start, EndSeconds: &end,
		ClockDomain: recording.Manifest.Interval.ClockDomain, ClockKind: monotonicClockKind, Evidence: []measurement.Reference{}}
	if err != nil {
		interval.Status, interval.Reason = failedStatus, err.Error()
	}
	recording.Manifest.Diagnostics = append(recording.Manifest.Diagnostics, interval)
	return err
}

func (recording *taskRecording) finish(taskError error) error {
	if !recording.Started.IsZero() && recording.Manifest.Interval.DurationSeconds == nil {
		recording.endOperation()
	}
	recording.Manifest.Status = completedStatus
	if taskError != nil {
		recording.Manifest.Status = failedStatus
		if errors.Is(taskError, context.DeadlineExceeded) {
			recording.Manifest.Status = timeoutStatus
		}
		recording.Manifest.Reason = taskError.Error()
		recording.Manifest.Interval.Reason = taskError.Error()
	}
	recording.Manifest.Interval.Status = recording.Manifest.Status
	var outputError error
	if taskError == nil && recording.OutputPath != "" {
		outputError = writeJSON(recording.OutputPath, recording.Manifest)
	}
	if outputError != nil {
		recording.Manifest.Status, recording.Manifest.Interval.Status = failedStatus, failedStatus
		recording.Manifest.Reason, recording.Manifest.Interval.Reason = outputError.Error(), outputError.Error()
	}
	return errors.Join(outputError, writeExclusiveJSON(filepath.Join(recording.Directory, taskFinishedFile), recording.Manifest))
}

func writeExclusiveJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return err
	}
	_, writeError := output.Write(append(content, '\n'))
	return errors.Join(writeError, output.Close())
}

func readHostIdentity(ctx context.Context) hostIdentity {
	hostname, _ := os.Hostname()
	boot, _ := os.ReadFile(linuxBootIdentityPath)
	host := hostIdentity{Hostname: hostname, BootID: strings.TrimSpace(string(boot)), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	ctx, cancel := context.WithTimeout(ctx, metadataRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataInstanceURL, nil)
	if err != nil {
		host.Reason = err.Error()
		return host
	}
	request.Header.Set(metadataFlavorHeader, metadataGoogleFlavor)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err == nil {
		defer response.Body.Close()
		value, readError := io.ReadAll(io.LimitReader(response.Body, metadataMaximumBytes))
		identity := strings.TrimSpace(string(value))
		if readError == nil && response.StatusCode == http.StatusOK && response.Header.Get(metadataFlavorHeader) == metadataGoogleFlavor && regexp.MustCompile(`^[0-9]+$`).MatchString(identity) {
			host.ID, host.Source, host.ObservedValue = "gce:"+identity, metadataInstanceURL, identity
			return host
		}
	}
	if host.BootID != "" {
		host.ID, host.Source, host.ObservedValue = "linux-boot:"+host.BootID, linuxBootIdentityPath, host.BootID
		return host
	}
	host.Reason = "physical host identity unavailable; process hostname is not proof of a distinct host"
	return host
}
