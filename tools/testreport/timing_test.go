package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A timing cell's verdict as the harness writes one: the figures from
// lib/timing.py and the plan its tool printed.
const timingVerdict = `{"experiment":"timing","scenario":"six","tool":"%s","dispat":"dispat 1.11.0-rc.7 (linux_arm64)",
  "passed":true,"checks":[{"check":"the release exited 0","ok":true}],
  "steps":[{"step":"plan","exit":0,"kind":"query","phase":"initial","seconds":0.01,"started":1.0,"ended":1.01}],
  "plan":{"packages":%s},
  "timing":{"relation":"build","planningSeconds":0.009,"makespanSeconds":1.942,"work":3.442,"criticalPath":1.399,
    "parallelism":4,"bound":1.399,"ratio":1.388,"criticalChain":["core build","api build","api publish"],
    "tasks":{"version":0,"build":4,"publish":4},"publishTimedAt":{"npm":4,"registry":0}}}`

func writeTimingCell(t *testing.T, dir, tool, plan string) {
	t.Helper()
	writeCell(t, dir, "timing-six-"+tool, strings.Replace(strings.Replace(timingVerdict, "%s", tool, 1), "%s", plan, 1),
		`{"label":"after-release","packages":{"core":{"registry":"1.1.0","state":"consistent"}}}`+"\n")
}

// The figures and the plan reach the report as the harness wrote them, the
// plan sorted by package and every other plan set beside dispat's.
func TestReadExperimentsReadsTheTimingAndComparesThePlans(t *testing.T) {
	dir := t.TempDir()
	writeTimingCell(t, dir, "dispat", `{"ui":"1.0.1","core":"1.1.0","cli":"1.0.1"}`)
	writeTimingCell(t, dir, "changesets", `{"core":"1.1.0","cli":"1.0.1","ui":"1.0.1"}`)
	writeTimingCell(t, dir, "lerna", `{"core":"1.1.0","cli":"1.0.1","docs":"1.0.1"}`)
	campaign, err := readExperiments(dir)
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]Cell{}
	for _, cell := range campaign.Cells {
		cells[cell.Tool] = cell
	}
	figures := cells["dispat"].Timing
	if figures == nil || figures.Work != 3.442 || figures.Parallelism != 4 || figures.Tasks.Build != 4 ||
		figures.PublishTimedAt.NPM != 4 || strings.Join(figures.CriticalChain, ",") != "core build,api build,api publish" {
		t.Fatalf("got %+v, want the figures the verdict carries", figures)
	}
	if got := cells["dispat"].Plan.Packages; len(got) != 3 || got[0].Name != "cli" || got[2] != (PlannedPackage{"ui", "1.0.1"}) {
		t.Errorf("dispat's plan = %+v, want three packages sorted by name", got)
	}
	if plan := cells["dispat"].Plan; plan.Reference != "" || plan.Agrees != nil {
		t.Errorf("dispat's plan = %+v, want the reference compared with nothing", plan)
	}
	if plan := cells["changesets"].Plan; plan.Reference != "timing-six-dispat" || plan.Agrees == nil || !*plan.Agrees {
		t.Errorf("changesets' plan = %+v, want agreement with timing-six-dispat", plan)
	}
	want := []PlanDifference{{"docs", "1.0.1", ""}, {"ui", "", "1.0.1"}}
	if plan := cells["lerna"].Plan; plan.Agrees == nil || *plan.Agrees || len(plan.Differences) != 2 ||
		plan.Differences[0] != want[0] || plan.Differences[1] != want[1] {
		t.Errorf("lerna's plan = %+v, want docs added and ui missing", plan)
	}
}

// A plan with no dispat plan of its scenario is recorded as compared with
// nothing, rather than as agreeing or as differing from an empty plan.
func TestAPlanWithoutAReferenceIsNotCompared(t *testing.T) {
	cells := []Cell{
		{ID: "timing-wide-nx", Experiment: "timing", Scenario: "wide", Tool: "nx", Plan: &Plan{Packages: []PlannedPackage{{"core", "1.1.0"}}}},
		{ID: "timing-six-dispat", Experiment: "timing", Scenario: "six", Tool: "dispat", Plan: &Plan{}},
	}
	comparePlans(cells)
	if plan := cells[0].Plan; plan.Reference != "" || plan.Agrees != nil || plan.Differences != nil {
		t.Fatalf("got %+v, want no comparison", plan)
	}
	if got := planOf(cells[0]); got != "no dispat plan to compare" {
		t.Errorf("planOf = %q", got)
	}
}

