# Integration test plan

This module (`tests/integration`) is the black-box integration suite for the dispat CLI. It compiles the real binary
from `services/dispat`, drives it against disposable git repositories exactly as a user's shell would, and asserts on
the three outputs a release run actually has: **git state** (tags, commits, file contents), **JSON log events**
(`--log-format json`, the machine-readable contract CI ingests), and, where *timing* rather than mere ordering is the
claim, **nanosecond-resolution execution timelines** recorded by a purpose-built probe.

## Goals

The suite was designed against twelve goals, one test file each:

1. **Concurrency** (`concurrency_test.go`): stable tests *guaranteeing* the budgets work. With concurrency 4 and five
   packages, the fifth's work starts exactly after one of the first four finishes; independent packages are picked up
   concurrently while dependants are awaited.
2. **Execution order by dependency graph** (`order_test.go`): scripts run in the order the graph dictates, under both
   `isBuildWaitingPublish` settings.
3. **Plan logic** (`plan_test.go`): prereleases, cancels, holds, catch-up, provider-failed and consumer-failed runs, and
   as many weird cases as earn their keep, including that scripts execute *according to* the plan (a held or cancelled
   package runs nothing; a resumed one runs exactly once).
4. **Everything else** (`config_test.go`): config validation and precedence, config file discovery, the
   `commitErrors` policy, initials baselines, the run-level hook frame, login scripts, the `flow.onFail` /
   `flow.onSkip` outcome scripts, GitHub release assets from build-exported attachments, and original cases the unit
   suites cannot witness.
5. **Space versioning modes** (`versioning_test.go`): `independent`, `fixed` and `fixedSparse` spaces driven side by
   side across multiple runs: rides and their "no changes" changelog entries, sparse alignment, the single shared
   prerelease train, failed-ride catch-up, holds/pins under a shared version, and no bleed between modes.
6. **The `dispat run` command** (`run_test.go`): space `runScripts` executed inside changed packages over the dependency
   graph with the full environment, the `dispat <script>` shorthand (including its narrowing to the package it is
   invoked from), the single-package target (`run <script> <package>`), the `--since` selection, the `--on-error`
   skip/continue policies, the concurrency budget (including graph ordering *under* concurrency), cross-package output
   carrying, skipping and error cases.
7. **Release records** (`records_test.go`): the durable artefacts themselves: changelog files accumulating across
   releases above pre-dispat content, annotated tags with their messages and targets, GitHub releases in commit mode,
   and commit mode's release commit, tag placement and push against a real bare remote.
8. **The `init`, `test` and `preview` commands** (`commands_test.go`): the starter config the very next `status`
   can load, one script run inside one package with its full `DISPAT_*` environment, and the pending release notes on
   stdout, for one package and for every pending package at once, narrowing to the fresh changeset across a
   prerelease train.
9. **Repository-scoped fatal errors** (`fatal_test.go`): the §16 bucket that aborts a run whatever `commitErrors`
   says, each constructed for real: a dependency cycle (E200), duplicate version tags (E191), and a shallow clone
   (E196). These are the cases where a partial release would be worst, so each asserts the non-zero exit, the code in
   the events, and that nothing was released or executed.
10. **The `compute` command** (`compute_test.go`): the manifest-derived dependency graph through the binary: the
    detect/apply/check loop with its backup and convergence, `keep` and removal semantics, and the W220 ambiguity
    reaching the JSON events.
11. **Native auto-versioning** (`autoversion_test.go`): manifests rewritten by the binary at the version stage:
    range reconciliation under the match policy, own-version writes, the serialised `syncLock` slot, the
    W192/W197/W203/W221 diagnostics as JSON events across three runs, and `commit.include` staging the regenerated
    root lock file into the release commit.
12. **Per-package overrides, versioning groups and `.dispatignore`** (`overrides_test.go`): the layered configuration
    through the binary: a `packages` entry replacing one flow entry while the sibling keeps the space's, the in-folder
    config file beating the entry (the tag proves it), `.dispatignore` exclusions (never released, unknown scope in
    commits), a declared `versionGroups` group spanning two spaces to one version (with the W210 ride) and its
    convergence, per-package changelog/GitHub record policies, the concurrency weight serialising a heavyweight build
    on the tsmark timeline, the config ascent walking past an in-folder file for the run shorthand, and run scripts
    defined only in an override (both layers).

Configs are authored as **typed models** from the public `pkg/models` module and marshalled to JSON by
`harness.WriteConfigModel`. The schema lives in one place, and a test that compiles is a test whose config loads. The
one shape the model deliberately cannot express, an unknown key, is written as `map[string]any` through
`harness.WriteConfigRaw`, because a typo'd key must be *rejected* at load, not silently ignored into a script-less
release.

