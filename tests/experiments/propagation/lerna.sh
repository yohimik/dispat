# Lerna receives an ordinary `fix(core): correct reader`. What that selects is
# Lerna's own answer and not something this protocol arranges: no package is
# forced, and the version step's own output is what says which packages Lerna
# chose to release.
#
# Lerna publishes a whole workspace at once and has no per-package publish, so
# the provider and the consumer reach the registry through `lerna exec`, which
# runs the same npm publication `lerna publish` runs. That is what makes the
# provider's success observable before the consumer's fault is injected. The
# build is `lerna run build`, which runs the package script dispat's build
# stage also runs, so the build scenario is one fault under two tools.
#
# The recovery is recorded as Lerna performs it. Nothing here borrows dispat's
# catch-up expectation, and the number of packages `from-package` republishes
# is part of the record rather than something the protocol asks for.

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

# The packages a `lerna version` step selected, read from its own change list.
lerna_selected() {
  sed -n 's/^ - \([^:]*\):.*/\1/p' "$1" | sort | tr '\n' ' '
}

# The packages a `lerna publish` step reported published, read from its own
# success list. `from-package` republishes everything the registry is missing,
# so how many that is belongs in the record.
lerna_published() {
  sed -n 's/^ - \([^@]*\)@.*/\1/p' "$1" | sort | tr '\n' ' '
}

run_experiment() {
  fixture lerna --propagation
  baseline_publish
  observe before

  step version lerna version --conventional-commits --yes
  echo "   lerna selected: $(lerna_selected "$OUT/step-version.log")"
  step provider-build lerna run build --scope core
  step provider lerna exec --scope core -- npm publish --ignore-scripts --registry "$REGISTRY"
  observe provider-published

  # Armed only now: the provider is published, tagged by the version step and
  # observed, so what follows is a consumer failure rather than a run that
  # never got that far.
  arm_fault
  step consumer-build lerna run build --scope cli
  consumer_rc=${STEP_RC[consumer-build]}
  if [ "$consumer_rc" = 0 ]; then
    # A failed build stops the pipeline, so the publish is attempted only when
    # the build stage passed. In the build scenario the absence of this step is
    # the observation.
    step consumer-publish lerna exec --scope cli -- npm publish --ignore-scripts --registry "$REGISTRY"
    consumer_rc=${STEP_RC[consumer-publish]}
  fi
  observe after-failure

  # Removing the injected fault makes no source commit and supplies no new
  # release intent, so what the next plan contains is whatever durable state
  # the failed run left behind.
  head_before_clear=$(git rev-parse HEAD)
  clear_fault
  head_after_clear=$(git rev-parse HEAD)
  step retry-plan lerna changed --all --long
  echo "   retry plan: $(grep -v '^lerna' "$OUT/step-retry-plan.log" | tr '\n' ' ')"

  step recovery-build lerna run build --scope cli
  step recovery lerna publish from-package --yes
  if [ "${STEP_RC[recovery]}" != 0 ]; then
    step cleanup git checkout -q -- .
    step recovery2 lerna publish from-package --yes
    recovery_rc=${STEP_RC[recovery2]}
    recovery_log=$OUT/step-recovery2.log
  else
    recovery_rc=${STEP_RC[recovery]}
    recovery_log=$OUT/step-recovery.log
  fi
  echo "   recovery published: $(lerna_published "$recovery_log")"
  observe after-recovery
  step final-plan lerna changed --all --long

  assert "the version step succeeded" [ "${STEP_RC[version]}" = 0 ]
  # Recorded rather than required: this is the distinction the experiment is
  # about, and Lerna's answer to it is the measurement. Its version command
  # bumps every transitive dependent of a changed package, so theme and docs
  # are released too, two levels from the patch and inside their ranges the
  # whole time.
  assert "lerna released the consumer although ~1.0.0 already accepted the patch" \
    observed provider-published '.packages.cli.manifest.version == "1.0.1"
      and .packages.cli.manifest.dependencies.core == "^1.0.1"'
  assert "lerna released the provider's indirect dependents as well" \
    observed provider-published '[.packages.docs, .packages.theme]
      | all(.manifest.version == "1.0.1" and (.manifest.dependencies.ui == "^1.0.1"))'
  assert "the provider published before the consumer was attempted" \
    bash -c '[ "$1" = 0 ] && [ "$2" = 0 ] && jq -e '\''.packages.core.registry == "1.0.1"'\'' "$3" >/dev/null' \
    _ "${STEP_RC[provider-build]}" "${STEP_RC[provider]}" "$OUT/observe-provider-published.json"
  assert "the consumer attempt failed" [ "$consumer_rc" != 0 ]
  case "$SCENARIO" in
    build)
      # -x, because the log also echoes the script's own source, and a line
      # that merely quotes the fault is not the fault happening.
      assert "the injected failure occurred in cli's build stage" \
        grep -qx "injected cli build failure" "$OUT/step-consumer-build.log"
      assert "cli never reached a publication attempt" [ ! -e "$OUT/step-consumer-publish.log" ]
      ;;
    publish)
      assert "cli's build succeeded before the refusal" [ "${STEP_RC[consumer-build]}" = 0 ]
      assert "the registry refused cli's upload" \
        grep -q "DENY PUT .*(package cli)" /registry/failproxy.log
      ;;
  esac
  assert "the consumer did not publish in the failed run" \
    observed after-failure '.packages.cli.registry == "1.0.0"'
  assert "the version step tagged the consumer it could not publish" \
    observed after-failure '.packages.cli.state == "orphan"'
  assert "removing the fault creates no release commit" [ "$head_before_clear" = "$head_after_clear" ]
  assert "the tag-based next-change plan is empty" \
    bash -c '[ "$1" = 1 ] && ! grep -qv "^lerna" "$2"' \
    _ "${STEP_RC[retry-plan]}" "$OUT/step-retry-plan.log"
  assert "from-package recovered the missing consumer" \
    bash -c '[ "$1" = 0 ] && jq -e '\''.packages.cli.registry == "1.0.1"'\'' "$2" >/dev/null' \
    _ "$recovery_rc" "$OUT/observe-after-recovery.json"
  assert "the provider remains at its first published version" \
    observed after-recovery '.packages.core.registry == "1.0.1"'
  assert "the final tag-based plan is empty" \
    bash -c '[ "$1" = 1 ] && ! grep -qv "^lerna" "$2"' \
    _ "${STEP_RC[final-plan]}" "$OUT/step-final-plan.log"
}
