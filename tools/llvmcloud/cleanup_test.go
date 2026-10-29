package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cleanupDriver(t *testing.T) *driver {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"private", "results/cloud"} {
		if err := os.MkdirAll(filepath.Join(root, name), privateDirectoryMode); err != nil {
			t.Fatal(err)
		}
	}
	return &driver{settings: options{Root: root, ID: "trial", Project: "project", Zone: defaultZone}}
}

func TestUncertainCoordinatorCreationRetainsIntentAndDeletesOnlyReconciledID(t *testing.T) {
	d := cleanupDriver(t)
	identity := strings.Repeat("a", 64)
	created, removed := false, false
	d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
		switch request.Args[0] {
		case "ps":
			if created {
				return []byte(identity), nil
			}
			return nil, nil
		case "create":
			var retained journal
			if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
				t.Fatal(err)
			}
			if retained.CoordinatorName != "llvm-coordinator-trial" || retained.Coordinator != "" {
				t.Fatalf("intent not retained: %+v", retained)
			}
			created = true
			return nil, context.Canceled
		case "inspect":
			return []byte(`[{"Id":"` + identity + `","Name":"/llvm-coordinator-trial","Config":{"Labels":{"dispat-trial":"trial"}}}]`), nil
		case "rm":
			var retained journal
			if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
				t.Fatal(err)
			}
			if retained.Coordinator != identity || request.Args[2] != identity {
				t.Fatal("delete did not use persisted exact identity")
			}
			removed = true
			return nil, nil
		}
		t.Fatalf("unexpected command %+v", request)
		return nil, nil
	}
	if err := d.createCoordinator(t.Context(), []string{"create"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := d.cleanupCoordinator(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !removed || d.state.Coordinator != "" || d.state.CoordinatorName != "" {
		t.Fatalf("cleanup state %+v removed=%v", d.state, removed)
	}
}

func TestCoordinatorCleanupRejectsUnrelatedContainerAndLookupFailure(t *testing.T) {
	identity := strings.Repeat("a", 64)
	for _, scenario := range []struct {
		name, actualName, label, retainedID string
		lookupErr                           error
	}{
		{name: "foreign label", actualName: "/llvm-coordinator-trial", label: "foreign"},
		{name: "foreign name", actualName: "/other", label: "trial"},
		{name: "changed ID", actualName: "/llvm-coordinator-trial", label: "trial", retainedID: strings.Repeat("b", 64)},
		{name: "lookup unavailable", lookupErr: errors.New("daemon unavailable")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			d.state.CoordinatorName, d.state.Coordinator = "llvm-coordinator-trial", scenario.retainedID
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				switch request.Args[0] {
				case "ps":
					return []byte(identity), scenario.lookupErr
				case "inspect":
					return json.Marshal([]map[string]any{{"Id": identity, "Name": scenario.actualName, "Config": map[string]any{"Labels": map[string]string{"dispat-trial": scenario.label}}}})
				default:
					t.Fatalf("unsafe cleanup command %+v", request)
					return nil, nil
				}
			}
			if err := d.cleanupCoordinator(t.Context()); err == nil {
				t.Fatal("unrelated/unknown container accepted")
			}
		})
	}
}

func TestPreexistingCoordinatorDoesNotBecomeCleanupIntent(t *testing.T) {
	d := cleanupDriver(t)
	d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
		if request.Args[0] != "ps" {
			t.Fatalf("unexpected mutation %+v", request)
		}
		return []byte(strings.Repeat("a", 64)), nil
	}
	if err := d.createCoordinator(t.Context(), []string{"create"}); err == nil {
		t.Fatal("preexisting container reused")
	}
	if d.state.CoordinatorName != "" {
		t.Fatal("preexisting container became owned intent")
	}
	if err := d.cleanupCoordinator(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSSHMastersCloseDespiteAbsentVMOrCollectionFailure(t *testing.T) {
	for _, collectionFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent VM", true: "failed collection"}[collectionFailure], func(t *testing.T) {
			d := cleanupDriver(t)
			d.state.Instances = []instance{{Name: "vm-one", ID: "123", Labels: map[string]string{"trial": "trial"}}, {Name: "vm-two", ID: "456", Labels: map[string]string{"trial": "trial"}}}
			for _, name := range []string{"llvm-node-1.sock", "llvm-node-2.sock"} {
				if err := os.WriteFile(filepath.Join(d.settings.Root, "private", name), nil, privateFileMode); err != nil {
					t.Fatal(err)
				}
			}
			closed := []string{}
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				if request.Name == "ssh" {
					if len(request.Args) != 5 || request.Args[0] != "-S" || request.Args[2] != "-O" || request.Args[3] != "exit" {
						t.Fatalf("unexpected SSH command %+v", request)
					}
					closed = append(closed, filepath.Base(request.Args[1]))
					if len(closed) == 1 {
						return nil, errors.New("first master refused exit")
					}
					return nil, nil
				}
				command := strings.Join(request.Args, " ")
				if collectionFailure && strings.Contains(command, "name=vm-one") {
					return json.Marshal(d.state.Instances[:1])
				}
				return []byte("[]"), nil
			}
			if err := d.cleanup(t.Context()); err == nil {
				t.Fatal("failed master closure not reported")
			}
			if strings.Join(closed, ",") != "llvm-node-1.sock,llvm-node-2.sock" {
				t.Fatalf("masters not independently closed: %v", closed)
			}
		})
	}
}