It deliberately duplicates as little as possible of the unit suites listed in
[`services/dispat/docs/architecture.md`](../../../services/dispat/docs/architecture.md#testing): those cover each
package against in-memory fakes, and `internal/cli`'s tests cover the controller itself (flags, arities, exit-code
mapping, the init starters). `services/dispat` hosts no end-to-end test at all; this module is the sole home of every
composition claim (the compiled binary, a real scheduler racing real processes, a config file read from disk, exit codes
over a process boundary), so every test here earns its place by asserting something a fake cannot witness.

## Why a separate Go module

- The suite must not import `services/dispat/internal/*`, and as a separate module it structurally *cannot* (Go's
  `internal` rule), which keeps it an honest black box; the one deliberate dispat import is the **public**
  `pkg/models` module, which exists precisely so external tooling can author configs as typed values: if a behaviour is
  not observable through the CLI, tags, files or logs, a test here cannot accidentally reach around and read it off a
  struct. The unit-tested git and shell code is therefore reused the only way a black box can reuse it:
  compiled into the binary under test, driven through the reusable `harness.Repo` git fixture.
- It keeps the slower end-to-end tests out of `go test ./...` for the production modules, while `go.work` makes builds
  and IDE navigation seamless.
- Its only dependency is `testify`, matching the existing test style.

## Architecture

```
tests/integration/
  go.mod                    separate module: github.com/yohimik/dispat/tests/integration
  cmd/tsmark/               the timing probe (see below)
  internal/harness/
    binary.go               builds dispat + tsmark once per test run (sync.Once
                            cache); with DISPAT_COVERDIR set, dispat is built
                            with -cover and every invocation's GOCOVERDIR points
                            there, folding the suite into the coverage badge
    repo.go                 Repo: git fixture (init, seed packages, commit, tags), config
                            writing, Release/ReleaseOK/Status/StatusOK/RunScript/
                            RunScriptOK/Command/CommandAt -> RunResult
    config.go               BaseFile() (concurrency + JSON logs + GitHub disabled),
                            WriteConfigModel (typed model -> dispat.json),
                            WriteConfigRaw (raw map, for shapes the model cannot express)
    events.go               Event: parsed JSON log lines; HasCode / HasCodeForPackage
    timeline.go             Interval: tsmark log parsing; the concurrency assertions
  helpers_test.go           fixtures shared across test files: packageNames,
                            seedIndependentPackages, singlePackageRepo, linkedRepo,
                            libsConfig/buildPublish model builders, the githubFake
                            recording server + decodeAll,
                            markerBuild/failIfMarker scripts, buildRuns
  concurrency_test.go       goal 1
  order_test.go             goal 2
  plan_test.go              goal 3
  config_test.go            goal 4
  versioning_test.go        goal 5
  run_test.go               goal 6
  records_test.go           goal 7
  commands_test.go          goal 8
  fatal_test.go             goal 9
  compute_test.go           goal 10
  autoversion_test.go       goal 11
  main_test.go              TestMain: removes the shared binary build dir at the
                            end of the whole run (a sync.Once cache no t.Cleanup
                            can own)
  docs/test-plan.md         this document
```

### The tsmark probe, and why timing assertions are trustworthy

dispat's own JSON logs carry RFC3339 timestamps with one-second resolution, which cannot distinguish "ran concurrently"
from "ran back to back within the same second". Instead of scraping logs, every timing-sensitive script is wired to
`tsmark`, a dependency-free Go binary that appends `<label> start <unixnano>` / `<label> end <unixnano>`
lines to a shared file (O_APPEND single-write lines, atomic on a local filesystem) and sleeps in between. The scheduler
either launched a process while another was still sleeping or it did not; the file says which, with no reliance on shell
tooling (`date +%N` prints a literal `N` on macOS) or host clocks.

Every concurrency claim is then checked **three independent ways** before it is believed
(`harness.AssertConcurrencyBudget`):

1. a sweep-line max-overlap count,
2. a brute-force O (n²) pairwise overlap count, independently written and required to agree with the sweep, so a
   tie-breaking bug in one cannot quietly agree a real scheduler defect out of existence,
3. a start-order argument: sorted by start, the (budget+1)-th task must not begin before one of the first *budget*
   tasks ended.

And the peak must be **exactly** `min(budget, tasks)`: "at most N" alone is satisfied by a scheduler that serialises
everything, "N reached" alone by one that ignores the limit; only the exact peak proves both halves.

Flakiness posture: *ordering* assertions (`AssertSequential`) are structural. The task graph either has the edge or it
does not, whatever any script's duration, so they cannot flake. *Overlap* assertions are made robust by sleeps (100-400
ms) one to two orders of magnitude above process-launch jitter. The suite passes repeated `-count` runs and
`-race`.

## Coverage matrix

