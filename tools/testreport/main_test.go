package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// errWriter refuses everything written to it. A rendering whose output never
// reached the reader is a failure of the run rather than a run with nothing
// to say, and only the writer can tell the caller which of the two happened.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("nothing accepts this") }

// A package that failed to build has no failing test to attribute the
// compiler error to, which is the case the unattributed package output
// exists for.
const buildFailureLog = `
{"Action":"output","Package":"p","Output":"# p\n"}
{"Action":"output","Package":"p","Output":"./x.go:3:2: undefined: y\n"}
{"Action":"fail","Package":"p","Elapsed":0}
`

// measuredRun lays out what a full test run leaves behind: one coverage
// profile and one `go test -json` log, in the folder shape `build` reads.
func measuredRun(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "coverage")
	write(t, filepath.Join(dir, "ccme.out"),
		"mode: atomic\ngithub.com/yohimik/dispat/pkg/ccme/v2/parse.go:1.1,2.2 4 1\n")
	write(t, filepath.Join(dir, "testlog", "ccme.json"), sampleLog)
	return dir
}

func TestVerifyCoverageStampsRejectsMissingStaleAndMixedRuns(t *testing.T) {
	const commit = "0123456789abcdef"
	makeRun := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range []string{"ccme", "config", "manifest", "models", "scanner", "writer", "tools", "dispat", "integration"} {
			write(t, filepath.Join(dir, name+".out"), "mode: atomic\np/a.go:1.1,2.2 1 1\n")
			write(t, filepath.Join(dir, name+".commit"), commit+"\n")
		}
		return dir
	}
	t.Run("complete fresh run", func(t *testing.T) {
		if err := verifyCoverageStamps(makeRun(t), commit); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("stale profile", func(t *testing.T) {
		dir := makeRun(t)
		write(t, filepath.Join(dir, "tools.commit"), "older\n")
		if err := verifyCoverageStamps(dir, commit); err == nil {
			t.Fatal("want stale stamp rejected")
		}
	})
	t.Run("missing profile", func(t *testing.T) {
		dir := makeRun(t)
		if err := os.Remove(filepath.Join(dir, "writer.out")); err != nil {
			t.Fatal(err)
		}
		if err := verifyCoverageStamps(dir, commit); err == nil {
			t.Fatal("want missing profile rejected")
		}
	})
	t.Run("mixed extra profile", func(t *testing.T) {
		dir := makeRun(t)
		write(t, filepath.Join(dir, "old.out"), "mode: atomic\np/a.go:1.1,2.2 1 1\n")
		if err := verifyCoverageStamps(dir, commit); err == nil {
			t.Fatal("want unexpected profile rejected")
		}
	})
}

func TestCoverageCommandPrintsTheMergedIntegerDenominator(t *testing.T) {
	const commit = "0123456789abcdef"
	dir := t.TempDir()
	for _, name := range []string{"ccme", "config", "manifest", "models", "scanner", "writer", "tools", "dispat", "integration"} {
		covered := "0"
		if name == "ccme" || name == "integration" {
			covered = "1"
		}
		write(t, filepath.Join(dir, name+".out"),
			"mode: atomic\nexample.test/"+name+"/x.go:1.1,2.2 3 "+covered+"\n")
		write(t, filepath.Join(dir, name+".commit"), commit+"\n")
	}
	var out, errs strings.Builder
	code := run([]string{"coverage", "-coverage", dir, "-commit", commit}, &out, &errs)
	if code != 0 || errs.Len() != 0 {
		t.Fatalf("coverage command = %d, stderr %q", code, errs.String())
	}
	if got := out.String(); got != "6 27 22.2%\n" {
		t.Fatalf("coverage output = %q, want merged covered statements, denominator and display percent", got)
	}
}

// TestRunDispatchesTheVerbs: the dispatch is what every gate calls, so a verb
// that fell through to the usage text, or a failure that came back as
// success, would pass every unit test in this package and fail the release.
func TestRunDispatchesTheVerbs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ccme.json")
	write(t, logPath, sampleLog)
	coverage := measuredRun(t)
	report := filepath.Join(t.TempDir(), "report.json")

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"render", []string{"render", logPath}, 0, "ccme: 2 tests"},
		{"experiments", []string{"experiments", fixtures}, 0, "3 cells on dispat 1.7.0"},
		{"build", []string{"build", "-coverage", coverage, "-out", report, "-commit", "abc123"}, 0, ""},
		{"a verb that failed", []string{"render"}, 1, ""},
		{"test without its separator", []string{"test", "unit"}, 2, ""},
		{"bench without its separator", []string{"bench", "unit"}, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errs strings.Builder
			if got := run(tc.args, &out, &errs); got != tc.code {
				t.Fatalf("run(%q) = %d, want %d", tc.args, got, tc.code)
			}
			if tc.want != "" && !strings.Contains(out.String(), tc.want) {
				t.Fatalf("run(%q) printed:\n%s\nwant %q in it", tc.args, out.String(), tc.want)
			}
			if errs.Len() != 0 {
				t.Fatalf("run(%q) wrote the usage text for a verb it understood: %s", tc.args, errs.String())
			}
		})
	}
}

