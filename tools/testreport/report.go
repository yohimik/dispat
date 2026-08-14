// Command testreport turns what a full test run left behind — the coverage
// profiles and the `go test -json` logs — into one JSON document the
// documentation site renders.
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
	GeneratedAt time.Time `json:"generatedAt"`
	Commit      string    `json:"commit"`
	Coverage    Coverage  `json:"coverage"`
	Suite       Suite     `json:"suite"`
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
	// ID is the log's name, chosen by whoever called scripts/go-test.sh.
	ID string `json:"id"`
	// Path is the module the invocation tested, derived from the import paths
	// in the log rather than from the ID.
	Path string `json:"path"`
	// Race marks the pass that ran under the race detector, by the `-race`
	// suffix scripts/go-test.sh documents.
	Race bool `json:"race"`
	Counts
}

// Counts is the tally of one invocation, or of the whole run.
//
// Tests and Fuzz split the top-level functions by kind; Passed, Failed and
// Skipped tally the same set, so Passed+Failed+Skipped == Tests+Fuzz.
// Subtests are counted but kept apart: a suite reporting its subtests as
// "tests" inflates the number several-fold, which is exactly the kind of
// unearned claim this tool exists to stop.
type Counts struct {
	Packages int     `json:"packages"`
	Tests    int     `json:"tests"`
	Fuzz     int     `json:"fuzz"`
	Subtests int     `json:"subtests"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Skipped  int     `json:"skipped"`
	Elapsed  float64 `json:"elapsed"`
}

// add folds one invocation's tally into a running total.
func (c *Counts) add(o Counts) {
	c.Packages += o.Packages
	c.Tests += o.Tests
	c.Fuzz += o.Fuzz
	c.Subtests += o.Subtests
	c.Passed += o.Passed
	c.Failed += o.Failed
	c.Skipped += o.Skipped
	c.Elapsed += o.Elapsed
}