### Goal 1: concurrency (`concurrency_test.go`)

| Test                                                             | Claim proven                                                                                                                                        |
|------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestConcurrencyBuildBudgetEnforced`                             | Budget 4, five independent packages: peak overlap exactly 4; the 5th build starts only after one of the first four ends (three independent checks). |
| `TestConcurrencyPublishBudgetIsIndependentOfBuild`               | Separate stage budgets: in one run, unconstrained builds reach overlap 5 while publishes stay capped at 2.                                          |
| `TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited` | Three independent providers pairwise overlap; their shared consumer's build starts strictly after all three provider builds end.                    |

### Goal 2: execution order by dependency graph (`order_test.go`)

| Test                                                      | Claim proven                                                                                                                                                                              |
|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOrderChainRunsInTopologicalOrder`                    | `base <- mid <- top`: builds and publishes each run in topological order, driven by `dependencies` edges alone.                                                                           |
| `TestOrderBuildWaitsForPublishWhenConfigured`             | `isBuildWaitingPublish: true`: consumer's build starts only after the provider's *publish* ends.                                                                                          |
| `TestOrderBuildDoesNotWaitForPublishByDefault`            | Flag false: consumer's build runs *during* the provider's publish (timing evidence), while the consumer's own publish still waits for it (structural, flag-independent).                  |
| `TestOrderDiamondDependencyConverges`                     | Fan-out/fan-in (`a -> b,c -> d`): `b`/`c` overlap; `d` waits for both, at build and at publish.                                                                                           |
| `TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv` | A `DueTo` consumer runs a version task whose `DISPAT_UPDATED_*` names exactly the live provider; a direct-release package in the *same space with the same versionScript* never runs one. |

### Goal 3: plan logic (`plan_test.go`)

