# The registry refuses one package mid-release. nx release tags before it
# publishes, so the refused package carries a tag for a version the
# registry never received. Then the recovery: publish again after the
# registry accepts.

run_experiment() {
  fixture nx --feature
  baseline_publish
  observe before

  step version nx release --skip-publish
  observe after-version

  deny cli
  step publish nx release publish --registry "$REGISTRY"
  observe after-refusal

  allow cli
  step recovery nx release publish --registry "$REGISTRY"
  observe after-recovery
  echo "   recovery said: $(grep -iE 'already published|skipp|409|conflict' "$OUT/step-recovery.log" | head -3 | tr -s ' ' | tr '\n' ';')"

  step push git push --follow-tags origin main
  observe after-push

  step next nx release --dry-run --skip-publish
  echo "   next plan: $(grep -E '^[a-z]+ .*New version [0-9.]+ written' "$OUT/step-next.log" | sed -E 's/^([a-z]+) .*New version ([0-9.]+) written.*/\1 \2/' | sort -u | tr '\n' ';')"

  assert "the refused run exited non-zero" [ "${STEP_RC[publish]}" != 0 ]
  assert "the refused package carries no tag for an unpublished version" \
    observed after-refusal '.packages.cli.state != "orphan"'
  assert "the recovery published the refused package" observed after-recovery '.packages.cli.registry == "1.0.1"'
  assert "every released package is consistent once pushed" \
    observed after-push '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is empty" bash -c "! grep -q 'New version' '$OUT/step-next.log'"
}
