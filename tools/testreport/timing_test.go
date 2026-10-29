package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validTiming is figures lib/timing.py could have written for a six-package
// release under dispat: four builds and four uploads, each listed.
func validTiming() map[string]any {
	var times []map[string]any
	for _, task := range []string{"build", "publish"} {
		for _, p := range []string{"core", "api", "cli", "ui"} {
			times = append(times, map[string]any{"package": p, "task": task, "start": 0.1, "seconds": 0.5})
		}
	}
	return map[string]any{
		"relation": "build", "observedRelation": "build",
		"consumerBuilds":  map[string]any{"edges": 3, "afterBuild": 3, "afterPublish": 0},
		"violations":      []any{},
		"planningSeconds": 0.005, "planBuilds": map[string]any{"count": 0, "seconds": 0},
		"makespanSeconds": 1.661, "idleSeconds": 0.448, "work": 2.663, "criticalPath": 1.204,
		"parallelism": 4, "bound": 1.204, "ratio": 1.379,
		"criticalChain": []string{"core build", "ui build", "ui publish"},
		"tasks": map[string]any{
			"build":      map[string]any{"count": 4, "seconds": 2.009, "median": 0.502, "max": 0.503, "failed": 0},
			"publish":    map[string]any{"count": 4, "seconds": 0.654, "median": 0.199, "max": 0.199, "failed": 0},
			"npmPublish": map[string]any{"count": 4, "seconds": 1.401, "median": 0.403, "max": 0.406, "failed": 0},
		},
		"taskTimes": times, "untimed": []any{}, "unknown": []any{}, "straddling": 0,
	}
}

func timingVerdict(t *testing.T, tool string, plan map[string]string, timing map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"experiment": "timing", "scenario": "six", "tool": tool, "dispat": "dispat 1.11.0-rc.7 (linux_arm64)",
		"passed": true, "checks": []any{map[string]any{"check": "the release exited 0", "ok": true}},
		"steps":  []any{map[string]any{"step": "plan", "exit": 0, "kind": "query", "phase": "initial", "seconds": 0.01}},
		"plan":   map[string]any{"packages": plan, "told": "feat(core)^: streaming reader"},
		"timing": timing,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func writeTimingCell(t *testing.T, dir, tool string, plan map[string]string) {
	t.Helper()
	writeCell(t, dir, "timing-six-"+tool, timingVerdict(t, tool, plan, validTiming()),
		`{"label":"after-release","packages":{"core":{"registry":"1.1.0","state":"consistent"}}}`+"\n")
}

// The figures and the plan reach the report as the harness wrote them, the
// plan sorted by package and every other plan set beside dispat's.
func TestReadExperimentsReadsTheTimingAndComparesThePlans(t *testing.T) {
	dir := t.TempDir()
	writeTimingCell(t, dir, "dispat", map[string]string{"ui": "1.0.1", "core": "1.1.0", "cli": "1.0.1"})
	writeTimingCell(t, dir, "changesets", map[string]string{"core": "1.1.0", "cli": "1.0.1", "ui": "1.0.1"})
	writeTimingCell(t, dir, "lerna", map[string]string{"core": "1.1.0", "cli": "1.0.1", "docs": "1.0.1"})
	campaign, err := readExperiments(dir)
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]Cell{}
	for _, cell := range campaign.Cells {
		cells[cell.Tool] = cell
	}
	figures := cells["dispat"].Timing
	if figures == nil || figures.Work != 2.663 || figures.Parallelism != 4 || figures.Tasks.Build.Count != 4 ||
		figures.Tasks.NPMPublish.Median != 0.403 || len(figures.TaskTimes) != 8 || figures.ObservedRelation != "build" ||
		strings.Join(figures.CriticalChain, ",") != "core build,ui build,ui publish" {
		t.Fatalf("got %+v, want the figures the verdict carries", figures)
	}
	if got := cells["dispat"].Plan; len(got.Packages) != 3 || got.Packages[0].Name != "cli" ||
		got.Packages[2] != (PlannedPackage{"ui", "1.0.1"}) || got.Told != "feat(core)^: streaming reader" {
		t.Errorf("dispat's plan = %+v, want three packages sorted by name and what it was told", got)
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

// The Plans column's and the final state's wording, from the list the site's
// tests read too: the page and the job summary say the same thing about one
// record.
func TestTheTextsTheSiteWritesToo(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "packages", "docs", "plugins", "test-report", "testdata", "texts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var texts struct {
		Plans []struct {
			Name string `json:"name"`
			Cell Cell   `json:"cell"`
			Want string `json:"want"`
		} `json:"plans"`
		States []struct {
			Name     string         `json:"name"`
			Packages []PackageState `json:"packages"`
			Want     string         `json:"want"`
		} `json:"states"`
	}
	if err := json.Unmarshal(body, &texts); err != nil {
		t.Fatal(err)
	}
	if len(texts.Plans) == 0 || len(texts.States) == 0 {
		t.Fatalf("texts.json holds %d plans and %d states", len(texts.Plans), len(texts.States))
	}
	for _, tc := range texts.Plans {
		t.Run("plan "+tc.Name, func(t *testing.T) {
			if got := planOf(tc.Cell); got != tc.Want {
				t.Fatalf("planOf = %q, want %q", got, tc.Want)
			}
		})
	}
	for _, tc := range texts.States {
		t.Run("state "+tc.Name, func(t *testing.T) {
			if got := stateOf(Cell{Final: State{Packages: tc.Packages}}); got != tc.Want {
				t.Fatalf("stateOf = %q, want %q", got, tc.Want)
			}
		})
	}
}

// Figures no measurement produces, and figures over records that did not
// cover the release, are refused like any malformed record.
func TestReadExperimentsRefusesImpossibleFigures(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"no ratio":                 func(m map[string]any) { m["ratio"] = nil },
		"a negative duration":      func(m map[string]any) { m["makespanSeconds"] = -1.0 },
		"no slot":                  func(m map[string]any) { m["parallelism"] = 0 },
		"a chain longer than work": func(m map[string]any) { m["criticalPath"] = 3.0; m["bound"] = 3.0; m["ratio"] = 0.554 },
		"a bound not the max":      func(m map[string]any) { m["bound"] = 1.0; m["ratio"] = 1.661 },
		"a ratio not the quotient": func(m map[string]any) { m["ratio"] = 2.0 },
		"an unknown relation":      func(m map[string]any) { m["relation"] = "version" },
		"an unknown observation":   func(m map[string]any) { m["observedRelation"] = "sometimes" },
		"an untimed package":       func(m map[string]any) { m["untimed"] = []string{"cli"} },
		"a package outside":        func(m map[string]any) { m["unknown"] = []string{"fixture"} },
		"a straddling record":      func(m map[string]any) { m["straddling"] = 1 },
		"no publication": func(m map[string]any) {
			m["tasks"].(map[string]any)["publish"] = map[string]any{"count": 0, "seconds": 0.654}
		},
		"work that is not its tasks": func(m map[string]any) { m["work"] = 4.0; m["bound"] = 1.204 },
		"a task list that is short":  func(m map[string]any) { m["taskTimes"] = m["taskTimes"].([]map[string]any)[:7] },
		"more idle than makespan":    func(m map[string]any) { m["idleSeconds"] = 2.0 },
		"a bound beaten in order": func(m map[string]any) {
			m["makespanSeconds"] = 1.0
			m["ratio"] = 0.831
		},
		"more consumer builds than edges": func(m map[string]any) {
			m["consumerBuilds"] = map[string]any{"edges": 3, "afterBuild": 4, "afterPublish": 0}
		},
	} {
		t.Run(name, func(t *testing.T) {
			timing := validTiming()
			change(timing)
			dir := t.TempDir()
			writeCell(t, dir, "timing-six-dispat", timingVerdict(t, "dispat", map[string]string{}, timing), "")
			if _, err := readExperiments(dir); err == nil || !strings.Contains(err.Error(), "timing") {
				t.Fatalf("readExperiments = %v, want the figures refused", err)
			}
		})
	}
}

