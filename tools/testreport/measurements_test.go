package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestExecutionMeasurementSurvivesReportAndSeparateTables(t *testing.T) {
	body, err := os.ReadFile("../../packages/docs/plugins/test-report/testdata/measurement.json")
	if err != nil {
		t.Fatal(err)
	}
	verdictBody, err := json.Marshal(map[string]any{"experiment": "timing", "tool": "dispat", "scenario": "test", "dispat": "dispat 1.11.0-rc.13 (linux_arm64)", "passed": true, "steps": []any{}, "checks": []any{}, "executionMeasurement": json.RawMessage(body)})
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
	var markdown bytes.Buffer
	if err := writeExperimentsMarkdown(&markdown, campaign); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"Captured inputs", "Measured intervals and outcomes", "Formula calculations", "analysis-projection", "Task-slot ceiling", "W_nom = sum_i d_i", "Clock assumptions and uncertainty", "producerExtra"} {
		if !strings.Contains(markdown.String(), section) {
			t.Fatalf("display omitted %s", section)
		}
	}
}
