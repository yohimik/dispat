package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestMeasurementRetainsTwoRequestProjectionAndNominalEvidence(t *testing.T) {
	settings, err := parseOptions([]string{"--trial-id", "measurement-fixture", "--source", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	settings.Output = t.TempDir()
	result := initialResult(settings)
	result.BinarySHA256, result.ToolVersions = strings.Repeat("a", 64), map[string]string{"fixture": "test-only"}
	record, err := initializeMeasurement(settings, &result)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	data, err := prepareFixture(context.Background(), settings, work, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := retainFixture(settings, data, record); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for index, node := range data.Nodes {
		t.Setenv(taskEvidenceEnvironment, node.EvidenceDir)
		t.Setenv("DISPAT_EXECUTION_NODE", node.Name)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		recording, err := beginTaskRecording(ctx, data.Tasks[index], t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		recording.beginOperation()
		recording.endOperation()
		if err := recording.finish(nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := retainCoordinatorWindow(settings, record, started, true); err != nil {
		t.Fatal(err)
	}
	if err := retainTaskEvidence(settings, data, record, io.Discard); err != nil {
		t.Fatal(err)
	}
	record.Trial.Status = completedStatus
	attachDurationInputs(record)
	if err := measurement.Derive(record); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := measurement.Validate(encoded); err != nil {
		t.Fatalf("producer emitted invalid measurement: %v\n%s", err, encoded)
	}
	if record.Graph.Counts != (measurement.GraphCounts{Vertices: 2, TimedVertices: 2}) {
		t.Fatalf("wrong outer graph counts: %+v", record.Graph.Counts)
	}
	if record.Resources.TaskSlotLimit.Value == nil || *record.Resources.TaskSlotLimit.Value != 2 || record.Resources.UsableOnePoolSlots.Value != nil || record.ObservedPeakOverlap.Value != nil || record.TimeWindows[0].Seconds != nil {
		t.Fatal("nested jobs or unmapped clocks manufactured outer capacity or overlap")
	}
	for _, calculation := range record.Analysis {
		if calculation.ID == "one-pool-upper-bound" {
			if calculation.Status != "unavailable" || calculation.Value != nil || len(calculation.Reasons) == 0 {
				t.Fatal("one-pool guarantee was invented")
			}
			continue
		}
		if calculation.Status != "computed" || calculation.Model != "descriptive" {
			t.Fatalf("available nominal evidence was lost: %+v", calculation)
		}
	}
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range record.Trial.Evidence {
		if _, err := os.Stat(filepath.Join(settings.Output, evidence.Path)); err != nil {
			t.Fatalf("raw evidence missing after cleanup: %v", err)
		}
	}
}

func TestTaskRecordingPreservesFailedAndInterruptedAttempts(t *testing.T) {
	t.Setenv(taskEvidenceEnvironment, t.TempDir())
	t.Setenv("DISPAT_EXECUTION_NODE", "node-failure")
	settings, _ := parseOptions(nil)
	item := taskForBackend(settings, "failure-trial", "X86")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recording, err := beginTaskRecording(ctx, item, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started, err := os.ReadFile(filepath.Join(recording.Directory, taskStartedFile))
	if err != nil {
		t.Fatal(err)
	}
	recording.beginOperation()
	if err := recording.finish(errors.New("compiler failed")); err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(filepath.Join(recording.Directory, taskFinishedFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest taskManifest
	if err := json.Unmarshal(final, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != failedStatus || manifest.Interval.DurationSeconds == nil || manifest.Interval.ClockKind != monotonicClockKind || manifest.Reason != "compiler failed" {
		t.Fatalf("failed attempt lost its measured diagnostic: %s", final)
	}
	after, err := os.ReadFile(filepath.Join(recording.Directory, taskStartedFile))
	if err != nil || string(started) != string(after) {
		t.Fatal("terminal recording overwrote initial raw evidence")
	}
	if err := recording.finish(nil); err == nil {
		t.Fatal("finished attempt could be overwritten")
	}
}

func TestTrialAndRepetitionDirectoriesNeverOverwrite(t *testing.T) {
	settings, err := parseOptions([]string{"--tool", changesetsTool, "--mode", distributedMode, "--trial-id", "immutable", "--results", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := runExperiment(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(settings.Results, "llvm-full-distributed-changesets-immutable-r1", "verdict.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runExperiment(context.Background(), settings); err == nil {
		t.Fatal("duplicate trial overwrote previous evidence")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(first) != string(after) {
		t.Fatal("duplicate trial changed existing verdict")
	}
	settings.Repetition++
	if err := runExperiment(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(settings.Results)
	if err != nil || len(entries) != 2 {
		t.Fatalf("repetition did not get its own directory: %v", err)
	}
}
