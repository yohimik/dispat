package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestExecutionMeasurementSurvivesReportAndSeparateTables(t *testing.T) {
	body, err := os.ReadFile("../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	verdictBody, err := json.Marshal(map[string]any{"experiment": "timing", "tool": "dispat", "scenario": "test", "dispat": "dispat 1.11.0-rc.13 (linux_arm64)", "passed": true, "steps": []any{}, "checks": []any{}, "timing": validTiming(), "executionMeasurement": json.RawMessage(body)})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeCell(t, directory, "timing-test-dispat", string(verdictBody), "")
	campaign, err := readExperiments(directory)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{`"producerExtra":{"kept":true}`, `"relativeRateErrorBound":null`, `"clockMethod":"nominal-seconds"`, `"value":null`} {
		if !strings.Contains(string(encoded), retained) {
			t.Fatalf("report omitted %s", retained)
		}
	}
	for name, render := range map[string]func(io.Writer, Experiments) error{"plain": writeExperimentsPlain, "Markdown": writeExperimentsMarkdown} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := render(&output, campaign); err != nil {
				t.Fatal(err)
			}
			previous := -1
			for _, section := range []string{"Captured inputs", "Measured intervals and outcomes", "Formula calculations"} {
				position := strings.LastIndex(output.String(), section)
				if position <= previous {
					t.Fatalf("separate display section %s is missing or out of order", section)
				}
				previous = position
			}
			for _, retained := range []string{"analysis-projection", "Task-slot ceiling", "W_nom = sum_i d_i", "Clock assumptions and uncertainty", "producerExtra", "configure and compile", "host-a:monotonic", "peer start through result verification", "unknown nominal-seconds", "descriptive / unavailable", "one-pool upper-bound evaluation is unsupported", "s = measurement:/resources/usableOnePoolSlots/value", "For model claims on cells with recorded execution measurements, use their separate Formula calculations sections, selected inputs and applicability reasons."} {
				if !strings.Contains(output.String(), retained) {
					t.Fatalf("display omitted %s", retained)
				}
			}
		})
	}
}

func TestPlainMeasurementSurvivesLLVMAndKernelSummaryBranches(t *testing.T) {
	body, err := os.ReadFile("../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	campaign := Experiments{Cells: []Cell{
		{ID: "llvm-full-hybrid-dispat", LLVM: llvmFixture(), ExecutionMeasurement: body},
		{ID: "linux-kernel-full-hybrid-dispat", LinuxKernel: kernelFixture("full"), ExecutionMeasurement: body},
	}}
	var output bytes.Buffer
	if err := writeExperimentsPlain(&output, campaign); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, cell := range campaign.Cells {
		if !strings.Contains(text, "Recorded execution measurement: "+cell.ID) {
			t.Fatalf("build summary skipped %s measurement", cell.ID)
		}
	}
	for _, section := range []string{"Captured inputs", "Measured intervals and outcomes", "Formula calculations"} {
		if strings.Count(text, section) != len(campaign.Cells) {
			t.Fatalf("build summaries omitted separate section %s", section)
		}
	}
	for _, retained := range []string{"LLVM: full", "Linux kernel 6.18.59: full / allmodconfig", "Available one-pool slots: unknown (placement is not interchangeable)", "Observed peak overlap: unknown (worker clock origins are unmapped)", "Resource records:", "Selected inputs:", "Applicability:"} {
		if !strings.Contains(text, retained) {
			t.Fatalf("plain build measurement omitted %s", retained)
		}
	}
	if strings.Contains(text, "Legacy observed-overlap diagnostics") {
		t.Fatal("structured-only build records were labelled as legacy diagnostics")
	}
}

func TestMeasurementRawDisplayPreservesUnknownNumericFields(t *testing.T) {
	body, err := os.ReadFile("../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	record["producerExtra"] = json.RawMessage(`{"integer":9007199254740993,"decimal":1.2300000000000000000001}`)
	var resources, clock map[string]json.RawMessage
	if err := json.Unmarshal(record["resources"], &resources); err != nil {
		t.Fatal(err)
	}
	resources["nodes"] = json.RawMessage(`[{"id":"worker","integer":9007199254740993,"decimal":1.2300000000000000000001}]`)
	record["resources"], err = json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record["clockCompatibility"], &clock); err != nil {
		t.Fatal(err)
	}
	clock["externalCounters"] = record["producerExtra"]
	record["clockCompatibility"], err = json.Marshal(clock)
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	campaign := Experiments{Cells: []Cell{{ID: "measured", ExecutionMeasurement: body}}}
	for name, render := range map[string]func(io.Writer, Experiments) error{"plain": writeExperimentsPlain, "Markdown": writeExperimentsMarkdown} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := render(&output, campaign); err != nil {
				t.Fatal(err)
			}
			for _, number := range []string{"9007199254740993", "1.2300000000000000000001"} {
				if !strings.Contains(output.String(), number) {
					t.Fatalf("retained raw number %s was lost or rounded", number)
				}
				clockStart := strings.Index(output.String(), "Clock assumptions and uncertainty:")
				clockEnd := strings.Index(output.String(), "Complete retained inputs, resources, intervals and evidence references")
				if clockStart < 0 || clockEnd <= clockStart || !strings.Contains(output.String()[clockStart:clockEnd], number) {
					t.Fatalf("clock subsection lost or rounded %s", number)
				}
				if name == "plain" {
					resourceStart := strings.Index(output.String(), "Resource records:")
					resourceEnd := strings.Index(output.String(), "Measured intervals and outcomes")
					if resourceStart < 0 || resourceEnd <= resourceStart || !strings.Contains(output.String()[resourceStart:resourceEnd], number) {
						t.Fatalf("resource subsection lost or rounded %s", number)
					}
				}
			}
		})
	}
}
