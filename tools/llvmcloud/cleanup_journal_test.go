package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCleanupAppendsReconciliationBeforeLaterAbsence(t *testing.T) {
	d := cleanupDriver(t)
	worker := instance{Name: "vm", ID: "123", Labels: map[string]string{"trial": "trial"}}
	d.state.Instances = []instance{{Name: worker.Name, Labels: worker.Labels}}
	deletionError := errors.New("delete outcome is unknown")
	nameLookups, identityLookups, deletions := 0, 0, 0
	d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
		command := strings.Join(request.Args, " ")
		switch command {
		case "--quiet --project project compute instances list --filter name=vm AND zone:" + defaultZone + " --format=json":
			nameLookups++
			if nameLookups == 1 {
				return json.Marshal([]instance{worker})
			}
			return []byte(" []\n"), nil
		case "--quiet --project project compute instances delete vm --zone " + defaultZone:
			deletions++
			return nil, deletionError
		case "--quiet --project project compute instances list --filter id=123 --format=json":
			identityLookups++
			return []byte("[ ]\n"), nil
		default:
			t.Fatalf("unexpected cleanup command %+v", request)
			return nil, nil
		}
	}
	if err := d.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.settings.Root, "results/cloud", worker.Name+cleanupAbsenceFileSuffix)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertCleanupJournalDigest(t, d, path, first)
	if err := d.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) <= len(first) || !bytes.HasPrefix(retained, first) {
		t.Fatalf("later cleanup overwrote earlier reconciliation: first=%s retained=%s", first, retained)
	}
	assertCleanupJournalDigest(t, d, path, retained)
	lines := bytes.Split(bytes.TrimSuffix(retained, []byte("\n")), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected two complete journal lines, got %d", len(lines))
	}
	var observations [2]cleanupAbsenceEvidence
	for index, line := range lines {
		if err := json.Unmarshal(line, &observations[index]); err != nil {
			t.Fatal(err)
		}
		if observations[index].InstanceName != worker.Name || observations[index].InstanceID != worker.ID {
			t.Fatalf("observation lost exact instance identity: %+v", observations[index])
		}
	}
	if observations[0].LookupFilter != "id=123" || observations[0].RawResponse != "[ ]\n" || observations[0].DeletionError != deletionError.Error() {
		t.Fatalf("reconciliation evidence lost: %+v", observations[0])
	}
	if observations[1].LookupFilter != "name=vm AND zone:"+defaultZone || observations[1].RawResponse != " []\n" || observations[1].DeletionError != "" {
		t.Fatalf("later absence evidence lost: %+v", observations[1])
	}
	if nameLookups != 2 || identityLookups != 1 || deletions != 1 {
		t.Fatalf("cleanup changed cloud operations: names=%d identities=%d deletions=%d", nameLookups, identityLookups, deletions)
	}
}

func TestCleanupJournalPreservesHistoricalEvidenceAndRejectsOversizedObservation(t *testing.T) {
	d := cleanupDriver(t)
	historicalPath := filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.json")
	historical := []byte("historical cleanup observation\n")
	if err := os.WriteFile(historicalPath, historical, privateFileMode); err != nil {
		t.Fatal(err)
	}
	evidence := cleanupAbsenceEvidence{InstanceName: "vm", InstanceID: "123", LookupFilter: "id=123", RawResponse: "[]\n"}
	if err := d.recordCleanupAbsence(evidence, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != privateFileMode {
		t.Fatalf("journal is not private: %v, %v", info, err)
	}
	evidence.RawResponse = strings.Repeat(" ", maximumCleanupObservationBytes)
	if err := d.recordCleanupAbsence(evidence, nil); err == nil {
		t.Fatal("oversized observation was appended")
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, before) {
		t.Fatalf("oversized observation changed prior journal bytes: %v", err)
	}
	legacy, err := os.ReadFile(historicalPath)
	if err != nil || !bytes.Equal(legacy, historical) {
		t.Fatalf("historical JSON evidence changed: %v", err)
	}
}

func TestCleanupJournalConcurrentAppendPreservesEveryObservation(t *testing.T) {
	d := cleanupDriver(t)
	d.state.Instances = []instance{{Name: "vm", ID: "123"}}
	const observations = 32
	ready := make(chan struct{})
	results := make(chan error, observations)
	var writers sync.WaitGroup
	for index := range observations {
		writers.Go(func() {
			<-ready
			evidence := cleanupAbsenceEvidence{InstanceName: "vm", InstanceID: "123", LookupFilter: "id=123", RawResponse: "[]\n"}
			results <- d.recordCleanupAbsence(evidence, fmt.Errorf("deletion result %d", index))
		})
	}
	close(ready)
	writers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	if len(lines) != observations {
		t.Fatalf("journal lost observations: got %d want %d", len(lines), observations)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var evidence cleanupAbsenceEvidence
		if err := json.Unmarshal(line, &evidence); err != nil {
			t.Fatalf("journal records were interleaved: %v", err)
		}
		if evidence.InstanceName != "vm" || evidence.InstanceID != "123" || evidence.LookupFilter != "id=123" || evidence.RawResponse != "[]\n" || seen[evidence.DeletionError] {
			t.Fatalf("journal observation changed or duplicated: %+v", evidence)
		}
		seen[evidence.DeletionError] = true
	}
	for index := range observations {
		if !seen[fmt.Sprintf("deletion result %d", index)] {
			t.Fatalf("journal lost observation %d", index)
		}
	}
	digests, err := d.cleanupAbsenceDigests()
	if err != nil || len(digests) != 1 || digests["vm-cleanup-absence.jsonl"] != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatalf("journal digest does not bind every appended observation: %v, %v", digests, err)
	}
}

