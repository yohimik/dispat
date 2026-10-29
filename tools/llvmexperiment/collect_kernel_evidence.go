package main

import (
	"fmt"
	"path/filepath"
	"slices"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

// Collection already verifies every retained file's digest. These bindings also
// ensure the reported configuration and inventory describe the executed task.
func bindKernelRawEvidence(root string, record verdict) error {
	byID, byPath := map[string]measurement.Evidence{}, map[string]measurement.Evidence{}
	for _, evidence := range record.ExecutionMeasurement.Trial.Evidence {
		byID[evidence.ID], byPath[evidence.Path] = evidence, evidence
	}
	seen := map[string]bool{}
	for _, interval := range record.ExecutionMeasurement.TaskIntervals {
		if interval.Operation != kernelOperation || interval.Status != completedStatus {
			continue
		}
		if len(interval.Evidence) != 1 {
			return fmt.Errorf(kernelTerminalCountError, interval.TaskID)
		}
		evidence := byID[interval.Evidence[0].EvidenceID]
		if filepath.Base(evidence.Path) != taskFinishedFile {
			return fmt.Errorf(kernelTerminalMissingError, interval.TaskID)
		}
		var manifest taskManifest
		if err := readHardwareJSON(filepath.Join(root, evidence.Path), &manifest); err != nil {
			return err
		}
		item := manifest.Task
		if err := validateTask(item); err != nil {
			return err
		}
		build := record.buildResult()
		if err := bindKernelTerminalArtifacts(build, manifest); err != nil {
			return err
		}
		if err := bindTerminalIntervals(record.ExecutionMeasurement.TaskIntervals, manifest); err != nil {
			return err
		}
		if item.Workload != kernelWorkload || item.Profile != build.Profile || item.Jobs != build.JobsPerExecutor || item.Run != record.ExecutionMeasurement.Trial.ID ||
			item.Name != interval.TaskID || manifest.Node != interval.NodeID || manifest.AttemptID != interval.AttemptID || manifest.Status != completedStatus || seen[item.Backend] {
			return fmt.Errorf(kernelTerminalIdentityError)
		}
		if !slices.Equal(manifest.ConfigureArguments, kernelCommand(item, manifest.WorkingDirectory, []string{kernelConfiguration(item.Profile)}).Args) ||
			!slices.Equal(manifest.BuildArguments, kernelCommand(item, manifest.WorkingDirectory, kernelMakeTargets(item)).Args) || !slices.Equal(manifest.BuildEnvironment, kernelBuildEnvironment()) {
			return fmt.Errorf(kernelExecutedArgumentsError, item.Backend)
		}
		config := byPath[filepath.Join(filepath.Dir(evidence.Path), kernelResolvedConfig)]
		if config.SHA256 == "" || config.SHA256 != build.ConfigDigests[item.Backend] || manifest.Kernel == nil || config.SHA256 != manifest.Kernel.ConfigSHA256 || manifest.Kernel.ModuleCount != build.ModuleCounts[item.Backend] {
			return fmt.Errorf(kernelTerminalConfigError, item.Backend)
		}
		if item.Profile == fullProfile {
			inventory := byPath[filepath.Join(filepath.Dir(evidence.Path), kernelModulesManifest)]
			order := byPath[filepath.Join(filepath.Dir(evidence.Path), kernelModulesOrder)]
			if inventory.SHA256 == "" || order.SHA256 == "" || !kernelArtifactDigestMatches(build, item.Name, inventory, order) {
				return fmt.Errorf(kernelInventoryEvidenceError, item.Backend)
			}
			if err := verifyKernelModuleInventory(filepath.Join(root, filepath.Dir(evidence.Path)), item, manifest.Kernel); err != nil {
				return err
			}
		} else if manifest.Kernel.AreModulesBuilt || manifest.Kernel.ModuleCount != 0 {
			return fmt.Errorf(kernelSmokeModulesError)
		}
		seen[item.Backend] = true
	}
	if len(seen) != executorCount {
		return fmt.Errorf(kernelRawCoverageError)
	}
	return nil
}

func bindKernelTerminalArtifacts(build *buildResult, manifest taskManifest) error {
	expected := map[string]artifact{}
	for _, entry := range manifest.Artifacts {
		if entry.Package != manifest.Task.Name || !slices.Contains(workloadOutputs(kernelWorkload, manifest.Task.Profile), entry.Path) {
			return fmt.Errorf(kernelTerminalArtifactsError, manifest.Task.Name)
		}
		if _, exists := expected[entry.Path]; exists {
			return fmt.Errorf(kernelTerminalArtifactsError, manifest.Task.Name)
		}
		expected[entry.Path] = entry
	}
	seen := map[string]bool{}
	for _, entry := range build.Artifacts {
		if entry.Package != manifest.Task.Name {
			continue
		}
		if seen[entry.Path] || expected[entry.Path] != entry {
			return fmt.Errorf(kernelTerminalArtifactsError, manifest.Task.Name)
		}
		seen[entry.Path] = true
	}
	if len(expected) != len(workloadOutputs(kernelWorkload, manifest.Task.Profile)) || len(seen) != len(expected) {
		return fmt.Errorf(kernelTerminalArtifactsError, manifest.Task.Name)
	}
	return nil
}

func kernelArtifactDigestMatches(build *buildResult, task string, entries ...measurement.Evidence) bool {
	for _, evidence := range entries {
		found := false
		for _, artifact := range build.Artifacts {
			if artifact.Package == task && artifact.Path == filepath.Base(evidence.Path) && artifact.SHA256 == evidence.SHA256 {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
