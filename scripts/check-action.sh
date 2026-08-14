#!/bin/sh
# What the composite action promises, checked against what it actually did.
#
# Two callers share this one file so they cannot drift: the Action workflow,
# which runs on a change to action.yml or either installer, and the release's
# post-release job, which runs it against the version that was just published.
# Both call it under `shell: bash` on all three runner operating systems, so it
# stays POSIX and does no path arithmetic of its own.
#
#   usage: check-action.sh <expected-version> <reported-version> <reported-path>
#
# <reported-version> and <reported-path> are the action's own `version` and
# `path` outputs; the binary is then called off $PATH rather than through that
# path, because the $GITHUB_PATH entry is the other half of what the action
# promises.
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: check-action.sh <expected-version> <reported-version> <reported-path>" >&2
  exit 2
fi

EXPECTED=$1
REPORTED=$2
REPORTED_PATH=$3

echo "expected version: $EXPECTED"
echo "version output:   $REPORTED"
echo "path output:      $REPORTED_PATH"

if [ "$REPORTED" != "$EXPECTED" ]; then
  echo "the action reports $REPORTED, expected $EXPECTED" >&2
  exit 1
fi

if [ ! -x "$REPORTED_PATH" ]; then
  echo "the path output is not an executable: $REPORTED_PATH" >&2
  exit 1
fi

# Straight off PATH, the way a job after this action would call it.
dispat --version
if ! dispat --version | grep -q "$EXPECTED"; then
  echo "dispat on PATH does not report $EXPECTED" >&2
  exit 1
fi

echo "the action installed $EXPECTED and put it on PATH"
