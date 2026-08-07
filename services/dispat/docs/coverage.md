# Test coverage

Statement coverage of the **whole test suite**, measured the way CI computes the badge on the repository README: one
`go test -covermode=atomic` profile per module (with `-coverpkg=./...` for the CLI module), **plus** the black-box
integration suite's profile — its harness builds the real dispat binary with `-cover` when `DISPAT_COVERDIR` is set and
points every invocation's `GOCOVERDIR` there, so the flows only that suite exercises (the app/cli composition, the
finalize phase, the commands) count too. All four text profiles concatenate into one (`go tool cover` merges the
overlapping blocks). The README carries one badge per layer — `unit` (all modules, **86.1%**), `integration` (the CLI
module through the instrumented binary, **83.7%**) — combining into the `coverage` badge's workspace total. Every badge
reflects the latest `main` build; the numbers here are a local snapshot, regenerated on **2026-08-08** with Go 1.26.

| Module / package                 | Statement coverage                                                                       |
|----------------------------------|------------------------------------------------------------------------------------------|
| **workspace total**              | **95.3%**                                                                                |
| `pkg/ccme` (commit parser)       | **96.8%**, plus fuzz tests, allocation tests and the specification's conformance vectors |
| `pkg/models` (public config)     | 100%                                                                                     |
| `services/dispat` (all packages) | **94.5%** aggregate, `main.go` included (the integration suite runs the real binary)     |
| - `main.go`, `script`, `model`   | 100%                                                                                     |
| - `graph` (scheduler)            | 98.9%                                                                                    |
| - `config`                       | 98.5%                                                                                    |
| - `cli` (controller)             | 98.1%                                                                                    |
| - `release` (executor)           | 97.1%                                                                                    |
| - `changelog`                    | 96.7%                                                                                    |
| - `plan` (planner)               | 94.9%                                                                                    |
| - `github`                       | 92.9%                                                                                    |
| - `gitx`                         | 91.2%                                                                                    |
| - `app`                          | 90.4%                                                                                    |

The per-package test inventory (what each package's suite actually asserts) is in
[Architecture / Testing](./architecture.md#testing).

## Reproducing

From the monorepo root (the `go.work` workspace resolves the cross-module dependencies):

```sh
go test -C pkg/ccme        ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-ccme.out"
go test -C pkg/models      ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-models.out"
go test -C services/dispat ./... -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$PWD/cover-cli.out"

export DISPAT_COVERDIR="$PWD/covdata" && mkdir -p "$DISPAT_COVERDIR"
go test -C tests/integration ./... -count=1
go tool covdata textfmt -i="$DISPAT_COVERDIR" -o="$PWD/cover-integration.out"

{ head -n 1 cover-ccme.out
  tail -q -n +2 cover-ccme.out cover-models.out cover-cli.out cover-integration.out
} > coverage.out
go tool cover -func=coverage.out | tail -n 1   # the workspace total
```

Per-package numbers come from grouping the merged profile's statement blocks by package directory (`go tool cover
-func` reports per function; CI's job summary prints the same total).

## What each layer contributes

The unit layer covers every package against in-memory fakes; on its own it reaches ~86%, because the composition —
the compiled binary, the finalize phase, the run/test/preview commands, real git over a process boundary — lives
exclusively in the [black-box integration suite](../../../tests/integration/README.md), which asserts behaviour (git
state, JSON log events, execution timelines) rather than statements. Instrumenting the binary it drives is what folds
that behaviour back into the statement numbers above without compromising the suite's black-box design: the harness
only sets two environment knobs, and with `DISPAT_COVERDIR` unset (the default, and every local `go test`) the binary
is built exactly as released. The suite's status and per-area summary live in
[test results](../../../tests/integration/docs/test-results.md), with the claim-by-claim matrix in the
[test plan](../../../tests/integration/docs/test-plan.md).
