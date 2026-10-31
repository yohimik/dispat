package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	experimentWorkflowPath = "../../.github/workflows/build-experiments.yml"
	productionWorkflowPath = "../../.github/workflows/release.yml"
)

func TestCloudTimeoutsFitWorkloadJobAndVMLifetimes(t *testing.T) {
	content, err := os.ReadFile(experimentWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			TimeoutMinutes string `yaml:"timeout-minutes"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	const timeoutExpressionFormat = "${{ inputs.purpose == 'kernel-gce' && %d || %d }}"
	expression := workflow.Jobs["llvm-gce"].TimeoutMinutes
	var kernelMinutes, llvmMinutes int
	if count, err := fmt.Sscanf(expression, timeoutExpressionFormat, &kernelMinutes, &llvmMinutes); err != nil || count != 2 || expression != fmt.Sprintf(timeoutExpressionFormat, kernelMinutes, llvmMinutes) {
		t.Fatalf("cell job must select its timeout only by workload: %q (%v)", expression, err)
	}
	if setupTimeout != 35*time.Minute || cleanupTimeout != 15*time.Minute {
		t.Fatal("workload execution extension changed setup or cleanup limits")
	}
	for _, scenario := range []struct {
		workload   string
		jobMinutes int
		lifetime   time.Duration
	}{{"llvm", llvmMinutes, 5 * time.Hour}, {"linux-kernel", kernelMinutes, 6 * time.Hour}} {
		t.Run(scenario.workload, func(t *testing.T) {
			timeouts := resolveWorkloadTimeouts(scenario.workload)
			jobTimeout := time.Duration(scenario.jobMinutes) * time.Minute
			if jobTimeout != scenario.lifetime || timeouts.vmLifetime != scenario.lifetime {
				t.Fatalf("job/VM lifetime = %v/%v, want %v", jobTimeout, timeouts.vmLifetime, scenario.lifetime)
			}
			phaseBudget := setupTimeout + timeouts.execution + cleanupTimeout
			if remaining := jobTimeout - phaseBudget; remaining < 10*time.Minute {
				t.Fatalf("job has only %v for evidence upload and cleanup recheck after bounded phases", remaining)
			}
		})
	}
}

const (
	campaignManualInputCount               = 0
	campaignRequiredWorkloadCount          = 2
	isCampaignEvidencePresent              = true
	campaignManualInputExpressionPrefix    = "inputs."
	campaignPlanJobName                    = "plan"
	campaignReleaseJobName                 = "release"
	campaignDocsOutputName                 = "docs"
	campaignStableOutputName               = "stable"
	campaignStatusStepID                   = "status"
	campaignStatusRunCommand               = "dispat status --require-release"
	campaignDocsOutputPrefix               = "docs="
	campaignLLVMRunOutputName              = "llvm-run"
	campaignLLVMAttemptOutputName          = "llvm-attempt"
	campaignLLVMArtifactOutputName         = "llvm-artifact"
	campaignKernelRunOutputName            = "kernel-run"
	campaignKernelAttemptOutputName        = "kernel-attempt"
	campaignKernelArtifactOutputName       = "kernel-artifact"
	campaignSelectorStepID                 = "campaigns"
	campaignDocsSelectionCondition         = "steps.status.outputs.docs == 'true'"
	campaignSelectRunCommand               = "dispat exec select-build-campaigns --for pkg:experiments --in pkg:experiments"
	campaignSourceEnvironmentName          = "EXPERIMENTS_BUILD_SOURCE_REVISION"
	campaignSourceExpression               = "${{ github.sha }}"
	campaignLLVMRunEnvironmentName         = "EXPERIMENTS_LLVM_RUN_ID"
	campaignLLVMRunExpression              = "${{ steps.campaigns.outputs.llvm-run }}"
	campaignLLVMAttemptEnvironmentName     = "EXPERIMENTS_LLVM_RUN_ATTEMPT"
	campaignLLVMAttemptExpression          = "${{ steps.campaigns.outputs.llvm-attempt }}"
	campaignKernelRunEnvironmentName       = "EXPERIMENTS_KERNEL_RUN_ID"
	campaignKernelRunExpression            = "${{ steps.campaigns.outputs.kernel-run }}"
	campaignKernelAttemptEnvironmentName   = "EXPERIMENTS_KERNEL_RUN_ATTEMPT"
	campaignKernelAttemptExpression        = "${{ steps.campaigns.outputs.kernel-attempt }}"
	campaignBinaryEnvironmentName          = "EXPERIMENTS_BUILD_BINARY_SHA256"
	campaignArtifactActionPrefix           = "actions/download-artifact@"
	campaignArtifactPathKey                = "path"
	campaignArtifactDirectoryPrefix        = "coverage/build-campaign-input/"
	campaignLLVMArtifactWorkloadName       = "llvm"
	campaignKernelArtifactWorkloadName     = "kernel"
	campaignDocsImportCondition            = "steps.status.outputs.docs == 'true'"
	campaignArtifactRunIDKey               = "run-id"
	campaignArtifactIDsKey                 = "artifact-ids"
	campaignArtifactNameKey                = "name"
	campaignArtifactPatternKey             = "pattern"
	campaignBinaryStepID                   = "campaign-binary"
	campaignLLVMDirectoryEnvironmentName   = "EXPERIMENTS_LLVM_CAMPAIGN_DIR"
	campaignKernelDirectoryEnvironmentName = "EXPERIMENTS_KERNEL_CAMPAIGN_DIR"
	campaignBinaryExportPrefix             = "EXPERIMENTS_BUILD_BINARY_SHA256="
	campaignGitHubEnvironmentFileName      = "GITHUB_ENV"
	campaignImportRunCommand               = "dispat exec import-build-campaigns"
	campaignValidateOnlyEnvironmentName    = "EXPERIMENTS_BUILD_VALIDATE_ONLY"
	campaignValidateOnlyEnabledValue       = "1"
	campaignPublishStepID                  = "dispat"
	campaignManualInputDependencyError     = "production release still depends on manually supplied workflow inputs"
	campaignStatusOrderingError            = "release package classification must precede campaign selection in the release job"
	campaignPlanOutputBindingErrorFormat   = "plan output %s contains release classification or campaign selection beyond the existence gate"
	campaignPlanSelectionError             = "campaign selection must run in the release job, not the existence gate"
	campaignSelectorWiringError            = "docs release must select source-bound campaigns once through its configured dispat script"
	campaignSelectorMissingError           = "docs release has no automatic source-bound campaign selection"
	campaignEnvironmentBindingErrorFormat  = "%s is not bound to the checkout and selected campaign"
	campaignJobEnvironmentErrorFormat      = "%s must be bound at the import and publication steps after campaign selection"
	campaignBinaryDerivationError          = "the measured binary must be derived from both downloaded campaign manifests"
	campaignArtifactBindingError           = "campaign download must use the exact automatically selected run and immutable artifact ID"
	campaignBinaryOrderingError            = "both exact artifacts must arrive before deriving their shared measured binary"
	campaignBinaryBindingErrorFormat       = "shared campaign binary gate omits %s"
	campaignValidationOrderingError        = "complete evidence validation must follow shared binary verification before publication"
	campaignPublicationOrderingError       = "publication can begin before both selected campaigns and their binary are validated"
	campaignPublicationMissingError        = "release publication step was not checked"
	campaignRunOutputExpressionFormat      = "${{ steps.campaigns.outputs.%s-run }}"
	campaignArtifactOutputExpressionFormat = "${{ steps.campaigns.outputs.%s-artifact }}"
	campaignArtifactSelectionUnset         = ""
	campaignManualInputsError              = "production release must discover campaign evidence without manual inputs"
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
	if strings.Contains(string(content), campaignManualInputExpressionPrefix) {
		t.Fatal(campaignManualInputDependencyError)
	}
	var workflow struct {
		Jobs map[string]struct {
			Outputs map[string]string `yaml:"outputs"`
			Env     map[string]string `yaml:"env"`
			Steps   []struct {
				ID, If, Run, Uses string
				With              map[string]string `yaml:"with"`
				Env               map[string]string `yaml:"env"`
				ContinueOnError   bool              `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	plan := workflow.Jobs[campaignPlanJobName]
	for _, outputName := range []string{campaignDocsOutputName, campaignStableOutputName, campaignLLVMRunOutputName, campaignLLVMAttemptOutputName, campaignLLVMArtifactOutputName, campaignKernelRunOutputName, campaignKernelAttemptOutputName, campaignKernelArtifactOutputName} {
		if _, isPresent := plan.Outputs[outputName]; isPresent {
			t.Fatalf(campaignPlanOutputBindingErrorFormat, outputName)
		}
	}
	for _, step := range plan.Steps {
		if step.ID == campaignSelectorStepID || strings.Contains(step.Run, campaignSelectRunCommand) {
			t.Fatal(campaignPlanSelectionError)
		}
	}
	release := workflow.Jobs[campaignReleaseJobName]
	if release.Env[campaignSourceEnvironmentName] != campaignSourceExpression {
		t.Fatalf(campaignEnvironmentBindingErrorFormat, campaignSourceEnvironmentName)
	}
	selectedBindings := map[string]string{
		campaignLLVMRunEnvironmentName:       campaignLLVMRunExpression,
		campaignLLVMAttemptEnvironmentName:   campaignLLVMAttemptExpression,
		campaignKernelRunEnvironmentName:     campaignKernelRunExpression,
		campaignKernelAttemptEnvironmentName: campaignKernelAttemptExpression,
	}
	for environmentName := range selectedBindings {
		if _, isPresent := release.Env[environmentName]; isPresent {
			t.Fatalf(campaignJobEnvironmentErrorFormat, environmentName)
		}
	}
	if _, isPresent := release.Env[campaignBinaryEnvironmentName]; isPresent {
		t.Fatal(campaignBinaryDerivationError)
	}
	downloadedWorkloads := map[string]bool{}
	var isClassified, isSelected, isBinaryVerified, isValidated, isPublished bool
	for _, step := range release.Steps {
		if step.ID == campaignStatusStepID {
			if isClassified || isSelected || step.ContinueOnError ||
				!strings.Contains(step.Run, campaignStatusRunCommand) || !strings.Contains(step.Run, campaignDocsOutputPrefix) {
				t.Fatal(campaignStatusOrderingError)
			}
			isClassified = isCampaignEvidencePresent
		}
		if step.ID == campaignSelectorStepID {
			if !isClassified || isSelected || step.If != campaignDocsSelectionCondition || step.ContinueOnError ||
				strings.TrimSpace(step.Run) != campaignSelectRunCommand {
				t.Fatal(campaignSelectorWiringError)
			}
			isSelected = isCampaignEvidencePresent
		}
		if strings.HasPrefix(step.Uses, campaignArtifactActionPrefix) && strings.Contains(step.With[campaignArtifactPathKey], campaignArtifactDirectoryPrefix) {
			workload := strings.TrimPrefix(step.With[campaignArtifactPathKey], campaignArtifactDirectoryPrefix)
			if !isSelected || (workload != campaignLLVMArtifactWorkloadName && workload != campaignKernelArtifactWorkloadName) || downloadedWorkloads[workload] ||
				step.If != campaignDocsImportCondition || step.ContinueOnError ||
				step.With[campaignArtifactRunIDKey] != fmt.Sprintf(campaignRunOutputExpressionFormat, workload) ||
				step.With[campaignArtifactIDsKey] != fmt.Sprintf(campaignArtifactOutputExpressionFormat, workload) ||
				step.With[campaignArtifactNameKey] != campaignArtifactSelectionUnset || step.With[campaignArtifactPatternKey] != campaignArtifactSelectionUnset {
				t.Fatal(campaignArtifactBindingError)
			}
			downloadedWorkloads[workload] = isCampaignEvidencePresent
		}
		if step.ID == campaignBinaryStepID {
			if isBinaryVerified || len(downloadedWorkloads) != campaignRequiredWorkloadCount || step.If != campaignDocsImportCondition || step.ContinueOnError {
				t.Fatal(campaignBinaryOrderingError)
			}
			for _, binding := range []string{campaignLLVMDirectoryEnvironmentName, campaignKernelDirectoryEnvironmentName, campaignBinaryExportPrefix, campaignGitHubEnvironmentFileName} {
				if !strings.Contains(step.Run, binding) {
					t.Fatalf(campaignBinaryBindingErrorFormat, binding)
				}
			}
			isBinaryVerified = isCampaignEvidencePresent
		}
		if strings.Contains(step.Run, campaignImportRunCommand) || step.ID == campaignPublishStepID {
			for environmentName, expectedExpression := range selectedBindings {
				if step.Env[environmentName] != expectedExpression {
					t.Fatalf(campaignEnvironmentBindingErrorFormat, environmentName)
				}
			}
		}
		if strings.Contains(step.Run, campaignImportRunCommand) {
			if !isBinaryVerified || step.If != campaignDocsImportCondition ||
				step.Env[campaignValidateOnlyEnvironmentName] != campaignValidateOnlyEnabledValue || step.ContinueOnError {
				t.Fatal(campaignValidationOrderingError)
			}
			isValidated = isCampaignEvidencePresent
		}
		if step.ID == campaignPublishStepID {
			isPublished = isCampaignEvidencePresent
			if !isValidated {
				t.Fatal(campaignPublicationOrderingError)
			}
		}
	}
	if !isSelected {
		t.Fatal(campaignSelectorMissingError)
	}
	if !isPublished {
		t.Fatal(campaignPublicationMissingError)
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
	if len(manual.Inputs) != campaignManualInputCount {
		t.Fatal(campaignManualInputsError)
	}
}
