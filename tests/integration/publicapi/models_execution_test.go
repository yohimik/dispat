package publicapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/models"
)

// A program generating worker configuration must preserve placement exactly:
// silently accepting a partial or misspelled pair could move a signing step.
func TestPublicAPIModelRunOnlyPreservesStagePlacement(t *testing.T) {
	for _, tc := range []struct {
		name, input, output, build, publish string
	}{
		{"one placement", `"orchestrator"`, `"orchestrator"`, "orchestrator", "orchestrator"},
		{"stage pair", `["worker","orchestrator"]`, `["worker","orchestrator"]`, "worker", "orchestrator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var placement models.RunOnly
			if err := json.Unmarshal([]byte(tc.input), &placement); err != nil {
				t.Fatalf("decode %s: %v", tc.input, err)
			}
			if placement.ResolveBuild() != tc.build || placement.ResolvePublish() != tc.publish {
				t.Fatalf("stage placement = %+v", placement)
			}
			encoded, err := json.Marshal(placement)
			if err != nil || string(encoded) != tc.output {
				t.Fatalf("JSON placement = %s, %v; want %s", encoded, err, tc.output)
			}
			yamlValue, err := placement.MarshalYAML()
			if err != nil {
				t.Fatalf("YAML placement: %v", err)
			}
			encoded, err = json.Marshal(yamlValue)
			if err != nil || string(encoded) != tc.output {
				t.Fatalf("YAML placement = %s, %v; want %s", encoded, err, tc.output)
			}
		})
	}
	var absent *models.RunOnly
	if absent.ResolveBuild() != models.RunOnlyBoth || absent.ResolvePublish() != models.RunOnlyBoth {
		t.Fatal("unstated placement did not permit either machine")
	}
	for _, tc := range []struct{ input, want string }{
		{`["worker"]`, "both stages"},
		{`["worker","orchestrator","both"]`, "both stages"},
		{`["worker",42]`, "runOnly[1]"},
		{`["Worker","orchestrator"]`, `runOnly[0]`},
		{`"local"`, "is not a placement"},
		{`42`, "wants"},
		{`{`, "unexpected end"},
	} {
		var placement models.RunOnly
		if err := json.Unmarshal([]byte(tc.input), &placement); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("decode %s = %v; want %q", tc.input, err, tc.want)
		}
	}
	if _, err := models.NormalizeRunOnly([]string{"worker", "orchestrator"}, "spaces[ops].runOnly"); err != nil {
		t.Fatalf("typed YAML pair: %v", err)
	}
}

// The public resolver fills only absent bounds. Invalid explicit numbers must
// remain visible to the loader so it can refuse them with the right field name.
func TestPublicAPIModelExecutionResolversPreserveExplicitBounds(t *testing.T) {
	var absent *models.ExecutionConfig
	if absent.ResolveRole() != models.ExecutionRoleOrchestrator || absent.IsWorker() || absent.IsDistributed() {
		t.Fatal("absent execution configuration changed local orchestration")
	}
	if absent.ResolveConcurrency() != models.DefaultExecutionConcurrency {
		t.Fatal("absent execution concurrency changed its default")
	}
	if got := absent.ResolveTimeouts(); got.Preflight != models.DefaultExecutionPreflightTimeout ||
		got.Task != models.DefaultExecutionTaskTimeout || got.Cancel != models.DefaultExecutionCancelTimeout {
		t.Fatalf("absent timeouts = %+v", got)
	}
	if got := absent.ResolveTransfer(); got.MaxFiles != models.DefaultExecutionMaxFiles ||
		got.MaxBytes != models.DefaultExecutionMaxBytes || got.MaxManifestBytes != models.DefaultExecutionMaxManifestBytes ||
		got.Timeout != models.DefaultExecutionTransferTimeout {
		t.Fatalf("absent transfer = %+v", got)
	}

	config := &models.ExecutionConfig{
		Role:        models.ExecutionRoleWorker,
		Concurrency: models.Int(-2),
		Workers:     []models.ExecutionWorkerConfig{{Name: "builder"}},
		Timeouts:    &models.ExecutionTimeoutsConfig{Preflight: 5, Task: -7},
		Transfer:    &models.ExecutionTransferConfig{MaxFiles: 8, MaxBytes: -9, Timeout: 11},
	}
	if !config.IsWorker() || !config.IsDistributed() || config.ResolveConcurrency() != -2 {
		t.Fatalf("explicit role, workers, or concurrency changed: %+v", config)
	}
	if got := config.ResolveTimeouts(); got.Preflight != 5 || got.Task != -7 ||
		got.Cancel != models.DefaultExecutionCancelTimeout {
		t.Fatalf("partial timeouts = %+v", got)
	}
	if got := config.ResolveTransfer(); got.MaxFiles != 8 || got.MaxBytes != -9 ||
		got.MaxManifestBytes != models.DefaultExecutionMaxManifestBytes || got.Timeout != 11 {
		t.Fatalf("partial transfer = %+v", got)
	}
}
