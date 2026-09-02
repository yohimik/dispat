# A colleague pushes to origin/main while dispat is releasing. The push lands
# right before dispat's own push of the release commit, after every package
# has published, so the release exists on the registry and only in this
# clone when the branch refuses it.

run_experiment() {
  fixture dispat --feature --colleague
  baseline_publish
  observe before

  step release with_shim dispat release --log-format json
  local colleague release
  colleague=$(colleague_sha)
  release=$(git rev-list -n1 core@1.1.0 2>/dev/null || echo "")
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-release.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe after ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}
  echo "   release lock on origin: $(git ls-remote --tags origin dispat-release-lock | wc -l | tr -d ' ')  local: $(git tag -l dispat-release-lock | wc -l | tr -d ' ')"
  echo "   branches on origin: $(git ls-remote --heads origin | awk '{print $2}' | sed 's|refs/heads/||' | tr '\n' ' ')"

  # What the next run would do: the colleague's change, and nothing that
  # this run already released. Then that run, for what it does with the
  # clone the first one left.
  step status dispat status --log-format json
  echo "   next plan: $(jq -r 'select(.package and .version and (.message | test("● changed|catch-up"))) | .package + " " + .version + " (" + .reason + ")"' "$OUT/step-status.log" | tr '\n' ';')"
  step rerun dispat release --log-format json
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-rerun.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe after-rerun ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

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
  assert "the colleague's commit is outside the tagged tree" \
    bash -c "! git merge-base --is-ancestor $colleague $release"
  assert "the tag names a release commit, not a merge" \
    bash -c "[ \"\$(git rev-list -n1 --parents $release | wc -w)\" = 2 ]"
  assert "the clone is level with origin, nothing in progress" \
    observed after '.local.aheadOfOrigin == "0" and .local.behindOrigin == "0" and .local.mergeInProgress == false'
  assert "the release lock was returned" bash -c '[ "$(git ls-remote --tags origin dispat-release-lock | wc -l)" = 0 ]'
  case "$SCENARIO" in
    conflict)
      assert "the conflict was reported (W243) and the release still landed (W242)" \
        bash -c "jq -r 'select(.code) | .code' '$OUT/step-release.log' | grep -qx W243 && jq -r 'select(.code) | .code' '$OUT/step-release.log' | grep -qx W242"
      assert "the colleague's side is kept on a release-conflicts branch" \
        bash -c "git ls-remote --heads origin 'release-conflicts/*' | grep -q ."
      assert "the merge carries the release's side of the conflicted file" \
        bash -c "m=\$(git log --merges --first-parent -n1 --format=%H origin/main) && git show \$m:packages/core/package.json | grep -q '\"version\": \"1.1.0\"' && ! git show \$m:packages/core/package.json | grep -q '<<<<'"
      ;;
    *)
      assert "the branch was joined, not refused (W242)" \
        bash -c "jq -r 'select(.code) | .code' '$OUT/step-release.log' | grep -qx W242"
      ;;
  esac
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c "[ \"\$(jq -r 'select(.package and .version and (.message | test(\"● changed|catch-up\"))) | .package' '$OUT/step-status.log' | sort | tr '\n' ' ')\" = '$(expected_next)' ]"
  local rerun_state
  case "$SCENARIO" in
    conflict) rerun_state='.packages.core.registry == "1.1.1" and .packages.core.state == "consistent"' ;;
    *) rerun_state='.packages.api.registry == "1.0.2" and .packages.api.state == "consistent"' ;;
  esac
  assert "the next run released the colleague's change from the same clone" \
    bash -c "[ '${STEP_RC[rerun]}' = 0 ] && jq -e '$rerun_state' '$OUT/observe-after-rerun.json'"
}
