package executionmeasurement

import (
	"encoding/json"
	"slices"
	"testing"
)

func setComparableIntervals(measurement *Measurement, origin float64) {
	for index := range measurement.TaskIntervals {
		start := origin
		end := start + *measurement.TaskIntervals[index].DurationSeconds
		measurement.TaskIntervals[index].StartSeconds, measurement.TaskIntervals[index].EndSeconds = &start, &end
		measurement.TaskIntervals[index].ClockDomain = "coordinator:monotonic"
	}
}

func TestModeledTimelineExcludesRetainedOmittedPhases(t *testing.T) {
	measurement := fixture(t)
	setComparableIntervals(&measurement, 0)
	extra := measurement.TaskIntervals[0]
	extra.Operation, extra.Boundary = "verify", "omitted executable verification"
	start, end, duration := 10.0, 11.0, 1.0
	extra.StartSeconds, extra.EndSeconds, extra.DurationSeconds = &start, &end, &duration
	measurement.TaskIntervals = append(measurement.TaskIntervals, extra)
	span, peak := 4.0, 2.0
	measurement.TimeWindows[1].Seconds, measurement.TimeWindows[1].Evidence, measurement.TimeWindows[1].ClockDomain = &span, measurement.Graph.Evidence, "coordinator:monotonic"
	measurement.ObservedPeakOverlap = Quantity{Value: &peak, Evidence: measurement.Graph.Evidence}
	deriveAndValidate(t, &measurement)
	if *measurement.Analysis[0].Value != 6 || *measurement.Analysis[2].Value != 4 {
		t.Fatal(measurement.Analysis)
	}
	span = 11
	body, _ := json.Marshal(measurement)
	if err := Validate(body); err == nil {
		t.Fatal("omitted phase widened the modeled task span")
	}
}

func TestComparableEvidenceOverridesDeclaredSchedulePremises(t *testing.T) {
	for _, origin := range []float64{0, 1e9} {
		measurement := fixture(t)
		setComparableIntervals(&measurement, origin)
		capacity := 1.0
		measurement.Resources.TaskSlotLimit.Value = &capacity
		deriveAndValidate(t, &measurement)
		if measurement.Analysis[2].Status != inapplicableStatus || !slices.Contains(measurement.Analysis[2].Reasons, capacityViolationReason) {
			t.Fatal(measurement.Analysis[2])
		}
		measurement = fixture(t)
		setComparableIntervals(&measurement, origin)
		start, end := origin+3.5, origin+5.5
		measurement.TaskIntervals[1].StartSeconds, measurement.TaskIntervals[1].EndSeconds = &start, &end
		measurement.Graph.Vertices = append(measurement.Graph.Vertices, Vertex{ID: "relay", Operation: "readiness", IsVirtual: true})
		measurement.Graph.Edges = []Edge{{"a", "relay", "build"}, {"relay", "b", "publish"}}
		deriveAndValidate(t, &measurement)
		if measurement.Analysis[2].Status != inapplicableStatus || !slices.Contains(measurement.Analysis[2].Reasons, precedenceViolationReason) {
			t.Fatal(measurement.Analysis[2])
		}
	}
}

func TestKnownViolationsTakePriorityOverMissingInputs(t *testing.T) {
	measurement := fixture(t)
	measurement.TaskIntervals[0].DurationSeconds, measurement.TaskIntervals[0].Reason = nil, "timing not retained"
	measurement.Trial.Status = "failed"
	delete(measurement.Premises, "precedenceValid")
	deriveAndValidate(t, &measurement)
	for _, index := range []int{2, 3} {
		calculation := measurement.Analysis[index]
		if calculation.Status != inapplicableStatus || !slices.Contains(calculation.Reasons, invalidScheduleReason) || !slices.Contains(calculation.Reasons, missingPrecedenceReason) || len(calculation.Reasons) < 3 {
			t.Fatal(calculation)
		}
	}
	measurement = fixture(t)
	delete(measurement.Premises, "precedenceValid")
	deriveAndValidate(t, &measurement)
	if measurement.Analysis[2].Status != unavailableStatus {
		t.Fatal(measurement.Analysis[2])
	}
}

func TestNominalMethodDoesNotUpgradeWallDurations(t *testing.T) {
	measurement := fixture(t)
	measurement.TaskIntervals[0].ClockKind = wallClock
	deriveAndValidate(t, &measurement)
	for _, calculation := range measurement.Analysis[:4] {
		if calculation.Value != nil || calculation.Status != inapplicableStatus {
			t.Fatal(calculation)
		}
	}
}

