package main

// The benchmark half: running `go test -bench`, keeping its stream, and
// reading the measurements back out of it.
//
// A benchmark is not a test and is not counted as one. It runs in a pass of
// its own, into a log folder of its own, and lands in a section of its own —
// because a tally answers "did it work" and a measurement answers "what does
// it cost", and a document that folds one into the other answers neither.
//
// The numbers are read from the stream `go test -bench -benchmem -json`
// produces rather than from a Go API, because there is no API: the benchmark
// result line is the format, and the -json wrapper carries it verbatim as an
// output event.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// benchDirName is where `testreport bench` keeps its streams, beside the
// testlog folder `testreport test` keeps its own in.
const benchDirName = "benchlog"

// goBench is `go test -bench` with a record of what it measured: it runs the
// benchmarks with -json, keeps the stream as coverage/benchlog/<name>.json at
// the repository root for `build` to fold into the report, and prints a human
// summary in its place.
//
//	testreport bench <log-name> -- <go test args...>
//
// The log name is the report's id for this invocation, and it is worth
// choosing to match the one the same module's tests script uses, so the two
// sections of the report name the same thing the same way.
//
// The caller supplies the benchmark flags. This runs no benchmark the caller
// did not ask for, because which benchmarks are worth a release's time is the
// package's own decision and not this program's.
func goBench(args []string) (int, error) {
	if len(args) < 2 || args[0] == "" || args[1] != "--" {
		return 2, errors.New("usage: testreport bench <log-name> -- <go test args...>")
	}
	name, rest := args[0], args[2:]

	root, err := repoRoot()
	if err != nil {
		return 1, err
	}
	logDir := filepath.Join(root, "coverage", benchDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return 1, err
	}
	logPath := filepath.Join(logDir, name+".json")

	fmt.Printf("%s: go test %s -json\n", name, strings.Join(rest, " "))

	f, err := os.Create(logPath)
	if err != nil {
		return 1, err
	}
	// -json last, for the same reason `testreport test` puts it last: a caller
	// may need -C, and go insists that one is the very first flag.
	cmd := exec.Command("go", append(append([]string{"test"}, rest...), "-json")...)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if err := f.Close(); err != nil {
		return 1, err
	}
	code := 0
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) {
			return 1, runErr
		}
		code = exit.ExitCode()
	}

	if err := summariseBench(logPath); err != nil {
		logf(levelWarn, "could not summarise %s: %v; the benchmarks themselves exited %d", logPath, err, code)
	}
	return code, nil
}

// summariseBench prints the human line a benchmark pass leaves behind.
func summariseBench(name string) error {
	log, err := readBenchFile(strings.TrimSuffix(filepath.Base(name), ".json"), name)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d benchmarks on %s/%s\n", log.id, len(log.results), log.goos, log.goarch)
	return nil
}

// benchLog is one benchmark stream, reduced to what the report carries.
type benchLog struct {
	id      string
	path    string
	goos    string
	goarch  string
	cpu     string
	results []Benchmark
}

// readBenchLog reads the measurements out of a `go test -bench -json` stream.
//
// Everything comes from the output events: the benchmark result line is the
// only place the numbers exist, and the machine header lines are the only
// place the machine does. The terminal pass/fail events say nothing a
// measurement needs, so they are ignored — a failing benchmark has already
// failed the run that produced this.
func readBenchLog(id string, r io.Reader) (*benchLog, error) {
	log := &benchLog{id: id}
	pkg := ""
	dec := json.NewDecoder(r)
	for {
		var ev event
		if err := dec.Decode(&ev); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if ev.Action != "output" {
			continue
		}
		line := strings.TrimRight(ev.Output, "\r\n")
		if header, value, ok := benchHeader(line); ok {
			switch header {
			case "goos":
				log.goos = value
			case "goarch":
				log.goarch = value
			case "cpu":
				log.cpu = value
			case "pkg":
				pkg = value
				if log.path == "" {
					log.path = moduleOf(strings.TrimPrefix(pkg, modulePrefix))
				}
			}
			continue
		}
		if result, ok := parseBenchLine(line); ok {
			result.Package = strings.TrimPrefix(pkg, modulePrefix)
			log.results = append(log.results, result)
		}
	}
	// By name, so the page reads in the order the file declares them rather
	// than in the order the machine happened to finish them.
	sort.Slice(log.results, func(i, j int) bool {
		if log.results[i].Package != log.results[j].Package {
			return log.results[i].Package < log.results[j].Package
		}
		return log.results[i].Name < log.results[j].Name
	})
	return log, nil
}

