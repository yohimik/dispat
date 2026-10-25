# The timing experiment under changesets: no fault, the scenario's pending
# minor to core, and changesets' own plan and release, each timed.
#
# The plan is `changeset status --verbose`. The release is changesets'
# documented one: `changeset version`, the commit it leaves to the operator,
# `changeset publish`, and the push changesets never makes.
#
# The relation is `build`: `changeset publish` runs `npm publish` for every
# package the registry lacks, and npm runs the package's `prepack` build right
# before its upload, waiting for no provider.

run_experiment() {
  timing_fixture changesets
  baseline_publish
  observe before

  step query:plan changeset status --verbose
  record_plan plan
  step release:run-version with_npm_shim changeset version
  step manual:run-commit git commit -qam "chore: version packages"
  step release:run-publish with_npm_shim changeset publish
  step manual:run-push git push --follow-tags origin main
  observe after-release

  measure_timing build after-release
  timing_asserts after-release
}
