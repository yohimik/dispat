package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	measurementPlainTitleFormat       = "\nRecorded execution measurement: %s\n"
	measurementPlainInputsText        = "  Captured inputs"
	measurementPlainTrialFormat       = "    Trial: %s / repetition %d / status %s\n"
	measurementPlainGraphFormat       = "    Graph: %s (%s); vertices %d; typed edges %d; virtual %d; timed %d\n"
	measurementPlainProvenanceFormat  = "    Graph provenance: %s\n    Omitted work: %s\n"
	measurementPlainCapacityFormat    = "    Task-slot ceiling: %s\n    Available one-pool slots: %s\n"
	measurementPlainResourcesFormat   = "    Resource records:\n%s\n"
	measurementPlainIntervalsText     = "  Measured intervals and outcomes"
	measurementPlainTaskFormat        = "    Task %s / attempt %s / executor %s / operation %s: %s seconds; boundary: %s; clock: %s (%s); outcome: %s; reason: %s\n"
	measurementPlainWindowFormat      = "    Window %s: %s seconds; boundary: %s; clock: %s (%s); aggregation: %s; reason: %s\n"
	measurementPlainOverlapFormat     = "    Observed peak overlap: %s\n"
	measurementPlainFormulaText       = "  Formula calculations"
	measurementPlainCalculationFormat = "    %s: %s\n      Selected inputs: %s\n      Value: %s %s; model/status: %s / %s; clock method: %s\n      Applicability: %s\n"
	measurementPlainClockFormat       = "  Clock assumptions and uncertainty:\n%s\n"
	measurementPlainRawFormat         = "  Complete retained inputs, resources, intervals and evidence references:\n%s\n"
	measurementQuantityReasonFormat   = "%s (%s)"
)

func writeMeasurementPlain(out *bufio.Writer, campaign Experiments) {
	for _, cell := range campaign.Cells {
		if len(cell.ExecutionMeasurement) == measurementAbsentBytes {
			continue
		}
		var measurement executionmeasurement.Measurement
		if err := decodeMeasurementDisplay(cell.ExecutionMeasurement, &measurement); err != nil {
			continue
		}
		fmt.Fprintf(out, measurementPlainTitleFormat, cell.ID)
		writeMeasurementInputsPlain(out, measurement)
		writeMeasurementIntervalsPlain(out, measurement)
		writeMeasurementCalculationsPlain(out, measurement)
		clock, _ := json.Marshal(measurement.ClockCompatibility)
		fmt.Fprintf(out, measurementPlainClockFormat, formatMeasurementJSON(clock))
		fmt.Fprintf(out, measurementPlainRawFormat, formatMeasurementJSON(cell.ExecutionMeasurement))
	}
}

func writeMeasurementInputsPlain(out *bufio.Writer, measurement executionmeasurement.Measurement) {
	fmt.Fprintln(out, measurementPlainInputsText)
	fmt.Fprintf(out, measurementPlainTrialFormat, measurement.Trial.ID, measurement.Trial.Repetition, measurement.Trial.Status)
	counts := measurement.Graph.Counts
	fmt.Fprintf(out, measurementPlainGraphFormat, measurement.Graph.ID, measurement.Graph.Scope, counts.Vertices, counts.Edges, counts.VirtualVertices, counts.TimedVertices)
	fmt.Fprintf(out, measurementPlainProvenanceFormat, measurement.Graph.Provenance, strings.Join(measurement.Graph.OmittedWork, measurementListSeparatorText))
	fmt.Fprintf(out, measurementPlainCapacityFormat, formatMeasurementQuantity(measurement.Resources.TaskSlotLimit), formatMeasurementQuantity(measurement.Resources.UsableOnePoolSlots))
	resources, _ := json.Marshal(measurement.Resources)
	fmt.Fprintf(out, measurementPlainResourcesFormat, formatMeasurementJSON(resources))
}

func writeMeasurementIntervalsPlain(out *bufio.Writer, measurement executionmeasurement.Measurement) {
	fmt.Fprintln(out, measurementPlainIntervalsText)
	for _, interval := range measurement.TaskIntervals {
		fmt.Fprintf(out, measurementPlainTaskFormat, interval.TaskID, interval.AttemptID, interval.NodeID, interval.Operation, measurementValue(interval.DurationSeconds), interval.Boundary, interval.ClockDomain, interval.ClockKind, interval.Status, interval.Reason)
	}
	for _, window := range measurement.TimeWindows {
		fmt.Fprintf(out, measurementPlainWindowFormat, window.ID, measurementValue(window.Seconds), window.Boundary, window.ClockDomain, window.ClockKind, window.Aggregation, window.Reason)
	}
	fmt.Fprintf(out, measurementPlainOverlapFormat, formatMeasurementQuantity(measurement.ObservedPeakOverlap))
}

func writeMeasurementCalculationsPlain(out *bufio.Writer, measurement executionmeasurement.Measurement) {
	fmt.Fprintln(out, measurementPlainFormulaText)
	for _, calculation := range measurement.Analysis {
		fmt.Fprintf(out, measurementPlainCalculationFormat, calculation.ID, calculation.Formula, formatMeasurementInputs(calculation.Inputs), measurementValue(calculation.Value), calculation.Unit, calculation.Model, calculation.Status, calculation.ClockMethod, strings.Join(calculation.Reasons, measurementListSeparatorText))
	}
}

func formatMeasurementQuantity(quantity executionmeasurement.Quantity) string {
	if quantity.Reason == measurementEmptyText {
		return measurementValue(quantity.Value)
	}
	return fmt.Sprintf(measurementQuantityReasonFormat, measurementValue(quantity.Value), quantity.Reason)
}
