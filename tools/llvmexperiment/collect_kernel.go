package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
)

type kernelCampaignInputs struct {
	Image         string            `json:"image"`
	Toolchain     map[string]string `json:"toolchain"`
	Configuration string            `json:"configuration"`
	ConfigDigests map[string]string `json:"configDigests"`
	ModuleCounts  map[string]int    `json:"moduleCounts"`
	Omissions     []string          `json:"omissions"`
	Targets       []string          `json:"targets"`
	Profile       string            `json:"profile"`
}

func readKernelCampaignInputs(environment, trialRoot string, record verdict) (*kernelCampaignInputs, error) {
	var provenance struct {
		Options struct {
			Image string `json:"image"`
		} `json:"options"`
	}
	if err := readHardwareJSON(filepath.Join(environment, "cloud", "provenance.json"), &provenance); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(provenance.Options.Image) {
		return nil, fmt.Errorf(kernelImageDigestError)
	}
	build := record.buildResult()
	if !maps.Equal(build.ToolVersions, record.ExecutionMeasurement.Trial.Toolchain) {
		return nil, fmt.Errorf(kernelToolchainReportMismatchError)
	}
	if build.Status == completedStatus {
		if err := bindKernelRawEvidence(trialRoot, record); err != nil {
			return nil, err
		}
	}
	for _, tool := range []string{"make", "gcc-12", "x86_64-linux-gnu-gcc-12", "aarch64-linux-gnu-gcc-12", "x86_64-linux-gnu-ld", "aarch64-linux-gnu-ld", "pahole", "kernel-package-manifest-sha256"} {
		if build.ToolVersions[tool] == "" {
			return nil, fmt.Errorf(kernelToolchainIdentityError, tool)
		}
	}
	toolchainDigest := ""
	for _, evidence := range record.ExecutionMeasurement.Trial.Evidence {
		if evidence.ID == "kernel-toolchain" {
			toolchainDigest = evidence.SHA256
		}
	}
	if toolchainDigest == "" || toolchainDigest != build.ToolVersions["kernel-package-manifest-sha256"] {
		return nil, fmt.Errorf(kernelToolchainEvidenceError)
	}
	return &kernelCampaignInputs{Image: provenance.Options.Image, Toolchain: maps.Clone(build.ToolVersions), Configuration: build.Configuration,
		ConfigDigests: maps.Clone(build.ConfigDigests), ModuleCounts: maps.Clone(build.ModuleCounts), Omissions: slices.Clone(build.Omissions), Targets: slices.Clone(build.BuildTargets), Profile: build.Profile}, nil
}

func compareKernelCampaignInputs(expected, actual kernelCampaignInputs) error {
	for _, field := range []struct{ Name, Expected, Actual string }{{"image", expected.Image, actual.Image}, {"configuration", expected.Configuration, actual.Configuration}, {"profile", expected.Profile, actual.Profile}} {
		if field.Expected != field.Actual {
			return fmt.Errorf(kernelInputMismatchError, field.Name, field.Expected, field.Actual)
		}
	}
	if !slices.Equal(expected.Omissions, actual.Omissions) {
		return fmt.Errorf(kernelOmissionsMismatchError, expected.Omissions, actual.Omissions)
	}
	if !slices.Equal(expected.Targets, actual.Targets) {
		return fmt.Errorf(kernelTargetsMismatchError, expected.Targets, actual.Targets)
	}
	for _, field := range []struct {
		Name             string
		Expected, Actual map[string]string
	}{{"configDigests", expected.ConfigDigests, actual.ConfigDigests}, {"toolchain", expected.Toolchain, actual.Toolchain}} {
		keys := maps.Clone(field.Expected)
		for key, value := range field.Actual {
			keys[key] = value
		}
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			if field.Expected[key] != field.Actual[key] {
				return fmt.Errorf(kernelMapMismatchError, field.Name, key, field.Expected[key], field.Actual[key])
			}
		}
	}
	for _, backend := range describeWorkload(kernelWorkload).Backends {
		if expected.ModuleCounts[backend] != actual.ModuleCounts[backend] {
			return fmt.Errorf(kernelModuleCountMismatchError, backend, expected.ModuleCounts[backend], actual.ModuleCounts[backend])
		}
	}
	return nil
}

