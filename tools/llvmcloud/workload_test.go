package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCloudWorkloadSelectionKeepsLLVMDefaultAndRejectsUnknownWorkloads(t *testing.T) {
	settings, err := parseOptions(hardwarePinArgs())
	if err != nil || settings.Workload != "llvm" {
		t.Fatalf("default workload = %q: %v", settings.Workload, err)
	}
	for _, value := range []string{"", "kernel", "Linux", "linux-kernel ", "../linux-kernel"} {
		if _, err := parseOptions(append(hardwarePinArgs(), "--workload", value)); err == nil {
			t.Fatalf("accepted workload %q", value)
		}
	}
}

func TestCloudWorkloadReachesCoordinatorAndRetainedOptions(t *testing.T) {
	for _, workload := range []string{"llvm", "linux-kernel"} {
		for _, executor := range []struct{ tool, mode string }{{"dispat", "compilation"}, {"dispat", "interpretation"}, {"dispat", "hybrid"}, {"nx", "manual-gcp-split"}, {"lerna", "manual-gcp-split"}} {
			t.Run(workload+"/"+executor.tool+"/"+executor.mode, func(t *testing.T) {
				settings, err := parseOptions(append(hardwarePinArgs(), "--workload", workload, "--tool", executor.tool, "--mode", executor.mode))
				if err != nil {
					t.Fatal(err)
				}
				d := &driver{settings: settings}
				args := d.formatCoordinatorRunArgs()
				if args[0] != "run" || flagValue(args, "--tool") != executor.tool || flagValue(args, "--mode") != executor.mode || flagValue(args, "--jobs") != "2" || flagValue(args, "--timeout") != "4h" || flagValue(args, "--trial-id") != settings.ID {
					t.Fatalf("coordinator changed workload, identity, mode or resource budget: %v", args)
				}
				if workload == "llvm" && slices.Contains(args, "--workload") || workload == "linux-kernel" && flagValue(args, "--workload") != workload {
					t.Fatalf("coordinator lost legacy LLVM entry-point compatibility or explicit kernel selection: %v", args)
				}
				encoded, err := json.Marshal(journal{Options: settings})
				if err != nil {
					t.Fatal(err)
				}
				var retained struct {
					Options struct {
						Workload string `json:"workload"`
					} `json:"options"`
				}
				if err := json.Unmarshal(encoded, &retained); err != nil || retained.Options.Workload != workload {
					t.Fatalf("requested workload absent from retained options: %s, %v", encoded, err)
				}
			})
		}
	}
}