// The Plans column's wording, which the site's timing.ts repeats string for
// string.
func TestPlanOf(t *testing.T) {
	agrees, differs := true, false
	two := []PlannedPackage{{"cli", "1.0.1"}, {"core", "1.1.0"}}
	for _, tc := range []struct {
		name string
		cell Cell
		want string
	}{
		{"no plan", Cell{Tool: "lerna"}, "not measured"},
		{"the reference", Cell{Tool: "dispat", Plan: &Plan{Packages: two}}, "the reference, 2 packages"},
		{"agreement", Cell{Tool: "nx", Plan: &Plan{Packages: two, Agrees: &agrees}}, "agrees with dispat, 2 packages"},
		{"one more package", Cell{Tool: "lerna", Plan: &Plan{Agrees: &differs, Differences: []PlanDifference{
			{"docs", "1.0.1", ""}, {"theme", "1.0.1", ""}}}}, "also docs 1.0.1, theme 1.0.1"},
		{"every kind", Cell{Tool: "changesets", Plan: &Plan{Agrees: &differs, Differences: []PlanDifference{
			{"chain2", "", "1.0.1"}, {"core", "1.2.0", "1.1.0"}, {"docs", "1.0.1", ""}, {"chain3", "", "1.0.1"}}}},
			"also docs 1.0.1; not chain2 1.0.1, chain3 1.0.1; core 1.2.0 for dispat's 1.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := planOf(tc.cell); got != tc.want {
				t.Fatalf("planOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// Figures no measurement produces are refused like any malformed record.
func TestReadExperimentsRefusesImpossibleFigures(t *testing.T) {
	for name, timing := range map[string]string{
		"a negative duration":         `{"relation":"build","makespanSeconds":-1,"parallelism":1}`,
		"no slot":                     `{"relation":"build","work":1,"criticalPath":1,"bound":1,"parallelism":0}`,
		"a chain longer than work":    `{"relation":"build","work":1,"criticalPath":2,"bound":2,"parallelism":1}`,
		"a bound that is not the max": `{"relation":"build","work":8,"criticalPath":1,"bound":1,"parallelism":2}`,
		"an unknown relation":         `{"relation":"version","work":1,"criticalPath":1,"bound":1,"parallelism":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeCell(t, dir, "timing-six-dispat", `{"tool":"dispat","timing":`+timing+`}`, "")
			if _, err := readExperiments(dir); err == nil || !strings.Contains(err.Error(), "timing") {
				t.Fatalf("readExperiments = %v, want the figures refused", err)
			}
		})
	}
}

// The report's JSON names every field the site's validator reads, and keeps
// a disagreement that is false rather than dropping it.
func TestTheReportCarriesTheTimingFields(t *testing.T) {
	agrees := false
	body, err := json.Marshal(Cell{Timing: &Timing{Relation: "build", Parallelism: 1},
		Plan: &Plan{Packages: []PlannedPackage{{"core", "1.1.0"}}, Reference: "timing-six-dispat", Agrees: &agrees,
			Differences: []PlanDifference{{"docs", "1.0.1", ""}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"planningSeconds":0`, `"makespanSeconds":0`, `"criticalPath":0`, `"parallelism":1`,
		`"criticalChain":null`, `"tasks":{"version":0,"build":0,"publish":0}`, `"publishTimedAt":{"npm":0,"registry":0}`,
		`"agrees":false`, `"reference":"timing-six-dispat"`, `"differences":[{"package":"docs","version":"1.0.1","reference":""}]`,
		`"packages":[{"name":"core","version":"1.1.0"}]`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the report does not carry %s:\n%s", want, body)
		}
	}
	plain, err := json.Marshal(Cell{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "timing") || strings.Contains(string(plain), `"plan"`) {
		t.Errorf("a cell without figures carries them: %s", plain)
	}
}

// Both renderings carry the figures and the plan: the job summary as a table
// of its own after the cells', the terminal as two lines per timing cell.
func TestRenderingsCarryTheTiming(t *testing.T) {
	agrees := true
	campaign := Experiments{Version: "1.11.0-rc.7", Cells: []Cell{
		{ID: "orphan-dispat", Tool: "dispat", Dispat: "1.11.0-rc.7", Passed: true},
		{ID: "timing-six-lerna", Tool: "lerna", Dispat: "1.11.0-rc.7",
			Timing: &Timing{Relation: "build", PlanningSeconds: 1.026, MakespanSeconds: 5.512, Work: 4.757,
				CriticalPath: 2.482, Parallelism: 3, Bound: 2.482, Ratio: 2.221},
			Plan: &Plan{Packages: []PlannedPackage{{"core", "1.1.0"}}, Agrees: &agrees}},
	}}
	var markdown strings.Builder
	if err := writeExperimentsMarkdown(&markdown, campaign); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| cell | planning | makespan | Wk | Lk | s | bound | ratio | plan |",
		"| timing-six-lerna | 1.026s | 5.512s | 4.757s | 2.482s | 3 | 2.482s | 2.22 | agrees with dispat, 1 package |",
	} {
		if !strings.Contains(markdown.String(), want) {
			t.Errorf("the table does not carry %q:\n%s", want, markdown.String())
		}
	}
	if strings.Count(markdown.String(), "| orphan-dispat |") != 1 {
		t.Errorf("a cell without figures is in the timing table:\n%s", markdown.String())
	}
	var plain strings.Builder
	if err := writeExperimentsPlain(&plain, campaign); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  timing: planning 1.026s, makespan 5.512s, Wk 4.757s, Lk 2.482s, s 3, bound 2.482s, ratio 2.22\n",
		"  plan: agrees with dispat, 1 package\n",
	} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("the summary does not carry %q:\n%s", want, plain.String())
		}
	}
	if strings.Count(plain.String(), "  timing:") != 1 {
		t.Errorf("a cell without figures has a timing line:\n%s", plain.String())
	}
}

// A campaign without a timing cell renders no timing table at all.
func TestACampaignWithoutTimingHasNoTimingTable(t *testing.T) {
	var markdown strings.Builder
	if err := writeExperimentsMarkdown(&markdown, Experiments{Cells: []Cell{{ID: "orphan-dispat"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(markdown.String(), "| planning |") {
		t.Errorf("a timing table without timing cells:\n%s", markdown.String())
	}
}
