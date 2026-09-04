#!/bin/sh
set -eu
root=${1:-$(git rev-parse --show-toplevel)}
tmp=${TMPDIR:-/tmp}/dispat-test-plan-$$
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp"
rg -o '`Test[A-Za-z0-9_]+' "$root/tests/integration/docs/test-plan.md" \
  | tr -d '`' | sort -u > "$tmp/referenced"
set -- \
  "$root/pkg/ccme" "$root/pkg/config" "$root/pkg/manifest" \
  "$root/pkg/models" "$root/pkg/scanner" "$root/pkg/writer" \
  "$root/services/dispat" "$root/tests/integration" "$root/tools"
rg -n --no-heading '^func Test[A-Za-z0-9_]+' "$@" --glob '*_test.go' \
  | sed 's/:.*func /\t/; s/(.*//' | sort -k2,2 -k1,1 > "$tmp/qualified"
cut -f2 "$tmp/qualified" | sort -u > "$tmp/defined"
missing=$(comm -23 "$tmp/referenced" "$tmp/defined")
if [ -n "$missing" ]; then
  printf 'test plan references tests that do not exist:\n%s\n' "$missing" >&2
  exit 1
fi
rg -o '^func Test[A-Za-z0-9_]+' "$root/tests/integration" --glob '*_test.go' \
  | sed 's/.*func //' | sort -u | sed '/^TestMain$/d' > "$tmp/integration"
unassigned=$(comm -23 "$tmp/integration" "$tmp/referenced")
if [ -n "$unassigned" ]; then
  printf 'integration tests without an explicit test-plan goal:\n%s\n' "$unassigned" >&2
  exit 1
fi

awk -F '\t' '
  NR == FNR { referenced[$1] = 1; next }
  referenced[$2] {
    count[$2]++
    files[$2] = files[$2] (files[$2] == "" ? "" : ", ") $1
  }
  END { for (name in count) if (count[name] > 1) print name ": " files[name] }
' "$tmp/referenced" "$tmp/qualified" | sort > "$tmp/conflicts"
if [ -s "$tmp/conflicts" ]; then
  printf 'referenced test names defined in more than one validated module:\n%s\n' "$(cat "$tmp/conflicts")" >&2
fi
printf 'validated %s referenced tests and %s integration goal assignments\n' \
  "$(wc -l < "$tmp/referenced" | tr -d ' ')" \
  "$(wc -l < "$tmp/integration" | tr -d ' ')"
