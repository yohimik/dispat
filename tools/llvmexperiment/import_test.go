package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImportFixture(t *testing.T, workload, run string) (importedCampaign, *campaignImport) {
	t.Helper()
	settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "aggregate"), Source: strings.Repeat("c", 40), Profile: fullProfile, Workload: workload}
	result := &campaignImport{Source: settings.Source, Binary: strings.Repeat("a", 64), cells: map[string]string{}}
	for _, cell := range campaignCells() {
		trialID := run + "-1-" + cell.Tool + "-" + cell.Mode
		var root string
		if workload == kernelWorkload {
			root = writeKernelImportFixture(t, settings, cell, trialID)
		} else {
			root = writeCampaignFixture(t, settings, cell)
		}
		var record verdict
		mustReadImportJSON(t, filepath.Join(root, "verdict.json"), &record)
		record.ExecutionMeasurement.Trial.ID = trialID
		record.ExecutionMeasurement.Trial.BinarySHA256 = result.Binary
		record.buildResult().BinarySHA256, record.buildResult().BinaryID = result.Binary, "candidate."+settings.Source
		record.Dispat = "dispat 1.11.0-rc.13-candidate." + settings.Source
		record.buildResult().ToolVersions[dispatTool], record.ExecutionMeasurement.Trial.Toolchain[dispatTool] = record.Dispat, record.Dispat
		if workload == llvmWorkload && cell.Tool != changesetsTool {
			record.LLVM.Nodes = []string{"synthetic-node-a", "synthetic-node-b"}
			for _, backend := range []string{"llvm-x86", "llvm-aarch64"} {
				for _, name := range outputTools(fullProfile) {
					record.LLVM.Artifacts = append(record.LLVM.Artifacts, artifact{Package: backend, Path: name, SHA256: strings.Repeat("d", 64), Bytes: 123})
				}
			}
		}
		for index := range record.ExecutionMeasurement.Trial.Evidence {
			entry := &record.ExecutionMeasurement.Trial.Evidence[index]
			if filepath.Base(entry.Path) != taskFinishedFile {
				continue
			}
			var manifest taskManifest
			mustReadImportJSON(t, filepath.Join(root, entry.Path), &manifest)
			manifest.Task.Run = trialID
			mustWriteImportJSON(t, filepath.Join(root, entry.Path), manifest)
			body, err := os.ReadFile(filepath.Join(root, entry.Path))
			if err != nil {
				t.Fatal(err)
			}
			entry.SHA256 = importDigest(body)
		}
		mustWriteImportJSON(t, filepath.Join(root, "verdict.json"), record)
		if cell.Tool == changesetsTool {
			continue
		}
		var provenance map[string]any
		cloud := filepath.Join(filepath.Dir(root), "cloud")
		mustReadImportJSON(t, filepath.Join(cloud, "provenance.json"), &provenance)
		options, _ := provenance["options"].(map[string]any)
		if options == nil {
			options = map[string]any{}
		}
		options["source"], options["binarySha256"] = settings.Source, result.Binary
		if options["image"] == nil {
			options["image"] = "sha256:" + strings.Repeat("a", 64)
		}
		options["id"], options["tool"], options["mode"], options["workload"], options["profile"] = trialID, cell.Tool, cell.Mode, workload, fullProfile
		provenance["options"] = options
		mustWriteImportJSON(t, filepath.Join(cloud, "provenance.json"), provenance)
		for _, node := range []string{"fixture-vm-1", "fixture-vm-2"} {
			var disk map[string]any
			mustReadImportJSON(t, filepath.Join(cloud, node+"-disk.json"), &disk)
			disk["sizeGb"], disk["type"] = "100", "https://example.test/zones/zone/diskTypes/pd-balanced"
			mustWriteImportJSON(t, filepath.Join(cloud, node+"-disk.json"), disk)
		}
	}
	if err := collectCampaignFiles(settings); err != nil {
		t.Fatal(err)
	}
	return importedCampaign{Workload: workload, Run: run, Attempt: "1", root: settings.Output}, result
}

func mustReadImportJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := readHardwareJSON(path, value); err != nil {
		t.Fatal(err)
	}
}

func mustWriteImportJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeJSON(path, value); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignImportRejectsStaleMixedAndCorruptEvidence(t *testing.T) {
	for _, scenario := range []string{"profile", "source", "binary", "missing", "duplicate", "rejected", "path", "trial", "version", "raw", "symlink", "disk", "driver"} {
		t.Run(scenario, func(t *testing.T) {
			selected, imported := writeImportFixture(t, llvmWorkload, "101")
			manifestPath := filepath.Join(selected.root, "campaign.json")
			var manifest campaignImportManifest
			mustReadImportJSON(t, manifestPath, &manifest)
			cell := manifest.Cells[0]
			verdictPath := filepath.Join(selected.root, cell.Verdict)
			var record verdict
			mustReadImportJSON(t, verdictPath, &record)
			switch scenario {
			case "profile":
				manifest.Profile = smokeProfile
			case "source":
				imported.Source = strings.Repeat("e", 40)
			case "binary":
				imported.Binary = strings.Repeat("e", 64)
			case "missing":
				manifest.Cells = manifest.Cells[1:]
			case "duplicate":
				manifest.Cells[1] = manifest.Cells[0]
			case "rejected":
				manifest.Cells[0].Reason = "raw evidence failed validation"
			case "path":
				manifest.Cells[0].Verdict = "cells/../../verdict.json"
			case "trial":
				record.ExecutionMeasurement.Trial.ID = "100-1-dispat-compilation"
			case "version":
				record.Dispat = "dispat relabeled-published-version"
			case "raw", "symlink":
				path := filepath.Join(filepath.Dir(verdictPath), record.ExecutionMeasurement.Trial.Evidence[0].Path)
				if scenario == "raw" {
					if err := os.WriteFile(path, []byte("corrupt"), fileMode); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(verdictPath, path); err != nil {
						t.Fatal(err)
					}
				}
			case "disk":
				path := filepath.Join(selected.root, cell.Evidence, "cloud", "fixture-vm-2-disk.json")
				var disk map[string]any
				mustReadImportJSON(t, path, &disk)
				disk["sizeGb"] = "200"
				mustWriteImportJSON(t, path, disk)
			case "driver":
				path := filepath.Join(selected.root, cell.Evidence, "cloud", "provenance.json")
				var driver map[string]any
				mustReadImportJSON(t, path, &driver)
				driver["options"].(map[string]any)["source"] = "another-source"
				mustWriteImportJSON(t, path, driver)
			}
			mustWriteImportJSON(t, manifestPath, manifest)
			mustWriteImportJSON(t, verdictPath, record)
			if err := validateImportedCampaign(t.Context(), &selected, imported); err == nil {
				t.Fatalf("accepted %s evidence", scenario)
			}
		})
	}
}

