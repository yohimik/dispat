#!/bin/sh
set -eu
. "$(dirname "$0")/cache.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
expected='orphan-dispat
orphan-lerna'
make_cell() {
  cell=$1 passed=$2
  mkdir -p "$work/$cell"
  printf 'log\n' > "$work/$cell.log"
  printf '{"passed":%s}\n' "$passed" > "$work/$cell/verdict.json"
  printf '{"step":"done"}\n' > "$work/$cell/observations.jsonl"
}
make_cell orphan-dispat true
make_cell orphan-lerna false
experiment_cache_write "$work" image-a "$expected"
experiment_cache_valid "$work" image-a "$expected" || { echo 'complete cache rejected' >&2; exit 1; }
experiment_cache_valid "$work" image-b "$expected" && { echo 'image mismatch accepted' >&2; exit 1; }

rm "$work/orphan-lerna/observations.jsonl"
experiment_cache_valid "$work" image-a "$expected" && { echo 'missing record accepted' >&2; exit 1; }
printf 'not json\n' > "$work/orphan-lerna/observations.jsonl"
experiment_cache_valid "$work" image-a "$expected" && { echo 'corrupt record accepted' >&2; exit 1; }
printf '{"step":"done"}\n' > "$work/orphan-lerna/observations.jsonl"

printf '{"passed":false}\n' > "$work/orphan-dispat/verdict.json"
experiment_cache_valid "$work" image-a "$expected" && { echo 'failed dispat accepted' >&2; exit 1; }
printf '{"passed":true}\n' > "$work/orphan-dispat/verdict.json"

make_cell stale-lerna false
experiment_cache_valid "$work" image-a "$expected" && { echo 'extra cell accepted' >&2; exit 1; }
echo 'experiment cache validation passed'
