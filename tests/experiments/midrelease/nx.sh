# A colleague pushes to origin/main while nx is releasing. Under the git
# settings nx documents for a release run (release.git with commit, tag and
# push), nx stages the version bumps and pushes before it commits or tags;
# the colleague's push lands right before that push. What follows is the
# recovery an operator is left with: drop the staged bumps, rebase, release
# again, publish, push what nx left local, then ask nx what it would
# version next.

run_experiment() {
  fixture nx --feature --colleague
  baseline_publish
  observe before

  step release with_shim nx release --skip-publish
  local colleague
  colleague=$(colleague_sha)
  observe after-release ${colleague:+--mark colleague=$colleague}
  echo "   staged, uncommitted: $(git diff --cached --name-only | tr '\n' ' ')"

  # Recovery by hand. The rebase refuses a dirty index, and the staged bumps
  # hold nothing nx does not recompute from git history, so they are
  # dropped and nx runs again on the joined branch.
  step recover bash -c 'git reset -q --hard && git pull --rebase origin main'
  step release2 nx release --skip-publish
  local release
  release=$(git rev-list -n1 core@1.1.0 2>/dev/null || echo "")
  observe after-release2 ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  step publish nx release publish --registry "$REGISTRY"
  observe after-publish ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  step push git push --follow-tags origin main
  observe after-push ${colleague:+--mark colleague=$colleague} ${release:+--mark release=$release}

  step next nx release --dry-run --skip-publish
  echo "   next plan: $(grep -E '^[a-z]+ .*New version [0-9.]+ written' "$OUT/step-next.log" | sed -E 's/^([a-z]+) .*New version ([0-9.]+) written.*/\1 \2/' | sort -u | tr '\n' ';')"

  assert "release exited 0" [ "${STEP_RC[release]}" = 0 ]
  assert "the release run left a commit and tags behind" \
    observed after-release '.packages.core.tags | has("1.1.0")'
  assert "registry serves core 1.1.0" observed after-publish '.packages.core.registry == "1.1.0"'
  assert "the release commit was pushed by the tool" observed after-publish '.marks.release.onOriginMain == true'
  assert "every released package is consistent once pushed by hand" \
    observed after-push '[.packages[] | select(.registry != "1.0.0")] | length > 0 and all(.state == "consistent")'
  assert "the next plan is the colleague's change and its dependents alone" \
    bash -c "[ \"\$(grep -E '^[a-z]+ .*New version [0-9.]+ written' '$OUT/step-next.log' | cut -d' ' -f1 | sort -u | tr '\n' ' ')\" = '$(expected_next)' ]"
}
