# Release scripts

You will find four standalone shell scripts here: two for CI glue and checks shared across workflows, one install
manifest listing the tools a release fetches at their newest releases, and one
host-run half of the TinyGo spike, which is not CI glue at all — buildx reaches only linux, so the darwin probes run
on a Mac by hand. Smaller scripts
live directly in dispat configuration files as script entries, such as `push-badge` in the root
[`dispat.yaml`](../dispat.yaml), `deploy-docs` in [`packages/docs/dispat.yaml`](../packages/docs/dispat.yaml), the link
bracket in [`services/dispat/dispat.yaml`](../services/dispat/dispat.yaml), and `push-readme` in
[`docker/dispat.yaml`](../docker/dispat.yaml), while test records live in [`tools/testreport`](../tools/testreport) as
`testreport test`. Each script entry sits beside the code it configures, so the root file holds shared space settings,
each space carries a `<space>/dispat.yaml`, and two exception packages carry their own package files.

Package scripts run **inside the releasing package's folder**, while root scripts run at the repository root. Scripts
receive everything they need through environment variables: the
[`DISPAT_*` variables](https://dispat.dev/reference/environment/) from their stage, plus variables
exported by CI.

| Script                                     | Called from                                            | Reads                                   | Produces |
|--------------------------------------------|--------------------------------------------------------|-----------------------------------------|----------|
| [`buildx-cache.sh`](./buildx-cache.sh)     | every `docker buildx build` in a dispat script         | `GITHUB_ACTIONS`, its scope argument    | The `--cache-from`/`--cache-to` flags for that build's cache scope, or nothing outside Actions. |
| [`check-action.sh`](./check-action.sh)     | the Action workflow and the release's post-release job | its arguments                           | Assertions that the composite action installed what it promised. |
| [`install-tools.sh`](./install-tools.sh)   | the release job; the ping and replay jobs, the `tiny-toolchain` stage of [`services/dispat/Dockerfile`](../services/dispat/Dockerfile), the `tinygo-spike-fork` stage of [`Dockerfile.tinygo`](../Dockerfile.tinygo) and `tinygo-spike-darwin.sh` run one of its lines directly with their own `--bin-dir` | `GITHUB_TOKEN`, `DISPAT_BIN_DIR` | Two `dispat install` lines, the newest crier and the newest TinyGo fork; nothing is pinned, and a tool needed on its own is one line of it run by hand. |
| [`tinygo-spike-darwin.sh`](./tinygo-spike-darwin.sh) | by hand, on a Mac                            | its toolchain pins, [`Dockerfile.tinygo`](../Dockerfile.tinygo)'s probe heredocs | The darwin half of the TinyGo spike — build, run, net and self-update probes for darwin/amd64+arm64, recorded as `coverage/tinygo-spike/darwin-*.log`, with `darwin-selfupdate.log` carrying the real-TLS update matrix and the platform verifier's answer about `SSL_CERT_FILE`. |

Every gate and stage of this repository runs inside Docker, so a CI job needs Docker, git and dispat itself — no Go,
Node or Terraform on the runner. The Go gates (vet, tests, gofmt, the coverage badge, the test report, `go mod tidy`)
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

Run `dispat exec coverage-badge` to merge the generated profiles in `coverage/` and produce the badge JSON — the merge
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
dispat exec experiments --for pkg:experiments --in pkg:experiments   # ~15 min: the twelve release experiment cells
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
