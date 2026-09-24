# A patch to the provider that every consumer's ~1.0.0 range already accepts,
# carrying explicit propagation intent: `fix(core)^: correct reader`. Semver
# alone would release core and reconcile nothing else; the caret is what asks
# for core's declared consumers to be released with it.
#
# The provider publishes, cli then fails, and a run with the fault removed must
# discharge the owed consumer without publishing the provider a second time and
# without being handed any new release intent. The catch-up is dispat's
# documented recovery: run the release again.
#
# The fault is armed before the first run. dispat runs the whole release as one
# command, so what orders the provider's publication before the consumer's
# build is the fixture's `isBuildWaitingPublish: true` on core, which holds
# cli's build until core is published. The protocol reads that ordering back
# out of the run's own log rather than assuming it.
#
# Every scenario but `build` and `publish` is dispat's alone:
#
#   build-distributed  the build fault, with every build on a worker node the
#                      run reaches through the repository being released
#   deselected         no fault: the first run selects the provider and two
#                      consumers, and cli sits the provider's release out
#   held               no fault: cli is held by `Release-As: none` while the
#                      provider ships, and the operator resumes it
#   deferred           the provider is refused while cli publishes its own
#                      fix, then ships alone on the next run
#   deferred-build     the same with cli's real build, which finished against
#                      the provider's planned version before the refusal

# The worker link every dispat command of the distributed cell names: a node
# name alone, which reaches the repository being released.
WORKER=()
if [ "$SCENARIO" = build-distributed ]; then
  WORKER=(--worker build-a)
fi

# logged <step> <jq condition>: whether any line of that step's log meets it.
logged() { json_lines "$OUT/step-$1.log" | jq -se "any(.[]; $2)" > /dev/null; }
never_logged() { ! logged "$@"; }

# published_before_built <step>: the ordering, read from the run's own log.
# The first line that published core under its planned tag comes before the
# first line of cli's build.
published_before_built() {
  json_lines "$OUT/step-$1.log" | jq -se '
    def at(f): [to_entries[] | select(.value | f) | .key] | min;
    at(.package == "core" and .stage == "publish" and .plannedTag == "core@1.0.1") as $provider
    | at(.package == "cli" and .stage == "build") as $consumer
    | $provider != null and $consumer != null and $provider < $consumer' > /dev/null
}

# released_with <step> <observation> <jq filter>: the step exited 0 and the
# observation taken after it meets the filter.
released_with() {
  [ "${STEP_RC[$1]:-}" = 0 ] && observed "$2" "$3" > /dev/null
}

# The catch-up plan: cli alone, at the version it is owed, as a W193.
catch_up_plan_asserts() {
  local version=$1
  assert "the next plan catches up cli alone" [ "$(planned retry-plan)" = "cli " ]
  assert "the catch-up owes cli $version" \
    logged retry-plan ".package == \"cli\" and has(\"bump\") and .version == \"$version\""
  assert "the catch-up is reported as W193" logged retry-plan '.package == "cli" and .code == "W193"'
}

# The records every run through the fix of core leaves: cli's range names the
# provider it shipped with, and ui's own consumers were never in the release.
range_asserts() {
  assert "the recovered consumer's range names the provider it shipped with" \
    jq -e '.packages.cli.manifest.dependencies.core == "^1.0.1"' "$OUT/last-observation.json"
  assert "propagation stopped at the provider's declared consumers" \
    jq -e '[.packages.docs, .packages.theme]
      | all(.manifest.version == "1.0.0" and .manifest.dependencies.ui == "~1.0.0" and .registry == "1.0.0")' \
    "$OUT/last-observation.json"
}

run_experiment() {
  case "$SCENARIO" in
    deferred|deferred-build) run_deferred ;;
    deselected) run_deselected ;;
    held) run_held ;;
    *) run_fault ;;
  esac
}

# ---- build, publish, build-distributed ---------------------------------------

# start_worker: a worker node in this container, named build-a, whose mailbox
# is the fixture's own origin. It shares nothing with the release but the
# origin and the signing secret: its configuration names no space and no
# package, and it materializes a checkout of its own for every task.
start_worker() {
  EXPERIMENT_EXECUTION_SECRET=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  export EXPERIMENT_EXECUTION_SECRET
  mkdir -p /work/node
  cat > /work/node/dispat.yaml <<EOF
execution:
  role: worker
  name: build-a
  endpoint: $REPO-origin.git
  secretEnv: EXPERIMENT_EXECUTION_SECRET
logFormat: json
EOF
  dispat --root /work/node worker --state-dir /work/node-state --idle-timeout 600 > "$OUT/worker.log" 2>&1 &
  WORKER_PID=$!
  echo "   worker build-a started (pid $WORKER_PID), mailbox $REPO-origin.git"
}

