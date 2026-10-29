package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	measurementUnknownText   = "unknown"
	measurementSecondsFormat = "%.6g"
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
		if err := json.Unmarshal(cell.ExecutionMeasurement, &measurement); err != nil {
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
			inputs := []string{}
			for symbol, reference := range calculation.Inputs {
				inputs = append(inputs, symbol+" = "+reference.EvidenceID+reference.Pointer)
			}
			sort.Strings(inputs)
			fmt.Fprintf(out, "| %s | %s | %s %s | %s / %s | %s |\n", escapePipes(calculation.Formula), escapePipes(strings.Join(inputs, "; ")), measurementValue(calculation.Value), calculation.Unit, calculation.Model, calculation.Status, escapePipes(strings.Join(calculation.Reasons, "; ")))
		}
		clock, _ := json.Marshal(measurement.ClockCompatibility)
		fmt.Fprintf(out, "\nClock assumptions and uncertainty: `%s`\n", escapePipes(string(clock)))
		fmt.Fprintln(out, "\n<details><summary>Complete retained inputs, resources, intervals and evidence references</summary>\n\n```json")
		var formatted any
		if json.Unmarshal(cell.ExecutionMeasurement, &formatted) == nil {
			if body, err := json.MarshalIndent(formatted, "", "  "); err == nil {
				fmt.Fprintln(out, string(body))
			}
		}
		fmt.Fprintln(out, "```\n\n</details>")
	}
}
