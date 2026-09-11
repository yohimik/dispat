#!/bin/sh
# Runtime validation for persisted experiment campaigns. The caller supplies
# the canonical, newline-separated cell ids produced by the current config.

experiment_cache_valid() {
  results=$1 image=$2 expected=$3
  manifest=$results/.cache-manifest.json
  expected_json=$(printf '%s\n' "$expected" | jq -Rsc 'split("\n") | map(select(length > 0))')
  jq -e --arg image "$image" --argjson cells "$expected_json" \
    '.schema == 1 and .image == $image and .cells == $cells' "$manifest" >/dev/null 2>&1 || return 1

  actual=$(find "$results" -mindepth 2 -maxdepth 2 -name verdict.json -exec dirname {} \; \
    | while IFS= read -r dir; do basename "$dir"; done | sort)
  [ "$actual" = "$expected" ] || return 1
  while IFS= read -r cell; do
    [ -s "$results/$cell.log" ] &&
      jq -e 'type == "object" and (.passed | type == "boolean")' \
        "$results/$cell/verdict.json" >/dev/null 2>&1 &&
      jq -e -s 'length > 0 and all(.[]; type == "object")' \
        "$results/$cell/observations.jsonl" >/dev/null 2>&1 || return 1
    case "$cell" in
      *-dispat) jq -e '.passed == true' "$results/$cell/verdict.json" >/dev/null 2>&1 || return 1 ;;
    esac
  done <<EOF
$expected
EOF
}

experiment_cache_write() {
  results=$1 image=$2 expected=$3
  cells=$(printf '%s\n' "$expected" | jq -Rsc 'split("\n") | map(select(length > 0))')
  jq -n --arg image "$image" --argjson cells "$cells" \
    '{schema: 1, image: $image, cells: $cells}' > "$results/.cache-manifest.json"
}
