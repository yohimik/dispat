package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDocsCampaignEvidenceIsPinnedAndValidatedBeforePublication(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Outputs map[string]string `yaml:"outputs"`
			Env     map[string]string `yaml:"env"`
			Steps   []struct {
				ID, If, Run, Uses string
				With              map[string]string `yaml:"with"`
				Env               map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Jobs["plan"].Outputs["docs"] != "${{ steps.status.outputs.docs }}" {
		t.Fatal("docs evidence gate is not tied to the computed release plan")
	}
	gate := false
	for _, step := range workflow.Jobs["plan"].Steps {
		if !strings.Contains(step.Run, "actions/runs/$run/attempts/$attempt") {
			continue
		}
		gate = step.If == "steps.status.outputs.docs == 'true'"
		for _, predicate := range []string{".head_sha == $source", ".run_attempt == $attempt", ".conclusion == \"success\"", ".path == \".github/workflows/release.yml\""} {
			if !strings.Contains(step.Run, predicate) {
				t.Fatalf("campaign provenance gate omits %s", predicate)
			}
		}
	}
	if !gate {
		t.Fatal("docs release has no selected-attempt provenance gate")
	}
	release := workflow.Jobs["release"]
	if release.Env["EXPERIMENTS_BUILD_SOURCE_REVISION"] != "${{ github.sha }}" || release.Env["EXPERIMENTS_BUILD_BINARY_SHA256"] != "${{ inputs.campaign-binary-sha256 }}" {
		t.Fatal("campaign import is not bound to the checkout and selected binary")
	}
	downloads := map[string]bool{}
	validated, published := false, false
	for _, step := range release.Steps {
		if strings.Contains(step.With["name"], "-gce-report-full-") {
			if step.If != "needs.plan.outputs.docs == 'true'" || step.With["run-id"] == "" || step.With["pattern"] != "" {
				t.Fatal("campaign artifact download is not an exact selected run")
			}
			downloads[step.With["name"]] = true
		}
		if strings.Contains(step.Run, "dispat exec import-build-campaigns") {
			validated = step.If == "needs.plan.outputs.docs == 'true'" && step.Env["EXPERIMENTS_BUILD_VALIDATE_ONLY"] == "1"
		}
		if step.ID == "dispat" {
			published = true
			if !validated || len(downloads) != 2 {
				t.Fatal("publication can begin before both campaigns are downloaded and validated")
			}
		}
	}
	if !published {
		t.Fatal("release publication step was not checked")
	}
}
