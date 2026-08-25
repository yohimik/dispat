# Release scripts

You will find two standalone shell scripts here for release stages and CI checks with complex logic. Smaller scripts
live directly in dispat configuration files as script entries, such as `push-badge` in the root
[`dispat.yaml`](../dispat.yaml), `deploy-docs` in [`packages/docs/dispat.yaml`](../packages/docs/dispat.yaml), the link
bracket in [`services/dispat/dispat.yaml`](../services/dispat/dispat.yaml), and `push-readme` in
[`docker/dispat.yaml`](../docker/dispat.yaml), while test records live in [`tools/testreport`](../tools/testreport) as
`testreport test`. Each script entry sits beside the code it configures, so the root file holds shared space settings,
each space carries a `<space>/dispat.yaml`, and two exception packages carry their own package files.

Package scripts run **inside the releasing package's folder**, while root scripts run at the repository root. Scripts
receive everything they need through environment variables: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/reference/environment/) from their stage, plus variables
exported by CI.

| Script                                     | Called from                                            | Reads                                   | Produces |
|--------------------------------------------|--------------------------------------------------------|-----------------------------------------|----------|
| [`coverage-badge.sh`](./coverage-badge.sh) | `coverage-badge` in the root `dispat.yaml`             | `coverage/*.out`, `GITHUB_STEP_SUMMARY` | The merged coverage profiles and the badge JSON in `coverage/`. |
| [`check-action.sh`](./check-action.sh)     | the Action workflow and the release's post-release job | its arguments                           | Assertions that the composite action installed what it promised. |

This repository omits build and docs-version scripts on purpose because builds happen inside Docker. The CLI produces
six release binaries from [`services/dispat/Dockerfile`](../services/dispat/Dockerfile), where the `build` script in
`services/dispat/dispat.yaml` brackets the build with `dispat autowriter --link-local` and `--unlink-local` so binaries
carry the current checkout instead of the `pkg/*` versions pinned in `go.mod`. The documentation site and its snapshots
build from the docs package Dockerfile using the `DOCS_VERSION` build argument, and the remaining images come from
[`docker compose` builds](../docker/README.md) driven by `docker/dispat.yaml`.

Do not run `go work sync` or `go mod tidy` while the link bracket is in place. Both commands delete `go.sum` entries
that local redirects make redundant, but unlinking requires those entries back. Use `--sync-lock=false` to protect
them, and rely on the `lint` job in [tests.yml](../.github/workflows/tests.yml) to catch leaks on every commit.

## The test and report tooling

Invoke `go test` through [`tools/testreport`](../tools/testreport) for every Go `tests` script:

```sh
go run github.com/yohimik/dispat/tools/testreport test <log-name> -- <go test args...>
```

This runs your tests with `-json`, saves the raw output stream to `coverage/testlog/<log-name>.json`, and prints a
summary to your terminal. It displays full failure output so you do not lose details to the JSON format, and returns
the exit status of the underlying test run. Pass a `<log-name>` that matches the target coverage profile (`ccme`,
`dispat`, `integration`), and append `-race` to mark race-detector passes.

Run [`coverage-badge.sh`](./coverage-badge.sh) to merge the generated profiles in `coverage/` and produce the badge
JSON. The `test-report` root script (`testreport build`) then compiles those profiles and logs into
`packages/docs/data/report.json`, which feeds the site [coverage](https://yohimik.github.io/dispat/internals/coverage/)
and [test results](https://yohimik.github.io/dispat/internals/test-results/) pages. The
[Release workflow](../.github/workflows/release.yml) runs both through `dispat exec` because only a `--since all` run
generates a complete profile set across all packages.

Run these commands to reproduce the entire test and report pipeline locally:

```sh
dispat run tests --since all      # ~6 min: every module's profile and log
dispat exec coverage-badge        # the merged profiles and the badge JSON
dispat exec test-report           # packages/docs/data/report.json
```

## Running one by hand

Run `dispat run <script> --since all --package <name>` to execute a package script in its folder with the full stage
environment, without creating a release. Add `--since all` to target packages that have no changes, or omit it to run
only on packages within the current release window. Run `dispat exec <script>` to execute a root script from the
repository root.

```sh
dispat run build --since all -p dispat     # the containerised cross-compile into services/dispat/dist
dispat run build --since all -p docs       # the containerised site build
dispat run build --since all -s docker     # docker compose build, all four images, pushing nothing
```

The publish scripts `deploy-docs` and `push-badge` are dangerous exceptions: **do not run them** by hand or through
dispat. They publish directly to the live site and badge records, so both scripts abort unless `CI=true`.

All scripts use POSIX `sh` with `set -eu`. Test changes to release scripts carefully, because only the
[Release workflow](../.github/workflows/release.yml) exercises the full release path.
