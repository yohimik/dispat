# tools

This directory holds repository tooling as a Go module. dispat sweeps it like any other package, but never versions,
tags, or publishes it. You declare it in the root `dispat.yaml` with `versioning: none`, so commands like
`dispat run vet`, `tests`, and `build` reach it automatically while release plans leave it alone.

## testreport

This is the only tool in the module. It wraps workspace test runs and aggregates their output for the documentation
site. That means every figure on the [coverage](https://dispat.dev/internals/coverage/) and
[test results](https://dispat.dev/internals/test-results/) pages comes directly from live runs.

Three verbs:

- `testreport test <log-name> -- <go test args...>` runs `go test -json`, stores the raw log under `coverage/testlog/`,
  prints a human-readable summary, and exits with the test run's own status. Every package's `tests` script in the
  workspace calls it, sending each suite's run to the site's test results page.
- `testreport build [-coverage dir] [-out file] [-commit sha]` merges saved logs and coverage profiles into
  `packages/docs/data/report.json`, which the documentation site reads at build time.
- `testreport render <log>` summarises an existing log file so you can inspect a past run without running the tests
  again.

The root `dispat.yaml` wires these verbs into your workspace. Package-level `tests` scripts call `testreport test`,
while the release workflow's `test-report` script calls `testreport build` after a full `--since all` sweep. That sweep
generates the complete dataset for the entire workspace.
