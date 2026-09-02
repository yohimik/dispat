# The registry refuses one package mid-release. changesets tags after it
# publishes, so the refused package carries no tag; the version commit is
# already made, though, and the recovery is another publish of what the
# commit names.

run_experiment() {
  fixture changesets --feature
  baseline_publish
  observe before

  step version changeset version
  step commit git commit -qam "chore: version packages"
  observe after-version

  deny cli
  step publish changeset publish
  observe after-refusal

  allow cli
  step recovery changeset publish
  observe after-recovery
  step push git push --follow-tags origin main
  observe after-push

  step status changeset status --verbose
  echo "   next plan: $(grep -E '^\s+- [a-z]+ -> |no changesets' "$OUT/step-status.log" | sed -E 's/^\s+- //' | sort -u | tr '\n' ';')"

  assert "the refused run exited non-zero" [ "${STEP_RC[publish]}" != 0 ]
  assert "the refused package carries no tag for an unpublished version" \
    observed after-refusal '.packages.cli.state != "orphan"'
  assert "the recovery published the refused package" observed after-recovery '.packages.cli.registry == "1.0.1"'
  assert "every released package is consistent once pushed" \
    observed after-push '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is empty" bash -c "! grep -Eq '^\s+- [a-z]+ -> ' '$OUT/step-status.log'"
}
