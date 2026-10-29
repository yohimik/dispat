package main

import (
	"bufio"
	"fmt"
	"strings"
)

const llvmWorkloadName = "LLVM"

// LLVMBuild records LLVM's workload-specific identity alongside its build evidence.
type LLVMBuild struct {
	DistributedBuild
	LLVMVersion string `json:"llvmVersion"`
}

// LLVMArtifact is the retained name for an LLVM executable artifact.
type LLVMArtifact = BuildArtifact

func validateLLVM(result *LLVMBuild) error {
	if result == nil {
		return nil
	}
	if result.LLVMVersion == "" {
		return fmt.Errorf("LLVM build version is missing")
	}
	packages, err := validateDistributedBuild(&result.DistributedBuild, llvmWorkloadName)
	if err != nil {
		return err
	}
	expectedTools := []string{"llvm-cxxfilt", "llvm-size"}
	if result.Profile == "full" {
		expectedTools = append(expectedTools, "llc", "opt", "llvm-config")
		if len(result.BuildTargets) != 1 || result.BuildTargets[0] != "all" {
			return fmt.Errorf("full LLVM build must name the all target")
		}
	}
	if result.Status == buildCompletedStatus {
		if len(result.Artifacts) != len(expectedTools)*buildExecutorCount {
			return fmt.Errorf("completed LLVM build lacks verified artifacts")
		}
		if len(packages) != buildExecutorCount {
			return fmt.Errorf("completed LLVM build must return artifacts from both configured builds")
		}
		for name, artifacts := range packages {
			for _, tool := range expectedTools {
				if _, exists := artifacts[tool]; !exists {
					return fmt.Errorf("LLVM build %s did not return %s", name, tool)
				}
			}
		}
	}
	return nil
}

func llvmOutcome(result *LLVMBuild) string { return buildOutcome(&result.DistributedBuild) }
func llvmSeconds(result *LLVMBuild) string { return buildSeconds(&result.DistributedBuild) }

func writeLLVMMarkdown(out *bufio.Writer, campaign Experiments) {
	printed := false
	for _, cell := range campaign.Cells {
		if cell.LLVM == nil {
			continue
		}
		if !printed {
			fmt.Fprintln(out, "\n| LLVM distributed build | profile | mode | result | elapsed | executors × Ninja jobs | targets | source | campaign Dispat binary |")
			fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|---|")
			printed = true
		}
		result := cell.LLVM
		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %d × %d | %s | %s | %s |\n",
			escapePipes(cell.Tool), escapePipes(result.Profile), escapePipes(result.Mode), escapePipes(llvmOutcome(result)),
			llvmSeconds(result), result.ExecutorCount, result.JobsPerExecutor,
			escapePipes(strings.Join(result.BuildTargets, ", ")), result.SourceRevision, escapePipes(result.BinaryID))
	}
}
