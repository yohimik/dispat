# A patch to the provider that every consumer's ~1.0.0 range already accepts,
# carrying explicit propagation intent: `fix(core)^: correct reader`. Semver
# alone would release core and reconcile nothing else; the caret is what asks
# for core's declared consumers to be released with it.
#
# The provider publishes, cli then fails, and a run with the fault removed must
# discharge the owed consumer without publishing the provider a second time and
# without being handed any new release intent.
#
# The fault is a sentinel file the consumer's build script reads. dispat runs
# the whole release as one command, so the sentinel exists before it starts;
# what orders the two is the fixture's `isBuildWaitingPublish: true` on core,
# which holds cli's build until core is published. The protocol asserts that
# ordering from the run's own log rather than assuming it.

arm_fault() {
  case "$SCENARIO" in
    build) touch /fault-consumer-build ;;
    publish) deny cli ;;
  esac
}

clear_fault() {
  rm -f /fault-consumer-build
  allow cli
}

planned_packages() {
  jq -r 'select(.package and .version and (.message | test("● changed|catch-up"))) | .package' "$1" | sort
}

# The first log line matching each half of the ordering, so "the provider was
# published before the consumer built" is read out of the record rather than
# inferred from the fact that both happened.
ORDERED='def at(f): [to_entries[] | select(.value | f) | .key] | min;
  at(.package == "core" and .stage == "publish" and (.message | test("published"))) as $provider
  | at(.package == "cli" and .stage == "build") as $consumer
  | ($provider != null) and ($consumer != null) and ($provider < $consumer)'

run_experiment() {
  if [ "$SCENARIO" = deferred ] || [ "$SCENARIO" = deferred-build ]; then
    run_deferred
    return
  fi
  fixture dispat --propagation
  baseline_publish
  observe before

  arm_fault
  step release1 dispat release --log-format json
  keep_publish_logs after-failure
  observe after-failure

  # Removing the injected fault makes no source commit and supplies no new
  # release intent. Status therefore exposes only durable catch-up state.
  head_before_clear=$(git rev-parse HEAD)
  clear_fault
  head_after_clear=$(git rev-parse HEAD)
  step retry-plan dispat status --log-format json
  echo "   retry plan: $(planned_packages "$OUT/step-retry-plan.log" | tr '\n' ' ')"
  step release2 dispat release --log-format json
  keep_publish_logs after-recovery
  observe after-recovery
  step final-plan dispat status --log-format json

  assert "the first run failed after publishing the provider" \
    bash -c '[ "$1" != 0 ] && jq -e '\''.packages.core.registry == "1.0.1" and .packages.core.state == "consistent"'\'' "$2" >/dev/null' \
    _ "${STEP_RC[release1]}" "$OUT/observe-after-failure.json"
  assert "cli built only after core was published" \
    jq -se "$ORDERED" "$OUT/step-release1.log"
  assert "the consumer did not publish in the failed run" \
    observed after-failure '.packages.cli.registry == "1.0.0"'
  # cli is opted into revertOnFail, so the failed run leaves no half-written
  # manifest behind: the version and the range it would have shipped with are
  # both rolled back.
  assert "the failed consumer's manifest was rolled back" \
    observed after-failure '.packages.cli.manifest.version == "1.0.0"
      and .packages.cli.manifest.dependencies.core == "~1.0.0"'
  case "$SCENARIO" in
    build)
      assert "the injected failure occurred in cli's build stage" \
        jq -se 'any(.[]; .package == "cli" and .failedStage == "build")' "$OUT/step-release1.log"
      # The distinction the scenario exists for: a fault in a publish-time
      # hook would have failed the publication and recorded the same summary
      # under the wrong stage.
      assert "cli never reached its publish stage" \
        jq -se 'all(.[]; (.package == "cli" and .stage == "publish") | not)' "$OUT/step-release1.log"
      ;;
    publish)
      assert "the injected failure occurred in cli's publish stage" \
        jq -se 'any(.[]; .package == "cli" and .failedStage == "publish")' "$OUT/step-release1.log"
      assert "cli's build succeeded before the refusal" \
        jq -se 'any(.[]; .package == "cli" and .stage == "build" and .message == "build succeeded")' \
        "$OUT/step-release1.log"
      assert "the registry refused cli's upload" \
        grep -q "DENY PUT .*(package cli)" /registry/failproxy.log
      ;;
  esac
  assert "removing the fault creates no release commit" [ "$head_before_clear" = "$head_after_clear" ]
  assert "the next plan catches up cli alone" \
    bash -c '[ "$(jq -r '\''select(.package and .version and (.message | test("● changed|catch-up"))) | .package'\'' "$1" | sort | tr "\n" " ")" = "cli " ]' \
    _ "$OUT/step-retry-plan.log"
  assert "the recovery succeeded and published cli" \
    bash -c '[ "$1" = 0 ] && jq -e '\''.packages.cli.registry == "1.0.1" and .packages.cli.state == "consistent"'\'' "$2" >/dev/null' \
    _ "${STEP_RC[release2]}" "$OUT/observe-after-recovery.json"
  assert "the recovery did not republish the provider" \
    observed after-recovery '.packages.core.registry == "1.0.1" and (.packages.core.tags | keys == ["1.0.0", "1.0.1"])'
  assert "the recovered consumer's range names the provider it shipped with" \
    observed after-recovery '.packages.cli.manifest.version == "1.0.1"
      and .packages.cli.manifest.dependencies.core == "^1.0.1"'
  # The caret asks for core's declared consumers and no further. ui's own
  # consumers were never in this release, and their manifests say so.
  assert "propagation stopped at the provider's declared consumers" \
    observed after-recovery '[.packages.docs, .packages.theme]
      | all(.manifest.version == "1.0.0" and (.manifest.dependencies.ui == "~1.0.0"))'
  assert "every package's final record is consistent or untouched" \
    observed after-recovery '[.packages[] | select(.state != "consistent" and .state != "baseline")] | length == 0'
  assert "the plan after recovery is empty" \
    bash -c '[ -z "$(jq -r '\''select(.package and .version and (.message | test("● changed|catch-up"))) | .package'\'' "$1")" ]' \
    _ "$OUT/step-final-plan.log"
}

