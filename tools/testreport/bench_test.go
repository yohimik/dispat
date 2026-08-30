package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One benchmark pass: the machine header `go test -bench` prints, two
// benchmarks with allocation figures, one that reports a throughput as well,
// and one custom unit the reader has no place for.
const sampleBenchLog = `
{"Action":"start","Package":"github.com/yohimik/dispat/pkg/config"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"goos: linux\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"goarch: amd64\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"pkg: github.com/yohimik/dispat/pkg/config\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"cpu: AMD EPYC 7763 64-Core Processor\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"BenchmarkReadTree/json/small-4         \t   82633\t     14261 ns/op\t  18.72 MB/s\t    3697 B/op\t      72 allocs/op\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"BenchmarkFold/lower-4                  \t100000000\t        16.68 ns/op\t       0 B/op\t       0 allocs/op\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"BenchmarkCustom-4                      \t      10\t      1000 ns/op\t       3.00 widgets/op\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"PASS\n"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/config","Elapsed":3.1}
`

// TestReadBenchLogReadsTheMachineAndTheMeasurements: a nanosecond figure
// without the machine that produced it is a number nobody can compare
// anything to, so the header is part of the reading.
func TestReadBenchLogReadsTheMachineAndTheMeasurements(t *testing.T) {
	log, err := readBenchLog("config", strings.NewReader(sampleBenchLog))
	if err != nil {
		t.Fatal(err)
	}
	if log.goos != "linux" || log.goarch != "amd64" {
		t.Errorf("machine = %s/%s", log.goos, log.goarch)
	}
	if log.cpu != "AMD EPYC 7763 64-Core Processor" {
		t.Errorf("cpu = %q", log.cpu)
	}
	if log.path != "pkg/config" {
		t.Errorf("path = %q, want the module the pkg line named", log.path)
	}
	if len(log.results) != 3 {
		t.Fatalf("results = %#v", log.results)
	}

	// Sorted by name, so the page reads the same way every run.
	if got := log.results[0].Name; got != "BenchmarkCustom" {
		t.Errorf("first = %q", got)
	}
	var read Benchmark
	for _, r := range log.results {
		if r.Name == "BenchmarkReadTree/json/small" {
			read = r
		}
	}
	want := Benchmark{
		Name: "BenchmarkReadTree/json/small", Package: "pkg/config", Procs: 4,
		Runs: 82633, NsPerOp: 14261, BytesPerOp: 3697, AllocsPerOp: 72, MBPerSec: 18.72,
	}
	if read != want {
		t.Errorf("read  = %#v\nwant  = %#v", read, want)
	}
}

// TestBenchNameKeepsItsShape: the -N suffix is a fact about the run rather
// than about the benchmark, so it travels in a field of its own and the name
// reads as the source wrote it — slashes included.
func TestBenchNameKeepsItsShape(t *testing.T) {
	for _, tc := range []struct {
		field string
		name  string
		procs int
	}{
		{"BenchmarkFold-8", "BenchmarkFold", 8},
		{"BenchmarkFold/lower-15", "BenchmarkFold/lower", 15},
		{"BenchmarkFold", "BenchmarkFold", 0},
		{"BenchmarkFold-x", "BenchmarkFold-x", 0},
		{"-8", "-8", 0},
	} {
		name, procs := splitProcs(tc.field)
		if name != tc.name || procs != tc.procs {
			t.Errorf("splitProcs(%q) = %q, %d; want %q, %d", tc.field, name, procs, tc.name, tc.procs)
		}
	}
}

// TestParseBenchLineRefusesWhatIsNotOne: everything else in the stream — the
// PASS line, a test's output, an empty line — has to read as "not a result"
// rather than as a measurement of nought.
func TestParseBenchLineRefusesWhatIsNotOne(t *testing.T) {
	for _, line := range []string{
		"", "PASS", "ok  \tgithub.com/yohimik/dispat/pkg/config\t3.1s",
		"BenchmarkFold-4", "BenchmarkFold-4\tnotanumber\t10 ns/op",
		"--- FAIL: TestSomething (0.00s)",
		"BenchmarkFold-4\t10\t   widgets/op",
	} {
		if _, ok := parseBenchLine(line); ok {
			t.Errorf("parseBenchLine(%q) read a result", line)
		}
	}
}

