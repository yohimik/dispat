package executionmeasurement

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

const (
	nominalClockMethod              = "nominal-seconds"
	descriptiveModel                = "descriptive"
	computedStatus                  = "computed"
	unavailableStatus               = "unavailable"
	inapplicableStatus              = "inapplicable"
	dimensionlessUnit               = "dimensionless"
	nominalWorkID                   = "nominal-work"
	nominalCriticalPathID           = "nominal-critical-path"
	nominalSlotBoundID              = "nominal-slot-bound"
	nominalEnclosingRatioID         = "nominal-enclosing-ratio"
	upperBoundID                    = "one-pool-upper-bound"
	workFormula                     = "W_nom = sum_i d_i"
	pathFormula                     = "F_i = d_i + max_{j -> i} F_j; L_nom = max_i F_i"
	slotFormula                     = "B_nom = max(L_nom, W_nom / s)"
	ratioFormula                    = "R_nom = T_enclosing / B_nom"
	upperFormula                    = "U = W/s + (1 - 1/s)*L"
	missingCapacityReason           = "no evidenced unit-task concurrency upper bound"
	invalidScheduleReason           = "failed or repeated attempts do not establish a failure-free modeled schedule"
	missingScheduleReason           = "successful completion of the modeled schedule is not established"
	missingPrecedenceReason         = "modeled precedence has not been verified"
	violatedPrecedenceReason        = "retained modeled precedence is violated"
	nonMonotonicDurationReason      = "nominal duration arithmetic requires local monotonic elapsed measurements"
	missingContainmentReason        = "no named enclosing interval with complete task-containment evidence"
	zeroDenominatorReason           = "the selected bound is zero; division is undefined"
	nominalOnlyUpperReason          = "one-pool upper-bound evaluation is unsupported by this nominal reporting profile"
	missingOnePoolReason            = "fixed available interchangeable slots and all CCME §13.11 clause 2 premises are not established"
	invalidClockCompatibilityReason = "nominal clock method requires actual domain identities, explicit assumptions and unknown relative-rate uncertainty"
	containmentViolationReason      = "comparable modeled task intervals cannot fit inside the declared enclosing window"
)

// Derive uses retained per-task inputs. It does not infer resources from observed concurrency.
func Derive(measurement *Measurement) error {
	normalizeEmptyRecords(measurement)
	if err := validateNominalClocks(measurement); err != nil {
		return err
	}
	graph, err := deriveGraph(measurement)
	if err != nil {
		return err
	}
	measurement.Graph.Counts = graph.counts
	durationInputs := map[string]Reference{}
	for index, vertex := range measurement.Graph.Vertices {
		durationInputs[vertex.ID] = measurementReference("/graph/vertices/" + strconv.Itoa(index) + "/durationInputs")
	}
	work := newCalculation(nominalWorkID, workFormula, durationInputs)
	critical := newCalculation(nominalCriticalPathID, pathFormula, map[string]Reference{"d": measurementReference("/graph/vertices"), "E": measurementReference("/graph/edges")})
	if len(graph.violations) > 0 {
		work.Status, critical.Status = inapplicableStatus, inapplicableStatus
		work.Reasons = append(slices.Clone(graph.violations), graph.reasons...)
		critical.Reasons = slices.Clone(work.Reasons)
	} else if !hasMonotonicDurations(measurement) {
		work.Status, critical.Status = inapplicableStatus, inapplicableStatus
		work.Reasons = append([]string{nonMonotonicDurationReason}, graph.reasons...)
		critical.Reasons = slices.Clone(work.Reasons)
	} else if len(graph.reasons) != 0 {
		work.Reasons, critical.Reasons = slices.Clone(graph.reasons), slices.Clone(graph.reasons)
	} else {
		setValue(&work, graph.work)
		setValue(&critical, graph.criticalPath)
		critical.Path = graph.path
	}
	slot := newCalculation(nominalSlotBoundID, slotFormula, map[string]Reference{"W_nom": measurementReference("/analysis/0/value"), "L_nom": measurementReference("/analysis/1/value"), "s": measurementReference("/resources/taskSlotLimit/value")})
	deriveSlot(&slot, measurement, graph, work)
	ratio := newCalculation(nominalEnclosingRatioID, ratioFormula, map[string]Reference{"B_nom": measurementReference("/analysis/2/value")})
	ratio.Unit = dimensionlessUnit
	deriveRatio(&ratio, measurement, slot)
	upper := newCalculation(upperBoundID, upperFormula, map[string]Reference{"W": measurementReference("/analysis/0/value"), "L": measurementReference("/analysis/1/value"), "s": measurementReference("/resources/usableOnePoolSlots/value")})
	upper.Status, upper.Reasons = unavailableStatus, []string{nominalOnlyUpperReason, missingOnePoolReason}
	if graph.schedulePremise == scheduleMissing {
		upper.Reasons = append(upper.Reasons, missingScheduleReason)
	}
	knownViolations := slices.Clone(graph.violations)
	if graph.schedulePremise == scheduleViolated {
		knownViolations = append(knownViolations, invalidScheduleReason)
	}
	if !hasMonotonicDurations(measurement) {
		knownViolations = append(knownViolations, nonMonotonicDurationReason)
	}
	knownViolations = append(knownViolations, findScheduleViolations(measurement, graph.order)...)
	if checkPrecedenceEvidence(measurement) == inapplicableStatus {
		knownViolations = append(knownViolations, violatedPrecedenceReason)
	}
	if len(knownViolations) > 0 {
		upper.Status, upper.Reasons = inapplicableStatus, append(knownViolations, upper.Reasons...)
	}
	measurement.Analysis = []Calculation{work, critical, slot, ratio, upper}
	for _, calculation := range measurement.Analysis {
		if calculation.Value != nil && !isFiniteNonnegative(*calculation.Value) {
			return fmt.Errorf(invalidMeasurementFormat, calculation.ID, invalidQuantityReason)
		}
	}
	return nil
}