# A consumer can publish its own work against the old provider baseline after
# the provider fails. On the next run only the provider is selected. The
# consumer is therefore absent from the provider's successful release, while
# its own tag has already consumed the original `fix(core)^` commit. Recovery
# must use durable release evidence, not a still-pending commit in its window.
run_deferred() {
  fixture dispat --deferred
  baseline_publish
  observe before

  # A deliberate registry refusal is one fault, so do not spend npm's
  # retry/backoff window repeating the same denied upload.
  export NPM_CONFIG_FETCH_RETRIES=0
  deny core
  step release1 dispat release --log-format json
  keep_publish_logs after-provider-failure
  observe after-provider-failure

  # The two propagated-only consumers were skipped before publication; their
  # version writes are inspection state from the failed run. Restore those
  # generated manifests before selecting them in the next release. This is
  # a worktree cleanup, not a source commit or new release instruction.
  if [ "$SCENARIO" = deferred-build ]; then
    step cleanup git checkout -- packages/ui/package.json packages/api/package.json packages/cli/package.json
  else
    step cleanup git checkout -- packages/ui/package.json packages/api/package.json
  fi
  allow core
  step provider-plan dispat status --package core,ui,api --log-format json
  step release2 dispat release --package core,ui,api --log-format json
  keep_publish_logs after-provider-success
  observe after-provider-success

  # No source commit or new release directive follows the provider success.
  head_before_retry=$(git rev-parse HEAD)
  step retry-plan dispat status --log-format json
  head_after_retry=$(git rev-parse HEAD)
  step release3 dispat release --log-format json
  keep_publish_logs after-catch-up
  observe after-catch-up
  step final-plan dispat status --log-format json

  if [ "$SCENARIO" = deferred-build ]; then
    assert "a built cli was withheld after its provider failed" \
      bash -c '[ "$1" != 0 ] && jq -e '\''.packages.core.registry == "1.0.0" and .packages.cli.registry == "1.0.0"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release1]}" "$OUT/observe-after-provider-failure.json"
    assert "the built cli was skipped with the artifact safety guard" \
      jq -se 'any(.[]; .package == "cli" and .code == "W194" and (.reason | test("build had already embedded")))' \
      "$OUT/step-release1.log"
  else
    assert "the first provider upload failed while cli published its own fix" \
      bash -c '[ "$1" != 0 ] && jq -e '\''.packages.core.registry == "1.0.0" and .packages.cli.registry == "1.0.1" and .packages.cli.state == "consistent"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release1]}" "$OUT/observe-after-provider-failure.json"
  fi
  assert "the provider's failed version was rolled back" \
    observed after-provider-failure '.packages.core.manifest.version == "1.0.0"'
  assert "the skipped consumers' generated manifests were restored" [ "${STEP_RC[cleanup]}" = 0 ]
  if [ "$SCENARIO" = deferred-build ]; then
    assert "the provider and other consumers released while cli waited" \
      bash -c '[ "$1" = 0 ] && jq -e '\''.packages.core.registry == "1.0.1" and .packages.core.state == "consistent" and .packages.cli.registry == "1.0.0"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release2]}" "$OUT/observe-after-provider-success.json"
  else
    assert "the selected provider and other consumers released without cli" \
      bash -c '[ "$1" = 0 ] && jq -e '\''.packages.core.registry == "1.0.1" and .packages.core.state == "consistent" and .packages.cli.registry == "1.0.1"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release2]}" "$OUT/observe-after-provider-success.json"
  fi
  assert "the selected run did not release cli" \
    bash -c '[ "$(jq -r '\''select(.package == "cli" and .version and (.message | test("● changed|catch-up"))) | .package'\'' "$1")" = "" ]' \
    _ "$OUT/step-provider-plan.log"
  assert "asking for the next plan creates no source commit" [ "$head_before_retry" = "$head_after_retry" ]
  assert "the next full plan releases cli alone" \
    bash -c '[ "$(jq -r '\''select(.package and .version and (.message | test("● changed|catch-up"))) | .package'\'' "$1" | sort | tr "\n" " ")" = "cli " ]' \
    _ "$OUT/step-retry-plan.log"
  if [ "$SCENARIO" = deferred-build ]; then
    assert "the built cli retains its own pending fix" \
      jq -se 'any(.[]; .package == "cli" and .reason == "direct" and .ownCommits == 1)' "$OUT/step-retry-plan.log"
    assert "the retry builds and publishes cli against the released provider" \
      bash -c '[ "$1" = 0 ] && jq -e '\''.packages.cli.registry == "1.0.1" and .packages.cli.state == "consistent" and .packages.core.registry == "1.0.1"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release3]}" "$OUT/observe-after-catch-up.json"
  else
    assert "the plan names a catch-up from the published core" \
      jq -se 'any(.[]; .package == "cli" and .code == "W193")' "$OUT/step-retry-plan.log"
    assert "the catch-up published cli without republishing core" \
      bash -c '[ "$1" = 0 ] && jq -e '\''.packages.cli.registry == "1.0.2" and .packages.cli.state == "consistent" and .packages.core.registry == "1.0.1"'\'' "$2" >/dev/null' \
      _ "${STEP_RC[release3]}" "$OUT/observe-after-catch-up.json"
  fi
  assert "the recovered cli manifest names the provider it shipped with" \
    observed after-catch-up '.packages.cli.manifest.dependencies.core == "^1.0.1"'
  assert "the plan after catch-up is empty" \
    bash -c '[ -z "$(jq -r '\''select(.package and .version and (.message | test("● changed|catch-up"))) | .package'\'' "$1")" ]' \
    _ "$OUT/step-final-plan.log"
}
