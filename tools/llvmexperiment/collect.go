package main

import (
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

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

type campaignOptions struct {
	Input   string
	Output  string
	Source  string
	Profile string
}

type campaignCell struct {
	Tool        string `json:"tool"`
	Mode        string `json:"mode"`
	Status      string `json:"status"`
	Verdict     string `json:"verdict,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Reason      string `json:"reason,omitempty"`
	TrialStatus string `json:"trialStatus,omitempty"`
}

func campaignCells() []campaignCell {
	return []campaignCell{{Tool: dispatTool, Mode: compilationMode}, {Tool: dispatTool, Mode: interpretationMode},
		{Tool: dispatTool, Mode: hybridMode}, {Tool: nxTool, Mode: manualGCPMode}, {Tool: lernaTool, Mode: manualGCPMode}, {Tool: changesetsTool, Mode: distributedMode}}
}

func collectCampaign(args []string) error {
	var settings campaignOptions
	flags := flag.NewFlagSet("llvmexperiment collect", flag.ContinueOnError)
	flags.StringVar(&settings.Input, "input", "", "six downloaded per-cell artifact directories")
	flags.StringVar(&settings.Output, "output", "", "fresh aggregate report directory on the same filesystem")
	flags.StringVar(&settings.Source, "source", "", "exact tested Dispat source commit")
	flags.StringVar(&settings.Profile, "profile", fullProfile, "full or smoke campaign")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || settings.Input == "" || settings.Output == "" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(settings.Source) || settings.Profile != fullProfile && settings.Profile != smokeProfile {
		return errors.New("LLVM collection requires input, fresh output, exact Dispat source and full or smoke profile")
	}
	return collectCampaignFiles(settings)
}

func collectCampaignFiles(settings campaignOptions) error {
	if err := os.Mkdir(settings.Output, directoryMode); err != nil {
		return fmt.Errorf("LLVM aggregate output must be fresh: %w", err)
	}
	for _, name := range []string{"cells", "environment"} {
		if err := os.Mkdir(filepath.Join(settings.Output, name), directoryMode); err != nil {
			return err
		}
	}
	cells := campaignCells()
	var failures []error
	binary := ""
	var hardware *campaignHardware
	for index := range cells {
		cell := &cells[index]
		if err := collectCampaignCell(settings, cell, &binary, &hardware); err != nil {
			cell.Status, cell.Reason = failedStatus, err.Error()
			failures = append(failures, err)
		}
	}
	manifest := struct {
		Profile      string            `json:"profile"`
		DispatSource string            `json:"dispatSourceRevision"`
		LLVMSource   string            `json:"llvmSourceRevision"`
		Binary       string            `json:"binarySha256"`
		Cells        []campaignCell    `json:"cells"`
		Hardware     *campaignHardware `json:"commonHardware,omitempty"`
	}{settings.Profile, settings.Source, llvmRevision, binary, cells, hardware}
	if err := writeExclusiveJSON(filepath.Join(settings.Output, "campaign.json"), manifest); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func collectCampaignCell(settings campaignOptions, cell *campaignCell, binary *string, hardware **campaignHardware) error {
	name := cell.Tool + "-" + cell.Mode
	input := filepath.Join(settings.Input, name)
	entries, err := os.ReadDir(input)
	if err != nil {
		return fmt.Errorf("LLVM cell %s has no downloaded evidence: %w", name, err)
	}
	environment := filepath.Join(settings.Output, "environment", name)
	if err := os.Rename(input, environment); err != nil {
		return err
	}
	cell.Evidence = filepath.Join("environment", name)
	paths := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(environment, entry.Name(), "verdict.json")); err == nil {
			paths = append(paths, entry.Name())
		}
	}
	if len(paths) != 1 {
		return fmt.Errorf("LLVM cell %s requires exactly one verdict; found %d (raw evidence retained)", name, len(paths))
	}
	path := filepath.Join(environment, paths[0], "verdict.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record verdict
	if err := json.Unmarshal(content, &record); err != nil {
		return err
	}
	if record.ExecutionMeasurement != nil {
		cell.TrialStatus = record.ExecutionMeasurement.Trial.Status
	}
	if err := validateCampaignCell(settings, *cell, record); err != nil {
		return err
	}
	if err := verifyRetainedEvidence(filepath.Dir(path), record.ExecutionMeasurement); err != nil {
		return err
	}
	if *binary != "" && *binary != record.LLVM.BinarySHA256 {
		return fmt.Errorf("LLVM cell %s used a different Dispat binary", name)
	}
	*binary = record.LLVM.BinarySHA256
	if cell.Tool != changesetsTool {
		actual, err := readCampaignHardware(environment, record)
		if err != nil {
			return fmt.Errorf("LLVM cell %s hardware evidence: %w", name, err)
		}
		if *hardware == nil {
			*hardware = actual
		} else if err := compareCampaignHardware(**hardware, *actual); err != nil {
			return fmt.Errorf("LLVM cell %s hardware differs from the campaign: %w", name, err)
		}
	}
	cell.Verdict = filepath.Join("cells", paths[0], "verdict.json")
	if err := os.Rename(filepath.Dir(path), filepath.Dir(filepath.Join(settings.Output, cell.Verdict))); err != nil {
		return err
	}
	cell.Status = record.LLVM.Status
	if cell.Status != completedStatus && cell.Status != unsupportedStatus {
		return fmt.Errorf("LLVM cell %s finished with status %s: %s", name, cell.Status, record.LLVM.Reason)
	}
	return nil
}

func validateCampaignCell(settings campaignOptions, cell campaignCell, record verdict) error {
	if record.Experiment != experimentName || record.Tool != cell.Tool || record.LLVM == nil || record.ExecutionMeasurement == nil {
		return errors.New("LLVM campaign cell has missing or mismatched identity")
	}
	build, trial := record.LLVM, record.ExecutionMeasurement.Trial
	if build.Mode != cell.Mode || build.Profile != settings.Profile || build.SourceRevision != llvmRevision ||
		trial.Mode != cell.Mode || trial.Tool != cell.Tool || trial.Profile != settings.Profile || trial.SourceRevision != llvmRevision ||
		build.ToolVersions["dispat-source-revision"] != settings.Source || trial.Toolchain["dispat-source-revision"] != settings.Source || build.BinarySHA256 != trial.BinarySHA256 {
		return fmt.Errorf("LLVM cell %s/%s source, binary, profile or mode differs from the selected campaign", cell.Tool, cell.Mode)
	}
	body, err := json.Marshal(record.ExecutionMeasurement)
	if err != nil {
		return err
	}
	if err := measurement.Validate(body); err != nil {
		return err
	}
	if cell.Tool == changesetsTool && (build.Status != unsupportedStatus || build.Seconds != nil || record.Passed) {
		return errors.New("Changesets unsupported record claims execution")
	}
	if cell.Tool != changesetsTool && build.Status == unsupportedStatus {
		return errors.New("a runnable LLVM cell was silently replaced with an unsupported record")
	}
	if build.Status == completedStatus {
		if !record.Passed || build.Seconds == nil || build.Topology != remoteTopology || len(record.ExecutionMeasurement.Resources.Nodes) != executorCount {
			return errors.New("completed cloud cell lacks verified real-VM topology or timing")
		}
		hosts := map[string]bool{}
		for _, node := range record.ExecutionMeasurement.Resources.Nodes {
			host, _ := node["hostId"].(string)
			if !regexp.MustCompile(`^gce:[0-9]+$`).MatchString(host) || hosts[host] {
				return errors.New("completed LLVM cloud cell lacks two distinct physical VM identities")
			}
			hosts[host] = true
		}
	} else if record.Passed || build.Seconds != nil {
		return errors.New("incomplete cloud cell claims successful execution timing")
	}
	return nil
}

func verifyRetainedEvidence(root string, record *measurement.Measurement) error {
	for _, entry := range record.Trial.Evidence {
		if entry.Path == "" || filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || entry.Path == ".." || strings.HasPrefix(entry.Path, "../") {
			return fmt.Errorf("LLVM evidence %s is not a retained relative path", entry.ID)
		}
		path := filepath.Join(root, entry.Path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("LLVM evidence %s is not a regular file", entry.ID)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return fmt.Errorf("LLVM retained evidence digest differs for %s", entry.ID)
		}
	}
	return nil
}