func newCalculation(identity, formula string, inputs map[string]Reference) Calculation {
	return Calculation{ID: identity, Formula: formula, Inputs: inputs, Unit: nominalClockMethod, Status: unavailableStatus, Model: descriptiveModel, ClockMethod: nominalClockMethod, Reasons: []string{}}
}

func setValue(calculation *Calculation, value float64) {
	calculation.Value, calculation.Status, calculation.Reasons = &value, computedStatus, []string{}
}

func deriveSlot(slot *Calculation, measurement *Measurement, graph graphValues, work Calculation) {
	violations := findScheduleViolations(measurement, graph.order)
	missing := []string{}
	if work.Status == inapplicableStatus {
		violations = append(violations, work.Reasons...)
	} else if work.Value == nil {
		missing = append(missing, work.Reasons...)
	}
	switch graph.schedulePremise {
	case scheduleViolated:
		violations = append(violations, invalidScheduleReason)
	case scheduleMissing:
		missing = append(missing, missingScheduleReason)
	}
	switch checkPrecedenceEvidence(measurement) {
	case inapplicableStatus:
		violations = append(violations, violatedPrecedenceReason)
	case unavailableStatus:
		missing = append(missing, missingPrecedenceReason)
	}
	if measurement.Resources.TaskSlotLimit.Value == nil {
		missing = append(missing, missingCapacityReason)
	}
	if len(violations) > 0 {
		slot.Status, slot.Reasons = inapplicableStatus, append(violations, missing...)
		return
	}
	if len(missing) > 0 {
		slot.Reasons = missing
		return
	}
	setValue(slot, math.Max(graph.criticalPath, graph.work / *measurement.Resources.TaskSlotLimit.Value))
}

func deriveRatio(ratio *Calculation, measurement *Measurement, slot Calculation) {
	windowIndex := findEnclosingWindow(measurement)
	if windowIndex >= 0 {
		ratio.Inputs["T_enclosing"] = measurementReference("/timeWindows/" + strconv.Itoa(windowIndex) + "/seconds")
	}
	if slot.Value == nil {
		ratio.Status, ratio.Reasons = slot.Status, slices.Clone(slot.Reasons)
	}
	if windowIndex < 0 {
		ratio.Reasons = append(ratio.Reasons, missingContainmentReason)
	}
	if slot.Value != nil && *slot.Value == 0 {
		ratio.Status = inapplicableStatus
		ratio.Reasons = append(ratio.Reasons, zeroDenominatorReason)
	}
	if windowIndex >= 0 && measurement.TimeWindows[windowIndex].ClockKind != monotonicClock {
		ratio.Status = inapplicableStatus
		ratio.Reasons = append(ratio.Reasons, nonMonotonicDurationReason)
	}
	if windowIndex >= 0 && hasContainmentViolation(measurement, measurement.TimeWindows[windowIndex]) {
		ratio.Status = inapplicableStatus
		ratio.Reasons = append(ratio.Reasons, containmentViolationReason)
	}
	if len(ratio.Reasons) > 0 {
		return
	}
	setValue(ratio, *measurement.TimeWindows[windowIndex].Seconds / *slot.Value)
}

