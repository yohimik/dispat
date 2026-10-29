package executionmeasurement

import (
	"slices"
	"testing"
)

func TestSchedulePremiseDistinguishesMissingFromFailedTrial(t *testing.T) {
	for _, scenario := range []struct {
		trial, want, reason string
	}{
		{"completed", unavailableStatus, "successful completion of the modeled schedule is not established"},
		{"blocked", unavailableStatus, "successful completion of the modeled schedule is not established"},
		{"unsupported", unavailableStatus, "successful completion of the modeled schedule is not established"},
		{"failed", inapplicableStatus, invalidScheduleReason},
		{"timeout", inapplicableStatus, invalidScheduleReason},
	} {
		t.Run(scenario.trial, func(t *testing.T) {
			measurement := fixture(t)
			measurement.Trial.Status = scenario.trial
			measurement.TaskIntervals = []TaskInterval{}
			for index := range measurement.Graph.Vertices {
				measurement.Graph.Vertices[index].DurationInputs = []Reference{}
			}
			deriveAndValidate(t, &measurement)
			for _, calculation := range measurement.Analysis[:2] {
				if calculation.Value != nil || calculation.Status != unavailableStatus {
					t.Fatalf("missing task durations invented descriptive arithmetic: %+v", calculation)
				}
			}
			for _, calculation := range measurement.Analysis[2:] {
				if calculation.Value != nil || calculation.Status != scenario.want || !slices.Contains(calculation.Reasons, scenario.reason) {
					t.Fatalf("wrong schedule applicability: %+v", calculation)
				}
				if scenario.want == unavailableStatus && slices.Contains(calculation.Reasons, invalidScheduleReason) {
					t.Fatalf("absence was converted to a failed attempt: %+v", calculation)
				}
			}
		})
	}
}

func TestSchedulePremiseTreatsOpenAndUnrecognizedIntervalsAsMissing(t *testing.T) {
	for _, status := range []string{"running", "unknown", "open", "canceled", "cancelled", "aborted"} {
		t.Run(status, func(t *testing.T) {
			measurement := fixture(t)
			measurement.TaskIntervals[0].Status = status
			measurement.TaskIntervals[0].DurationSeconds = nil
			measurement.TaskIntervals[0].Reason = "completion was not established"
			deriveAndValidate(t, &measurement)
			for _, calculation := range measurement.Analysis[2:] {
				if calculation.Value != nil || calculation.Status != unavailableStatus || !slices.Contains(calculation.Reasons, missingScheduleReason) || slices.Contains(calculation.Reasons, invalidScheduleReason) {
					t.Fatalf("unestablished interval outcome became a violation: %+v", calculation)
				}
			}
		})
	}
}

func TestSchedulePremisePreservesViolationWithOpenIntervals(t *testing.T) {
	for _, outcome := range []string{"failed", "timeout", "distinct unfinished attempt"} {
		for _, reverse := range []bool{false, true} {
			name := outcome + "/open-first"
			if reverse {
				name = outcome + "/open-last"
			}
			t.Run(name, func(t *testing.T) {
				measurement := fixture(t)
				open := measurement.TaskIntervals[0]
				open.Operation, open.Status, open.Reason, open.DurationSeconds = "diagnostic", "running", "unfinished phase", nil
				other := open
				other.Operation = "second-diagnostic"
				if outcome == "distinct unfinished attempt" {
					other.AttemptID = "second-attempt"
				} else {
					other.Status = outcome
				}
				if reverse {
					open, other = other, open
				}
				measurement.TaskIntervals = append(measurement.TaskIntervals, open, other)
				deriveAndValidate(t, &measurement)
				for _, calculation := range measurement.Analysis[2:] {
					if calculation.Value != nil || calculation.Status != inapplicableStatus || !slices.Contains(calculation.Reasons, invalidScheduleReason) || slices.Contains(calculation.Reasons, missingScheduleReason) {
						t.Fatalf("open evidence obscured a known failed or repeated attempt: %+v", calculation)
					}
				}
			})
		}
	}
}

