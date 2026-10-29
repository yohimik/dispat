package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const testBootImage = "https://www.googleapis.com/compute/v1/projects/ubuntu-os-cloud/global/images/ubuntu-2404-noble-amd64-v20261023"

func hardwarePinArgs() []string {
	return []string{"--project", "my-project", "--id", "123-1-hybrid", "--image", "sha256:" + strings.Repeat("a", 64), "--archive", "image.tar.gz", "--source", strings.Repeat("b", 40), "--binary-sha256", strings.Repeat("c", 64)}
}

func TestHardwarePinOptionsRejectNonExactReferences(t *testing.T) {
	for _, platform := range []string{cascadeLakePlatformName, iceLakePlatformName} {
		settings, err := parseOptions(append(hardwarePinArgs(), "--boot-image", testBootImage, "--cpu-platform", platform))
		if err != nil || settings.BootImage != testBootImage || settings.CPUPlatform != platform {
			t.Fatalf("pins %+v, error %v", settings, err)
		}
	}
	for _, pin := range []struct{ flag, value string }{
		{"--boot-image", ""},
		{"--boot-image", "ubuntu-2404-lts-amd64"},
		{"--boot-image", "https://www.googleapis.com/compute/v1/projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-amd64"},
		{"--boot-image", strings.Replace(testBootImage, "ubuntu-os-cloud", "other-project", 1)},
		{"--boot-image", testBootImage + "?family=latest"},
		{"--boot-image", testBootImage + "/../other"},
		{"--boot-image", testBootImage + "#other"},
		{"--boot-image", " " + testBootImage},
		{"--cpu-platform", ""},
		{"--cpu-platform", "Automatic"},
		{"--cpu-platform", "Intel Ice Lake "},
		{"--cpu-platform", "Intel Ice Lake\n"},
		{"--cpu-platform", "AMD Milan"},
	} {
		t.Run(pin.flag+"/"+pin.value, func(t *testing.T) {
			args := append(hardwarePinArgs(), pin.flag, pin.value)
			if _, err := parseOptions(args); err == nil ||
				!strings.Contains(err.Error(), "must be") {
				t.Fatalf("invalid hardware pin accepted by argument parser: %v", err)
			}
		})
	}
}

func TestMachineProfileOptionsRequireCompatibleExactCPU(t *testing.T) {
	for _, scenario := range []struct {
		name, machine, platform string
		wantError               bool
	}{
		{name: "N2 remains default", machine: defaultMachineTypeName},
		{name: "N2 Cascade Lake", machine: defaultMachineTypeName, platform: cascadeLakePlatformName},
		{name: "N2 Ice Lake", machine: defaultMachineTypeName, platform: iceLakePlatformName},
		{name: "C3 Sapphire Rapids", machine: c3MachineTypeName, platform: sapphirePlatformName},
		{name: "C3 requires explicit CPU", machine: c3MachineTypeName, wantError: true},
		{name: "C3 rejects Cascade Lake", machine: c3MachineTypeName, platform: cascadeLakePlatformName, wantError: true},
		{name: "C3 rejects Ice Lake", machine: c3MachineTypeName, platform: iceLakePlatformName, wantError: true},
		{name: "N2 rejects Sapphire Rapids", machine: defaultMachineTypeName, platform: sapphirePlatformName, wantError: true},
		{name: "unknown machine", machine: "c3-standard-8", platform: sapphirePlatformName, wantError: true},
		{name: "empty machine", wantError: true},
		{name: "machine whitespace", machine: c3MachineTypeName + " ", platform: sapphirePlatformName, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			args := append(hardwarePinArgs(), "--machine-type", scenario.machine)
			if scenario.platform != "" {
				args = append(args, "--cpu-platform", scenario.platform)
			}
			settings, err := parseOptions(args)
			if (err != nil) != scenario.wantError || err == nil && (settings.MachineType != scenario.machine || settings.CPUPlatform != scenario.platform) {
				t.Fatalf("settings %+v, error %v", settings, err)
			}
		})
	}
	settings, err := parseOptions(hardwarePinArgs())
	if err != nil || settings.MachineType != defaultMachineTypeName {
		t.Fatalf("default changed: %+v, %v", settings, err)
	}
}

func TestProvisionRejectsIncompatibleProfileBeforeCommands(t *testing.T) {
	d := &driver{settings: options{MachineType: c3MachineTypeName, CPUPlatform: iceLakePlatformName}}
	d.runCommand = func(context.Context, invocation) ([]byte, error) {
		t.Fatal("invalid machine profile ran an external command")
		return nil, nil
	}
	if err := d.provision(t.Context()); err == nil {
		t.Fatal("incompatible CPU profile accepted")
	}
}