// TestRunPrintsTheUsageForWhatItCannotDo: a mistyped verb has to say what the
// verbs are, on stderr and with a code of its own. Doing nothing and
// reporting success is how a gate that never ran passes.
func TestRunPrintsTheUsageForWhatItCannotDo(t *testing.T) {
	for name, args := range map[string][]string{
		"no verb at all":         nil,
		"a verb that is not one": {"summarize", "coverage/testlog/ccme.json"},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errs strings.Builder
			if got := run(args, &out, &errs); got != 2 {
				t.Fatalf("run(%q) = %d, want 2", args, got)
			}
			if !strings.Contains(errs.String(), "usage:") {
				t.Fatalf("run(%q) said %q on stderr, want the usage text", args, errs.String())
			}
			if out.Len() != 0 {
				t.Fatalf("run(%q) put the usage on stdout: %s", args, out.String())
			}
		})
	}
}

// TestRunReturnsTheSuiteAndBenchmarkCodes: the two verbs that run go test
// return its own exit code through the dispatch, which is what keeps a
// release gate transparent to what the run reported.
func TestRunReturnsTheSuiteAndBenchmarkCodes(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
		var out, errs strings.Builder
		if got := run([]string{"test", "unit", "--", "./..."}, &out, &errs); got != 0 {
			t.Fatalf("run test = %d, want 0", got)
		}
		if !strings.Contains(out.String(), "unit: 1 tests") {
			t.Fatalf("run test printed:\n%s", out.String())
		}
		if _, err := os.Stat(filepath.Join(root, "coverage", "testlog", "unit.json")); err != nil {
			t.Fatalf("the log the report folds in is not there: %v", err)
		}
	})
	t.Run("bench", func(t *testing.T) {
		root := workspace(t, benchmarkBody)
		var out, errs strings.Builder
		code := run([]string{"bench", "unit", "--", "-bench=.", "-benchtime=1x", "./..."}, &out, &errs)
		if code != 0 {
			t.Fatalf("run bench = %d, want 0", code)
		}
		if !strings.Contains(out.String(), "unit: 1 benchmarks on ") {
			t.Fatalf("run bench printed:\n%s", out.String())
		}
		if _, err := os.Stat(filepath.Join(root, "coverage", benchDirName, "unit.json")); err != nil {
			t.Fatalf("the stream the report folds in is not there: %v", err)
		}
	})
}

// TestBuildCarriesForwardAndListsWhatItMeasured: -keep is the freshness rule
// as a flag and -modules is how the script driving a release says which
// modules this run re-measured, without parsing JSON in shell.
func TestBuildCarriesForwardAndListsWhatItMeasured(t *testing.T) {
	dir := t.TempDir()
	coverage := measuredRun(t)
	write(t, filepath.Join(coverage, benchDirName, "config.json"), sampleBenchLog)

	earlier := Report{Commit: "0123456789abcdef", Benchmarks: Benchmarks{Groups: []BenchGroup{
		{ID: "ccme", Path: "pkg/ccme", Results: []Benchmark{{Name: "BenchmarkOld"}}},
	}}}
	body, err := json.Marshal(earlier)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "earlier.json")
	write(t, keep, string(body))

	out := filepath.Join(dir, "report.json")
	modules := filepath.Join(dir, "listed", "modules.txt")
	err = build([]string{
		"-coverage", coverage, "-out", out, "-commit", "abc123",
		"-keep", keep, "-modules", modules,
	})
	if err != nil {
		t.Fatal(err)
	}

	var report Report
	body, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Benchmarks.Groups) != 2 {
		t.Fatalf("groups = %#v, want this run's and the one it did not measure", report.Benchmarks.Groups)
	}
	if benchCount(report.Benchmarks) != 4 {
		t.Errorf("benchCount = %d, want the three measured and the one carried over",
			benchCount(report.Benchmarks))
	}

	listed, err := os.ReadFile(modules)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(listed); got != "pkg/ccme\npkg/config\n" {
		t.Errorf("the modules list is %q, want one module per line", got)
	}
}

