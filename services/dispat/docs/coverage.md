# Test coverage

The whole test suite currently covers **94.7%** of the workspace's statements (unit layer alone: 87.9%; the
integration layer's instrumented binary: 81.3%).

The number is measured the way CI computes the badge on the repository README. Each module's own tests produce one
`go test -covermode=atomic` profile (with `-coverpkg=./...` for the CLI module). The black-box integration suite
contributes one more: its harness builds the real dispat binary with `-cover` when `DISPAT_COVERDIR` is set and points
every invocation's `GOCOVERDIR` there, so the flows only that suite exercises (the app/cli composition, the finalize
phase, the commands, signal handling) count too. All seven text profiles concatenate into one, `go tool cover` merges
the overlapping blocks, and the total becomes the badge, always for the latest `main` build. The per-layer numbers
appear in CI's job summary.

The badge is the authoritative, always-current number. The table below is a hand-run local snapshot, regenerated on
**2026-08-09** with Go 1.26 using the steps under [Reproducing](#reproducing), and drifts until someone regenerates it.

| Module / package                        | Statement coverage                                                                       |
|-----------------------------------------|------------------------------------------------------------------------------------------|
| **workspace total**                     | **94.7%**                                                                                |
| `pkg/ccme` (commit parser)              | **97.0%**, plus fuzz tests, allocation tests and the specification's conformance vectors |
| `pkg/models` (public config)            | 100%                                                                                     |
| `pkg/manifest` (shared vocabulary)      | 100%                                                                                     |
| `pkg/scanner` (manifest reader)         | **94.9%**, plus two fuzz targets over every registered parser                            |
| `pkg/writer` (manifest writer)          | **85.4%**, plus a fuzz target proving rewrites never corrupt valid JSON                  |
| `services/dispat` (all packages)        | **94.5%** aggregate, `main.go` included (the integration suite runs the real binary)     |
| - `main.go`, `globx`, `model`           | 100%                                                                                     |
| - `cli` (controller)                    | 99.2%                                                                                    |
| - `graph` (scheduler)                   | 98.2%                                                                                    |
| - `changelog`                           | 97.0%                                                                                    |
| - `plan` (planner)                      | 96.3%                                                                                    |
| - `release` (executor)                  | 95.1%                                                                                    |
| - `gitx`                                | 93.8%                                                                                    |
| - `config`                              | 93.3%                                                                                    |
| - `github`                              | 93.0%                                                                                    |
| - `app`                                 | 92.1%                                                                                    |
| - `script`                              | 92.0%                                                                                    |

The per-package test inventory (what each package's suite actually asserts) is in
[Architecture / Testing](./architecture.md#testing).

## Reproducing

From the monorepo root (the `go.work` workspace resolves the cross-module dependencies):

```sh
go test -C pkg/ccme        ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-ccme.out"
go test -C pkg/manifest    ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-manifest.out"
go test -C pkg/models      ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-models.out"
go test -C pkg/scanner     ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-scanner.out"
go test -C pkg/writer      ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-writer.out"
go test -C services/dispat ./... -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$PWD/cover-cli.out"

export DISPAT_COVERDIR="$PWD/covdata" && mkdir -p "$DISPAT_COVERDIR"
go test -C tests/integration ./... -count=1
go tool covdata textfmt -i="$DISPAT_COVERDIR" -o="$PWD/cover-integration.out"

{ head -n 1 cover-ccme.out
  tail -q -n +2 cover-ccme.out cover-manifest.out cover-models.out cover-scanner.out cover-writer.out cover-cli.out cover-integration.out
} > coverage.out
go tool cover -func=coverage.out | tail -n 1   # the workspace total
```

Per-package numbers come from filtering the merged profile down to one package's files (keep the mode line) and
running the same `go tool cover -func | tail -n 1` on the filtered profile.

## What each layer contributes

The unit layer covers every package against in-memory fakes; on its own it reaches ~88%, because the composition (the
compiled binary, the finalize phase, the run/test/preview commands, real git over a process boundary, SIGINT handling)
lives exclusively in the [black-box integration suite](../../../tests/integration/README.md), which asserts behaviour
(git state, JSON log events, execution timelines) rather than statements. Instrumenting the binary it drives is what
folds that behaviour back into the statement numbers above without compromising the suite's black-box design: the
harness only sets two environment knobs, and with `DISPAT_COVERDIR` unset (the default, and every local `go test`) the
binary is built exactly as released. The suite's status and per-area summary live in
[test results](../../../tests/integration/docs/test-results.md), with the claim-by-claim matrix in the
[test plan](../../../tests/integration/docs/test-plan.md).
