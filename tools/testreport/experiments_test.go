package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three cells under testdata/experiments are real records, copied out of
// a campaign rather than written for a test: a passing midrelease cell with a
// scenario, a passing orphan cell without one, and a compared tool's cell
// whose expectations did not all hold. orphan-lerna keeps the shape the
// harness used to write, pretty-printed objects back to back, which is what
// the decoder loop exists for.
const fixtures = "testdata/experiments"

// The failure this fences: a reader that took the folder's own ordering, or
// recomputed a verdict of its own, would report a campaign that disagrees with
// the exit codes the run produced. Every field here is read from what the
// harness wrote and nothing is derived.
func TestReadExperimentsReadsEveryCell(t *testing.T) {
	campaign, err := readExperiments(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(campaign.Cells) != 3 {
		t.Fatalf("got %d cells, want the three fixtures", len(campaign.Cells))
	}
	if campaign.Version != "1.7.0" {
		t.Errorf("version = %q, want the release every cell ran against", campaign.Version)
	}
	want := []string{"midrelease-conflict-dispat", "orphan-dispat", "orphan-lerna"}
	for i, id := range want {
		if campaign.Cells[i].ID != id {
			t.Fatalf("cell %d is %q, want %q: cells are ordered by name", i, campaign.Cells[i].ID, id)
		}
	}

	conflict := campaign.Cells[0]
	if conflict.Experiment != "midrelease" || conflict.Scenario != "conflict" || conflict.Tool != "dispat" {
		t.Errorf("got %+v, want the midrelease conflict cell for dispat", conflict)
	}
	if conflict.Platform != "linux_arm64" {
		t.Errorf("platform = %q, want the one the binary reported", conflict.Platform)
	}
	if !conflict.Passed || held(conflict.Checks) != 14 || len(conflict.Checks) != 14 {
		t.Errorf("got %d/%d, passed=%v; want 14/14 holding", held(conflict.Checks), len(conflict.Checks), conflict.Passed)
	}
	if len(conflict.Steps) != 3 || conflict.Steps[0].Name != "release" || conflict.Steps[0].Exit != 0 {
		t.Errorf("got steps %+v, want release, status, rerun", conflict.Steps)
	}
	if conflict.Final.Label != "after-rerun" {
		t.Errorf("final label = %q, want the last observation the run took", conflict.Final.Label)
	}
	if got := names(conflict.Final); got != "api cli core ui" {
		t.Errorf("final packages = %q, want the four the run touched, sorted", got)
	}

	orphan := campaign.Cells[1]
	if orphan.Scenario != "" {
		t.Errorf("scenario = %q, want empty: the orphan experiment has only one", orphan.Scenario)
	}
	if !orphan.Passed || len(orphan.Checks) != 7 {
		t.Errorf("got %d checks, passed=%v; want 7/7 holding", len(orphan.Checks), orphan.Passed)
	}

	lerna := campaign.Cells[2]
	if lerna.Passed {
		t.Error("the lerna cell is recorded as passing; a compared tool's record is not an expectation")
	}
	if held(lerna.Checks) != 3 || len(lerna.Checks) != 5 {
		t.Errorf("got %d/%d, want lerna's 3/5", held(lerna.Checks), len(lerna.Checks))
	}
	if len(lerna.Steps) != 6 {
		t.Errorf("got %d steps, want the six the recovery needed", len(lerna.Steps))
	}
	if lerna.Final.Label == "" || len(lerna.Final.Packages) == 0 {
		t.Errorf("got %+v, want a final state read from the pretty-printed observations", lerna.Final)
	}
}

// names is the final state's packages as one string, for an assertion that
// reads like the table's own column.
func names(s State) string {
	var out []string
	for _, p := range s.Packages {
		out = append(out, p.Name)
	}
	return strings.Join(out, " ")
}

// A campaign is about one image. Two versions in one folder means two runs
// were merged, and naming either would be naming the wrong one for half the
// rows.
func TestCommonVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		cells []Cell
		want  string
	}{
		"one release":       {[]Cell{{Dispat: "1.7.1"}, {Dispat: "1.7.1"}}, "1.7.1"},
		"a cell without":    {[]Cell{{Dispat: ""}, {Dispat: "1.7.1"}}, "1.7.1"},
		"two releases":      {[]Cell{{Dispat: "1.7.0"}, {Dispat: "1.7.1"}}, ""},
		"nothing at all":    {[]Cell{{Dispat: ""}}, ""},
		"no cells whatever": {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := commonVersion(tc.cells); got != tc.want {
				t.Fatalf("commonVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// The version line is what names the image the whole page is about, so a line
// the binary did not print the usual way is carried whole rather than guessed
// at.
func TestParseDispatVersion(t *testing.T) {
	for _, tc := range []struct {
		line, version, platform string
	}{
		{"dispat 1.7.0 (linux_arm64)", "1.7.0", "linux_arm64"},
		{"dispat 1.8.0-rc.3 (linux_amd64)", "1.8.0-rc.3", "linux_amd64"},
		{"dispat 1.7.0", "1.7.0", ""},
		{"dispat dev (darwin_arm64)", "dev", "darwin_arm64"},
		{"  dispat 1.7.0 (linux_amd64)  ", "1.7.0", "linux_amd64"},
		{"", "", ""},
		{"dispat", "", ""},
	} {
		version, platform := parseDispatVersion(tc.line)
		if version != tc.version || platform != tc.platform {
			t.Errorf("parseDispatVersion(%q) = %q, %q; want %q, %q",
				tc.line, version, platform, tc.version, tc.platform)
		}
	}
}

// The failure this fences: a campaign folder also holds a console log per cell
// and the rendered table, and a reader treating every entry as a cell would
// fail the report on the first of them.
func TestReadExperimentsIgnoresEverythingThatIsNotACell(t *testing.T) {
	dir := t.TempDir()
	writeCell(t, dir, "orphan-dispat", `{"tool":"dispat","passed":true,"checks":[{"check":"a","ok":true}]}`, "")
	write(t, filepath.Join(dir, "orphan-dispat.log"), "docker run said things\n")
	write(t, filepath.Join(dir, "experiments.md"), "| cell |\n")
	campaign, err := readExperiments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(campaign.Cells) != 1 || campaign.Cells[0].ID != "orphan-dispat" {
		t.Fatalf("got %+v, want the one cell folder", campaign.Cells)
	}
}

// A run that measured nothing still has coverage and a suite to report, so an
// absent or empty folder leaves the section empty rather than failing the
// build. An empty campaign is what a page renders as "not measured".
func TestReadExperimentsWithoutRecords(t *testing.T) {
	t.Run("absent folder", func(t *testing.T) {
		campaign, err := readExperiments(filepath.Join(t.TempDir(), "nothing-here"))
		if err != nil {
			t.Fatalf("an absent folder is a warning, not an error: %v", err)
		}
		if len(campaign.Cells) != 0 || campaign.Version != "" {
			t.Fatalf("got %+v, want an empty campaign", campaign)
		}
	})
	t.Run("empty folder", func(t *testing.T) {
		campaign, err := readExperiments(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(campaign.Cells) != 0 {
			t.Fatalf("got %+v, want an empty campaign", campaign)
		}
	})
}

// A cell that was cancelled or is still running has no verdict. Refusing the
// whole report over it would lose the eleven that finished.
func TestReadExperimentsSkipsACellWithoutAVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "orphan-nx"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCell(t, dir, "orphan-dispat", `{"tool":"dispat","passed":true}`, "")
	campaign, err := readExperiments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(campaign.Cells) != 1 || campaign.Cells[0].ID != "orphan-dispat" {
		t.Fatalf("got %+v, want the cell that finished", campaign.Cells)
	}
}

// A record that cannot be parsed is a record nobody can trust, so it stops the
// report and the error names the file rather than the folder.
func TestReadExperimentsRefusesMalformedRecords(t *testing.T) {
	t.Run("verdict", func(t *testing.T) {
		dir := t.TempDir()
		writeCell(t, dir, "orphan-dispat", `{"tool": nope}`, "")
		_, err := readExperiments(dir)
		if err == nil || !strings.Contains(err.Error(), "verdict.json") {
			t.Fatalf("err = %v, want one naming verdict.json", err)
		}
	})
	t.Run("observations", func(t *testing.T) {
		dir := t.TempDir()
		writeCell(t, dir, "orphan-dispat", `{"tool":"dispat"}`, "{\"label\":\"before\"}\n{not json}\n")
		_, err := readExperiments(dir)
		if err == nil || !strings.Contains(err.Error(), "observations.jsonl") {
			t.Fatalf("err = %v, want one naming observations.jsonl", err)
		}
	})
}

// A record that cannot be opened at all is not a record that is absent. The
// three unreadable cases each stop the report rather than reading as a run
// that measured nothing, which is what an errors.Is check on os.ErrNotExist
// alone would have made of them.
func TestReadExperimentsRefusesUnreadableRecords(t *testing.T) {
	t.Run("the campaign is a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "experiments")
		write(t, path, "not a folder\n")
		if _, err := readExperiments(path); err == nil {
			t.Fatal("want an error rather than an empty campaign")
		}
	})
	t.Run("the verdict is a folder", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "orphan-dispat", "verdict.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := readExperiments(dir); err == nil {
			t.Fatal("want an error rather than a skipped cell")
		}
	})
	t.Run("the observations are a folder", func(t *testing.T) {
		dir := t.TempDir()
		writeCell(t, dir, "orphan-dispat", `{"tool":"dispat"}`, "")
		if err := os.MkdirAll(filepath.Join(dir, "orphan-dispat", "observations.jsonl"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := readExperiments(dir); err == nil {
			t.Fatal("want an error rather than an empty final state")
		}
	})
}

// The verb reports a folder it could not read rather than printing a table of
// nothing: a job summary saying "0 cells" for a campaign that ran twelve is
// worse than a step that failed.
func TestExperimentsVerbReportsAnUnreadableCampaign(t *testing.T) {
	dir := t.TempDir()
	writeCell(t, dir, "orphan-dispat", `{`, "")
	for _, args := range [][]string{{dir}, {"-markdown", dir}} {
		if err := experiments(args, io.Discard); err == nil {
			t.Fatalf("experiments(%q) succeeded; want the malformed record reported", args)
		}
	}
}

// The observations are appended in the order they were taken, so the last is
// the state the run ended in. A brace-counting reader gets that wrong the
// moment a commit subject contains one, which release commits routinely do.
func TestReadLastObservationTakesTheLastAndSurvivesBracesInStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observations.jsonl")
	write(t, path, `{"label":"before","packages":{"core":{"registry":"1.0.0","state":"baseline"}}}
{"label":"after","packages":{"core":{"registry":"1.1.0","state":"chore(release): {core@1.1.0}"}}}
`)
	last, err := readLastObservation(path)
	if err != nil {
		t.Fatal(err)
	}
	if last == nil || last.Label != "after" {
		t.Fatalf("got %+v, want the last observation", last)
	}
	if got := last.Packages["core"].State; got != "chore(release): {core@1.1.0}" {
		t.Fatalf("state = %q, want the braces inside the string kept", got)
	}
}

// An absent observations file is a cell that fell over early; a path that
// cannot be opened for any other reason is a broken record, and the two must
// not read the same. os.ErrNotExist alone would fold the second into the
// first.
func TestReadLastObservationSeparatesAbsentFromUnopenable(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "observations.jsonl")
	last, err := readLastObservation(absent)
	if err != nil || last != nil {
		t.Fatalf("got %+v, %v; want nothing and no error for an absent file", last, err)
	}
	// A file where a folder should be: ENOTDIR, which is not ErrNotExist.
	notADir := filepath.Join(dir, "cell")
	write(t, notADir, "this is a file\n")
	if _, err := readLastObservation(filepath.Join(notADir, "observations.jsonl")); err == nil {
		t.Fatal("want an error rather than an empty final state")
	}
}

// A campaign with nothing in it says so, rather than formatting zero into a
// sentence that reads as a claim about a release.
func TestHeadlineOfAnEmptyCampaign(t *testing.T) {
	var out strings.Builder
	if err := writeExperimentsPlain(&out, Experiments{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "no cells recorded" {
		t.Fatalf("got %q, want the empty campaign said plainly", got)
	}
}

// A cell that fell over before its first observation still has a verdict
// worth reporting, and its final state is simply empty.
func TestReadCellWithoutObservations(t *testing.T) {
	dir := t.TempDir()
	writeCell(t, dir, "orphan-dispat", `{"tool":"dispat","passed":false,"checks":[{"check":"a","ok":false}]}`, "")
	cell, err := readCell(dir, "orphan-dispat")
	if err != nil {
		t.Fatal(err)
	}
	if cell == nil {
		t.Fatal("want the cell, not a skip")
	}
	if cell.Final.Label != "" || len(cell.Final.Packages) != 0 {
		t.Fatalf("final = %+v, want it empty", cell.Final)
	}
}

// A package the run never touched says nothing about the run, and a map's
// order is deliberately not one: the state a table shows is the touched
// packages, by name.
func TestFinalStateDropsTheBaselineAndSorts(t *testing.T) {
	o := &observation{Label: "after", Packages: map[string]observedPackage{
		"ui":    {Registry: "1.0.1", State: "consistent"},
		"theme": {Registry: "1.0.0", State: "baseline"},
		"core":  {Registry: "1.1.0", State: "consistent"},
		"cli":   {Registry: "1.0.0", State: "orphan"},
	}}
	got := finalState(o)
	if got.Label != "after" {
		t.Errorf("label = %q", got.Label)
	}
	if names(got) != "cli core ui" {
		t.Fatalf("packages = %q, want the three touched ones, sorted", names(got))
	}
	if got.Packages[0].Registry != "1.0.0" || got.Packages[0].State != "orphan" {
		t.Errorf("got %+v, want cli's refused upload recorded as it stands", got.Packages[0])
	}
}

// The failure this fences: an expectation is a sentence somebody wrote, and
// one containing a pipe would split a markdown row into columns that do not
// exist, silently shifting every cell after it.
func TestWriteExperimentsMarkdown(t *testing.T) {
	campaign := Experiments{Version: "1.7.1", Cells: []Cell{
		{
			ID: "orphan-dispat", Tool: "dispat", Dispat: "1.7.1",
			Steps:  []Step{{Name: "release1", Exit: 1}, {Name: "release2", Exit: 0}},
			Checks: []Check{{Name: "a | b", OK: false}, {Name: "c", OK: true}},
			Final: State{Label: "after", Packages: []PackageState{
				{Name: "core", Registry: "1.1.0", State: "consistent"},
			}},
		},
	}}
	var out strings.Builder
	if err := writeExperimentsMarkdown(&out, campaign); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"1 cell on dispat 1.7.1",
		"| cell | tool | dispat | steps | checks | outcome | final state |",
		"`release1=1 release2=0`",
		"| 1/2 |",
		`a \| b`,
		"`core=1.1.0/consistent`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the table does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| a | b |") {
		t.Errorf("an unescaped pipe split a row:\n%s", got)
	}
}

// The plain rendering is what a terminal gets, and it names the platform: a
// reading taken on a laptop is not the recorded one.
func TestWriteExperimentsPlain(t *testing.T) {
	campaign := Experiments{Version: "1.7.1", Cells: []Cell{{
		ID: "orphan-dispat", Tool: "dispat", Dispat: "1.7.1", Platform: "linux_amd64",
		Passed: true,
		Steps:  []Step{{Name: "release1", Exit: 1}},
		Checks: []Check{{Name: "a", OK: true}},
		Final:  State{Packages: []PackageState{{Name: "core", Registry: "1.1.0", State: "consistent"}}},
	}}}
	var out strings.Builder
	if err := writeExperimentsPlain(&out, campaign); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"1 cell on dispat 1.7.1",
		"orphan-dispat",
		"1.7.1 linux_amd64",
		"steps: release1=1",
		"checks: 1/1  holds",
		"state: core=1.1.0/consistent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary does not carry %q:\n%s", want, got)
		}
	}
}

