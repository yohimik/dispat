# A colleague pushes to origin/main while dispat is releasing. The push lands
# right before dispat's own push of the release commit, after every package
# has published, so the release exists on the registry and only in this
# clone when the branch refuses it.

# The packages a correct next plan holds once the colleague's commit is on
# main and this run's release is recorded. dispat moves a dependent when a
# provider's change reaches it, so the colleague's patch of core reaches
# nothing beyond core, and the clean scenario's patch of api is api's alone.
# Sorted, space-ended, the way the transcript renders a plan's package list.
next_plan() {
  case "$SCENARIO" in
    conflict) echo 'core ' ;;
    *) echo 'api ' ;;
  esac
}

run_experiment() {
  fixture dispat --feature --colleague
  baseline_publish
  observe before

  step release with_shim dispat release --log-format json
  keep_publish_logs after-release
  COLLEAGUE=$(colleague_sha)
  RELEASE=$(release_sha core@1.1.0)
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-release.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe_marked after
  echo "   release lock on origin: $(git ls-remote --tags origin dispat-release-lock | wc -l | tr -d ' ')  local: $(git tag -l dispat-release-lock | wc -l | tr -d ' ')"
  echo "   branches on origin: $(git ls-remote --heads origin | awk '{print $2}' | sed 's|refs/heads/||' | tr '\n' ' ')"

  # What the next run would do: the colleague's change, and nothing that
  # this run already released. Then that run, for what it does with the
  # clone the first one left.
  step status dispat status --log-format json
  echo "   next plan: $(jq -r 'select(.package and .version and (.message | test("● changed|catch-up"))) | .package + " " + .version + " (" + .reason + ")"' "$OUT/step-status.log" | tr '\n' ';')"
  step rerun dispat release --log-format json
  keep_publish_logs after-rerun
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-rerun.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe_marked after-rerun

  # The expectations of a release that finished: it exited cleanly, every
  # package it published is tagged on origin and the tag is on main, the
  # colleague's commit is on main outside the tagged tree, the tagged commit
  # is what the run made rather than something rewritten on top, and the
  # next plan sees only what the colleague changed.
  assert "release exited 0" [ "${STEP_RC[release]}" = 0 ]
  assert "registry serves core 1.1.0 and the three consumers at 1.0.1" \
    observed after '.packages.core.registry == "1.1.0" and .packages.cli.registry == "1.0.1" and .packages.ui.registry == "1.0.1" and .packages.api.registry == "1.0.1"'
  assert "every released package is consistent (tag on origin, on main, matching the registry)" \
    observed after '[.packages.core, .packages.cli, .packages.ui, .packages.api] | all(.state == "consistent")'
  assert "the colleague's commit is on origin/main" observed after '.marks.colleague.onOriginMain == true'
  assert "the release commit is on origin/main's first-parent chain" observed after '.marks.release.onFirstParentChain == true'
  # Both shas are guarded rather than interpolated: a run that tagged nothing
  # leaves one empty, and `git merge-base --is-ancestor "" ""` is a question
  # about neither commit that the shell was reading as an expectation that
  # held.
  assert "the colleague's commit is outside the tagged tree" \
    bash -c '[ -n "$1" ] && [ -n "$2" ] && ! git merge-base --is-ancestor "$1" "$2"' \
    _ "$COLLEAGUE" "$RELEASE"
  assert "the tag names a release commit, not a merge" \
    bash -c '[ -n "$1" ] && [ "$(git rev-list -n1 --parents "$1" | wc -w)" = 2 ]' \
    _ "$RELEASE"
  assert "the clone is level with origin, nothing in progress" \
    observed after '.local.aheadOfOrigin == 0 and .local.behindOrigin == 0 and .local.mergeInProgress == false'
  assert "the release lock was returned" \
    bash -c '[ "$(git ls-remote --tags origin dispat-release-lock | wc -l)" = 0 ]'
  case "$SCENARIO" in
    conflict)
      assert "the conflict was reported (W243) and the release still landed (W242)" \
        bash -c 'jq -r "select(.code) | .code" "$1" | grep -qx W243 && jq -r "select(.code) | .code" "$1" | grep -qx W242' \
        _ "$OUT/step-release.log"
      assert "the colleague's side is kept on a release-conflicts branch" \
        bash -c "git ls-remote --heads origin 'release-conflicts/*' | grep -q ."
      assert "the merge carries the release's side of the conflicted file" \
        bash -c 'm=$(git log --merges --first-parent -n1 --format=%H origin/main) && [ -n "$m" ] && git show "$m:packages/core/package.json" | grep -q "\"version\": \"1.1.0\"" && ! git show "$m:packages/core/package.json" | grep -q "<<<<"'
      ;;
    *)
      assert "the branch was joined, not refused (W242)" \
        bash -c 'jq -r "select(.code) | .code" "$1" | grep -qx W242' _ "$OUT/step-release.log"
      ;;
  esac
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c '[ "$(jq -r "select(.package and .version and (.message | test(\"● changed|catch-up\"))) | .package" "$1" | sort | tr "\n" " ")" = "$2" ]' \
    _ "$OUT/step-status.log" "$(next_plan)"
  local rerun_state
  case "$SCENARIO" in
    conflict) rerun_state='.packages.core.registry == "1.1.1" and .packages.core.state == "consistent"' ;;
    *) rerun_state='.packages.api.registry == "1.0.2" and .packages.api.state == "consistent"' ;;
  esac
  assert "the next run released the colleague's change from the same clone" \
    bash -c '[ "$1" = 0 ] && jq -e "$2" "$3" > /dev/null' \
    _ "${STEP_RC[rerun]}" "$rerun_state" "$OUT/observe-after-rerun.json"
}
