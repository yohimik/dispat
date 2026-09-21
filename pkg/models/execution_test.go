package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The execution object is read by a node deciding what it is allowed to do,
// so the questions worth testing here are the ones a caller asks without
// knowing whether the key was written at all: an absent object has to answer
// every one of them, and answer with the documented default. The other half
// is the contract every model in this package keeps, that a marshalled model
// is a loadable config, which for this key also has to hold that a node that
// never heard of worker nodes marshals no execution key whatsoever.

func TestExecutionDefaultsAnswerAnAbsentKey(t *testing.T) {
	// Nil is what File.Execution holds when the key is absent, and it is the
	// receiver every accessor is called on in that state.
	var absent *ExecutionConfig
	if got := absent.ResolveRole(); got != ExecutionRoleOrchestrator {
		t.Errorf("an absent execution key is the orchestrator role, got %q", got)
	}
	if absent.IsWorker() {
		t.Error("an absent execution key is not a worker")
	}
	if absent.IsDistributed() {
		t.Error("an absent execution key delegates nothing")
	}
	if got := absent.ResolveConcurrency(); got != DefaultExecutionConcurrency {
		t.Errorf("ResolveConcurrency() = %d, want %d", got, DefaultExecutionConcurrency)
	}
	wantTimeouts := ExecutionTimeoutsConfig{
		Preflight: DefaultExecutionPreflightTimeout,
		Task:      DefaultExecutionTaskTimeout,
		Cancel:    DefaultExecutionCancelTimeout,
	}
	if got := absent.ResolveTimeouts(); got != wantTimeouts {
		t.Errorf("ResolveTimeouts() = %+v, want %+v", got, wantTimeouts)
	}
	wantTransfer := ExecutionTransferConfig{
		MaxFiles:         DefaultExecutionMaxFiles,
		MaxBytes:         DefaultExecutionMaxBytes,
		MaxManifestBytes: DefaultExecutionMaxManifestBytes,
		Timeout:          DefaultExecutionTransferTimeout,
	}
	if got := absent.ResolveTransfer(); got != wantTransfer {
		t.Errorf("ResolveTransfer() = %+v, want %+v", got, wantTransfer)
	}
	// An object stating nothing says exactly what no object says, which is
	// what lets the loader check the defaults with the same sentences it
	// checks a written configuration with.
	stated := &ExecutionConfig{}
	if stated.ResolveRole() != absent.ResolveRole() ||
		stated.ResolveConcurrency() != absent.ResolveConcurrency() ||
		stated.ResolveTimeouts() != absent.ResolveTimeouts() ||
		stated.ResolveTransfer() != absent.ResolveTransfer() {
		t.Error("an empty execution object must resolve exactly as an absent one")
	}
}

func TestExecutionResolvesStatedValues(t *testing.T) {
	// A stated value wins, a zero inside a partially written object still
	// takes its own default, and a stated nonpositive capacity is handed back
	// as written so the loader can name it in the refusal rather than quietly
	// repairing it.
	c := &ExecutionConfig{
		Role:        ExecutionRoleWorker,
		Concurrency: Int(0),
		Timeouts:    &ExecutionTimeoutsConfig{Task: 90},
		Transfer:    &ExecutionTransferConfig{MaxBytes: 4096},
	}
	if got := c.ResolveRole(); got != ExecutionRoleWorker {
		t.Errorf("ResolveRole() = %q", got)
	}
	if !c.IsWorker() {
		t.Error("the worker role must read as a worker")
	}
	if got := c.ResolveConcurrency(); got != 0 {
		t.Errorf("ResolveConcurrency() = %d, want the stated 0 back", got)
	}
	timeouts := c.ResolveTimeouts()
	if timeouts.Task != 90 {
		t.Errorf("stated task timeout lost: %+v", timeouts)
	}
	if timeouts.Preflight != DefaultExecutionPreflightTimeout || timeouts.Cancel != DefaultExecutionCancelTimeout {
		t.Errorf("an unstated sibling must keep its default: %+v", timeouts)
	}
	transfer := c.ResolveTransfer()
	if transfer.MaxBytes != 4096 {
		t.Errorf("stated maxBytes lost: %+v", transfer)
	}
	if transfer.MaxFiles != DefaultExecutionMaxFiles ||
		transfer.MaxManifestBytes != DefaultExecutionMaxManifestBytes ||
		transfer.Timeout != DefaultExecutionTransferTimeout {
		t.Errorf("an unstated sibling must keep its default: %+v", transfer)
	}
}

