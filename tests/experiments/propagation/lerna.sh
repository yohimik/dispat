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

# The packages a `lerna version` step selected, read from its own change list.
lerna_selected() {
  sed -n 's/^ - \([^:]*\):.*/\1/p' "$1" | sort | tr '\n' ' '
}

# The packages a `lerna publish` step reported published, read from its own
# success list.
lerna_published() {
  sed -n 's/^ - \([^@]*\)@.*/\1/p' "$1" | sort | tr '\n' ' '
}

run_experiment() {
  fixture lerna --propagation
  baseline_publish
  observe before

  arm_fault
  step release:version lerna version --conventional-commits --yes
  echo "   lerna selected: $(lerna_selected "$OUT/step-version.log")"
  step release:publish lerna publish from-git --yes
  echo "   lerna published: $(lerna_published "$OUT/step-publish.log")"
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan lerna changed --all --long
  step release:recovery lerna publish from-package --yes
  if [ "${STEP_RC[recovery]}" != 0 ] && [ -n "$(git status --porcelain)" ]; then
    echo "   the recovery refused a working tree its own failed publish left dirty: $(git status --porcelain | wc -l | tr -d ' ') paths"
    step manual:cleanup git checkout -q -- .
    step release:recovery2 lerna publish from-package --yes
    echo "   lerna published: $(lerna_published "$OUT/step-recovery2.log")"
  else
    echo "   lerna published: $(lerna_published "$OUT/step-recovery.log")"
  fi
  observe after-recovery
  step query:final-plan lerna changed --all --long

  first_run_asserts publish after-failure
  catch_up_asserts 1.0.1
}
