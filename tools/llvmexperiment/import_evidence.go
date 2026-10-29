package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func retainImportedEnvironment(root string, imported *campaignImport) error {
	for index := range imported.Campaigns {
		selected := &imported.Campaigns[index]
		if err := verifyImportedCleanupSnapshot(selected.root, selected.optionalEvidence); err != nil {
			return err
		}
		selected.Evidence = map[string]string{}
		paths := []string{"campaign.json"}
		for relative := range selected.optionalEvidence {
			paths = append(paths, relative)
		}
		for _, cell := range campaignCells() {
			if cell.Tool == changesetsTool {
				continue
			}
			cloud := filepath.Join("environment", cell.Tool+"-"+cell.Mode, "cloud")
			paths = append(paths, filepath.Join(cloud, "provenance.json"))
			paths = append(paths, filepath.Join(cloud, importPrepareDockerInfo), filepath.Join(cloud, importCoordinatorDockerInfo))
			var census struct {
				Instances []hardwareInstance `json:"instances"`
			}
			if err := readHardwareJSON(filepath.Join(selected.root, cloud, "provenance.json"), &census); err != nil {
				return err
			}
			for _, instance := range census.Instances {
				if instance.Name == "" || instance.Name == "." || instance.Name == ".." || filepath.Base(instance.Name) != instance.Name {
					return fmt.Errorf("retained instance identity is not a safe filename")
				}
				for _, suffix := range []string{".json", "-disk.json", "-resources.txt", importWorkerDockerSuffix} {
					paths = append(paths, filepath.Join(cloud, instance.Name+suffix))
				}
			}
		}
		for _, relative := range paths {
			content, err := os.ReadFile(filepath.Join(selected.root, relative))
			if err != nil {
				return err
			}
			digest := importDigest(content)
			if expected, optional := selected.optionalEvidence[relative]; optional && digest != expected {
				return fmt.Errorf(importCleanupSourceChangedError)
			}
			if relative == "campaign.json" && digest != selected.ManifestSHA256 {
				return fmt.Errorf("campaign manifest changed during import")
			}
			destination := filepath.Join(importedEvidenceDirectory, selected.Workload, relative)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, destination)), directoryMode); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, destination), content, fileMode); err != nil {
				return err
			}
			selected.Evidence[destination] = digest
		}
		// Verify allocation against the retained bytes used by the report.
		retained := filepath.Join(root, importedEvidenceDirectory, selected.Workload)
		if err := verifyImportedCleanupSnapshot(retained, selected.optionalEvidence); err != nil {
			return err
		}
		for _, cell := range campaignCells() {
			if cell.Tool == changesetsTool {
				continue
			}
			var record verdict
			name := selected.Workload + "-" + fullProfile + "-" + cell.Mode + "-" + cell.Tool
			if err := readHardwareJSON(filepath.Join(root, name, "verdict.json"), &record); err != nil {
				return err
			}
			environment := filepath.Join(retained, "environment", cell.Tool+"-"+cell.Mode)
			hardware, err := readCampaignHardware(environment, record)
			if err != nil {
				return err
			}
			hardware.NinjaJobs, hardware.MakeJobs, hardware.LinkPool, hardware.LinkPoolReason = 0, 0, 0, ""
			if err := compareCampaignHardware(*imported.Allocation, *hardware); err != nil {
				return err
			}
			if err := bindImportedCloudIdentity(environment, imported, record); err != nil {
				return err
			}
			if selected.Workload == kernelWorkload {
				var manifest campaignImportManifest
				if err := readHardwareJSON(filepath.Join(retained, "campaign.json"), &manifest); err != nil {
					return err
				}
				inputs, err := readKernelCampaignInputs(environment, filepath.Join(root, name), record)
				if err != nil {
					return err
				}
				if err := compareKernelCampaignInputs(*manifest.KernelInputs, *inputs); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
