package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImportCleanupFixture(t *testing.T, selected importedCampaign) (string, map[string][]byte) {
	t.Helper()
	cloud := filepath.Join(selected.root, "environment", "dispat-compilation", "cloud")
	var provenance map[string]any
	mustReadImportJSON(t, filepath.Join(cloud, "provenance.json"), &provenance)
	options := provenance["options"].(map[string]any)
	options["project"], options["zone"] = "fixture-project", "fixture-zone"
	mustWriteImportJSON(t, filepath.Join(cloud, "provenance.json"), provenance)
	var census importCleanupCensus
	mustReadImportJSON(t, filepath.Join(cloud, "provenance.json"), &census)
	worker := census.Instances[0]
	first := importCleanupObservation{InstanceName: worker.Name, InstanceID: worker.ID, LookupFilter: "id=" + worker.ID, RawResponse: " \n[ ]\n", DeletionError: "delete outcome was unknown"}
	second := first
	second.LookupFilter, second.DeletionError = "name="+worker.Name+" AND zone:fixture-zone", ""
	firstBody, _ := json.Marshal(first)
	secondBody, _ := json.Marshal(second)
	journal := append(append(append(firstBody, '\n'), secondBody...), '\n')
	files := map[string][]byte{worker.Name + importCleanupJournalSuffix: journal}
	worker = census.Instances[1]
	legacy, _ := json.MarshalIndent(importCleanupObservation{InstanceName: worker.Name, InstanceID: worker.ID, LookupFilter: "id=" + worker.ID, RawResponse: "[]"}, "", "  ")
	files[worker.Name+importCleanupLegacySuffix] = append(legacy, '\n')
	cleanup, _ := json.Marshal(map[string]any{"complete": false, "resourcesReleased": false, "error": "collection remains incomplete", "absenceEvidenceSha256": map[string]string{census.Instances[0].Name + importCleanupJournalSuffix: importDigest(journal)}})
	files[importCloudCleanupFileName] = cleanup
	trial, _ := json.Marshal(map[string]any{"trial": options, "status": "failed", "reason": "cleanup failed", "seconds": 42, "boundary": "driver lifecycle"})
	files[importCloudTrialFileName] = trial
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(cloud, name), body, fileMode); err != nil {
			t.Fatal(err)
		}
	}
	return cloud, files
}

