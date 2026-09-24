# Changesets receives explicit patch entries for core and its three direct
# consumers. This is the nearest equivalent to the requested release set of
# `fix(core)^`; Changesets does not interpret that commit syntax.
#
# The first run is Changesets' documented release with the fault armed before
# it: `changeset version`, a commit of what it wrote (the fixture's
# `commit: false` leaves that to the operator), and `changeset publish`. The
# build is where Changesets runs one: `changeset publish` publishes every
# package the registry does not hold yet through `npm publish`, which runs the
# package's `prepack` script. Nothing is published by hand and no package is
# attempted out of the order Changesets chooses.
#
# The catch-up is Changesets' documented recovery once the fault is gone: ask
# `changeset status`, run `changeset publish` again, which publishes whatever
# the registry is missing and tags it, and push, which Changesets never does.

run_experiment() {
  fixture changesets --propagation
  baseline_publish
  observe before

  arm_fault
  step release:version changeset version
  step manual:commit git commit -qam "chore: version packages"
  step release:publish changeset publish
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan changeset status --verbose
  step release:recovery changeset publish
  step manual:push git push --follow-tags origin main
  observe after-recovery
  step query:final-plan changeset status --verbose

  first_run_asserts publish after-failure
  catch_up_asserts 1.0.1
}
