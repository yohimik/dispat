# Integration test results

Current status of the black-box suite ([setup and running](../README.md); the claim-by-claim coverage matrix is in
the [test plan](./test-plan.md)).

## Status

- The whole suite passes: `go test ./...` in [`tests/integration`](..).
- **Race-clean**: `go test ./... -race` passes.
- **Stable**: ordering assertions are structural (flake-free), overlap assertions use sleeps one to two orders of
  magnitude above process-launch jitter, and every concurrency claim is verified three independent ways before it is
  believed. Repeated runs (`go test ./... -count 5`) are the manual stability check; CI runs the suite twice per
  build (once instrumented for coverage, once under `-race`), not with `-count`.

## Coverage by area

One test file per goal:

| File                  | Area                                                                                                                                                                                                                                                                                                                                                                                          |
|-----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `concurrency_test.go` | Build/publish concurrency budgets: peaks reached *and* respected, budgets independent per stage. Every claim checked three independent ways.                                                                                                                                                                                                                                                  |
| `order_test.go`       | The dependency graph driving script execution order, under both `isBuildWaitingPublish` modes; version-task placement and environment.                                                                                                                                                                                                                                                        |
| `plan_test.go`        | Plan logic across consecutive runs: cancels, holds, pins and their guards, catch-up, blocked consumers, prerelease trains, convergence.                                                                                                                                                                                                                                                       |
| `config_test.go`      | Config validation and precedence, config file discovery (the `--config` fallback names), parser options (custom types, default propagation depth, strictTypes), custom shells, login scripts, outcome scripts, revertOnFail, tag-format round trips, GitHub releases, script-output accumulation across stages and hooks (onFail included), release attachments, the full nine-hook per-package frame in order, and the gating/warn-only hook authority split.                           |
| `versioning_test.go`  | The three space versioning modes side by side: fixed rides, sparse alignment, one shared train, failed-ride catch-up, holds and pins under a shared version, and the W211/W212 conflict resolutions (competing pins, divergent channels).                                                                                                                                                                                                                                  |
| `run_test.go`         | The `dispat run` command: graph-ordered execution (also under concurrency), the command shorthand, both `--on-error` policies, the concurrency budget, cross-package output carrying, skipping.                                                                                                                                                                                               |
| `records_test.go`     | The release records as artefacts: changelog accumulation above pre-dispat content, custom file/title/sections, annotated tags (object type, message, target), commit mode's release commit + tag placement + push to a bare remote, push skipping remote-existing tags, the `PACKAGE_<KEY>` exported-commit tag pin, `commit.verify=false`, and a fully failed run leaving history untouched. |
| `fatal_test.go`       | The repository-scoped fatal errors, each constructed for real: a dependency cycle (E200), duplicate version tags on different commits (E191), and a shallow clone (E196). All refuse to release with the code in the events and nothing executed.                                                                                                                                             |
| `compute_test.go`     | The compute command through the binary: the detect/apply/check loop with backup and convergence, keep/removal semantics, and the W220 ambiguity as a JSON event.                                                                                                                                                                                                                             |
| `autoversion_test.go` | Native auto-versioning through the binary: range/version rewriting under the match policy, the serialised syncLock slot, the W192/W197/W203/W221 diagnostics across three runs, and commit.include staging the regenerated root lock file.                                                                                                                                                    |

## Regression fences

Two planner properties carry dedicated guard tests: a rejected `Release-As` pin must not swallow a sibling bump, and a
propagated graduation transition must graduate the dependants. See the
[regression fences in the test plan](./test-plan.md#regression-fences).
