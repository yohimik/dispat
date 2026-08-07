# Unit test coverage

Statement coverage of the workspace's **unit test layer**, measured the way CI computes the badge on the repository
README: one `go test -covermode=atomic` profile per module, with `-coverpkg=./...` for the CLI module so its in-process
end-to-end suites (`cli`, `app`) also count toward the internal packages they drive, merged into a single profile. The
badge always reflects the latest `main` build; the table below is a local snapshot, regenerated on **2026-08-07** with
Go 1.26.

| Module / package                 | Statement coverage                                                                       |
|----------------------------------|------------------------------------------------------------------------------------------|
| **workspace total**              | **94.8%**                                                                                |
| `pkg/ccme` (commit parser)       | **96.9%**, plus fuzz tests, allocation tests and the specification's conformance vectors |
| `pkg/models` (public config)     | 100%                                                                                     |
| `services/dispat` (all packages) | **93.7%** aggregate; the thin `main.go` entry point is the only uncovered file           |
| - `script`, `model`              | 100%                                                                                     |
| - `graph` (scheduler)            | 98.9%                                                                                    |
| - `config`                       | 97.7%                                                                                    |
| - `release` (executor)           | 97.1%                                                                                    |
| - `changelog`                    | 96.7%                                                                                    |
| - `plan` (planner)               | 94.7%                                                                                    |
| - `github`                       | 92.6%                                                                                    |
| - `gitx`                         | 91.5%                                                                                    |
| - `cli` (end-to-end, in-process) | 89.2%                                                                                    |
| - `app` (end-to-end, in-process) | 86.0%                                                                                    |

The per-package test inventory (what each package's suite actually asserts) is in
[Architecture / Testing](./architecture.md#testing).

## Reproducing

From the monorepo root (the `go.work` workspace resolves the cross-module dependencies):

```sh
go test -C pkg/ccme        ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-ccme.out"
go test -C pkg/models      ./... -count=1 -covermode=atomic -coverprofile="$PWD/cover-models.out"
go test -C services/dispat ./... -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$PWD/cover-dispat.out"

{ head -n 1 cover-ccme.out
  tail -q -n +2 cover-ccme.out cover-models.out cover-dispat.out
} > coverage.out
go tool cover -func=coverage.out | tail -n 1   # the workspace total
```

Per-package numbers come from grouping the merged profile's statement blocks by package directory (`go tool cover
-func` reports per function; CI's job summary prints the same total).

## What the integration suite adds

The [integration suite](../../../tests/integration/README.md) is deliberately **black-box**: it compiles the real dispat
binary and drives it from the outside, so it produces no statement-coverage profile of dispat's sources on top of the
numbers above. What it covers is behaviour: git state, JSON log events, execution timelines. Its status and per-area
summary live in [test results](../../../tests/integration/docs/test-results.md), with the claim-by-claim matrix in
the [test plan](../../../tests/integration/docs/test-plan.md).