# stop_worker: SIGTERM, which a worker answers by finishing what it claimed
# and exiting 0.
stop_worker() {
  [ -n "${WORKER_PID:-}" ] || return 0
  kill -TERM "$WORKER_PID" 2>/dev/null || true
  local rc=0
  wait "$WORKER_PID" || rc=$?
  echo "   worker build-a stopped with exit $rc"
}

run_fault() {
  fixture dispat --propagation
  baseline_publish
  observe before
  [ "$SCENARIO" = build-distributed ] && start_worker

  arm_fault
  step release:release1 dispat release "${WORKER[@]}" --log-format json
  keep_publish_logs after-failure
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan dispat status "${WORKER[@]}" --log-format json
  step release:release2 dispat release "${WORKER[@]}" --log-format json
  keep_publish_logs after-recovery
  observe after-recovery
  step query:final-plan dispat status "${WORKER[@]}" --require-release --log-format json
  stop_worker

  first_run_asserts release1 after-failure
  assert "cli built only after core was published" published_before_built release1
  # cli is opted into revertOnFail, so the failed run leaves no half-written
  # manifest behind: the version and the range it would have shipped with are
  # both rolled back.
  assert "the failed consumer's manifest was rolled back" \
    observed after-failure '.packages.cli.manifest.version == "1.0.0"
      and .packages.cli.manifest.dependencies.core == "~1.0.0"'
  case "$SCENARIO" in
    publish)
      assert "the failure is cli's publish stage" logged release1 '.package == "cli" and .failedStage == "publish"'
      assert "the registry refused cli's upload" [ "$(proxy_refused cli)" -gt 0 ]
      ;;
    *)
      assert "the failure is cli's build stage" logged release1 '.package == "cli" and .failedStage == "build"'
      # The distinction the scenario exists for: a fault in a publish-time hook
      # would have failed the publication and recorded the same summary under
      # the wrong stage.
      assert "cli never reached its publish stage" never_logged release1 '.package == "cli" and .stage == "publish"'
      ;;
  esac
  if [ "$SCENARIO" = build-distributed ]; then
    assert "cli's build ran on the worker build-a" \
      logged release1 '.message == "task outcome" and .package == "cli" and .stage == "build"
        and .worker == "build-a" and .here == false'
  fi
  catch_up_plan_asserts "1.0.0 -> 1.0.1"
  catch_up_asserts 1.0.1
  range_asserts
}

# ---- deselected --------------------------------------------------------------

# The consumer sits out its provider's release: the first run selects core, ui
# and api, and nothing fails. The next full run must find what cli is owed
# from the provider's durable release alone.
run_deselected() {
  fixture dispat --propagation
  baseline_publish
  observe before

  step release:release1 dispat release --package core,ui,api --log-format json
  keep_publish_logs after-provider
  observe after-provider

  catch_up
  step query:retry-plan dispat status --log-format json
  step release:release2 dispat release --log-format json
  keep_publish_logs after-catch-up
  observe after-catch-up
  step query:final-plan dispat status --require-release --log-format json

  first_run_shipped after-provider
  assert "the selected run released the provider and the two selected consumers" \
    released_with release1 after-provider '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  catch_up_plan_asserts "1.0.0 -> 1.0.1"
  catch_up_asserts 1.0.1
  range_asserts
}

# ---- held ----------------------------------------------------------------------

# The operator holds cli with `Release-As: none` before the provider's fix, so
# the provider ships while cli is withheld, then resumes it with
# `Release-As: auto`. The resume is the operator's own decision about cli and
# belongs to the first run's phase; what the release then needs to finish is
# the catch-up.
run_held() {
  fixture dispat --held --propagation
  baseline_publish
  observe before

  step release:release1 dispat release --log-format json
  keep_publish_logs after-hold
  observe after-hold
  local resumed
  resumed="$(( ${EXPERIMENT_EPOCH:-1735689600} + 60 * 3 )) +0000"
  step manual:resume env GIT_AUTHOR_DATE="$resumed" GIT_COMMITTER_DATE="$resumed" \
    bash -c 'git commit -q --allow-empty -m "release(cli): resume" -m "Release-As: auto" && git push -q origin main'

  catch_up
  step query:retry-plan dispat status --log-format json
  step release:release2 dispat release --log-format json
  keep_publish_logs after-catch-up
  observe after-catch-up
  step query:final-plan dispat status --require-release --log-format json

  first_run_shipped after-hold
  assert "the first run held cli (W154)" logged release1 '.package == "cli" and .code == "W154"'
  assert "the first run exited 0" [ "${STEP_RC[release1]}" = 0 ]
  catch_up_plan_asserts "1.0.0 -> 1.0.1"
  catch_up_asserts 1.0.1
  range_asserts
}

