package harness

// A stand-in `git` on PATH, for the Git failures a black-box run cannot reach
// any other way.
//
// Some of what dispat does with Git has no fixture. `write-tree` failing, or
// `commit-tree` refusing, is a broken object store rather than a state a test
// can assemble; and what those arms promise is behaviour all the same — a
// package refused before it publishes, a diagnostic instead of a fatal — so
// the invocation is made to fail instead of the repository being broken.
//
// The stand-in is a POSIX shell script named `git`, in a directory put first
// on the PATH of the process under test and of nothing else. Every invocation
// it is not asked about is exec'd through to the real Git, so a run behaves
// exactly as it always did up to the one call the scenario is about, and the
// fixture's own `Repo.Git` calls never go near it: they resolve `git` from the
// test process's own PATH.
//
// dispat runs Git as `git -C <dir> [-c user.name=… -c user.email=…] <verb> …`,
// so the joined arguments carry both the repository and the subcommand and one
// shell glob selects either: `*commit-tree*` is a verb, `*-C */.links/sdk *`
// is a repository. Where one glob matches more invocations than a scenario
// means, the ordinal narrows it further.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// GitFaultMarker is what the stand-in writes to standard error on the
// invocation it fails. It is the suite's own wording rather than Git's, which
// is the point: no assertion in this suite may depend on how Git or the
// operating system phrases a failure.
const GitFaultMarker = "dispat integration git fault"

// GitFault is one Git failure injected into the dispat process under test: a
// glob over the arguments, which of the matching invocations it applies to,
// and what the stand-in answers them with.
//
// The zero ordinal fails every match. A positive Nth fails only that match,
// and with Onward it fails that match and every one after it — which is the
// difference between "the settlement's first `update-index` fails" and "this
// repository's Git is broken from here on".
type GitFault struct {
	// Pattern is a shell glob matched against the joined arguments of each
	// invocation, as `case "$*" in <Pattern>)`. An empty pattern matches
	// nothing, so a fault must always say what it is about.
	Pattern string
	// Code is the exit status a failed invocation reports. Zero selects 1,
	// which is what `git config` answers for a key that is absent; 128 is what
	// it answers for a file it cannot read, and the two are told apart by the
	// code under test, so a scenario says which one it means.
	Code int
	// Nth is the one-based ordinal of the match this fault applies to. Zero
	// applies it to every match.
	Nth int
	// Onward extends a positive Nth to every later match as well.
	Onward bool
	// Output is what the stand-in writes on standard output in place of
	// running Git at all. A fault with an Output succeeds — it is how a
	// scenario hands the code under test a reply it cannot parse, or a
	// revision that is not the one on disk — so Code says nothing about it.
	Output string
	// After runs the real command before reporting failure. It models a lost
	// response after a successful remote write; the caller must read durable
	// state to distinguish it from a rejected write.
	After bool

	t       testing.TB
	dir     string
	matches string
	realGit string
}

// NewGitFault writes the stand-in `git` into a fresh temporary directory and
// returns the fault, ready to be handed to a run through Env.
//
// The real Git is resolved here, once, to an absolute path: the stand-in has
// to reach it without consulting the PATH it is itself first on.
func NewGitFault(t testing.TB, fault GitFault) *GitFault {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in git is a POSIX shell script")
	}
	realGit, err := exec.LookPath("git")
	require.NoError(t, err, "git not available")
	fault.t, fault.realGit = t, realGit
	fault.dir = t.TempDir()
	fault.matches = filepath.Join(fault.dir, "matches")
	require.NoError(t, os.MkdirAll(fault.matches, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fault.dir, "git"), []byte(gitFaultScript), 0o755))
	return &fault
}

