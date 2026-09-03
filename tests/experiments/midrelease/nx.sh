# A colleague pushes to origin/main while nx is releasing. Under the git
# settings nx documents for a release run (release.git with commit, tag and
# push), nx stages the version bumps and pushes before it commits or tags;
# the colleague's push lands right before that push. What follows is the
# recovery an operator is left with: drop the staged bumps, rebase, release
# again, publish, push what nx left local, then ask nx what it would
# version next.

# The packages a correct next plan holds once the colleague's commit is on
# main and this run's release is recorded. nx's cascade reaches every
# dependent of a changed package, transitively, so the conflict scenario's
# patch of core reaches all six; the clean scenario's patch of api reaches
# api alone, which has no dependents. Sorted, space-ended, the way the
# transcript renders a plan's package list.
next_plan() {
  case "$SCENARIO" in
    conflict) echo 'api cli core docs theme ui ' ;;
    *) echo 'api ' ;;
  esac
}

run_experiment() {
  fixture nx --feature --colleague
  baseline_publish
  observe before

  step release with_shim nx release --skip-publish
  COLLEAGUE=$(colleague_sha)
  observe_marked after-release
  echo "   staged, uncommitted: $(git diff --cached --name-only | tr '\n' ' ')"

  # Recovery by hand. The rebase refuses a dirty index, and the staged bumps
  # hold nothing nx does not recompute from git history, so they are
  # dropped and nx runs again on the joined branch.
  step recover bash -c 'git reset -q --hard && git pull --rebase origin main'
  step release2 nx release --skip-publish
  RELEASE=$(release_sha core@1.1.0)
  observe_marked after-release2

  step publish nx release publish --registry "$REGISTRY"
  observe_marked after-publish

  step push git push --follow-tags origin main
  observe_marked after-push

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
    bash -c '[ "$(grep -E "^[a-z]+ .*New version [0-9.]+ written" "$1" | cut -d" " -f1 | sort -u | tr "\n" " ")" = "$2" ]' \
    _ "$OUT/step-next.log" "$(next_plan)"
}