# ---- deferred, deferred-build ----------------------------------------------------

# A consumer can publish its own work against the old provider baseline after
# the provider fails. On the next run only the provider is selected. The
# consumer is therefore absent from the provider's successful release, while
# its own tag has already consumed the original `fix(core)^` commit. Recovery
# must use durable release evidence, not a still-pending commit in its window.
run_deferred() {
  fixture dispat --deferred
  baseline_publish
  observe before

  # shellcheck disable=SC2034 # read by common.sh, which checks the fault fired
  FAULT=provider
  if [ "$SCENARIO" = deferred-build ]; then
    # The refusal waits for cli's build output, so cli has built against the
    # provider's planned version by the time the provider is refused. Without
    # the gate that order is a race the build usually wins.
    deny core "$REPO/packages/cli/dist/index.js"
  else
    deny core
  fi
  step release:release1 dispat release --log-format json
  keep_publish_logs after-provider-failure
  observe after-provider-failure

  # The two propagated-only consumers were skipped before publication; their
  # version writes are inspection state from the failed run. Restoring those
  # generated manifests before selecting them in the next release is a
  # worktree cleanup by hand, not a source commit or new release instruction.
  if [ "$SCENARIO" = deferred-build ]; then
    step manual:cleanup git checkout -- packages/ui/package.json packages/api/package.json packages/cli/package.json
  else
    step manual:cleanup git checkout -- packages/ui/package.json packages/api/package.json
  fi
  allow core
  step query:provider-plan dispat status --package core,ui,api --log-format json
  if [ "$SCENARIO" = deferred ]; then
    # cli released its own fix at the head this run starts from. The provider
    # released alone there would share cli's commit, which ancestry cannot
    # order, so the plan reports E201 and a release would be refused. One
    # empty commit, dated by the fixture's clock like every commit of the
    # fixture, moves the head past cli's release.
    local retried
    retried="$(( ${EXPERIMENT_EPOCH:-1735689600} + 60 * 4 )) +0000"
    step manual:provider-commit env GIT_AUTHOR_DATE="$retried" GIT_COMMITTER_DATE="$retried" \
      git commit --allow-empty -qm "chore(core): retry the provider"
  fi
  step release:release2 dispat release --package core,ui,api --log-format json
  keep_publish_logs after-provider-success
  observe after-provider-success

  catch_up
  step query:retry-plan dispat status --log-format json
  step release:release3 dispat release --log-format json
  keep_publish_logs after-catch-up
  observe after-catch-up
  step query:final-plan dispat status --require-release --log-format json

  assert "the first run reported the failure" [ "${STEP_RC[release1]}" != 0 ]
  assert "the provider's failed version was rolled back" \
    observed after-provider-failure '.packages.core.registry == "1.0.0" and .packages.core.manifest.version == "1.0.0"'
  if [ "$SCENARIO" = deferred-build ]; then
    assert "the provider was refused after cli had built" \
      grep -qE "^failproxy: DENY PUT [^ ]+ \\(package core\\) after " /registry/failproxy.log
    assert "the built cli was withheld (W194)" logged release1 '.package == "cli" and .code == "W194"'
    assert "the provider and the other consumers released while cli waited" \
      released_with release2 after-provider-success \
      '.packages.core.registry == "1.0.1" and .packages.core.state == "consistent" and .packages.cli.registry == "1.0.0"'
    assert "cli's own fix is still pending in the next plan" \
      logged retry-plan '.package == "cli" and has("bump") and .reason == "direct" and .ownCommits == 1'
    assert "the next plan releases cli alone" [ "$(planned retry-plan)" = "cli " ]
    catch_up_asserts 1.0.1
  else
    assert "cli published its own fix while the provider failed" \
      observed after-provider-failure '.packages.cli.registry == "1.0.1" and .packages.cli.state == "consistent"'
    assert "the provider-only plan at cli's release commit reports E201 for cli" \
      logged provider-plan '.package == "cli" and .code == "E201"'
    assert "the selected provider and the other consumers released without cli" \
      released_with release2 after-provider-success \
      '.packages.core.registry == "1.0.1" and .packages.core.state == "consistent" and .packages.cli.registry == "1.0.1"'
    catch_up_plan_asserts "1.0.1 -> 1.0.2"
    catch_up_asserts 1.0.2
  fi
  assert "the recovered consumer's range names the provider it shipped with" \
    jq -e '.packages.cli.manifest.dependencies.core == "^1.0.1"' "$OUT/last-observation.json"
}