| Test                                                      | Claims proven                                                                                                                                                                                                                                                  |
|-----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestPlanCancelSemantics`                                 | Cancel discards pending work irreversibly (post-cancel fix releases 0.0.1, not 0.1.1); a spent cancel warns (W170); a cancelled/no-op release run executes zero scripts.                                                                                       |
| `TestPlanHoldResumeAndReleaseAsAuto`                      | Hold reports the withheld version (W154) and excludes the package from *execution*, not just tagging (zero script runs while held); resume releases at accumulated `max()` with exactly one build; redundant `auto` warns (W158).                              |
| `TestPlanExactPinGuards`                                  | E153 (not greater), E157 (major jump > 1), E154 (multi-package pin), each in an isolated repo so a rejected pin cannot collide with earlier tags.                                                                                                              |
| `TestPlanRejectedPinFallsBackToTheComputedBump`           | A rejected pin has §16's unit-scoped blast radius: E156 fires, the bad unit contributes nothing, and the sibling `feat` still releases at its computed 0.1.0 (a regression fence; see Regression fences).                                                   |
| `TestPlanConsumerFailureCatchesUpAfterProviderPublished`  | Consumer fails while provider publishes; the next run catches the consumer up at the owed version, labelled W193, provider not re-released; a third run converges.                                                                                             |
| `TestPlanProviderBuildFailureBlocksConsumerThenHeals`     | Provider fails to build; consumer is blocked (W194), never attempted; after the fix both release in one run, with neither W194 nor W193.                                                                                                                       |
| `TestPlanCatchUpWholeHistoryForNeverReleasedConsumer`     | A package created *after* a provider's propagating commit still catches up on its first ever run; an untagged package's window is the whole history.                                                                                                           |
| `TestPlanPrereleaseTrainWeirdCases`                       | `^@beta` cannot drag a stable consumer (W208); `^@beta++1` brings it onto the train; a multi-package direct transition graduates the whole train; the graduated train converges.                                                                               |
| `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` | A propagated `beta>stable` *transition* graduates the dependants still on the named train (the `release(core)@beta>stable@@beta>stable++N` form configuration.md documents), and the graduated train converges (a regression fence; see Regression fences). |
| `TestPlanChannelOnlyReleaseAndEntryPatch` | A release directive that only moves the channel is still a release, explained by W202; entering a prerelease channel with nothing pending takes the §11.4 entry patch, explained by W204, and its scripts execute. |

### Goal 4: config, login, originals (`config_test.go`)

| Test                                                   | Claim proven                                                                                                                                                                                                                                                                                                                                                                                                                  |
|--------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestConfigUnknownKeyIsRejected`                       | A typo'd top-level key fails the run (exit 1) instead of being silently ignored.                                                                                                                                                                                                                                                                                                                                              |
| `TestConfigFileFallbackResolution`                     | Without `--config` the binary resolves the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists (a model-marshalled config under the yaml name loads and plans); with none present it exits 1 with an error naming what it tried.                                                                                                                                                                   |
| `TestConfigResolutionAscendsToTheMonorepoRoot`         | Without `--config`, resolution climbs from `--root` through its parents; a release invoked from `packages/core` tags and writes changelogs exactly as one from the top and converges, while an explicit `--config` never ascends.                                                                                                                                                                                             |
| `TestConfigGitRepositoryGuard`                         | A config with no git repository around it fails `status` with one clear error before any work, and `init` refuses a `--root` that is not a repository root: no raw git errors, nothing written.                                                                                                                                                                                                                              |
| `TestConfigConcurrencyFlagOverridesFile`               | `--concurrency` beats the file value *at runtime*: measured overlap, not parsed config, is the evidence.                                                                                                                                                                                                                                                                                                                      |
| `TestConfigCustomShellIsUsed`                          | `"shell": ["/bin/bash", "-c"]` actually switches the interpreter (a bashism invalid under `/bin/sh` succeeds).                                                                                                                                                                                                                                                                                                                |
| `TestConfigLoginOncePerSpaceAcrossSpaces`              | Two spaces sharing one login *script text* log in once **each**; the gate is keyed by space, not by script.                                                                                                                                                                                                                                                                                                                   |
| `TestConfigLoginFailureIsolatedToItsSpace`             | A failing login fails every publish of its space and none of another space's.                                                                                                                                                                                                                                                                                                                                                 |
| `TestConfigOnFailAndOnSkipOutcomeScripts`              | In one failing run: `flow.onFail` fires once for the failed package with `DISPAT_FAILED_STAGE`/`DISPAT_ERROR`, `flow.onSkip` once for the blocked consumer with `DISPAT_BLOCKED_BY`, neither for the package that published; and an onFail sequence whose first command fails still runs to the end (warn-only).                                                                                                              |
| `TestConfigNonPackageScopesReplacesDefault`            | Setting `nonPackageScopes` **replaces** the `["release"]` default: the custom scope becomes exempt, `release` stops being exempt.                                                                                                                                                                                                                                                                                             |
| `TestConfigFusedPrereleaseTagFormatRoundTrips`         | `{name}@v{version}-{channel}{counter}`: `beta0` is written, read back, converges, and the counter continues to `beta1` over three runs; a second space overriding with the normative format keeps its own spelling: the format is a per-space property.                                                                                                                                                                      |
| `TestConfigRevertOnFailAppliesAfterVersionStageOnSkip` | The skip-after-version-stage rollback: the consumer's version script dirties its folder, the provider's publish fails, and the skipped consumer's folder is restored.                                                                                                                                                                                                                                                         |
| `TestConfigGithubReleasePrereleaseFlagFollowsChannel`  | Against an httptest GitHub API: the same package's releases flip `prerelease: true -> false` across a real beta release and its graduation.                                                                                                                                                                                                                                                                                   |
| `TestConfigGithubReleaseAttachments`                   | The whole script-output path through the real binary: the build exports `DISPAT_EXPORT_GITHUB` (two files, opting the package into the GitHub release) plus an ordinary output into `$DISPAT_OUTPUT`, later stages see the output as `DISPAT_OUTPUT_*` (plus the `DISPAT_OUTPUTS` listing) and the export under its full name, and both files arrive as assets at the endpoint the created release advertised (`upload_url`). |
| `TestConfigParserOptions`                              | The top-level `parser` object drives real parsing: a custom type table makes `docs` release, the configured default propagation depth reaches a consumer with no caret, `strictTypes` raises E140 (tolerated under the default `commitErrors`), and an invalid parser value fails the load with exit 1.                                                                                                                       |
| `TestConfigScriptOutputsCarryAcrossStagesAndHooks`     | The full accumulation contract, hooks included: a `beforeBuild` *hook* export reaches build and publish, the build's export reaches publish (with `DISPAT_OUTPUTS` listing both in export order), and the failing package's `onFail` receives the hook's export **and** what the failed build exported before dying.                                                                                                          |
| `TestConfigCommitErrorsPolicy`                         | A unit-scoped error (E130) under the default `warn` still releases the sibling work; under `error` the release is refused (exit 1, nothing tagged) while `status` still exits 0 and reports the plan.                                                                                                                                                                                                                         |
| `TestConfigInitialsBaselines`                          | Initials seed the version of a package whose newest tag is unparseable (the pre-last tag is NOT used and the window still starts at the broken tag), an unmatched initials key only warns, and the next release reads the new real tag back.                                                                                                                                                                                 |
| `TestConfigRunLevelHooks`                              | The run-level hook frame against a real remote: every hook fires in phase order in the monorepo root, postAll sees the run outcome and the workspace listing, and a quiet second run keeps the commit/push hooks off because their phases never happen.                                                                                                                                                                       |
| `TestConfigRunLevelHookFailureSemantics`               | A failing warn-only run hook (postAll) does not fail the run and its sequence continues; the gating beforeAll aborts the run before any release work.                                                                                                                                                                                                                                                                         |