// TestBuildDiscoversTheCommitItMeasured: an artifact nobody can trace to a
// commit is one nobody can tell from a stale one, so an absent -commit is
// discovered rather than left out.
func TestBuildDiscoversTheCommitItMeasured(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	t.Setenv("GITHUB_SHA", sha)
	out := filepath.Join(t.TempDir(), "report.json")
	if err := build([]string{"-coverage", measuredRun(t), "-out", out}); err != nil {
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
	if report.Commit != sha {
		t.Fatalf("commit = %q, want the one discovered", report.Commit)
	}
}

// TestBuildRefusesWhatItCannotMeasureOrWrite: every one of these is a run
// that measured less than it was told to, and a report written anyway would
// publish a page whose missing section reads as a measurement of nought.
func TestBuildRefusesWhatItCannotMeasureOrWrite(t *testing.T) {
	t.Run("no coverage profiles", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "coverage")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := build([]string{"-coverage", dir, "-out", filepath.Join(t.TempDir(), "r.json")}); err == nil {
			t.Fatal("want the empty coverage folder reported")
		}
	})
	t.Run("no test logs", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "coverage")
		write(t, filepath.Join(dir, "ccme.out"), "mode: atomic\np/a.go:1.1,2.2 1 1\n")
		if err := build([]string{"-coverage", dir, "-out", filepath.Join(t.TempDir(), "r.json")}); err == nil {
			t.Fatal("want the missing testlog folder reported")
		}
	})
	t.Run("a benchmark stream that is not one", func(t *testing.T) {
		coverage := measuredRun(t)
		write(t, filepath.Join(coverage, benchDirName, "config.json"), "{")
		if err := build([]string{"-coverage", coverage, "-out", filepath.Join(t.TempDir(), "r.json")}); err == nil {
			t.Fatal("want the malformed stream reported")
		}
	})
	t.Run("an output folder it cannot make", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "occupied"), "not a folder\n")
		err := build([]string{
			"-coverage", measuredRun(t), "-commit", "abc",
			"-out", filepath.Join(dir, "occupied", "report.json"),
		})
		if err == nil {
			t.Fatal("want the folder it could not make reported")
		}
	})
	t.Run("an output path that is a folder", func(t *testing.T) {
		err := build([]string{"-coverage", measuredRun(t), "-commit", "abc", "-out", t.TempDir()})
		if err == nil {
			t.Fatal("want the unwritable output reported")
		}
	})
	t.Run("a modules list it cannot write", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "occupied"), "not a folder\n")
		err := build([]string{
			"-coverage", measuredRun(t), "-commit", "abc",
			"-out", filepath.Join(dir, "report.json"),
			"-modules", filepath.Join(dir, "occupied", "modules.txt"),
		})
		if err == nil {
			t.Fatal("want the modules list it could not write reported")
		}
	})
}

// TestReadCoverageKeepsTheLayersApart: the layers overlap heavily, so the
// total is neither their sum nor their average but the merge. The badge
// script's own working files are in the same folder and are not a package's
// measurement of anything.
func TestReadCoverageKeepsTheLayersApart(t *testing.T) {
	const block = "github.com/yohimik/dispat/pkg/ccme/v2/parse.go"
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ccme.out"),
		"mode: atomic\n"+block+":1.1,2.2 2 1\n"+block+":4.1,5.2 2 0\n")
	write(t, filepath.Join(dir, integrationProfile),
		"mode: set\n"+block+":4.1,5.2 2 1\n")
	write(t, filepath.Join(dir, "coverage.out"),
		"mode: atomic\n"+block+":9.1,10.2 5 1\n")

	cov, err := readCoverage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cov.Total.Statements != 4 || cov.Total.Covered != 4 {
		t.Errorf("total = %+v, want the merge of the two profiles and none of the merge output", cov.Total)
	}
	if cov.Unit.Statements != 4 || cov.Unit.Covered != 2 {
		t.Errorf("unit = %+v, want the package's own binary alone", cov.Unit)
	}
	if cov.Integration.Statements != 2 || cov.Integration.Covered != 2 {
		t.Errorf("integration = %+v, want the instrumented binary alone", cov.Integration)
	}
	if len(cov.Modules) != 1 || cov.Modules[0].Path != "pkg/ccme" {
		t.Errorf("modules = %+v", cov.Modules)
	}
}

