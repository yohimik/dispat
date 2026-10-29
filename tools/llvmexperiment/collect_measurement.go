package main

import (
	"fmt"
	"path/filepath"
	"reflect"

	measurement "github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	terminalIntervalsError   = "task %s reported intervals differ from its retained terminal manifest"
	terminalEvidenceError    = "task %s lacks one retained terminal manifest reference"
	coordinatorEvidenceError = "coordinator window differs from its retained measurement and containment evidence"
)

func verifyObservedMeasurement(root string, record *measurement.Measurement) error {
	evidence := map[string]measurement.Evidence{}
	for _, entry := range record.Trial.Evidence {
		evidence[entry.ID] = entry
	}
	seen := map[string]taskManifest{}
	for _, interval := range record.TaskIntervals {
		if len(interval.Evidence) != 1 || interval.Evidence[0].Source != "evidence" || interval.Evidence[0].Pointer != "" {
			return fmt.Errorf(terminalEvidenceError, interval.TaskID)
		}
		entry := evidence[interval.Evidence[0].EvidenceID]
		if filepath.Base(entry.Path) != taskFinishedFile {
			return fmt.Errorf(terminalEvidenceError, interval.TaskID)
		}
		manifest, exists := seen[entry.ID]
		if !exists {
			if err := readHardwareJSON(filepath.Join(root, entry.Path), &manifest); err != nil {
				return err
			}
			if err := bindTerminalIntervals(record.TaskIntervals, manifest); err != nil {
				return err
			}
			seen[entry.ID] = manifest
		}
		if manifest.Status != completedStatus || manifest.Task.Run != record.Trial.ID || manifest.Task.Name != interval.TaskID || manifest.AttemptID != interval.AttemptID || manifest.Node != interval.NodeID {
			return fmt.Errorf(terminalEvidenceError, interval.TaskID)
		}
	}
	return verifyCoordinatorWindow(root, record, evidence["coordinator-window"])
}

func verifyCoordinatorWindow(root string, record *measurement.Measurement, entry measurement.Evidence) error {
	if filepath.Base(entry.Path) != "coordinator-window.json" || record.Premises["enclosingWindowId"] != coordinatorWindowID {
		return fmt.Errorf(coordinatorEvidenceError)
	}
	var raw struct {
		Seconds                       *float64 `json:"seconds"`
		ClockDomain                   string   `json:"clockDomain"`
		ClockKind                     string   `json:"clockKind"`
		Boundary                      string   `json:"boundary"`
		ContainsTasks                 []string `json:"containsTasks"`
		StartedBeforePeerLaunch       bool     `json:"startedBeforePeerLaunch"`
		AllArtifactsVerifiedBeforeEnd bool     `json:"allArtifactsVerifiedBeforeEnd"`
	}
	if err := readHardwareJSON(filepath.Join(root, entry.Path), &raw); err != nil {
		return err
	}
	reference := measurement.Reference{Source: "evidence", EvidenceID: entry.ID}
	expected := measurement.TimeWindow{ID: coordinatorWindowID, Boundary: raw.Boundary, Seconds: raw.Seconds, ClockDomain: raw.ClockDomain,
		ClockKind: raw.ClockKind, Evidence: []measurement.Reference{reference}, ContainsTasks: raw.ContainsTasks, ContainmentProof: []measurement.Reference{reference}}
	count := 0
	for _, window := range record.TimeWindows {
		if window.ID == coordinatorWindowID {
			count++
			if !reflect.DeepEqual(window, expected) || !raw.StartedBeforePeerLaunch || !raw.AllArtifactsVerifiedBeforeEnd {
				return fmt.Errorf(coordinatorEvidenceError)
			}
		}
	}
	if count != 1 {
		return fmt.Errorf(coordinatorEvidenceError)
	}
	return nil
}

func bindTerminalIntervals(intervals []measurement.TaskInterval, manifest taskManifest) error {
	expected := map[string]measurement.TaskInterval{}
	for _, interval := range append([]measurement.TaskInterval{manifest.Interval}, manifest.Diagnostics...) {
		if _, exists := expected[interval.Operation]; exists {
			return fmt.Errorf(terminalIntervalsError, manifest.Task.Name)
		}
		interval.Evidence = nil
		expected[interval.Operation] = interval
	}
	seen := map[string]bool{}
	for _, interval := range intervals {
		if interval.TaskID != manifest.Task.Name || interval.AttemptID != manifest.AttemptID {
			continue
		}
		interval.Evidence = nil
		if seen[interval.Operation] || !reflect.DeepEqual(expected[interval.Operation], interval) {
			return fmt.Errorf(terminalIntervalsError, manifest.Task.Name)
		}
		seen[interval.Operation] = true
	}
	if len(expected) != len(seen) {
		return fmt.Errorf(terminalIntervalsError, manifest.Task.Name)
	}
	return nil
}
