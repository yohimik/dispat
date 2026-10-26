# A patch to the provider that every consumer's ~1.0.0 range already accepts,
# carrying explicit propagation intent: `fix(core)^: correct reader`. Semver
# alone would release core and reconcile nothing else; the caret is what asks
# for core's declared consumers to be released with it.
#
# For a failed build, the provider publishes and the next run discharges the
# owed consumer. A failed external publish has a different contract: its
# request may still be in flight, so the release lock stays held and a fresh
# invocation is fenced until that exact outcome is reconciled.
#
# The fault is armed before the first run. dispat runs the whole release as one
# command, so what orders the provider's publication before the consumer's
# build is the fixture's `isBuildWaitingPublish: true` on core, which holds
# cli's build until core is published. The protocol reads that ordering back
# out of the run's own log rather than assuming it.
#
# lerna, nx and changesets run every scenario as well, each through its own
# nearest mechanism; their protocols say which. The scenarios beyond `build`
# and `publish`:
#
#   build-distributed  the build fault, with every build on a worker node the
#                      run reaches through the repository being released
#   deselected         no fault: the first run selects the provider and two
#                      consumers, and cli sits the provider's release out
#   held               no fault: cli is held by `Release-As: none` while the
#                      provider ships, and the operator resumes it
#   deferred           cli's build is disabled; a refused provider upload
#                      retains the lock before cli's version can begin
#   deferred-build     cli's build is enabled, but its version waits for the
#                      provider's record, so no build can start before a
#                      refused provider upload settles

# The distributed cell serves an authenticated return channel within its
# isolated container. The worker identity and mailbox are pinned in the two
# node configurations, and the first node advertises this local HTTPS URL.
WORKER=()
if [ "$SCENARIO" = build-distributed ]; then
  WORKER=(--return-endpoint https://127.0.0.1:18443)
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
  receipts_asserts
}

# The release that finished pushed its tags and removed every publication
# receipt that stood in for them.
receipts_asserts() {
  assert "no publication receipt is left on origin" \
    jq -e '.origin.receipts == []' "$OUT/last-observation.json"
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
  name: build-a
  endpoint: file://$REPO-origin.git
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

  if [ "$SCENARIO" = publish ]; then
    # The registry's 502 does not prove its upload is no longer in flight.
    # A second release must encounter the retained exclusion before it can
    # send another upload. The build-fault scenarios below have no external
    # publication uncertainty and still exercise automatic catch-up.
    local cli_uploads_before_retry core_uploads_before_retry
    cli_uploads_before_retry=$(proxy_uploads cli '[0-9]+')
    core_uploads_before_retry=$(proxy_uploads core '[0-9]+')
    clear_fault
    catch_up
    step release:release2 dispat release "${WORKER[@]}" --log-format json
    observe after-fenced-retry

    first_run_asserts release1 after-failure
    assert "the registry refused cli's upload" [ "$(proxy_refused cli)" -gt 0 ]
    assert "the first run reports cli's unsettled publication (E228)" \
      logged release1 '.code == "E228" and (.error | contains("cli@1.0.1"))'
    assert "the unresolved upload retains the remote release lock" \
      git --git-dir="$REPO-origin.git" show-ref --verify --quiet refs/tags/dispat-release-lock
    assert "a fresh release is fenced by the retained lock (E336)" \
      logged release2 '.code == "E336"'
    assert "the fenced retry did not upload cli again" \
      [ "$(proxy_uploads cli '[0-9]+')" = "$cli_uploads_before_retry" ]
    assert "the fenced retry did not republish core" \
      [ "$(proxy_uploads core '[0-9]+')" = "$core_uploads_before_retry" ]
    assert "the fenced retry advanced no tags" \
      jq -se '.[0].origin.tags == .[1].origin.tags' \
        "$OUT/observe-after-failure.json" "$OUT/observe-after-fenced-retry.json"
    return
  fi

  clear_fault
  catch_up
  step query:retry-plan dispat status --log-format json
  step release:release2 dispat release "${WORKER[@]}" --log-format json
  keep_publish_logs after-recovery
  observe after-recovery
  step query:final-plan dispat status --require-release --log-format json
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

# AutoVersion makes cli's version task wait for core's signed record. That
# record cannot be settled while core's external upload is unresolved, so a
# cli build cannot precede the refused upload. The two fixtures retain their
# distinct flow declarations, but both must stop safely before cli's effect.
cli_build_enabled() { ! grep -Fq 'flow: {build: []}' "$REPO/dispat.yaml"; }

run_deferred() {
  fixture dispat --deferred
  baseline_publish
  observe before

  # shellcheck disable=SC2034 # read by common.sh, which checks the fault fired
  FAULT=provider
  deny core
  step release:release1 dispat release --log-format json
  keep_publish_logs after-provider-failure
  observe after-provider-failure
  local core_uploads_before_retry
  core_uploads_before_retry=$(proxy_uploads core '[0-9]+')
  allow core
  catch_up
  step release:release2 dispat release --log-format json
  observe after-fenced-retry

  assert "the first run reported the failure" [ "${STEP_RC[release1]}" != 0 ]
  assert "the registry refused core's upload" [ "$(proxy_refused core)" -gt 0 ]
  assert "core's unresolved external publication is reported (E228)" \
    logged release1 '.code == "E228" and (.error | contains("core@1.0.1"))'
  assert "the failed provider and cli remain at their baseline registry versions" \
    observed after-provider-failure '.packages.core.registry == "1.0.0" and .packages.cli.registry == "1.0.0"'
  assert "cli's version and build did not start while core's record was unsettled" \
    never_logged release1 '.package == "cli" and (.stage == "version" or .stage == "build")'
  if [ "$SCENARIO" = deferred-build ]; then
    assert "this fixture keeps cli's real build stage configured" \
      cli_build_enabled
  else
    assert "this fixture disables cli's build stage" \
      grep -Fq 'flow: {build: []}' "$REPO/dispat.yaml"
  fi
  assert "the unresolved upload retains the remote release lock" \
    git --git-dir="$REPO-origin.git" show-ref --verify --quiet refs/tags/dispat-release-lock
  assert "a fresh release is fenced by that lock (E336)" logged release2 '.code == "E336"'
  assert "the fenced retry made no second core upload" \
    [ "$(proxy_uploads core '[0-9]+')" = "$core_uploads_before_retry" ]
  assert "the fenced retry advanced no tags" \
    jq -se '.[0].origin.tags == .[1].origin.tags' \
      "$OUT/observe-after-provider-failure.json" "$OUT/observe-after-fenced-retry.json"
}
