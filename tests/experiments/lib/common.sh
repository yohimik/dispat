# Shared by every experiment script: the registry, the transcript, the
# observation steps and the verdict. Sourced by run.sh after it has chosen
# the experiment and the tool; the experiment scripts see only the
# functions below and the variables OUT, TOOL, SCENARIO and REGISTRY.

export REGISTRY=http://127.0.0.1:4873
export DENY_DIR=/deny

# The fixture's six packages and the version they all start at, named once
# here and read from the environment by fixture.py, observe.py and
# failproxy.py. Four copies of the same list is how the fixture and the state
# reader come to disagree about what a run is even about.
export EXPERIMENT_PACKAGES="core cli ui api theme docs"
export EXPERIMENT_BASELINE=1.0.0

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
  jq -nc --arg s "$name" --argjson e "$rc" '{step: $s, exit: $e}' >> "$OUT/steps.jsonl"
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
  jq -r '"   origin/main=" + (.origin.main.subject // "none") +
         " local " + (.local.aheadOfOrigin | tostring) + " ahead " + (.local.behindOrigin | tostring) + " behind" +
         ([.marks | to_entries[] | " " + .key + (if .value.onOriginMain then " on main" else " NOT on main" end)] | join(""))' \
    "$OUT/observe-$label.json"
}

# observe_marked <label>: observe with whichever of the two commits this run
# has identified so far. A mark of an empty sha is not a mark, so a commit the
# protocol has not reached yet contributes no argument rather than an argument
# the observer would have to interpret.
observe_marked() {
  local label=$1
  local -a marks=()
  [ -n "${COLLEAGUE:-}" ] && marks+=(--mark "colleague=$COLLEAGUE")
  [ -n "${RELEASE:-}" ] && marks+=(--mark "release=$RELEASE")
  observe "$label" "${marks[@]}"
}

# ---- the registry ---------------------------------------------------------

# wait_for <url> <what> <log>: poll until it answers, or say which one did not
# come up and show its log. Both halves are polled: the proxy is what every
# tool talks to, and a proxy that is not listening yet turns the first publish
# of a run into a connection refused nobody attributes to the harness.
wait_for() {
  local url=$1 what=$2 log=$3 attempt=0
  while [ "$attempt" -lt 60 ]; do
    curl -fsS "$url" > /dev/null 2>&1 && return 0
    attempt=$((attempt + 1))
    sleep 0.5
  done
  echo "$what never answered $url: $attempt attempts over 30s"
  cat "$log" 2>/dev/null
  exit 1
}

start_registry() {
  verdaccio --config /exp/lib/verdaccio.yaml > /registry/verdaccio.log 2>&1 &
  UPSTREAM=127.0.0.1:4874 python3 /exp/lib/failproxy.py > /registry/failproxy.log 2>&1 &
  wait_for http://127.0.0.1:4874/-/ping verdaccio /registry/verdaccio.log
  wait_for "$REGISTRY/-/ping" "the fault proxy" /registry/failproxy.log

  # npm wants a token even where the registry permits anonymous publish. A
  # request that answers with something other than a token is fatal here
  # rather than an empty NPM_TOKEN every later publish fails on for a reason
  # that names the wrong thing.
  local body
  body=$(curl -fsS -X PUT -H 'content-type: application/json' \
    -d '{"name":"experiments","password":"experiments-pass"}' \
    "$REGISTRY/-/user/org.couchdb.user:experiments") || {
      echo "the registry refused the token request"
      cat /registry/failproxy.log 2>/dev/null
      exit 1
    }
  NPM_TOKEN=$(printf '%s' "$body" | jq -er .token) || {
      echo "the registry returned no token: $body"
      exit 1
    }
  export NPM_TOKEN
  echo "registry ready: verdaccio on 4874, the fault proxy on 4873"
}

dispat_version() { dispat --version 2>/dev/null | grep '^dispat '; }

# The compared tools' versions, each read the way that tool reports one. nx
# prints a block rather than a version, and its "Global" line says "Not found"
# for a local install, so the local line is the one that answers the question;
# the package's own manifest is the fallback when the block's wording moves.
lerna_version() { lerna --version 2>/dev/null || echo unknown; }
changesets_version() { changeset --version 2>/dev/null | tail -n 1 || echo unknown; }
nx_version() {
  local v
  v=$(nx --version 2>/dev/null | sed -n 's/.*Local:[[:space:]]*v\{0,1\}\([0-9][^[:space:]]*\).*/\1/p' | head -n 1)
  [ -n "$v" ] || v=$(node -p "require('/opt/tools/node_modules/nx/package.json').version" 2>/dev/null)
  echo "${v:-unknown}"
}