// A campaign nobody could name a version for says so rather than picking one,
// and a verdict that failed with nothing to point at is the harness's
// "no expectations were recorded" case reaching a reader.
func TestRenderingAnUnnameableCampaign(t *testing.T) {
	campaign := Experiments{Cells: []Cell{{ID: "orphan-nx", Tool: "nx", Passed: false}}}
	var out strings.Builder
	if err := writeExperimentsPlain(&out, campaign); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 cell, on more than one dispat") {
		t.Errorf("want the campaign named as mixed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "did not hold") {
		t.Errorf("want a verdict with nothing to point at said plainly:\n%s", out.String())
	}
}

// The verb is what a job summary and a terminal both call, so its argument
// handling is part of the contract: a mistyped flag or a second folder must
// fail rather than quietly summarise the default.
func TestExperimentsVerb(t *testing.T) {
	t.Run("markdown over a folder", func(t *testing.T) {
		var out bytes.Buffer
		if err := experiments([]string{"-markdown", fixtures}, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "3 cells on dispat 1.7.0") || !strings.Contains(out.String(), "| cell |") {
			t.Fatalf("got:\n%s", out.String())
		}
	})
	t.Run("plain over a folder", func(t *testing.T) {
		var out bytes.Buffer
		if err := experiments([]string{fixtures}, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "midrelease-conflict-dispat") || strings.Contains(out.String(), "| cell |") {
			t.Fatalf("got:\n%s", out.String())
		}
	})
	t.Run("two folders", func(t *testing.T) {
		if err := experiments([]string{fixtures, fixtures}, io.Discard); err == nil {
			t.Fatal("want an error rather than a summary of one of them")
		}
	})
	t.Run("an unknown flag", func(t *testing.T) {
		// ContinueOnError, so the flag package returns the error rather than
		// killing the process a test is running in.
		if err := experiments([]string{"-nope"}, io.Discard); err == nil {
			t.Fatal("want an error rather than the default folder summarised")
		}
	})
}

