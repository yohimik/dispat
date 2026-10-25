#!/bin/sh
# The npm shim against a stand-in npm: which calls it times, which package it
# names, and that the exit code and the arguments reach npm and come back
# unchanged. The timing rows on the page are made of these records.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/shim" "$work/pkg/core" "$work/workspace"
ln -s "$here/npm-shim" "$work/shim/npm"
cat > "$work/bin/npm" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$NPM_CALLS"
case "$*" in *fail*) exit 7 ;; esac
EOF
chmod +x "$work/bin/npm"
printf '{"name": "core", "version": "1.0.0"}\n' > "$work/pkg/core/package.json"

export NPM_CALLS="$work/calls" EXPERIMENT_TIMING="$work/timing.jsonl"
PATH="$work/shim:$work/bin:$PATH"
failures=0

check() { # <case> <command...>
  name=$1; shift
  if "$@" > /dev/null; then echo "ok    $name"; else echo "FAIL  $name"; failures=$((failures + 1)); fi
}
records() { jq -s length "$EXPERIMENT_TIMING"; }

(cd "$work/pkg/core" && npm publish --registry=http://127.0.0.1:4873)
check "a publish from the package is timed under its name" \
  jq -e -s 'length == 1 and .[0].package == "core" and .[0].task == "publish" and .[0].exit == 0
    and .[0].end >= .[0].start' "$EXPERIMENT_TIMING"

(cd "$work/workspace" && npm publish "$work/pkg/core" --json --tag=latest)
check "a publish that names its folder is timed under that folder's package" \
  jq -e -s 'length == 2 and .[1].package == "core"' "$EXPERIMENT_TIMING"

rc=0
(cd "$work/pkg/core" && npm publish --fail) || rc=$?
check "a failed publish keeps npm's exit code" [ "$rc" = 7 ]
check "a failed publish is timed with its exit code" jq -e -s '.[2].exit == 7' "$EXPERIMENT_TIMING"

(cd "$work/pkg/core" && npm run --silent build && npm view core version)
check "every other call is forwarded and not timed" [ "$(records)" = 3 ]

(cd "$work/pkg/core" && EXPERIMENT_TIMING="" npm publish)
check "without a timing file a publish is forwarded and not timed" [ "$(records)" = 3 ]

check "every call reached npm with its arguments" [ "$(sed -n 2p "$NPM_CALLS")" = "publish $work/pkg/core --json --tag=latest" ]
check "npm saw every call once" [ "$(wc -l < "$NPM_CALLS" | tr -d ' ')" = 6 ]

[ "$failures" = 0 ] || { echo "$failures npm shim check(s) failed"; exit 1; }
echo 'npm shim checks passed'
