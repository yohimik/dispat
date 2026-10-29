package executionmeasurement

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) Measurement {
	t.Helper()
	body, err := os.ReadFile("../../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(body); err != nil {
		t.Fatal(err)
	}
	var result Measurement
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func deriveAndValidate(t *testing.T, measurement *Measurement) {
	t.Helper()
	if err := Derive(measurement); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(measurement)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(body); err != nil {
		t.Fatal(err)
	}
}

func TestNominalArithmeticUsesMeasuredDurationsAndCapacity(t *testing.T) {
	measurement := fixture(t)
	for index, expected := range []float64{6, 4, 4, 2} {
		if measurement.Analysis[index].Value == nil || *measurement.Analysis[index].Value != expected {
			t.Fatalf("calculation %d = %+v; want %v", index, measurement.Analysis[index], expected)
		}
	}
	measurement.Graph.Vertices = append(measurement.Graph.Vertices, Vertex{ID: "relay", Operation: "readiness", IsVirtual: true, DurationInputs: []Reference{}})
	measurement.Graph.Edges = []Edge{{From: "a", To: "relay", Kind: "build"}, {From: "relay", To: "b", Kind: "publish"}}
	deriveAndValidate(t, &measurement)
	if measurement.Graph.Counts != (GraphCounts{Vertices: 3, Edges: 2, VirtualVertices: 1, TimedVertices: 2}) {
		t.Fatal(measurement.Graph.Counts)
	}
	if *measurement.Analysis[1].Value != 6 || strings.Join(measurement.Analysis[1].Path, ",") != "a,relay,b" {
		t.Fatal(measurement.Analysis[1])
	}
	if *measurement.Analysis[3].Value != 8.0/6.0 {
		t.Fatal(measurement.Analysis[3])
	}
}

func TestMissingCoverageAndFailuresDisableScheduleClaims(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*Measurement)
		workKnown bool
		status    string
	}{
		{"missing duration", func(m *Measurement) {
			m.TaskIntervals[0].DurationSeconds = nil
			m.TaskIntervals[0].Reason = "task did not produce a record"
		}, false, unavailableStatus},
		{"failed trial", func(m *Measurement) { m.Trial.Status = "failed" }, true, inapplicableStatus},
		{"retry", func(m *Measurement) {
			extra := m.TaskIntervals[0]
			extra.AttemptID = "attempt-0"
			extra.Status = "failed"
			m.TaskIntervals = append(m.TaskIntervals, extra)
		}, true, inapplicableStatus},
		{"unknown capacity", func(m *Measurement) { m.Resources.TaskSlotLimit = Quantity{Reason: "no admitted cap evidence"} }, true, unavailableStatus},
		{"violated precedence", func(m *Measurement) {
			m.Premises["precedenceValid"] = false
			m.Premises["precedenceReason"] = "observed dependency violation"
		}, true, inapplicableStatus},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			measurement := fixture(t)
			testCase.mutate(&measurement)
			deriveAndValidate(t, &measurement)
			if (measurement.Analysis[0].Value != nil) != testCase.workKnown || measurement.Analysis[2].Value != nil || measurement.Analysis[2].Status != testCase.status || measurement.Analysis[3].Value != nil {
				t.Fatal(measurement.Analysis)
			}
		})
	}
}

func TestMeasurementReaderRejectsContradictoryEvidence(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Measurement)
	}{
		{"counts", func(m *Measurement) { m.Graph.Counts.Vertices++ }},
		{"cycle", func(m *Measurement) { m.Graph.Edges = []Edge{{"a", "b", "build"}, {"b", "a", "build"}} }},
		{"unknown edge", func(m *Measurement) { m.Graph.Edges = []Edge{{"a", "missing", "build"}} }},
		{"duplicate vertex", func(m *Measurement) { m.Graph.Vertices = append(m.Graph.Vertices, m.Graph.Vertices[0]) }},
		{"formula", func(m *Measurement) { wrong := 42.0; m.Analysis[0].Value = &wrong }},
		{"formula input", func(m *Measurement) { m.Analysis[2].Inputs["s"] = measurementReference("/observedPeakOverlap/value") }},
		{"clock method", func(m *Measurement) { m.Analysis[0].ClockMethod = "common-domain" }},
		{"unproved overlap", func(m *Measurement) {
			value := 2.0
			m.ObservedPeakOverlap = Quantity{Value: &value, Evidence: m.Graph.Evidence}
		}},
		{"invented clock accuracy", func(m *Measurement) {
			m.ClockCompatibility["uncertainty"] = map[string]any{"relativeRateErrorBound": 0, "reason": "assumed"}
		}},
		{"fractional task cap", func(m *Measurement) { value := 1.5; m.Resources.TaskSlotLimit.Value = &value }},
		{"missing evidence", func(m *Measurement) { m.Graph.Evidence[0].EvidenceID = "missing" }},
		{"negative duration", func(m *Measurement) { value := -1.0; m.TaskIntervals[0].DurationSeconds = &value }},
		{"false containment", func(m *Measurement) { m.TimeWindows[0].ContainsTasks = []string{"a"} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			measurement := fixture(t)
			testCase.mutate(&measurement)
			body, err := json.Marshal(measurement)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(body); err == nil {
				t.Fatal("contradictory evidence accepted")
			}
		})
	}
}

func TestEmptyGraphHasZeroWorkAndUndefinedRatio(t *testing.T) {
	measurement := fixture(t)
	measurement.Graph.Vertices, measurement.Graph.Edges, measurement.TaskIntervals = []Vertex{}, []Edge{}, []TaskInterval{}
	deriveAndValidate(t, &measurement)
	if *measurement.Analysis[0].Value != 0 || *measurement.Analysis[1].Value != 0 || measurement.Analysis[3].Value != nil || measurement.Analysis[3].Reasons[0] != zeroDenominatorReason || measurement.Analysis[3].Status != inapplicableStatus {
		t.Fatal(measurement.Analysis)
	}
}

func TestComparableTimelineChecksSpanAndPeak(t *testing.T) {
	measurement := fixture(t)
	for index, start := range []float64{0, 1} {
		end := start + *measurement.TaskIntervals[index].DurationSeconds
		measurement.TaskIntervals[index].StartSeconds, measurement.TaskIntervals[index].EndSeconds = &start, &end
		measurement.TaskIntervals[index].ClockDomain = "coordinator:monotonic"
	}
	span, peak := 4.0, 2.0
	measurement.TimeWindows[1].Seconds, measurement.TimeWindows[1].Evidence, measurement.TimeWindows[1].ClockDomain = &span, measurement.Graph.Evidence, "coordinator:monotonic"
	measurement.ObservedPeakOverlap = Quantity{Value: &peak, Evidence: measurement.Graph.Evidence}
	deriveAndValidate(t, &measurement)
	peak = 3
	body, _ := json.Marshal(measurement)
	if err := Validate(body); err == nil {
		t.Fatal("incorrect observed peak accepted")
	}
}
