package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	releaseWorkflowManualInputCount        = 0
	releaseWorkflowManualInputsErrorFormat = "production Release must discover its evidence automatically, without manual inputs: %v"
	releaseWorkflowPath                    = "../../.github/workflows/release.yml"
	releaseWorkflowPublishJob              = "release"
	releaseWorkflowBuildSelector           = "--target bootstrap-export"
	releaseWorkflowGatherSelector          = "test-fragment-*"
	releaseWorkflowMergeSelector           = "dispat exec test-fragment-merge"
	releaseWorkflowFreshnessSelector       = "sh scripts/badge-cache-test.sh"
	releaseWorkflowCoverageSelector        = "dispat exec coverage-badge"
	releaseWorkflowBadgeSelector           = "dispat exec push-badge"
	releaseWorkflowPublishStep             = "dispat"
	releaseWorkflowPublishCondition        = "needs.plan.outputs.releasing == 'true'"
	releaseWorkflowStableCondition         = "steps.status.outputs.stable == 'true'"
	releaseWorkflowFullEnvironment         = "DISPAT_TEST_REQUIRE_ALL"
	releaseWorkflowFullValue               = "true"
	releaseWorkflowSuccess                 = "success"
	releaseWorkflowMarker                  = "publication-reached"
	releaseWorkflowMarkerCommand           = "printf '%s\\n' '" + releaseWorkflowMarker + "'"
	releaseWorkflowShell                   = "/bin/sh"
	releaseWorkflowShellArguments          = "-eu"
	releaseWorkflowShellCommandFlag        = "-c"
	releaseWorkflowFixtureMode             = 0o755
	releaseWorkflowLogName                 = "commands.log"
	releaseWorkflowLogEnvironment          = "RELEASE_WORKFLOW_COMMAND_LOG"
	releaseWorkflowFailEnvironment         = "RELEASE_WORKFLOW_FAIL_COMMAND"
	releaseWorkflowInjectedExitCode        = 73
	releaseWorkflowDispatCommand           = "dispat"
	releaseWorkflowMergeCommand            = "test-fragment-merge"
	releaseWorkflowFreshnessCommand        = "freshness"
	releaseWorkflowCoverageCommand         = "coverage-badge"
	releaseWorkflowBadgeCommand            = "push-badge"
	releaseWorkflowFreshnessPath           = "scripts/badge-cache-test.sh"
	releaseWorkflowFixtureScriptHead       = "#!/bin/sh\nset -eu\n"
	releaseWorkflowCommandStub             = releaseWorkflowFixtureScriptHead + `
[ "$#" -eq 2 ] && [ "$1" = exec ] || exit 91
printf '%s\n' "$2" >> "$RELEASE_WORKFLOW_COMMAND_LOG"
if [ "$2" = test-fragment-merge ]; then
  [ "${DISPAT_TEST_REQUIRE_ALL:-}" = true ] || exit 92
fi
if [ "$2" = "${RELEASE_WORKFLOW_FAIL_COMMAND:-}" ]; then
  exit 73
fi
`
	releaseWorkflowFreshnessStub = releaseWorkflowFixtureScriptHead + `
printf '%s\n' freshness >> "$RELEASE_WORKFLOW_COMMAND_LOG"
if [ "${RELEASE_WORKFLOW_FAIL_COMMAND:-}" = freshness ]; then
  exit 73
fi
`
)

