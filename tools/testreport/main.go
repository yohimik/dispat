package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const usage = `usage:
  testreport build  [-coverage dir] [-out file] [-commit sha]   build the report from a full test run
  testreport render <log>                                       summarise one go test -json log
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	case "render":
		err = render(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		logf(levelError, "%v", err)
		os.Exit(1)
	}
}

// build assembles the report from what a full test run left in the coverage
// folder: one profile per package's `tests` script, and one `go test -json`
// log per invocation.
func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dir := fs.String("coverage", "coverage", "folder holding the coverage profiles and testlog/")
	out := fs.String("out", filepath.Join("packages", "docs", "data", "report.json"), "where to write the report")
	commit := fs.String("commit", "", "the commit the run measured; discovered from git when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report := Report{GeneratedAt: time.Now().UTC().Truncate(time.Second), Commit: *commit}
	if report.Commit == "" {
		report.Commit = discoverCommit()
	}

	cov, err := readCoverage(*dir)
	if err != nil {
		return err
	}
	report.Coverage = cov

	suite, err := readSuite(filepath.Join(*dir, "testlog"))
	if err != nil {
		return err
	}
	report.Suite = suite

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(body, '\n'), 0o644); err != nil {
		return err
	}
	logf(levelInfo, "wrote %s: %.1f%% of %d statements, %d tests, %d fuzz targets, commit %s",
		*out, report.Coverage.Total.Percent, report.Coverage.Total.Statements,
		report.Suite.Totals.Tests, report.Suite.Totals.Fuzz, short(report.Commit))
	return nil
}

// mergeOutputs are the files scripts/coverage-badge.sh writes back into the
// same folder it reads. Folding a merge into itself would not change a
// block-keyed total, but it would list the badge's own working files as if a
// package had produced them.
var mergeOutputs = map[string]bool{"coverage.out": true, "coverage-unit.out": true}

// integrationProfile is the one profile that is not a module's own test
// binary: the black-box suite's instrumented dispat, converted from its
// GOCOVERDIR by `go tool covdata textfmt`.
const integrationProfile = "integration.out"

// readCoverage measures the three layers the docs report: the unit profiles on
// their own, the integration profile on its own, and everything merged.
//
// The layers overlap heavily — the CLI's own tests and the instrumented binary
// both cover services/dispat — so the total is neither their sum nor their
// average. It is the merge, which is why each layer is accumulated separately
// and the total accumulated again from all of them.
func readCoverage(dir string) (Coverage, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.out"))
	if err != nil {
		return Coverage{}, err
	}
	unit, integration, total := newCoverage(), newCoverage(), newCoverage()
	var units []string
	haveIntegration := false
	for _, entry := range entries {
		name := filepath.Base(entry)
		if mergeOutputs[name] {
			logf(levelDebug, "skipping %s: a merge output, not a package's profile", name)
			continue
		}
		layer := unit
		if name == integrationProfile {
			layer, haveIntegration = integration, true
		} else {
			units = append(units, strings.TrimSuffix(name, ".out"))
		}
		if err := layer.addFile(entry); err != nil {
			return Coverage{}, err
		}
		if err := total.addFile(entry); err != nil {
			return Coverage{}, err
		}
		logf(levelDebug, "read %s", entry)
	}
	if len(units) == 0 {
		return Coverage{}, fmt.Errorf("no unit coverage profiles in %s: run the whole suite first (`dispat run tests --since all`)", dir)
	}
	sort.Strings(units)
	logf(levelInfo, "coverage: unit profiles %s", strings.Join(units, " "))
	if !haveIntegration {
		logf(levelWarn, "no %s in %s: the integration layer will read as zero", integrationProfile, dir)
	}
	cov := Coverage{
		Total:       total.stats(),
		Unit:        unit.stats(),
		Integration: integration.stats(),
		Modules:     total.modules(),
	}
	logf(levelInfo, "coverage: unit %.1f%% / integration %.1f%% / total %.1f%%",
		cov.Unit.Percent, cov.Integration.Percent, cov.Total.Percent)
	return cov, nil
}

// readSuite folds every `go test -json` log in the folder into the report. The
// log's file name is its id, which is what scripts/go-test.sh was given.
func readSuite(dir string) (Suite, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return Suite{}, err
	}
	if len(entries) == 0 {
		return Suite{}, fmt.Errorf("no test logs in %s: the `tests` scripts run through scripts/go-test.sh, which writes them", dir)
	}
	sort.Strings(entries)
	var suite Suite
	for _, entry := range entries {
		id := strings.TrimSuffix(filepath.Base(entry), ".json")
		log, err := readLogFile(id, entry)
		if err != nil {
			return Suite{}, err
		}
		if log.Failed > 0 || len(log.failedPackages) > 0 {
			// Not fatal: the report describes what happened, and the run that
			// produced a failure has already failed on its own exit code.
			logf(levelWarn, "%s: %d failed", id, log.Failed)
		}
		logf(levelDebug, "%s: %d tests, %d fuzz targets, %d subtests, %d packages, %.0fs",
			id, log.Tests, log.Fuzz, log.Subtests, log.Packages, log.Elapsed)
		suite.Groups = append(suite.Groups, log.group())
		// The race pass is the same tests again under a different detector, so
		// folding it into the totals would report the integration suite twice
		// and claim several hundred tests the repository does not have. It
		// keeps its own row instead, which is where the claim that the suite is
		// race-clean comes from.
		if !log.race {
			suite.Totals.add(log.Counts)
		}
	}
	// By module rather than by log name, which is the order the docs table
	// reads in: `pkg/ccme` before `services/dispat` before `tests/integration`,
	// with the race pass under the run it repeats.
	sort.Slice(suite.Groups, func(i, j int) bool {
		a, b := suite.Groups[i], suite.Groups[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return !a.Race && b.Race
	})
	logf(levelInfo, "suite: %d tests and %d fuzz targets across %d invocations, %s",
		suite.Totals.Tests, suite.Totals.Fuzz, len(suite.Groups), duration(suite.Totals.Elapsed))
	return suite, nil
}

// render summarises one `go test -json` log for a human, and prints the output
// of everything that failed.
//
// It is what makes the JSON stream acceptable as the only output of a test
// run: `go test -json` is unreadable, and a CI log that hides why a test
// failed is worse than a missing report.
func render(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("render takes one log file")
	}
	name := fs.Arg(0)

	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	log, err := readLog(strings.TrimSuffix(filepath.Base(name), ".json"), f)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	if log.Failed > 0 || len(log.failedPackages) > 0 {
		if _, err := f.Seek(0, 0); err != nil {
			return err
		}
		if err := log.writeFailures(f, os.Stdout); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	summary := fmt.Sprintf("%s: %d tests", log.id, log.Tests)
	if log.Fuzz > 0 {
		summary += fmt.Sprintf(" and %d fuzz targets", log.Fuzz)
	}
	summary += fmt.Sprintf(" in %d packages, %s: %d passed", log.Packages, duration(log.Elapsed), log.Passed)
	if log.Skipped > 0 {
		summary += fmt.Sprintf(", %d skipped", log.Skipped)
	}
	if log.Failed > 0 {
		summary += fmt.Sprintf(", %d FAILED", log.Failed)
	}
	if len(log.failedPackages) > 0 && log.Failed == 0 {
		summary += fmt.Sprintf(", %d package(s) FAILED to build", len(log.failedPackages))
	}
	fmt.Println(summary)
	return nil
}

// discoverCommit asks git which commit the run measured. Empty rather than
// fatal when there is no git and no CI variable: the stamp is there to expose
// a stale artifact, and a missing stamp is itself visible on the page.
func discoverCommit() string {
	if sha := os.Getenv("GITHUB_SHA"); sha != "" {
		return sha
	}
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		logf(levelWarn, "no commit stamp: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// duration renders a run's seconds the way its length deserves: a unit suite
// finishes in a number of seconds worth a decimal, the integration suite in a
// number of minutes where one is noise.
func duration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	return fmt.Sprintf("%dm%ds", int(seconds)/60, int(seconds)%60)
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

type level string

const (
	levelDebug level = "debug"
	levelInfo  level = "info"
	levelWarn  level = "warn"
	levelError level = "error"
)

// logf writes progress and diagnostics to stderr, leaving stdout to `render`'s
// human output. Debug is off unless TESTREPORT_DEBUG is set: the per-file and
// per-log lines are what you want when a number looks wrong and noise
// otherwise.
func logf(l level, format string, args ...any) {
	if l == levelDebug && os.Getenv("TESTREPORT_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "testreport: %s: %s\n", l, fmt.Sprintf(format, args...))
}
