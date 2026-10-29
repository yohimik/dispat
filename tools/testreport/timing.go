package main

// The timing cells' half of the experiments: the figures lib/timing.py wrote
// and the plan each tool printed, held to what a measurement could have
// produced, the plans compared with dispat's, and both rendered.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// referenceTool is the tool whose plan every other tool's plan of the same
// scenario is set beside.
const referenceTool = "dispat"

// rounding is the slack between figures lib/timing.py rounded to the
// millisecond on their own.
const rounding = 0.002

// clockSlack is how far a release that honoured its precedence can still
// come in under its bound: two of the harness's 50 ms clock slacks, one at
// each end of the release's span.
const clockSlack = 0.1

// namedGroup is the most packages a rendered group names; a larger one is
// counted.
const namedGroup = 4

// wholeState is the most packages a final state lists one by one.
const wholeState = 8

const (
	legacyTimingPlainFormat         = "planning %s, release span %s, Wk %s, Lk %s, observed peak overlap %d, observed-cap diagnostic %s, release / diagnostic %.2f, idle %s, relation %s, not honoured %s"
	legacyTimingMarkdownHeader      = "| cell | planning | release span | Wk | Lk | observed peak overlap | observed-cap diagnostic | release / diagnostic | idle | relation | not honoured |"
	legacyTimingDiagnosticNoteText  = "Legacy observed-overlap diagnostics: observed peak overlap does not establish a configured concurrency ceiling. The observed-cap diagnostic and release / diagnostic quotient are not validated theorem bounds or cross-tool speedups."
	legacyTimingStructuredGuideText = "For model claims on cells with recorded execution measurements, use their separate Formula calculations sections, selected inputs and applicability reasons."
)

var relations = map[string]bool{"publish": true, "build": true, "none": true}

// verdictPlan is the plan as verdict.json carries it: package to version, and
// what the tool was told.
type verdictPlan struct {
	Packages map[string]string `json:"packages"`
	Told     string            `json:"told"`
}