| `TestConfigFormatsSmoke`                               | One minimal smoke per config format: the same monorepo releases through the binary under dispat.json, dispat.yaml and the init command's dispat.toml starter; the json leg also pins that `status` reports without tagging.                                                                                                                                                                                                    |
| `TestConfigAllStageHooksFireInOrder`                   | Every one of the nine per-package hooks plus the announce stage fires, in the documented frame order, on a provider/consumer pair; the consumer additionally runs the version stage with its two hooks inside the same frame.                                                                                                                                                                                                 |
| `TestConfigStageHookAuthoritySplit`                    | The documented authority split: failing postPublish and the whole announce frame only warn (exit 0, tag exists), while a failing gating hook (postBuild) fails the package, tags nothing, and fires onFail with the stage that carried the failure.                                                                                                                                                                            |

### Goal 5: space versioning modes (`versioning_test.go`)

| Test                                                   | Claim proven                                                                                                                                                                                                                                                                     |
|--------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningFixedSpaceLifecycle`                    | Four runs over a fixed space next to an independent one: a change to either member releases both at one version (W210 on the rider, "no changes" changelog entry, no leaked notes), quiet runs converge, and the independent space never moves with any of it.                    |
| `TestVersioningFixedSparseLifecycle`                   | Sparse across four runs: only changed members release (no W210), an unchanged member keeps its version, its first change jumps it to the space version, and a joint change lands both on one shared next version.                                                                 |
| `TestVersioningThreeModesSideBySide`                   | One commit through fixed + sparse + independent spaces at once: each mode moves exactly its own set, the independent newcomer versions from its own history (`0.0.1`, not the space-aligned `0.1.1`), and all three converge together.                                            |
| `TestVersioningFixedSharedPrereleaseTrain`             | A fixed space runs a *single* train: one member's `@beta` takes the whole space to `beta.0`, later work continues it to `beta.1` for both, one member's graduation ends it for both, and the graduated space converges.                                                           |
| `TestVersioningFixedRideFailureThenAlignmentCatchUp`   | A ride can fail like any release: the changed member publishes, the rider fails, and the next run aligns the rider at exactly the space's published version (W210) without re-releasing anyone; a third run converges.                                                            |
| `TestVersioningCrossSpaceDependencyIntoFixedSpace`     | A caret from an independent provider into one fixed-space member: the member gets an ordinary DueTo release (version task, `DISPAT_UPDATED_*`), its space mate rides to the same version with no version task; edges stay package-scoped where versions are space-scoped.         |
| `TestVersioningFixedHoldAndResume`                     | `Release-As: none` on one member keeps only it back; the resume aligns it to the space's published version.                                                                                                                                                                      |
| `TestVersioningFixedExactPinMovesTheSpace`             | An exact pin naming one member moves the whole space to the pinned version; the pin guards (E153) keep applying to the shared version afterwards.                                                                                                                                |
| `TestVersioningFixedSpaceExecutesEveryMemberScript`    | A ride is a full release at the execution level: build scripts run for the rider too.                                                                                                                                                                                            |
| `TestVersioningFixedConflictResolutions`               | The two fixed-space conflict warnings: competing exact pins resolve to the newest with W211 (the loser must not also release), and members resolving to different channels release as one channel with W212.                                                                      |

### Goal 6: the `dispat run` command (`run_test.go`)

| Test                                               | Claim proven                                                                                                                                                                                                                                                                                                  |
|----------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestRunExecutesChangedPackagesInTopologicalOrder` | The script runs once per changed package of the defining space, providers before consumers; a space without the name is skipped; nothing is tagged.                                                                                                                                                           |
| `TestRunShorthandCommand`                          | `dispat lint` is `dispat run lint` when the word is not a command name, and it runs the script rather than releasing.                                                                                                                                                                                         |
| `TestRunReceivesTheFullPackageEnvironment`         | The run script sees the stage environment (`DISPAT_PACKAGE`, `DISPAT_NEW_VERSION`, `DISPAT_TAG`, the workspace listing) with `DISPAT_STAGE=run:<name>`.                                                                                                                                                       |
| `TestRunUnknownScriptFails`                        | A name no space defines exits 1 instead of silently running nothing.                                                                                                                                                                                                                                          |
| `TestRunOnErrorPolicies`                           | Under the default `--on-error=skip` a failed provider's dependents are skipped; under `continue` they still run; both exit 1; an unknown policy is a usage error (exit 2).                                                                                                                                    |
| `TestRunConcurrencyBudget`                         | Independent packages' scripts overlap under `--concurrency 3` (measured, three independent checks) and serialise under the config's budget of 1.                                                                                                                                                              |
| `TestRunGraphOrderingUnderConcurrency`             | Both scheduling promises in one graph: three providers' scripts pairwise overlap at the full budget while their shared consumer's script never starts before every provider's ended.                                                                                                                          |
| `TestRunCarriesOutputsAcrossPackages`              | A provider's `$DISPAT_OUTPUT` export (written with the `DISPAT_OUTPUT_` prefix spelling) reaches its consumers as `DISPAT_OUTPUT_<NAME>` with `DISPAT_OUTPUT_SOURCE_<NAME>` naming the exporting script, transitively, through a middle package whose space defines no script at all.                         |
| `TestRunCarriesOutputsFromAFailedProvider`         | Under `--on-error continue` a failed provider's dependents still run and still receive what the failed script exported before dying.                                                                                                                                                                          |
| `TestRunSkipsUnchangedPackages`                    | After a release nothing is changed and the script runs zero times; a fresh change narrows the run to exactly the changed package.                                                                                                                                                                             |
| `TestRunInFixedSpaceIncludesRides`                 | In a fixed space a ride is a changed package, so the run script executes in every member.                                                                                                                                                                                                                     |
| `TestRunTargetsANamedPackage`                      | `run <script> <package>` runs exactly the named package (changed or not, no graph) and errors on an unknown package or one whose space does not define the script.                                                                                                                                          |
| `TestRunShorthandNarrowsToTheInvokedPackage`       | `dispat <script>` from inside a package folder (or a nested subdirectory) runs only that package, riding the config ascent; from the monorepo top it still covers every changed package.                                                                                                                      |
| `TestRunSinceSelectsByCommitScopes`                | `--since HEAD~1` narrows the run to what the last commit addressed (the written scope wins over the changed files, a scopeless unit derives from its files, §6.2), `-s all` selects every package, an unknown revision exits 1, and combining `--since` with an explicit package is a usage error (exit 2). |

