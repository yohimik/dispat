# Release scripts

The two shell scripts behind this repository's release stages and CI checks that carry enough logic to deserve a file
of their own. Everything smaller lives directly in the dispat configuration as a script entry (`push-badge`
in the root [`dispat.yaml`](../dispat.yaml), `deploy-docs` in
[`packages/docs/dispat.yaml`](../packages/docs/dispat.yaml), the link bracket in
[`services/dispat/dispat.yaml`](../services/dispat/dispat.yaml), `push-readme` in
[`docker/dispat.yaml`](../docker/dispat.yaml)), and the test-run record lives in the Go tooling
(`testreport test` in [`tools/testreport`](../tools/testreport)). Since the config split, each script entry sits
beside the code it configures: the root file holds what every space shares, each space carries a `<space>/dispat.yaml`,
and the two packages with exceptions of their own carry a package file. Package scripts run **inside the releasing
package's folder**; root scripts run at the repository root. Everything they need arrives in the environment: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/reference/environment/) a stage is given, plus whatever CI
exports.

| Script                                     | Called from                                            | Reads                                   | Produces |
|--------------------------------------------|--------------------------------------------------------|-----------------------------------------|----------|
| [`coverage-badge.sh`](./coverage-badge.sh) | `coverage-badge` in the root `dispat.yaml`             | `coverage/*.out`, `GITHUB_STEP_SUMMARY` | The merged coverage profiles and the badge JSON in `coverage/`. |
| [`check-action.sh`](./check-action.sh)     | the Action workflow and the release's post-release job | its arguments                           | Assertions that the composite action installed what it promised. |

There is no build script here, and no docs-version script either, on purpose. The builds happen inside Docker: the
CLI's six release binaries come out of [`services/dispat/Dockerfile`](../services/dispat/Dockerfile) (see the `build`
script in `services/dispat/dispat.yaml`, which brackets the build with `dispat autowriter --link-local` /
`--unlink-local` so the binaries carry this checkout rather than the `pkg/*` versions `go.mod` pins), the site and its
per-minor version snapshot come out of the docs package's Dockerfile (the `DOCS_VERSION` build arg decides whether a
snapshot is cut, and an empty one, which is what a prerelease gets, cuts nothing), and the images are
[`docker compose` builds](../docker/README.md) driven from `docker/dispat.yaml`. Nothing is left for a host
shell to decide.

Do not run `go work sync` or `go mod tidy` while the link bracket is in place: both delete the `go.sum` entries a
local redirect makes redundant, and unlinking needs them back. That is what `--sync-lock=false` is for, and the `lint`
job in [tests.yml](../.github/workflows/tests.yml) checks for both leaks on every commit.

## The test and report tooling

Every Go `tests` script invokes `go test` through [`tools/testreport`](../tools/testreport):

```sh
go run github.com/yohimik/dispat/tools/testreport test <log-name> -- <go test args...>
```

It runs the tests with `-json`, keeps the stream as `coverage/testlog/<log-name>.json`, and prints a human summary in
its place, with the full output of anything that failed, so nothing is lost to the machine format. The exit status is
the test run's own. The log name is the report's id for that invocation and is chosen to match the coverage profile
the same invocation writes (`ccme`, `dispat`, `integration`); a name ending in `-race` marks the race-detector pass.

[`coverage-badge.sh`](./coverage-badge.sh) merges the profiles that run leaves in `coverage/` and writes the badge
JSON, and the `test-report` root script (`testreport build`) turns the same profiles and logs into
`packages/docs/data/report.json`, which is where the documentation site's
[coverage](https://yohimik.github.io/dispat/internals/coverage/) and
[test results](https://yohimik.github.io/dispat/internals/test-results/) pages get their numbers. Both run through
`dispat exec` from the [Release workflow](../.github/workflows/release.yml) and nowhere else: only a `--since all` run
produces a complete profile set, so a total from a windowed run would be a number about whichever packages happened to
change.

To reproduce the whole thing locally:

```sh
dispat run tests --since all      # ~6 min: every module's profile and log
dispat exec coverage-badge        # the merged profiles and the badge JSON
dispat exec test-report           # packages/docs/data/report.json
```

## Running one by hand

`dispat run <script> --since all --package <name>` runs a package script once inside that package, with the exact
environment its stage would hand it, releasing nothing. `--since all` is what reaches a package that has not changed;
drop it to run the script only when the package is in the release window. `dispat exec <script>` runs a root script at
the repository root.

```sh
dispat run build --since all -p dispat     # the containerised cross-compile into services/dispat/dist
dispat run build --since all -p docs       # the containerised site build
dispat run build --since all -s docker     # docker compose build, all four images, pushing nothing
```

The two pushing scripts, `deploy-docs` and `push-badge`, are the exception: **do not run them**, by hand or through
dispat. Each publishes to the live repository (the site, the badge), and the release run is the only context in which
that is a record rather than an accident, so both refuse unless `CI=true`.

The scripts are POSIX `sh` with `set -eu`, and the release-path ones are the release path itself: a change there is
only really exercised by the [Release workflow](../.github/workflows/release.yml).