func bindKernelNestedHardware(hardware *campaignHardware, record verdict) error {
	vertices := map[string]bool{}
	for _, vertex := range record.ExecutionMeasurement.Graph.Vertices {
		if !vertex.IsVirtual {
			vertices[vertex.ID] = false
		}
	}
	for _, nested := range record.ExecutionMeasurement.Resources.Nested {
		task, _ := nested["taskId"].(string)
		seen, exists := vertices[task]
		if !exists || seen {
			return fmt.Errorf(kernelNestedTaskError, task)
		}
		jobs, err := positiveHardwareInteger(nested, "makeJobs")
		if err != nil {
			return err
		}
		reason, _ := nested["linkPoolReason"].(string)
		link, hasLink := nested["linkPool"]
		if !hasLink || link != nil || reason == "" {
			return fmt.Errorf(kernelLinkLimitError)
		}
		if hardware.MakeJobs != 0 && (hardware.MakeJobs != jobs || hardware.LinkPoolReason != reason) {
			return fmt.Errorf(kernelNestedLimitsMismatchError)
		}
		if jobs != record.buildResult().JobsPerExecutor {
			return fmt.Errorf(kernelMakeJobsMismatchError, record.buildResult().JobsPerExecutor, jobs)
		}
		hardware.MakeJobs, hardware.LinkPoolReason = jobs, reason
		vertices[task] = true
	}
	for task, seen := range vertices {
		if !seen {
			return fmt.Errorf(kernelMakeJobsMissingError, task)
		}
	}
	return nil
}

func validateKernelCampaignResult(result *buildResult) error {
	if result.KernelVersion != kernelVersion || result.LLVMVersion != "" || result.Configuration != kernelConfiguration(result.Profile) ||
		!slices.Equal(result.Backends, describeWorkload(kernelWorkload).Backends) || !slices.Equal(result.BuildTargets, workloadTargets(kernelWorkload, result.Profile)) || !slices.Equal(result.Omissions, kernelOmissions(result.Profile)) {
		return fmt.Errorf(kernelWorkloadMismatchError)
	}
	if len(result.ConfigDigests) != executorCount || len(result.ModuleCounts) != executorCount || len(result.Artifacts) != executorCount*len(workloadOutputs(kernelWorkload, result.Profile)) {
		return fmt.Errorf(kernelCoverageError)
	}
	for _, backend := range result.Backends {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(result.ConfigDigests[backend]) {
			return fmt.Errorf(kernelConfigDigestError, backend)
		}
		count, present := result.ModuleCounts[backend]
		if !present || result.Profile == fullProfile && count < 1 || result.Profile == smokeProfile && count != 0 {
			return fmt.Errorf(kernelModuleCountError, backend, result.Profile)
		}
		seen := map[string]bool{}
		for _, artifact := range result.Artifacts {
			if artifact.Package != taskName(kernelWorkload, backend) {
				continue
			}
			if seen[artifact.Path] || !slices.Contains(workloadOutputs(kernelWorkload, result.Profile), artifact.Path) {
				return fmt.Errorf(kernelArtifactIdentityError, artifact.Package, artifact.Path)
			}
			seen[artifact.Path] = true
			if artifact.Path == ".config" && artifact.SHA256 != result.ConfigDigests[backend] {
				return fmt.Errorf(kernelConfigArtifactError, backend)
			}
		}
		if len(seen) != len(workloadOutputs(kernelWorkload, result.Profile)) {
			return fmt.Errorf(kernelArtifactCoverageError, backend)
		}
	}
	return nil
}

func (hardware campaignHardware) MarshalJSON() ([]byte, error) {
	type plainHardware campaignHardware
	if hardware.MakeJobs == 0 {
		return json.Marshal(plainHardware(hardware))
	}
	return json.Marshal(struct {
		plainHardware
		LinkPool *int `json:"linkPoolPerTask"`
	}{plainHardware(hardware), nil})
}