### Goal 7: release records (`records_test.go`)

| Test                                                              | Claim proven                                                                                                                                                                                                                                                                                                                                                   |
|-------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestRecordsChangelogAccumulatesAcrossReleases`                   | Entries prepend newest first under one never-duplicated title; a changelog that predated dispat keeps its content below every generated entry; a multi-unit commit groups its sections by bump (Breaking Changes above Fixes, run 1's unit staying in its own entry); a consumer's entry carries the provider's version *movement* (`- core: 0.1.0 -> 1.0.0`). |
| `TestRecordsChangelogCustomFileTitleAndSections`                  | `changelog.file`, `changelog.title` and the section-title options change the artefact on disk, and the default `CHANGELOG.md` is not written next to the configured file.                                                                                                                                                                                      |
| `TestRecordsTagsAreAnnotatedWithReleaseMessages`                  | A release tag is an annotated tag *object* (`cat-file -t` = `tag`), its message is `release <tag>`, and it peels to the commit that was released.                                                                                                                                                                                                              |
| `TestRecordsReleaseCommitTagsAndPush`                             | Commit mode against a real bare remote: one `chore(release): ...` commit carrying every published changelog, tags placed on that commit (not the source commit), branch + tags actually on the remote after the push, and a re-run converging because the release-commit scope is exempt by default.                                                           |
| `TestRecordsCommitModeLeavesHistoryUntouchedWhenNothingPublished` | A run where every package fails leaves the history exactly as it was: no release commit, no tags.                                                                                                                                                                                                                                                              |
| `TestRecordsPushSkipsExistingRemoteTags`                          | A tag already present on the remote (a partially pushed earlier run) is skipped while the branch, the release commit and every new tag still arrive; the pre-existing remote tag keeps its original target.                                                                                                                                                    |
| `TestRecordsExportedPackageCommitPinsTheTag`                      | A release script exporting `PACKAGE_<KEY>=<commitHash>` pins its package's tag to that commit; the release commit still carries the changelog on top.                                                                                                                                                                                                          |
| `TestRecordsExportedCommitExcludesTagFromReleaseCommitAndPushes`  | Mixed run: only the exporting package's tag moves to the exported commit and no tag for it is created on the release commit (`tag --points-at` names only the space mate's); the push delivers both tags with their targets intact.                                                                                                                            |
| `TestRecordsExportedCommitPinsTagOutsideCommitMode`               | Without commit mode the export redirects the tag from HEAD to the exported commit: the tag lands there, no tag exists at HEAD, and HEAD does not move.                                                                                                                                                                                                         |
| `TestRecordsPushVerifyDisabled`                                   | `commit.verify=false` switches the upfront ls-remote check off: the release work happens and only the push itself fails; asserted against the default in the same test, which fails fast before any work (no tags, no changelog).                                                                                                                             |
| `TestRecordsChangelogDisabled`                                    | `changelog.enabled=false` switches the file recorder off without touching anything else: the release still publishes and tags, no changelog appears.                                                                                                                                                                                                           |
| `TestRecordsCommitModeGithubFinalize`                             | GitHub in commit mode: releases created in the finalize phase, the body documenting the exact commit and tag, the recorder opt-in per package (no export, no release), a `PACKAGE_<KEY>` export overriding commit and `target_commitish`, and `commit.messageFormat` rendering `{packages}`/`{tags}`.                                                          |

### Goal 8: the init, test and preview commands (`commands_test.go`)

| Test                                | Claim proven                                                                                                                                                                                                                                                                                  |
|-------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestCommandsInitThenStatusCompose` | `dispat init --format toml` then a plain `dispat status`: the fallback finds `dispat.toml` with no `--config` anywhere, the starter config loads and discovers the package, and a second `init` refuses to overwrite (exit 1).                                                                |
| `TestCommandsTestScript`            | `dispat test <script> <pkg>` runs the script inside the package folder with the full `DISPAT_*` environment (`DISPAT_NEW_VERSION`, `DISPAT_STAGE=test:<name>`) and releases nothing; unknown script, unknown package and a failing script all exit 1.                                         |
| `TestCommandsPreviewNotesWindowing` | `dispat preview <pkg>` prints the pending notes (header, sections, entries), reports "no pending changes" once released, errors on an unknown package; and across a prerelease train the preview and each entry narrow to the fresh changeset while the graduation collects the whole train. |
| `TestCommandsPreviewAllPackages` | `dispat preview` with no package renders every package with something pending in publish order, keeps quiet packages out, reports "no pending changes" once nothing is pending, and rejects more than one argument (exit 2). |

