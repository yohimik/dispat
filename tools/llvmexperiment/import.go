package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const importedCampaignManifest = "imported-build-campaigns.json"

type campaignImportOptions struct {
	LLVM, Kernel, Results, Source, Binary          string
	LLVMRun, LLVMAttempt, KernelRun, KernelAttempt string
	ValidateOnly                                   bool
}

type importedCampaign struct {
	Workload         string            `json:"workload"`
	Run              string            `json:"run"`
	Attempt          string            `json:"attempt"`
	ManifestSHA256   string            `json:"manifestSha256"`
	Cells            map[string]string `json:"verdictSha256"`
	Evidence         map[string]string `json:"retainedEnvironmentSha256"`
	root             string
	optionalEvidence map[string]string
}

type campaignImport struct {
	Source     string             `json:"measuredDispatSourceRevision"`
	Binary     string             `json:"measuredDispatBinarySha256"`
	Version    string             `json:"measuredDispatVersion"`
	BinaryID   string             `json:"measuredDispatBinaryId"`
	Campaigns  []importedCampaign `json:"campaigns"`
	Allocation *campaignHardware  `json:"commonAllocation"`
	Disk       *importDiskProfile `json:"commonDisk"`
	NestedJobs int                `json:"innerJobsPerTask"`
	Images     map[string]string  `json:"workloadImages"`
	cells      map[string]string
}

type campaignImportManifest struct {
	Profile        string                `json:"profile"`
	Source         string                `json:"dispatSourceRevision"`
	WorkloadSource string                `json:"workloadSourceRevision"`
	Binary         string                `json:"binarySha256"`
	Workload       string                `json:"workload"`
	Cells          []campaignCell        `json:"cells"`
	Hardware       *campaignHardware     `json:"commonHardware"`
	KernelInputs   *kernelCampaignInputs `json:"kernelInputs"`
}

func importCampaigns(ctx context.Context, args []string) error {
	settings := campaignImportOptions{}
	flags := flag.NewFlagSet("llvmexperiment import-campaigns", flag.ContinueOnError)
	flags.StringVar(&settings.LLVM, "llvm", "", "selected full LLVM aggregate directory")
	flags.StringVar(&settings.Kernel, "kernel", "", "selected full Linux kernel aggregate directory")
	flags.StringVar(&settings.Results, "results", "", "standard report results directory")
	flags.StringVar(&settings.Source, "source", "", "exact measured Dispat checkout revision")
	flags.StringVar(&settings.Binary, "binary-sha256", "", "exact measured Dispat binary SHA256")
	flags.StringVar(&settings.LLVMRun, "llvm-run", "", "selected LLVM workflow run ID")
	flags.StringVar(&settings.LLVMAttempt, "llvm-attempt", "", "selected LLVM workflow attempt")
	flags.StringVar(&settings.KernelRun, "kernel-run", "", "selected kernel workflow run ID")
	flags.StringVar(&settings.KernelAttempt, "kernel-attempt", "", "selected kernel workflow attempt")
	flags.BoolVar(&settings.ValidateOnly, "validate-only", false, "validate all evidence without changing results")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || settings.LLVM == "" || settings.Kernel == "" || settings.Results == "" ||
		!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(settings.Source) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(settings.Binary) {
		return errors.New("campaign import requires both aggregate directories, results, exact source and binary SHA256")
	}
	for _, identity := range []string{settings.LLVMRun, settings.LLVMAttempt, settings.KernelRun, settings.KernelAttempt} {
		if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(identity) {
			return errors.New("campaign import requires positive exact workflow run and attempt IDs")
		}
	}
	validated, err := validateCampaignImport(ctx, settings)
	if err != nil {
		return err
	}
	if settings.ValidateOnly {
		return nil
	}
	return installCampaignImport(ctx, settings.Results, validated)
}

func validateCampaignImport(ctx context.Context, settings campaignImportOptions) (*campaignImport, error) {
	result := &campaignImport{Source: settings.Source, Binary: settings.Binary, cells: map[string]string{}}
	for _, selected := range []importedCampaign{
		{Workload: llvmWorkload, Run: settings.LLVMRun, Attempt: settings.LLVMAttempt, root: settings.LLVM},
		{Workload: kernelWorkload, Run: settings.KernelRun, Attempt: settings.KernelAttempt, root: settings.Kernel},
	} {
		if err := validateImportedCampaign(ctx, &selected, result); err != nil {
			return nil, fmt.Errorf("%s campaign: %w", selected.Workload, err)
		}
		result.Campaigns = append(result.Campaigns, selected)
	}
	return result, nil
}

