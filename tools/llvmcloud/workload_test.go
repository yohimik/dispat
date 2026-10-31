package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
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
	for _, scenario := range []struct {
		workload string
		timeout  string
		duration time.Duration
	}{{"llvm", "4h", 4 * time.Hour}, {"linux-kernel", "5h", 5 * time.Hour}} {
		for _, executor := range []struct{ tool, mode string }{{"dispat", "compilation"}, {"dispat", "interpretation"}, {"dispat", "hybrid"}, {"nx", "manual-gcp-split"}, {"lerna", "manual-gcp-split"}} {
			t.Run(scenario.workload+"/"+executor.tool+"/"+executor.mode, func(t *testing.T) {
				workload := scenario.workload
				settings, err := parseOptions(append(hardwarePinArgs(), "--workload", workload, "--tool", executor.tool, "--mode", executor.mode))
				if err != nil {
					t.Fatal(err)
				}
				d := cleanupDriver(t)
				settings.Root = d.settings.Root
				d.settings = settings
				started := time.Now()
				var coordinatorCtx context.Context
				var args, commands []string
				d.runCommand = func(ctx context.Context, request invocation) ([]byte, error) {
					deadline, exists := ctx.Deadline()
					if !exists || deadline.Before(started.Add(scenario.duration)) || deadline.After(time.Now().Add(scenario.duration)) {
						t.Fatalf("coordinator deadline %v does not allow %v", deadline, scenario.duration)
					}
					if coordinatorCtx != nil && ctx != coordinatorCtx {
						t.Fatal("coordinator lifecycle changed its context")
					}
					coordinatorCtx = ctx
					commands = append(commands, request.Args[0])
					switch request.Args[0] {
					case "ps":
						return nil, nil
					case "create":
						index := slices.Index(request.Args, "run")
						if index < 0 {
							t.Fatalf("missing coordinator run command: %v", request.Args)
						}
						args = request.Args[index:]
						return []byte(strings.Repeat("a", 64)), nil
					case "start":
						return nil, nil
					}
					t.Fatalf("unexpected command: %+v", request)
					return nil, nil
				}
				if err := d.runCoordinator(t.Context()); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(commands, []string{"ps", "create", "start"}) || coordinatorCtx.Err() != context.Canceled {
					t.Fatalf("incomplete or unclosed coordinator lifecycle: %v, %v", commands, coordinatorCtx.Err())
				}
				if args[0] != "run" || flagValue(args, "--tool") != executor.tool || flagValue(args, "--mode") != executor.mode || flagValue(args, "--jobs") != "2" || flagValue(args, "--timeout") != scenario.timeout || flagValue(args, "--trial-id") != settings.ID {
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

func TestCloudCoordinatorPreservesEarlierDeadlineAndCancellation(t *testing.T) {
	for _, workload := range []string{"llvm", "linux-kernel"} {
		t.Run(workload, func(t *testing.T) {
			d := cleanupDriver(t)
			d.settings.Workload = workload
			deadline := time.Now().Add(time.Minute)
			ctx, cancel := context.WithDeadline(t.Context(), deadline)
			defer cancel()
			d.runCommand = func(commandCtx context.Context, request invocation) ([]byte, error) {
				if actual, exists := commandCtx.Deadline(); !exists || !actual.Equal(deadline) {
					t.Fatalf("caller deadline lost: %v", actual)
				}
				switch request.Args[0] {
				case "ps":
					return nil, nil
				case "create":
					return []byte(strings.Repeat("a", 64)), nil
				case "start":
					cancel()
					return nil, commandCtx.Err()
				}
				t.Fatalf("unexpected command: %+v", request)
				return nil, nil
			}
			if err := d.runCoordinator(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation lost: %v", err)
			}
		})
	}
}
