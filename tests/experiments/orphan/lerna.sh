# The registry refuses one package mid-release. lerna tags before it
# publishes, so the refused package carries a tag for a version the
# registry never received. Then the recovery: publish from-package after
# the registry accepts again.

run_experiment() {
  fixture lerna --feature
  baseline_publish
  observe before

  step release:version lerna version --conventional-commits --yes
  observe after-version

  deny cli
  step release:publish lerna publish from-git --yes
  observe after-refusal

  allow cli
  catch_up
  step release:recovery lerna publish from-package --yes
  if [ "${STEP_RC[recovery]}" != 0 ]; then
    # lerna refuses a dirty tree; the failed publish leaves one behind.
    step manual:cleanup git checkout -q -- .
    step release:recovery2 lerna publish from-package --yes
  fi
  observe after-recovery

  step query:changed lerna changed --all --long
  echo "   next plan: $(grep -v '^lerna' "$OUT/step-changed.log" | tr '\n' ';')"

  orphan_asserts publish
  assert "the recovery needed no manual cleanup" [ "${STEP_RC[recovery]}" = 0 ]
  assert "the next plan is empty" plan_is_empty changed
}
