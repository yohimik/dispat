# Lerna receives an ordinary `fix(core): correct reader`. What that selects is
# Lerna's own answer and not something this protocol arranges: no package is
# forced, and the version step's own output is what says which packages Lerna
# chose to release.
#
# The first run is Lerna's documented release, `lerna version` and then
# `lerna publish from-git`, with the fault armed before it. The build is where
# Lerna documents one: `lerna publish` packs every package it publishes, and
# packing runs the package's `prepack` script, which is the same build script
# dispat's build stage runs. Nothing is published by hand and no package is
# attempted out of the order Lerna chooses.
#
# The catch-up is Lerna's documented recovery once the fault is gone: ask
# `lerna changed`, then `lerna publish from-package`, which publishes whatever
# the registry is missing. A manual step is taken only when that recovery fails
# and the protocol can see why: a working tree the failed publish left dirty.
#
# The other scenarios, through Lerna's nearest mechanism:
#
#   build-distributed  Lerna has no worker of its own, so the build fault
#                      runs where Lerna runs every build
#   deselected         `lerna version` takes no package selection, so the
#                      first run is unselected
#   held               cli is marked private, which Lerna never publishes
#                      and `--no-private` never versions, then unmarked
#   deferred           the provider is refused while cli's own fix is
#                      pending, then the registry diff is published again
#   deferred-build     the same with cli's real build

# The packages a `lerna version` step selected, read from its own change list.
lerna_selected() {
  sed -n 's/^ - \([^:]*\):.*/\1/p' "$1" | sort | tr '\n' ' '
}

# The packages a `lerna publish` step reported published, read from its own
# success list.
lerna_published() {
  sed -n 's/^ - \([^@]*\)@.*/\1/p' "$1" | sort | tr '\n' ' '
}

# lerna_release <version step> <publish step> [<version flag>...]: Lerna's
# documented release, `lerna version` and then `lerna publish from-git`, with
# what each step chose read back out of its own output.
lerna_release() {
  local version=$1 publish=$2
  shift 2
  step "release:$version" lerna version --conventional-commits --yes "$@"
  echo "   lerna selected: $(lerna_selected "$OUT/step-$version.log")"
  step "release:$publish" lerna publish from-git --yes
  echo "   lerna published: $(lerna_published "$OUT/step-$publish.log")"
}

# lerna_publish_missing <step> <cleanup step> <retry step>: `lerna publish
# from-package`, which publishes whatever the registry is missing. When it
# refuses a working tree its own failed publish left rewritten, the manifests
# are checked out by hand and the same command runs again.
lerna_publish_missing() {
  local publish=$1 cleanup=$2 retry=$3
  step "release:$publish" lerna publish from-package --yes
  if [ "${STEP_RC[$publish]}" != 0 ] && [ -n "$(git status --porcelain)" ]; then
    echo "   the recovery refused a working tree its own failed publish left dirty: $(git status --porcelain | wc -l | tr -d ' ') paths"
    step "manual:$cleanup" git checkout -q -- .
    step "release:$retry" lerna publish from-package --yes
    echo "   lerna published: $(lerna_published "$OUT/step-$retry.log")"
  else
    echo "   lerna published: $(lerna_published "$OUT/step-$publish.log")"
  fi
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
  fixture lerna --propagation
  baseline_publish
  observe before
  if [ "$SCENARIO" = build-distributed ]; then
    echo "   lerna has no worker of its own; every build runs where lerna runs"
  fi

  arm_fault
  lerna_release version publish
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan lerna changed --all --long
  lerna_publish_missing recovery cleanup recovery2
  observe after-recovery
  step query:final-plan lerna changed --all --long

  first_run_asserts publish after-failure
  catch_up_asserts 1.0.1
}

# ---- deselected --------------------------------------------------------------

# The operator would release core, ui and api and leave cli out. `lerna
# version` has no package selection, so the first run is Lerna's release as it
# is, and the catch-up is the same release again.
run_deselected() {
  fixture lerna --propagation
  baseline_publish
  observe before

  echo "   lerna version takes no package selection; the release runs unselected"
  lerna_release version publish
  observe after-provider

  catch_up
  step query:retry-plan lerna changed --all --long
  lerna_release version2 publish2
  observe after-catch-up
  step query:final-plan lerna changed --all --long

  first_run_shipped after-provider
  assert "the selected run released the provider and the two selected consumers" \
    released_with publish after-provider '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  catch_up_asserts 1.0.1
}

# ---- held ----------------------------------------------------------------------

# cli is marked private before the provider's fix, and every version step
# passes `--no-private`, so the provider ships while cli is neither versioned
# nor published. The operator then removes the mark, and the catch-up is the
# same release again.
run_held() {
  fixture lerna --held --propagation
  baseline_publish
  observe before

  lerna_release version publish --no-private
  observe after-hold
  resume_release packages/cli/package.json 'del(.private)'

  catch_up
  step query:retry-plan lerna changed --all --long
  lerna_release version2 publish2 --no-private
  observe after-catch-up
  step query:final-plan lerna changed --all --long

  first_run_shipped after-hold
  assert "the first run exited 0" [ "${STEP_RC[version]}${STEP_RC[publish]}" = 00 ]
  catch_up_asserts 1.0.1
}

# ---- deferred, deferred-build ----------------------------------------------------

# cli has a fix of its own pending when the provider's upload is refused. The
# operator would then release the provider without cli; Lerna publishes no
# selection, so that run is `lerna publish from-package` as it is, and the
# catch-up is the same registry diff once more.
run_deferred() {
  fixture lerna --deferred
  baseline_publish
  observe before

  # shellcheck disable=SC2034 # read by common.sh, which checks the fault fired
  FAULT=provider
  if [ "$SCENARIO" = deferred-build ]; then
    deny core "$REPO/packages/cli/dist/index.js"
  else
    deny core
  fi
  lerna_release version publish
  observe after-provider-failure

  allow core
  echo "   lerna publish takes no package selection; the provider's run publishes whatever the registry is missing"
  lerna_publish_missing provider provider-cleanup provider2
  observe after-provider-success

  catch_up
  step query:retry-plan lerna changed --all --long
  lerna_publish_missing recovery cleanup recovery2
  observe after-catch-up
  step query:final-plan lerna changed --all --long

  provider_phase_asserts publish after-provider-failure after-provider-success
  if [ "$SCENARIO" = deferred-build ]; then
    catch_up_asserts 1.0.1
  else
    catch_up_asserts 1.0.2
  fi
}
