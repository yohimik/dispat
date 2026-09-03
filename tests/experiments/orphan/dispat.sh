# The registry refuses one package mid-release. dispat tags after it
# publishes, so a refused publish leaves no tag to lie about; the next run
# picks up the package that did not go out and nothing else.

run_experiment() {
  fixture dispat --feature
  baseline_publish
  observe before

  deny cli
  step release1 dispat release --log-format json
  keep_publish_logs after-refusal
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-release1.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe after-refusal
  echo "   cli publish log: $(tail -n 2 /tmp/publish-cli.log 2>/dev/null | tr '\n' ' ')"

  allow cli
  step release2 dispat release --log-format json
  keep_publish_logs after-recovery
  observe after-recovery

  step status dispat status --log-format json
  echo "   next plan: $(jq -r 'select(.package and .version and (.message | test("● changed|catch-up"))) | .package + " " + .version' "$OUT/step-status.log" | tr '\n' ';')"

  assert "the refused run exited non-zero" [ "${STEP_RC[release1]}" != 0 ]
  assert "the refused package is neither tagged nor on the registry after the refusal" \
    observed after-refusal '.packages.cli.registry == "1.0.0" and (.packages.cli.tags | keys == ["1.0.0"])'
  assert "the packages that published are consistent after the refusal" \
    observed after-refusal '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  assert "the recovery run exited 0" [ "${STEP_RC[release2]}" = 0 ]
  assert "the recovery published the refused package and tagged it" \
    observed after-recovery '.packages.cli.registry == "1.0.1" and .packages.cli.state == "consistent"'
  assert "the recovery republished nothing" \
    observed after-recovery '.packages.core.registry == "1.1.0" and .packages.ui.registry == "1.0.1" and .packages.api.registry == "1.0.1" and (.packages.core.tags | keys == ["1.0.0", "1.1.0"])'
  assert "the next plan is empty" \
    bash -c '[ -z "$(jq -r "select(.package and .version and (.message | test(\"● changed|catch-up\"))) | .package" "$1")" ]' \
    _ "$OUT/step-status.log"
}