// UnmarshalJSON refuses figures without a ratio: lib/timing.py has none to
// give when there was no bound, and a missing ratio decoded as 0 would read as
// the fastest release in the table.
func (t *Timing) UnmarshalJSON(body []byte) error {
	var probe struct {
		Ratio *float64 `json:"ratio"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return err
	}
	if probe.Ratio == nil {
		return errors.New("timing ratio is missing or null")
	}
	type plain Timing
	return json.Unmarshal(body, (*plain)(t))
}

// newPlan sorts a recorded plan by package, so two reads of one record render
// one table.
func newPlan(recorded *verdictPlan) *Plan {
	if recorded == nil {
		return nil
	}
	plan := &Plan{Packages: []PlannedPackage{}, Told: recorded.Told}
	for name, version := range recorded.Packages {
		plan.Packages = append(plan.Packages, PlannedPackage{Name: name, Version: version})
	}
	sort.Slice(plan.Packages, func(i, j int) bool { return plan.Packages[i].Name < plan.Packages[j].Name })
	return plan
}

// validateTiming refuses figures no measurement produces, and figures over
// records that did not cover the release.
func validateTiming(t *Timing) error {
	if t == nil {
		return nil
	}
	if !relations[t.Relation] {
		return fmt.Errorf("timing relation %q is not publish, build or none", t.Relation)
	}
	if t.ObservedRelation != "" && !relations[t.ObservedRelation] {
		return fmt.Errorf("timing observed relation %q is not publish, build or none", t.ObservedRelation)
	}
	if len(t.Untimed) > 0 || len(t.Unknown) > 0 || t.Straddling > 0 {
		return fmt.Errorf("timing records do not cover the release: untimed %v, outside the graph %v, %d straddling",
			t.Untimed, t.Unknown, t.Straddling)
	}
	for name, seconds := range map[string]float64{
		"planningSeconds": t.PlanningSeconds, "makespanSeconds": t.MakespanSeconds, "idleSeconds": t.IdleSeconds,
		"work": t.Work, "criticalPath": t.CriticalPath, "bound": t.Bound, "ratio": t.Ratio,
		"planBuilds.seconds": t.PlanBuilds.Seconds,
	} {
		if seconds < 0 || math.IsNaN(seconds) {
			return fmt.Errorf("timing %s is %v", name, seconds)
		}
	}
	if t.Tasks.Build.Count < 1 || t.Tasks.Publish.Count < 1 {
		return fmt.Errorf("timing has %d builds and %d publications, and a release that shipped ran both",
			t.Tasks.Build.Count, t.Tasks.Publish.Count)
	}
	if t.Parallelism < 1 {
		return fmt.Errorf("timing parallelism is %d, and a release that ran a task ran it in a slot", t.Parallelism)
	}
	if math.Abs(t.Tasks.Build.Seconds+t.Tasks.Publish.Seconds-t.Work) > rounding {
		return fmt.Errorf("timing work %v is not its builds' %v and publications' %v", t.Work,
			t.Tasks.Build.Seconds, t.Tasks.Publish.Seconds)
	}
	if len(t.TaskTimes) != t.Tasks.Build.Count+t.Tasks.Publish.Count {
		return fmt.Errorf("timing lists %d tasks for %d builds and %d publications", len(t.TaskTimes),
			t.Tasks.Build.Count, t.Tasks.Publish.Count)
	}
	if t.CriticalPath > t.Work+rounding {
		return fmt.Errorf("timing critical path %v is longer than the work %v it is part of", t.CriticalPath, t.Work)
	}
	if t.IdleSeconds > t.MakespanSeconds+rounding {
		return fmt.Errorf("timing idle %v is longer than the makespan %v", t.IdleSeconds, t.MakespanSeconds)
	}
	bound := math.Max(t.CriticalPath, t.Work/float64(t.Parallelism))
	if math.Abs(t.Bound-bound) > rounding {
		return fmt.Errorf("timing bound %v is not max(Lk, Wk / s) = %v", t.Bound, bound)
	}
	if math.Abs(t.Ratio*t.Bound-t.MakespanSeconds) > rounding*(1+t.Ratio+t.Bound) {
		return fmt.Errorf("timing ratio %v is not the makespan %v over the bound %v", t.Ratio, t.MakespanSeconds, t.Bound)
	}
	if len(t.Violations) == 0 && t.MakespanSeconds < t.Bound-clockSlack {
		return fmt.Errorf("timing makespan %v beats the bound %v of a precedence it honoured", t.MakespanSeconds, t.Bound)
	}
	b := t.ConsumerBuilds
	if b.AfterBuild > b.Edges || b.AfterPublish > b.Edges {
		return fmt.Errorf("timing counts %d and %d consumer builds after their providers of %d", b.AfterBuild,
			b.AfterPublish, b.Edges)
	}
	return nil
}

// comparePlans sets every plan beside dispat's plan of the same experiment and
// scenario, read from that cell's own record.
func comparePlans(cells []Cell) {
	references := map[string]Cell{}
	for _, cell := range cells {
		if cell.Tool == referenceTool && cell.Plan != nil {
			references[cell.Experiment+"\x00"+cell.Scenario] = cell
		}
	}
	for i := range cells {
		plan := cells[i].Plan
		if plan == nil || cells[i].Tool == referenceTool {
			continue
		}
		reference, ok := references[cells[i].Experiment+"\x00"+cells[i].Scenario]
		if !ok {
			continue
		}
		plan.Reference = reference.ID
		plan.Differences = planDifferences(plan.Packages, reference.Plan.Packages)
		agrees := len(plan.Differences) == 0
		plan.Agrees = &agrees
	}
}

// planDifferences is every package the two plans do not release at one
// version, by name.
func planDifferences(packages, reference []PlannedPackage) []PlanDifference {
	versions, referenceVersions := map[string]string{}, map[string]string{}
	for _, p := range packages {
		versions[p.Name] = p.Version
	}
	for _, p := range reference {
		referenceVersions[p.Name] = p.Version
	}
	var names []string
	for name := range versions {
		names = append(names, name)
	}
	for name := range referenceVersions {
		if _, ok := versions[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var differences []PlanDifference
	for _, name := range names {
		if versions[name] != referenceVersions[name] {
			differences = append(differences, PlanDifference{Package: name, Version: versions[name], Reference: referenceVersions[name]})
		}
	}
	return differences
}

// group is packages that share one value, in the order the first of them
// came.
type group struct {
	value string
	names []string
}

func grouped(names, values []string) []group {
	var groups []group
	at := map[string]int{}
	for i, name := range names {
		j, ok := at[values[i]]
		if !ok {
			j = len(groups)
			at[values[i]] = j
			groups = append(groups, group{value: values[i]})
		}
		groups[j].names = append(groups[j].names, name)
	}
	return groups
}

// plannedOf renders a plan's packages by version: `api, cli, ui at 1.0.1;
// core at 1.1.0`, a group of more than namedGroup counted as `40 packages`.
func plannedOf(packages []PlannedPackage) string {
	names, versions := make([]string, len(packages)), make([]string, len(packages))
	for i, p := range packages {
		names[i], versions[i] = p.Name, p.Version
	}
	var parts []string
	for _, g := range grouped(names, versions) {
		who := strings.Join(g.names, ", ")
		if len(g.names) > namedGroup {
			who = counted(len(g.names), "package")
		}
		parts = append(parts, who+" at "+g.value)
	}
	return strings.Join(parts, "; ")
}

// planOf renders a cell's plan beside dispat's, the way the site's Plans table
// does (packages/docs's timing.ts writes the same strings, and both are held
// to packages/docs/plugins/test-report/testdata/texts.json):
//
//	not measured                          the cell recorded no plan
//	the reference: api, cli, ui at 1.0.1; core at 1.1.0    dispat's own plan
//	no dispat plan to compare             no dispat cell of the scenario recorded one
//	agrees with dispat, 6 packages        the same packages at the same versions
//
// and otherwise what differs, in three clauses that appear when they apply:
// `also docs 1.0.1, theme 1.0.1; not chain2 1.0.1; core 1.2.0 for dispat's
// 1.1.0`, the versions being this plan's except after `not`, which names the
// release only dispat plans.
func planOf(cell Cell) string {
	plan := cell.Plan
	switch {
	case plan == nil:
		return "not measured"
	case cell.Tool == referenceTool:
		return "the reference: " + plannedOf(plan.Packages)
	case plan.Agrees == nil:
		return "no dispat plan to compare"
	case *plan.Agrees:
		return "agrees with dispat, " + counted(len(plan.Packages), "package")
	}
	var also, not, changed []string
	for _, d := range plan.Differences {
		switch {
		case d.Reference == "":
			also = append(also, d.Package+" "+d.Version)
		case d.Version == "":
			not = append(not, d.Package+" "+d.Reference)
		default:
			changed = append(changed, fmt.Sprintf("%s %s for dispat's %s", d.Package, d.Version, d.Reference))
		}
	}
	var clauses []string
	if len(also) > 0 {
		clauses = append(clauses, "also "+strings.Join(also, ", "))
	}
	if len(not) > 0 {
		clauses = append(clauses, "not "+strings.Join(not, ", "))
	}
	if len(changed) > 0 {
		clauses = append(clauses, strings.Join(changed, ", "))
	}
	return strings.Join(clauses, "; ")
}

// stateEntries is a final state as the tables print it, `core=1.1.0/consistent`
// per package. A state of more than wholeState packages prints a group of
// more than namedGroup packages sharing a version and a state as one entry,
// `40 packages=1.0.1/consistent` (the site's timing.ts writes the same).
func stateEntries(packages []PackageState) []string {
	names, values := make([]string, len(packages)), make([]string, len(packages))
	for i, p := range packages {
		names[i], values[i] = p.Name, p.Registry+"/"+p.State
	}
	var entries []string
	for _, g := range grouped(names, values) {
		if len(packages) > wholeState && len(g.names) > namedGroup {
			entries = append(entries, counted(len(g.names), "package")+"="+g.value)
			continue
		}
		for _, name := range g.names {
			entries = append(entries, name+"="+g.value)
		}
	}
	return entries
}

// seconds is a duration as the tables print one, to the millisecond the
// records are rounded to.
func seconds(value float64) string { return fmt.Sprintf("%.3fs", value) }

// timingCells is the cells that carry figures or a plan, in the report's
// order.
func timingCells(cells []Cell) []Cell {
	var out []Cell
	for _, cell := range cells {
		if cell.Timing != nil || cell.Plan != nil {
			out = append(out, cell)
		}
	}
	return out
}

// relationOf is the declared relation, with the one the records show when
// they differ: `build (records: publish)`.
func relationOf(t *Timing) string {
	if t.ObservedRelation == "" || t.ObservedRelation == t.Relation {
		return t.Relation
	}
	return fmt.Sprintf("%s (records: %s)", t.Relation, t.ObservedRelation)
}

// violationsOf is the edges a release did not honour, `none` or
// `core build > ui build, ...`.
func violationsOf(t *Timing) string {
	if len(t.Violations) == 0 {
		return "none"
	}
	edges := make([]string, len(t.Violations))
	for i, e := range t.Violations {
		edges[i] = e.Before + " > " + e.After
	}
	return strings.Join(edges, ", ")
}

// statsOf is one kind's tasks, `41, median 0.502s, max 0.509s, total
// 20.585s`, and `, 1 failed` when any was.
func statsOf(s TaskStats) string {
	if s.Count == 0 {
		return "none"
	}
	text := fmt.Sprintf("%d, median %s, max %s, total %s", s.Count, seconds(s.Median), seconds(s.Max), seconds(s.Seconds))
	if s.Failed > 0 {
		text += fmt.Sprintf(", %d failed", s.Failed)
	}
	return text
}

func timingOf(cell Cell) string {
	t := cell.Timing
	if t == nil {
		return "not measured"
	}
	return fmt.Sprintf(legacyTimingPlainFormat,
		seconds(t.PlanningSeconds), seconds(t.MakespanSeconds), seconds(t.Work), seconds(t.CriticalPath),
		t.Parallelism, seconds(t.Bound), t.Ratio, seconds(t.IdleSeconds), relationOf(t), violationsOf(t))
}

// tasksOf renders a cell's tasks by kind as one line.
func tasksOf(cell Cell) string {
	t := cell.Timing
	if t == nil {
		return "not measured"
	}
	return fmt.Sprintf("builds %s; publications %s; npm publish %s; builds while planning %d",
		statsOf(t.Tasks.Build), statsOf(t.Tasks.Publish), statsOf(t.Tasks.NPMPublish), t.PlanBuilds.Count)
}
