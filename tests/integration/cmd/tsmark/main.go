// Command tsmark is the timing probe of the integration suite: it appends a
// nanosecond-resolution "<label> start <unixnano>" line to a log file,
// optionally runs a command and sleeps, then appends the matching
// "<label> end <unixnano>" line. A failed command still gets an end mark and
// retains its exit status.
//
// dispat's own JSON logs cannot answer "did these two scripts overlap?" —
// zerolog's default time field is RFC3339 at one-second resolution, which
// cannot tell two scripts a budget ran side by side from two a scheduler
// ran back to back within the same second. Wiring the test scripts to a
// tiny dependency-free binary that stamps real timestamps turns the
// question into evidence: the scheduler either launched this process while
// another was still sleeping or it did not, and the log file says which —
// with no reliance on shell tooling (`date +%N` prints literal "N" on
// macOS) or log formatting.
//
// usage: tsmark <logfile> <label> [sleepMillis] [-- command args...]
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const (
	tsmarkCommandSeparator      = "--"
	tsmarkCommandSeparatorIndex = 4
	tsmarkCommandArgIndex       = 5
	tsmarkCommandArgsIndex      = 6
	tsmarkUsage                 = "usage: tsmark <logfile> <label> [sleepMillis] [-- command args...]"
	tsmarkCommandError          = "tsmark: command:"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, tsmarkUsage)
		os.Exit(2)
	}
	logfile, label := os.Args[1], os.Args[2]

	var sleep time.Duration
	if len(os.Args) > 3 {
		ms, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "tsmark: bad sleepMillis:", err)
			os.Exit(2)
		}
		sleep = time.Duration(ms) * time.Millisecond
	}
	if len(os.Args) > tsmarkCommandSeparatorIndex &&
		(len(os.Args) <= tsmarkCommandArgIndex || os.Args[tsmarkCommandSeparatorIndex] != tsmarkCommandSeparator) {
		fmt.Fprintln(os.Stderr, tsmarkUsage)
		os.Exit(2)
	}

	f, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsmark: open logfile:", err)
		os.Exit(1)
	}
	defer f.Close()

	mark(f, label, "start")
	var commandErr error
	if len(os.Args) > tsmarkCommandSeparatorIndex {
		command := exec.Command(os.Args[tsmarkCommandArgIndex], os.Args[tsmarkCommandArgsIndex:]...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		commandErr = command.Run()
	}
	if commandErr == nil && sleep > 0 {
		time.Sleep(sleep)
	}
	mark(f, label, "end")
	if commandErr != nil {
		fmt.Fprintln(os.Stderr, tsmarkCommandError, commandErr)
		var exitErr *exec.ExitError
		if errors.As(commandErr, &exitErr) && exitErr.ExitCode() > 0 {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

// mark appends one line. A single Write of a short line is atomic on a local
// filesystem opened O_APPEND, which is all the concurrency guarantee this
// needs: several tsmark processes racing to append never interleave within a
// line.
func mark(f *os.File, label, event string) {
	line := fmt.Sprintf("%s %s %d\n", label, event, time.Now().UnixNano())
	if _, err := f.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, "tsmark: write:", err)
		os.Exit(1)
	}
}