func TestBootImageResolutionUsesExactPinWithoutFamilyFallback(t *testing.T) {
	d := &driver{settings: options{BootImage: testBootImage}}
	d.runCommand = func(context.Context, invocation) ([]byte, error) {
		t.Fatal("explicit boot image invoked a resolver")
		return nil, nil
	}
	if image, err := d.resolveBootImage(t.Context()); err != nil || image != testBootImage {
		t.Fatalf("resolved %q: %v", image, err)
	}
	for _, scenario := range []struct {
		name, response string
		failure        error
		wantError      bool
	}{
		{name: "one family resolution", response: testBootImage + "\n"},
		{name: "invalid family response", response: testBootImage + "/mutable", wantError: true},
		{name: "resolver unavailable", failure: errors.New("unavailable"), wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d.settings.BootImage = ""
			calls := 0
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				calls++
				if request.Name != "gcloud" || !slices.Equal(request.Args, []string{"--quiet", "--project", "ubuntu-os-cloud", "compute", "images", "describe-from-family", "ubuntu-2404-lts-amd64", "--format=value(selfLink)"}) {
					t.Fatalf("unexpected resolver command %+v", request)
				}
				return []byte(scenario.response), scenario.failure
			}
			image, err := d.resolveBootImage(t.Context())
			if calls != 1 || (err != nil) != scenario.wantError || err == nil && image != testBootImage {
				t.Fatalf("calls %d, image %q, error %v", calls, image, err)
			}
		})
	}
}