func TestCampaignImportPreservesOptionalCleanupFailureEvidence(t *testing.T) {
	llvm, base := writeImportFixture(t, llvmWorkload, "101")
	kernel, _ := writeImportFixture(t, kernelWorkload, "202")
	expected := map[string]map[string][]byte{}
	for _, selected := range []importedCampaign{llvm, kernel} {
		_, expected[selected.Workload] = writeImportCleanupFixture(t, selected)
	}
	settings := campaignImportOptions{LLVM: llvm.root, Kernel: kernel.root, Results: t.TempDir(), Source: base.Source, Binary: base.Binary, LLVMRun: "101", LLVMAttempt: "1", KernelRun: "202", KernelAttempt: "1"}
	validated, err := validateCampaignImport(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := installCampaignImport(t.Context(), settings.Results, validated); err != nil {
		t.Fatal(err)
	}
	var installed campaignImport
	mustReadImportJSON(t, filepath.Join(settings.Results, importedCampaignManifest), &installed)
	for _, campaign := range installed.Campaigns {
		for name, before := range expected[campaign.Workload] {
			relative := filepath.Join(importedEvidenceDirectory, campaign.Workload, "environment", "dispat-compilation", "cloud", name)
			after, err := os.ReadFile(filepath.Join(settings.Results, relative))
			if err != nil || !bytes.Equal(before, after) || campaign.Evidence[relative] != importDigest(before) {
				t.Fatalf("cleanup evidence %s changed or lost its hash: %v", name, err)
			}
		}
		var record verdict
		mustReadImportJSON(t, filepath.Join(settings.Results, campaign.Workload+"-full-compilation-dispat", "verdict.json"), &record)
		if !record.Passed || record.buildResult().Status != completedStatus {
			t.Fatal("failed driver cleanup rewrote the completed computation verdict")
		}
		for path := range campaign.Evidence {
			if strings.Contains(path, "changesets") && strings.Contains(path, "cleanup") {
				t.Fatal("cleanup evidence was fabricated for an unsupported executor")
			}
		}
	}
}

func TestCampaignImportRejectsIncompleteOrForeignCleanupEvidence(t *testing.T) {
	selected, _ := writeImportFixture(t, llvmWorkload, "101")
	cloud, originals := writeImportCleanupFixture(t, selected)
	journalName := "fixture-vm-1" + importCleanupJournalSuffix
	for _, scenario := range []string{"missing declared", "digest mismatch", "foreign reference", "incomplete map", "missing declaration", "null declaration", "missing cleanup", "empty journal", "truncated journal", "malformed line", "unknown field", "foreign name", "foreign ID", "wrong filter", "null absence", "present instance", "foreign trial", "malformed cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			t.Cleanup(func() {
				for name, content := range originals {
					if err := os.WriteFile(filepath.Join(cloud, name), content, fileMode); err != nil {
						t.Error(err)
					}
				}
			})
			journal := bytes.Clone(originals[journalName])
			var cleanup map[string]any
			if err := json.Unmarshal(originals[importCloudCleanupFileName], &cleanup); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing declared":
				if err := os.Remove(filepath.Join(cloud, journalName)); err != nil {
					t.Fatal(err)
				}
			case "digest mismatch":
				cleanup["absenceEvidenceSha256"] = map[string]string{journalName: strings.Repeat("e", 64)}
			case "foreign reference":
				cleanup["absenceEvidenceSha256"] = map[string]string{"../foreign.jsonl": importDigest(journal)}
			case "incomplete map":
				cleanup["absenceEvidenceSha256"] = map[string]string{}
			case "missing declaration":
				delete(cleanup, "absenceEvidenceSha256")
			case "null declaration":
				cleanup["absenceEvidenceSha256"] = nil
			case "missing cleanup":
				if err := os.Remove(filepath.Join(cloud, importCloudCleanupFileName)); err != nil {
					t.Fatal(err)
				}
			case "empty journal":
				journal = nil
			case "truncated journal":
				journal = bytes.TrimSuffix(journal, []byte("\n"))
			case "malformed line":
				journal = []byte("{\n")
			case "unknown field":
				journal = bytes.Replace(journal, []byte(`"deletionError"`), []byte(`"credential"`), 1)
			case "foreign name":
				journal = bytes.ReplaceAll(journal, []byte("fixture-vm-1"), []byte("foreign-vm"))
			case "foreign ID":
				journal = bytes.Replace(journal, []byte(`"instanceId":"`), []byte(`"instanceId":"foreign-`), 1)
			case "wrong filter":
				journal = bytes.Replace(journal, []byte(`"lookupFilter":"id=`), []byte(`"lookupFilter":"id=foreign-`), 1)
			case "null absence":
				journal = bytes.Replace(journal, []byte(`"rawResponse":" \n[ ]\n"`), []byte(`"rawResponse":"null"`), 1)
			case "present instance":
				journal = bytes.Replace(journal, []byte(`"rawResponse":" \n[ ]\n"`), []byte(`"rawResponse":"[{}]"`), 1)
			case "foreign trial":
				if err := os.WriteFile(filepath.Join(cloud, importCloudTrialFileName), bytes.Replace(originals[importCloudTrialFileName], []byte("101-1-dispat-compilation"), []byte("999-1-dispat-compilation"), 1), fileMode); err != nil {
					t.Fatal(err)
				}
			case "malformed cleanup":
				if err := os.WriteFile(filepath.Join(cloud, importCloudCleanupFileName), []byte("{"), fileMode); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(journal, originals[journalName]) {
				cleanup["absenceEvidenceSha256"] = map[string]string{journalName: importDigest(journal)}
				if err := os.WriteFile(filepath.Join(cloud, journalName), journal, fileMode); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "malformed cleanup" && scenario != "missing cleanup" {
				mustWriteImportJSON(t, filepath.Join(cloud, importCloudCleanupFileName), cleanup)
			}
			if _, err := readImportedCleanupEvidence(cloud); err == nil {
				t.Fatalf("accepted %s", scenario)
			}
		})
	}
}

func TestCampaignImportCleanupRechecksCensusPathsBeforeReadingFiles(t *testing.T) {
	selected, _ := writeImportFixture(t, llvmWorkload, "101")
	cloud, _ := writeImportCleanupFixture(t, selected)
	path := filepath.Join(cloud, importCloudProvenanceFileName)
	var census importCleanupCensus
	mustReadImportJSON(t, path, &census)
	for _, name := range []string{"../outside", "/outside", ".", ""} {
		census.Instances[0].Name = name
		mustWriteImportJSON(t, path, census)
		if _, err := readImportedCleanupEvidence(cloud); err == nil || err.Error() != importCleanupCensusIdentityError {
			t.Fatalf("unsafe census name %q reached optional file lookup: %v", name, err)
		}
	}
}

