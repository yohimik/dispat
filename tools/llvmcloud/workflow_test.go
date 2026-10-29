package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExperimentDockerStoreIsInitializedBeforeImageUse(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run, Uses string
				With      map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	stores := map[string]bool{}
	for name, job := range workflow.Jobs {
		configured := false
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "docker/setup-docker-action@") {
				if configured {
					t.Fatalf("%s configures the Docker store more than once", name)
				}
				var daemon struct {
					Driver   string          `json:"storage-driver"`
					Features map[string]bool `json:"features"`
				}
				if err := json.Unmarshal([]byte(step.With["daemon-config"]), &daemon); err != nil {
					t.Fatalf("%s daemon configuration: %v", name, err)
				}
				containerd, explicit := daemon.Features["containerd-snapshotter"]
				switch name {
				case "llvm-prepare", "llvm-gce":
					if daemon.Driver != "overlay2" || !explicit || containerd {
						t.Fatalf("%s does not explicitly use classic overlay2", name)
					}
				case "release":
					if !explicit || !containerd {
						t.Fatal("production multiarchitecture release lost its containerd image store")
					}
				default:
					t.Fatalf("unexpected Docker store override in %s", name)
				}
				configured, stores[name] = true, true
			}
			imageUse := strings.HasPrefix(step.Uses, "docker/setup-buildx-action@") || strings.Contains(step.Run, "docker build") || strings.Contains(step.Run, "docker run") || strings.Contains(step.Run, "docker load")
			if name == "llvm-gce" && strings.HasPrefix(step.Uses, "actions/download-artifact@") {
				imageUse = true
			}
			if (name == "llvm-prepare" || name == "llvm-gce" || name == "release") && imageUse && !configured {
				t.Fatalf("%s uses images before selecting its Docker store", name)
			}
			if name == "llvm-prepare" && strings.Contains(step.Run, "docker buildx build") {
				proof, build := strings.Index(step.Run, dockerInfoFormat), strings.Index(step.Run, "docker run")
				if proof < 0 || build < 0 || proof >= build || !strings.Contains(step.Run, "prepare-docker-info.json > checksums.txt") {
					t.Fatal("preparation must retain sanitized store evidence before image use and checksum it")
				}
				if !strings.Contains(step.Run, `experiment_builder="$(docker context show)"`) ||
					!strings.Contains(step.Run, `docker buildx inspect "$experiment_builder" | awk '$1 == "Driver:" && $2 == "docker" && NF == 2 { found=1 } END { exit found ? 0 : 1 }'`) ||
					strings.Count(step.Run, `docker buildx build --builder "$experiment_builder"`) != 2 || strings.Contains(step.Run, "--builder default") {
					t.Fatal("both experiment images must use the selected context's native builder so local base images resolve in the pinned daemon")
				}
			}
		}
	}
	for _, name := range []string{"llvm-prepare", "llvm-gce", "release"} {
		if !stores[name] {
			t.Fatalf("%s has no explicit Docker store configuration", name)
		}
	}
}

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