func TestSchedulePremisePreservesDescriptiveWorkAndKnownViolations(t *testing.T) {
	for _, scenario := range []struct {
		name, want, reason string
		mutate             func(*Measurement)
	}{
		{"unsupported with measured durations", unavailableStatus, "successful completion of the modeled schedule is not established", func(m *Measurement) {
			m.Trial.Status = "unsupported"
		}},
		{"open diagnostic phase", unavailableStatus, "successful completion of the modeled schedule is not established", func(m *Measurement) {
			interval := m.TaskIntervals[0]
			interval.Operation, interval.Status, interval.Reason = "diagnostic", "running", "phase has no terminal evidence"
			interval.DurationSeconds = nil
			m.TaskIntervals = append(m.TaskIntervals, interval)
		}},
		{"failed trial", inapplicableStatus, invalidScheduleReason, func(m *Measurement) { m.Trial.Status = "failed" }},
		{"timed out trial", inapplicableStatus, invalidScheduleReason, func(m *Measurement) { m.Trial.Status = "timeout" }},
		{"completed retry", inapplicableStatus, invalidScheduleReason, func(m *Measurement) {
			interval := m.TaskIntervals[0]
			interval.AttemptID = "second-attempt"
			m.TaskIntervals = append(m.TaskIntervals, interval)
		}},
		{"capacity violation with missing completion", inapplicableStatus, capacityViolationReason, func(m *Measurement) {
			m.Trial.Status = "unsupported"
			setComparableIntervals(m, 0)
			capacity := 1.0
			m.Resources.TaskSlotLimit.Value = &capacity
		}},
		{"precedence violation with missing completion", inapplicableStatus, violatedPrecedenceReason, func(m *Measurement) {
			m.Trial.Status = "blocked"
			m.Premises["precedenceValid"], m.Premises["precedenceReason"] = false, "retained prerequisite violation"
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			measurement := fixture(t)
			scenario.mutate(&measurement)
			deriveAndValidate(t, &measurement)
			if measurement.Analysis[0].Value == nil || *measurement.Analysis[0].Value != 6 || measurement.Analysis[1].Value == nil || *measurement.Analysis[1].Value != 4 {
				t.Fatal("schedule eligibility changed W/L arithmetic", measurement.Analysis)
			}
			for _, calculation := range measurement.Analysis[2:] {
				if calculation.Value != nil || calculation.Status != scenario.want || !slices.Contains(calculation.Reasons, scenario.reason) {
					t.Fatalf("wrong schedule applicability: %+v", calculation)
				}
				if (scenario.reason == capacityViolationReason || scenario.reason == violatedPrecedenceReason) && !slices.Contains(calculation.Reasons, "successful completion of the modeled schedule is not established") {
					t.Fatalf("known violation discarded missing-premise reason: %+v", calculation)
				}
			}
		})
	}
}

func TestSchedulePremiseRecognizesOnlyExplicitFailureAndSuccessOutcomes(t *testing.T) {
	for _, status := range []string{"failed", "timeout", "completed", "success"} {
		t.Run(status, func(t *testing.T) {
			measurement := fixture(t)
			measurement.TaskIntervals[0].Status = status
			deriveAndValidate(t, &measurement)
			if status == "failed" || status == "timeout" {
				for _, calculation := range measurement.Analysis[2:] {
					if calculation.Value != nil || calculation.Status != inapplicableStatus || !slices.Contains(calculation.Reasons, invalidScheduleReason) {
						t.Fatalf("known failed outcome lost its violation: %+v", calculation)
					}
				}
				return
			}
			for index, expected := range []float64{6, 4, 4, 2} {
				if measurement.Analysis[index].Value == nil || *measurement.Analysis[index].Value != expected {
					t.Fatal("complete successful schedule lost unchanged arithmetic", measurement.Analysis)
				}
			}
		})
	}
}