// Env is what one invocation needs to run against the stand-in: the PATH that
// finds it, the real Git it falls through to, and the fault's own settings.
//
// It is appended to whatever else the scenario passes — the file-protocol
// permission a fleet needs, above all — and exec keeps the last value of a
// repeated key, so this PATH wins over the inherited one.
func (f *GitFault) Env() []string {
	code := f.Code
	if code == 0 {
		code = 1
	}
	onward := "0"
	if f.Onward {
		onward = "1"
	}
	after := "0"
	if f.After {
		after = "1"
	}
	output := f.Output
	outputFile := ""
	if strings.ContainsRune(output, '\x00') {
		outputFile = filepath.Join(f.dir, "fault-output")
		if err := os.WriteFile(outputFile, []byte(output), 0o600); err != nil {
			f.t.Fatalf("writing Git fault output: %v", err)
		}
		output = ""
	}
	return []string{
		"PATH=" + f.dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DISPAT_IT_GIT_REAL=" + f.realGit,
		"DISPAT_IT_GIT_FAULT_PATTERN=" + f.Pattern,
		"DISPAT_IT_GIT_FAULT_CODE=" + strconv.Itoa(code),
		"DISPAT_IT_GIT_FAULT_NTH=" + strconv.Itoa(f.Nth),
		"DISPAT_IT_GIT_FAULT_ONWARD=" + onward,
		"DISPAT_IT_GIT_FAULT_AFTER=" + after,
		"DISPAT_IT_GIT_FAULT_OUTPUT=" + output,
		"DISPAT_IT_GIT_FAULT_OUTPUT_FILE=" + outputFile,
		"DISPAT_IT_GIT_FAULT_DIR=" + f.matches,
	}
}

// Matches is how many invocations the pattern has selected so far, across
// every run the environment was handed to — the count a scenario asserts on
// when what it is proving is that the call was made at all.
func (f *GitFault) Matches() int {
	f.t.Helper()
	entries, err := os.ReadDir(f.matches)
	require.NoError(f.t, err)
	return len(entries)
}

// gitFaultScript is the stand-in itself.
//
// The ordinal is claimed with `mkdir`, which is the one counter a shell can
// keep without a race: a release forks Git from several goroutines at once,
// and a read-modify-write of a counter file would hand two of them the same
// number. Whoever creates the directory owns that ordinal.
//
// The pattern is expanded unquoted in the `case` word, which is what makes it
// a glob rather than a literal; a `case` pattern is not field-split, so a
// glob may contain spaces and select a whole command line.
const gitFaultScript = `#!/bin/sh
# Written by tests/integration/internal/harness/gitfault.go. It stands in for
# git on the PATH of one dispat process and passes everything it is not asked
# about through to the real one.
case "$*" in
$DISPAT_IT_GIT_FAULT_PATTERN)
	ordinal=1
	while ! mkdir "$DISPAT_IT_GIT_FAULT_DIR/$ordinal" 2>/dev/null; do
		ordinal=$((ordinal + 1))
	done
	selected=0
	if [ "$DISPAT_IT_GIT_FAULT_NTH" -eq 0 ]; then
		selected=1
	elif [ "$DISPAT_IT_GIT_FAULT_ONWARD" -eq 1 ]; then
		if [ "$ordinal" -ge "$DISPAT_IT_GIT_FAULT_NTH" ]; then
			selected=1
		fi
	elif [ "$ordinal" -eq "$DISPAT_IT_GIT_FAULT_NTH" ]; then
		selected=1
	fi
	if [ "$selected" -eq 1 ]; then
		if [ "$DISPAT_IT_GIT_FAULT_AFTER" -eq 1 ]; then
			"$DISPAT_IT_GIT_REAL" "$@" || exit "$?"
		fi
		if [ -n "$DISPAT_IT_GIT_FAULT_OUTPUT_FILE" ]; then
			cat "$DISPAT_IT_GIT_FAULT_OUTPUT_FILE"
			exit 0
		fi
		if [ -n "$DISPAT_IT_GIT_FAULT_OUTPUT" ]; then
			printf '%s' "$DISPAT_IT_GIT_FAULT_OUTPUT"
			exit 0
		fi
		echo "` + GitFaultMarker + ` $*" >&2
		exit "$DISPAT_IT_GIT_FAULT_CODE"
	fi
	;;
esac
exec "$DISPAT_IT_GIT_REAL" "$@"
`
