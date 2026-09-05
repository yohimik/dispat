package main

import (
	"encoding/json"
	"io"
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
		"BenchmarkFold-4\t10\tabc ns/op",
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

// The throwaway module the runner tests measure: one benchmark, in a
// workspace of its own.
const benchmarkBody = `package mod

import "testing"

func BenchmarkNothing(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}
`

// A benchmark that unlinks the stream it is being written into. The runner
// holds the file open, so the pass finishes and exits 0 with nothing left for
// the summary to read, which is the one way to reach that warning without
// breaking the run itself.
const unlinkingBenchmarkBody = `package mod

import (
	"os"
	"testing"
)

func BenchmarkUnlinksItsOwnStream(b *testing.B) {
	if err := os.Remove("../coverage/benchlog/unit.json"); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
	}
}
`

// TestGoBenchUsage: the argument shape is <name> -- <args...>, the same as
// `test`, and anything else is a usage error (2) rather than a benchmark pass
// nobody asked for.
func TestGoBenchUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":   nil,
		"no separator":   {"config", "-bench=."},
		"empty log name": {"", "--", "-bench=."},
	} {
		t.Run(name, func(t *testing.T) {
			code, err := goBench(args, io.Discard)
			if code != 2 || err == nil {
				t.Fatalf("goBench(%q) = %d, %v; want 2 and a usage error", args, code, err)
			}
		})
	}
}

// TestGoBenchKeepsTheStream: a pass exits 0, leaves the -json stream as
// coverage/benchlog/<name>.json at the workspace root for `build` to fold in,
// and prints the human line in its place. The machine is part of that record:
// a nanosecond figure without it is a number nobody can compare anything to.
func TestGoBenchKeepsTheStream(t *testing.T) {
	root := workspace(t, benchmarkBody)

	var out strings.Builder
	code, err := goBench([]string{"unit", "--", "-bench=.", "-benchtime=1x", "./..."}, &out)
	if err != nil || code != 0 {
		t.Fatalf("goBench = %d, %v; want 0, nil", code, err)
	}
	log, err := readBenchFile("unit", filepath.Join(root, "coverage", benchDirName, "unit.json"))
	if err != nil {
		t.Fatalf("the written stream does not parse: %v", err)
	}
	if len(log.results) != 1 || log.results[0].Name != "BenchmarkNothing" {
		t.Fatalf("results = %#v, want the one benchmark measured", log.results)
	}
	if log.goos == "" || log.goarch == "" {
		t.Errorf("machine = %s/%s, want the header the pass printed", log.goos, log.goarch)
	}
	if !strings.Contains(out.String(), "unit: 1 benchmarks on ") {
		t.Errorf("goBench printed:\n%s", out.String())
	}
}

// TestGoBenchFailurePropagates: the exit code is the run's own, so a
// benchmark that fell over fails the gate driving it rather than passing on a
// summary of no measurements.
func TestGoBenchFailurePropagates(t *testing.T) {
	workspace(t, `package mod

import "testing"

func BenchmarkNo(b *testing.B) { b.Fatal("no") }
`)
	code, err := goBench([]string{"unit", "--", "-bench=.", "-benchtime=1x", "./..."}, io.Discard)
	if err != nil {
		t.Fatalf("goBench error = %v; a benchmark failure is a code, not an error", err)
	}
	if code == 0 {
		t.Fatal("goBench = 0; want the failing run's own nonzero code")
	}
}