func validateImportedCampaign(ctx context.Context, selected *importedCampaign, result *campaignImport) error {
	if err := inspectImportTree(ctx, selected.root); err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(selected.root, "campaign.json"))
	if err != nil {
		return err
	}
	var manifest campaignImportManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return err
	}
	descriptor := describeWorkload(selected.Workload)
	if manifest.Profile != fullProfile || manifest.Source != result.Source || manifest.Binary != result.Binary ||
		manifest.Workload != descriptor.Name || manifest.WorkloadSource != descriptor.Revision || manifest.Hardware == nil {
		return errors.New("aggregate profile, source, binary, workload or hardware differs from the selected full campaign")
	}
	expected := map[string]campaignCell{}
	for _, cell := range campaignCells() {
		expected[cell.Tool+"-"+cell.Mode] = cell
	}
	if len(manifest.Cells) != len(expected) {
		return errors.New("aggregate must contain exactly six selected cells")
	}
	for _, name := range []string{"cells", "environment"} {
		entries, err := os.ReadDir(filepath.Join(selected.root, name))
		if err != nil {
			return err
		}
		if len(entries) != len(expected) {
			return fmt.Errorf("aggregate %s must contain exactly six cell directories", name)
		}
	}
	selected.Cells = map[string]string{}
	for _, cell := range manifest.Cells {
		identity := cell.Tool + "-" + cell.Mode
		if _, exists := expected[identity]; !exists {
			return fmt.Errorf("unexpected or duplicate cell %s", identity)
		}
		delete(expected, identity)
		if cell.Reason != "" {
			return fmt.Errorf("cell %s was rejected by collection: %s", identity, cell.Reason)
		}
		parts := strings.Split(filepath.ToSlash(cell.Verdict), "/")
		if len(parts) != 3 || parts[0] != "cells" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || parts[2] != "verdict.json" ||
			filepath.Clean(cell.Verdict) != cell.Verdict || cell.Evidence != filepath.Join("environment", identity) {
			return fmt.Errorf("cell %s has an invalid retained evidence path", identity)
		}
		root := filepath.Join(selected.root, filepath.Dir(cell.Verdict))
		body, err := os.ReadFile(filepath.Join(root, "verdict.json"))
		if err != nil {
			return err
		}
		var record verdict
		if err := json.Unmarshal(body, &record); err != nil {
			return err
		}
		options := campaignOptions{Source: result.Source, Profile: fullProfile, Workload: selected.Workload}
		if err := validateCampaignCell(options, cell, record); err != nil {
			return err
		}
		build, trial := record.buildResult(), record.ExecutionMeasurement.Trial
		if trial.ID != selected.Run+"-"+selected.Attempt+"-"+identity || trial.Repetition != trialRepetitionDefault ||
			build.BinarySHA256 != result.Binary || cell.Status != build.Status || cell.TrialStatus != trial.Status || trial.Status != build.Status ||
			cell.Status != completedStatus && cell.Status != unsupportedStatus {
			return fmt.Errorf("cell %s has stale or mismatched run, binary, repetition or completion evidence", identity)
		}
		if record.Dispat == "" || record.Dispat != build.ToolVersions[dispatTool] || build.BinaryID == "" ||
			result.Version != "" && (result.Version != record.Dispat || result.BinaryID != build.BinaryID) {
			return fmt.Errorf("cell %s has missing or mixed measured Dispat version identity", identity)
		}
		result.Version, result.BinaryID = record.Dispat, build.BinaryID
		if err := verifyRetainedEvidence(root, record.ExecutionMeasurement); err != nil {
			return err
		}
		if cell.Status == completedStatus {
			if err := verifyObservedMeasurement(root, record.ExecutionMeasurement); err != nil {
				return err
			}
		}
		if cell.Tool != changesetsTool {
			environment := filepath.Join(selected.root, cell.Evidence)
			hardware, err := readCampaignHardware(environment, record)
			if err != nil {
				return err
			}
			if err := compareCampaignHardware(*manifest.Hardware, *hardware); err != nil {
				return err
			}
			allocation := *hardware
			if result.NestedJobs != 0 && result.NestedJobs != build.JobsPerExecutor {
				return fmt.Errorf("LLVM/kernel inner job allocations differ: expected %d, observed %d", result.NestedJobs, build.JobsPerExecutor)
			}
			result.NestedJobs = build.JobsPerExecutor
			allocation.NinjaJobs, allocation.MakeJobs, allocation.LinkPool = 0, 0, 0
			allocation.LinkPoolReason = ""
			if result.Allocation == nil {
				result.Allocation = &allocation
			} else if err := compareCampaignHardware(*result.Allocation, allocation); err != nil {
				return fmt.Errorf("LLVM/kernel resource allocations differ: %w", err)
			}
			if err := bindImportedCloudIdentity(environment, result, record); err != nil {
				return err
			}
			if selected.Workload == kernelWorkload {
				if manifest.KernelInputs == nil {
					return errors.New("kernel aggregate has no pinned kernel inputs")
				}
				inputs, err := readKernelCampaignInputs(environment, root, record)
				if err != nil {
					return err
				}
				if err := compareKernelCampaignInputs(*manifest.KernelInputs, *inputs); err != nil {
					return err
				}
			}
		}
		name := descriptor.Name + "-" + fullProfile + "-" + cell.Mode + "-" + cell.Tool
		selected.Cells[name] = importDigest(body)
		result.cells[name] = root
	}
	selected.ManifestSHA256 = importDigest(content)
	selected.optionalEvidence, err = snapshotImportedCleanupEvidence(selected.root)
	return err
}

func importDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
