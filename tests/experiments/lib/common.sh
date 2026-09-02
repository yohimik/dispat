# Shared by every experiment script: the registry, the transcript, the
# observation steps and the verdict. Sourced by run.sh after it has chosen
# the experiment and the tool; the experiment scripts see only the
# functions below and the variables OUT, TOOL, SCENARIO and REGISTRY.

export REGISTRY=http://127.0.0.1:4873
export DENY_DIR=/deny
mkdir -p "$DENY_DIR" /registry

# ---- transcript -----------------------------------------------------------

say() { printf '\n== %s\n' "$*"; }

# step <name> <command...>: run one step of the protocol, recording its exit
# code in steps.jsonl beside the observation taken right after it. The
# command's output goes to the transcript and to its own file.
step() {
  local name=$1; shift
  say "$name: $*"
  local rc=0
  "$@" > "$OUT/step-$name.log" 2>&1 || rc=$?
  echo "exit=$rc"
  tail -n 12 "$OUT/step-$name.log" | sed 's/^/   | /'
  STEP_RC[$name]=$rc
  printf '{"step":%s,"exit":%s}\n' "$(jq -Rn --arg s "$name" '$s')" "$rc" >> "$OUT/steps.jsonl"
}
declare -A STEP_RC

# observe <label> [--mark name=sha ...]: read the clone, the origin and the
# registry into one JSON object, appended to observations.jsonl and shown as
# a per-package summary line.
observe() {
  local label=$1; shift
  python3 /exp/lib/observe.py "$REPO" --label "$label" "$@" > "$OUT/observe-$label.json"
  cat "$OUT/observe-$label.json" >> "$OUT/observations.jsonl"
  jq -r '"   " + .label + ": " + ([.packages | to_entries[] | .key + "=" + .value.registry + "/" + .value.state] | join(" "))' \
    "$OUT/observe-$label.json"
  jq -r '"   origin/main=" + (.origin.main.subject // "none") + " local " + .local.aheadOfOrigin + " ahead " + .local.behindOrigin + " behind" +
         ([.marks | to_entries[] | " " + .key + (if .value.onOriginMain then " on main" else " NOT on main" end)] | join(""))' \
    "$OUT/observe-$label.json"
}

# ---- the registry ---------------------------------------------------------

start_registry() {
  verdaccio --config /exp/lib/verdaccio.yaml > /registry/verdaccio.log 2>&1 &
  UPSTREAM=127.0.0.1:4874 python3 /exp/lib/failproxy.py > /registry/failproxy.log 2>&1 &
  local i
  for i in $(seq 60); do
    curl -fsS http://127.0.0.1:4874/-/ping > /dev/null 2>&1 && break
    sleep 0.5
  done
  curl -fsS http://127.0.0.1:4874/-/ping > /dev/null || { echo "registry did not start"; cat /registry/verdaccio.log; exit 1; }
  # npm wants a token even where the registry permits anonymous publish.
  NPM_TOKEN=$(curl -fsS -X PUT -H 'content-type: application/json' \
    -d '{"name":"experiments","password":"experiments-pass"}' \
    "$REGISTRY/-/user/org.couchdb.user:experiments" | jq -r .token)
  export NPM_TOKEN
}

dispat_version() { dispat --version 2>/dev/null | grep '^dispat '; }

registry_version() {
  curl -fsS "$REGISTRY/$1" 2>/dev/null | jq -r '."dist-tags".latest // "absent"' || echo absent
}

deny() { touch "$DENY_DIR/$1"; }
allow() { rm -f "$DENY_DIR/$1"; }

# ---- the fixture ----------------------------------------------------------

fixture() { # <flavour> [--feature] [--colleague]
  REPO=/work/$1
  python3 /exp/lib/fixture.py "$REPO" "$@"
  cd "$REPO"
  [ -e node_modules ] || ln -s /opt/tools/node_modules node_modules
}

baseline_publish() { # all six at 1.0.0, straight to the registry
  local p
  for p in core cli ui api theme docs; do
    (cd "$REPO/packages/$p" && npm publish --ignore-scripts --registry "$REGISTRY" > /dev/null 2>&1) \
      || { echo "baseline publish of $p failed"; exit 1; }
  done
  echo "   baseline: $(for p in core cli ui api theme docs; do printf '%s=%s ' "$p" "$(registry_version "$p")"; done)"
}

# ---- the colleague's push -------------------------------------------------

