#!/bin/bash
# The container's entrypoint: one experiment, one tool, one fresh registry.
#
#   run.sh <experiment> <tool> [scenario]
#
# experiments: orphan, midrelease   tools: lerna, nx, changesets, dispat
# scenarios: midrelease takes clean (default) or conflict; orphan takes none,
# and records an empty scenario rather than a name it never used.
#
# Everything the run leaves behind goes under /results/<experiment>[-<scenario>]-<tool>/:
# the transcript, every step's output, one observation per step, the git
# calls the tool made, and verdict.json. The exit code is the verdict when
# the tool under test is dispat, and 0 otherwise: the compared tools' runs
# are records of what they do, not expectations about them.
#
#   EXPECT=0  record the dispat cell without gating on it. The default is 1,
#             which is what makes a dispat cell a test; 0 is for re-recording
#             the protocol after a change to it, when the point of the run is
#             the transcript rather than the verdict.
set -u

EXPERIMENT=${1:?experiment}
TOOL=${2:?tool}

# Validated before either reaches a path, a results folder or a `bash -c`
# string: these three arrive from a workflow matrix and from a caller's
# environment, and an experiment that interpolated them unchecked would run
# whatever it was handed.
case "$EXPERIMENT" in
  orphan|midrelease) ;;
  *) echo "no such experiment: $EXPERIMENT (orphan, midrelease)" >&2; exit 2 ;;
esac
case "$TOOL" in
  lerna|nx|changesets|dispat) ;;
  *) echo "no such tool: $TOOL (lerna, nx, changesets, dispat)" >&2; exit 2 ;;
esac
if [ "$EXPERIMENT" = orphan ]; then
  # One scenario, so it has no name. The verdict records the empty string
  # rather than "clean", which would claim a choice the experiment never made.
  SCENARIO=""
  OUT=/results/$EXPERIMENT-$TOOL
else
  SCENARIO=${3:-clean}
  case "$SCENARIO" in
    clean|conflict) ;;
    *) echo "no such scenario: $SCENARIO (clean, conflict)" >&2; exit 2 ;;
  esac
  OUT=/results/$EXPERIMENT-$SCENARIO-$TOOL
fi
[ -f "/exp/$EXPERIMENT/$TOOL.sh" ] || { echo "no protocol for $EXPERIMENT/$TOOL" >&2; exit 2; }

rm -rf "$OUT" && mkdir -p "$OUT"
: > "$OUT/steps.jsonl"
: > "$OUT/observations.jsonl"
export EXPERIMENT TOOL SCENARIO OUT

# The library and the protocol live at the container's paths, which no
# checker standing in the source tree can follow.
# shellcheck source=/dev/null
. /exp/lib/common.sh
# shellcheck source=/dev/null
. "/exp/$EXPERIMENT/$TOOL.sh"

# The protocol, as a function rather than a brace group piped into tee: a
# pipeline runs its left side in a subshell, where an `exit` ends the subshell
# and the group's status is tee's. That made a registry that never started a
# run that passed. Here the transcript is a plain redirection, the status is
# the function's own, and the console copy is printed by a trap that fires
# however the run ends.
protocol() {
  echo "experiment=$EXPERIMENT tool=$TOOL scenario=${SCENARIO:-none}"
  echo "$(dispat_version)  lerna $(lerna_version)  nx $(nx_version)  changesets $(changesets_version)  node $(node --version)  git $(git --version | cut -d' ' -f3)"
  start_registry
  run_experiment
  verdict
}

exec 3>&1
SHOWN=0
show_transcript() {
  [ "$SHOWN" = 1 ] && return 0
  SHOWN=1
  cat "$OUT/transcript.txt" >&3 2>/dev/null || true
}
trap show_transcript EXIT

rc=0
protocol > "$OUT/transcript.txt" 2>&1 || rc=$?
show_transcript
if [ "$rc" != 0 ]; then
  echo "the protocol exited $rc" >&2
  exit "$rc"
fi

if [ "$TOOL" = dispat ] && [ "${EXPECT:-1}" = 1 ]; then
  jq -e .passed "$OUT/verdict.json" > /dev/null
fi