func checkPrecedenceEvidence(measurement *Measurement) string {
	isValid, isKnown := measurement.Premises["precedenceValid"].(bool)
	evidence, err := document(measurement.Premises["evidence"])
	records, _ := evidence.([]any)
	if !isKnown {
		return unavailableStatus
	}
	if !isValid {
		return inapplicableStatus
	}
	if err != nil || len(records) == 0 {
		return unavailableStatus
	}
	if !isValid {
		return inapplicableStatus
	}
	return computedStatus
}

func hasMonotonicDurations(measurement *Measurement) bool {
	for _, vertex := range measurement.Graph.Vertices {
		for _, reference := range vertex.DurationInputs {
			for index, interval := range measurement.TaskIntervals {
				if reference.Pointer == taskIntervalsPrefix+strconv.Itoa(index)+durationSuffix && interval.DurationSeconds != nil && interval.ClockKind != monotonicClock {
					return false
				}
			}
		}
	}
	return true
}

func findEnclosingWindow(measurement *Measurement) int {
	identity, _ := measurement.Premises["enclosingWindowId"].(string)
	for index, window := range measurement.TimeWindows {
		if window.ID != identity || window.Seconds == nil || len(window.ContainmentProof) == 0 {
			continue
		}
		isComplete := true
		for _, vertex := range measurement.Graph.Vertices {
			if !vertex.IsVirtual && !slices.Contains(window.ContainsTasks, vertex.ID) {
				isComplete = false
				break
			}
		}
		if isComplete {
			return index
		}
	}
	return -1
}

func validateNominalClocks(measurement *Measurement) error {
	root, err := document(measurement.ClockCompatibility)
	if err != nil {
		return err
	}
	clock, _ := root.(map[string]any)
	domains, _ := clock["domains"].([]any)
	assumptions, _ := clock["assumptions"].([]any)
	uncertainty, _ := clock["uncertainty"].(map[string]any)
	reason, _ := uncertainty["reason"].(string)
	relativeRate, hasRate := uncertainty["relativeRateErrorBound"]
	reference, _ := clock["referenceDomain"].(string)
	if clock["method"] != nominalClockMethod || reference == "" || !slices.Contains(domains, any(reference)) || len(assumptions) == 0 || !hasRate || relativeRate != nil || reason == "" {
		return fmt.Errorf(invalidMeasurementFormat, "clockCompatibility", invalidClockCompatibilityReason)
	}
	seenDomains := map[string]bool{}
	for _, value := range domains {
		domain, isString := value.(string)
		if !isString || strings.TrimSpace(domain) == "" || seenDomains[domain] {
			return fmt.Errorf(invalidMeasurementFormat, "clockCompatibility.domains", invalidClockCompatibilityReason)
		}
		seenDomains[domain] = true
	}
	for _, value := range assumptions {
		assumption, isString := value.(string)
		if !isString || strings.TrimSpace(assumption) == "" {
			return fmt.Errorf(invalidMeasurementFormat, "clockCompatibility.assumptions", invalidClockCompatibilityReason)
		}
	}
	for _, interval := range measurement.TaskIntervals {
		if !slices.Contains(domains, any(interval.ClockDomain)) {
			return fmt.Errorf(invalidMeasurementFormat, interval.TaskID, invalidClockCompatibilityReason)
		}
	}
	for _, window := range measurement.TimeWindows {
		if window.Seconds != nil && !slices.Contains(domains, any(window.ClockDomain)) {
			return fmt.Errorf(invalidMeasurementFormat, window.ID, invalidClockCompatibilityReason)
		}
	}
	return nil
}