func TestCampaignImportCleanupBindsTrialExecutionPins(t *testing.T) {
	selected, _ := writeImportFixture(t, llvmWorkload, "101")
	cloud, originals := writeImportCleanupFixture(t, selected)
	for _, field := range []string{"image", "bootImage", "machineType", "cpuPlatform"} {
		t.Run(field, func(t *testing.T) {
			var trial map[string]any
			if err := json.Unmarshal(originals[importCloudTrialFileName], &trial); err != nil {
				t.Fatal(err)
			}
			trial["trial"].(map[string]any)[field] = "different pinned setting"
			mustWriteImportJSON(t, filepath.Join(cloud, importCloudTrialFileName), trial)
			if _, err := readImportedCleanupEvidence(cloud); err == nil || err.Error() != importCleanupTrialIdentityError {
				t.Fatalf("trial %s contradicts selected cloud identity: %v", field, err)
			}
		})
	}
}

func TestCampaignImportDetectsOptionalEvidenceMutationBeforeInstall(t *testing.T) {
	for _, scenario := range []string{"journal and hash changed", "legacy removed", "trial changed", "new optional file"} {
		t.Run(scenario, func(t *testing.T) {
			selected, imported := writeImportFixture(t, llvmWorkload, "101")
			cloud, _ := writeImportCleanupFixture(t, selected)
			if scenario == "new optional file" {
				if err := os.Remove(filepath.Join(cloud, importCloudTrialFileName)); err != nil {
					t.Fatal(err)
				}
			}
			if err := validateImportedCampaign(t.Context(), &selected, imported); err != nil {
				t.Fatal(err)
			}
			imported.Campaigns = []importedCampaign{selected}
			switch scenario {
			case "legacy removed":
				if err := os.Remove(filepath.Join(cloud, "fixture-vm-2"+importCleanupLegacySuffix)); err != nil {
					t.Fatal(err)
				}
			case "trial changed":
				var trial map[string]any
				mustReadImportJSON(t, filepath.Join(cloud, importCloudTrialFileName), &trial)
				trial["reason"] = "different diagnostic"
				mustWriteImportJSON(t, filepath.Join(cloud, importCloudTrialFileName), trial)
			case "new optional file":
				writeImportCleanupFixture(t, selected)
			case "journal and hash changed":
				path := filepath.Join(cloud, "fixture-vm-1"+importCleanupJournalSuffix)
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				body = bytes.Replace(body, []byte("delete outcome was unknown"), []byte("another valid diagnostic"), 1)
				if err := os.WriteFile(path, body, fileMode); err != nil {
					t.Fatal(err)
				}
				mustWriteImportJSON(t, filepath.Join(cloud, importCloudCleanupFileName), map[string]any{"complete": false, "absenceEvidenceSha256": map[string]string{filepath.Base(path): importDigest(body)}})
			}
			results := t.TempDir()
			if err := installCampaignImport(t.Context(), results, imported); err == nil {
				t.Fatal("changed optional evidence was installed")
			}
			if _, err := os.Stat(filepath.Join(results, importedCampaignManifest)); !os.IsNotExist(err) {
				t.Fatal("failed import published a manifest")
			}
		})
	}
}

func TestCampaignImportMissingCleanupEvidenceRemainsUnrecorded(t *testing.T) {
	selected, imported := writeImportFixture(t, llvmWorkload, "101")
	if err := validateImportedCampaign(t.Context(), &selected, imported); err != nil {
		t.Fatal(err)
	}
	if len(selected.optionalEvidence) != 0 {
		t.Fatal("missing optional observations became recorded evidence")
	}
	imported.Campaigns = []importedCampaign{selected}
	results := t.TempDir()
	if err := installCampaignImport(t.Context(), results, imported); err != nil {
		t.Fatal(err)
	}
	for path := range imported.Campaigns[0].Evidence {
		if strings.Contains(path, "cleanup") || filepath.Base(path) == "trial.json" {
			t.Fatal("legacy missing cleanup was synthesized")
		}
	}
}

func TestCampaignImportRevalidatesStagedCleanupEvidence(t *testing.T) {
	selected, _ := writeImportFixture(t, llvmWorkload, "101")
	writeImportCleanupFixture(t, selected)
	expected, err := snapshotImportedCleanupEvidence(selected.root)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(t.TempDir(), "copy")
	if err := copyImportTree(t.Context(), selected.root, stage); err != nil {
		t.Fatal(err)
	}
	if err := verifyImportedCleanupSnapshot(stage, expected); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stage, "environment", "dispat-compilation", "cloud", importCloudCleanupFileName)
	var cleanup map[string]any
	mustReadImportJSON(t, path, &cleanup)
	cleanup["complete"], cleanup["resourcesReleased"] = true, true
	mustWriteImportJSON(t, path, cleanup)
	if err := verifyImportedCleanupSnapshot(stage, expected); err == nil {
		t.Fatal("changed staged cleanup outcome retained source validation")
	}
}