# The action the git shim fires once, from the colleague's clone: a commit
# on origin/main that the release did not plan. The clean scenario changes a
# file no release commit touches; the conflict scenario edits the line
# right above the version field the release rewrites, so the two sides
# cannot be joined without a decision.
colleague_action() {
  local clone=$REPO-colleague script=$OUT/colleague.sh
  {
    echo "set -eu"
    echo "cd $clone"
    case "$SCENARIO" in
      conflict)
        echo "python3 - <<'EOF'"
        echo "import json"
        echo "p = 'packages/core/package.json'"
        echo "d = json.load(open(p))"
        echo "d = {'name': d['name'], 'description': 'the core library', **{k: v for k, v in d.items() if k != 'name'}}"
        echo "json.dump(d, open(p, 'w'), indent=1); open(p, 'a').write('\n')"
        echo "EOF"
        echo 'msg="fix(core): describe the package"'
        [ "$TOOL" = changesets ] && echo "printf -- '---\\n\"core\": patch\\n---\\n\\ndescribe the package\\n' > .changeset/describe-the-package.md"
        ;;
      *)
        echo 'echo "// concurrent fix" >> packages/api/index.js'
        echo 'msg="fix(api): concurrent change"'
        [ "$TOOL" = changesets ] && echo "printf -- '---\\n\"api\": patch\\n---\\n\\nconcurrent change\\n' > .changeset/concurrent-change.md"
        ;;
    esac
    echo 'git add -A && git commit -q -m "$msg" && git push -q origin HEAD:refs/heads/main'
    echo "git rev-parse HEAD > $OUT/colleague.sha"
    echo 'echo "colleague pushed $(git rev-parse --short HEAD): $msg"'
  } > "$script"
  echo "bash $script"
}

# with_shim <command...>: run the tool's release command with the git shim
# first on PATH, armed to fire the colleague's push right before the tool's
# first push of its own (the release lock's push, which dispat makes before
# it plans, is not a push of the release).
with_shim() {
  mkdir -p /shim && ln -sf /exp/lib/git-shim /shim/git
  rm -rf /tmp/shim
  SHIM_LOG=$OUT/git-calls.log \
  SHIM_TRIGGER='(^| )push( |$)' \
  SHIM_EXCLUDE='dispat-release-lock|(^| )commit( |$)' \
  SHIM_ACTION="$(colleague_action)" \
  SHIM_STATE=/tmp/shim \
  PATH=/shim:$PATH "$@"
}

colleague_sha() { cat "$OUT/colleague.sha" 2>/dev/null || echo ""; }

# The packages a correct next plan holds once the colleague's commit is on
# main and the run's release is recorded: the package the colleague changed
# and, under the tool's own rule for dependents, those too. A patch of core
# reaches every dependent, transitively, under lerna and nx; it stays with
# core under changesets, whose dependents move only when a range no longer
# holds, and under dispat, whose dependents follow a provider only when its
# change reaches them. Sorted, space-ended, the way the experiments render
# a plan's package list.
expected_next() {
  case "$SCENARIO:$TOOL" in
    conflict:dispat|conflict:changesets) echo 'core ' ;;
    conflict:*) echo 'api cli core docs theme ui ' ;;
    *) echo 'api ' ;;
  esac
}

# ---- the verdict ----------------------------------------------------------

# assert <name> <command...>: an expectation about the state, recorded either
# way. The verdict fails when any expectation does, and run.sh turns that
# into the exit code for the tool the expectations are about.
declare -a CHECKS
assert() {
  local name=$1; shift
  if "$@" > /dev/null 2>&1; then
    CHECKS+=("{\"check\":$(jq -Rn --arg s "$name" '$s'),\"ok\":true}")
    echo "   ok    $name"
  else
    CHECKS+=("{\"check\":$(jq -Rn --arg s "$name" '$s'),\"ok\":false}")
    echo "   FAIL  $name"
  fi
}

# observed <label> <jq filter>: true when the filter holds on that observation.
observed() { jq -e "$2" "$OUT/observe-$1.json"; }

verdict() {
  local checks
  checks=$(printf '%s\n' "${CHECKS[@]:-}" | jq -s '.')
  jq -n --arg experiment "$EXPERIMENT" --arg tool "$TOOL" --arg scenario "$SCENARIO" \
        --arg dispat "$(dispat_version)" \
        --argjson checks "$checks" \
        --slurpfile steps "$OUT/steps.jsonl" \
        '{experiment: $experiment, tool: $tool, scenario: $scenario, dispat: $dispat,
          steps: $steps, checks: $checks,
          passed: ([$checks[] | select(.ok == false)] | length == 0)}' > "$OUT/verdict.json"
  jq -r 'if .passed then "VERDICT: all expectations hold" else "VERDICT: " + ([.checks[] | select(.ok == false) | .check] | join("; ")) end' "$OUT/verdict.json"
}