// TestReadBenchmarksFoldsEveryStream: the folder is the section, and a run
// that measured nothing is a warning rather than a failure — a release that
// rebuilt only the docs still has coverage and a suite to report.
func TestReadBenchmarksFoldsEveryStream(t *testing.T) {
	dir := t.TempDir()
	empty, err := readBenchmarks(dir)
	if err != nil {
		t.Fatalf("an empty folder: %v", err)
	}
	if len(empty.Groups) != 0 {
		t.Errorf("groups = %#v", empty.Groups)
	}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", sampleBenchLog)
	write("ccme.json", strings.ReplaceAll(sampleBenchLog, "pkg/config", "pkg/ccme"))
	// A stream with no results at all is skipped rather than carried as an
	// empty group: a module with no benchmarks has nothing to say.
	write("models.json", `{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/models","Elapsed":0.1}`)

	got, err := readBenchmarks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	if got.Groups[0].Path != "pkg/ccme" || got.Groups[1].Path != "pkg/config" {
		t.Errorf("groups are not in module order: %q, %q", got.Groups[0].Path, got.Groups[1].Path)
	}
	if benchCount(got) != 6 {
		t.Errorf("benchCount = %d", benchCount(got))
	}
}

// TestCarryForwardKeepsWhatThisRunDidNotMeasure: a module this run
// benchmarked replaces whatever was there; a module it did not keeps what it
// had. That is the whole freshness rule, and it is what stops a docs build
// from losing a page's numbers because one run had nothing to say about them.
func TestCarryForwardKeepsWhatThisRunDidNotMeasure(t *testing.T) {
	previous := Report{
		Commit: "0123456789abcdef",
		Benchmarks: Benchmarks{Groups: []BenchGroup{
			{ID: "ccme", Path: "pkg/ccme", Results: []Benchmark{{Name: "BenchmarkOld"}}},
			{ID: "config", Path: "pkg/config", Results: []Benchmark{{Name: "BenchmarkStale"}}},
		}},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	body, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	report := Report{Benchmarks: Benchmarks{Groups: []BenchGroup{
		{ID: "config", Path: "pkg/config", Results: []Benchmark{{Name: "BenchmarkFresh"}}},
	}}}
	carryForward(&report, path)

	if len(report.Benchmarks.Groups) != 2 {
		t.Fatalf("groups = %#v", report.Benchmarks.Groups)
	}
	if got := report.Benchmarks.Groups[0]; got.Path != "pkg/ccme" || got.Results[0].Name != "BenchmarkOld" {
		t.Errorf("the unmeasured module did not keep its numbers: %#v", got)
	}
	if got := report.Benchmarks.Groups[1]; got.Path != "pkg/config" || got.Results[0].Name != "BenchmarkFresh" {
		t.Errorf("the measured module did not replace its numbers: %#v", got)
	}

	// No earlier report at all is the normal case rather than a mistake.
	standalone := Report{Benchmarks: Benchmarks{Groups: []BenchGroup{{Path: "pkg/config"}}}}
	carryForward(&standalone, filepath.Join(dir, "absent.json"))
	if len(standalone.Benchmarks.Groups) != 1 {
		t.Errorf("groups = %#v", standalone.Benchmarks.Groups)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.json"), []byte("not a report"), 0o644); err != nil {
		t.Fatal(err)
	}
	carryForward(&standalone, filepath.Join(dir, "junk.json"))
	if len(standalone.Benchmarks.Groups) != 1 {
		t.Errorf("groups = %#v", standalone.Benchmarks.Groups)
	}
}
