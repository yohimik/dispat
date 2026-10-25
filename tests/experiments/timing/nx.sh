# The timing experiment under nx: no fault, the scenario's pending minor to
# core, and nx's own plan and release, each timed.
#
# The plan is `nx release --dry-run`. The release is `nx release --yes`, which
# versions, commits, tags and publishes, and the push by hand of the release
# commit nx 23.1.2 leaves on the clone alone, which belongs to the release.
#
# The relation is `none`: the pre-version command is `nx run-many -t build`,
# and the fixture's nx.json declares no `dependsOn` for the build target, so
# nx orders no package's build after its providers'. Every build still runs
# before nx versions or publishes anything. The records are held to it, edge
# by edge.

run_experiment() {
  timing_fixture nx
  baseline_publish
  observe before

  step query:plan nx release --dry-run
  record_plan plan
  step release:run with_npm_shim nx release --yes
  if [ "$(git rev-list --count origin/main..HEAD)" -gt 0 ]; then
    step manual:run-push git push --follow-tags origin main
  fi
  observe after-release

  measure_timing none after-release
  timing_asserts after-release
}