### Goal 9: repository-scoped fatal errors (`fatal_test.go`)

| Test                             | Claim proven                                                                                                                                                                                              |
|----------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFatalDependencyCycle`       | A cyclic dependency graph loads as config but refuses to plan: exit 1, E200 in the events, no tag, no script, and `status` refuses too.                                                                   |
| `TestFatalDuplicateVersionTags`  | Two reachable tags parsing to the same version of one package on different commits (`core@0.1.0` next to a hand-planted `core@0.1.0+dup`) make the baseline ambiguous: exit 1, E191, pending work unreleased. |
| `TestFatalShallowRepository`     | A `git clone --depth 1` of the repository refuses to release: exit 1, E196 in the events, instead of silently planning over truncated history.                                                             |

### Goal 10: the compute command (`compute_test.go`)

| Test                                  | Claim proven                                                                                                                                                                                                 |
|---------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestComputeDetectApplyStatus`        | The full loop through the binary: a preview writes nothing, `--check` exits 1 on drift, `--write` applies with a byte-identical `.backup`, the next `status` orders by the new edge, and `--check` converges. |
| `TestComputeKeepAndRemoval`           | A stale edge is suggested for removal; `keep: true` silences it, survives `--write`, and the config still loads.                                                                                             |
| `TestComputeAmbiguousNameReportsW220` | Two packages declaring one manifest name derive no edges and the ambiguity reaches the JSON events as W220.                                                                                                  |

(The command's finer grain, meaning cross-ecosystem matching, interactive selection, the TOML snippet fallback,
stale-endpoint removals and error paths, is unit-tested in `services/dispat/internal/app`, where each case is one in-memory monorepo
away instead of one binary invocation.)

### Goal 11: native auto-versioning (`autoversion_test.go`)