func TestCampaignImportPreservesCandidateBytesAndReplacesOnlyBuildCells(t *testing.T) {
	llvm, imported := writeImportFixture(t, llvmWorkload, "101")
	kernel, _ := writeImportFixture(t, kernelWorkload, "202")
	settings := campaignImportOptions{LLVM: llvm.root, Kernel: kernel.root, Results: filepath.Join(t.TempDir(), "results"), Source: imported.Source, Binary: imported.Binary,
		LLVMRun: "101", LLVMAttempt: "1", KernelRun: "202", KernelAttempt: "1"}
	validated, err := validateCampaignImport(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.cells) != 12 {
		t.Fatalf("expected twelve validated full cells, got %d", len(validated.cells))
	}
	standard := filepath.Join(settings.Results, "timing-six-dispat")
	legacy := filepath.Join(settings.Results, "llvm-full-distributed-nx")
	for _, path := range []string{standard, legacy} {
		if err := os.MkdirAll(path, directoryMode); err != nil {
			t.Fatal(err)
		}
	}
	standardBody := []byte(`{"experiment":"timing","dispat":"published"}`)
	if err := os.WriteFile(filepath.Join(standard, "verdict.json"), standardBody, fileMode); err != nil {
		t.Fatal(err)
	}
	mustWriteImportJSON(t, filepath.Join(legacy, "verdict.json"), verdict{Experiment: llvmWorkload})
	if err := installCampaignImport(t.Context(), settings.Results, validated); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("stale native-cloud row remained")
	}
	actual, err := os.ReadFile(filepath.Join(standard, "verdict.json"))
	if err != nil || string(actual) != string(standardBody) {
		t.Fatal("standard released record changed")
	}
	for name, root := range validated.cells {
		before, err := os.ReadFile(filepath.Join(root, "verdict.json"))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(settings.Results, name, "verdict.json"))
		if err != nil || string(before) != string(after) {
			t.Fatalf("candidate verdict %s changed during import", name)
		}
	}
	if !strings.Contains(validated.Version, "candidate.") || validated.BinaryID != "candidate."+settings.Source {
		t.Fatal("candidate identity was relabeled")
	}
	if err := installCampaignImport(t.Context(), settings.Results, validated); err != nil {
		t.Fatal("same explicit selection could not replace prior import:", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := validateCampaignImport(cancelled, settings); err == nil {
		t.Fatal("cancelled import continued")
	}
}

func TestCampaignImportMissingSelectionAndValidateOnlyHaveNoResultsEffects(t *testing.T) {
	if err := importCampaigns(t.Context(), nil); err == nil {
		t.Fatal("implicit campaign selection was accepted")
	}
	llvm, imported := writeImportFixture(t, llvmWorkload, "101")
	kernel, _ := writeImportFixture(t, kernelWorkload, "202")
	results := filepath.Join(t.TempDir(), "absent")
	args := []string{"--llvm", llvm.root, "--kernel", kernel.root, "--results", results, "--source", imported.Source, "--binary-sha256", imported.Binary,
		"--llvm-run", "101", "--llvm-attempt", "1", "--kernel-run", "202", "--kernel-attempt", "1", "--validate-only"}
	if err := importCampaigns(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(results); !os.IsNotExist(err) {
		t.Fatal("validate-only changed results")
	}
	var manifest campaignImportManifest
	path := filepath.Join(kernel.root, "campaign.json")
	mustReadImportJSON(t, path, &manifest)
	manifest.Hardware.CPUPlatform = "different CPU"
	mustWriteImportJSON(t, path, manifest)
	if err := importCampaigns(t.Context(), args[:len(args)-1]); err == nil {
		t.Fatal("invalid second campaign was imported")
	}
	if _, err := os.Stat(results); !os.IsNotExist(err) {
		t.Fatal("invalid campaign changed results")
	}
}

func TestCampaignImportInstallRefusesChangedValidatedBytesAndForeignDestinations(t *testing.T) {
	for _, scenario := range []string{"verdict changed", "raw changed", "foreign destination", "unmanaged build row", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			selected, imported := writeImportFixture(t, llvmWorkload, "101")
			if err := validateImportedCampaign(t.Context(), &selected, imported); err != nil {
				t.Fatal(err)
			}
			imported.Campaigns = []importedCampaign{selected}
			results := t.TempDir()
			name := "llvm-full-compilation-dispat"
			source := imported.cells[name]
			switch scenario {
			case "verdict changed":
				if err := os.WriteFile(filepath.Join(source, "verdict.json"), []byte("{}"), fileMode); err != nil {
					t.Fatal(err)
				}
			case "raw changed":
				if err := os.WriteFile(filepath.Join(source, "fixture.json"), []byte("changed"), fileMode); err != nil {
					t.Fatal(err)
				}
			case "foreign destination":
				if err := os.Mkdir(filepath.Join(results, name), directoryMode); err != nil {
					t.Fatal(err)
				}
				mustWriteImportJSON(t, filepath.Join(results, name, "verdict.json"), verdict{Experiment: "timing"})
			case "unmanaged build row":
				path := filepath.Join(results, "older-trial")
				if err := os.Mkdir(path, directoryMode); err != nil {
					t.Fatal(err)
				}
				mustWriteImportJSON(t, filepath.Join(path, "verdict.json"), verdict{Experiment: llvmWorkload})
			case "symlink":
				if err := os.Symlink(source, filepath.Join(results, name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := installCampaignImport(t.Context(), results, imported); err == nil {
				t.Fatal("unsafe replacement succeeded")
			}
			if _, err := os.Stat(filepath.Join(results, importedCampaignManifest)); !os.IsNotExist(err) {
				t.Fatal("failed import published a manifest")
			}
		})
	}
}

type importCancellationContext struct {
	context.Context
	beforeCheck func()
}

func (ctx importCancellationContext) Err() error {
	ctx.beforeCheck()
	return ctx.Context.Err()
}

func TestCampaignImportRollbackPreservesPriorRecordsEvenWhenRestoreFails(t *testing.T) {
	for _, blockRestore := range []bool{false, true} {
		t.Run(map[bool]string{false: "restored", true: "backup retained"}[blockRestore], func(t *testing.T) {
			selected, imported := writeImportFixture(t, llvmWorkload, "101")
			if err := validateImportedCampaign(t.Context(), &selected, imported); err != nil {
				t.Fatal(err)
			}
			imported.Campaigns = []importedCampaign{selected}
			results := t.TempDir()
			legacy := filepath.Join(results, "llvm-full-distributed-nx")
			if err := os.Mkdir(legacy, directoryMode); err != nil {
				t.Fatal(err)
			}
			mustWriteImportJSON(t, filepath.Join(legacy, "verdict.json"), verdict{Experiment: llvmWorkload, Dispat: "prior-candidate"})
			original, err := os.ReadFile(filepath.Join(legacy, "verdict.json"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			triggered := false
			interrupted := importCancellationContext{Context: ctx, beforeCheck: func() {
				if triggered {
					return
				}
				if _, err := os.Stat(filepath.Join(results, importedEvidenceDirectory)); err != nil {
					return
				}
				triggered = true
				if blockRestore {
					if err := os.Mkdir(legacy, directoryMode); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(legacy, "foreign"), []byte("concurrent destination"), fileMode); err != nil {
						t.Fatal(err)
					}
				}
				cancel()
			}}
			err = installCampaignImport(interrupted, results, imported)
			if err == nil || !triggered {
				t.Fatalf("post-mutation cancellation did not fail import: %v", err)
			}
			stages, scanErr := filepath.Glob(filepath.Join(results, ".build-campaign-import-*"))
			if scanErr != nil {
				t.Fatal(scanErr)
			}
			restored := filepath.Join(legacy, "verdict.json")
			if blockRestore {
				if len(stages) != 1 || !strings.Contains(err.Error(), "prior campaign data remains") {
					t.Fatalf("recovery backup not retained: %v, %v", stages, err)
				}
				restored = filepath.Join(stages[0], "old", filepath.Base(legacy), "verdict.json")
			} else if len(stages) != 0 {
				t.Fatal("successful rollback retained staging")
			}
			actual, readErr := os.ReadFile(restored)
			if readErr != nil || string(actual) != string(original) {
				t.Fatal("rollback lost prior campaign bytes", readErr)
			}
		})
	}
}

func TestCampaignImportStagesInsideWritableResultsWithUnwritableParent(t *testing.T) {
	selected, imported := writeImportFixture(t, llvmWorkload, "101")
	if err := validateImportedCampaign(t.Context(), &selected, imported); err != nil {
		t.Fatal(err)
	}
	imported.Campaigns = []importedCampaign{selected}
	parent := t.TempDir()
	results := filepath.Join(parent, "results")
	if err := os.Mkdir(results, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, directoryMode) })
	if err := installCampaignImport(t.Context(), results, imported); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignImportMatchesInnerJobsAndHardwareAcrossWorkloads(t *testing.T) {
	llvm, _ := writeImportFixture(t, llvmWorkload, "101")
	kernel, base := writeImportFixture(t, kernelWorkload, "202")
	for _, scenario := range []string{"jobs", "cpu", "disk"} {
		t.Run(scenario, func(t *testing.T) {
			imported := &campaignImport{Source: base.Source, Binary: base.Binary, cells: map[string]string{}}
			if err := validateImportedCampaign(t.Context(), &llvm, imported); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "jobs":
				imported.NestedJobs++
			case "cpu":
				imported.Allocation.CPUPlatform = "different actual first campaign CPU"
			case "disk":
				imported.Disk.SizeGB = "200"
			}
			if err := validateImportedCampaign(t.Context(), &kernel, imported); err == nil {
				t.Fatal("mixed workload allocation was accepted")
			}
		})
	}
}
