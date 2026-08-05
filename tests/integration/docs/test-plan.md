# Integration test plan

This module (`tests/integration`) is the black-box integration suite for the dispat CLI. It compiles the real
binary from `services/cli`, drives it against disposable git repositories exactly as a user's shell would, and
asserts on the three outputs a release run actually has: **git state** (tags, commits, file contents), **JSON log
events** (`--log-format json`, the machine-readable contract CI ingests), and — where *timing* rather than mere
ordering is the claim — **nanosecond-resolution execution timelines** recorded by a purpose-built probe.

## Goals

The suite was designed against four goals, one test file each:

1. **Concurrency** (`concurrency_test.go`) — stable tests *guaranteeing* the budgets work: with concurrency 4 and
   five packages, the fifth's work starts exactly after one of the first four finishes; independent packages are
   picked up concurrently while dependants are awaited.
2. **Execution order by dependency graph** (`order_test.go`) — scripts run in the order the graph dictates, under
   both `isBuildWaitingPublish` settings.
3. **Plan logic** (`plan_test.go`) — prereleases, cancels, holds, catch-up, provider-failed and consumer-failed
   runs, and as many weird cases as earn their keep — including that scripts execute *according to* the plan
   (a held or cancelled package runs nothing; a resumed one runs exactly once).
4. **Everything else** (`config_test.go`) — config validation and precedence, login scripts, the `run.onFail` /
   `run.onSkip` outcome scripts, and original cases the unit suites cannot witness.

Configs are written in the current schema — stages and hooks in the nested `run` objects (per-space and
top-level) — and the suite pins the migration edge deliberately: a config still using the legacy flat keys
(`buildScript` on a space) must be *rejected* at load, not silently ignored into a script-less release.

It deliberately duplicates as little as possible of the unit suites listed in
[`services/cli/docs/architecture.md`](../../../services/cli/docs/architecture.md#testing): those already cover each
package against in-memory fakes, and `internal/cli`'s own tests cover the end-to-end happy paths in-process. What
only this module can check is the composition — the compiled binary, a real scheduler racing real processes, a
config file read from disk, exit codes over a process boundary — so every test here earns its place by asserting
something a fake cannot witness.

## Why a separate Go module

- The suite must not import `services/cli/internal/*` — and as a separate module it structurally *cannot* (Go's
  `internal` rule), which keeps it an honest black box: if a behaviour is not observable through the CLI, tags,
  files or logs, a test here cannot accidentally reach around and read it off a struct. The unit-tested git and
  shell code is therefore reused the only way a black box can reuse it: compiled into the binary under test,
  with the harness mirroring the *fixture patterns* of `internal/cli`'s own end-to-end tests (`initRepo` + a
  `git` closure, promoted to the reusable `harness.Repo`).
- It keeps the slower end-to-end tests out of `go test ./...` for the production modules, while `go.work` makes
  builds and IDE navigation seamless.
- Its only dependency is `testify`, matching the existing test style.

## Architecture

```
tests/integration/
  go.mod                    separate module: github.com/yohimik/dispat/tests/integration
  cmd/tsmark/               the timing probe (see below)
  internal/harness/
    binary.go               builds dispat + tsmark once per test run (sync.Once cache)
    repo.go                 Repo: git fixture (init, seed packages, commit, tags), config
                            writing, Base() config fragment, Release/ReleaseOK/Status/
                            StatusOK -> RunResult
    events.go               Event: parsed JSON log lines; HasCode / HasCodeForPackage
    timeline.go             Interval: tsmark log parsing; the concurrency assertions
  helpers_test.go           fixtures shared across test files: packageNames,
                            seedIndependentPackages, singlePackageRepo, linkedRepo,
                            markerBuild/failIfMarker scripts, buildRuns
  concurrency_test.go       goal 1
  order_test.go             goal 2
  plan_test.go              goal 3
  config_test.go            goal 4
  docs/test-plan.md         this document
```

### The tsmark probe, and why timing assertions are trustworthy

dispat's own JSON logs carry RFC3339 timestamps — one-second resolution — which cannot distinguish "ran
concurrently" from "ran back to back within the same second". Instead of scraping logs, every timing-sensitive
script is wired to `tsmark`, a dependency-free Go binary that appends `<label> start <unixnano>` /
`<label> end <unixnano>` lines to a shared file (O_APPEND single-write lines, atomic on a local filesystem) and
sleeps in between. The scheduler either launched a process while another was still sleeping or it did not; the
file says which, with no reliance on shell tooling (`date +%N` prints a literal `N` on macOS) or host clocks.

Every concurrency claim is then checked **three independent ways** before it is believed
(`harness.AssertConcurrencyBudget`):

