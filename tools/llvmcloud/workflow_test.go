package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	experimentWorkflowPath = "../../.github/workflows/build-experiments.yml"
	productionWorkflowPath = "../../.github/workflows/release.yml"
)

func TestExperimentDockerStoreIsInitializedBeforeImageUse(t *testing.T) {
	type workflowJob struct {
		Steps []struct {
			Run, Uses string
			With      map[string]string `yaml:"with"`
		} `yaml:"steps"`
	}
	jobs := make(map[string]workflowJob)
	for _, path := range []string{experimentWorkflowPath, productionWorkflowPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]workflowJob `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(content, &workflow); err != nil {
			t.Fatal(err)
		}
		for name, job := range workflow.Jobs {
			if _, exists := jobs[name]; exists {
				t.Fatalf("job %s occurs in both production and experiment workflows", name)
			}
			jobs[name] = job
		}
	}
	stores := map[string]bool{}
	for name, job := range jobs {
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
	content, err := os.ReadFile(productionWorkflowPath)
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
		for _, predicate := range []string{".head_sha == $source", ".run_attempt == $attempt", ".conclusion == \"success\"", ".path == \".github/workflows/build-experiments.yml\""} {
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

func TestBuildExperimentsRemainSeparateFromRelease(t *testing.T) {
	type workflowDocument struct {
		On          map[string]yaml.Node `yaml:"on"`
		Permissions map[string]string    `yaml:"permissions"`
		Jobs        map[string]struct {
			Uses        string            `yaml:"uses"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	read := func(path string) workflowDocument {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow workflowDocument
		if err := yaml.Unmarshal(content, &workflow); err != nil {
			t.Fatal(err)
		}
		return workflow
	}
	experiments := read(experimentWorkflowPath)
	dispatch, exists := experiments.On["workflow_dispatch"]
	if !exists || len(experiments.On) != 1 {
		t.Fatal("build experiments must run only by explicit manual dispatch")
	}
	var manual struct {
		Inputs map[string]struct {
			Default  string   `yaml:"default"`
			Type     string   `yaml:"type"`
			Required bool     `yaml:"required"`
			Options  []string `yaml:"options"`
		} `yaml:"inputs"`
	}
	if err := dispatch.Decode(&manual); err != nil {
		t.Fatal(err)
	}
	purpose := manual.Inputs["purpose"]
	if purpose.Type != "choice" || !purpose.Required || purpose.Default != "llvm-gce" ||
		!slices.Equal(purpose.Options, []string{"llvm-gce", "kernel-gce"}) {
		t.Fatal("experiment dispatch must select only LLVM or kernel, never production release")
	}
	if len(experiments.Permissions) != 1 || experiments.Permissions["contents"] != "read" {
		t.Fatal("build experiment workflow must have read-only repository permissions")
	}
	var jobs []string
	for name, job := range experiments.Jobs {
		jobs = append(jobs, name)
		if job.Uses != "" || job.Permissions["contents"] != "read" {
			t.Fatalf("experiment job %s delegates execution or widens repository access", name)
		}
		for permission, level := range job.Permissions {
			if permission != "id-token" && level != "read" && level != "none" {
				t.Fatalf("experiment job %s grants %s=%s", name, permission, level)
			}
		}
		for _, step := range job.Steps {
			for _, command := range []string{"dispat release", "git push", "gh release create", "npm publish", "docker push"} {
				if strings.Contains(step.Run, command) {
					t.Fatalf("experiment job %s contains publication command %q", name, command)
				}
			}
		}
	}
	slices.Sort(jobs)
	if !slices.Equal(jobs, []string{"llvm-gce", "llvm-prepare", "llvm-report"}) {
		t.Fatalf("standalone experiments must contain only prepare, execution and evidence collection: %v", jobs)
	}
	production := read(productionWorkflowPath)
	for name := range production.Jobs {
		if strings.HasPrefix(name, "llvm-") {
			t.Fatalf("experiment job %s remains in the production release graph", name)
		}
	}
	manual.Inputs = nil
	productionDispatch := production.On["workflow_dispatch"]
	if err := productionDispatch.Decode(&manual); err != nil {
		t.Fatal(err)
	}
	if _, exists := manual.Inputs["purpose"]; exists {
		t.Fatal("production release still accepts an experiment purpose")
	}
}
