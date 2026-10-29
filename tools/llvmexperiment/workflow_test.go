package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCloudReportCollectsSixExactArtifactsWithoutPreparedImage(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/build-experiments.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, step := range workflow.Jobs["llvm-report"].Steps {
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			name := step.With["name"]
			if name == "" || step.With["pattern"] != "" || names[name] {
				t.Fatal("cloud report uses ambiguous or repeated artifacts")
			}
			names[name] = true
		}
	}
	for _, cell := range campaignCells() {
		name := "${{ inputs.purpose }}-" + cell.Tool + "-" + cell.Mode + "-${{ github.run_id }}-${{ github.run_attempt }}"
		if !names[name] {
			t.Fatalf("cloud report omits %s", name)
		}
	}
	if len(names) != len(campaignCells()) {
		t.Fatal("cloud report downloads unrelated prepared image or extra campaign records")
	}
}
