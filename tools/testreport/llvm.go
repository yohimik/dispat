package main

import (
	"bufio"
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	llvmCompletedStatus   = "completed"
	llvmFailedStatus      = "failed"
	llvmBlockedStatus     = "blocked"
	llvmUnsupportedStatus = "unsupported"
	llvmExecutorCount     = 2
	llvmNotMeasuredText   = "not measured"
)

var llvmRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var llvmDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LLVMBuild records the real build workload and its availability separately from a measured duration.
type LLVMBuild struct {
	Status          string            `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	Mode            string            `json:"mode"`
	Profile         string            `json:"profile"`
	LLVMVersion     string            `json:"llvmVersion"`
	SourceRevision  string            `json:"sourceRevision"`
	Topology        string            `json:"topology"`
	ExecutorCount   int               `json:"executorCount"`
	JobsPerExecutor int               `json:"jobsPerExecutor"`
	TimeoutSeconds  *float64          `json:"timeoutSeconds,omitempty"`
	BuildTargets    []string          `json:"buildTargets"`
	Backends        []string          `json:"backends"`
	Cache           string            `json:"cache"`
	Seconds         *float64          `json:"seconds,omitempty"`
	Nodes           []string          `json:"nodes"`
	Artifacts       []LLVMArtifact    `json:"artifacts"`
	Platform        string            `json:"platform,omitempty"`
	ToolVersions    map[string]string `json:"toolVersions,omitempty"`
	BinaryID        string            `json:"binaryId,omitempty"`
	BinarySHA256    string            `json:"binarySha256,omitempty"`
}

// LLVMArtifact identifies one returned executable verified by the coordinator.
type LLVMArtifact struct {
	Package string `json:"package"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

func validateLLVM(result *LLVMBuild) error {
	if result == nil {
		return nil
	}
	if !llvmRevisionPattern.MatchString(result.SourceRevision) || result.LLVMVersion == "" || result.Topology == "" || result.Cache == "" {
		return fmt.Errorf("LLVM build source, topology or cache policy is missing or invalid")
	}
	if result.BinarySHA256 != "" && !llvmDigestPattern.MatchString(result.BinarySHA256) {
		return fmt.Errorf("LLVM experiment Dispat binary SHA-256 is invalid")
	}
	if result.TimeoutSeconds != nil && (*result.TimeoutSeconds <= 0 || math.IsNaN(*result.TimeoutSeconds) || math.IsInf(*result.TimeoutSeconds, 0)) {
		return fmt.Errorf("LLVM experiment timeout must be a finite positive number when recorded")
	}
	if result.ExecutorCount != llvmExecutorCount || result.JobsPerExecutor < 1 || len(result.BuildTargets) == 0 || len(result.Backends) != llvmExecutorCount {
		return fmt.Errorf("LLVM build workload or executor budget is invalid")
	}
	if result.Profile != "full" && result.Profile != "smoke" {
		return fmt.Errorf("LLVM profile %q is invalid", result.Profile)
	}
	expectedTools := []string{"llvm-cxxfilt", "llvm-size"}
	if result.Profile == "full" {
		expectedTools = append(expectedTools, "llc", "opt", "llvm-config")
		if len(result.BuildTargets) != 1 || result.BuildTargets[0] != "all" {
			return fmt.Errorf("full LLVM build must name the all target")
		}
	}
	if result.Mode != "compilation" && result.Mode != "interpretation" && result.Mode != "hybrid" && result.Mode != "distributed" && result.Mode != "manual-gcp-split" {
		return fmt.Errorf("LLVM distribution mode %q is invalid", result.Mode)
	}
	switch result.Status {
	case llvmCompletedStatus:
		if result.Seconds == nil || *result.Seconds <= 0 || math.IsNaN(*result.Seconds) || math.IsInf(*result.Seconds, 0) {
			return fmt.Errorf("completed LLVM build has no finite positive duration")
		}
		if len(result.Nodes) != llvmExecutorCount || result.Nodes[0] == "" || result.Nodes[0] == result.Nodes[1] || result.Nodes[1] == "" || len(result.Artifacts) != len(expectedTools)*llvmExecutorCount {
			return fmt.Errorf("completed LLVM build lacks two distinct executors or verified artifacts")
		}
	case llvmFailedStatus, llvmBlockedStatus, llvmUnsupportedStatus:
		if result.Reason == "" || result.Seconds != nil {
			return fmt.Errorf("unmeasured LLVM build must explain its status and omit the duration")
		}
	default:
		return fmt.Errorf("LLVM build status %q is invalid", result.Status)
	}
	packages := map[string]map[string]bool{}
	for _, item := range result.Artifacts {
		if item.Package == "" || item.Path == "" || item.Bytes <= 0 || !llvmDigestPattern.MatchString(item.SHA256) {
			return fmt.Errorf("LLVM artifact %q has invalid identity, size or SHA-256", item.Path)
		}
		if packages[item.Package] == nil {
			packages[item.Package] = map[string]bool{}
		}
		if packages[item.Package][item.Path] {
			return fmt.Errorf("LLVM artifact %s/%s is duplicated", item.Package, item.Path)
		}
		packages[item.Package][item.Path] = true
	}
	if result.Status == llvmCompletedStatus {
		if len(packages) != llvmExecutorCount {
			return fmt.Errorf("completed LLVM build must return artifacts from both configured builds")
		}
		for name, artifacts := range packages {
			for _, tool := range expectedTools {
				if !artifacts[tool] {
					return fmt.Errorf("LLVM build %s did not return %s", name, tool)
				}
			}
		}
	}
	return nil
}

func llvmOutcome(result *LLVMBuild) string {
	if result.Reason != "" {
		return result.Status + ": " + result.Reason
	}
	return result.Status
}

func llvmSeconds(result *LLVMBuild) string {
	if result.Seconds == nil {
		return llvmNotMeasuredText
	}
	return seconds(*result.Seconds)
}

func writeLLVMMarkdown(out *bufio.Writer, campaign Experiments) {
	printed := false
	for _, cell := range campaign.Cells {
		if cell.LLVM == nil {
			continue
		}
		if !printed {
			fmt.Fprintln(out, "\n| LLVM distributed build | profile | mode | result | elapsed | executors × Ninja jobs | targets | source |")
			fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|")
			printed = true
		}
		result := cell.LLVM
		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %d × %d | %s | %s |\n",
			escapePipes(cell.Tool), escapePipes(result.Profile), escapePipes(result.Mode), escapePipes(llvmOutcome(result)),
			llvmSeconds(result), result.ExecutorCount, result.JobsPerExecutor,
			escapePipes(strings.Join(result.BuildTargets, ", ")), result.SourceRevision)
	}
}
