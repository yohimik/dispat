# Integration test results

Current status of the black-box suite ([setup and running](../README.md); the claim-by-claim coverage matrix is in
the [test plan](./test-plan.md)).

## Status

- The whole suite passes: `go test ./...` in [`tests/integration`](..).
- **Race-clean**: `go test ./... -race` passes.
- **Stable**: repeated runs (`-count`) pass; ordering assertions are structural (flake-free), overlap assertions use
  sleeps one to two orders of magnitude above process-launch jitter, and every concurrency claim is verified three
  independent ways before it is believed.

## Coverage by area

One test file per goal:

| File                  | Area                                                                                                                                       |
|-----------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `concurrency_test.go` | Build/publish concurrency budgets: peaks reached *and* respected, budgets independent per stage. Every claim checked three independent ways. |
| `order_test.go`       | The dependency graph driving script execution order, under both `isBuildWaitingPublish` modes; version-task placement and environment.      |
| `plan_test.go`        | Plan logic across consecutive runs: cancels, holds, pins and their guards, catch-up, blocked consumers, prerelease trains, convergence.     |
| `config_test.go`      | Config validation and precedence, parser options (custom types, default propagation depth, strictTypes), custom shells, login scripts, outcome scripts, revertOnFail, tag-format round trips, GitHub releases, script-output accumulation across stages and hooks (onFail included) and release attachments. |
| `versioning_test.go`  | The three space versioning modes side by side: fixed rides, sparse alignment, one shared train, failed-ride catch-up, holds and pins under a shared version. |
| `run_test.go`         | The `dispat run` command: graph-ordered execution (also under concurrency), the command shorthand, both `--on-error` policies, the concurrency budget, cross-package output carrying, skipping. |

## Findings

The suite originally turned up two behaviours contradicting the documentation (a rejected `Release-As` pin
swallowing a sibling bump; a propagated graduation transition never graduating dependants). Both are now **fixed in
the planner**, with the fences flipped into tests guarding the corrected behaviour — see the
[findings in the test plan](./test-plan.md#findings).
