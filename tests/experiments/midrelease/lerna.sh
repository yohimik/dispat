# A colleague pushes to origin/main while lerna is releasing. lerna commits
# and tags before it publishes, and pushes the branch and the tags in one
# step; the colleague's push lands right before that. What follows is the
# recovery an operator is left with: rebase onto the branch, push again,
# then ask lerna what it would release next.

# The packages a correct next plan holds once the colleague's commit is on
# main and this run's release is recorded. lerna's implicit cascade reaches
# every dependent of a changed package, transitively, so the conflict
# scenario's patch of core reaches all six; the clean scenario's patch of api
# reaches api alone, which has no dependents. Sorted, space-ended, the way
# the transcript renders a plan's package list.
next_plan() {
  case "$SCENARIO" in
    conflict) echo 'api cli core docs theme ui ' ;;
    *) echo 'api ' ;;
  esac
}

run_experiment() {
  fixture lerna --feature --colleague
  baseline_publish
  observe before

  step version with_shim lerna version --conventional-commits --yes
  COLLEAGUE=$(colleague_sha)
  RELEASE=$(release_sha core@1.1.0)
  observe_marked after-version

  # The versions are tagged whether or not the branch went out, and
  # from-git publishes what the tags name.
  step publish lerna publish from-git --yes
  observe_marked after-publish

  # Recovery by hand: take the colleague's commit under the release, push
  # the branch and every tag the run made.
  recover_by_rebase
  step push git push --follow-tags origin main
  observe_marked after-recovery

  step changed lerna changed --all --long
  echo "   next plan: $(grep -v '^lerna' "$OUT/step-changed.log" | tr '\n' ';')"

  assert "version exited 0" [ "${STEP_RC[version]}" = 0 ]
  assert "registry serves core 1.1.0" observed after-publish '.packages.core.registry == "1.1.0"'
  assert "the release commit is on origin/main after recovery" observed after-recovery '.marks.release.onOriginMain == true'
  assert "every released package is consistent after recovery" \
    observed after-recovery '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c '[ "$(grep -v "^lerna" "$1" | awk "{print \$1}" | sort | tr "\n" " ")" = "$2" ]' \
    _ "$OUT/step-changed.log" "$(next_plan)"
}
