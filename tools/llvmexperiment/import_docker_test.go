package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignImportRequiresEveryClassicDockerStoreObservation(t *testing.T) {
	for _, workload := range []string{llvmWorkload, kernelWorkload} {
		t.Run(workload, func(t *testing.T) {
			selected, base := writeImportFixture(t, workload, "101")
			if err := validateImportedCampaign(t.Context(), &selected, base); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{importPrepareDockerInfo, importCoordinatorDockerInfo, "fixture-vm-1" + importWorkerDockerSuffix, "fixture-vm-2" + importWorkerDockerSuffix} {
				path := filepath.Join(selected.root, "environment", "dispat-compilation", "cloud", name)
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, scenario := range []struct{ name, content string }{
					{"missing", ""}, {"malformed", "{"},
					{"nonclassic", `{"ServerVersion":"29.9.2","Driver":"overlayfs"}`},
					{"containerd marker", `{"ServerVersion":"29.9.2","Driver":"overlay2","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`},
					{"missing version", `{"Driver":"overlay2"}`},
					{"blank version", `{"ServerVersion":" ","Driver":"overlay2"}`},
					{"trailing JSON", `{"ServerVersion":"29.9.2","Driver":"overlay2"}{}`},
					{"unsanitized", `{"ServerVersion":"29.9.2","Driver":"overlay2","HTTPProxy":"sensitive proxy setting"}`},
				} {
					t.Run(name+"/"+scenario.name, func(t *testing.T) {
						t.Cleanup(func() {
							if err := os.WriteFile(path, original, fileMode); err != nil {
								t.Error(err)
							}
						})
						if scenario.name == "missing" {
							err = os.Remove(path)
						} else {
							err = os.WriteFile(path, []byte(scenario.content), fileMode)
						}
						if err != nil {
							t.Fatal(err)
						}
						imported := &campaignImport{Source: base.Source, Binary: base.Binary, cells: map[string]string{}}
						if err := validateImportedCampaign(t.Context(), &selected, imported); err == nil || !strings.Contains(err.Error(), "Docker store observation "+name) {
							t.Fatalf("expected selected store observation refusal, got %v", err)
						}
					})
				}
			}
		})
	}
}

func assertImportedDockerEvidence(t *testing.T, results string, expected *campaignImport) {
	t.Helper()
	var retained campaignImport
	mustReadImportJSON(t, filepath.Join(results, importedCampaignManifest), &retained)
	for index, selected := range retained.Campaigns {
		for _, cell := range campaignCells() {
			if cell.Tool == changesetsTool {
				continue
			}
			versions := map[string]bool{}
			for _, name := range []string{importPrepareDockerInfo, importCoordinatorDockerInfo, "fixture-vm-1" + importWorkerDockerSuffix, "fixture-vm-2" + importWorkerDockerSuffix} {
				relative := filepath.Join("environment", cell.Tool+"-"+cell.Mode, "cloud", name)
				destination := filepath.Join(importedEvidenceDirectory, selected.Workload, relative)
				before, err := os.ReadFile(filepath.Join(expected.Campaigns[index].root, relative))
				if err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(filepath.Join(results, destination))
				if err != nil || !bytes.Equal(before, after) || selected.Evidence[destination] != importDigest(after) {
					t.Fatalf("store evidence %s was changed, omitted, or not hashed: %v", destination, err)
				}
				var store importedDockerStore
				mustReadImportJSON(t, filepath.Join(results, destination), &store)
				versions[store.ServerVersion] = true
			}
			if len(versions) != 4 {
				t.Fatal("independently observed daemon versions were not preserved")
			}
		}
	}
}
