# Release scripts

The shell scripts dispat runs as this repository's own release stages. They are referenced by name from `scripts` in
[`dispat.yaml`](../dispat.yaml) and run **inside the releasing package's folder**, which is why each one reaches the
repository root as `../../`. Everything they need arrives in the environment: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/reference/environment) a stage is given, plus whatever CI exports.

| Script                                       | Package  | `flow` slot | Reads                                                        | Produces                                                                                     |
|----------------------------------------------|----------|-------------|--------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| [`build-dispat.sh`](./build-dispat.sh)       | `dispat` | `build`     | `DISPAT_NEW_VERSION`, `DISPAT_OUTPUT`                        | One cross-compiled binary per platform in `services/dispat/dist`, exported as GitHub release assets through `DISPAT_EXPORT_GITHUB`. Brackets the build with `dispat autowriter --link-local` / `--unlink-local`, so the binaries carry this checkout rather than the `pkg/*` versions `go.mod` pins. |
| [`cut-docs-version.sh`](./cut-docs-version.sh) | `docs`   | `version`   | `DISPAT_IS_PRERELEASE`, `DISPAT_VERSION`, `DISPAT_NEW_VERSION` | A `packages/docs/versioned_docs/version-<minor>` snapshot, once per stable minor. Nothing on a prerelease. |
| [`deploy-docs.sh`](./deploy-docs.sh)         | `docs`   | `publish`   | `CI`, `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `DISPAT_NEW_VERSION` | A force-pushed orphan `gh-pages` branch carrying `packages/docs/build`.                       |

The `docker` space has no scripts here at all. Its stages are
[`docker compose build` and `docker compose push`](../docker/README.md) with a `docker login` one-liner, written
straight into [`dispat.yaml`](../dispat.yaml), because that is all they are: the image's version lives in its
`docker-compose.yml`, which dispat reads and rewrites as a manifest, and its moving tags live in a per-channel file the
stage selects with `-f $DISPAT_CHANNEL.yml`. Nothing is left for a shell to decide.

Each script's own header comment carries the reasoning behind it: why the binaries are built with `GOWORK=off`, why a
docs snapshot is cut per minor rather than per patch, and why the deploy pushes a branch instead of using
`actions/deploy-pages`.

`build-dispat.sh` is the one with a bracket around it. The versions `services/dispat/go.mod` pins are only as fresh as
the last release that bumped them, so a `pkg/*` module changed without a version bump would ship as its published copy
while every test in CI ran the working tree. `dispat autowriter --link-local` points each one at its folder for the
duration of the build and `--unlink-local` takes the redirects back out, before `beforePublish` runs
`dispat commit --tag --push`, which stages `services/dispat` and would otherwise publish a `go.mod` no consumer can
resolve. A `trap` covers the failure and interrupt paths, and the script re-checks both `go.mod` and `go.sum` at the end
rather than trusting either. `GOWORK=off` stays: only the intra-repo modules are redirected, so the build still proves
this module's own `go.mod` and `go.sum` cover its third-party requirements.

Do not run `go work sync` or `go mod tidy` while the links are in place: both delete the `go.sum` entries a local
redirect makes redundant, and unlinking needs them back. That is what `--sync-lock=false` is for, and the `lint` job in
[tests.yml](../.github/workflows/tests.yml) checks for both leaks on every commit.

## Not release stages: the test scripts

Two scripts here belong to the test run rather than to a `flow` slot, and neither takes a `DISPAT_*` variable.

[`go-test.sh`](./go-test.sh) is how every Go `tests` script in [`dispat.yaml`](../dispat.yaml) invokes `go test`:

```sh
sh scripts/go-test.sh <log-name> -- <go test args...>
```

It runs the tests with `-json`, keeps the stream as `coverage/testlog/<log-name>.json`, and prints a human summary in
its place — with the full output of anything that failed, so nothing is lost to the machine format. The exit status is
the test run's own. The log name is the report's id for that invocation and is chosen to match the coverage profile the
same script writes (`ccme`, `dispat`, `integration`); a name ending in `-race` marks the race-detector pass.

[`coverage-badge.sh`](./coverage-badge.sh) merges the profiles that run leaves in `coverage/` and writes the README
badge, and `go run github.com/yohimik/dispat/tools/testreport build` turns the same profiles and logs into
`packages/docs/data/report.json`, which is where the documentation site's
[coverage](https://yohimik.github.io/dispat/internals/coverage) and
[test results](https://yohimik.github.io/dispat/internals/test-results) pages get their numbers. Both are called from
the [Release workflow](../.github/workflows/release.yml) and nowhere else: only a `--since all` run produces a complete
set, so a total from a windowed run would be a number about whichever packages happened to change.

To reproduce the whole thing locally:

```sh
dispat run tests --since all                  # ~6 min: every module's profile and log
sh scripts/coverage-badge.sh                  # the merged profiles and the badge JSON
go run github.com/yohimik/dispat/tools/testreport build   # packages/docs/data/report.json
```

## Running one by hand

`dispat run <script> --since all --package <name>` runs a script once inside that package, with the exact environment
its stage would hand it, releasing nothing. `--since all` is what reaches a package that has not changed; drop it to
run the script only when the package is in the release window.

```sh
dispat run build-dispat --since all -p dispat        # cross-compiles into services/dispat/dist
dispat run build-docs --since all -p docs            # pnpm install + docusaurus build
dispat run cut-docs-version --since all -p docs      # writes a versioned_docs snapshot if the version warrants one
dispat run build-image --since all -s docker         # docker compose build, all four images, pushing nothing
```

`deploy-docs.sh` is the exception: **do not run it**, by hand or through `dispat run`. It publishes the live site and
refuses unless `CI=true`, for the reason spelled out next.

`deploy-docs.sh` force-pushes the live site,
and the tag dispat writes afterwards is the only record that the leg completed, so a deploy outside a release run
publishes a working tree to production and leaves nothing behind saying so. The script refuses unless `CI=true` for
exactly that reason. These are top-level `scripts`, so every changed package resolves them: without a
`--package` filter (`dispat run deploy-docs`) the run would reach all of them at once.

The scripts are POSIX `sh` with `set -eu`, and they are the release path itself: a change here is only really exercised
by the [Release workflow](../.github/workflows/release.yml).
