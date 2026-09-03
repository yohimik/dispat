# tools

This directory holds repository tooling as a Go module. dispat sweeps it like any other package, but never versions,
tags, or publishes it. You declare it in the root `dispat.yaml` with `versioning: none`, so commands like
`dispat run vet`, `tests`, and `build` reach it automatically while release plans leave it alone.

## testreport

This is the only tool in the module. It wraps workspace test and benchmark runs and aggregates their output for the
documentation site. That means every figure on the [coverage](https://dispat.dev/internals/coverage/),
[test results](https://dispat.dev/internals/test-results/), [benchmarks](https://dispat.dev/internals/benchmarks/) and
[release experiments](https://dispat.dev/internals/experiments/) pages comes directly from live runs.

Five verbs:

- `testreport test <log-name> -- <go test args...>` runs `go test -json`, stores the raw log under `coverage/testlog/`,
  prints a human-readable summary, and exits with the test run's own status. Every package's `tests` script in the
  workspace calls it, sending each suite's run to the site's test results page.
- `testreport bench <log-name> -- <go test args...>` runs `go test -bench -json`, stores the stream under
  `coverage/benchlog/`, and summarises what it measured. Benchmarks run in a pass of their own so that a measurement is
  never taken on a machine busy running tests, and so that a benchmark is never tallied as one.
- `testreport build [-coverage dir] [-out file] [-commit sha] [-keep file] [-modules file] [-experiments dir]` merges
  saved logs, coverage profiles, benchmark streams and the release experiments' records into
  `packages/docs/data/report.json`, which the documentation site reads at build time. `-keep` carries an earlier
  report's measurements forward for the modules this run did not measure; `-modules` writes the list of modules it did,
  which is what the docs build checks itself against; `-experiments` names the campaign's results folder, which
  defaults to `experiments` under the coverage folder.
- `testreport render <log>` summarises an existing log file so you can inspect a past run without running the tests
  again.
- `testreport experiments [-markdown] [dir]` summarises a release experiments campaign, one row per cell: the steps and
  their exit codes, how many expectations held, and the state the run ended in. `-markdown` renders the table a job
  summary shows. It reads the same records the report does, so a run's summary and the published page can never say
  different things about one campaign.

The root `dispat.yaml` wires these verbs into your workspace. Package-level `tests` scripts call `testreport test`, and
the release run's docs package measures the whole report in a `beforeBuild` hook — the `measured` target of
`Dockerfile.gotest`, which asks the test and benchmark stages for their output directly rather than reading a folder
somebody had to fill first.
