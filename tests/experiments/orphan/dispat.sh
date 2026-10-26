# The registry refuses one package mid-release. The command's failed reply
# does not prove that its upload is no longer in flight, so dispat retains the
# release lock. The next invocation must be fenced until an operator can prove
# the exact external outcome; this fixture never deletes the lock.

run_experiment() {
  fixture dispat --feature
  baseline_publish
  observe before

  deny cli
  step release:release1 dispat release --log-format json
  keep_publish_logs after-refusal
  echo "   codes reported: $(jq -r 'select(.code) | .code' "$OUT/step-release1.log" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
  observe after-refusal
  echo "   cli publish log: $(tail -n 2 /tmp/publish-cli.log 2>/dev/null | tr '\n' ' ')"
  local uploads_before_retry
  uploads_before_retry=$(proxy_uploads cli '[0-9]+')

  allow cli
  catch_up
  step release:release2 dispat release --log-format json
  observe after-fenced-retry

  assert "the refused run exited non-zero" [ "${STEP_RC[release1]}" != 0 ]
  assert "the upload was deliberately refused" [ "$(proxy_refused cli)" -gt 0 ]
  assert "the first run reports an unsettled external publication (E228)" \
    jq -se 'any(.[]; .code == "E228")' "$OUT/step-release1.log"
  assert "the refused package is neither tagged nor on the registry after the refusal" \
    observed after-refusal '.packages.cli.registry == "1.0.0" and (.packages.cli.tags | keys == ["1.0.0"])'
  assert "the packages that published are consistent after the refusal" \
    observed after-refusal '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  assert "the unresolved publication retains the remote release lock" \
    git --git-dir="$REPO-origin.git" show-ref --verify --quiet refs/tags/dispat-release-lock
  assert "a fresh release is fenced by that lock (E336)" \
    jq -se 'any(.[]; .code == "E336")' "$OUT/step-release2.log"
  assert "the fenced retry made no second cli upload" \
    [ "$(proxy_uploads cli '[0-9]+')" = "$uploads_before_retry" ]
  assert "the fenced retry advanced no tags" \
    jq -se '.[0].origin.tags == .[1].origin.tags' \
      "$OUT/observe-after-refusal.json" "$OUT/observe-after-fenced-retry.json"
  assert "no publication receipt is left on origin after the refused run" \
    observed after-refusal '.origin.receipts == []'
}
