# Integration tests

The black-box integration suite for the dispat CLI. It is a separate Go module that structurally cannot import
`services/dispat/internal/*` (Go's `internal` rule): it compiles the **real binary** from
[`services/dispat`](../../services/dispat), drives it against disposable git repositories exactly as a user's shell
would, and asserts on the three outputs a release run actually has:

- **git state**: tags (their objects, messages and targets), commits, changelog files;
- **JSON log events**: `--log-format json`, the machine-readable contract CI ingests;
- **execution timelines**: nanosecond-resolution intervals recorded by the purpose-built `tsmark` probe, wherever
  *timing* rather than mere ordering is the claim.

Configs are authored as typed models from the public [`pkg/models`](../../pkg/models) module and marshalled to JSON,
so a test that compiles is a test whose config loads.

The full design (goals, architecture, the coverage matrix per test, flakiness posture and conventions for new tests)
lives in the **[test plan](./docs/test-plan.md)**.

## Setup

No setup beyond the repository itself is needed. Requirements:

- the **Go toolchain** (see [`go.mod`](./go.mod) for the version) on `PATH`;
- **git** on `PATH`; every test creates and drives a real repository (tests skip if git is missing).

The module resolves its dispat dependencies through the workspace (`go.work` at the monorepo root) and local
`replace` directives, so a plain checkout is ready to run. The dispat and tsmark binaries are compiled **once per
`go test` invocation** and shared across all tests (a `sync.Once` cache in the harness); each test creates its
repository in a fresh `t.TempDir()`, so tests are independent and safe to run in any order or subset.

## Running

```sh
cd tests/integration
go test ./...                    # the whole suite
go test ./... -race              # also clean under the race detector
go test . -run TestVersioning    # one area (see the test files below)
go test ./... -count 5           # stability check: repeated runs pass
```

## Results

The suite's current status and per-area coverage summary live in **[test results](./docs/test-results.md)**: what
passes (including `-race` and repeated `-count` runs), what each test file covers, and the planner properties fenced
by dedicated regression tests. The
workspace's unit-test statement coverage table lives in
**[unit test coverage](../../services/dispat/docs/coverage.md)**.
