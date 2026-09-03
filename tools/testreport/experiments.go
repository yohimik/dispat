package main

// The release experiments' half: reading what tests/experiments left in a
// results folder, and rendering it.
//
// The harness writes two files per cell that anyone else has a use for.
// verdict.json is what the run decided: the protocol's steps with their exit
// codes, the expectations with whether they held, and the one boolean the
// exit code came from. observations.jsonl is what the run saw, one object per
// step, and the last of them is the state the fixture ended in. Everything
// below is those two files, read the way the site and a job summary both want
// them: one row per cell, one campaign per image.
//
// It is a reader rather than a re-implementation on purpose. The verdict is
// the harness's, taken as it stands: a report that recomputed whether a cell
// passed would be a second opinion about a run it did not perform, and the
// two would disagree the first time either changed.

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// experimentsDirName is where a campaign's cells land under coverage/, beside
// the testlog and benchlog folders the suites and benchmarks leave.
const experimentsDirName = "experiments"

// baselineState is the state of a package the run never touched. It is
// dropped from a final state: a table column showing all six packages when a
// release moved four says nothing about the four.
const baselineState = "baseline"

// verdict is verdict.json as the harness writes it. Steps and Checks decode
// straight into the report's own types, because the harness's field names are
// where those names came from.
type verdict struct {
	Experiment string  `json:"experiment"`
	Tool       string  `json:"tool"`
	Scenario   string  `json:"scenario"`
	Dispat     string  `json:"dispat"`
	Steps      []Step  `json:"steps"`
	Checks     []Check `json:"checks"`
	Passed     bool    `json:"passed"`
}

// observation is one line of observations.jsonl, reduced to the part a report
// carries. The file holds a great deal more (every tag, both sides' shas, the
// marks), which belongs in the artifact a failure is read from rather than in
// a page.
type observation struct {
	Label    string                     `json:"label"`
	Packages map[string]observedPackage `json:"packages"`
}

type observedPackage struct {
	Registry string `json:"registry"`
	State    string `json:"state"`
}

// readExperiments folds every cell in a results folder into the report.
//
// An absent folder is a warning rather than an error, for the same reason an
// absent benchmark stream is: a run that measured nothing still has coverage
// and a suite to report, and the docs page renders what it has. A folder that
// is there and malformed is an error, because that is a run whose records
// cannot be trusted rather than a run that made none.
func readExperiments(dir string) (Experiments, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		logf(levelWarn, "no experiments in %s: the experiments section will be empty", dir)
		return Experiments{}, nil
	}
	if err != nil {
		return Experiments{}, err
	}
	var out Experiments
	for _, entry := range entries {
		if !entry.IsDir() {
			// The campaign leaves a console log per cell and a rendered table
			// beside the folders; neither is a cell.
			logf(levelDebug, "experiments: skipping %s, not a cell folder", entry.Name())
			continue
		}
		cell, err := readCell(dir, entry.Name())
		if err != nil {
			return Experiments{}, err
		}
		if cell == nil {
			continue
		}
		logf(levelDebug, "experiments: %s: %s %s, %d steps, %d/%d expectations, %s",
			cell.ID, cell.Tool, cell.Dispat, len(cell.Steps), held(cell.Checks), len(cell.Checks),
			cell.Final.Label)
		out.Cells = append(out.Cells, *cell)
	}
	if len(out.Cells) == 0 {
		logf(levelWarn, "no cells with a verdict in %s: the experiments section will be empty", dir)
		return out, nil
	}
	// ReadDir sorts by name already; sorting again states that the order is
	// the report's rather than the file system's.
	sort.Slice(out.Cells, func(i, j int) bool { return out.Cells[i].ID < out.Cells[j].ID })
	out.Version = commonVersion(out.Cells)
	logf(levelInfo, "experiments: %d cells on dispat %s, %d holding",
		len(out.Cells), or(out.Version, "(mixed)"), holding(out.Cells))
	return out, nil
}