// The failure this fences: the experiments reach the site through the report
// like every other measurement, and a build that dropped the section would
// publish a page whose table is empty for a run that recorded twelve cells.
func TestBuildFoldsInTheExperiments(t *testing.T) {
	dir := t.TempDir()
	coverage := filepath.Join(dir, "coverage")
	if err := os.MkdirAll(filepath.Join(coverage, "testlog"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(coverage, "ccme.out"),
		"mode: atomic\ngithub.com/yohimik/dispat/pkg/ccme/v2/parse.go:1.1,2.2 4 1\n")
	write(t, filepath.Join(coverage, "testlog", "ccme.json"), sampleLog)

	out := filepath.Join(dir, "report.json")
	err := build([]string{"-coverage", coverage, "-out", out, "-commit", "abc123", "-experiments", fixtures})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Commit != "abc123" {
		t.Errorf("commit = %q", report.Commit)
	}
	if report.Experiments.Version != "1.7.0" || len(report.Experiments.Cells) != 3 {
		t.Fatalf("got %d cells on %q, want the three fixtures on 1.7.0",
			len(report.Experiments.Cells), report.Experiments.Version)
	}
	if report.Coverage.Total.Statements != 4 || report.Suite.Totals.Tests != 2 {
		t.Errorf("the other sections did not survive: %+v %+v", report.Coverage.Total, report.Suite.Totals)
	}

	t.Run("the folder defaults under the coverage folder", func(t *testing.T) {
		// No -experiments: <coverage>/experiments does not exist here, which
		// is the ordinary case for a run that measured none.
		second := filepath.Join(dir, "second.json")
		if err := build([]string{"-coverage", coverage, "-out", second, "-commit", "abc123"}); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(second)
		if err != nil {
			t.Fatal(err)
		}
		var report Report
		if err := json.Unmarshal(body, &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Experiments.Cells) != 0 {
			t.Fatalf("got %d cells, want none", len(report.Experiments.Cells))
		}
	})
}

// A campaign folder that is there and unreadable is not a campaign nobody
// ran, so build fails rather than publishing a page missing a section it was
// told to measure.
func TestBuildFailsOnAMalformedCampaign(t *testing.T) {
	dir := t.TempDir()
	coverage := filepath.Join(dir, "coverage")
	if err := os.MkdirAll(filepath.Join(coverage, "testlog"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(coverage, "ccme.out"),
		"mode: atomic\ngithub.com/yohimik/dispat/pkg/ccme/v2/parse.go:1.1,2.2 4 1\n")
	write(t, filepath.Join(coverage, "testlog", "ccme.json"), sampleLog)
	writeCell(t, filepath.Join(coverage, experimentsDirName), "orphan-dispat", `{`, "")

	err := build([]string{"-coverage", coverage, "-out", filepath.Join(dir, "report.json"), "-commit", "abc"})
	if err == nil {
		t.Fatal("want an error rather than a report with the section quietly missing")
	}
}

// ---- helpers --------------------------------------------------------------

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCell lays out one cell the way the harness does: a folder with a
// verdict and, when there is one, its observations.
func writeCell(t *testing.T, dir, id, verdict, observations string) {
	t.Helper()
	write(t, filepath.Join(dir, id, "verdict.json"), verdict)
	if observations != "" {
		write(t, filepath.Join(dir, id, "observations.jsonl"), observations)
	}
}

// TestExperimentsVerbReportsATableItCouldNotDeliver: the table is buffered
// and flushed once, so a reader that stops taking it has to surface as the
// verb's own error. A job summary that is quietly half a table is a campaign
// misreported rather than a step that failed.
func TestExperimentsVerbReportsATableItCouldNotDeliver(t *testing.T) {
	dir := t.TempDir()
	// Longer than either buffer, so the failure is reached while the table is
	// being written rather than only on the final flush.
	long := strings.Repeat("an expectation too long for any buffer ", 200)
	writeCell(t, dir, "orphan-dispat",
		`{"tool":"dispat","dispat":"dispat 1.7.1 (linux_amd64)","checks":[{"check":"`+long+`","ok":false}]}`, "")
	for _, args := range [][]string{{dir}, {"-markdown", dir}} {
		if err := experiments(args, errWriter{}); err == nil {
			t.Fatalf("experiments(%q) succeeded on a reader that took none of it", args)
		}
	}
}
