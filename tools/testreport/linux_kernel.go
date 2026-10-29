package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	kernelExperimentName     = "linux-kernel"
	kernelWorkloadName       = "Linux kernel"
	kernelFullConfiguration  = "allmodconfig"
	kernelSmokeConfiguration = "defconfig"
	kernelX86Backend         = "x86_64"
	kernelARMBackend         = "arm64"
	kernelPackagePrefix      = "kernel-"
	kernelConfigArtifact     = ".config"
)

// LinuxKernelBuild retains the kernel configuration and verified artifacts separately from LLVM.
type LinuxKernelBuild struct {
	DistributedBuild
	KernelVersion string            `json:"kernelVersion"`
	Configuration string            `json:"configuration"`
	Omissions     []string          `json:"omissions"`
	ConfigDigests map[string]string `json:"configDigests"`
	ModuleCounts  map[string]int64  `json:"moduleCounts"`
}

func (result *LinuxKernelBuild) UnmarshalJSON(content []byte) error {
	type kernelRecord LinuxKernelBuild
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return err
	}
	for name := range fields {
		if strings.EqualFold(name, "llvmVersion") {
			return fmt.Errorf("Linux kernel result must not contain llvmVersion")
		}
	}
	return json.Unmarshal(content, (*kernelRecord)(result))
}

func validateLinuxKernel(result *LinuxKernelBuild) error {
	if result == nil {
		return nil
	}
	packages, err := validateDistributedBuild(&result.DistributedBuild, kernelWorkloadName)
	if err != nil {
		return err
	}
	if result.KernelVersion == "" || result.Omissions == nil {
		return fmt.Errorf("Linux kernel version or explicit omissions are missing or invalid")
	}
	for _, omission := range result.Omissions {
		if strings.TrimSpace(omission) == "" {
			return fmt.Errorf("Linux kernel omission must name excluded work")
		}
	}
	if !slices.Equal(result.Backends, []string{kernelX86Backend, kernelARMBackend}) {
		return fmt.Errorf("Linux kernel backends must be exactly x86_64 and arm64")
	}
	for backend, digest := range result.ConfigDigests {
		if !isKernelBackend(backend) || !buildDigestPattern.MatchString(digest) {
			return fmt.Errorf("Linux kernel backend %s has an invalid configuration digest or identity", backend)
		}
	}
	for backend, modules := range result.ModuleCounts {
		if !isKernelBackend(backend) || modules < 0 {
			return fmt.Errorf("Linux kernel backend %s has an invalid module count or identity", backend)
		}
	}
	expectedTargets := []string{"vmlinux", "architecture-boot-image"}
	expectedArtifacts := []string{"vmlinux.stripped", "boot-image", kernelConfigArtifact}
	expectedConfiguration := kernelSmokeConfiguration
	if result.Profile == buildFullProfile {
		expectedConfiguration = kernelFullConfiguration
		expectedTargets = []string{"vmlinux", "modules", "architecture-boot-image"}
		expectedArtifacts = append(expectedArtifacts, "Module.symvers", "modules.order", "modules-manifest.json", "sample-module.ko.stripped")
	} else if len(result.Omissions) == 0 {
		return fmt.Errorf("Linux kernel smoke profile must disclose its omitted work")
	}
	if result.Configuration != expectedConfiguration || !slices.Equal(result.BuildTargets, expectedTargets) {
		return fmt.Errorf("Linux kernel %s profile has incorrect configuration or build targets", result.Profile)
	}
	if result.Status != buildCompletedStatus {
		return nil
	}
	if len(result.ConfigDigests) != buildExecutorCount || len(result.ModuleCounts) != buildExecutorCount || len(packages) != buildExecutorCount || len(result.Artifacts) != len(expectedArtifacts)*buildExecutorCount {
		return fmt.Errorf("completed Linux kernel build lacks both configurations, module counts or verified artifacts")
	}
	for _, backend := range result.Backends {
		digest, hasDigest := result.ConfigDigests[backend]
		modules, hasModules := result.ModuleCounts[backend]
		if !hasDigest || !buildDigestPattern.MatchString(digest) || !hasModules ||
			result.Profile == buildFullProfile && modules <= 0 || result.Profile == buildSmokeProfile && modules != 0 {
			return fmt.Errorf("Linux kernel backend %s has invalid configuration digest or module count", backend)
		}
		artifacts := packages[kernelPackagePrefix+backend]
		for _, name := range expectedArtifacts {
			if _, exists := artifacts[name]; !exists {
				return fmt.Errorf("Linux kernel backend %s did not return %s", backend, name)
			}
		}
		if artifacts[kernelConfigArtifact].SHA256 != digest {
			return fmt.Errorf("Linux kernel backend %s configuration digest differs from its returned .config", backend)
		}
	}
	return nil
}

func isKernelBackend(backend string) bool {
	return backend == kernelX86Backend || backend == kernelARMBackend
}

func writeLinuxKernelMarkdown(out *bufio.Writer, campaign Experiments) {
	printed := false
	for _, cell := range campaign.Cells {
		if cell.LinuxKernel == nil {
			continue
		}
		if !printed {
			fmt.Fprintln(out, "\n| Linux kernel distributed build | version | profile / configuration | mode | result | elapsed | executors × make jobs | targets | omissions | source | campaign Dispat binary |")
			fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|---|---|---|")
			printed = true
		}
		result := cell.LinuxKernel
		fmt.Fprintf(out, "| %s | %s | %s / %s | %s | %s | %s | %d × %d | %s | %s | %s | %s |\n",
			escapePipes(cell.Tool), result.KernelVersion, escapePipes(result.Profile), escapePipes(result.Configuration),
			escapePipes(result.Mode), escapePipes(buildOutcome(&result.DistributedBuild)), buildSeconds(&result.DistributedBuild),
			result.ExecutorCount, result.JobsPerExecutor, escapePipes(strings.Join(result.BuildTargets, ", ")),
			escapePipes(strings.Join(result.Omissions, "; ")), result.SourceRevision, escapePipes(result.BinaryID))
	}
}
