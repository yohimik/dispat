package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// One package holding a test with two subtests, a fuzz target, a skipped test,
// and a second package with no test files at all.
const sampleLog = `
{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse/subject"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse/subject","Elapsed":0.01}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse/footer"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse/footer","Elapsed":0.01}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse","Elapsed":0.02}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"FuzzParse","Elapsed":0.30}
{"Action":"skip","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestNeedsGit","Elapsed":0}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Elapsed":1.5}
{"Action":"skip","Package":"github.com/yohimik/dispat/pkg/ccme/internal/fixtures","Elapsed":0}
`

// The failure this fences: `go test -json` reports every subtest as a result
// of its own, so counting results as tests would have this suite claim four
// test functions where the source has three.
func TestReadLogCountsTopLevelFunctionsOnly(t *testing.T) {
	log, err := readLog("ccme", strings.NewReader(sampleLog))
	if err != nil {
		t.Fatal(err)
	}
	if log.Tests != 2 {
		t.Errorf("tests = %d, want the two Test functions", log.Tests)
	}
	if log.Fuzz != 1 {
		t.Errorf("fuzz = %d, want the one fuzz target", log.Fuzz)
	}
	if log.Subtests != 2 {
		t.Errorf("subtests = %d, want the two subtests, counted apart", log.Subtests)
	}
	if log.Passed != 2 || log.Skipped != 1 || log.Failed != 0 {
		t.Errorf("got %d passed, %d skipped, %d failed", log.Passed, log.Skipped, log.Failed)
	}
	if log.Passed+log.Failed+log.Skipped != log.Tests+log.Fuzz {
		t.Errorf("the outcomes do not tally with the functions: %+v", log.Counts)
	}
}

// A package-level skip is `go test` saying the package holds no test files.
// Counting it would make the package total a count of folders.
func TestReadLogCountsOnlyPackagesThatRan(t *testing.T) {
	log, err := readLog("ccme", strings.NewReader(sampleLog))
	if err != nil {
		t.Fatal(err)
	}
	if log.Packages != 1 {
		t.Errorf("packages = %d, want the one package with tests", log.Packages)
	}
	if log.Elapsed != 1.5 {
		t.Errorf("elapsed = %v, want the package's own time, not the tests' sum", log.Elapsed)
	}
}

func TestReadLogDerivesTheModuleAndTheRacePass(t *testing.T) {
	log, err := readLog("integration-race", strings.NewReader(
		`{"Action":"pass","Package":"github.com/yohimik/dispat/tests/integration","Elapsed":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if log.path != "tests/integration" {
		t.Errorf("path = %q", log.path)
	}
	if !log.race {
		t.Error("a -race log must be marked as one")
	}
	if g := log.group(); !g.Race || g.Path != "tests/integration" || g.ID != "integration-race" {
		t.Errorf("group = %+v", g)
	}
}

const failingLog = `
{"Action":"output","Package":"p","Test":"TestOK","Output":"    ok, nothing to see\n"}
{"Action":"pass","Package":"p","Test":"TestOK","Elapsed":0}
{"Action":"output","Package":"p","Test":"TestBroken/case","Output":"    want 1, got 2\n"}
{"Action":"fail","Package":"p","Test":"TestBroken/case","Elapsed":0}
{"Action":"output","Package":"p","Test":"TestBroken","Output":"--- FAIL: TestBroken\n"}
{"Action":"fail","Package":"p","Test":"TestBroken","Elapsed":0}
{"Action":"output","Package":"p","Output":"FAIL\tp\n"}
{"Action":"fail","Package":"p","Elapsed":0.1}
`

// The whole reason `go test -json` is acceptable as a run's only output: the
// failure has to still be readable afterwards, including a subtest's, whose
// output is attributed to the subtest rather than to the function that failed.
func TestWriteFailuresPrintsTheFailuresAndNothingElse(t *testing.T) {
	log, err := readLog("unit", strings.NewReader(failingLog))
	if err != nil {
		t.Fatal(err)
	}
	if log.Failed != 1 {
		t.Fatalf("failed = %d, want the one failing function", log.Failed)
	}
	var out strings.Builder
	if err := log.writeFailures(strings.NewReader(failingLog), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"want 1, got 2", "--- FAIL: TestBroken", "FAIL\tp"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing to see") {
		t.Errorf("a passing test's output leaked into the failure report:\n%s", got)
	}
}

// A package that fails to build has no failing test to attribute the compiler
// error to, so the error only survives if unattributed package output does.
func TestWriteFailuresPrintsABuildFailure(t *testing.T) {
	const buildFailure = `
{"Action":"output","Package":"p","Output":"# p\n"}
{"Action":"output","Package":"p","Output":"./x.go:3:2: undefined: y\n"}
{"Action":"fail","Package":"p","Elapsed":0}
`
	log, err := readLog("unit", strings.NewReader(buildFailure))
	if err != nil {
		t.Fatal(err)
	}
	if log.Failed != 0 || len(log.failedPackages) != 1 {
		t.Fatalf("got %d failed tests and %d failed packages", log.Failed, len(log.failedPackages))
	}
	var out strings.Builder
	if err := log.writeFailures(strings.NewReader(buildFailure), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "undefined: y") {
		t.Errorf("the compiler error did not survive:\n%s", out.String())
	}
}

func TestReadLogRejectsACorruptLog(t *testing.T) {
	if _, err := readLog("unit", strings.NewReader("not json\n")); err == nil {
		t.Fatal("want an error rather than a report of zero tests")
	}
}

// The report's own logs are named after the packages that wrote them, which is
// not the order a reader wants: sorted by name, `tests/integration` lands
// between `services/dispat` and `pkg/manifest`.
func TestSuiteGroupsAreOrderedByModule(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ id, pkg string }{
		{"writer", "github.com/yohimik/dispat/pkg/writer"},
		{"integration-race", "github.com/yohimik/dispat/tests/integration"},
		{"dispat", "github.com/yohimik/dispat/services/dispat"},
		{"integration", "github.com/yohimik/dispat/tests/integration"},
		{"ccme", "github.com/yohimik/dispat/pkg/ccme"},
	} {
		line := fmt.Sprintf(`{"Action":"pass","Package":%q,"Elapsed":1}`+"\n", tc.pkg)
		if err := os.WriteFile(filepath.Join(dir, tc.id+".json"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	suite, err := readSuite(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range suite.Groups {
		got = append(got, g.ID)
	}
	want := []string{"ccme", "writer", "dispat", "integration", "integration-race"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (module order, race pass under the run it repeats)", got, want)
	}

	// The race pass runs the same tests again. Counting it would have the
	// report claim the integration suite's tests twice over.
	if suite.Totals.Packages != 4 {
		t.Fatalf("totals count %d packages across five logs, want the four the race pass does not repeat",
			suite.Totals.Packages)
	}
}

func TestCountsAdd(t *testing.T) {
	var total Counts
	total.add(Counts{Packages: 1, Tests: 2, Fuzz: 1, Subtests: 4, Passed: 3, Elapsed: 1.5})
	total.add(Counts{Packages: 2, Tests: 3, Skipped: 1, Failed: 1, Passed: 1, Elapsed: 0.5})
	want := Counts{Packages: 3, Tests: 5, Fuzz: 1, Subtests: 4, Passed: 4, Failed: 1, Skipped: 1, Elapsed: 2}
	if total != want {
		t.Fatalf("got %+v, want %+v", total, want)
	}
}