// A release that beat its bound by breaking an edge is a record of that,
// with the edge named, rather than a malformed one.
func TestABoundBeatenOutOfOrderIsARecord(t *testing.T) {
	timing := validTiming()
	timing["makespanSeconds"], timing["ratio"] = 1.0, 0.831
	timing["violations"] = []any{map[string]any{"before": "core build", "after": "ui build"}}
	dir := t.TempDir()
	writeCell(t, dir, "timing-six-dispat", timingVerdict(t, "dispat", map[string]string{}, timing), "")
	campaign, err := readExperiments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := violationsOf(campaign.Cells[0].Timing); got != "core build > ui build" {
		t.Errorf("violationsOf = %q", got)
	}
}

// The report's JSON names every field the site's validator reads, and keeps
// a disagreement that is false rather than dropping it.
func TestTheReportCarriesTheTimingFields(t *testing.T) {
	agrees := false
	body, err := json.Marshal(Cell{Timing: &Timing{Relation: "build", Parallelism: 1},
		Plan: &Plan{Packages: []PlannedPackage{{"core", "1.1.0"}}, Told: "feat(core)^: streaming reader",
			Reference: "timing-six-dispat", Agrees: &agrees, Differences: []PlanDifference{{"docs", "1.0.1", ""}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"planningSeconds":0`, `"makespanSeconds":0`, `"idleSeconds":0`, `"criticalPath":0`,
		`"parallelism":1`, `"criticalChain":null`, `"violations":null`, `"planBuilds":{"count":0,"seconds":0}`,
		`"consumerBuilds":{"edges":0,"afterBuild":0,"afterPublish":0}`,
		`"npmPublish":{"count":0,"seconds":0,"median":0,"max":0,"failed":0}`, `"taskTimes":null`,
		`"told":"feat(core)^: streaming reader"`, `"agrees":false`, `"reference":"timing-six-dispat"`,
		`"differences":[{"package":"docs","version":"1.0.1","reference":""}]`,
		`"packages":[{"name":"core","version":"1.1.0"}]`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the report does not carry %s:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "observedRelation") {
		t.Errorf("an observed relation nobody recorded is carried: %s", body)
	}
	plain, err := json.Marshal(Cell{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "timing") || strings.Contains(string(plain), `"plan"`) {
		t.Errorf("a cell without figures carries them: %s", plain)
	}
}

// Both renderings carry the figures, the tasks and the plan: the job summary
// as tables of their own after the cells', the terminal as lines per timing
// cell.
func TestRenderingsCarryTheTiming(t *testing.T) {
	agrees := true
	campaign := Experiments{Version: "1.11.0-rc.7", Cells: []Cell{
		{ID: "orphan-dispat", Tool: "dispat", Dispat: "1.11.0-rc.7", Passed: true},
		{ID: "timing-six-lerna", Tool: "lerna", Dispat: "1.11.0-rc.7",
			Timing: &Timing{Relation: "build", ObservedRelation: "publish", PlanningSeconds: 1.026, MakespanSeconds: 5.512,
				IdleSeconds: 1.4, Work: 4.757, CriticalPath: 2.482, Parallelism: 3, Bound: 2.482, Ratio: 2.221,
				Violations: []Edge{{"core publish", "cli publish"}},
				Tasks: TaskKinds{Build: TaskStats{Count: 6, Seconds: 3.015, Median: 0.502, Max: 0.504},
					Publish: TaskStats{Count: 6, Seconds: 0.778, Median: 0.138, Max: 0.167, Failed: 1}},
				PlanBuilds: PlanBuilds{Count: 2, Seconds: 1}},
			Plan: &Plan{Packages: []PlannedPackage{{"core", "1.1.0"}}, Told: "feat(core): streaming reader", Agrees: &agrees}},
	}}
	var markdown strings.Builder
	if err := writeExperimentsMarkdown(&markdown, campaign); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| cell | planning | release span | Wk | Lk | observed peak overlap | observed-cap diagnostic | release / diagnostic | idle | relation | not honoured |",
		"| timing-six-lerna | 1.026s | 5.512s | 4.757s | 2.482s | 3 | 2.482s | 2.22 | 1.400s | build (records: publish) | core publish > cli publish |",
		"| timing-six-lerna | 6, median 0.502s, max 0.504s, total 3.015s | 6, median 0.138s, max 0.167s, total 0.778s, 1 failed | none | 2 |",
		"| timing-six-lerna | feat(core): streaming reader | agrees with dispat, 1 package |",
	} {
		if !strings.Contains(markdown.String(), want) {
			t.Errorf("the tables do not carry %q:\n%s", want, markdown.String())
		}
	}
	if strings.Count(markdown.String(), "| orphan-dispat |") != 1 {
		t.Errorf("a cell without figures is in the timing tables:\n%s", markdown.String())
	}
	var plain strings.Builder
	if err := writeExperimentsPlain(&plain, campaign); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  timing: planning 1.026s, release span 5.512s, Wk 4.757s, Lk 2.482s, observed peak overlap 3, observed-cap diagnostic 2.482s, release / diagnostic 2.22, idle 1.400s, relation build (records: publish), not honoured core publish > cli publish\n",
		"  tasks: builds 6, median 0.502s, max 0.504s, total 3.015s; publications 6, median 0.138s, max 0.167s, total 0.778s, 1 failed; npm publish none; builds while planning 2\n",
		"  plan: agrees with dispat, 1 package\n",
	} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("the summary does not carry %q:\n%s", want, plain.String())
		}
	}
	if strings.Count(plain.String(), "  timing:") != 1 {
		t.Errorf("a cell without figures has a timing line:\n%s", plain.String())
	}
	for name, output := range map[string]string{"plain": plain.String(), "Markdown": markdown.String()} {
		for _, qualification := range []string{"Legacy observed-overlap diagnostics", "does not establish a configured concurrency ceiling", "not validated theorem bounds or cross-tool speedups"} {
			if !strings.Contains(output, qualification) {
				t.Errorf("%s omitted diagnostic qualification %q", name, qualification)
			}
		}
		for _, inventedSection := range []string{"Captured inputs", "Formula calculations", "Recorded execution measurement", "see below"} {
			if strings.Contains(output, inventedSection) {
				t.Errorf("%s implies a structured measurement exists in a legacy-only report: %q", name, inventedSection)
			}
		}
	}
}

// A campaign without a timing cell renders no timing table at all.
func TestACampaignWithoutTimingHasNoTimingTable(t *testing.T) {
	var markdown strings.Builder
	if err := writeExperimentsMarkdown(&markdown, Experiments{Cells: []Cell{{ID: "orphan-dispat"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(markdown.String(), "| planning |") || strings.Contains(markdown.String(), "| told |") {
		t.Errorf("a timing table without timing cells:\n%s", markdown.String())
	}
}
