# The timing experiment under dispat: no fault, the scenario's pending minor
# to core, and dispat's own plan and release, each timed.
#
# The plan is `dispat status`, which plans without releasing. The release is
# `dispat release`, one command for the whole plan, with the npm shim on PATH
# for the publish script the fixture configures.
#
# The relation is `build`: the fixture leaves isBuildWaitingPublish at its
# default, false, which lets a consumer build as soon as its providers have
# built, while a consumer still publishes after its providers (§19.2). The
# records are held to it, edge by edge.

run_experiment() {
  timing_fixture dispat
  baseline_publish
  observe before

  step query:plan dispat status --log-format json
  record_plan plan
  step release:run with_npm_shim dispat release --log-format json
  observe after-release

  measure_timing build after-release
  timing_asserts after-release
}