const (
	releaseWorkflowNoDependencyCount             = 0
	releaseWorkflowManualTriggerCount            = 1
	releaseWorkflowPlanOutputCount               = 1
	releaseWorkflowSuccessfulExitCode            = 0
	releaseWorkflowNothingToReleaseExitCode      = 3
	releaseWorkflowFailedExitCode                = 1
	isReleaseWorkflowStepPresent                 = true
	releaseWorkflowPlanJob                       = "plan"
	releaseWorkflowPingJob                       = "ping"
	releaseWorkflowMatrixJob                     = "test-lane"
	releaseWorkflowActionPostJob                 = "post-release"
	releaseWorkflowNPMPostJob                    = "post-release-npm"
	releaseWorkflowManualTriggerName             = "workflow_dispatch"
	releaseWorkflowEmptyValue                    = ""
	releaseWorkflowPurposeReference              = "inputs.purpose"
	releaseWorkflowUnitLane                      = "unit"
	releaseWorkflowIntegrationLane               = "integration"
	releaseWorkflowRaceLane                      = "race"
	releaseWorkflowChecksLane                    = "checks"
	releaseWorkflowExperimentsLane               = "experiments"
	releaseWorkflowNonExperimentCondition        = "matrix.lane != 'experiments'"
	releaseWorkflowExperimentCondition           = "matrix.lane == 'experiments'"
	releaseWorkflowUnitCondition                 = "matrix.lane == 'unit'"
	releaseWorkflowWorkerEnvironment             = "CI_WORKER"
	releaseWorkflowMatrixTestCommand             = "dispat run \"test:${{ matrix.lane }}\""
	releaseWorkflowFullSelectionOption           = "--since all"
	releaseWorkflowFragmentCollectCommand        = "dispat exec test-fragment-collect"
	releaseWorkflowLinkCommand                   = "dispat run link"
	releaseWorkflowUnlinkCommand                 = "dispat run unlink"
	releaseWorkflowAlwaysCondition               = "always()"
	releaseWorkflowExperimentsCommand            = "dispat exec experiments --for pkg:experiments --in pkg:experiments"
	releaseWorkflowCandidateArtifactEnvironment  = "EXPERIMENTS_DISPAT_ARTIFACT"
	releaseWorkflowCandidateArtifactPath         = "${{ runner.temp }}/dispat-fresh/dispat"
	releaseWorkflowCandidateIdentityEnvironment  = "EXPERIMENTS_DISPAT_ID"
	releaseWorkflowCandidateIdentityValue        = "candidate.${{ github.sha }}.linux-amd64"
	releaseWorkflowCandidateResultsEnvironment   = "EXPERIMENTS_RESULTS"
	releaseWorkflowCandidateResultsPath          = "${{ runner.temp }}/candidate-experiments"
	releaseWorkflowCandidateReceiptPath          = "${{ runner.temp }}/experiment-candidate.json"
	releaseWorkflowCandidateArtifactName         = "candidate-experiments-${{ github.run_id }}-${{ github.run_attempt }}"
	releaseWorkflowUploadActionPrefix            = "actions/upload-artifact@"
	releaseWorkflowArtifactNameKey               = "name"
	releaseWorkflowArtifactPathKey               = "path"
	releaseWorkflowReleasingOutputName           = "releasing"
	releaseWorkflowReleasingOutputExpression     = "${{ steps.status.outputs.releasing }}"
	releaseWorkflowStatusCommand                 = "dispat status --require-release"
	releaseWorkflowStatusStepID                  = "status"
	releaseWorkflowConfiguredCommandPrefix       = "dispat exec"
	releaseWorkflowStatusExitEnvironment         = "RELEASE_WORKFLOW_STATUS_EXIT"
	releaseWorkflowGitHubOutputEnvironment       = "GITHUB_OUTPUT"
	releaseWorkflowGitHubSummaryEnvironment      = "GITHUB_STEP_SUMMARY"
	releaseWorkflowRunnerTempEnvironment         = "RUNNER_TEMP"
	releaseWorkflowStatusOutputFile              = "plan.outputs"
	releaseWorkflowStatusSummaryFile             = "summary.md"
	releaseWorkflowStatusCommandLog              = "status-command.log"
	releaseWorkflowPathEnvironmentPrefix         = "PATH="
	releaseWorkflowSystemPathSuffix              = ":/usr/bin:/bin"
	releaseWorkflowEnvironmentSeparator          = "="
	releaseWorkflowLineBreak                     = "\n"
	releaseWorkflowStatusArgumentsLine           = "status --require-release\n"
	releaseWorkflowPlanYesOutput                 = "releasing=true\n"
	releaseWorkflowPlanNoOutput                  = "releasing=false\n"
	releaseWorkflowJQCommand                     = "jq"
	releaseWorkflowPlanYesCase                   = "release exists"
	releaseWorkflowPlanNoCase                    = "nothing to release"
	releaseWorkflowPlanErrorCase                 = "status error"
	releaseWorkflowPlanUnexpectedCase            = "unexpected status failure"
	releaseWorkflowStagesErrorFormat             = "Release must contain only plan, two parallel job definitions, release and post-release jobs; got %v"
	releaseWorkflowManualTriggerError            = "production Release must have only its manual trigger"
	releaseWorkflowFirstStageError               = "the production plan must be the unconditional first stage"
	releaseWorkflowPostDependencyErrorFormat     = "%s must follow only the production release"
	releaseWorkflowParallelDependencyErrorFormat = "%s must run independently after a successful release-existence check"
	releaseWorkflowPrerequisitesErrorFormat      = "publication prerequisites = %v; the complete matrix and credential ping must gate release"
	releaseWorkflowPublicationConditionError     = "publication must require a real release plan and preserve failed dependency status"
	releaseWorkflowMatrixError                   = "unit, integration, race, checks and experiments must run independently without fail-fast or a serial matrix cap"
	releaseWorkflowWorkerIsolationError          = "only the unit matrix lane may enable the distributed test worker"
	releaseWorkflowTestSweepError                = "the four test lanes must run complete interpreted sweeps while excluding experiments"
	releaseWorkflowTestFragmentError             = "complete test fragments must follow successful test sweeps and exclude the experiment lane"
	releaseWorkflowLinkIsolationError            = "workspace linking must exclude the experiment lane"
	releaseWorkflowUnlinkIsolationError          = "workspace unlinking must run after test failures while excluding experiments"
	releaseWorkflowExperimentBindingError        = "the required experiment lane must use this checkout binary and distinct candidate results"
	releaseWorkflowExperimentArtifactError       = "candidate experiment evidence must be retained separately even when its run fails"
	releaseWorkflowLaneStepsMissingError         = "the test matrix is missing a required sweep, fragment, experiment run or candidate artifact"
	releaseWorkflowPlanOutputError               = "the first stage must expose only whether anything needs releasing"
	releaseWorkflowPlanExtraCommandError         = "the first stage must not run configured checks or campaign discovery"
	releaseWorkflowPlanStatusStepError           = "release existence must be checked once without a conditional or ignored error"
	releaseWorkflowPlanExitErrorFormat           = "status exit %d: got exit %d, want %d; output=%s"
	releaseWorkflowPlanResultErrorFormat         = "status exit %d: release output=%q error=%v, want %q"
	releaseWorkflowPlanInvocationErrorFormat     = "status invocation=%q error=%v; expected only status --require-release"
	releaseWorkflowPlanMarkerErrorFormat         = "status exit %d did not control subsequent execution: %s"
	releaseWorkflowStatusExitValueFormat         = releaseWorkflowStatusExitEnvironment + "=%d"
	releaseWorkflowStatusStub                    = releaseWorkflowFixtureScriptHead + `
printf '%s\n' "$*" >> "$RELEASE_WORKFLOW_COMMAND_LOG"
[ "$#" -eq 2 ] && [ "$1" = status ] && [ "$2" = --require-release ] || exit 91
printf '%s\n' 'Human-readable release status, deliberately not JSON.'
exit "$RELEASE_WORKFLOW_STATUS_EXIT"
`
	releaseWorkflowUnexpectedJQStub = releaseWorkflowFixtureScriptHead + `
printf '%s\n' 'Plan must not parse channels or campaign evidence.' >&2
exit 97
`
)

