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
	releaseWorkflowStableCondition         = "needs.plan.outputs.stable == 'true'"
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

type releaseWorkflowStep struct {
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
	If              string                `yaml:"if"`
	Needs           yaml.Node             `yaml:"needs"`
	Steps           []releaseWorkflowStep `yaml:"steps"`
	ContinueOnError bool                  `yaml:"continue-on-error"`
	Strategy        struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct {
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
	var names []string
	for name := range jobs {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"ping", "plan", "post-release", "post-release-npm", "release", "test-lane", "test-unit"}) {
		t.Fatalf("Release must contain only plan, parallel prerequisites, release and post-release jobs; got %v", names)
	}
	dispatch, exists := workflow.On["workflow_dispatch"]
	if !exists || len(workflow.On) != 1 {
		t.Fatal("production Release must have only its manual trigger")
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
	plan := jobs["plan"]
	if len(releaseWorkflowDependencies(t, plan)) != 0 || plan.If != "" || plan.ContinueOnError {
		t.Fatal("the production plan must be the unconditional first stage")
	}
	for _, name := range []string{"post-release", "post-release-npm"} {
		if !slices.Equal(releaseWorkflowDependencies(t, jobs[name]), []string{releaseWorkflowPublishJob}) ||
			strings.Contains(jobs[name].If, "inputs.purpose") {
			t.Fatalf("%s must follow only the production release", name)
		}
	}
	ping := jobs["ping"]
	if !slices.Equal(releaseWorkflowDependencies(t, ping), []string{"plan"}) ||
		ping.If != releaseWorkflowPublishCondition || ping.ContinueOnError {
		t.Fatal("the announcement check must run in parallel when the plan has a release")
	}
	release := jobs[releaseWorkflowPublishJob]
	dependencies := releaseWorkflowDependencies(t, release)
	slices.Sort(dependencies)
	if !slices.Equal(dependencies, []string{"ping", "plan", "test-lane", "test-unit"}) {
		t.Fatalf("publication prerequisites = %v; every parallel lane and ping must gate release", dependencies)
	}
	if release.If != releaseWorkflowPublishCondition || release.ContinueOnError {
		t.Fatal("publication must require a real release plan and preserve failed dependency status")
	}
	lanes := jobs["test-lane"]
	if lanes.Strategy.FailFast == nil || *lanes.Strategy.FailFast ||
		!slices.Equal(lanes.Strategy.Matrix.Lane, []string{"integration", "race", "checks"}) {
		t.Fatal("integration, race and checks must run independently without matrix fail-fast")
	}
	for _, name := range []string{"test-unit", "test-lane"} {
		job := jobs[name]
		if !slices.Equal(releaseWorkflowDependencies(t, job), []string{"plan"}) ||
			job.If != releaseWorkflowPublishCondition || job.ContinueOnError {
			t.Fatalf("%s does not run independently after the plan", name)
		}
		collected, swept := false, false
		command := "dispat run test:unit"
		if name == "test-lane" {
			command = `dispat run "test:${{ matrix.lane }}"`
		}
		for _, step := range job.Steps {
			if strings.Contains(step.Run, command) {
				swept = strings.Contains(step.Run, "--since all") && !step.ContinueOnError
			}
			if !strings.Contains(step.Run, "dispat exec test-fragment-collect") {
				continue
			}
			collected = swept && step.Env[releaseWorkflowFullEnvironment] == releaseWorkflowFullValue &&
				step.If == "" && !step.ContinueOnError
		}
		if !collected {
			t.Fatalf("%s must collect complete evidence only after its full successful sweep", name)
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
		"UNIT_RESULT":      "${{ needs['test-unit'].result }}",
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
