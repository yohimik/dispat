#!/bin/sh
# The catch-up summary, over step records shaped the way each protocol writes
# them: lib/recovery.jq decides what the Catch-up column says about every
# tool, so each way a catch-up can go is pinned here rather than discovered on
# the published page.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# An observation of a finished release: every package consistent or at its
# baseline, the clone clean and level with its origin.
settled='{"label":"after","local":{"dirty":false,"mergeInProgress":false,"rebaseInProgress":false,"aheadOfOrigin":0,"behindOrigin":0},"packages":{"core":{"state":"consistent"},"cli":{"state":"consistent"},"docs":{"state":"baseline"}}}'

failures=0

# summarise <steps, one JSON object per line> <observation> <plan empty>
summarise() {
  printf '%s\n' "$1" > "$work/steps.jsonl"
  printf '%s\n' "$2" > "$work/last.json"
  jq -c -s --argjson planEmpty "$3" --slurpfile last "$work/last.json" \
    -f "$here/recovery.jq" "$work/steps.jsonl"
}

# expect <case> <want> <steps> [<observation> [<plan empty>]]
expect() {
  name=$1 want=$2 steps=$3 observation=${4:-$settled} plan_empty=${5:-true}
  got=$(summarise "$steps" "$observation" "$plan_empty")
  if [ "$got" = "$want" ]; then
    echo "ok    $name"
  else
    echo "FAIL  $name: got $got, want $want"
    failures=$((failures + 1))
  fi
}

# dispat: one failed run, then its own release once, between two questions.
expect "dispat: one release after the fault" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":true}' \
  '{"step":"release1","exit":1,"kind":"release","phase":"initial","seconds":0.5}
{"step":"retry-plan","exit":0,"kind":"query","phase":"catch-up","seconds":0.1}
{"step":"release2","exit":0,"kind":"release","phase":"catch-up","seconds":0.3}
{"step":"final-plan","exit":3,"kind":"query","phase":"catch-up","seconds":0.1}'

# lerna: the recovery fails, a manual checkout, the recovery again. The failed
# command ends the first run and the manual step keeps it ended.
expect "lerna: fail, manual, ok" \
  '{"runs":2,"releaseCommands":2,"manualCommands":1,"converged":true}' \
  '{"step":"version","exit":0,"kind":"release","phase":"initial","seconds":2}
{"step":"publish","exit":1,"kind":"release","phase":"initial","seconds":3}
{"step":"retry-plan","exit":1,"kind":"query","phase":"catch-up","seconds":1}
{"step":"recovery","exit":1,"kind":"release","phase":"catch-up","seconds":1}
{"step":"cleanup","exit":0,"kind":"manual","phase":"catch-up","seconds":0}
{"step":"recovery2","exit":0,"kind":"release","phase":"catch-up","seconds":2}
{"step":"final-plan","exit":1,"kind":"query","phase":"catch-up","seconds":1}'

# changesets: the publish succeeds and the operator pushes what it tagged.
expect "changesets: publish, then a push by hand" \
  '{"runs":1,"releaseCommands":1,"manualCommands":1,"converged":true}' \
  '{"step":"version","exit":0,"kind":"release","phase":"initial","seconds":1}
{"step":"commit","exit":0,"kind":"manual","phase":"initial","seconds":0}
{"step":"publish","exit":1,"kind":"release","phase":"initial","seconds":2}
{"step":"retry-plan","exit":0,"kind":"query","phase":"catch-up","seconds":1}
{"step":"recovery","exit":0,"kind":"release","phase":"catch-up","seconds":2}
{"step":"push","exit":0,"kind":"manual","phase":"catch-up","seconds":0}
{"step":"final-plan","exit":0,"kind":"query","phase":"catch-up","seconds":1}'

# Two of the tool's own commands in a row are one run; a failed one ends the
# run, so the command after it starts another.
expect "two commands in one run" \
  '{"runs":1,"releaseCommands":2,"manualCommands":0,"converged":true}' \
  '{"step":"a","exit":0,"kind":"release","phase":"catch-up","seconds":1}
{"step":"b","exit":0,"kind":"release","phase":"catch-up","seconds":1}'
expect "a failed command ends its run" \
  '{"runs":2,"releaseCommands":2,"manualCommands":0,"converged":true}' \
  '{"step":"a","exit":1,"kind":"release","phase":"catch-up","seconds":1}
{"step":"b","exit":0,"kind":"release","phase":"catch-up","seconds":1}'

# Questions alone: nothing was needed, which is a summary rather than none.
expect "questions only" \
  '{"runs":0,"releaseCommands":0,"manualCommands":0,"converged":true}' \
  '{"step":"release1","exit":0,"kind":"release","phase":"initial","seconds":1}
{"step":"plan","exit":3,"kind":"query","phase":"catch-up","seconds":1}'

# No catch-up phase at all: the protocol measured none.
expect "initial phase only" 'null' \
  '{"step":"release","exit":0,"kind":"release","phase":"initial","seconds":1}
{"step":"status","exit":0,"kind":"query","phase":"initial","seconds":1}'

# Each half of convergence on its own.
catch_up='{"step":"recovery","exit":0,"kind":"release","phase":"catch-up","seconds":1}'
expect "a next plan that is not empty" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":false}' "$catch_up" "$settled" false
expect "an orphan tag" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":false}' "$catch_up" \
  "$(printf '%s' "$settled" | jq -c '.packages.cli.state = "orphan"')"
expect "a dirty clone" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":false}' "$catch_up" \
  "$(printf '%s' "$settled" | jq -c '.local.dirty = true')"
expect "a clone ahead of its origin" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":false}' "$catch_up" \
  "$(printf '%s' "$settled" | jq -c '.local.aheadOfOrigin = 1')"
expect "a rebase left in progress" \
  '{"runs":1,"releaseCommands":1,"manualCommands":0,"converged":false}' "$catch_up" \
  "$(printf '%s' "$settled" | jq -c '.local.rebaseInProgress = true')"

if [ "$failures" -ne 0 ]; then
  echo "$failures recovery summary case(s) failed" >&2
  exit 1
fi
echo 'recovery summary cases passed'
