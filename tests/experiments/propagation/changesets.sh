# Changesets receives explicit patch entries for core and its three direct
# consumers. This is the nearest equivalent to the requested release set of
# `fix(core)^`; Changesets does not interpret that commit syntax. The operator
# runs each build script and publishes core first, because `changeset publish`
# has no per-package build stage or provider-first injection point.

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

run_experiment() {
  fixture changesets --propagation
  baseline_publish
  observe before

  step version changeset version
  step commit git commit -qam "chore: version packages"
  step provider-build npm --prefix packages/core run --silent build
  step provider-publish bash -c 'cd packages/core && npm publish --ignore-scripts --registry "$REGISTRY"'
  observe provider-published

  arm_fault
  step consumer-build npm --prefix packages/cli run --silent build
  consumer_rc=${STEP_RC[consumer-build]}
  if [ "$consumer_rc" = 0 ]; then
    step consumer-publish bash -c 'cd packages/cli && npm publish --ignore-scripts --registry "$REGISTRY"'
    consumer_rc=${STEP_RC[consumer-publish]}
  fi
  observe after-failure

  head_before_clear=$(git rev-parse HEAD)
  clear_fault
  head_after_clear=$(git rev-parse HEAD)
  step retry-plan changeset status --verbose
  step recovery-build npm --prefix packages/cli run --silent build
  step recovery changeset publish
  observe after-recovery
  step push git push --follow-tags origin main
  observe after-push
  step final-plan changeset status --verbose

  assert "the version step selected core and the three explicit consumers" \
    observed provider-published '[.packages.core, .packages.cli, .packages.ui, .packages.api]
      | all(.manifest.version == "1.0.1")'
  assert "the provider published before the consumer was attempted" \
    bash -c '[ "$1" = 0 ] && [ "$2" = 0 ] && jq -e '\''.packages.core.registry == "1.0.1"'\'' "$3" >/dev/null' \
    _ "${STEP_RC[provider-build]}" "${STEP_RC[provider-publish]}" "$OUT/observe-provider-published.json"
  assert "the consumer attempt failed" [ "$consumer_rc" != 0 ]
  case "$SCENARIO" in
    build)
      assert "the injected failure occurred in cli's build script" \
        grep -qx "injected cli build failure" "$OUT/step-consumer-build.log"
      assert "cli never reached a publication attempt" [ ! -e "$OUT/step-consumer-publish.log" ]
      ;;
    publish)
      assert "cli's build succeeded before the refusal" [ "${STEP_RC[consumer-build]}" = 0 ]
      assert "the registry refused cli's upload" \
        grep -q "DENY PUT .*(package cli)" /registry/failproxy.log
      ;;
  esac
  assert "the consumer did not publish in the failed attempt" \
    observed after-failure '.packages.cli.registry == "1.0.0"'
  assert "removing the fault creates no release commit" [ "$head_before_clear" = "$head_after_clear" ]
  assert "the recovery published the missing consumer" \
    bash -c '[ "$1" = 0 ] && jq -e '\''.packages.cli.registry == "1.0.1"'\'' "$2" >/dev/null' \
    _ "${STEP_RC[recovery]}" "$OUT/observe-after-recovery.json"
  assert "the provider remains at its first published version" \
    observed after-recovery '.packages.core.registry == "1.0.1"'
  assert "the registry-aware retry skipped the already-published core" \
    bash -c 'grep -q "3 packages are already published" "$1" && ! grep -q "^core@1.0.1$" "$1"' \
    _ "$OUT/step-recovery.log"
  assert "the manually published provider still lacks a Changesets tag" \
    observed after-push '.packages.core.state == "unrecorded"'
  assert "status remains empty after recovery" \
    bash -c '! grep -Eq "^\\s+- [a-z]+ -> " "$1"' _ "$OUT/step-final-plan.log"
}
