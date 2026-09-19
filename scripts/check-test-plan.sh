#!/bin/sh
# The traceability gate, for a shell that has a Go toolchain: the integration
# test plan must name every integration test exactly once and name nothing that
# does not exist, and the requirement matrix must agree with the tree.
#
# The checking itself is `testreport testplan`, in Go, because the same gate
# runs in CI on a machine with no ripgrep and because the matrix percentages
# are a claim about the release. This file is the local spelling of it; CI
# runs the `testplan` target of Dockerfile.gotest.
#
# Usage: check-test-plan.sh [root] [flags...]
# A leading argument that is not a flag is the repository root; the rest are
# passed through, which is how a caller asks for the thresholds:
# -minimum-mapped 95 -minimum-critical 100.
set -eu
root=
case ${1:-} in
  "" | -*) ;;
  *) root=$1; shift ;;
esac
if [ -z "$root" ]; then
  root=$(git rev-parse --show-toplevel)
fi
root=$(cd "$root" && pwd)
cd "$root/tools"
exec go run github.com/yohimik/dispat/tools/testreport testplan "$@" "$root"