# The version the registry serves for a package, or `absent`. A refusal and an
# empty body are both "absent"; the distinction the record needs between a
# missing package and a broken registry is observe.py's, which reports the
# error rather than folding it into a state.
registry_version() {
  local body
  body=$(curl -fsS "$REGISTRY/$1" 2>/dev/null) || { echo absent; return 0; }
  printf '%s' "$body" | jq -er '."dist-tags".latest' 2>/dev/null || echo absent
}

deny() { touch "$DENY_DIR/$1"; }
allow() { rm -f "$DENY_DIR/$1"; }

# ---- the fixture ----------------------------------------------------------

fixture() { # <flavour> [--feature] [--colleague]
  REPO=/work/$1
  python3 /exp/lib/fixture.py "$REPO" "$@"
  cd "$REPO" || exit 1
  [ -e node_modules ] || ln -s /opt/tools/node_modules node_modules
}

baseline_publish() { # all six at the baseline, straight to the registry
  local p
  for p in $EXPERIMENT_PACKAGES; do
    (cd "$REPO/packages/$p" && npm publish --ignore-scripts --registry "$REGISTRY" > /dev/null 2>&1) \
      || { echo "baseline publish of $p failed"; exit 1; }
  done
  local line=""
  for p in $EXPERIMENT_PACKAGES; do line="$line$p=$(registry_version "$p") "; done
  echo "   baseline: $line"
}

# keep_publish_logs <tag>: the per-package publish output the dispat fixture
# redirects to /tmp, copied into the record under the phase it belongs to. The
# refusal's log is overwritten by the recovery's, so a copy at each phase is
# the only way the refused upload's own message survives the container.
keep_publish_logs() {
  local tag=$1 f
  mkdir -p "$OUT/publish-$tag"
  for f in /tmp/publish-*.log; do
    [ -e "$f" ] || continue
    cp "$f" "$OUT/publish-$tag/$(basename "$f")"
  done
}

# ---- the colleague's push -------------------------------------------------

# The action the git shim fires once, from the colleague's clone: a commit
# on origin/main that the release did not plan. The clean scenario changes a
# file no release commit touches; the conflict scenario edits the line
# right above the version field the release rewrites, so the two sides
# cannot be joined without a decision.
# The heredoc lines below are the generated script's source, not this
# shell's: the escapes and the single-quoted expansions are what the colleague's
# script must contain, so they travel through echo unexpanded on purpose.
# shellcheck disable=SC2016,SC2028
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
# first push of its own. The trigger reads the git subcommand, so a commit
# whose message happens to say "push" is not one; the exclude carries the
# release lock's tag name, because dispat's push of the lock precedes the plan
# and is not a push of the release.
with_shim() {
  mkdir -p /shim && ln -sf /exp/lib/git-shim /shim/git
  rm -rf /tmp/shim
  SHIM_LOG=$OUT/git-calls.log \
  SHIM_TRIGGER='^push$' \
  SHIM_EXCLUDE='dispat-release-lock' \
  SHIM_ACTION="$(colleague_action)" \
  SHIM_STATE=/tmp/shim \
  PATH=/shim:$PATH "$@"
}

colleague_sha() { cat "$OUT/colleague.sha" 2>/dev/null || echo ""; }

# release_sha <tag>: the commit a tag the run wrote names, or empty when the
# run wrote none. Empty is a real answer here (a tool that never tagged), so
# every assertion about it guards rather than interpolating an empty string
# into a git command that would then mean something else entirely.
release_sha() { git rev-list -n1 "$1" 2>/dev/null || echo ""; }

# ---- the recoveries -------------------------------------------------------

# recover_by_rebase: the recovery an operator is left with when the tool's own
# push was refused. The rebase stops on the conflict scenario's overlapping
# edit; the release's side of the version line is what a release keeps, so the
# resolution takes it and carries on.
recover_by_rebase() {
  step rebase git pull --rebase origin main
  if [ "${STEP_RC[rebase]}" != 0 ]; then
    step resolve bash -c 'git checkout --theirs -- packages/core/package.json && git add -A && GIT_EDITOR=true git rebase --continue'
  fi
}

