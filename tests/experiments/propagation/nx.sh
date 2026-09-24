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
# `nx release --dry-run`, then publish what the first run versioned with
# `nx release publish` when core@1.0.1 is tagged, or release again with
# `nx release --yes` when the first run versioned nothing. nx 23.1.2 pushes
# before it commits in this fixture, so a clone its release left ahead of the
# origin is pushed by hand.

run_experiment() {
  fixture nx --propagation
  baseline_publish
  observe before

  arm_fault
  step release:release1 nx release --yes
  observe after-failure

  clear_fault
  catch_up
  step query:retry-plan nx release --dry-run
  if [ -n "$(git tag -l core@1.0.1)" ]; then
    step release:recovery nx release publish
  else
    step release:recovery nx release --yes
  fi
  if [ "$(git rev-list --count origin/main..HEAD)" -gt 0 ]; then
    echo "   the release left $(git rev-list --count origin/main..HEAD) commit(s) on this clone alone"
    step manual:push git push --follow-tags origin main
  fi
  observe after-recovery
  step query:final-plan nx release --dry-run

  first_run_asserts release1 after-failure
  catch_up_asserts 1.0.1
}
