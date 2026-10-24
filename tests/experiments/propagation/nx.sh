# nx receives an ordinary `fix(core): correct reader`, and what that selects is
# nx's own answer: no package is forced, and the version step's own output is
# what says which packages nx chose to release.
#
# The first run is nx's documented release with the fault armed before it:
# `nx release --yes`, which versions, commits, tags, pushes and publishes. The
# build is where nx documents one, `release.version.preVersionCommand`, set in
# the fixture to `npx nx run-many -t build`, which runs every package's build
# script before anything is versioned. Nothing is published by hand and no
# package is attempted out of the order nx chooses.
#
# The catch-up is nx's documented recovery once the fault is gone: ask
# `nx release --dry-run`, then publish what an earlier run versioned with
# `nx release publish` when a version it tagged is missing from the registry,
# or release again with `nx release --yes` when nothing is. nx 23.1.2 pushes
# before it commits in this fixture, so a clone its release left ahead of the
# origin is pushed by hand.
#
# The other scenarios, through nx's nearest mechanism:
#
#   build-distributed  nx distributes tasks only through Nx Cloud, which this
#                      run disables, so the build fault runs where nx runs
#                      every build
#   deselected         the first run is `nx release --projects core,ui,api`
#   held               `release.projects` excludes cli, then includes it
#   deferred           the provider is refused while cli's own fix is
#                      pending, then released with `--projects core,ui,api`
#   deferred-build     the same with cli's real build

# nx_finish <step> <push step> [<flag>...]: publish what an earlier run
# versioned when a version it tagged is missing from the registry, or release
# again when none is, then push by hand what the release left on the clone
# alone. The tagged version the registry lacks is what an operator reads off
# the registry and the tags; the latest observation holds both.
nx_finish() {
  local release=$1 push=$2
  shift 2
  if tail -n 1 "$OUT/observations.jsonl" | jq -e 'any(.packages[]; .state == "orphan")' > /dev/null; then
    step "release:$release" nx release publish "$@"
  else
    step "release:$release" nx release --yes "$@"
  fi
  nx_push_if_ahead "$push"
}

# nx_push_if_ahead <step>: the push by hand of a release commit nx left on the
# clone alone.
nx_push_if_ahead() {
  if [ "$(git rev-list --count origin/main..HEAD)" -gt 0 ]; then
    echo "   the release left $(git rev-list --count origin/main..HEAD) commit(s) on this clone alone"
    step "manual:$1" git push --follow-tags origin main
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
  fixture nx --propagation
  baseline_publish
  observe before
  if [ "$SCENARIO" = build-distributed ]; then
    echo "   nx distributes tasks only through Nx Cloud, which this run disables; every build runs where nx runs"
  fi

  arm_fault
  step release:release1 nx release --yes
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan nx release --dry-run
  nx_finish recovery push
  observe after-recovery
  step query:final-plan nx release --dry-run

  first_run_asserts release1 after-failure
  catch_up_asserts 1.0.1
}

# ---- deselected --------------------------------------------------------------

# The first run selects core, ui and api; whether nx adds cli back as a
# dependent of core is nx's answer, read from the record.
run_deselected() {
  fixture nx --propagation
  baseline_publish
  observe before

  step release:release1 nx release --projects core,ui,api --yes
  echo "   nx versioned: $(nx_listed "$OUT/step-release1.log" | tr '\n' ' ')"
  nx_push_if_ahead push1
  observe after-provider

  catch_up
  step query:retry-plan nx release --dry-run
  nx_finish release2 push
  observe after-catch-up
  step query:final-plan nx release --dry-run

  first_run_shipped after-provider
  assert "the selected run released the provider and the two selected consumers" \
    released_with release1 after-provider '[.packages.core, .packages.ui, .packages.api] | all(.state == "consistent")'
  catch_up_asserts 1.0.1
}

# ---- held ----------------------------------------------------------------------

# `release.projects` excludes cli before the provider's fix, so the provider
# ships while cli is outside every release. The operator then restores the
# pattern, and the catch-up is the same release again.
run_held() {
  fixture nx --held --propagation
  baseline_publish
  observe before

  step release:release1 nx release --yes
  echo "   nx versioned: $(nx_listed "$OUT/step-release1.log" | tr '\n' ' ')"
  nx_push_if_ahead push1
  observe after-hold
  resume_release nx.json '.release.projects = ["*"]'

  catch_up
  step query:retry-plan nx release --dry-run
  nx_finish release2 push
  observe after-catch-up
  step query:final-plan nx release --dry-run

  first_run_shipped after-hold
  assert "the first run exited 0" [ "${STEP_RC[release1]}" = 0 ]
  catch_up_asserts 1.0.1
}

# ---- deferred, deferred-build ----------------------------------------------------

# cli has a fix of its own pending when the provider's upload is refused. The
# provider's run selects core, ui and api, finishing what the first run
# versioned where it versioned anything, and the catch-up is nx's recovery
# with no selection.
run_deferred() {
  fixture nx --deferred
  baseline_publish
  observe before

  # shellcheck disable=SC2034 # read by common.sh, which checks the fault fired
  FAULT=provider
  if [ "$SCENARIO" = deferred-build ]; then
    deny core "$REPO/packages/cli/dist/index.js"
  else
    deny core
  fi
  step release:release1 nx release --yes
  observe after-provider-failure

  allow core
  nx_finish provider provider-push --projects core,ui,api
  observe after-provider-success

  catch_up
  step query:retry-plan nx release --dry-run
  nx_finish recovery push
  observe after-catch-up
  step query:final-plan nx release --dry-run

  provider_phase_asserts release1 after-provider-failure after-provider-success
  if [ "$SCENARIO" = deferred-build ]; then
    catch_up_asserts 1.0.1
  else
    catch_up_asserts 1.0.2
  fi
}
