package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDockerStoreEvidenceRejectsDifferentIDSemantics(t *testing.T) {
	for _, scenario := range []struct {
		name, content string
		wantError     bool
	}{
		{name: "classic", content: `{"ServerVersion":"28.0.4","Driver":"overlay2","DriverStatus":[["Backing Filesystem","extfs"]]}`},
		{name: "newer classic", content: `{"ServerVersion":"29.9.2","Driver":"overlay2","DriverStatus":[]}`},
		{name: "containerd", content: `{"ServerVersion":"29.9.2","Driver":"overlayfs","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`, wantError: true},
		{name: "containerd marker with classic name", content: `{"ServerVersion":"29.9.2","Driver":"overlay2","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`, wantError: true},
		{name: "unknown store", content: `{"ServerVersion":"28.0.4","Driver":"vfs"}`, wantError: true},
		{name: "missing version", content: `{"Driver":"overlay2"}`, wantError: true},
		{name: "invalid JSON", content: `unexpected daemon response`, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			err := d.retainDockerStore([]byte(scenario.content), coordinatorDockerInfoFileName)
			if (err != nil) != scenario.wantError {
				t.Fatalf("store result: %v", err)
			}
			retained, err := os.ReadFile(filepath.Join(d.settings.Root, "results/cloud", coordinatorDockerInfoFileName))
			if err != nil || string(retained) != scenario.content {
				t.Fatalf("raw store evidence missing: %q, %v", retained, err)
			}
		})
	}
}

func TestCoordinatorStoreMismatchFailsBeforeProvisioning(t *testing.T) {
	d := &driver{settings: options{Root: t.TempDir(), ID: "store-check", MachineType: defaultMachineTypeName}}
	calls := 0
	d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
		calls++
		if request.Name != "docker" || !slices.Equal(request.Args, []string{dockerInfoArgumentsName, dockerInfoFormatFlag, dockerInfoFormat}) {
			t.Fatalf("store mismatch reached provisioning: %+v", request)
		}
		return []byte(`{"ServerVersion":"29.9.2","Driver":"overlayfs","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`), nil
	}
	if err := d.run(t.Context()); err == nil || !strings.Contains(err.Error(), "classic overlay2") || calls != 1 {
		t.Fatalf("calls %d, result %v", calls, err)
	}
	if _, err := os.Stat(filepath.Join(d.settings.Root, "results/cloud", coordinatorDockerInfoFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStoreMismatchFailsBeforeImageLoad(t *testing.T) {
	d := cleanupDriver(t)
	var worker instance
	if err := json.Unmarshal([]byte(`{"name":"worker-1","networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &worker); err != nil {
		t.Fatal(err)
	}
	d.state.Instances = []instance{worker}
	calls := 0
	d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
		calls++
		expected := shellCommand([]string{"sudo", "docker", dockerInfoArgumentsName, dockerInfoFormatFlag, dockerInfoFormat})
		if request.Name != "ssh" || request.Args[len(request.Args)-1] != expected {
			t.Fatalf("store mismatch reached image load or execution: %+v", request)
		}
		return []byte(`{"ServerVersion":"29.9.2","Driver":"overlayfs","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`), nil
	}
	if err := d.prepareWorkers(t.Context()); err == nil || !strings.Contains(err.Error(), "classic overlay2") || calls != 1 {
		t.Fatalf("calls %d, result %v", calls, err)
	}
	if _, err := os.Stat(filepath.Join(d.settings.Root, "results/cloud", worker.Name+workerDockerInfoFileSuffix)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStartupSelectsClassicDockerStoreBeforeReady(t *testing.T) {
	check := exec.CommandContext(t.Context(), "sh", "-n")
	check.Stdin = strings.NewReader(startupScript)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("startup shell syntax: %s, %v", output, err)
	}
	_, configuration, found := strings.Cut(startupScript, "<<'DOCKER_CONFIG'\n")
	if !found {
		t.Fatal("startup has no explicit Docker store configuration")
	}
	configuration, remainder, found := strings.Cut(configuration, "\nDOCKER_CONFIG\n")
	if !found {
		t.Fatal("startup Docker store configuration is unterminated")
	}
	var daemon struct {
		Driver   string          `json:"storage-driver"`
		Features map[string]bool `json:"features"`
	}
	if err := json.Unmarshal([]byte(configuration), &daemon); err != nil {
		t.Fatal(err)
	}
	containerd, isExplicit := daemon.Features["containerd-snapshotter"]
	if daemon.Driver != "overlay2" || !isExplicit || containerd || !strings.HasPrefix(remainder, "systemctl restart docker\ntest \"$(docker info --format '{{.Driver}}')\" = overlay2\n") {
		t.Fatalf("startup does not enforce classic image identities before ready: %s", startupScript)
	}
}
