#!/bin/sh
# The traceability gate's own tests. They are Go tests of the command that does
# the checking (tools/testreport/testplan_test.go), so they run in the ordinary
# tools suite instead of only when somebody remembers this file; this is the
# local spelling that runs them on their own.
#
# Each case is one failure mode the gate has to keep: a reference to a test
# that does not exist, an ambiguous bare name, an integration test with no
# plan goal, a matrix row whose status the tree does not support, and the
# thresholds.
set -eu
root=${1:-$(git rev-parse --show-toplevel)}
root=$(cd "$root" && pwd)
cd "$root/tools"
go test ./testreport -run 'TestTestPlan|TestRequirementMatrix' -count=1 -v