type releaseWorkflowStep struct {
	Uses            string            `yaml:"uses"`
	ID              string            `yaml:"id"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	With            map[string]string `yaml:"with"`
	ContinueOnError bool              `yaml:"continue-on-error"`
}

type releaseWorkflowDefinition struct {
	On   map[string]yaml.Node          `yaml:"on"`
	Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
}

type releaseWorkflowJob struct {
	Outputs         map[string]string     `yaml:"outputs"`
	Env             map[string]string     `yaml:"env"`
	If              string                `yaml:"if"`
	Needs           yaml.Node             `yaml:"needs"`
	Steps           []releaseWorkflowStep `yaml:"steps"`
	ContinueOnError bool                  `yaml:"continue-on-error"`
	Strategy        struct {
		FailFast    *bool `yaml:"fail-fast"`
		MaxParallel *int  `yaml:"max-parallel"`
		Matrix      struct {
			Lane []string `yaml:"lane"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

func readReleaseWorkflow(t *testing.T) releaseWorkflowDefinition {
	t.Helper()
	content, err := os.ReadFile(releaseWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflowDefinition
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func releaseWorkflowDependencies(t *testing.T, job releaseWorkflowJob) []string {
	t.Helper()
	if job.Needs.Kind == 0 {
		return nil
	}
	if job.Needs.Kind == yaml.ScalarNode {
		return []string{job.Needs.Value}
	}
	var dependencies []string
	if err := job.Needs.Decode(&dependencies); err != nil {
		t.Fatal(err)
	}
	return dependencies
}

func releaseWorkflowStepIndex(t *testing.T, job releaseWorkflowJob, name string) int {
	t.Helper()
	index := -1
	for position, step := range job.Steps {
		matches := strings.Contains(step.Run, name) || step.With["pattern"] == name
		if name == releaseWorkflowPublishStep {
			matches = step.ID == name
		}
		if !matches {
			continue
		}
		if index >= 0 {
			t.Fatalf("release step %q occurs more than once", name)
		}
		index = position
	}
	if index < 0 {
		t.Fatalf("release has no step %q", name)
	}
	return index
}

func TestReleaseWorkflowRequiresParallelCompleteLanes(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	jobs := workflow.Jobs
	var jobNames []string
	for jobName := range jobs {
		jobNames = append(jobNames, jobName)
	}
	slices.Sort(jobNames)
	if !slices.Equal(jobNames, []string{releaseWorkflowPingJob, releaseWorkflowPlanJob, releaseWorkflowActionPostJob, releaseWorkflowNPMPostJob, releaseWorkflowPublishJob, releaseWorkflowMatrixJob}) {
		t.Fatalf(releaseWorkflowStagesErrorFormat, jobNames)
	}
	dispatch, isPresent := workflow.On[releaseWorkflowManualTriggerName]
	if !isPresent || len(workflow.On) != releaseWorkflowManualTriggerCount {
		t.Fatal(releaseWorkflowManualTriggerError)
	}
	var manual struct {
		Inputs map[string]yaml.Node `yaml:"inputs"`
	}
	if err := dispatch.Decode(&manual); err != nil {
		t.Fatal(err)
	}
	if len(manual.Inputs) != releaseWorkflowManualInputCount {
		t.Fatalf(releaseWorkflowManualInputsErrorFormat, manual.Inputs)
	}
	plan := jobs[releaseWorkflowPlanJob]
	if len(releaseWorkflowDependencies(t, plan)) != releaseWorkflowNoDependencyCount || plan.If != releaseWorkflowEmptyValue || plan.ContinueOnError {
		t.Fatal(releaseWorkflowFirstStageError)
	}
	for _, jobName := range []string{releaseWorkflowActionPostJob, releaseWorkflowNPMPostJob} {
		if !slices.Equal(releaseWorkflowDependencies(t, jobs[jobName]), []string{releaseWorkflowPublishJob}) ||
			strings.Contains(jobs[jobName].If, releaseWorkflowPurposeReference) {
			t.Fatalf(releaseWorkflowPostDependencyErrorFormat, jobName)
		}
	}
	for _, jobName := range []string{releaseWorkflowPingJob, releaseWorkflowMatrixJob} {
		job := jobs[jobName]
		if !slices.Equal(releaseWorkflowDependencies(t, job), []string{releaseWorkflowPlanJob}) ||
			job.If != releaseWorkflowPublishCondition || job.ContinueOnError {
			t.Fatalf(releaseWorkflowParallelDependencyErrorFormat, jobName)
		}
	}
	release := jobs[releaseWorkflowPublishJob]
	dependencies := releaseWorkflowDependencies(t, release)
	slices.Sort(dependencies)
	if !slices.Equal(dependencies, []string{releaseWorkflowPingJob, releaseWorkflowPlanJob, releaseWorkflowMatrixJob}) {
		t.Fatalf(releaseWorkflowPrerequisitesErrorFormat, dependencies)
	}
	if release.If != releaseWorkflowPublishCondition || release.ContinueOnError {
		t.Fatal(releaseWorkflowPublicationConditionError)
	}
	lanes := jobs[releaseWorkflowMatrixJob]
	if lanes.Strategy.FailFast == nil || *lanes.Strategy.FailFast ||
		!slices.Equal(lanes.Strategy.Matrix.Lane, []string{releaseWorkflowUnitLane, releaseWorkflowIntegrationLane, releaseWorkflowRaceLane, releaseWorkflowChecksLane, releaseWorkflowExperimentsLane}) ||
		(lanes.Strategy.MaxParallel != nil && *lanes.Strategy.MaxParallel < len(lanes.Strategy.Matrix.Lane)) {
		t.Fatal(releaseWorkflowMatrixError)
	}
	if !strings.Contains(lanes.Env[releaseWorkflowWorkerEnvironment], releaseWorkflowUnitCondition) {
		t.Fatal(releaseWorkflowWorkerIsolationError)
	}
	var isSwept, isCollected, isExperimentRequired, isCandidateRetained bool
	for _, step := range lanes.Steps {
		if strings.Contains(step.Run, releaseWorkflowMatrixTestCommand) {
			if step.If != releaseWorkflowNonExperimentCondition || step.ContinueOnError || !strings.Contains(step.Run, releaseWorkflowFullSelectionOption) {
				t.Fatal(releaseWorkflowTestSweepError)
			}
			isSwept = isReleaseWorkflowStepPresent
		}
		if strings.Contains(step.Run, releaseWorkflowFragmentCollectCommand) {
			if !isSwept || step.If != releaseWorkflowNonExperimentCondition || step.ContinueOnError || step.Env[releaseWorkflowFullEnvironment] != releaseWorkflowFullValue {
				t.Fatal(releaseWorkflowTestFragmentError)
			}
			isCollected = isReleaseWorkflowStepPresent
		}
		if strings.Contains(step.Run, releaseWorkflowLinkCommand) && step.If != releaseWorkflowNonExperimentCondition {
			t.Fatal(releaseWorkflowLinkIsolationError)
		}
		if strings.Contains(step.Run, releaseWorkflowUnlinkCommand) &&
			(!strings.Contains(step.If, releaseWorkflowNonExperimentCondition) || !strings.Contains(step.If, releaseWorkflowAlwaysCondition)) {
			t.Fatal(releaseWorkflowUnlinkIsolationError)
		}
		if strings.Contains(step.Run, releaseWorkflowExperimentsCommand) {
			if step.If != releaseWorkflowExperimentCondition || step.ContinueOnError ||
				step.Env[releaseWorkflowCandidateArtifactEnvironment] != releaseWorkflowCandidateArtifactPath ||
				step.Env[releaseWorkflowCandidateIdentityEnvironment] != releaseWorkflowCandidateIdentityValue ||
				step.Env[releaseWorkflowCandidateResultsEnvironment] != releaseWorkflowCandidateResultsPath {
				t.Fatal(releaseWorkflowExperimentBindingError)
			}
			isExperimentRequired = isReleaseWorkflowStepPresent
		}
		if strings.HasPrefix(step.Uses, releaseWorkflowUploadActionPrefix) && step.With[releaseWorkflowArtifactNameKey] == releaseWorkflowCandidateArtifactName {
			if !isExperimentRequired || step.ContinueOnError || !strings.Contains(step.If, releaseWorkflowExperimentCondition) ||
				!strings.Contains(step.If, releaseWorkflowAlwaysCondition) || !strings.Contains(step.With[releaseWorkflowArtifactPathKey], releaseWorkflowCandidateResultsPath) ||
				!strings.Contains(step.With[releaseWorkflowArtifactPathKey], releaseWorkflowCandidateReceiptPath) {
				t.Fatal(releaseWorkflowExperimentArtifactError)
			}
			isCandidateRetained = isReleaseWorkflowStepPresent
		}
	}
	if !isSwept || !isCollected || !isExperimentRequired || !isCandidateRetained {
		t.Fatal(releaseWorkflowLaneStepsMissingError)
	}
}

func TestReleaseWorkflowPlanOnlyChecksExistence(t *testing.T) {
	plan := readReleaseWorkflow(t).Jobs[releaseWorkflowPlanJob]
	status := plan.Steps[releaseWorkflowStepIndex(t, plan, releaseWorkflowStatusCommand)]
	if status.ID != releaseWorkflowStatusStepID || status.If != releaseWorkflowEmptyValue || status.ContinueOnError {
		t.Fatal(releaseWorkflowPlanStatusStepError)
	}
	for _, scenario := range []struct {
		name                   string
		statusExit, wantedExit int
		wantedOutput           string
	}{
		{releaseWorkflowPlanYesCase, releaseWorkflowSuccessfulExitCode, releaseWorkflowSuccessfulExitCode, releaseWorkflowPlanYesOutput},
		{releaseWorkflowPlanNoCase, releaseWorkflowNothingToReleaseExitCode, releaseWorkflowSuccessfulExitCode, releaseWorkflowPlanNoOutput},
		{releaseWorkflowPlanErrorCase, releaseWorkflowFailedExitCode, releaseWorkflowFailedExitCode, releaseWorkflowEmptyValue},
		{releaseWorkflowPlanUnexpectedCase, releaseWorkflowInjectedExitCode, releaseWorkflowInjectedExitCode, releaseWorkflowEmptyValue},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			for name, content := range map[string]string{releaseWorkflowDispatCommand: releaseWorkflowStatusStub, releaseWorkflowJQCommand: releaseWorkflowUnexpectedJQStub} {
				path := filepath.Join(directory, name)
				write(t, path, content)
				if err := os.Chmod(path, releaseWorkflowFixtureMode); err != nil {
					t.Fatal(err)
				}
			}
			outputPath := filepath.Join(directory, releaseWorkflowStatusOutputFile)
			commandLog := filepath.Join(directory, releaseWorkflowStatusCommandLog)
			command := exec.CommandContext(t.Context(), releaseWorkflowShell, releaseWorkflowShellArguments, releaseWorkflowShellCommandFlag, status.Run+releaseWorkflowLineBreak+releaseWorkflowMarkerCommand)
			command.Dir = directory
			command.Env = []string{
				releaseWorkflowPathEnvironmentPrefix + directory + releaseWorkflowSystemPathSuffix,
				releaseWorkflowGitHubOutputEnvironment + releaseWorkflowEnvironmentSeparator + outputPath,
				releaseWorkflowGitHubSummaryEnvironment + releaseWorkflowEnvironmentSeparator + filepath.Join(directory, releaseWorkflowStatusSummaryFile),
				releaseWorkflowRunnerTempEnvironment + releaseWorkflowEnvironmentSeparator + directory,
				releaseWorkflowLogEnvironment + releaseWorkflowEnvironmentSeparator + commandLog,
				fmt.Sprintf(releaseWorkflowStatusExitValueFormat, scenario.statusExit),
			}
			output, err := command.CombinedOutput()
			exitCode := releaseWorkflowSuccessfulExitCode
			if err != nil {
				var processExit *exec.ExitError
				if !errors.As(err, &processExit) {
					t.Fatal(err)
				}
				exitCode = processExit.ExitCode()
			}
			if exitCode != scenario.wantedExit {
				t.Fatalf(releaseWorkflowPlanExitErrorFormat, scenario.statusExit, exitCode, scenario.wantedExit, output)
			}
			planOutput, err := os.ReadFile(outputPath)
			if (err != nil && !errors.Is(err, os.ErrNotExist)) || string(planOutput) != scenario.wantedOutput {
				t.Fatalf(releaseWorkflowPlanResultErrorFormat, scenario.statusExit, planOutput, err, scenario.wantedOutput)
			}
			invocation, err := os.ReadFile(commandLog)
			if err != nil || string(invocation) != releaseWorkflowStatusArgumentsLine {
				t.Fatalf(releaseWorkflowPlanInvocationErrorFormat, invocation, err)
			}
			if strings.Contains(string(output), releaseWorkflowMarker) != (scenario.wantedExit == releaseWorkflowSuccessfulExitCode) {
				t.Fatalf(releaseWorkflowPlanMarkerErrorFormat, scenario.statusExit, output)
			}
		})
	}
	if len(plan.Outputs) != releaseWorkflowPlanOutputCount || plan.Outputs[releaseWorkflowReleasingOutputName] != releaseWorkflowReleasingOutputExpression {
		t.Error(releaseWorkflowPlanOutputError)
	}
	for _, step := range plan.Steps {
		if strings.Contains(step.Run, releaseWorkflowConfiguredCommandPrefix) {
			t.Error(releaseWorkflowPlanExtraCommandError)
		}
	}
}

func TestReleaseWorkflowPrerequisiteGuardFailsClosed(t *testing.T) {
	release := readReleaseWorkflow(t).Jobs[releaseWorkflowPublishJob]
	if len(release.Steps) == 0 {
		t.Fatal("the production release has no prerequisite guard")
	}
	guard := release.Steps[0]
	if guard.If != "" || guard.ContinueOnError {
		t.Fatal("the unconditional prerequisite guard must be the first release step")
	}
	bindings := map[string]string{
		"PLAN_RESULT":      "${{ needs.plan.result }}",
		"TEST_LANE_RESULT": "${{ needs['test-lane'].result }}",
		"PING_RESULT":      "${{ needs.ping.result }}",
	}
	for name, expression := range bindings {
		if guard.Env[name] != expression {
			t.Fatalf("%s does not read its actual prerequisite result", name)
		}
	}
	check := func(t *testing.T, changed, result string, success bool) {
		t.Helper()
		command := exec.CommandContext(t.Context(), releaseWorkflowShell, releaseWorkflowShellArguments,
			releaseWorkflowShellCommandFlag, guard.Run+"\n"+releaseWorkflowMarkerCommand)
		for name := range bindings {
			value := releaseWorkflowSuccess
			if name == changed {
				value = result
			}
			command.Env = append(command.Env, name+"="+value)
		}
		output, err := command.CombinedOutput()
		if (err == nil) != success || strings.Contains(string(output), releaseWorkflowMarker) != success {
			t.Fatalf("prerequisite %s=%q: err=%v output=%s", changed, result, err, output)
		}
	}
	t.Run("all succeeded", func(t *testing.T) { check(t, "", "", true) })
	for name := range bindings {
		for _, status := range []string{"failure", "skipped", "cancelled", "", "unknown"} {
			t.Run(name+"/"+status, func(t *testing.T) { check(t, name, status, false) })
		}
	}
}

func TestReleaseWorkflowEvidenceFailuresStopPublication(t *testing.T) {
	release := readReleaseWorkflow(t).Jobs[releaseWorkflowPublishJob]
	build := releaseWorkflowStepIndex(t, release, releaseWorkflowBuildSelector)
	gather := releaseWorkflowStepIndex(t, release, releaseWorkflowGatherSelector)
	merge := releaseWorkflowStepIndex(t, release, releaseWorkflowMergeSelector)
	publish := releaseWorkflowStepIndex(t, release, releaseWorkflowPublishStep)
	if !(build < gather && gather < merge && merge < publish) {
		t.Fatal("the fresh CLI must validate downloaded test fragments before publication")
	}
	if release.Steps[gather].With["pattern"] != "test-fragment-*" ||
		release.Steps[gather].With["path"] != "coverage-fragments" ||
		release.Steps[gather].If != "" || release.Steps[gather].ContinueOnError {
		t.Fatal("every release must download the four test fragments before verification")
	}
	names := []string{releaseWorkflowMergeSelector, releaseWorkflowFreshnessSelector,
		releaseWorkflowCoverageSelector, releaseWorkflowBadgeSelector}
	previous := gather
	for index, name := range names {
		position := releaseWorkflowStepIndex(t, release, name)
		step := release.Steps[position]
		if position <= previous || position >= publish || step.ContinueOnError || step.Run == "" {
			t.Fatalf("%s cannot be bypassed before publication", name)
		}
		if index == 0 {
			if step.If != "" || step.Env[releaseWorkflowFullEnvironment] != releaseWorkflowFullValue {
				t.Fatal("prereleases must also verify complete source-bound test evidence")
			}
		} else if step.If != releaseWorkflowStableCondition {
			t.Fatalf("%s must use the stable release coverage policy", name)
		}
		previous = position
	}
	for _, scenario := range []struct {
		name, failure string
		stable        bool
		commands      []string
	}{
		{"prerelease success", "", false, []string{releaseWorkflowMergeCommand}},
		{"prerelease missing evidence", releaseWorkflowMergeCommand, false, []string{releaseWorkflowMergeCommand}},
		{"stable success", "", true, []string{releaseWorkflowMergeCommand, releaseWorkflowFreshnessCommand, releaseWorkflowCoverageCommand, releaseWorkflowBadgeCommand}},
		{"stable missing evidence", releaseWorkflowMergeCommand, true, []string{releaseWorkflowMergeCommand}},
		{"stale coverage", releaseWorkflowFreshnessCommand, true, []string{releaseWorkflowMergeCommand, releaseWorkflowFreshnessCommand}},
		{"coverage below minimum", releaseWorkflowCoverageCommand, true, []string{releaseWorkflowMergeCommand, releaseWorkflowFreshnessCommand, releaseWorkflowCoverageCommand}},
		{"badge publication failure", releaseWorkflowBadgeCommand, true, []string{releaseWorkflowMergeCommand, releaseWorkflowFreshnessCommand, releaseWorkflowCoverageCommand, releaseWorkflowBadgeCommand}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range map[string]string{
				releaseWorkflowDispatCommand: releaseWorkflowCommandStub,
				releaseWorkflowFreshnessPath: releaseWorkflowFreshnessStub,
			} {
				write(t, filepath.Join(dir, path), content)
				if err := os.Chmod(filepath.Join(dir, path), releaseWorkflowFixtureMode); err != nil {
					t.Fatal(err)
				}
			}
			var script strings.Builder
			for _, name := range names {
				step := release.Steps[releaseWorkflowStepIndex(t, release, name)]
				if step.If != "" && !scenario.stable {
					continue
				}
				script.WriteString("(\n")
				if value, exists := step.Env[releaseWorkflowFullEnvironment]; exists {
					fmt.Fprintf(&script, "export %s=%s\n", releaseWorkflowFullEnvironment, value)
				}
				script.WriteString(step.Run)
				script.WriteString("\n)\n")
			}
			script.WriteString(releaseWorkflowMarkerCommand)
			command := exec.CommandContext(t.Context(), releaseWorkflowShell, releaseWorkflowShellArguments,
				releaseWorkflowShellCommandFlag, script.String())
			command.Dir = dir
			command.Env = []string{"PATH=" + dir + ":/usr/bin:/bin",
				releaseWorkflowLogEnvironment + "=" + filepath.Join(dir, releaseWorkflowLogName),
				releaseWorkflowFailEnvironment + "=" + scenario.failure}
			output, err := command.CombinedOutput()
			if scenario.failure != "" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != releaseWorkflowInjectedExitCode {
					t.Fatalf("expected injected gate failure, got %v: %s", err, output)
				}
			} else if err != nil {
				t.Fatalf("valid release evidence failed: %v: %s", err, output)
			}
			if strings.Contains(string(output), releaseWorkflowMarker) != (scenario.failure == "") {
				t.Fatalf("publication did not follow the evidence gate result: %s", output)
			}
			calls, err := os.ReadFile(filepath.Join(dir, releaseWorkflowLogName))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(strings.Fields(string(calls)), scenario.commands) {
				t.Fatalf("commands = %q, want %v", calls, scenario.commands)
			}
		})
	}
}