// readBenchFile reads one benchmark stream from disk.
func readBenchFile(id, name string) (*benchLog, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	log, err := readBenchLog(id, f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return log, nil
}

// group is the log as the report carries it.
func (l *benchLog) group() BenchGroup {
	return BenchGroup{
		ID: l.id, Path: l.path, Goos: l.goos, Goarch: l.goarch, CPU: l.cpu,
		Results: l.results,
	}
}

// benchHeader reads one of the `goos: linux` lines `go test -bench` prints
// before the results.
func benchHeader(line string) (string, string, bool) {
	name, value, ok := strings.Cut(line, ": ")
	if !ok {
		return "", "", false
	}
	switch name {
	case "goos", "goarch", "pkg", "cpu":
		return name, strings.TrimSpace(value), true
	}
	return "", "", false
}

// parseBenchLine reads one benchmark result line:
//
//	BenchmarkReadTree/json/small-15   	 82633	     14261 ns/op	  18.72 MB/s	   3697 B/op	      72 allocs/op
//
// The shape is fixed by the testing package: the name with its GOMAXPROCS
// suffix, the iteration count, then value/unit pairs of which only the four
// below have a place in the report. An unrecognised unit is skipped rather
// than refused, because a benchmark may report a custom one and the rest of
// its line is still worth reading.
func parseBenchLine(line string) (Benchmark, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
		return Benchmark{}, false
	}
	runs, err := strconv.Atoi(fields[1])
	if err != nil {
		return Benchmark{}, false
	}
	name, procs := splitProcs(fields[0])
	out := Benchmark{Name: name, Procs: procs, Runs: runs}
	seen := false
	for i := 2; i+1 < len(fields); i += 2 {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return Benchmark{}, false
		}
		switch fields[i+1] {
		case "ns/op":
			out.NsPerOp, seen = value, true
		case "MB/s":
			out.MBPerSec, seen = value, true
		case "B/op":
			out.BytesPerOp, seen = int(value), true
		case "allocs/op":
			out.AllocsPerOp, seen = int(value), true
		}
	}
	return out, seen
}

// splitProcs takes the -N GOMAXPROCS suffix off a benchmark's name. The number
// is a fact about the run rather than about the benchmark, so it travels in a
// field of its own and the name reads as it was written.
func splitProcs(field string) (string, int) {
	i := strings.LastIndex(field, "-")
	if i <= 0 {
		return field, 0
	}
	procs, err := strconv.Atoi(field[i+1:])
	if err != nil {
		return field, 0
	}
	return field[:i], procs
}

// readBenchmarks folds every benchmark stream in the folder into the report.
//
// An absent folder is not an error: a run that measured nothing still has
// coverage and a suite to report, and the docs page renders what it has.
func readBenchmarks(dir string) (Benchmarks, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return Benchmarks{}, err
	}
	if len(entries) == 0 {
		logf(levelWarn, "no benchmark logs in %s: the benchmarks section will be empty", dir)
		return Benchmarks{}, nil
	}
	sort.Strings(entries)
	var out Benchmarks
	for _, entry := range entries {
		id := strings.TrimSuffix(filepath.Base(entry), ".json")
		log, err := readBenchFile(id, entry)
		if err != nil {
			return Benchmarks{}, err
		}
		if len(log.results) == 0 {
			logf(levelWarn, "%s: no benchmark results in the stream", id)
			continue
		}
		logf(levelDebug, "%s: %d benchmarks on %s/%s", id, len(log.results), log.goos, log.goarch)
		out.Groups = append(out.Groups, log.group())
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Path < out.Groups[j].Path })
	total := 0
	for _, g := range out.Groups {
		total += len(g.Results)
	}
	logf(levelInfo, "benchmarks: %d measurements across %d modules", total, len(out.Groups))
	return out, nil
}