func assertCleanupJournalDigest(t *testing.T, d *driver, path string, content []byte) {
	t.Helper()
	var record cleanupRecord
	if err := readJSON(filepath.Join(d.settings.Root, "results/cloud/cleanup.json"), &record); err != nil {
		t.Fatal(err)
	}
	if len(record.AbsenceEvidenceSHA256) != 1 || record.AbsenceEvidenceSHA256[filepath.Base(path)] != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatalf("cleanup snapshot does not bind current journal bytes: %+v", record)
	}
}

func TestCleanupDigestInventoryIncludesOnlyOwnedJournals(t *testing.T) {
	d := cleanupDriver(t)
	d.state.Instances = []instance{{Name: "vm", ID: "123"}, {Name: "absent", ID: "456"}}
	for name, content := range map[string]string{
		"vm-cleanup-absence.jsonl":      "owned journal\n",
		"foreign-cleanup-absence.jsonl": "unowned journal\n",
		"vm-cleanup-absence.json":       "legacy evidence\n",
	} {
		if err := os.WriteFile(filepath.Join(d.settings.Root, "results/cloud", name), []byte(content), privateFileMode); err != nil {
			t.Fatal(err)
		}
	}
	digests, err := d.cleanupAbsenceDigests()
	if err != nil || len(digests) != 1 || digests["vm-cleanup-absence.jsonl"] != fmt.Sprintf("%x", sha256.Sum256([]byte("owned journal\n"))) {
		t.Fatalf("digest inventory adopted another file or lost owned bytes: %v, %v", digests, err)
	}
}

func TestCleanupDigestInventoryRejectsUnsafeOrUnreadableJournals(t *testing.T) {
	for _, scenario := range []string{"directory", "symlink", "nonlocal name"} {
		t.Run(scenario, func(t *testing.T) {
			d := cleanupDriver(t)
			d.state.Instances = []instance{{Name: "vm", ID: "123"}}
			path := filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl")
			switch scenario {
			case "directory":
				if err := os.Mkdir(path, privateDirectoryMode); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "unowned"), path); err != nil {
					t.Fatal(err)
				}
			case "nonlocal name":
				d.state.Instances[0].Name = "../vm"
			}
			digests, err := d.cleanupAbsenceDigests()
			if err == nil || len(digests) != 0 {
				t.Fatalf("unproven journal bytes got a digest: %v, %v", digests, err)
			}
		})
	}
}

func TestCleanupSnapshotDeclaresEmptyJournalInventory(t *testing.T) {
	d := cleanupDriver(t)
	if err := d.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := readJSON(filepath.Join(d.settings.Root, "results/cloud/cleanup.json"), &record); err != nil {
		t.Fatal(err)
	}
	if string(record["absenceEvidenceSha256"]) != "{}" {
		t.Fatalf("missing journals were not represented as a known empty map: %s", record["absenceEvidenceSha256"])
	}
}
