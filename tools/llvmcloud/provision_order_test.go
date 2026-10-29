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

func TestProvisionRecordsBothAllocationsBeforeGuestReadiness(t *testing.T) {
	stockout := errors.New("second instance capacity unavailable")
	for _, scenario := range []struct {
		name            string
		secondCreateErr error
		secondPlatform  string
		cancelReadiness bool
		wantError       string
		wantReadyCalls  int
		wantIDs         []string
	}{
		{name: "matching pair", secondPlatform: sapphirePlatformName, wantReadyCalls: 2, wantIDs: []string{"100", "101"}},
		{name: "second allocation fails", secondCreateErr: stockout, wantError: stockout.Error(), wantIDs: []string{"100", ""}},
		{name: "second platform mismatches", secondPlatform: iceLakePlatformName, wantError: "expected exact platform", wantIDs: []string{"100", ""}},
		{name: "readiness fails after allocation", secondPlatform: sapphirePlatformName, cancelReadiness: true, wantError: context.Canceled.Error(), wantReadyCalls: 1, wantIDs: []string{"100", "101"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			d.settings.BootImage, d.settings.CPUPlatform, d.settings.MachineType = testBootImage, sapphirePlatformName, c3MachineTypeName
			d.state.Options = d.settings
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			created := []string{}
			readyCalls := 0
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
						created = append(created, args[3])
						var saved journal
						if err := readJSON(filepath.Join(d.settings.Root, stateFile), &saved); err != nil {
							t.Fatal(err)
						}
						if len(saved.Instances) != len(created) || saved.Instances[len(created)-1].Name != args[3] || saved.Instances[len(created)-1].ID != "" {
							t.Fatalf("allocation intent not durably retained before create: %+v", saved.Instances)
						}
						if len(created) == workerCount {
							if saved.Instances[0].ID != "100" {
								t.Fatalf("first exact identity missing before second create: %+v", saved.Instances)
							}
							return nil, scenario.secondCreateErr
						}
						return nil, nil
					case slices.Equal(args[:3], []string{"compute", "instances", "describe"}):
						index := slices.Index(created, args[3])
						item := d.state.Instances[index]
						item.ID, item.Status, item.CPUPlatform = strconv.Itoa(index+100), "RUNNING", sapphirePlatformName
						if index > 0 {
							item.CPUPlatform = scenario.secondPlatform
						}
						item.MachineType = "https://www.googleapis.com/compute/v1/projects/" + d.settings.Project + "/zones/" + d.settings.Zone + "/machineTypes/" + c3MachineTypeName
						if err := json.Unmarshal([]byte(`{"disks":[{"source":"disk","interface":"NVME"}],"networkInterfaces":[{"nicType":"GVNIC","accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &item); err != nil {
							t.Fatal(err)
						}
						return json.Marshal(item)
					case slices.Equal(args[:3], []string{"compute", "disks", "describe"}):
						return json.Marshal(map[string]string{"sourceImage": testBootImage})
					case slices.Equal(args[:3], []string{"compute", "instances", "get-guest-attributes"}):
						readyCalls++
						var saved journal
						if err := readJSON(filepath.Join(d.settings.Root, stateFile), &saved); err != nil {
							t.Fatal(err)
						}
						if len(saved.Instances) != workerCount || saved.Instances[0].ID != "100" || saved.Instances[1].ID != "101" {
							t.Errorf("guest readiness preceded both exact journaled allocations: %+v", saved.Instances)
						}
						for _, item := range saved.Instances {
							for _, suffix := range []string{".json", "-disk.json"} {
								if _, err := os.Stat(filepath.Join(d.settings.Root, "results/cloud", item.Name+suffix)); err != nil {
									t.Errorf("guest readiness preceded retained worker evidence: %v", err)
								}
							}
						}
						if scenario.cancelReadiness {
							cancel()
							return nil, ctx.Err()
						}
						return []byte("ssh-ed25519 fixture\n"), nil
					}
				}
				t.Fatalf("unexpected provisioning command: %+v", request)
				return nil, nil
			}
			err := d.provision(ctx)
			if (err == nil) != (scenario.wantError == "") || err != nil && !strings.Contains(err.Error(), scenario.wantError) {
				t.Fatalf("provision returned %v, expected %q", err, scenario.wantError)
			}
			if len(created) != workerCount || readyCalls != scenario.wantReadyCalls {
				t.Errorf("created %v, readiness calls %d; want %d allocations and %d readiness calls", created, readyCalls, workerCount, scenario.wantReadyCalls)
			}
			var retained journal
			if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, item := range retained.Instances {
				ids = append(ids, item.ID)
				if item.Labels["trial"] != d.settings.ID || item.Labels["dispat-experiment"] != "llvm" {
					t.Errorf("cleanup ownership changed: %+v", item)
				}
			}
			if !slices.Equal(ids, scenario.wantIDs) {
				t.Errorf("retained cleanup identities %v, expected %v", ids, scenario.wantIDs)
			}
		})
	}
}
