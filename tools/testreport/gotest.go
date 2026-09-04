package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// goTest is `go test` with a record of what it did: it runs the tests with
// -json, keeps the stream as coverage/testlog/<name>.json at the repository
// root for `build` to fold into the report, and prints a human summary in its
// place — plus the full output of anything that failed, which is the part a
// raw -json stream would otherwise bury. The record and its rendering live in
// one program, so a `tests` script needs no shell wrapper around go test.
//
//	testreport test <log-name> -- <go test args...>
//
// The log name is the report's id for this invocation, and it is worth
// choosing to match the coverage profile the same invocation writes (`ccme`,
// `dispat`, `integration`). A name ending in -race marks the pass run under
// the race detector; nothing else reads the name.
//
// The returned code is the test run's own, whatever the summary does: a
// failing suite must fail the release gate this guards even when its log
// cannot be summarised.
//
// The summary and the command line go to the writer the caller hands over,
// which is the process's stdout in production.
func goTest(args []string, w io.Writer) (int, error) {
	if len(args) < 2 || args[0] == "" || args[1] != "--" {
		return 2, errors.New("usage: testreport test <log-name> -- <go test args...>")
	}
	name, rest := args[0], args[2:]

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
	logPath := filepath.Join(logDir, name+".json")
	stamp := filepath.Join(root, "coverage", name+".commit")
	commit := os.Getenv("TESTREPORT_COMMIT")
	if !strings.HasSuffix(name, "-race") {
		// Invalidate an earlier pass before starting. A failed or interrupted
		// run must never certify an old profile as fresh.
		if err := os.Remove(stamp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 1, err
		}
	}

	// To stdout beside the summary, not through logf: the command line is the
	// run's human output, and a stderr line would surface as a warning in the
	// driver's log.
	fmt.Fprintf(w, "%s: go test %s -json\n", name, strings.Join(rest, " "))

	f, err := os.Create(logPath)
	if err != nil {
		return 1, err
	}
	// -json goes last rather than first: a caller may need -C, and go insists
	// that one is the very first flag on the command line. go test takes its
	// own flags after the package list too, so the two can coexist that way
	// round and only that way round.
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

	if err := summarise(logPath, w); err != nil {
		logf(levelWarn, "could not summarise %s: %v; the tests themselves exited %d", logPath, err, code)
	}
	if code == 0 && commit != "" && !strings.HasSuffix(name, "-race") {
		if err := os.WriteFile(stamp, []byte(commit+"\n"), 0o644); err != nil {
			return 1, err
		}
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