// readCell reads one cell's folder. A folder without a verdict is skipped
// with a warning: a cell that was cancelled or is still running has one, and
// refusing the whole report over it would lose the eleven that finished.
func readCell(dir, id string) (*Cell, error) {
	path := filepath.Join(dir, id, "verdict.json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		logf(levelWarn, "%s has no verdict.json: skipped", filepath.Join(dir, id))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v verdict
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	version, platform := parseDispatVersion(v.Dispat)
	cell := &Cell{
		ID: id, Experiment: v.Experiment, Scenario: v.Scenario, Tool: v.Tool,
		Dispat: version, Platform: platform,
		Steps: v.Steps, Checks: v.Checks, Passed: v.Passed,
	}
	last, err := readLastObservation(filepath.Join(dir, id, "observations.jsonl"))
	if err != nil {
		return nil, err
	}
	if last == nil {
		logf(levelWarn, "%s recorded no observations: its final state will be empty", id)
		return cell, nil
	}
	cell.Final = finalState(last)
	return cell, nil
}

// readLastObservation reads the state a run ended in.
//
// A decoder loop rather than a read of the last line, because the file has
// been two shapes: one object per line as the harness writes it now, and
// pretty-printed objects back to back as it wrote them before. A decoder
// handles both, and handles the thing a brace-counting reader gets wrong,
// which is a brace inside a string. The last object wins, because the
// observations are appended in the order they were taken.
//
// An absent file is not an error: a cell that fell over before its first
// observation has one, and its verdict is still worth reporting.
func readLastObservation(path string) (*observation, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReader(f))
	var last *observation
	for {
		var o observation
		if err := dec.Decode(&o); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		copied := o
		last = &copied
	}
	return last, nil
}

// finalState turns one observation into the state a table shows: the packages
// the run touched, by name, with the baseline dropped.
func finalState(o *observation) State {
	state := State{Label: o.Label}
	for name, p := range o.Packages {
		if p.State == baselineState {
			continue
		}
		state.Packages = append(state.Packages, PackageState{Name: name, Registry: p.Registry, State: p.State})
	}
	// By name, because a Go map's order is deliberately not one and a report
	// that reordered its own rows between two runs of the same input would be
	// unreadable as a diff.
	sort.Slice(state.Packages, func(i, j int) bool { return state.Packages[i].Name < state.Packages[j].Name })
	return state
}

// parseDispatVersion splits the binary's own version line into the release
// and the platform it was built for:
//
//	dispat 1.7.0 (linux_arm64)  ->  1.7.0, linux_arm64
//
// A line the binary did not print the way it usually does is returned whole
// as the version, with no platform. Guessing would be worse: the version is
// what names the image the whole page is about.
func parseDispatVersion(line string) (string, string) {
	fields := strings.Fields(line)
	if len(fields) > 0 && fields[0] == "dispat" {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", ""
	}
	version := fields[0]
	platform := ""
	if len(fields) > 1 {
		platform = strings.Trim(fields[1], "()")
	}
	return version, platform
}

// commonVersion is the release every cell ran against, or empty when they
// disagree.
//
// A campaign is about one image. Two versions in one folder means results
// from two runs were merged, and a page naming either of them would be naming
// the wrong one for half its rows, so it names neither and says so.
func commonVersion(cells []Cell) string {
	version := ""
	for _, cell := range cells {
		if cell.Dispat == "" {
			continue
		}
		if version == "" {
			version = cell.Dispat
			continue
		}
		if cell.Dispat != version {
			logf(levelWarn, "cells ran against more than one dispat (%s and %s): the campaign's version is left empty",
				version, cell.Dispat)
			return ""
		}
	}
	return version
}

// held counts the expectations that did.
func held(checks []Check) int {
	n := 0
	for _, c := range checks {
		if c.OK {
			n++
		}
	}
	return n
}

// holding counts the cells whose expectations all held.
func holding(cells []Cell) int {
	n := 0
	for _, cell := range cells {
		if cell.Passed {
			n++
		}
	}
	return n
}

// or is the fallback for a field that is legitimately empty.
func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// stepsOf renders a cell's protocol as the one-line form the table carries:
// `release=0 status=0 rerun=0`.
func stepsOf(cell Cell) string {
	parts := make([]string, 0, len(cell.Steps))
	for _, s := range cell.Steps {
		parts = append(parts, fmt.Sprintf("%s=%d", s.Name, s.Exit))
	}
	return strings.Join(parts, " ")
}

// stateOf renders a cell's final state: `core=1.1.0/consistent cli=1.0.1/consistent`.
func stateOf(cell Cell) string {
	parts := make([]string, 0, len(cell.Final.Packages))
	for _, p := range cell.Final.Packages {
		parts = append(parts, fmt.Sprintf("%s=%s/%s", p.Name, p.Registry, p.State))
	}
	return strings.Join(parts, " ")
}