func TestProvisionRequiresExactCampaignPinsOnEachWorker(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		machine       string
		expectedCPU   string
		platforms     []string
		diskImage     string
		actualMachine string
		diskInterface string
		nicType       string
		memoryMB      int
		createErr     error
		wantError     string
		wantVMs       int
	}{
		{name: "matching pair", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, wantVMs: 2},
		{name: "first worker drifts", platforms: []string{cascadeLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "second worker drifts", platforms: []string{iceLakePlatformName, cascadeLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 2},
		{name: "both workers drift together", platforms: []string{cascadeLakePlatformName, cascadeLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "newer minimum match is not exact", platforms: []string{"Intel Sapphire Rapids", iceLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "boot disk drifts", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage + "-other", wantError: "selected exact image", wantVMs: 1},
		{name: "creation fails without fallback", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, createErr: errors.New("platform unavailable"), wantError: "platform unavailable", wantVMs: 1},
		{name: "C3 matching pair", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, platforms: []string{sapphirePlatformName, sapphirePlatformName}, diskImage: testBootImage, wantVMs: 2},
		{name: "C3 wrong first CPU", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, platforms: []string{iceLakePlatformName, sapphirePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "C3 wrong second CPU", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, platforms: []string{sapphirePlatformName, iceLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 2},
		{name: "C3 wrong machine", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, actualMachine: defaultMachineTypeName, platforms: []string{sapphirePlatformName, sapphirePlatformName}, diskImage: testBootImage, wantError: "required identity/type", wantVMs: 1},
		{name: "C3 wrong disk interface", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, diskInterface: "SCSI", platforms: []string{sapphirePlatformName, sapphirePlatformName}, diskImage: testBootImage, wantError: "NVME boot disk", wantVMs: 1},
		{name: "C3 wrong NIC", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, nicType: "VIRTIO_NET", platforms: []string{sapphirePlatformName, sapphirePlatformName}, diskImage: testBootImage, wantError: "GVNIC network interface", wantVMs: 1},
		{name: "C3 wrong boot image", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, platforms: []string{sapphirePlatformName, sapphirePlatformName}, diskImage: testBootImage + "-other", wantError: "selected exact image", wantVMs: 1},
		{name: "C3 wrong capacity", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, memoryMB: 8192, wantError: "unexpected CPU or memory capacity"},
		{name: "C3 stockout has no fallback", machine: c3MachineTypeName, expectedCPU: sapphirePlatformName, createErr: errors.New("platform unavailable"), wantError: "platform unavailable", wantVMs: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if scenario.machine == "" {
				scenario.machine = defaultMachineTypeName
			}
			if scenario.expectedCPU == "" {
				scenario.expectedCPU = iceLakePlatformName
			}
			if scenario.actualMachine == "" {
				scenario.actualMachine = scenario.machine
			}
			if scenario.diskInterface == "" {
				scenario.diskInterface = c3BootDiskInterfaceName
			}
			if scenario.nicType == "" {
				scenario.nicType = c3NICTypeName
			}
			if scenario.memoryMB == 0 {
				scenario.memoryMB = 16384
			}
			d := cleanupDriver(t)
			d.settings.BootImage, d.settings.CPUPlatform, d.settings.MachineType = testBootImage, scenario.expectedCPU, scenario.machine
			d.state.Options = d.settings
			created := []string{}
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				switch request.Name {
				case "ssh-keygen":
					return nil, os.WriteFile(filepath.Join(d.settings.Root, "private/key.pub"), []byte("ssh-ed25519 fixture\n"), privateFileMode)
				case "ssh":
					return nil, nil
				case "gcloud":
					args := request.Args[3:]
					switch {
					case slices.Equal(args[:3], []string{"compute", "machine-types", "describe"}):
						if args[3] != scenario.machine {
							t.Fatalf("wrong machine lookup: %v", args)
						}
						return json.Marshal(map[string]int{"guestCpus": 4, "memoryMb": scenario.memoryMB})
					case slices.Equal(args[:3], []string{"compute", "instances", "list"}):
						return []byte("[]"), nil
					case slices.Equal(args[:3], []string{"compute", "instances", "create"}):
						if flagValue(args, "--image") != testBootImage || flagValue(args, "--machine-type") != scenario.machine || flagValue(args, "--boot-disk-size") != "100GB" || flagValue(args, "--boot-disk-type") != "pd-balanced" {
							t.Fatalf("create omitted exact campaign pins: %v", args)
						}
						if scenario.machine == c3MachineTypeName {
							if slices.Contains(args, minimumCPUPlatformFlagName) || flagValue(args, bootDiskInterfaceFlagName) != c3BootDiskInterfaceName || flagValue(args, networkInterfaceFlagName) != c3NetworkInterfaceValue || slices.Contains(args, "--no-address") {
								t.Fatalf("C3 interface or CPU arguments differ: %v", args)
							}
						} else if flagValue(args, minimumCPUPlatformFlagName) != scenario.expectedCPU || slices.Contains(args, bootDiskInterfaceFlagName) || slices.Contains(args, networkInterfaceFlagName) {
							t.Fatalf("N2 arguments changed: %v", args)
						}
						var retained journal
						if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
							t.Fatal(err)
						}
						if retained.Options.BootImage != testBootImage || retained.Options.CPUPlatform != scenario.expectedCPU || retained.Options.MachineType != scenario.machine || retained.Image != testBootImage {
							t.Fatalf("pins not journaled before create: %+v", retained)
						}
						created = append(created, args[3])
						return nil, scenario.createErr
					case slices.Equal(args[:3], []string{"compute", "instances", "describe"}):
						index := len(created) - 1
						item := d.state.Instances[index]
						item.ID, item.Status = strconv.Itoa(index+100), "RUNNING"
						item.CPUPlatform = scenario.platforms[index]
						item.MachineType = "https://www.googleapis.com/compute/v1/projects/" + d.settings.Project + "/zones/" + d.settings.Zone + "/machineTypes/" + scenario.actualMachine
						if err := json.Unmarshal([]byte(`{"disks":[{"source":"disk"}],"networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &item); err != nil {
							t.Fatal(err)
						}
						item.Disks[0].Interface = scenario.diskInterface
						item.NetworkInterfaces[0].NICType = scenario.nicType
						return json.Marshal(item)
					case slices.Equal(args[:3], []string{"compute", "disks", "describe"}):
						return json.Marshal(map[string]string{"sourceImage": scenario.diskImage})
					case slices.Equal(args[:3], []string{"compute", "instances", "get-guest-attributes"}):
						return []byte("ssh-ed25519 fixture\n"), nil
					}
				}
				t.Fatalf("unexpected command or family fallback: %+v", request)
				return nil, nil
			}
			err := d.provision(t.Context())
			if len(created) != scenario.wantVMs || (err == nil) != (scenario.wantError == "") || err != nil && !strings.Contains(err.Error(), scenario.wantError) {
				t.Fatalf("created %v, error %v", created, err)
			}
		})
	}
}

func flagValue(args []string, name string) string {
	index := slices.Index(args, name)
	if index < 0 || index+1 >= len(args) {
		return ""
	}
	return args[index+1]
}
