package executionmeasurement

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	invalidMeasurementFormat = "execution measurement %s: %s"
	invalidIdentityReason    = "missing or inconsistent identity, provenance, or evidence"
	invalidQuantityReason    = "requires a finite nonnegative value with evidence, or null with a reason"
	invalidCapacityReason    = "task capacity must be a positive integer"
	invalidClockReason       = "requires an identified monotonic, wall, or bounded-mapping clock"
	invalidIntervalReason    = "endpoints and duration are inconsistent"
	invalidCountsReason      = "counts do not match the retained graph and complete duration coverage"
	invalidAnalysisReason    = "calculation differs from recomputation of its recorded inputs or applicability"
	comparisonTolerance      = 1e-9
	monotonicClock           = "monotonic"
	wallClock                = "wall"
	boundedMappingClock      = "bounded-mapping"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Validate checks a typed copy while callers retain the original JSON, including extra evidence fields.
func Validate(body json.RawMessage) error {
	var measurement Measurement
	if err := json.Unmarshal(body, &measurement); err != nil {
		return err
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return err
	}
	if err := validateStructure(&measurement, root); err != nil {
		return err
	}
	expected := measurement
	if err := Derive(&expected); err != nil {
		return err
	}
	if expected.Graph.Counts != measurement.Graph.Counts {
		return fmt.Errorf(invalidMeasurementFormat, "graph.counts", invalidCountsReason)
	}
	if len(expected.Analysis) != len(measurement.Analysis) {
		return fmt.Errorf(invalidMeasurementFormat, "analysis", invalidAnalysisReason)
	}
	for index, calculation := range expected.Analysis {
		actual := measurement.Analysis[index]
		if !equalCalculation(calculation, actual) {
			return fmt.Errorf(invalidMeasurementFormat, actual.ID, invalidAnalysisReason)
		}
		if actual.ID == nominalCriticalPathID {
			if err := validateAttainingPath(&measurement, actual); err != nil {
				return err
			}
		}
	}
	return nil
}

func equalCalculation(expected, actual Calculation) bool {
	if expected.ID != actual.ID || expected.Formula != actual.Formula || expected.Status != actual.Status || expected.Unit != actual.Unit || expected.Model != actual.Model || expected.ClockMethod != actual.ClockMethod || !slices.Equal(expected.Reasons, actual.Reasons) || (expected.ID != nominalCriticalPathID && !slices.Equal(expected.Path, actual.Path)) || len(expected.Inputs) != len(actual.Inputs) {
		return false
	}
	for symbol, reference := range expected.Inputs {
		if actual.Inputs[symbol] != reference {
			return false
		}
	}
	if expected.Value == nil || actual.Value == nil {
		return expected.Value == nil && actual.Value == nil
	}
	return nearlyEqual(*expected.Value, *actual.Value)
}

func nearlyEqual(left, right float64) bool {
	return isFiniteNonnegative(left) && isFiniteNonnegative(right) && math.Abs(left-right) <= comparisonTolerance*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}

