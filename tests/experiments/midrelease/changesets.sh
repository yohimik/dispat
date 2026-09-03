# A colleague pushes to origin/main while a changesets release is under
# way. changesets has no push of its own: version, commit, publish and tag,
# then the operator pushes the branch and the tags; the colleague's push
# lands right before that. Then the recovery an operator is left with:
# rebase onto the branch, push again, and ask changesets what it would
# release next.

# The packages a correct next plan holds once the colleague's commit is on
# main and this run's release is recorded. changesets moves a dependent only
# when a range no longer holds, so the colleague's patch of core reaches
# nothing beyond core, and the clean scenario's patch of api is api's alone.
# Sorted, space-ended, the way the transcript renders a plan's package list.
next_plan() {
  case "$SCENARIO" in
    conflict) echo 'core ' ;;
    *) echo 'api ' ;;
  esac
}

run_experiment() {
  fixture changesets --feature --colleague
  baseline_publish
  observe before

  step version changeset version
  step commit git commit -qam "chore: version packages"
  RELEASE=$(git rev-parse HEAD)
  step publish changeset publish
  observe_marked after-publish

  # The push is the operator's: changesets makes none of its own, so the
  # injection fires on the command the protocol runs here rather than on
  # anything the tool does.
  step push with_shim git push --follow-tags origin main
  COLLEAGUE=$(colleague_sha)
  observe_marked after-push

  recover_by_rebase
  step push2 git push --follow-tags origin main
  observe_marked after-recovery

  step status changeset status --verbose
  echo "   next plan: $(grep -E '^\s+- [a-z]+ -> ' "$OUT/step-status.log" | sed -E 's/^\s+- //' | sort -u | tr '\n' ';')"

  assert "publish exited 0" [ "${STEP_RC[publish]}" = 0 ]
  assert "registry serves core 1.1.0" observed after-publish '.packages.core.registry == "1.1.0"'
  assert "the release commit is on origin/main after recovery" observed after-recovery '.marks.release.onOriginMain == true'
  assert "every released package is consistent after recovery" \
    observed after-recovery '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c '[ "$(grep -E "^\s+- [a-z]+ -> " "$1" | sed -E "s/^\s+- ([a-z]+) ->.*/\1/" | sort -u | tr "\n" " ")" = "$2" ]' \
    _ "$OUT/step-status.log" "$(next_plan)"
}