| Test                                        | Claim proven                                                                                                                                                                                                                                              |
|---------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoVersionReleaseRewritesManifests`   | A `workspace:*` range is reconciled to the provider's released version, a hand-pin outside the match globs survives, both own versions advance, and the syncLock snapshot proves it ran after the rewrite.                                                 |
| `TestAutoVersionSyncLockSerialised`         | Several packages' syncLock scripts never overlap under the default budget of 1 while builds keep the build budget: the corrupted-shared-lockfile guard over the real scheduler.                                                                           |
| `TestAutoVersionDiagnosticsAndCommitInclude` | Three runs: W221 for a rewritten edge with no configured counterpart (and `commit.include` staging the regenerated root package-lock.json into the release commit); W192+W197 after the manifest was hand-edited backwards; W203 when the provider goes to beta under a stable consumer. All asserted as JSON events per package. |

### Goal 12: per-package overrides, versioning groups and `.dispatignore` (`overrides_test.go`)

| Test                                          | Claim proven                                                                                                                                                                                                                                              |
|-----------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOverridesFlowBuildPerPackage`            | A `packages` entry replaces one flow entry for one package alone: the override's build runs for it, the space's for the sibling, the un-named stages inherit (both publish and tag), and a second run converges.                                          |
| `TestOverridesInFolderFileWins`               | The package folder's own dispat.json is the most local layer — its `tagFormat` beats the `packages` entry's, proven by the tag the release actually creates, while the sibling keeps the repository default.                                              |
| `TestOverridesDispatignore`                   | A folder listed in `.dispatignore` is not a package: never released, and a commit scoping it draws the unknown-scope diagnostic (E130) like any non-package name.                                                                                         |
| `TestOverridesVersionGroupSpansSpaces`        | A declared `versionGroups` group joined by two spaces versions as one: a change in one space rides the other space's package to the same version (W210 on the rider), and aligned members converge on the next run.                                       |
| `TestOverridesPerPackageRecords`              | Record policies resolve per package: one package writes its changelog under an overridden file name, its sibling disables both records, and the GitHub fake receives exactly the enabled package's release.                                               |
| `TestOverridesPackageConcurrencyWeight`       | A package whose `concurrency` equals the build budget occupies it whole: its build overlaps no other build on the tsmark timeline while the ordinary packages stay free to overlap each other.                                                            |
| `TestOverridesRunShorthandFromPackageFolder`  | The config ascent walks past the package's own (spaces-less) override file to the monorepo root, so the run shorthand keeps working from inside a package folder that carries one.                                                                        |
| `TestOverridesRunScriptOnlyInPackage`         | A run script defined only in a package's in-folder file (found through discovery) or only in a `packages` entry (found in the loaded config) runs in that package alone; a name defined nowhere stays a hard error.                                       |

## Regression fences

Two planner behaviours are subtle enough to earn dedicated guard tests: each pins a property whose violation once
produced a plausible-looking but wrong plan, so a regression fails exactly one clearly-labelled test.

1. **A rejected `Release-As` pin must not swallow a sibling unit's bump.** Every pin guard reports its error and falls
   back to the ordinarily computed version (§16's unit-scoped blast radius): the sibling releases at its computed
   version, and a lone rejected pin releases nothing. Guarded by `TestPlanRejectedPinFallsBackToTheComputedBump` (and
   unit tests in `internal/plan`). The failure mode being fenced: a package publishing and tagging its unchanged
   baseline while silently dropping a `feat` that shared the commit with the bad pin.
2. **A propagated graduation transition must graduate the dependant.** Transitions bypass the graduation guard (a
   propagated *bare* `stable` is still suppressed), so `release(core)@beta>stable@@beta>stable++*` ends the whole
   train. Guarded by `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` (and unit tests in `internal/plan`).
   The failure mode being fenced: dependants left on the train (W200/W206) by the exact form the configuration page
   documents for ending one.

## Running

```sh
cd tests/integration
go test ./...            # requires git and the go toolchain on PATH
go test ./... -race      # also clean
```

Binaries are built once per `go test` invocation and shared across all tests. Each test creates its repository in a
fresh `t.TempDir()`, so tests are independent and safe to run in any order or subset.

## Conventions for new tests

- Assert against JSON events (`res.Events`, `harness.HasCodeForPackage`) and git state, never against pretty log text.
  Prefer `HasCodeForPackage` over `HasCode` whenever the diagnostic names a package.
- Author configs as `pkg/models` values starting from `harness.BaseFile(concurrency...)` and write them with
  `r.WriteConfigModel(cfg)`; fall back to `harness.WriteConfigRaw` only for shapes the model cannot express (unknown
  keys). Reuse the shared fixtures in `helpers_test.go` (`singlePackageRepo`, `linkedRepo`,
  `libsConfig`, `markerBuild`/`buildRuns` for scripts-ran-according-to-plan claims). A config exercised by exactly one
  test stays next to that test, written out in full: the config is the test input, and hiding it behind a builder would
  obscure what is being exercised.
- `r.ReleaseOK()` / `r.StatusOK()` for runs that must succeed; plain `Release()`/`Status()` plus an explicit code
  assertion where a non-zero exit is the point.
- `HasTag` is an exact match; "was this package tagged at all" is `TagCount("pkg@")`, because an exact match against a
  bare prefix passes vacuously.
- Use `tsmark` for any claim about *when* something ran; `AssertSequential` for claims about *order* (structural,
  flake-free); reserve `AssertOverlaps`/`AssertConcurrencyBudget`, with generous sleeps, for claims that genuinely
  require overlap.
- One flowing multi-run scenario per behaviour cluster: each extra run in an existing scenario is far cheaper than a new
  fixture, and convergence ("run it again, nothing happens") is itself worth asserting at the end of most scenarios.
- `harness.BaseFile` already disables GitHub; a test overriding the `GitHub` field (the recorder tests) is the only
  place that re-enables it, always against an httptest server.
