# tools

Repository tooling: a Go module dispat sweeps like any other package but never versions, tags or publishes. It is
declared in the root `dispat.yaml` with `versioning: none`, so `dispat run vet`, `tests` and `build` reach it without a
hand-kept module list, while the release plan leaves it alone.

## testreport

The one tool here. It wraps the workspace's test runs and turns their combined output into the numbers the
documentation site publishes, so no figure on the [coverage](https://yohimik.github.io/dispat/internals/coverage/) or
[test results](https://yohimik.github.io/dispat/internals/test-results/) pages is ever typed by hand.

Three verbs:

- `testreport test <log-name> -- <go test args...>` runs `go test -json`, keeps the raw log under `coverage/testlog/`,
  prints a human summary, and exits with the test run's own status. Every package's `tests` script in the workspace
  goes through it, which is how each suite's run lands in the site's test-results page.
- `testreport build [-coverage dir] [-out file] [-commit sha]` merges the kept logs and coverage profiles into
  `packages/docs/data/report.json`, the file the documentation site reads at build time.
- `testreport render <log>` summarises one kept log again, for reading a past run without re-running it.

The root `dispat.yaml` wires the verbs in: the per-package `tests` scripts call `testreport test`, and the release
workflow's `test-report` script calls `testreport build` after a full `--since all` sweep, which is the one run whose
numbers describe the whole workspace.