// TestReadCoverageRefusesARunItCannotRead: a skewed total is worse than no
// report, and a folder holding nothing but the integration profile is a run
// that never executed the unit suites it was told to merge.
func TestReadCoverageRefusesARunItCannotRead(t *testing.T) {
	t.Run("a pattern that is not one", func(t *testing.T) {
		if _, err := readCoverage("["); err == nil {
			t.Fatal("want the malformed folder name reported")
		}
	})
	t.Run("the integration profile alone", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, integrationProfile), "mode: set\np/a.go:1.1,2.2 1 1\n")
		if _, err := readCoverage(dir); err == nil {
			t.Fatal("want the missing unit profiles reported")
		}
	})
	t.Run("a profile that is not one", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "ccme.out"), "mode: atomic\np/a.go:1.1,2.2 x 1\n")
		if _, err := readCoverage(dir); err == nil {
			t.Fatal("want the corrupt profile reported rather than skewing the total")
		}
	})
}

// TestReadSuiteDescribesRatherThanRefuses: a failing pass and a benchmark
// that ran in one are both warnings, because the report says what happened
// and the run that produced them has already failed on its own exit code. A
// folder it cannot read at all is a different matter.
func TestReadSuiteDescribesRatherThanRefuses(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "unit.json"), failingLog)
	write(t, filepath.Join(dir, "config.json"),
		`{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/config","Test":"BenchmarkFold","Elapsed":1.2}`+"\n"+
			`{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/config","Elapsed":1.5}`+"\n")

	suite, err := readSuite(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(suite.Groups) != 2 {
		t.Fatalf("groups = %#v", suite.Groups)
	}
	if suite.Totals.Failed != 1 || suite.Totals.Benchmarks != 1 {
		t.Errorf("totals = %+v, want the failure and the benchmark carried, not dropped", suite.Totals)
	}

	t.Run("a pattern that is not one", func(t *testing.T) {
		if _, err := readSuite("["); err == nil {
			t.Fatal("want the malformed folder name reported")
		}
	})
	t.Run("no logs at all", func(t *testing.T) {
		if _, err := readSuite(t.TempDir()); err == nil {
			t.Fatal("want the empty testlog folder reported: the tests scripts write those")
		}
	})
	t.Run("a log that is not one", func(t *testing.T) {
		bad := t.TempDir()
		write(t, filepath.Join(bad, "unit.json"), "not json\n")
		if _, err := readSuite(bad); err == nil {
			t.Fatal("want the corrupt log reported rather than counted as zero tests")
		}
	})
}

// TestRenderTakesExactlyOneLog: the verb is what a human runs over a stream
// nobody can read, so naming none or two of them has to fail rather than
// summarise whichever it happened to be handed.
func TestRenderTakesExactlyOneLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ccme.json")
	write(t, path, sampleLog)
	for name, args := range map[string][]string{
		"no log":   nil,
		"two logs": {path, path},
	} {
		t.Run(name, func(t *testing.T) {
			if err := render(args, io.Discard); err == nil {
				t.Fatal("want an error rather than a summary of one of them")
			}
		})
	}
	var out strings.Builder
	if err := render([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	const want = "ccme: 2 tests and 1 fuzz targets in 1 packages, 1.5s: 2 passed, 1 skipped"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("render printed %q, want %q", got, want)
	}
}

// TestSummarisePrintsWhatFailedBeforeTheTally: a CI log that hides why a test
// failed is worse than a missing report, so the failure output comes first
// and the one-line tally after it.
func TestSummarisePrintsWhatFailedBeforeTheTally(t *testing.T) {
	dir := t.TempDir()
	failing := filepath.Join(dir, "unit.json")
	write(t, failing, failingLog)

	var out strings.Builder
	if err := summarise(failing, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	failure, tally := strings.Index(got, "want 1, got 2"), strings.Index(got, "1 FAILED")
	if failure < 0 || tally < 0 {
		t.Fatalf("summarise printed:\n%s", got)
	}
	if failure > tally {
		t.Fatalf("the tally came before the failure it is about:\n%s", got)
	}

	t.Run("a package that failed to build", func(t *testing.T) {
		path := filepath.Join(dir, "build.json")
		write(t, path, buildFailureLog)
		var out strings.Builder
		if err := summarise(path, &out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"undefined: y", "1 package(s) FAILED to build"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("summarise printed:\n%s\nwant %q in it", out.String(), want)
			}
		}
	})

	t.Run("a reader that cannot take the failures", func(t *testing.T) {
		if err := summarise(failing, errWriter{}); err == nil {
			t.Fatal("want the undelivered failure report surfaced rather than a clean tally")
		}
	})
}

