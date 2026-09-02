#!/bin/bash
# The container's entrypoint: one experiment, one tool, one fresh registry.
#
#   run.sh <experiment> <tool> [scenario]
#
# experiments: orphan, midrelease   tools: lerna, nx, changesets, dispat
# scenarios (midrelease only): clean (default), conflict
#
# Everything the run leaves behind goes under /results/<experiment>[-<scenario>]-<tool>/:
# the transcript, every step's output, one observation per step, the git
# calls the tool made, and verdict.json. The exit code is the verdict when
# the tool under test is dispat, and 0 otherwise: the compared tools' runs
# are records of what they do, not expectations about them.
set -u
EXPERIMENT=${1:?experiment}
TOOL=${2:?tool}
SCENARIO=${3:-clean}
[ -f "/exp/$EXPERIMENT/$TOOL.sh" ] || { echo "no such experiment/tool: $EXPERIMENT/$TOOL" >&2; exit 2; }

if [ "$EXPERIMENT" = orphan ]; then
  OUT=/results/$EXPERIMENT-$TOOL
else
  OUT=/results/$EXPERIMENT-$SCENARIO-$TOOL
fi
rm -rf "$OUT" && mkdir -p "$OUT"
: > "$OUT/steps.jsonl"
export EXPERIMENT TOOL SCENARIO OUT

# shellcheck source=lib/common.sh
. /exp/lib/common.sh
# shellcheck disable=SC1090
. "/exp/$EXPERIMENT/$TOOL.sh"

{
  echo "experiment=$EXPERIMENT tool=$TOOL scenario=$SCENARIO"
  echo "$(dispat_version)  lerna $(lerna --version 2>/dev/null)  nx $(nx --version 2>/dev/null | grep -i global | cut -d' ' -f3)  changesets $(changeset --version 2>/dev/null | tail -1)  node $(node --version)  git $(git --version | cut -d' ' -f3)"
  start_registry
  run_experiment
  verdict
} 2>&1 | tee "$OUT/transcript.txt"

if [ "$TOOL" = dispat ] && [ "${EXPECT:-1}" = 1 ]; then
  jq -e .passed "$OUT/verdict.json" > /dev/null
fi
