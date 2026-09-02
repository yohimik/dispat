# A colleague pushes to origin/main while a changesets release is under
# way. changesets has no push of its own: version, commit, publish and tag,
# then the operator pushes the branch and the tags; the colleague's push
# lands right before that. Then the recovery an operator is left with:
# rebase onto the branch, push again, and ask changesets what it would
# release next.

run_experiment() {
  fixture changesets --feature --colleague
  baseline_publish
  observe before

  step version changeset version
  step commit git commit -qam "chore: version packages"
  local release
  release=$(git rev-parse HEAD)
  step publish changeset publish
  observe after-publish --mark release="$release"

  step push with_shim git push --follow-tags origin main
  local colleague
  colleague=$(colleague_sha)
  observe after-push ${colleague:+--mark colleague=$colleague} --mark release="$release"

  step rebase git pull --rebase origin main
  if [ "${STEP_RC[rebase]}" != 0 ]; then
    step resolve bash -c 'git checkout --theirs -- packages/core/package.json && git add -A && GIT_EDITOR=true git rebase --continue'
  fi
  step push2 git push --follow-tags origin main
  observe after-recovery ${colleague:+--mark colleague=$colleague} --mark release="$release"

  step status changeset status --verbose
  echo "   next plan: $(grep -E '^\s+- [a-z]+ -> ' "$OUT/step-status.log" | sed -E 's/^\s+- //' | sort -u | tr '\n' ';')"

  assert "publish exited 0" [ "${STEP_RC[publish]}" = 0 ]
  assert "registry serves core 1.1.0" observed after-publish '.packages.core.registry == "1.1.0"'
  assert "the release commit is on origin/main after recovery" observed after-recovery '.marks.release.onOriginMain == true'
  assert "every released package is consistent after recovery" \
    observed after-recovery '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c "[ \"\$(grep -E '^\s+- [a-z]+ -> ' '$OUT/step-status.log' | sed -E 's/^\s+- ([a-z]+) ->.*/\1/' | sort -u | tr '\n' ' ')\" = '$(expected_next)' ]"
}