// TestSummariseRefusesALogItCannotRead: a log that is absent or corrupt is a
// run whose record is broken, which the caller has to hear about; `test`
// downgrades it to a warning itself, because the tests' own code is what a
// gate reads.
func TestSummariseRefusesALogItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if err := summarise(filepath.Join(dir, "absent.json"), io.Discard); err == nil {
		t.Fatal("want the absent log reported")
	}
	corrupt := filepath.Join(dir, "unit.json")
	write(t, corrupt, "not json\n")
	if err := summarise(corrupt, io.Discard); err == nil {
		t.Fatal("want the corrupt log reported rather than a summary of zero tests")
	}
}

// TestDiscoverCommit: the stamp is what makes a stale artifact visible, so CI
// wins over the checkout and a run with neither leaves it empty rather than
// guessing at one.
func TestDiscoverCommit(t *testing.T) {
	t.Run("what CI says it built", func(t *testing.T) {
		const sha = "0123456789abcdef0123456789abcdef01234567"
		t.Setenv("GITHUB_SHA", sha)
		if got := discoverCommit(); got != sha {
			t.Fatalf("discoverCommit = %q, want GITHUB_SHA", got)
		}
	})
	t.Run("what the checkout says", func(t *testing.T) {
		t.Setenv("GITHUB_SHA", "")
		t.Setenv("GIT_AUTHOR_NAME", "testreport")
		t.Setenv("GIT_AUTHOR_EMAIL", "testreport@example.invalid")
		t.Setenv("GIT_COMMITTER_NAME", "testreport")
		t.Setenv("GIT_COMMITTER_EMAIL", "testreport@example.invalid")
		t.Chdir(t.TempDir())
		for _, args := range [][]string{
			{"init", "--quiet"},
			{"-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "a commit to point at"},
		} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Skipf("no usable git here: git %s: %v: %s", strings.Join(args, " "), err, out)
			}
		}
		got := discoverCommit()
		if got == "" || strings.ContainsAny(got, " \t\r\n") {
			t.Fatalf("discoverCommit = %q, want the checkout's own HEAD, trimmed", got)
		}
	})
	t.Run("neither", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("GITHUB_SHA", "")
		// Nothing above a temp folder is a checkout, and the ceiling says so
		// even where something is.
		t.Setenv("GIT_CEILING_DIRECTORIES", dir)
		t.Chdir(dir)
		if got := discoverCommit(); got != "" {
			t.Fatalf("discoverCommit = %q, want it empty rather than guessed at", got)
		}
	})
}

// TestDuration: a unit suite finishes in seconds worth a decimal and the
// integration suite in minutes where one is noise, so the two are rendered
// the way their length deserves.
func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		seconds float64
		want    string
	}{
		{0, "0.0s"},
		{1.54, "1.5s"},
		{59.94, "59.9s"},
		{60, "1m0s"},
		{125.4, "2m5s"},
	} {
		if got := duration(tc.seconds); got != tc.want {
			t.Errorf("duration(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

// TestWriteBenchModules: the list is the freshness signal a release carries
// forward as an output, and a plain file is the one shape every caller
// already knows how to read.
func TestWriteBenchModules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "listed", "modules.txt")
	b := Benchmarks{Groups: []BenchGroup{{Path: "pkg/ccme"}, {Path: "pkg/config"}}}
	if err := writeBenchModules(path, b); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "pkg/ccme\npkg/config\n" {
		t.Fatalf("wrote %q, want one module per line", got)
	}

	t.Run("nothing measured", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "modules.txt")
		if err := writeBenchModules(empty, Benchmarks{}); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(empty)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("wrote %q, want an empty list rather than a blank line", body)
		}
	})
	t.Run("a folder it cannot make", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "occupied"), "not a folder\n")
		if err := writeBenchModules(filepath.Join(dir, "occupied", "modules.txt"), b); err == nil {
			t.Fatal("want the folder it could not make reported")
		}
	})
}