func validateStructure(measurement *Measurement, root any) error {
	if err := validateRecordShape(measurement, root); err != nil {
		return err
	}
	if value, exists := measurement.Premises["precedenceValid"]; exists && value != nil {
		isValid, isBoolean := value.(bool)
		reason, _ := measurement.Premises["precedenceReason"].(string)
		if !isBoolean || (!isValid && strings.TrimSpace(reason) == "") {
			return fmt.Errorf(invalidMeasurementFormat, "premises.precedenceValid", invalidIdentityReason)
		}
	}
	trial := measurement.Trial
	if measurement.Schema != Schema || trial.ID == "" || trial.Repetition < 1 || trial.Tool == "" || trial.Mode == "" || trial.Profile == "" || trial.Workload == "" || trial.SourceRevision == "" || trial.ProtocolRevision == "" || !digestPattern.MatchString(trial.ConfigurationSHA256) || !digestPattern.MatchString(trial.BinarySHA256) || len(trial.Toolchain) == 0 || !slices.Contains([]string{completedStatus, "failed", "timeout", "blocked", "unsupported"}, trial.Status) {
		return fmt.Errorf(invalidMeasurementFormat, "trial", invalidIdentityReason)
	}
	evidence := map[string]bool{}
	for _, record := range trial.Evidence {
		if record.ID == "" || evidence[record.ID] || (record.Path == "" && record.URI == "") || !digestPattern.MatchString(record.SHA256) {
			return fmt.Errorf(invalidMeasurementFormat, "trial.evidence", invalidIdentityReason)
		}
		evidence[record.ID] = true
	}
	if len(evidence) == 0 || measurement.Graph.ID == "" || measurement.Graph.Provenance == "" || len(measurement.Graph.Evidence) == 0 || !slices.Contains([]string{"native-task-graph", "analysis-projection", "fixture-workload"}, measurement.Graph.Scope) {
		return fmt.Errorf(invalidMeasurementFormat, "graph", invalidIdentityReason)
	}
	references := slices.Clone(measurement.Graph.Evidence)
	for _, vertex := range measurement.Graph.Vertices {
		references = append(references, vertex.DurationInputs...)
	}
	nodes := map[string]bool{}
	for _, node := range measurement.Resources.Nodes {
		identity, _ := node["id"].(string)
		_, hasHost := node["hostId"]
		if identity == "" || nodes[identity] || !hasHost {
			return fmt.Errorf(invalidMeasurementFormat, "resources.nodes", invalidIdentityReason)
		}
		nodes[identity] = true
	}
	for index, quantity := range []Quantity{measurement.Resources.TaskSlotLimit, measurement.Resources.UsableOnePoolSlots, measurement.ObservedPeakOverlap} {
		if err := validateQuantity(quantity); err != nil {
			return err
		}
		references = append(references, quantity.Evidence...)
		if quantity.Value != nil && ((index < 2 && *quantity.Value < 1) || math.Trunc(*quantity.Value) != *quantity.Value) {
			return fmt.Errorf(invalidMeasurementFormat, "capacity/overlap", invalidCapacityReason)
		}
	}
	intervalIDs := map[string]bool{}
	attemptNodes := map[string]string{}
	for _, interval := range measurement.TaskIntervals {
		identity := interval.TaskID + "\x00" + interval.AttemptID + "\x00" + interval.Operation
		if interval.TaskID == "" || interval.AttemptID == "" || !nodes[interval.NodeID] || interval.Operation == "" || interval.Boundary == "" || interval.Status == "" || intervalIDs[identity] {
			return fmt.Errorf(invalidMeasurementFormat, "taskIntervals", invalidIdentityReason)
		}
		intervalIDs[identity] = true
		attemptID := interval.TaskID + "\x00" + interval.AttemptID
		if previous, exists := attemptNodes[attemptID]; exists && previous != interval.NodeID {
			return fmt.Errorf(invalidMeasurementFormat, attemptID, invalidIdentityReason)
		}
		attemptNodes[attemptID] = interval.NodeID
		if err := validateQuantity(Quantity{Value: interval.DurationSeconds, Evidence: interval.Evidence, Reason: interval.Reason}); err != nil {
			return err
		}
		if err := validateClock(interval.ClockDomain, interval.ClockKind); err != nil {
			return err
		}
		if (interval.StartSeconds == nil) != (interval.EndSeconds == nil) {
			return fmt.Errorf(invalidMeasurementFormat, interval.TaskID, invalidIntervalReason)
		}
		if interval.StartSeconds != nil && (math.IsNaN(*interval.StartSeconds) || math.IsInf(*interval.StartSeconds, 0) || math.IsNaN(*interval.EndSeconds) || math.IsInf(*interval.EndSeconds, 0) || *interval.EndSeconds < *interval.StartSeconds || interval.DurationSeconds == nil || !nearlyEqual(*interval.EndSeconds-*interval.StartSeconds, *interval.DurationSeconds)) {
			return fmt.Errorf(invalidMeasurementFormat, interval.TaskID, invalidIntervalReason)
		}
		references = append(references, interval.Evidence...)
	}
	windows := map[string]bool{}
	for _, window := range measurement.TimeWindows {
		if window.ID == "" || windows[window.ID] || window.Boundary == "" {
			return fmt.Errorf(invalidMeasurementFormat, "timeWindows", invalidIdentityReason)
		}
		windows[window.ID] = true
		if err := validateQuantity(Quantity{Value: window.Seconds, Evidence: window.Evidence, Reason: window.Reason}); err != nil {
			return err
		}
		if err := validateClock(window.ClockDomain, window.ClockKind); err != nil {
			return err
		}
		if window.ID == "step-sum" && (len(window.IncludedSteps) == 0 || window.Aggregation == "") {
			return fmt.Errorf(invalidMeasurementFormat, window.ID, invalidIdentityReason)
		}
		references = append(references, window.Evidence...)
		references = append(references, window.ContainmentProof...)
	}
	for _, calculation := range measurement.Analysis {
		for _, reference := range calculation.Inputs {
			references = append(references, reference)
		}
	}
	for _, container := range []map[string]any{measurement.Premises, measurement.ClockCompatibility} {
		body, err := json.Marshal(container["evidence"])
		if err != nil {
			return err
		}
		var extra []Reference
		if err := json.Unmarshal(body, &extra); err != nil {
			return err
		}
		references = append(references, extra...)
	}
	for _, reference := range references {
		if err := validateReference(root, reference, evidence); err != nil {
			return err
		}
	}
	return validateComparableTimeline(measurement)
}

