# Release scripts

These scripts provide shared CI checks, compatibility entry points, and toolchain probes. Package-specific commands
live beside their packages in `dispat.yaml`. Shared configuration stays at the root only when several packages need it.
The tool installer is a thin entry point to [`tools/bootstrap.yaml`](../tools/bootstrap.yaml), which owns the Aqua version
and installation steps. Both Docker builds and local installs use that same policy.

Package scripts run **inside the releasing package's folder**, while root scripts run at the repository root. Scripts
receive everything they need through environment variables: the
[`DISPAT_*` variables](https://dispat.dev/reference/environment/) from their stage, plus variables
exported by CI.

| Script                                     | Called from                                            | Reads                                   | Produces |
|--------------------------------------------|--------------------------------------------------------|-----------------------------------------|----------|
| [`buildx-cache.sh`](./buildx-cache.sh)     | every `docker buildx build` in a dispat script         | `GITHUB_ACTIONS`, its write scope and optional read-only scopes | `TEST_COMMIT`, plus the Actions cache flags in CI. Aggregate builds can import package scopes while updating only their own. |
| [`ci-worker.sh`](./ci-worker.sh)           | the release's full-suite job                           | `DISPAT_EXECUTION_SECRET`, `DISPAT_CI_WORKER_*`, the Workload Identity credentials | One Compute Engine worker per run: created, started as `dispat worker`, its log collected, deleted; the job runs the suite against it with `--worker`. |
| [`check-action.sh`](./check-action.sh)     | the Action workflow and the release's post-release job | its arguments                           | Assertions that the composite action installed what it promised. |
| [`install-tools.sh`](./install-tools.sh)   | the release job; the ping and replay jobs, the `tiny-toolchain` stage of [`services/dispat/Dockerfile`](../services/dispat/Dockerfile), the `tinygo-spike-fork` stage of [`Dockerfile.tinygo`](../Dockerfile.tinygo) and `tinygo-spike-darwin.sh` | `GITHUB_TOKEN`, `DISPAT_BIN_DIR`; optionally `[all\|crier\|tinygo] [destination]` | Installs the pinned Aqua with dispat, then the repository-recorded crier and TinyGo fork through `.aqua/aqua.yaml`. The destination receives `aqua` and the selected tools: a real `crier` binary and/or a link to the complete TinyGo tree at `tinygo`. |
| [`lint-config-scripts.py`](./lint-config-scripts.py) | the `shellcheck` target of [`Dockerfile.gotest`](../Dockerfile.gotest) | every committed dispat config named on its command line | Each `scripts` entry as one shell file, held to `sh -n` and to shellcheck. It refuses a configuration shape it cannot read rather than skipping it, and `--self-test` holds the reader to the four shapes an entry is written in. |
| [`tinygo-spike-darwin.sh`](./tinygo-spike-darwin.sh) | by hand, on a Mac                            | its toolchain pins, [`Dockerfile.tinygo`](../Dockerfile.tinygo)'s probe heredocs | The darwin half of the TinyGo spike: build, run, net and self-update probes for darwin/amd64+arm64, recorded as `coverage/tinygo-spike/darwin-*.log`, with `darwin-selfupdate.log` carrying the real-TLS update matrix and the platform verifier's answer about `SSL_CERT_FILE`. |

Repository gates run inside Docker, so the commit CI jobs need Docker, git and dispat itself. The release job also
installs Node and pnpm to compile, pack and publish the npm distribution through npm trusted publishing. Terraform
and the native Go builds remain inside Docker. The Go gates (vet, tests, gofmt, the coverage badge, the test report, `go mod tidy`)
are targets of [`Dockerfile.gotest`](../Dockerfile.gotest) at the repository root; each dispat script drives one
`docker buildx build` and reads results back as exported files. The CLI produces
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

Add `--shards N` before the `--` to split a slow suite over N concurrent `go test` processes. The command lists the
tests with the same arguments, runs an exact round-robin share of them in each process, and still writes one log, one
summary and one exit status. A `-coverprofile` is written per shard and merged into the file it names. The
`test-integration` target of [`Dockerfile.gotest`](../Dockerfile.gotest) runs both integration passes in six shards:
the suite spends most of its time waiting on the processes it drives, so a single process inside a container needs
about twice the hour a pass is allowed.

Run `dispat exec coverage-badge` to merge the generated profiles in `coverage/` and produce the badge JSON; the merge
logic lives in the `badge` target of [`Dockerfile.gotest`](../Dockerfile.gotest), and the summary table it writes to
`coverage/summary.md` is what the script appends to the job summary. The `test-report` root script (the `report`
target) then compiles those profiles and logs into
`packages/docs/data/report.json`, which feeds the site [coverage](https://dispat.dev/internals/coverage/)
and [test results](https://dispat.dev/internals/test-results/) pages. The
[Release workflow](../.github/workflows/release.yml) runs both through `dispat exec` because only a `--since all` run
generates a complete profile set across all packages.

Run these commands to reproduce the entire test and report pipeline locally:

```sh
dispat run tests --since all      # ~6 min: every module's profile and log
dispat exec coverage-badge        # the merged profiles and the badge JSON
dispat exec experiments --for pkg:experiments --in pkg:experiments   # the release experiment matrix
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

All scripts use POSIX `sh` with `set -eu`, and so does the shell inside every `scripts` entry of a dispat config: the
`shellcheck` target reads those entries out of the configuration files and holds them to the same two checks it holds
a file to. Test changes to release scripts carefully, because only the
[Release workflow](../.github/workflows/release.yml) exercises the full release path.

## What the override ladder resolves to

The release calls some scripts by name from one package, so a name that moves a level fails at release time rather
than in a gate. Run `dispat exec config-check` to assert the resolutions that matter: the package gates that override
their space's, the record step the services space names in `flow.publish`, and a root script reaching a package that
declares none of its own. The check reads this repository's configuration through dispat itself, with `shell` replaced
by a printer, so every resolution it asserts is printed rather than run.