1. a sweep-line max-overlap count,
2. a brute-force O(n²) pairwise overlap count — independently written, required to agree with the sweep, so a
   tie-breaking bug in one cannot quietly agree a real scheduler defect out of existence,
3. a start-order argument: sorted by start, the (budget+1)-th task must not begin before one of the first
   *budget* tasks ended.

And the peak must be **exactly** `min(budget, tasks)` — "at most N" alone is satisfied by a scheduler that
serialises everything, "N reached" alone by one that ignores the limit; only the exact peak proves both halves.

Flakiness posture: *ordering* assertions (`AssertSequential`) are structural — the task graph either has the edge
or it does not, whatever any script's duration — so they cannot flake. *Overlap* assertions are made robust by
sleeps (100–400 ms) one to two orders of magnitude above process-launch jitter. The suite passes repeated
`-count` runs and `-race`.

## Coverage matrix

### Goal 1 — concurrency (`concurrency_test.go`)

| Test | Claim proven |
|---|---|
| `TestConcurrencyBuildBudgetEnforced` | Budget 4, five independent packages: peak overlap exactly 4 — the 5th build starts only after one of the first four ends (three independent checks). |
| `TestConcurrencyPublishBudgetIsIndependentOfBuild` | Separate stage budgets: in one run, unconstrained builds reach overlap 5 while publishes stay capped at 2. |
| `TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited` | Three independent providers pairwise overlap; their shared consumer's build starts strictly after all three provider builds end. |

### Goal 2 — execution order by dependency graph (`order_test.go`)

| Test | Claim proven |
|---|---|
| `TestOrderChainRunsInTopologicalOrder` | `base <- mid <- top`: builds and publishes each run in topological order, driven by `dependencies` edges alone. |
| `TestOrderBuildWaitsForPublishWhenConfigured` | `isBuildWaitingPublish: true` — consumer's build starts only after the provider's *publish* ends. |
| `TestOrderBuildDoesNotWaitForPublishByDefault` | Flag false — consumer's build runs *during* the provider's publish (timing evidence), while the consumer's own publish still waits for it (structural, flag-independent). |
| `TestOrderDiamondDependencyConverges` | Fan-out/fan-in (`a -> b,c -> d`): `b`/`c` overlap; `d` waits for both, at build and at publish. |
| `TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv` | A `DueTo` consumer runs a version task whose `DISPAT_UPDATED_*` names exactly the live provider; a direct-release package in the *same space with the same versionScript* never runs one. |

### Goal 3 — plan logic (`plan_test.go`)

| Test | Claims proven |
|---|---|
| `TestPlanCancelSemantics` | Cancel discards pending work irreversibly (post-cancel fix releases 0.0.1, not 0.1.1); a spent cancel warns (W170); a cancelled/no-op release run executes zero scripts. |
| `TestPlanHoldResumeAndReleaseAsAuto` | Hold reports the withheld version (W154) and excludes the package from *execution*, not just tagging (zero script runs while held); resume releases at accumulated `max()` with exactly one build; redundant `auto` warns (W158). |
| `TestPlanExactPinGuards` | E153 (not greater), E157 (major jump > 1), E154 (multi-package pin), each in an isolated repo so a rejected pin cannot collide with earlier tags. |
| `TestPlanRejectedPinSwallowsAnAccompanyingBump` | **Regression fence, finding #1** (below). |
| `TestPlanConsumerFailureCatchesUpAfterProviderPublished` | Consumer fails while provider publishes; the next run catches the consumer up at the owed version, labelled W193, provider not re-released; a third run converges. |
| `TestPlanProviderBuildFailureBlocksConsumerThenHeals` | Provider fails to build; consumer is blocked (W194), never attempted; after the fix both release in one run, with neither W194 nor W193. |
| `TestPlanCatchUpWholeHistoryForNeverReleasedConsumer` | A package created *after* a provider's propagating commit still catches up on its first ever run — an untagged package's window is the whole history. |
| `TestPlanPrereleaseTrainWeirdCases` | `^@beta` cannot drag a stable consumer (W208); `^@beta++1` brings it onto the train; a multi-package direct transition graduates the whole train; the graduated train converges. |
| `TestPlanPropagatedGraduationTransitionIsSuppressed` | **Regression fence, finding #2** (below). |

### Goal 4 — config, login, originals (`config_test.go`)