// outcomeOf is the cell's verdict as a phrase: what held, or what did not.
func outcomeOf(cell Cell) string {
	if cell.Passed {
		return "holds"
	}
	var failed []string
	for _, c := range cell.Checks {
		if !c.OK {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) == 0 {
		// A verdict that failed with nothing to point at is the harness's
		// "no expectations were recorded" case reaching a reader.
		return "did not hold"
	}
	return strings.Join(failed, "; ")
}

// headline opens both renderings with what the campaign is: how many cells,
// on which release.
//
// The two degenerate cases are spelled out rather than formatted into the
// same sentence, because "0 cells on dispat " and "12 cells on dispat " are
// both sentences a reader would take as a claim about a release.
func headline(e Experiments) string {
	cells := fmt.Sprintf("%d cells", len(e.Cells))
	switch {
	case len(e.Cells) == 0:
		return "no cells recorded"
	case len(e.Cells) == 1:
		cells = "1 cell"
	}
	if e.Version == "" {
		return cells + ", on more than one dispat"
	}
	return cells + " on dispat " + e.Version
}

// writeExperimentsMarkdown renders the campaign as the table a job summary
// shows. Pipes in a check's own wording are escaped: an expectation is a
// sentence somebody wrote, and one of them containing a pipe would otherwise
// split a row into columns that do not exist.
func writeExperimentsMarkdown(w io.Writer, e Experiments) error {
	out := bufio.NewWriter(w)
	fmt.Fprintf(out, "%s\n\n", headline(e))
	fmt.Fprintln(out, "| cell | tool | dispat | steps | checks | outcome | final state |")
	fmt.Fprintln(out, "|---|---|---|---|---|---|---|")
	for _, cell := range e.Cells {
		fmt.Fprintf(out, "| %s | %s | %s | `%s` | %d/%d | %s | `%s` |\n",
			escapePipes(cell.ID), escapePipes(cell.Tool), escapePipes(cell.Dispat),
			escapePipes(stepsOf(cell)), held(cell.Checks), len(cell.Checks),
			escapePipes(outcomeOf(cell)), escapePipes(stateOf(cell)))
	}
	return out.Flush()
}

// writeExperimentsPlain renders the campaign for a terminal: the same facts
// without a table nobody can read at eighty columns.
func writeExperimentsPlain(w io.Writer, e Experiments) error {
	out := bufio.NewWriter(w)
	fmt.Fprintf(out, "%s\n", headline(e))
	for _, cell := range e.Cells {
		fmt.Fprintf(out, "\n%s\n", cell.ID)
		fmt.Fprintf(out, "  %s  %s\n", cell.Tool, strings.TrimSpace(cell.Dispat+" "+cell.Platform))
		fmt.Fprintf(out, "  steps: %s\n", stepsOf(cell))
		fmt.Fprintf(out, "  checks: %d/%d  %s\n", held(cell.Checks), len(cell.Checks), outcomeOf(cell))
		fmt.Fprintf(out, "  state: %s\n", stateOf(cell))
	}
	return out.Flush()
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// experiments is the verb: the campaign's table, from a results folder.
//
//	testreport experiments [-markdown] [dir]
//
// It exists so the summary a job prints and the page a release publishes are
// one renderer over one set of records. A second implementation in shell or
// python is how the two came to say different things about the same run.
func experiments(args []string) error {
	fs := flag.NewFlagSet("experiments", flag.ContinueOnError)
	markdown := fs.Bool("markdown", false, "render the table as markdown for a job summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("experiments takes one results folder, got %d", fs.NArg())
	}
	dir := filepath.Join("coverage", experimentsDirName)
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	campaign, err := readExperiments(dir)
	if err != nil {
		return err
	}
	// Buffered, and flushed once: a table written a cell at a time to a pipe
	// that a job summary is reading is a table that arrives interleaved with
	// whatever else the step said.
	out := bufio.NewWriter(os.Stdout)
	if *markdown {
		if err := writeExperimentsMarkdown(out, campaign); err != nil {
			return err
		}
	} else if err := writeExperimentsPlain(out, campaign); err != nil {
		return err
	}
	return out.Flush()
}