// TestGoBenchRefusesWhatItCannotRecord: the stream is the record `build`
// folds in, so a pass whose stream could not be written is a failure rather
// than a measurement nobody kept.
func TestGoBenchRefusesWhatItCannotRecord(t *testing.T) {
	t.Run("no workspace above it", func(t *testing.T) {
		t.Chdir(t.TempDir())
		code, err := goBench([]string{"unit", "--", "-bench=.", "./..."}, io.Discard)
		if code != 1 || err == nil {
			t.Fatalf("goBench = %d, %v; want 1 and the no-go.work error", code, err)
		}
	})
	t.Run("a log folder it cannot make", func(t *testing.T) {
		root := workspace(t, benchmarkBody)
		write(t, filepath.Join(root, "coverage"), "not a folder\n")
		code, err := goBench([]string{"unit", "--", "-bench=.", "./..."}, io.Discard)
		if code != 1 || err == nil {
			t.Fatalf("goBench = %d, %v; want 1 and the folder it could not make", code, err)
		}
	})
	t.Run("a stream it cannot create", func(t *testing.T) {
		root := workspace(t, benchmarkBody)
		if err := os.MkdirAll(filepath.Join(root, "coverage", benchDirName, "unit.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		code, err := goBench([]string{"unit", "--", "-bench=.", "./..."}, io.Discard)
		if code != 1 || err == nil {
			t.Fatalf("goBench = %d, %v; want 1 and the stream it could not create", code, err)
		}
	})
	t.Run("no toolchain to run", func(t *testing.T) {
		workspace(t, benchmarkBody)
		t.Setenv("PATH", "")
		code, err := goBench([]string{"unit", "--", "-bench=.", "./..."}, io.Discard)
		if code != 1 || err == nil {
			t.Fatalf("goBench = %d, %v; want 1 and the toolchain it could not run", code, err)
		}
	})
}

// TestGoBenchWarnsWhenItCannotSummarise: the summary is a rendering, and
// losing a pass's own exit code to a rendering that failed would hide a run
// that worked.
func TestGoBenchWarnsWhenItCannotSummarise(t *testing.T) {
	root := workspace(t, unlinkingBenchmarkBody)
	code, err := goBench([]string{"unit", "--", "-bench=.", "-benchtime=1x", "./..."}, io.Discard)
	if code != 0 || err != nil {
		t.Fatalf("goBench = %d, %v; want the run's own 0 despite the summary", code, err)
	}
	if _, err := os.Stat(filepath.Join(root, "coverage", benchDirName, "unit.json")); !os.IsNotExist(err) {
		t.Fatalf("the benchmark did not remove its own stream: %v", err)
	}
}

// TestReadBenchLogRefusesAStreamThatIsNotOne: a stream that does not decode
// is a broken record, and a reader that shrugged at it would report a module
// as having measured nothing.
func TestReadBenchLogRefusesAStreamThatIsNotOne(t *testing.T) {
	if _, err := readBenchLog("config", strings.NewReader("not json\n")); err == nil {
		t.Fatal("want an error rather than a group with no results")
	}
}

// TestReadBenchLogOrdersAcrossPackages: one invocation can measure several
// packages, and the page reads by package and then by name rather than in the
// order the machine happened to finish them.
func TestReadBenchLogOrdersAcrossPackages(t *testing.T) {
	const stream = `
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"pkg: github.com/yohimik/dispat/pkg/config\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"BenchmarkZ-4\t10\t1.00 ns/op\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/ccme/v2","Output":"pkg: github.com/yohimik/dispat/pkg/ccme/v2\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/ccme/v2","Output":"BenchmarkA-4\t10\t1.00 ns/op\n"}
`
	log, err := readBenchLog("all", strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if log.path != "pkg/config" {
		t.Errorf("path = %q, want the module the first pkg line named", log.path)
	}
	if len(log.results) != 2 {
		t.Fatalf("results = %#v", log.results)
	}
	if log.results[0].Package != "pkg/ccme" || log.results[1].Package != "pkg/config" {
		t.Fatalf("results are not in package order: %#v", log.results)
	}
}

// TestBenchHeaderReadsOnlyTheMachineLines: the headers are four names, and a
// line that merely contains ": " is output rather than one of them.
func TestBenchHeaderReadsOnlyTheMachineLines(t *testing.T) {
	for _, line := range []string{"note: worth reading", "ok: not a header", "no separator at all"} {
		if name, value, ok := benchHeader(line); ok {
			t.Errorf("benchHeader(%q) = %q, %q, true; want it read as output", line, name, value)
		}
	}
	name, value, ok := benchHeader("cpu: Apple M1 Pro")
	if !ok || name != "cpu" || value != "Apple M1 Pro" {
		t.Errorf("benchHeader = %q, %q, %v", name, value, ok)
	}
}

// TestReadBenchFileRefusesWhatItCannotRead: a stream named in the folder and
// unreadable is a broken record, reported by name so the invocation that
// wrote it can be found.
func TestReadBenchFileRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBenchFile("config", filepath.Join(dir, "absent.json")); err == nil {
		t.Fatal("want the absent stream reported")
	}
	corrupt := filepath.Join(dir, "config.json")
	if err := os.WriteFile(corrupt, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readBenchFile("config", corrupt)
	if err == nil || !strings.Contains(err.Error(), corrupt) {
		t.Fatalf("err = %v, want the corrupt stream reported by name", err)
	}
}

// TestReadBenchmarksRefusesWhatItCannotRead: an absent folder is a run that
// measured nothing, but a folder holding a stream that will not parse is a
// run whose records cannot be trusted.
func TestReadBenchmarksRefusesWhatItCannotRead(t *testing.T) {
	if _, err := readBenchmarks("["); err == nil {
		t.Fatal("want the malformed folder name reported")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.json"), "not json\n")
	if _, err := readBenchmarks(dir); err == nil {
		t.Fatal("want the corrupt stream reported rather than an empty section")
	}
}

// TestReadBenchLogJoinsALineSplitAcrossEvents: the testing package prints a
// benchmark's name before it runs and its figures after, so under load the
// two arrive as two output events. A reader that took each event as a line
// dropped the measurement, which is how a release lost rows from its table
// while the benchmarks themselves passed.
func TestReadBenchLogJoinsALineSplitAcrossEvents(t *testing.T) {
	const split = `
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"goos: linux\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"goarch: amd64\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"pkg: github.com/yohimik/dispat/pkg/config\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Test":"BenchmarkNothing","Output":"BenchmarkNothing-15    \t"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Test":"BenchmarkNothing","Output":"       1\t         0 ns/op\n"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"BenchmarkTail-15    \t"}
{"Action":"output","Package":"github.com/yohimik/dispat/pkg/config","Output":"       2\t         5 ns/op"}
`
	log, err := readBenchLog("config", strings.NewReader(split))
	if err != nil {
		t.Fatal(err)
	}
	if len(log.results) != 2 {
		t.Fatalf("results = %#v, want the split line and the unterminated one both read", log.results)
	}
	if log.results[0].Name != "BenchmarkNothing" || log.results[0].Runs != 1 || log.results[0].Procs != 15 {
		t.Errorf("split line read as %#v", log.results[0])
	}
	if log.results[1].Name != "BenchmarkTail" || log.results[1].NsPerOp != 5 {
		t.Errorf("unterminated line read as %#v", log.results[1])
	}
	if log.goos != "linux" || log.path != "pkg/config" {
		t.Errorf("headers lost across the join: %s %s", log.goos, log.path)
	}
}
