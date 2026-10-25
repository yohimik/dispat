package main

// The timing cells' half of the experiments: the figures lib/timing.py wrote
// and the plan each tool printed, held to what a measurement could have
// produced, the plans compared with dispat's, and both rendered.

import (
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

// verdictPlan is the plan as verdict.json carries it: package to version.
type verdictPlan struct {
	Packages map[string]string `json:"packages"`
}

// newPlan sorts a recorded plan by package, so two reads of one record render
// one table.
func newPlan(recorded *verdictPlan) *Plan {
	if recorded == nil {
		return nil
	}
	plan := &Plan{Packages: []PlannedPackage{}}
	for name, version := range recorded.Packages {
		plan.Packages = append(plan.Packages, PlannedPackage{Name: name, Version: version})
	}
	sort.Slice(plan.Packages, func(i, j int) bool { return plan.Packages[i].Name < plan.Packages[j].Name })
	return plan
}

// validateTiming refuses figures no measurement produces: a negative
// duration, no task slot, a longest chain longer than all the work, or a
// bound that is not max(Lk, Wk / s).
func validateTiming(timing *Timing) error {
	if timing == nil {
		return nil
	}
	if timing.Relation != "build" && timing.Relation != "publish" {
		return fmt.Errorf("timing relation %q is neither build nor publish", timing.Relation)
	}
	for name, seconds := range map[string]float64{
		"planningSeconds": timing.PlanningSeconds, "makespanSeconds": timing.MakespanSeconds,
		"work": timing.Work, "criticalPath": timing.CriticalPath, "bound": timing.Bound,
	} {
		if seconds < 0 || math.IsNaN(seconds) {
			return fmt.Errorf("timing %s is %v", name, seconds)
		}
	}
	if timing.Parallelism < 1 {
		return fmt.Errorf("timing parallelism is %d, and a release that ran a task ran it in a slot", timing.Parallelism)
	}
	if timing.CriticalPath > timing.Work+rounding {
		return fmt.Errorf("timing critical path %v is longer than the work %v it is part of", timing.CriticalPath, timing.Work)
	}
	bound := math.Max(timing.CriticalPath, timing.Work/float64(timing.Parallelism))
	if math.Abs(timing.Bound-bound) > rounding {
		return fmt.Errorf("timing bound %v is not max(Lk, Wk / s) = %v", timing.Bound, bound)
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

// planOf renders a cell's plan beside dispat's, the way the site's Plans table
// does (packages/docs's timing.ts writes the same strings):
//
//	not measured                      the cell recorded no plan
//	the reference, 4 packages         dispat's own plan
//	no dispat plan to compare         no dispat cell of the scenario recorded one
//	agrees with dispat, 6 packages    the same packages at the same versions
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
		return "the reference, " + counted(len(plan.Packages), "package")
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

// timingOf renders a cell's figures as one line, `planning 0.009s, makespan
// 1.942s, Wk 3.442s, Lk 1.399s, s 4, bound 1.399s, ratio 1.39`.
func timingOf(cell Cell) string {
	t := cell.Timing
	if t == nil {
		return "not measured"
	}
	return fmt.Sprintf("planning %s, makespan %s, Wk %s, Lk %s, s %d, bound %s, ratio %.2f",
		seconds(t.PlanningSeconds), seconds(t.MakespanSeconds), seconds(t.Work), seconds(t.CriticalPath),
		t.Parallelism, seconds(t.Bound), t.Ratio)
}
