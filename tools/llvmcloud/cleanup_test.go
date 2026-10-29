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
