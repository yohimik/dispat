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
#
# The other scenarios, through Changesets' nearest mechanism:
#
#   build-distributed  Changesets has no worker, so the build fault runs
#                      where Changesets runs every build
#   deselected         the first run is `changeset version --ignore cli`,
#                      with cli's entry in a changeset of its own
#   held               the configuration's `ignore` names cli, then nothing
#   deferred           the provider is refused while cli's own fix is
#                      pending, then the registry diff is published again
#   deferred-build     the same with cli's real build

# changesets_release <suffix> [<version flag>...]: Changesets' documented
# release run to the end: `changeset version`, the commit and push it leaves to
# the operator, and `changeset publish` between them. The suffix keeps the
# step names of a second release apart from the first's.
changesets_release() {
  local suffix=$1
  shift
  step "release:version$suffix" changeset version "$@"
  step "manual:commit$suffix" git commit -qam "chore: version packages"
  step "release:publish$suffix" changeset publish
  step "manual:push$suffix" git push --follow-tags origin main
}

run_experiment() {
  case "$SCENARIO" in
    deferred|deferred-build) run_deferred ;;
    deselected) run_deselected ;;
    held) run_held ;;
    *) run_fault ;;
  esac
}

# ---- build, publish, build-distributed ---------------------------------------

run_fault() {
  fixture changesets --propagation
  baseline_publish
  observe before
  if [ "$SCENARIO" = build-distributed ]; then
    echo "   changesets has no worker; every build runs where changesets runs"
  fi

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

# ---- deselected --------------------------------------------------------------

# The first run ignores cli, whose entry stays pending in its own changeset,
# and the catch-up is the same release with nothing ignored.
run_deselected() {
  fixture changesets --propagation
  baseline_publish
  observe before

  changesets_release "" --ignore cli
  observe after-provider

  catch_up
  step query:retry-plan changeset status --verbose
  changesets_release 2
  observe after-catch-up
  step query:final-plan changeset status --verbose

  first_run_shipped after-provider
  assert "the selected run released the provider and the two selected consumers" \
    released_with publish after-provider '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  catch_up_asserts 1.0.1
}

# ---- held ----------------------------------------------------------------------

# The configuration ignores cli before the provider's fix, so the provider
# ships while cli's changeset waits. The operator then empties the ignore
# list, and the catch-up is the same release again.
run_held() {
  fixture changesets --held --propagation
  baseline_publish
  observe before

  changesets_release ""
  observe after-hold
  resume_release .changeset/config.json '.ignore = []'

  catch_up
  step query:retry-plan changeset status --verbose
  changesets_release 2
  observe after-catch-up
  step query:final-plan changeset status --verbose

  first_run_shipped after-hold
  assert "the first run exited 0" [ "${STEP_RC[version]}${STEP_RC[publish]}" = 00 ]
  catch_up_asserts 1.0.1
}

# ---- deferred, deferred-build ----------------------------------------------------

# cli has a fix of its own pending when the provider's upload is refused. The
# operator would then release the provider without cli; `changeset publish`
# takes no selection, so that run publishes whatever the registry is missing,
# and the catch-up is the same registry diff once more.
run_deferred() {
  fixture changesets --deferred
  baseline_publish
  observe before

  # shellcheck disable=SC2034 # read by common.sh, which checks the fault fired
  FAULT=provider
  if [ "$SCENARIO" = deferred-build ]; then
    deny core "$REPO/packages/cli/dist/index.js"
  else
    deny core
  fi
  step release:version changeset version
  step manual:commit git commit -qam "chore: version packages"
  step release:publish changeset publish
  observe after-provider-failure

  allow core
  echo "   changeset publish takes no package selection; the provider's run publishes whatever the registry is missing"
  step release:provider changeset publish
  step manual:provider-push git push --follow-tags origin main
  observe after-provider-success

  catch_up
  step query:retry-plan changeset status --verbose
  step release:recovery changeset publish
  step manual:push git push --follow-tags origin main
  observe after-catch-up
  step query:final-plan changeset status --verbose

  provider_phase_asserts publish after-provider-failure after-provider-success
  if [ "$SCENARIO" = deferred-build ]; then
    catch_up_asserts 1.0.1
  else
    catch_up_asserts 1.0.2
  fi
}
