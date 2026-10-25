# The timing experiment under lerna: no fault, the scenario's pending minor to
# core, and lerna's own plan and release, each timed.
#
# lerna has no dry run, so the plan is `lerna version` with nothing committed,
# tagged or pushed, run in a scratch clone of the fixture taken just before it:
# the versions it writes there are its plan, and the clone under test is never
# touched. The release is lerna's documented two commands, `lerna version`
# and `lerna publish from-git`.
#
# lerna publishes in-process through libnpmpublish rather than through the npm
# CLI, so the npm shim sees none of its publications. Every tool's publication
# is timed at the registry's proxy, so that is no difference here.
#
# The relation is `build`: `lerna publish` packs every package, which runs its
# `prepack` build, in dependency order before it publishes any of them. The
# records are held to it, edge by edge.

lerna_plan() {
  (cd "$REPO-plan" && lerna version --conventional-commits --yes --no-git-tag-version --no-push)
}

run_experiment() {
  timing_fixture lerna
  baseline_publish
  observe before

  step manual:plan-clone git clone -q "$REPO" "$REPO-plan"
  step query:plan lerna_plan
  record_plan plan
  step release:run-version with_npm_shim lerna version --conventional-commits --yes
  step release:run-publish with_npm_shim lerna publish from-git --yes
  observe after-release

  measure_timing build after-release
  timing_asserts after-release
}