| Test | Claim proven |
|---|---|
| `TestConfigUnknownKeyIsRejected` | A typo'd top-level key **and** a legacy flat space key (`buildScript`, pre-`run` schema) both fail the run (exit 1) instead of being silently ignored. |
| `TestConfigConcurrencyFlagOverridesFile` | `--concurrency` beats the file value *at runtime*: measured overlap, not parsed config, is the evidence. |
| `TestConfigCustomShellIsUsed` | `"shell": ["/bin/bash", "-c"]` actually switches the interpreter (a bashism invalid under `/bin/sh` succeeds). |
| `TestConfigLoginOncePerSpaceAcrossSpaces` | Two spaces sharing one login *script text* log in once **each** — the gate is keyed by space, not by script. |
| `TestConfigLoginFailureIsolatedToItsSpace` | A failing login fails every publish of its space and none of another space's. |
| `TestConfigOnFailAndOnSkipOutcomeScripts` | In one failing run: `run.onFail` fires once for the failed package with `DISPAT_FAILED_STAGE`/`DISPAT_ERROR`, `run.onSkip` once for the blocked consumer with `DISPAT_BLOCKED_BY`, neither for the package that published — and an onFail sequence whose first command fails still runs to the end (warn-only). |
| `TestConfigNonPackageScopesReplacesDefault` | Setting `nonPackageScopes` **replaces** the `["release"]` default: the custom scope becomes exempt, `release` stops being exempt. |
| `TestConfigFusedPrereleaseTagFormatRoundTrips` | `{name}@v{version}-{channel}{counter}`: `beta0` is written, read back, converges, and the counter continues to `beta1` over three runs. |
| `TestConfigRevertOnFailAppliesAfterVersionStageOnSkip` | The skip-after-version-stage rollback: the consumer's version script dirties its folder, the provider's publish fails, and the skipped consumer's folder is restored. |
| `TestConfigGithubReleasePrereleaseFlagFollowsChannel` | Against an httptest GitHub API: the same package's releases flip `prerelease: true -> false` across a real beta release and its graduation. |

## Findings

Two behaviours contradict a reasonable reading of the documentation. Both are pinned as regression fences with
comments marking them as *observed*, not endorsed, so a fix will fail exactly one clearly-labelled test each:

1. **A rejected `Release-As` pin swallows a sibling unit's bump** (`TestPlanRejectedPinSwallowsAnAccompanyingBump`)
   — §16's unit-scoped blast radius suggests a rejected pin should leave a sibling `feat` unit's release intact;
   instead the package publishes and tags its unchanged baseline version, silently dropping the feature release.
2. **A propagated graduation transition never graduates the dependant**
   (`TestPlanPropagatedGraduationTransitionIsSuppressed`) — `channel.go` documents transitions as the deliberate
   exception allowed to graduate dependants, and `configuration.md`'s worked example
   `release(core)@beta>stable@@beta>stable++*` relies on it, but the propagation call site resolves every
   propagated value with `graduates=false`, so the dependant stays on the train (W200/W206).

## Running

```sh
cd tests/integration
go test ./...            # requires git and the go toolchain on PATH
go test ./... -race      # also clean
```

Binaries are built once per `go test` invocation and shared across all tests. Each test creates its repository
in a fresh `t.TempDir()`, so tests are independent and safe to run in any order or subset.

## Conventions for new tests

- Assert against JSON events (`res.Events`, `harness.HasCodeForPackage`) and git state — never against pretty
  log text. Prefer `HasCodeForPackage` over `HasCode` whenever the diagnostic names a package.
- Reuse the shared fixtures in `helpers_test.go` (`singlePackageRepo`, `linkedRepo`, `markerBuild`/`buildRuns`
  for scripts-ran-according-to-plan claims) and close config literals with `harness.Base(concurrency)`. A config
  exercised by exactly one test stays next to that test, written out in full — the config is the test input, and
  hiding it behind a builder would obscure what is being exercised.
- `r.ReleaseOK()` / `r.StatusOK()` for runs that must succeed; plain `Release()`/`Status()` plus an explicit
  code assertion where a non-zero exit is the point.
- `HasTag` is an exact match; "was this package tagged at all" is `TagCount("pkg@")` — an exact match against a
  bare prefix passes vacuously.
- Use `tsmark` for any claim about *when* something ran; `AssertSequential` for claims about *order*
  (structural, flake-free); reserve `AssertOverlaps`/`AssertConcurrencyBudget`, with generous sleeps, for claims
  that genuinely require overlap.
- One flowing multi-run scenario per behaviour cluster: each extra run in an existing scenario is far cheaper
  than a new fixture, and convergence ("run it again, nothing happens") is itself worth asserting at the end of
  most scenarios.
- `harness.Base` already disables GitHub; a config written in full repeats `"github": {"enabled": false}` unless
  the GitHub recorder is the thing under test.
