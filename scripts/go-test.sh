#!/bin/sh
# `go test`, with a record of what it did.
#
#   sh scripts/go-test.sh <log-name> -- <go test args...>
#
# Runs the tests with -json, keeps the stream as coverage/testlog/<log-name>.json
# for `testreport build` to fold into the documentation site's report, and
# prints a human summary in its place — plus the full output of anything that
# failed, which is the part a raw -json stream would otherwise bury.
#
# The log name is the report's id for this invocation, and it is worth choosing
# to match the coverage profile the same script writes (`ccme`, `dispat`,
# `integration`). A name ending in -race marks the pass run under the race
# detector; nothing else reads the name.
#
# Called from the `tests` scripts in dispat.yaml, so it runs in the package's
# own folder and finds the repository through its own path rather than the
# working directory.
set -eu

# Percentages and durations are formatted from here down; a locale with a comma
# decimal separator would put one in the JSON report.
LC_ALL=C
export LC_ALL

NAME=${1:-}
if [ -z "$NAME" ] || [ "${2:-}" != "--" ]; then
  echo "error: usage: go-test.sh <log-name> -- <go test args...>" >&2
  exit 2
fi
shift 2

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
LOGDIR=$ROOT/coverage/testlog
# By import path rather than by folder: this runs from whichever package's
# folder its caller was invoked in, and go.work resolves a workspace module
# from any of them.
TESTREPORT=github.com/yohimik/dispat/tools/testreport
LOG=$LOGDIR/$NAME.json
# Creates coverage/ on the way, which is where the callers' -coverprofile
# flags point.
mkdir -p "$LOGDIR"

echo "go-test: $NAME: go test $* -json"

# Two things about this line.
#
# -json goes last rather than first: a caller may need -C, and go insists that
# one is the very first flag on the command line. go test takes its own flags
# after the package list too, so the two can coexist that way round and only
# that way round.
#
# And it is not `go test ... | tee`: a pipeline in POSIX sh reports the *last*
# command's status, so a failing suite would be read as a pass and the release
# gate it guards would open. The status is taken from the test run itself and
# given back at the end, whatever the summary does.
rc=0
go test "$@" -json > "$LOG" || rc=$?

if ! go run "$TESTREPORT" render "$LOG"; then
  echo "warn: could not summarise $LOG; the tests themselves exited $rc" >&2
fi

exit "$rc"
