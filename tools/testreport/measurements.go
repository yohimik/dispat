package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	measurementUnknownText          = "unknown"
	measurementSecondsFormat        = "%.6g"
	measurementAbsentBytes          = 0
	measurementEmptyText            = ""
	measurementJSONIndentText       = "  "
	measurementListSeparatorText    = "; "
	measurementInputReferenceFormat = "%s = %s:%s%s"
	measurementInvalidJSONErrorText = "recorded execution measurement is not a single valid JSON value"
)

func measurementValue(value *float64) string {
	if value == nil {
		return measurementUnknownText
	}
	return fmt.Sprintf(measurementSecondsFormat, *value)
}

func writeMeasurementMarkdown(out *bufio.Writer, campaign Experiments) {
	for _, cell := range campaign.Cells {
		if len(cell.ExecutionMeasurement) == 0 {
			continue
		}
		var measurement executionmeasurement.Measurement
		if err := decodeMeasurementDisplay(cell.ExecutionMeasurement, &measurement); err != nil {
			continue
		}
		fmt.Fprintf(out, "\n### Recorded execution measurement: %s\n\n", escapePipes(cell.ID))
		fmt.Fprintln(out, "Captured inputs\n\n| Trial | Graph scope | Vertices | Typed edges | Virtual | Timed | Task-slot ceiling | Available one-pool slots |")
		fmt.Fprintln(out, "|---|---|---:|---:|---:|---:|---|---|")
		counts := measurement.Graph.Counts
		fmt.Fprintf(out, "| %s / repetition %d | %s | %d | %d | %d | %d | %s | %s |\n", escapePipes(measurement.Trial.ID), measurement.Trial.Repetition, measurement.Graph.Scope, counts.Vertices, counts.Edges, counts.VirtualVertices, counts.TimedVertices, measurementValue(measurement.Resources.TaskSlotLimit.Value), measurementValue(measurement.Resources.UsableOnePoolSlots.Value))
		fmt.Fprintf(out, "\nGraph provenance: %s. Omitted work: %s.\n", escapePipes(measurement.Graph.Provenance), escapePipes(strings.Join(measurement.Graph.OmittedWork, "; ")))
		fmt.Fprintln(out, "\nMeasured intervals and outcomes\n\n| Operation/window | Boundary | Seconds | Clock | Outcome/reason |")
		fmt.Fprintln(out, "|---|---|---:|---|---|")
		for _, interval := range measurement.TaskIntervals {
			fmt.Fprintf(out, "| %s / %s / %s | %s | %s | %s (%s) | %s %s |\n", escapePipes(interval.TaskID), escapePipes(interval.AttemptID), escapePipes(interval.NodeID), escapePipes(interval.Boundary), measurementValue(interval.DurationSeconds), escapePipes(interval.ClockDomain), interval.ClockKind, interval.Status, escapePipes(interval.Reason))
		}
		for _, window := range measurement.TimeWindows {
			fmt.Fprintf(out, "| %s | %s | %s | %s (%s) | %s |\n", escapePipes(window.ID), escapePipes(window.Boundary), measurementValue(window.Seconds), escapePipes(window.ClockDomain), window.ClockKind, escapePipes(window.Reason))
		}
		fmt.Fprintln(out, "\nFormula calculations\n\n| Formula | Selected inputs | Value/unit | Model/status | Applicability |")
		fmt.Fprintln(out, "|---|---|---|---|---|")
		for _, calculation := range measurement.Analysis {
			fmt.Fprintf(out, "| %s | %s | %s %s | %s / %s | %s |\n", escapePipes(calculation.Formula), escapePipes(formatMeasurementInputs(calculation.Inputs)), measurementValue(calculation.Value), calculation.Unit, calculation.Model, calculation.Status, escapePipes(strings.Join(calculation.Reasons, "; ")))
		}
		clock, _ := json.Marshal(measurement.ClockCompatibility)
		fmt.Fprintf(out, "\nClock assumptions and uncertainty: `%s`\n", escapePipes(string(clock)))
		fmt.Fprintln(out, "\n<details><summary>Complete retained inputs, resources, intervals and evidence references</summary>\n\n```json")
		fmt.Fprintln(out, formatMeasurementJSON(cell.ExecutionMeasurement))
		fmt.Fprintln(out, "```\n\n</details>")
	}
}

func decodeMeasurementDisplay(body json.RawMessage, measurement *executionmeasurement.Measurement) error {
	if !json.Valid(body) {
		return errors.New(measurementInvalidJSONErrorText)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(measurement)
}

func formatMeasurementInputs(references map[string]executionmeasurement.Reference) string {
	inputs := []string{}
	for symbol, reference := range references {
		inputs = append(inputs, fmt.Sprintf(measurementInputReferenceFormat, symbol, reference.Source, reference.EvidenceID, reference.Pointer))
	}
	sort.Strings(inputs)
	return strings.Join(inputs, measurementListSeparatorText)
}

func formatMeasurementJSON(body json.RawMessage) string {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, body, measurementEmptyText, measurementJSONIndentText); err != nil {
		return measurementUnknownText
	}
	return formatted.String()
}
