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
				if !slices.Contains(request.Args, "describe-from-family") {
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
		name      string
		platforms []string
		diskImage string
		createErr error
		wantError string
		wantVMs   int
	}{
		{name: "matching pair", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, wantVMs: 2},
		{name: "first worker drifts", platforms: []string{cascadeLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "second worker drifts", platforms: []string{iceLakePlatformName, cascadeLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 2},
		{name: "both workers drift together", platforms: []string{cascadeLakePlatformName, cascadeLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "newer minimum match is not exact", platforms: []string{"Intel Sapphire Rapids", iceLakePlatformName}, diskImage: testBootImage, wantError: "expected exact platform", wantVMs: 1},
		{name: "boot disk drifts", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage + "-other", wantError: "selected exact image", wantVMs: 1},
		{name: "creation fails without fallback", platforms: []string{iceLakePlatformName, iceLakePlatformName}, diskImage: testBootImage, createErr: errors.New("platform unavailable"), wantError: "platform unavailable", wantVMs: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			d.settings.BootImage, d.settings.CPUPlatform = testBootImage, iceLakePlatformName
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
						return []byte(`{"guestCpus":4,"memoryMb":16384}`), nil
					case slices.Equal(args[:3], []string{"compute", "instances", "list"}):
						return []byte("[]"), nil
					case slices.Equal(args[:3], []string{"compute", "instances", "create"}):
						if flagValue(args, "--image") != testBootImage || flagValue(args, "--min-cpu-platform") != iceLakePlatformName {
							t.Fatalf("create omitted exact campaign pins: %v", args)
						}
						var retained journal
						if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
							t.Fatal(err)
						}
						if retained.Options.BootImage != testBootImage || retained.Options.CPUPlatform != iceLakePlatformName || retained.Image != testBootImage {
							t.Fatalf("pins not journaled before create: %+v", retained)
						}
						created = append(created, args[3])
						return nil, scenario.createErr
					case slices.Equal(args[:3], []string{"compute", "instances", "describe"}):
						index := len(created) - 1
						item := d.state.Instances[index]
						item.ID, item.Status = strconv.Itoa(index+100), "RUNNING"
						item.CPUPlatform = scenario.platforms[index]
						item.MachineType = "https://www.googleapis.com/compute/v1/projects/" + d.settings.Project + "/zones/" + d.settings.Zone + "/machineTypes/" + machineType
						if err := json.Unmarshal([]byte(`{"disks":[{"source":"disk"}],"networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &item); err != nil {
							t.Fatal(err)
						}
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
