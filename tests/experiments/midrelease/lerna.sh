# A colleague pushes to origin/main while lerna is releasing. lerna commits
# and tags before it publishes, and pushes the branch and the tags in one
# step; the colleague's push lands right before that. What follows is the
# recovery an operator is left with: rebase onto the branch, push again,
# then ask lerna what it would release next.

run_experiment() {
  fixture lerna --feature --colleague
  baseline_publish
  observe before

  step version with_shim lerna version --conventional-commits --yes
  local colleague release
  colleague=$(colleague_sha)
  release=$(git rev-list -n1 core@1.1.0 2>/dev/null || echo "")
  observe after-version ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  # The versions are tagged whether or not the branch went out, and
  # from-git publishes what the tags name.
  step publish lerna publish from-git --yes
  observe after-publish ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  # Recovery by hand: take the colleague's commit under the release, push
  # the branch and every tag the run made.
  step rebase git pull --rebase origin main
  if [ "${STEP_RC[rebase]}" != 0 ]; then
    step resolve bash -c 'git checkout --theirs -- packages/core/package.json && git add -A && GIT_EDITOR=true git rebase --continue'
  fi
  step push git push --follow-tags origin main
  observe after-recovery ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  step changed lerna changed --all --long
  echo "   next plan: $(grep -v '^lerna' "$OUT/step-changed.log" | tr '\n' ';')"

  assert "version exited 0" [ "${STEP_RC[version]}" = 0 ]
  assert "registry serves core 1.1.0" observed after-publish '.packages.core.registry == "1.1.0"'
  assert "the release commit is on origin/main after recovery" observed after-recovery '.marks.release.onOriginMain == true'
  assert "every released package is consistent after recovery" \
    observed after-recovery '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c "[ \"\$(grep -v '^lerna' '$OUT/step-changed.log' | awk '{print \$1}' | sort | tr '\n' ' ')\" = '$(expected_next)' ]"
}
