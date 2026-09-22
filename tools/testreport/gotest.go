package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// testUsage is the argument shape of `testreport test`, printed whenever a
// command line does not have it.
const testUsage = "usage: testreport test <log-name> [--shards N] -- <go test args...>"

// goTest is `go test` with a record of what it did: it runs the tests with
// -json, keeps the stream as coverage/testlog/<name>.json at the repository
// root for `build` to fold into the report, and prints a human summary in its
// place, plus the full output of anything that failed, which is the part a
// raw -json stream would otherwise bury. The record and its rendering live in
// one program, so a `tests` script needs no shell wrapper around go test.
//
//	testreport test <log-name> [--shards N] -- <go test args...>
//
// The log name is the report's id for this invocation, and it is worth
// choosing to match the coverage profile the same invocation writes (`ccme`,
// `dispat`, `integration`). A name ending in -race marks the pass run under
// the race detector; nothing else reads the name.
//
// --shards N splits the run over N concurrent go test processes, each running
// an exact share of the tests, and keeps their streams as the one log a
// single process would have written (see runShardedGoTest). Without it, or
// with N of 1, the run is one go test process.
//
// The returned code is the test run's own, whatever the summary does: a
// failing suite must fail the release gate this guards even when its log
// cannot be summarised.
//
// The summary and the command line go to the writer the caller hands over,
// which is the process's stdout in production.
func goTest(args []string, w io.Writer) (int, error) {
	invocation, err := parseTestInvocation(args)
	if err != nil {
		return 2, err
	}

	// The callers run inside whichever package folder their `tests` script
	// was invoked in; the log folder is the repository's, found by the
	// workspace file the same way `go run` found this program.
	root, err := repoRoot()
	if err != nil {
		return 1, err
	}
	logDir := filepath.Join(root, "coverage", "testlog")
	// Creates coverage/ on the way, which is where the callers'
	// -coverprofile flags point.
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return 1, err
	}
	invocation.logPath = filepath.Join(logDir, invocation.logName+".json")
	stamp := filepath.Join(root, "coverage", invocation.logName+".commit")
	commit := os.Getenv("TESTREPORT_COMMIT")
	if !strings.HasSuffix(invocation.logName, "-race") {
		// Invalidate an earlier pass before starting. A failed or interrupted
		// run must never certify an old profile as fresh.
		if err := os.Remove(stamp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 1, err
		}
	}

	code, err := runTestInvocation(invocation, w)
	if err != nil {
		return 1, err
	}
	if code == 0 && commit != "" && !strings.HasSuffix(invocation.logName, "-race") {
		if err := os.WriteFile(stamp, []byte(commit+"\n"), 0o644); err != nil {
			return 1, err
		}
	}
	return code, nil
}

// testInvocation is one `testreport test` command line: the log it keeps, how
// many go test processes share the run, and the go test arguments they share.
type testInvocation struct {
	logName    string
	shardCount int
	goArgs     []string
	// logPath is where the -json stream is kept, resolved against the
	// repository once the command line has been read.
	logPath string
}

// parseTestInvocation reads `<log-name> [--shards N] -- <go test args...>`.
//
// Everything a sharded run cannot honour is refused here, before a stamp is
// invalidated or a log is touched, so a command line that could never run
// changes nothing on disk.
func parseTestInvocation(args []string) (testInvocation, error) {
	if len(args) < 2 || args[0] == "" {
		return testInvocation{}, errors.New(testUsage)
	}
	invocation := testInvocation{logName: args[0], shardCount: 1}
	rest := args[1:]
	if rest[0] == "--shards" {
		if len(rest) < 3 {
			return testInvocation{}, errors.New(testUsage)
		}
		shardCount, err := strconv.Atoi(rest[1])
		if err != nil || shardCount < 1 {
			return testInvocation{}, fmt.Errorf("--shards takes a positive number of processes, got %q", rest[1])
		}
		invocation.shardCount, rest = shardCount, rest[2:]
	}
	if rest[0] != "--" {
		return testInvocation{}, errors.New(testUsage)
	}
	invocation.goArgs = rest[1:]
	if invocation.shardCount > 1 {
		if err := verifyShardableArgs(invocation.goArgs); err != nil {
			return testInvocation{}, err
		}
	}
	return invocation, nil
}

// runTestInvocation runs the tests one process or several, whichever the
// command line asked for, and leaves the log at the invocation's path.
func runTestInvocation(invocation testInvocation, w io.Writer) (int, error) {
	if invocation.shardCount > 1 {
		return runShardedGoTest(invocation, w)
	}
	return runGoTestProcess(invocation, w)
}

// runGoTestProcess is the run in one go test process: the stream goes
// straight into the log, and the summary is read back out of it.
func runGoTestProcess(invocation testInvocation, w io.Writer) (int, error) {
	// To stdout beside the summary, not through logf: the command line is the
	// run's human output, and a stderr line would surface as a warning in the
	// driver's log.
	fmt.Fprintf(w, "%s: go test %s -json\n", invocation.logName, strings.Join(invocation.goArgs, " "))

	f, err := os.Create(invocation.logPath)
	if err != nil {
		return 1, err
	}
	// -json goes last rather than first: a caller may need -C, and go insists
	// that one is the very first flag on the command line. go test takes its
	// own flags after the package list too, so the two can coexist that way
	// round and only that way round.
	cmd := exec.Command("go", append(append([]string{"test"}, invocation.goArgs...), "-json")...)
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

	if err := summarise(invocation.logPath, w); err != nil {
		logf(levelWarn, "could not summarise %s: %v; the tests themselves exited %d", invocation.logPath, err, code)
	}
	return code, nil
}

// repoRoot ascends from the working directory to the folder holding go.work,
// which is what makes the log land in the repository's coverage/ from
// whichever package folder the caller stood in.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.work above the working directory; testreport test runs inside the workspace")
		}
		dir = parent
	}
}