func TestPartialCoverageCannotHideMalformedIdentitiesOrReferences(t *testing.T) {
	for _, mutation := range []func(*Measurement){
		func(m *Measurement) {
			m.Graph.Vertices[0].DurationInputs = nil
			extra := m.TaskIntervals[0]
			extra.TaskID = "orphan"
			m.TaskIntervals = append(m.TaskIntervals, extra)
		},
		func(m *Measurement) {
			m.TaskIntervals[0].DurationSeconds = nil
			m.Graph.Vertices[0].DurationInputs = append(m.Graph.Vertices[0].DurationInputs, measurementReference("/taskIntervals/999/durationSeconds"))
		},
	} {
		measurement := fixture(t)
		mutation(&measurement)
		if err := Derive(&measurement); err == nil {
			t.Fatal("partial coverage hid malformed identity/reference")
		}
	}
}

func TestMeasuredTaskSpanRetainsItsActualClockDomain(t *testing.T) {
	measurement := fixture(t)
	setComparableIntervals(&measurement, 0)
	span := 4.0
	measurement.TimeWindows[1].Seconds, measurement.TimeWindows[1].Evidence, measurement.TimeWindows[1].ClockDomain = &span, measurement.Graph.Evidence, "host-b:monotonic"
	body, _ := json.Marshal(measurement)
	if err := Validate(body); err == nil {
		t.Fatal("span was relabeled with another clock domain")
	}
}

func TestReaderAcceptsEitherAttainingCriticalPath(t *testing.T) {
	measurement := fixture(t)
	duration := 4.0
	measurement.TaskIntervals[1].DurationSeconds = &duration
	deriveAndValidate(t, &measurement)
	measurement.Analysis[1].Path = []string{"b"}
	body, _ := json.Marshal(measurement)
	if err := Validate(body); err != nil {
		t.Fatal(err)
	}
}

func TestSameClockContainmentContradictionDisablesOnlyRatio(t *testing.T) {
	measurement := fixture(t)
	setComparableIntervals(&measurement, 0)
	seconds := 3.0
	measurement.TimeWindows[0].Seconds = &seconds
	deriveAndValidate(t, &measurement)
	for index, expected := range []float64{6, 4, 4} {
		if measurement.Analysis[index].Value == nil || *measurement.Analysis[index].Value != expected {
			t.Fatal(measurement.Analysis[index])
		}
	}
	if measurement.Analysis[3].Value != nil || measurement.Analysis[3].Status != inapplicableStatus || !slices.Contains(measurement.Analysis[3].Reasons, containmentViolationReason) {
		t.Fatal(measurement.Analysis[3])
	}
	measurement = fixture(t)
	measurement.TimeWindows[0].Seconds = &seconds
	deriveAndValidate(t, &measurement)
	if measurement.Analysis[3].Value == nil || *measurement.Analysis[3].Value != .75 {
		t.Fatal("unmapped nominal ratio below one was treated as a proved clock contradiction")
	}
}

func TestOverlappingPhasesDoNotInventAdditionalOccupiedSlots(t *testing.T) {
	measurement := fixture(t)
	measurement.Graph.Vertices = measurement.Graph.Vertices[:1]
	measurement.TaskIntervals = measurement.TaskIntervals[:1]
	setComparableIntervals(&measurement, 0)
	phase := measurement.TaskIntervals[0]
	phase.Operation = "nested-phase"
	start, end, seconds := 1.0, 3.0, 2.0
	phase.StartSeconds, phase.EndSeconds, phase.DurationSeconds = &start, &end, &seconds
	measurement.TaskIntervals = append(measurement.TaskIntervals, phase)
	measurement.Graph.Vertices[0].DurationRule = sumDisjointRule
	measurement.Graph.Vertices[0].DurationInputs = append(measurement.Graph.Vertices[0].DurationInputs, measurementReference("/taskIntervals/1/durationSeconds"))
	capacity := 1.0
	measurement.Resources.TaskSlotLimit.Value = &capacity
	deriveAndValidate(t, &measurement)
	if measurement.Analysis[0].Status != inapplicableStatus || measurement.Analysis[4].Status != inapplicableStatus {
		t.Fatal(measurement.Analysis)
	}
	if slices.Contains(measurement.Analysis[2].Reasons, capacityViolationReason) {
		t.Fatal("one attempt's overlapping phase diagnostics were counted as two slots")
	}
}

func TestKnownEmptyListsNormalizeWithoutReplacingUnknownNumbers(t *testing.T) {
	measurement := fixture(t)
	measurement.Resources.UsableOnePoolSlots.Evidence = nil
	measurement.TimeWindows[1].Evidence = nil
	deriveAndValidate(t, &measurement)
	if measurement.Resources.UsableOnePoolSlots.Evidence == nil || measurement.TimeWindows[1].Evidence == nil || measurement.Resources.UsableOnePoolSlots.Value != nil || measurement.TimeWindows[1].Seconds != nil {
		t.Fatal("empty records and unknown numbers were conflated")
	}
}