# orphan_asserts <publish-step>: the three expectations the orphan experiment
# holds every compared tool to. Stated once, because the same three sentences
# in three files drift into three different sentences.
orphan_asserts() {
  local publish=$1
  assert "the refused run exited non-zero" [ "${STEP_RC[$publish]}" != 0 ]
  assert "the refused package carries no tag for an unpublished version" \
    observed after-refusal '.packages.cli.state != "orphan"'
  assert "the recovery published the refused package" \
    observed after-recovery '.packages.cli.registry == "1.0.1"'
}

# ---- the verdict ----------------------------------------------------------

# assert <name> <command...>: an expectation about the state, recorded either
# way. The verdict fails when any expectation does, and run.sh turns that
# into the exit code for the tool the expectations are about.
#
# The command's own output is kept rather than discarded: an expectation that
# fails because jq could not parse a file said so, once, into /dev/null.
declare -a CHECKS
ASSERTS=0
assert() {
  local name=$1; shift
  ASSERTS=$((ASSERTS + 1))
  local log=$OUT/assert-$ASSERTS.log
  if "$@" > "$log" 2>&1; then
    CHECKS+=("$(jq -nc --arg s "$name" '{check: $s, ok: true}')")
    echo "   ok    $name"
  else
    CHECKS+=("$(jq -nc --arg s "$name" '{check: $s, ok: false}')")
    echo "   FAIL  $name"
    tail -n 8 "$log" | sed 's/^/         | /'
  fi
}

# observed <label> <jq filter>: true when the filter holds on that observation.
observed() { jq -e "$2" "$OUT/observe-$1.json"; }

# validate_records: every line-oriented record this run wrote is one JSON
# object per line, or the run failed.
#
# This is a check on the harness rather than on the tool, which is why it is
# not an expectation: a record nobody can parse is not a record, and every
# reader downstream (the report, the summary, the page) would report its
# absence as a tool that did nothing. It exists because the shim's own logger
# once handed git's arguments to `jq --args`, which parsed `-C` as a colour
# flag and refused `-m` outright, so the calls that mattered most were the
# ones missing from the log.
validate_records() {
  local f
  for f in "$OUT/steps.jsonl" "$OUT/observations.jsonl" "$OUT/git-calls.log"; do
    [ -s "$f" ] || continue
    python3 -c 'import json, sys
path = sys.argv[1]
for n, line in enumerate(open(path), 1):
    if not line.strip():
        continue
    try:
        json.loads(line)
    except ValueError as e:
        raise SystemExit(f"{path}:{n} is not one JSON object on one line: {e}")' "$f" || {
      echo "the records are not readable; the run is not a record"
      exit 1
    }
  done
}

# keep_registry_logs: verdaccio's own log and the fault proxy's decision log,
# copied into the record. Without them a question about what the registry did
# (which request was denied, what a retry got, whether the proxy answered at
# all) can only be asked by running the cell again.
keep_registry_logs() {
  mkdir -p "$OUT/registry"
  cp /registry/verdaccio.log /registry/failproxy.log "$OUT/registry/" 2>/dev/null || true
}

verdict() {
  keep_registry_logs
  validate_records
  local checks
  if [ "${#CHECKS[@]}" -eq 0 ]; then
    # A protocol that recorded nothing has not passed. The empty array read as
    # "no failures" is how a run that fell over before its first expectation
    # reported that everything held.
    checks=$(jq -nc '[{check: "no expectations were recorded", ok: false}]')
  else
    checks=$(printf '%s\n' "${CHECKS[@]}" | jq -s '.')
  fi
  jq -n --arg experiment "$EXPERIMENT" --arg tool "$TOOL" --arg scenario "$SCENARIO" \
        --arg dispat "$(dispat_version)" \
        --argjson checks "$checks" \
        --slurpfile steps "$OUT/steps.jsonl" \
        '{experiment: $experiment, tool: $tool, scenario: $scenario, dispat: $dispat,
          steps: $steps, checks: $checks,
          passed: ([$checks[] | select(.ok == false)] | length == 0)}' > "$OUT/verdict.json"
  jq -r 'if .passed then "VERDICT: all expectations hold" else "VERDICT: " + ([.checks[] | select(.ok == false) | .check] | join("; ")) end' "$OUT/verdict.json"
}