func validateRecordShape(measurement *Measurement, root any) error {
	arrays := []string{"/trial/evidence", "/graph/vertices", "/graph/edges", "/graph/omittedWork", "/graph/evidence", "/resources/nodes", "/resources/pools", "/resources/nested", "/resources/taskSlotLimit/evidence", "/resources/usableOnePoolSlots/evidence", "/taskIntervals", "/timeWindows", "/observedPeakOverlap/evidence", "/analysis", "/clockCompatibility/evidence"}
	values := []string{"/resources/taskSlotLimit/value", "/resources/usableOnePoolSlots/value", "/observedPeakOverlap/value", "/graph/counts/vertices", "/graph/counts/edges", "/graph/counts/virtualVertices", "/graph/counts/timedVertices"}
	for index := range measurement.Graph.Vertices {
		prefix := "/graph/vertices/" + strconv.Itoa(index)
		arrays = append(arrays, prefix+"/durationInputs")
		value, err := resolve(root, prefix+"/isVirtual")
		if err != nil {
			return err
		}
		if _, isBoolean := value.(bool); !isBoolean {
			return fmt.Errorf(invalidMeasurementFormat, prefix, invalidIdentityReason)
		}
	}
	for index := range measurement.TaskIntervals {
		prefix := "/taskIntervals/" + strconv.Itoa(index)
		arrays = append(arrays, prefix+"/evidence")
		values = append(values, prefix+"/durationSeconds")
	}
	for index := range measurement.TimeWindows {
		prefix := "/timeWindows/" + strconv.Itoa(index)
		arrays = append(arrays, prefix+"/evidence")
		values = append(values, prefix+"/seconds")
	}
	for _, pointer := range arrays {
		value, err := resolve(root, pointer)
		if err != nil {
			return err
		}
		if _, isArray := value.([]any); !isArray {
			return fmt.Errorf(invalidMeasurementFormat, pointer, invalidIdentityReason)
		}
	}
	for _, pointer := range values {
		if _, err := resolve(root, pointer); err != nil {
			return err
		}
	}
	return nil
}

func validateAttainingPath(measurement *Measurement, calculation Calculation) error {
	if calculation.Value == nil || len(measurement.Graph.Vertices) == 0 {
		if len(calculation.Path) != 0 {
			return fmt.Errorf(invalidMeasurementFormat, calculation.ID, invalidAnalysisReason)
		}
		return nil
	}
	graph, err := deriveGraph(measurement)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	total := 0.0
	for index, identity := range calculation.Path {
		duration, exists := graph.durations[identity]
		if !exists || seen[identity] {
			return fmt.Errorf(invalidMeasurementFormat, calculation.ID, invalidAnalysisReason)
		}
		seen[identity] = true
		if index > 0 {
			isConnected := false
			for _, edge := range measurement.Graph.Edges {
				if edge.From == calculation.Path[index-1] && edge.To == identity {
					isConnected = true
					break
				}
			}
			if !isConnected {
				return fmt.Errorf(invalidMeasurementFormat, calculation.ID, invalidAnalysisReason)
			}
		}
		total += duration
	}
	if len(calculation.Path) == 0 || !nearlyEqual(total, *calculation.Value) {
		return fmt.Errorf(invalidMeasurementFormat, calculation.ID, invalidAnalysisReason)
	}
	return nil
}

func validateQuantity(quantity Quantity) error {
	if quantity.Value == nil {
		if quantity.Reason != "" {
			return nil
		}
		return fmt.Errorf(invalidMeasurementFormat, "quantity", invalidQuantityReason)
	}
	if !isFiniteNonnegative(*quantity.Value) || len(quantity.Evidence) == 0 {
		return fmt.Errorf(invalidMeasurementFormat, "quantity", invalidQuantityReason)
	}
	return nil
}

func validateClock(domain, kind string) error {
	if domain == "" || !slices.Contains([]string{monotonicClock, wallClock, boundedMappingClock}, kind) {
		return fmt.Errorf(invalidMeasurementFormat, "clock", invalidClockReason)
	}
	return nil
}
