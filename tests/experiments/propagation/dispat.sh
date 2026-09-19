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
