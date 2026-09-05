package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// raceSuffix marks the log of a pass run under the race detector. The
// convention is `testreport test`'s, and this is the only place that reads
// it.
const raceSuffix = "-race"

// The prefixes the testing package gives a function its kind by. They are the
// whole of how this tool tells a test from a fuzz target from a benchmark.
const (
	fuzzPrefix  = "Fuzz"
	benchPrefix = "Benchmark"
)

// event is one line of a `go test -json` stream. Only the fields this tool
// reads are declared; the rest of the schema is free to grow.
type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// terminal reports whether the event is a result rather than progress.
func (e event) terminal() bool {
	return e.Action == "pass" || e.Action == "fail" || e.Action == "skip"
}

// suiteLog is one `go test -json` log, reduced to what the report and the
// failure renderer need.
type suiteLog struct {
	id   string
	path string
	race bool
	Counts

	// failedPackages holds packages whose result was `fail`, which includes
	// the ones that failed to build and therefore have no failing test to
	// point at.
	failedPackages map[string]bool
	// failedTests is keyed by package and top-level test name, so a failing
	// subtest is found through the function that ran it.
	failedTests map[string]bool

	// fuzzSeen and fuzzSeeds record the fuzz targets and what each of them was
	// run against, keyed by package and name. A target's own result and its
	// corpus entries arrive as separate events in no fixed order, so both are
	// collected and joined at the end.
	fuzzSeen  map[string]bool
	fuzzSeeds map[string]int
}

func failedKey(pkg, test string) string { return pkg + "\t" + test }

// topLevel strips the subtest path, leaving the test function's own name.
func topLevel(test string) string {
	if i := strings.Index(test, "/"); i >= 0 {
		return test[:i]
	}
	return test
}

// readLog parses one `go test -json` log.
//
// Counting is deliberately narrow. Only top-level entries are tallied as tests
// and fuzz targets: `go test -json` emits a result per subtest too, and a
// suite that counts those as tests reports a number several times larger than
// the one it can defend. Subtests are kept as their own figure. Packages are
// counted only when they ran — a package-level `skip` is `go test` reporting
// that it holds no test files.
func readLog(id string, r io.Reader) (*suiteLog, error) {
	log := &suiteLog{
		id:             id,
		race:           strings.HasSuffix(id, raceSuffix),
		failedPackages: map[string]bool{},
		failedTests:    map[string]bool{},
		fuzzSeen:       map[string]bool{},
		fuzzSeeds:      map[string]int{},
	}
	dec := json.NewDecoder(r)
	for {
		var ev event
		if err := dec.Decode(&ev); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if log.path == "" && ev.Package != "" {
			log.path = moduleOf(workspacePackage(ev.Package))
		}
		if !ev.terminal() {
			continue
		}
		if ev.Test == "" {
			if ev.Action != "skip" {
				log.Packages++
				log.Elapsed += ev.Elapsed
			}
			if ev.Action == "fail" {
				log.failedPackages[ev.Package] = true
			}
			continue
		}
		if strings.Contains(ev.Test, "/") {
			log.Subtests++
			if top := topLevel(ev.Test); strings.HasPrefix(top, fuzzPrefix) {
				// A fuzz target under a plain `go test` runs its corpus: the
				// f.Add seeds and whatever testdata/fuzz holds. Each entry
				// arrives as a subtest of the target, which is what makes the
				// corpus countable without reading the tree.
				log.fuzzSeeds[failedKey(ev.Package, top)]++
			}
			if ev.Action == "fail" {
				log.failedTests[failedKey(ev.Package, topLevel(ev.Test))] = true
			}
			continue
		}
		switch {
		case strings.HasPrefix(ev.Test, fuzzPrefix):
			log.Fuzz++
			log.fuzzSeen[failedKey(ev.Package, ev.Test)] = true
		case strings.HasPrefix(ev.Test, benchPrefix):
			// A benchmark is a measurement rather than a test. It reaches this
			// log only when somebody ran `testreport test` with -bench, and
			// counting it as a test would inflate the one number this tool
			// exists to keep honest. `testreport bench` is where it belongs.
			log.Benchmarks++
			continue
		default:
			log.Tests++
		}
		switch ev.Action {
		case "pass":
			log.Passed++
		case "fail":
			log.Failed++
			log.failedTests[failedKey(ev.Package, ev.Test)] = true
		case "skip":
			log.Skipped++
		}
	}
	return log, nil
}

// readLogFile parses one `go test -json` log from disk, naming it after the
// file so the caller's chosen id survives into the report.
func readLogFile(id, name string) (*suiteLog, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	log, err := readLog(id, f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return log, nil
}

// group is the log as the report carries it.
func (l *suiteLog) group() Group {
	return Group{ID: l.id, Path: l.path, Race: l.race, Counts: l.Counts, FuzzTargets: l.fuzzTargets()}
}

// fuzzTargets joins the targets seen with the corpus entries counted under
// them, in a fixed order so two runs of one suite produce the same document.
func (l *suiteLog) fuzzTargets() []FuzzTarget {
	if len(l.fuzzSeen) == 0 {
		return nil
	}
	out := make([]FuzzTarget, 0, len(l.fuzzSeen))
	for key := range l.fuzzSeen {
		pkg, name, _ := strings.Cut(key, "\t")
		out = append(out, FuzzTarget{
			Name:    name,
			Package: workspacePackage(pkg),
			Seeds:   l.fuzzSeeds[key],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// writeFailures prints the output of everything that failed, and nothing else.
//
// It takes a second read of the log rather than holding every output event
// from a passing run in memory. A failed package with no failed test is a
// build failure, whose output is attributed to the package rather than to any
// test — printing the package's unattributed output is what makes a compile
// error visible here instead of only in the exit code.
func (l *suiteLog) writeFailures(r io.Reader, w io.Writer) error {
	dec := json.NewDecoder(r)
	for {
		var ev event
		if err := dec.Decode(&ev); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if ev.Action != "output" || !l.failedPackages[ev.Package] {
			continue
		}
		if ev.Test != "" && !l.failedTests[failedKey(ev.Package, topLevel(ev.Test))] {
			continue
		}
		if _, err := io.WriteString(w, ev.Output); err != nil {
			return err
		}
	}
}
