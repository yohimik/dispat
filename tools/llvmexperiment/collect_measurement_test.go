package main

import (
	"path/filepath"
	"strings"
	"testing"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

func TestObservedMeasurementRejectsInlineTaskSubstitutionWithUnchangedRawEvidence(t *testing.T) {
	mutations := map[string]func(*measurement.TaskInterval){
		"duration": func(value *measurement.TaskInterval) {
			seconds := 1.0
			value.DurationSeconds, value.EndSeconds = &seconds, &seconds
		},
		"start":        func(value *measurement.TaskInterval) { seconds := 1.0; value.StartSeconds = &seconds },
		"task":         func(value *measurement.TaskInterval) { value.TaskID = "other-task" },
		"attempt":      func(value *measurement.TaskInterval) { value.AttemptID = "other-attempt" },
		"node":         func(value *measurement.TaskInterval) { value.NodeID = "other-node" },
		"status":       func(value *measurement.TaskInterval) { value.Status = failedStatus },
		"operation":    func(value *measurement.TaskInterval) { value.Operation = "other-operation" },
		"boundary":     func(value *measurement.TaskInterval) { value.Boundary = "other-boundary" },
		"clock-domain": func(value *measurement.TaskInterval) { value.ClockDomain = "other-clock" },
		"clock-kind":   func(value *measurement.TaskInterval) { value.ClockKind = wallClockKind },
		"reason":       func(value *measurement.TaskInterval) { value.Reason = "unobserved reason" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			root, record := observedMeasurementFixture(t)
			mutate(&record.TaskIntervals[0])
			if err := verifyRetainedEvidence(root, record); err != nil {
				t.Fatal("raw fixture unexpectedly changed:", err)
			}
			if err := verifyObservedMeasurement(root, record); err == nil {
				t.Fatalf("inline %s substitution was accepted over unchanged terminal evidence", name)
			}
		})
	}
}

func TestObservedMeasurementRejectsInlineEnclosingWindowSubstitution(t *testing.T) {
	mutations := map[string]func(*measurement.TimeWindow){
		"seconds":      func(value *measurement.TimeWindow) { seconds := 1.0; value.Seconds = &seconds },
		"clock-domain": func(value *measurement.TimeWindow) { value.ClockDomain = "other-clock" },
		"clock-kind":   func(value *measurement.TimeWindow) { value.ClockKind = wallClockKind },
		"boundary":     func(value *measurement.TimeWindow) { value.Boundary = "other-boundary" },
		"tasks":        func(value *measurement.TimeWindow) { value.ContainsTasks = []string{"a"} },
		"containment":  func(value *measurement.TimeWindow) { value.ContainmentProof = nil },
		"evidence":     func(value *measurement.TimeWindow) { value.Evidence[0].Pointer = "/unobserved" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			root, record := observedMeasurementFixture(t)
			mutate(&record.TimeWindows[0])
			if err := verifyRetainedEvidence(root, record); err != nil {
				t.Fatal("raw fixture unexpectedly changed:", err)
			}
			if err := verifyObservedMeasurement(root, record); err == nil {
				t.Fatalf("inline %s substitution was accepted over unchanged coordinator evidence", name)
			}
		})
	}
}

func observedMeasurementFixture(t *testing.T) (string, *measurement.Measurement) {
	t.Helper()
	settings := campaignOptions{Input: t.TempDir(), Output: filepath.Join(t.TempDir(), "report"), Source: strings.Repeat("c", 40), Profile: fullProfile}
	root := writeCampaignFixture(t, settings, campaignCells()[0])
	var record verdict
	if err := readHardwareJSON(filepath.Join(root, "verdict.json"), &record); err != nil {
		t.Fatal(err)
	}
	if err := verifyObservedMeasurement(root, record.ExecutionMeasurement); err != nil {
		t.Fatal(err)
	}
	return root, record.ExecutionMeasurement
}