func TestExecutionIsDistributedFollowsTheWorkerList(t *testing.T) {
	// Delegation is the one question the release path asks before anything
	// about execution changes, and it is the worker list that answers it: a
	// role, a capacity or a mailbox of its own changes nothing.
	for name, tc := range map[string]struct {
		config *ExecutionConfig
		want   bool
	}{
		"absent":               {nil, false},
		"empty object":         {&ExecutionConfig{}, false},
		"named node only":      {&ExecutionConfig{Name: "build-a", Endpoint: "/srv/mail.git"}, false},
		"empty worker list":    {&ExecutionConfig{Workers: []ExecutionWorkerConfig{}}, false},
		"one worker":           {&ExecutionConfig{Workers: []ExecutionWorkerConfig{{Name: "a"}}}, true},
		"worker role, no list": {&ExecutionConfig{Role: ExecutionRoleWorker}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.config.IsDistributed(); got != tc.want {
				t.Errorf("IsDistributed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExecutionModelRoundTrip(t *testing.T) {
	// The json tag is the config file's key, so a marshalled model is a
	// loadable dispat.json. Node names keep their case through the file,
	// which is the whole reason workers are a list of objects rather than a
	// map, and a stated capacity of 0 survives as a stated 0 because the
	// field is a pointer.
	f := File{
		Execution: &ExecutionConfig{
			Role:        ExecutionRoleOrchestrator,
			Concurrency: Int(2),
			Name:        "Build-A",
			Endpoint:    "ssh://build.example.test/srv/mailbox.git",
			SecretEnv:   "DISPAT_EXECUTION_SECRET",
			Workers: []ExecutionWorkerConfig{
				{Name: "Build-B", Endpoint: "https://git.example.test/mailbox-b.git"},
			},
			Timeouts: &ExecutionTimeoutsConfig{Preflight: 30, Task: 600, Cancel: 15},
			Transfer: &ExecutionTransferConfig{MaxFiles: 10, MaxBytes: 20, MaxManifestBytes: 30, Timeout: 40},
		},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var got File
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, f) {
		t.Fatalf("round trip:\n got %#v\nwant %#v", got.Execution, f.Execution)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	execution, ok := raw["execution"].(map[string]any)
	if !ok {
		t.Fatalf("the execution key must marshal back into a loadable file: %s", data)
	}
	for _, key := range []string{"role", "concurrency", "name", "endpoint", "secretEnv", "workers", "timeouts", "transfer"} {
		if _, stated := execution[key]; !stated {
			t.Errorf("execution is missing key %q: %v", key, execution)
		}
	}
	workers, ok := execution["workers"].([]any)
	if !ok || len(workers) != 1 {
		t.Fatalf("workers must marshal as a list: %v", execution["workers"])
	}
	if name := workers[0].(map[string]any)["name"]; name != "Build-B" {
		t.Errorf("node name case lost in the round trip: %v", name)
	}
	// A stated capacity of 0 is a refusal the loader owes the reader, so the
	// key has to survive the file rather than being dropped as a zero value.
	zero, err := json.Marshal(File{Execution: &ExecutionConfig{Concurrency: Int(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"execution":{"concurrency":0}}`; string(zero) != want {
		t.Errorf("a stated zero capacity must reach the file:\n got %s\nwant %s", zero, want)
	}
	// A configuration that never heard of worker nodes writes no execution
	// key at all, which is what keeps every existing file byte for byte what
	// it was.
	empty, err := json.Marshal(File{})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != "{}" {
		t.Errorf("a node with no execution settings must marshal no execution key: %s", empty)
	}
}
