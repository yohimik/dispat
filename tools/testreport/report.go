// Command testreport turns what a full test run left behind — the coverage
// profiles, the `go test -json` logs and the benchmark streams — into one JSON
// document the documentation site renders.
//
// It exists because the numbers were written by hand in four places and had
// already drifted apart: the coverage page, the CLI README and the integration
// suite's results file each claimed a different total. A release measures all
// of them for real; this turns that measurement into the only copy.
//
// The document is a release artifact, never committed: the release workflow
// builds it in the job that runs the full suite and hands it to the job that
// builds the site. A site built without one renders the same pages with the
// numbers left out.
package main

import "time"

// Report is the whole document. Field order here is the order it serialises
// in, which is the order a human reading the artifact wants.
type Report struct {
	// GeneratedAt and Commit are what make a stale artifact visible: the docs
	// page prints both, so numbers from another commit cannot pass silently
	// for this one.
	GeneratedAt time.Time  `json:"generatedAt"`
	Commit      string     `json:"commit"`
	Coverage    Coverage   `json:"coverage"`
	Suite       Suite      `json:"suite"`
	Benchmarks  Benchmarks `json:"benchmarks"`
}

// Coverage is the statement coverage of the workspace, by layer and by
// package.
type Coverage struct {
	// Total is every profile merged: the number the README badge carries.
	Total Stats `json:"total"`
	// Unit is the modules' own test binaries, Integration the instrumented
	// dispat binary the black-box suite drives. They overlap, which is the
	// point — Total is not their sum.
	Unit        Stats    `json:"unit"`
	Integration Stats    `json:"integration"`
	Modules     []Module `json:"modules"`
}

// Stats is one statement-coverage measurement.
type Stats struct {
	Statements int     `json:"statements"`
	Covered    int     `json:"covered"`
	Percent    float64 `json:"percent"`
}

// Module is one Go module of the workspace with the packages inside it. The
// module's own numbers are its packages' totals, so the docs table can render
// a module row and indent its packages under it.
type Module struct {
	Path string `json:"path"`
	Stats
	Packages []Package `json:"packages"`
}

// Package is one Go package's coverage, keyed by its workspace-relative path.
type Package struct {
	Path string `json:"path"`
	Stats
}

// Suite is what the test run itself did, one entry per `go test` invocation.
type Suite struct {
	Totals Counts  `json:"totals"`
	Groups []Group `json:"groups"`
}

// Group is one invocation: the `tests` script of one package, or one of the
// integration suite's two passes.
type Group struct {
	// ID is the log's name, chosen by whoever called `testreport test`.
	ID string `json:"id"`
	// Path is the module the invocation tested, derived from the import paths
	// in the log rather than from the ID.
	Path string `json:"path"`
	// Race marks the pass that ran under the race detector, by the `-race`
	// suffix `testreport test` documents.
	Race bool `json:"race"`
	Counts
	// FuzzTargets are the fuzz functions this invocation ran, each with the
	// number of corpus entries it was executed against. A fuzz target under a
	// plain `go test` runs its seed corpus, so the entry count is what the run
	// actually exercised rather than a promise about a fuzzing session nobody
	// ran here.
	FuzzTargets []FuzzTarget `json:"fuzzTargets"`
}

// FuzzTarget is one fuzz function and what the run put through it.
type FuzzTarget struct {
	Name string `json:"name"`
	// Package is the import path, so two targets of one name in two packages
	// stay apart.
	Package string `json:"package"`
	// Seeds is the corpus entries executed: the f.Add seeds, plus whatever
	// testdata/fuzz holds for the target.
	Seeds int `json:"seeds"`
}

// Benchmarks is what the benchmark pass measured, one group per invocation.
//
// It is a section of its own rather than part of Suite because a benchmark run
// is a different run: the suite proves the code is right, and this says what it
// costs. Folding a measurement into a tally would make neither readable.
type Benchmarks struct {
	Groups []BenchGroup `json:"groups"`
}

// BenchGroup is one `testreport bench` invocation, with the machine it ran on.
// The machine is part of the measurement: a nanosecond figure without the CPU
// that produced it is a number nobody can compare anything to.
type BenchGroup struct {
	ID      string      `json:"id"`
	Path    string      `json:"path"`
	Goos    string      `json:"goos"`
	Goarch  string      `json:"goarch"`
	CPU     string      `json:"cpu"`
	Results []Benchmark `json:"results"`
}

// Benchmark is one benchmark's result, as `go test -bench -benchmem` reports
// it. Zero is "the benchmark did not report this": a run without -benchmem
// leaves the two allocation figures at nought, and only a benchmark calling
// SetBytes reports a throughput.
type Benchmark struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	// Procs is the GOMAXPROCS the name carried as its -N suffix.
	Procs int `json:"procs"`
	// Runs is the iteration count the timing was averaged over, which is what
	// says how much to trust it.
	Runs        int     `json:"runs"`
	NsPerOp     float64 `json:"nsPerOp"`
	BytesPerOp  int     `json:"bytesPerOp"`
	AllocsPerOp int     `json:"allocsPerOp"`
	MBPerSec    float64 `json:"mbPerSec"`
}

// Counts is the tally of one invocation, or of the whole run.
//
// Tests and Fuzz split the top-level functions by kind; Passed, Failed and
// Skipped tally the same set, so Passed+Failed+Skipped == Tests+Fuzz.
// Subtests are counted but kept apart: a suite reporting its subtests as
// "tests" inflates the number several-fold, which is exactly the kind of
// unearned claim this tool exists to stop.
type Counts struct {
	Packages int `json:"packages"`
	Tests    int `json:"tests"`
	Fuzz     int `json:"fuzz"`
	// Benchmarks counts the benchmark functions that ran in this invocation,
	// which is nought for every suite: benchmarks run in a pass of their own
	// and are measured rather than tallied. The field exists so that a suite
	// run with -bench cannot quietly report them as tests.
	Benchmarks int     `json:"benchmarks"`
	Subtests   int     `json:"subtests"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Skipped    int     `json:"skipped"`
	Elapsed    float64 `json:"elapsed"`
}

// add folds one invocation's tally into a running total.
func (c *Counts) add(o Counts) {
	c.Packages += o.Packages
	c.Tests += o.Tests
	c.Fuzz += o.Fuzz
	c.Benchmarks += o.Benchmarks
	c.Subtests += o.Subtests
	c.Passed += o.Passed
	c.Failed += o.Failed
	c.Skipped += o.Skipped
	c.Elapsed += o.Elapsed
}
