# The registry refuses one package mid-release. lerna tags before it
# publishes, so the refused package carries a tag for a version the
# registry never received. Then the recovery: publish from-package after
# the registry accepts again.

run_experiment() {
  fixture lerna --feature
  baseline_publish
  observe before

  step version lerna version --conventional-commits --yes
  observe after-version

  deny cli
  step publish lerna publish from-git --yes
  observe after-refusal

  allow cli
  step recovery lerna publish from-package --yes
  if [ "${STEP_RC[recovery]}" != 0 ]; then
    # lerna refuses a dirty tree; the failed publish leaves one behind.
    step cleanup git checkout -q -- .
    step recovery2 lerna publish from-package --yes
  fi
  observe after-recovery

  step changed lerna changed --all --long
  echo "   next plan: $(grep -v '^lerna' "$OUT/step-changed.log" | tr '\n' ';')"

  assert "the refused run exited non-zero" [ "${STEP_RC[publish]}" != 0 ]
  assert "the refused package carries no tag for an unpublished version" \
    observed after-refusal '.packages.cli.state != "orphan"'
  assert "the recovery published the refused package" observed after-recovery '.packages.cli.registry == "1.0.1"'
  assert "the recovery needed no manual cleanup" [ "${STEP_RC[recovery]}" = 0 ]
  assert "the next plan is empty" bash -c "! grep -qv '^lerna' '$OUT/step-changed.log'"
}