func TestCleanupSeparatesResourceReleaseFromCollectionFailure(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		collectionError error
		deleteError     error
		isAbsent        bool
	}{
		{name: "complete"},
		{name: "collection failed after interrupted setup", collectionError: errors.New("evidence directory is absent")},
		{name: "deletion failed", deleteError: errors.New("delete permission denied")},
		{name: "collection failed but deletion reconciled absent", collectionError: errors.New("evidence directory is absent"), deleteError: errors.New("delete outcome is unknown"), isAbsent: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			var worker instance
			if err := json.Unmarshal([]byte(`{"name":"vm","id":"123","labels":{"trial":"trial"},"networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &worker); err != nil {
				t.Fatal(err)
			}
			d.state.Instances = []instance{worker}
			deleted, reconciledDeletion := false, false
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				if request.Name == "ssh" {
					return nil, scenario.collectionError
				}
				command := strings.Join(request.Args, " ")
				if strings.Contains(command, "instances delete") {
					deleted = true
					return nil, scenario.deleteError
				}
				if strings.Contains(command, "id=123") {
					reconciledDeletion = true
					if scenario.deleteError != nil && !scenario.isAbsent {
						return json.Marshal([]instance{worker})
					}
					return []byte("[]"), nil
				}
				return json.Marshal([]instance{worker})
			}
			err := d.cleanup(t.Context())
			var record cleanupRecord
			if readErr := readJSON(filepath.Join(d.settings.Root, "results/cloud/cleanup.json"), &record); readErr != nil {
				t.Fatal(readErr)
			}
			wantReleased := scenario.deleteError == nil || scenario.isAbsent
			wantComplete := scenario.collectionError == nil && wantReleased
			if (err == nil) != wantComplete || record.Complete != wantComplete || record.ResourcesReleased != wantReleased || !deleted || !reconciledDeletion {
				t.Fatalf("cleanup err=%v record=%+v deleted=%v deletion reconciled=%v", err, record, deleted, reconciledDeletion)
			}
			if (record.CollectionError != "") != (scenario.collectionError != nil) {
				t.Fatalf("collection failure was lost or invented: %+v", record)
			}
		})
	}
}

func TestCleanupReconcilesFailedDeletionWithFreshExactAbsence(t *testing.T) {
	deleteError := errors.New("delete outcome is unknown")
	lookupError := errors.New("instance lookup is unavailable")
	var worker instance
	if err := json.Unmarshal([]byte(`{"name":"vm","id":"123","labels":{"trial":"trial"},"networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &worker); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name          string
		response      string
		lookupErr     error
		knownIdentity bool
		blockEvidence bool
		wantError     bool
	}{
		{name: "fresh exact absence", response: " \n[ ]\n"},
		{name: "known instance disappears before delete", response: "[]", knownIdentity: true},
		{name: "lookup unavailable", lookupErr: lookupError, wantError: true},
		{name: "lookup cancelled", lookupErr: context.Canceled, wantError: true},
		{name: "still present", response: `[{"name":"vm","id":"123","labels":{"trial":"trial"}}]`, wantError: true},
		{name: "different identity", response: `[{"name":"vm","id":"456","labels":{"trial":"trial"}}]`, wantError: true},
		{name: "malformed lookup", response: "invalid JSON", wantError: true},
		{name: "null lookup is not absence", response: "null", wantError: true},
		{name: "absence evidence cannot be retained", response: "[]", blockEvidence: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			d := cleanupDriver(t)
			d.state.Instances = []instance{{Name: worker.Name, Labels: worker.Labels}}
			if scenario.knownIdentity {
				d.state.Instances[0] = worker
			}
			if scenario.blockEvidence {
				if err := os.Mkdir(filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl"), privateDirectoryMode); err != nil {
					t.Fatal(err)
				}
			}
			deletions, absenceLookups := 0, 0
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				if request.Name == "ssh" {
					return nil, nil
				}
				command := strings.Join(request.Args, " ")
				switch command {
				case "--quiet --project project compute instances list --filter name=vm AND zone:" + defaultZone + " --format=json":
					if deletions != 0 {
						t.Fatal("post-delete reconciliation used a mutable name lookup")
					}
					return json.Marshal([]instance{worker})
				case "--quiet --project project compute instances delete vm --zone " + defaultZone:
					var retained journal
					if err := readJSON(filepath.Join(d.settings.Root, stateFile), &retained); err != nil {
						t.Fatal(err)
					}
					if len(retained.Instances) != 1 || retained.Instances[0].ID != worker.ID {
						t.Fatalf("deletion did not retain reconciled identity: %+v", retained)
					}
					deletions++
					return nil, deleteError
				case "--quiet --project project compute instances list --filter id=123 --format=json":
					if deletions != 1 {
						t.Fatal("absence lookup did not follow exactly one deletion attempt")
					}
					absenceLookups++
					return []byte(scenario.response), scenario.lookupErr
				default:
					t.Fatalf("unexpected cleanup command %+v", request)
					return nil, nil
				}
			}
			err := d.cleanup(t.Context())
			var record cleanupRecord
			if readErr := readJSON(filepath.Join(d.settings.Root, "results/cloud/cleanup.json"), &record); readErr != nil {
				t.Fatal(readErr)
			}
			if (err != nil) != scenario.wantError || record.Complete == scenario.wantError || record.ResourcesReleased == scenario.wantError || deletions != 1 || absenceLookups != 1 {
				t.Fatalf("cleanup err=%v record=%+v deletions=%d absence lookups=%d", err, record, deletions, absenceLookups)
			}
			if scenario.wantError && !errors.Is(err, deleteError) || scenario.lookupErr != nil && !errors.Is(err, scenario.lookupErr) {
				t.Fatalf("cleanup lost the unresolved deletion or lookup cause: %v", err)
			}
			var evidence cleanupAbsenceEvidence
			evidenceError := readJSON(filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl"), &evidence)
			if scenario.blockEvidence {
				if evidenceError == nil {
					t.Fatalf("failed evidence write was accepted: %+v", evidence)
				}
				return
			}
			if scenario.wantError {
				if !errors.Is(evidenceError, os.ErrNotExist) {
					t.Fatalf("unproven absence was retained as evidence: %+v, %v", evidence, evidenceError)
				}
				return
			}
			if evidenceError != nil || evidence.InstanceName != worker.Name || evidence.InstanceID != worker.ID || evidence.LookupFilter != "id=123" || evidence.RawResponse != scenario.response || evidence.DeletionError != deleteError.Error() {
				t.Fatalf("cleanup lost exact reconciliation evidence: %+v, %v", evidence, evidenceError)
			}
		})
	}
}

func TestCleanupRequiresExplicitInitialAbsenceAndRetainsIt(t *testing.T) {
	for _, response := range []string{"[]\n", "null"} {
		t.Run(strings.TrimSpace(response), func(t *testing.T) {
			d := cleanupDriver(t)
			d.state.Instances = []instance{{Name: "vm", ID: "123", Labels: map[string]string{"trial": "trial"}}}
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				if strings.Join(request.Args, " ") != "--quiet --project project compute instances list --filter name=vm AND zone:"+defaultZone+" --format=json" {
					t.Fatalf("unexpected cleanup command %+v", request)
				}
				return []byte(response), nil
			}
			err := d.cleanup(t.Context())
			var evidence cleanupAbsenceEvidence
			evidenceError := readJSON(filepath.Join(d.settings.Root, "results/cloud/vm-cleanup-absence.jsonl"), &evidence)
			if response == "null" {
				if err == nil || !errors.Is(evidenceError, os.ErrNotExist) {
					t.Fatalf("null lookup treated as absence: %v, %+v, %v", err, evidence, evidenceError)
				}
				return
			}
			if err != nil || evidenceError != nil || evidence.InstanceName != "vm" || evidence.InstanceID != "123" || evidence.LookupFilter != "name=vm AND zone:"+defaultZone || evidence.RawResponse != response || evidence.DeletionError != "" {
				t.Fatalf("initial absence evidence lost: %v, %+v, %v", err, evidence, evidenceError)
			}
		})
	}
}
