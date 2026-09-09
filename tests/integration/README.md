# Integration tests

This is the black-box integration suite for the dispat CLI. Because it is a separate Go module, Go's `internal` rule
prevents it from importing `services/dispat/internal/*`. It compiles the **real binary** from
[`services/dispat`](../../services/dispat), runs it against disposable git repositories just like your shell would, and
asserts on three release outputs:

- **git state**: tags (their objects, messages and targets), commits, and changelog files.
- **JSON log events**: `--log-format json`, the machine-readable contract CI ingests.
- **execution timelines**: nanosecond-resolution intervals recorded by the purpose-built `tsmark` probe, wherever
  *timing* rather than mere ordering is the claim.

You author test configs as typed models from the public [`pkg/models`](../../pkg/models) module and marshal them to
JSON. If your test compiles, its config is guaranteed to load.

Read the **[test plan](./docs/test-plan.md)** for the full design, including goals, architecture, flakiness policies,
conventions, and the per-test coverage matrix.

## Setup

You do not need any setup beyond cloning the repository. Ensure you have these tools installed:

- The **Go toolchain** (see [`go.mod`](./go.mod) for the version) on `PATH`.
- **git** on `PATH`. Every test creates and drives a real repository, and tests skip automatically if git is missing.

Dependencies resolve through the workspace (`go.work` at the monorepo root), so a clean checkout is ready immediately.
The harness compiles the dispat and tsmark binaries **once per `go test` invocation** and caches them via `sync.Once`
across all tests. Each test builds its repository in a fresh `t.TempDir()`, so you can run tests in any order or select
any subset safely.

`DISPAT_TEST_COMPILER=<go-or-tinygo-path>` selects the compiler for every dispat binary the harness builds, including
the version-stamped candidates used by the self-update tests. TinyGo builds use the release flags `-opt=z -no-debug`,
compile with `-p 2`, and run with `GOMAXPROCS=2` and a 6 GiB Go heap limit. The test runner and the `tsmark` timing
helper still use Go.

`DISPAT_TEST_BINARY=<path>` makes the harness drive an already-built ordinary binary. It may be paired with
`DISPAT_TEST_COMPILER=tinygo` so version-stamped fixtures use the same compiler, or with
`DISPAT_TEST_VERSIONED_BINARY_DIR=<dir>`, whose matching files are named `dispat-<version>`. A self-update test that
asks for a versioned binary rejects an incomplete prebuilt selection instead of silently building that fixture with
Go. This is how a release gate can test the exact artifact it will export while keeping every candidate on TinyGo.

Prebuilt and TinyGo modes reject `DISPAT_COVERDIR` and `DISPAT_TEST_RACE=1`: those settings promise Go coverage or race
instrumentation that the selected binaries do not carry. Ordinary Go coverage and race runs retain their existing
behavior.

## Running

```sh
cd tests/integration
go test ./...                    # the whole suite
go test ./... -race              # also clean under the race detector
go test . -run TestVersioning    # one area (see the test files below)
go test ./... -count 5           # stability check: repeated runs pass
```

## Results

Current test results are omitted here so the documentation contains only verified measurements. Every release runs the
entire suite and publishes live data alongside the documentation site:

- **[test results](https://dispat.dev/internals/test-results/)**: counts, timings, race detector results,
  and the scope of each test file.
- **[test coverage](https://dispat.dev/internals/coverage/)**: statement coverage across all workspace
  packages, including this suite's instrumented binary.

To see which goals each test proves, consult the matrix in the [test plan](./docs/test-plan.md).
