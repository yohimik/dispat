# Integration test plan

This module (`tests/integration`) is the black-box integration suite for the dispat CLI. It compiles the real binary
from `services/dispat`, drives it against disposable git repositories exactly as a user's shell would, and asserts on
the three outputs a release run actually has: **git state** (tags, commits, file contents), **JSON log events**
(`--log-format json`, the machine-readable contract CI ingests), and, where *timing* rather than mere ordering is the
claim, **nanosecond-resolution execution timelines** recorded by a purpose-built probe.

## Goals

The suite was designed against twenty-one goals, one test file each:

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
5. **Space versioning modes** (`versioning_test.go`): all seven modes driven side by side across multiple runs:
   `independent`, the full-version `fixed`/`fixedSparse`, and the partial `fixedMajorMinor`/`fixedMajor` pairs that
   hold only a prefix in common. Rides and their "no changes" changelog entries (naming the part that is shared),
   sparse alignment, versions diverging again below the shared part, the single shared prerelease train against a
   train that stays local, failed-ride catch-up, holds and pins under a shared version against pins that stay inside
   it, mixed shared depths in one group, and no bleed between modes.
6. **The `dispat run` command** (`run_test.go`): a script executed inside changed packages over the dependency graph
   with the full environment, resolved per package through the three `scripts` levels (package, space, file) so the
   level a name is defined at decides what the run covers, the `dispat <script>` shorthand and the two-word
   spelling narrowing alike to the folder they are invoked from, the `--package`/`--space` selection, the `--since`
   selection and how the two compose (a window is picked, the filter narrows it), the
   `--consumers` expansion (transitive dependents of the window, full members of the skip cascade), the `--on-error`
   skip/continue policies, the concurrency budget (including graph ordering *under* concurrency), cross-package output
   carrying, skipping and error cases.
7. **Release records** (`records_test.go`): the durable artefacts themselves: changelog files accumulating across
   releases above pre-dispat content, annotated tags with their messages and targets, GitHub releases in commit mode,
   and commit mode's release commit, tag placement and push against a real bare remote.
8. **The `init` and `preview` commands** (`commands_test.go`): the starter config the very next `status` can load, and
   the pending release notes on stdout, for one package and for every pending package at once, narrowing to the fresh
   changeset across a prerelease train.
9. **Repository-scoped fatal errors** (`fatal_test.go`): the §16 bucket that aborts a run whatever `commitErrors`
   says, each constructed for real: a dependency cycle (E200), duplicate version tags (E191), and a shallow clone
   (E196). These are the cases where a partial release would be worst, so each asserts the non-zero exit, the code in
   the events, and that nothing was released or executed.
10. **The `compute` command** (`compute_test.go`): everything the binary derives from the manifests. The dependency
    graph: the detect/apply/check loop with its backup and convergence, `keep` and removal semantics, and the W220
    ambiguity reaching the JSON events. The baselines: `initials` entries seeded from the versions the manifests
    declare, against real tags, since which packages need one is a question only git can answer.
11. **Native auto-versioning** (`autoversion_test.go`): files reconciled by the binary at the version stage, under
    either of the two strategies or neither. The parsing one: range reconciliation under the match policy,
    own-version writes, the W192/W197/W203/W221 diagnostics as JSON events across three runs, and `commit.include`
    staging the regenerated root lock file into the release commit. The replacing one: literal substitution over a
    Gradle build script and a README with nothing parsed at all, binary files skipped rather than corrupted, and W222
    for a rule that matched nothing. Plus the serialised `syncLock` slot, which keeps its budget even when neither
    strategy is configured and it is the whole of the version stage, and `manifestNames` making a package with no
    readable identity visible to `compute` and to auto-versioning through the one index they share.
12. **Per-package overrides, versioning groups and `.dispatignore`** (`overrides_test.go`): the layered configuration
    through the binary: a `packages` entry replacing one flow entry while the sibling keeps the space's, the in-folder
    config file beating the entry (the tag proves it), `.dispatignore` exclusions (never released, unknown scope in
    commits), a declared `versionGroups` group spanning two spaces to one version (with the W210 ride) and its
    convergence, per-package changelog/GitHub record policies, the concurrency weight serialising a heavyweight build on
    the tsmark timeline, the config ascent walking past an in-folder file for the run shorthand, `scripts` defined at
    each of the three levels (both override layers included), and one `flow.build` resolving to a different command per
    package.
13. **The top-level `packages` section: standalone packages and package dependencies** (`packages_test.go`): a
    `packages` entry with a `path` releasing as a full package outside every space (own flow, tag, changelog, and
    convergence), the standalone path config errors (escaping the root, naming no folder), provider lists declared in a
    `packages` entry or an in-folder config file ordering the graph like top-level edges, and `dispat compute`
    editing each declaration where it lives: removals applied to the declaring package config (in-folder file or the
    nested entry list, each with its own backup) while manifest-detected additions land in the root list.
14. **Interruption** (`interrupt_test.go`): a SIGINT mid-run shuts the run down gracefully through the real signal
    handler: the in-flight script is killed, remaining packages report `cancelled` rather than `failed` or `skipped`,
    nothing is tagged for work that did not finish, and the next run releases the cancelled packages at the version they
    were owed.
15. **The standalone step commands** (`standalone_test.go`): `dispat changelog`, `dispat autoversion` and
    `dispat commit` through the binary: the shared package selection (`--package`/`--space` terms, folder-narrowed,
    root-wide), the
    W222 changelog idempotence, the in-flow scenario where nested step commands land the changelog inside the tagged
    commit and the outer run skips the pre-written entry (W222) and the pre-created tag (W223), the `--tag`/`--push`
    committer identity and remote delivery, the `DISPAT_OUTPUT` commit pin export, and autoversion's reconcile-once
    plus syncLock-once convergence.
16. **The manifest commands** (`manifests_test.go`): `dispat scanner`, `dispat writer` and `dispat replacer`, the
    `pkg/scanner` and `pkg/writer` libraries exposed as commands. What only the binary can witness: that they run with
    no config file, no commit and no plan at all; that the folder and file paths a shell hands them resolve against
    `--root`; that the scanner's partial-result contract and the three writer and replacer outcomes reach the process
    exit code, plainly and under `--strict`; that a format-preserving rewrite really does leave every other byte alone
    on disk; that the replacer replaces every occurrence and chains its substitutions in the order they were given;
    that the scanner reads back what the writer just wrote; and that the three command words did not cost the run
    shorthand a script of the same name.
17. **The `--package` / `--space` / `--group` selection** (`filter_test.go`): the one selection every package command
    shares, through the binary: name, comma-separated, repeated, glob and case-insensitive terms; a space term staying
    inside its space; a group term crossing spaces and reaching a standalone package that joined it, while reaching no
    package that versions on its own; a standalone package belonging to no space and reachable only through
    `--package` or its group; an unmatched term failing with the mirror hint that names the flag that would reach it; the invocation folder standing in for the terms nobody
    typed (a package folder, a nested subfolder, a space folder, the root, outside everything, and the deepest match
    winning over an enclosing one) while an explicit term always beats it; the filter narrowing a window and never
    widening it (`--since all` being what reaches an unchanged package) and composing with `--consumers`; the same
    terms and folder inference on `preview`, the step commands and `compute`, whose suggestions are scoped to the
    selected consumers while detection still reads every package's manifests; the same selection on `release` and
    `status`, where publish order additionally withholds a consumer whose provider was left out (`W230`), a split
    versioning group is warned about and released (`W231`), naming the group instead of its members releases it whole
    and cannot split it, and `--strict` refuses either before anything is built; and a positional package name now
    being a usage error.
18. **Docker through the binary** (`docker_test.go`): the ecosystem dispat was built around and the last one it could
    read. What only a real run can show: that `compute` derives an image-to-image edge from a `FROM` line nobody wrote
    into the config; that a release reconciles the consumer's `FROM` and `COPY --from` tags and a compose file's
    `image` and `build.tags` to the versions it has just computed, the package's own version in the service it builds
    and its provider's in the service it pulls; that a build stage and a port mapping are left alone; and that the
    repository a Docker manifest declares — never a folder name — reaches the workspace index the planner and the
    writer share.
19. **The `autoreplace` command** (`autoreplace_test.go`): `dispat writer`'s edits applied to the packages the plan
    selects. What only the binary can witness: that the manifests it writes are the ones it found by scanning each
    covered package rather than any it was told about; that the edits land byte for byte, leaving every other byte of
    the fixture alone, and converge on a second pass; that `{version}` resolves against the plan the binary has just
    computed, for the package an edit names and for the covered package's own version field, and that a placeholder
    naming no package of the workspace is refused before a file is opened; that `--manifests root` stops at the package
    folder while `all` descends without stamping a nested manifest's own version; that `--only-updated` follows the
    plan, so the same command is a clean no-op once everything it named has been released; that `--strict` is asked
    across the whole sweep rather than per file; that `--replace` adds and removes a redirect; that the syncLock
    scripts run exactly where a manifest changed and `--sync-lock=false` skips them; and that every refusal (a stale
    edit, a malformed spec, an unknown scope, nothing to write, a positional argument) reaches the process exit code.

20. **Self-update** (`selfupdate_test.go`): dispat replacing its own binary, which is the one thing no other area can
    witness, because it is the one command that overwrites the file it is running from. Two binaries are built at two
    versions and a fake releases API hands one out: the old one downloads it, checks it against the published size and
    checksum, runs it, and steps aside for it, and the same path then answers with the new version while the old one
    waits beside it. Then the rest of the surface, all through the process boundary: `--check` changing nothing and
    exiting 1 because there is something to install; a second run reporting it is already current and `--force`
    installing anyway; `--release` reaching a named version including backwards and refusing one nobody published;
    `--rollback` restoring the backup and rotating, so a second rollback returns and the directory is never left
    holding a parked file; a checksum that does not describe what arrived refusing with the working binary untouched
    and no backup created; a release with no binary for this platform naming the ones it has; prereleases passed over
    until `--prerelease` asks for them and `--force` being the way back off that line; the backup surviving six days
    and being cleared by the next command after eight; and the notice riding out on an ordinary command, staying out of
    JSON output, and not being made at all when the configuration says no.

21. **The release guards** (`guard_test.go`): the two refusals that stop a release before it starts.
    `run.allowBranch` turns a branch list into a precondition, so a run from a branch outside it exits 1 naming both the
    branch and the patterns, with nothing built or tagged, while `dispat status` keeps working anywhere and a `*` glob
    reaches slashed names. The push-mode behind-remote check compares the checkout against the branch it would push to
    and refuses a stale one, because that plan was computed from tags another clone has already moved past. The pair is
    also proven to be off unless asked for, and to sit behind `commit.verify` — the run that skips the check is carried
    all the way to its rejected push, which is the wasted release the check exists to prevent.

22. **The shell helpers** (`if_test.go`, `exec_test.go`): the two commands that run one script instead of sweeping a
    selection. `dispat if` picks a shell string from a condition on the environment, so the same invocation answers
    three ways in three environments; every spelling of the grammar is driven through the process boundary, the chosen
    script's exit code becomes the command's, `--on-failure` replaces it, and a branch is ordinary shell text so
    another dispat command nests inside one. `dispat exec` runs one *declared* script, where one subject decides both
    which level is read and whose environment the script gets: the exact mode refuses a name from a level nobody
    asked about, `--fallback` walks the layers the way `dispat run` does with the nearer level still winning, and
    `--script-from` crosses the text and the context without the environment following. The pair's load-bearing claim
    is that a declared script reading `DISPAT_*` becomes runnable outside a release: `--env both` computes the plan on
    demand, the default scope computes none at all (proven by taking the repository away), and inside a `run` script
    the variables arrive by inheritance with no flag.

Configs are authored as **typed models** from the public `pkg/models` module and marshalled to JSON by
`harness.WriteConfigModel`. The schema lives in one place, and a test that compiles is a test whose config loads. The
one shape the model deliberately cannot express, an unknown key, is written as `map[string]any` through
`harness.WriteConfigRaw`, because a typo'd key must be *rejected* at load, not silently ignored into a script-less
release.

It deliberately duplicates as little as possible of the unit suites listed in
[`Architecture`](https://yohimik.github.io/dispat/architecture#testing): those cover each
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
                            RunScriptOK/Command/CommandAt/CommandInput -> RunResult;
                            StartRelease -> Proc (Signal/Wait) for the
                            interruption scenarios. Every repository and bare
                            remote starts on harness.DefaultBranch ("main"),
                            pinned rather than inherited from the host's
                            init.defaultBranch, which differs between developer
                            machines and CI runners
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
  overrides_test.go         goal 12
  packages_test.go          goal 13
  interrupt_test.go         goal 14
  standalone_test.go        goal 15
  manifests_test.go         goal 16
  filter_test.go            goal 17
  docker_test.go            goal 18
  autoreplace_test.go       goal 19
  selfupdate_test.go        goal 20
  guard_test.go             goal 21
  if_test.go                goal 22 (dispat if)
  exec_test.go              goal 22 (dispat exec)
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

| Test                                                      | Claims proven                                                                                                                                                                                                                                               |
|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestPlanCancelSemantics`                                 | Cancel discards pending work irreversibly (post-cancel fix releases 0.0.1, not 0.1.1); a spent cancel warns (W170); a cancelled/no-op release run executes zero scripts.                                                                                    |
| `TestPlanHoldResumeAndReleaseAsAuto`                      | Hold reports the withheld version (W154) and excludes the package from *execution*, not just tagging (zero script runs while held); resume releases at accumulated `max()` with exactly one build; redundant `auto` warns (W158).                           |
| `TestPlanExactPinGuards`                                  | E153 (not greater), E157 (major jump > 1), E154 (multi-package pin), each in an isolated repo so a rejected pin cannot collide with earlier tags.                                                                                                           |
| `TestPlanRejectedPinFallsBackToTheComputedBump`           | A rejected pin has §16's unit-scoped blast radius: E156 fires, the bad unit contributes nothing, and the sibling `feat` still releases at its computed 0.1.0 (a regression fence; see Regression fences).                                                   |
| `TestPlanConsumerFailureCatchesUpAfterProviderPublished`  | Consumer fails while provider publishes; the next run catches the consumer up at the owed version, labelled W193, provider not re-released; a third run converges.                                                                                          |
| `TestPlanProviderBuildFailureBlocksConsumerThenHeals`     | Provider fails to build; consumer is blocked (W194), never attempted; after the fix both release in one run, with neither W194 nor W193.                                                                                                                    |
| `TestPlanCatchUpWholeHistoryForNeverReleasedConsumer`     | A package created *after* a provider's propagating commit still catches up on its first ever run; an untagged package's window is the whole history.                                                                                                        |
| `TestPlanPrereleaseTrainWeirdCases`                       | `^%beta` cannot drag a stable consumer (W208); `^%beta++1` brings it onto the train; a multi-package direct transition graduates the whole train; the graduated train converges.                                                                            |
| `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` | A propagated `beta>stable` *transition* graduates the dependants still on the named train (the `release(core)%beta>stable%%beta>stable++N` form configuration.md documents), and the graduated train converges (a regression fence; see Regression fences). |
| `TestPlanChannelOnlyReleaseAndEntryPatch`                 | A release directive that only moves the channel is still a release, explained by W202; entering a prerelease channel with nothing pending takes the §11.4 entry patch, explained by W204, and its scripts execute.                                          |

### Goal 4: config, login, originals (`config_test.go`)

| Test                                                   | Claim proven                                                                                                                                                                                                                                                                                                                                                                                                                  |
|--------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestConfigUnknownKeyIsRejected`                       | A typo'd top-level key fails the run (exit 1) instead of being silently ignored.                                                                                                                                                                                                                                                                                                                                              |
| `TestConfigFileFallbackResolution`                     | Without `--config` the binary resolves the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists (a model-marshalled config under the yaml name loads and plans); with none present it exits 1 with an error naming what it tried.                                                                                                                                                                   |
| `TestConfigResolutionAscendsToTheMonorepoRoot`         | Without `--config`, resolution climbs from `--root` through its parents; a release invoked from `packages/core` tags and writes changelogs exactly as one from the top and converges, while an explicit `--config` never ascends.                                                                                                                                                                                             |
| `TestConfigGitRepositoryGuard`                         | A config with no git repository around it fails `status` with one clear error before any work, and `init` refuses a `--root` that is not a repository root: no raw git errors, nothing written.                                                                                                                                                                                                                               |
| `TestConfigConcurrencyFlagOverridesFile`               | `--concurrency` beats the file value *at runtime*: measured overlap, not parsed config, is the evidence.                                                                                                                                                                                                                                                                                                                      |
| `TestConfigCustomShellIsUsed`                          | `"shell": ["/bin/bash", "-c"]` actually switches the interpreter (a bashism invalid under `/bin/sh` succeeds).                                                                                                                                                                                                                                                                                                                |
| `TestStaticEnvReachesScripts`                          | The `env` layers (top level, space, package) merge with the most local winning, keys keep their exact case through the binary, a value's `$DISPAT_VERSION` reference expands to the package's own version, and a run hook sees the top-level layer alone.                                    |
| `TestStaticEnvCannotShadowComputedVariables`           | A static key under the reserved `DISPAT_` prefix is a load-time refusal, not a variable quietly ignored: that is what lets a script trust `DISPAT_VERSION`.                                                                                                                                  |
| `TestStaticEnvRefusesUnusableKeys`                     | The two other keys that could never reach a script intact, an `=` in the name and an empty name, are each refused with the reason.                                                                                                                                                           |
| `TestStaticEnvFromFolderConfigFiles`                   | The two in-folder layers, a space folder's config file and a package folder's, reach the scripts with their case intact and the most local winning.                                                                                                                                          |
| `TestStaticEnvReachesTheLoginScript`                   | A space's `env` reaches its login script, which runs once per space in the space folder with no package in view.                                                                                                                                                                             |
| `TestConfigCustomObjectIsIgnored`                      | A `custom` object at all three levels loads without tripping the unknown-key guard and changes nothing about the release.                                                                                                                                                                    |
| `TestConfigLoginOncePerSpaceAcrossSpaces`              | Two spaces sharing one login *script text* log in once **each**; the gate is keyed by space, not by script.                                                                                                                                                                                                                                                                                                                   |
| `TestConfigLoginFailureIsolatedToItsSpace`             | A failing login fails every publish of its space and none of another space's.                                                                                                                                                                                                                                                                                                                                                 |
| `TestConfigOnFailAndOnSkipOutcomeScripts`              | In one failing run: `flow.onFail` fires once for the failed package with `DISPAT_FAILED_STAGE`/`DISPAT_ERROR`, `flow.onSkip` once for the blocked consumer with `DISPAT_BLOCKED_BY`, neither for the package that published; and an onFail sequence whose first command fails still runs to the end (warn-only).                                                                                                              |
| `TestConfigNonPackageScopesReplacesDefault`            | Setting `nonPackageScopes` **replaces** the `["release"]` default: the custom scope becomes exempt, `release` stops being exempt.                                                                                                                                                                                                                                                                                             |
| `TestConfigFusedPrereleaseTagFormatRoundTrips`         | `{name}%v{version}-{channel}{counter}`: `beta0` is written, read back, converges, and the counter continues to `beta1` over three runs; a second space overriding with the normative format keeps its own spelling: the format is a per-space property.                                                                                                                                                                       |
| `TestConfigRevertOnFailAppliesAfterVersionStageOnSkip` | The skip-after-version-stage rollback: the consumer's version script dirties its folder, the provider's publish fails, and the skipped consumer's folder is restored.                                                                                                                                                                                                                                                         |
| `TestConfigGithubReleasePrereleaseFlagFollowsChannel`  | Against an httptest GitHub API: the same package's releases flip `prerelease: true -> false` across a real beta release and its graduation.                                                                                                                                                                                                                                                                                   |
| `TestConfigGithubReleaseAttachments`                   | The whole script-output path through the real binary: the build exports `DISPAT_EXPORT_GITHUB` (two files, opting the package into the GitHub release) plus an ordinary output into `$DISPAT_OUTPUT`, later stages see the output as `DISPAT_OUTPUT_*` (plus the `DISPAT_OUTPUTS` listing) and the export under its full name, and both files arrive as assets at the endpoint the created release advertised (`upload_url`). |
| `TestConfigParserOptions`                              | The top-level `parser` object drives real parsing: a custom type table makes `docs` release, the configured default propagation depth reaches a consumer with no caret, `strictTypes` raises E140 (tolerated under the default `commitErrors`), and an invalid parser value fails the load with exit 1.                                                                                                                       |
| `TestConfigScriptOutputsCarryAcrossStagesAndHooks`     | The full accumulation contract, hooks included: a `beforeBuild` *hook* export reaches build and publish, the build's export reaches publish (with `DISPAT_OUTPUTS` listing both in export order), and the failing package's `onFail` receives the hook's export **and** what the failed build exported before dying.                                                                                                          |
| `TestConfigCommitErrorsPolicy`                         | A unit-scoped error (E130) under the default `warn` still releases the sibling work; under `error` the release is refused (exit 1, nothing tagged) while `status` still exits 0 and reports the plan.                                                                                                                                                                                                                         |
| `TestConfigParserQuiet`                                | `parser.quiet` hides the parser's own findings (E140) while the planner's (E130) still print, still counts every diagnostic and reports how many were hidden, still refuses the release under `commitErrors: error`, and `--quiet-parser` overrides the config in both directions. |
| `TestConfigInitialsBaselines`                          | Initials seed the version of a package whose newest tag is unparseable (the pre-last tag is NOT used and the window still starts at the broken tag), an unmatched initials key only warns, and the next release reads the new real tag back.                                                                                                                                                                                  |
| `TestConfigRunLevelHooks`                              | The run-level hook frame against a real remote: every hook fires in phase order in the monorepo root, postAll sees the run outcome and the workspace listing, and a quiet second run keeps the commit/push hooks off because their phases never happen.                                                                                                                                                                       |
| `TestConfigRunLevelHookFailureSemantics`               | A failing warn-only run hook (postAll) does not fail the run and its sequence continues; the gating beforeAll aborts the run before any release work.                                                                                                                                                                                                                                                                         |
| `TestConfigFormatsSmoke`                               | One minimal smoke per config format: the same monorepo releases through the binary under dispat.json, dispat.yaml and the init command's dispat.toml starter; the json leg also pins that `status` reports without tagging.                                                                                                                                                                                                   |
| `TestConfigDispatignoreSelectsTheConfigFile`           | A folder holding two config files names the one to skip in its `.dispatignore`, and the surviving file decides: proven at the repository root, in a space folder and in a package folder, each by the tag only that file's format could produce.                                                                              |
| `TestConfigResolutionAscendsPastASpaceFile`            | A space folder's file declares `packages`, like a monorepo of standalone packages does; run from inside the space and from the space folder itself, resolution still reaches the root above, because that root claims the folder.                                                                                             |
| `TestConfigSpaceLayerRejections`                       | What the new layers may not say, each refused before any work: `path` on a space's `packages` entry, `path` and `spaces` in a space file, `packages` on a package entry, and a space `packages` key matching no folder of that space.                                                                                          |
| `TestConfigAllStageHooksFireInOrder`                   | Every one of the nine per-package hooks plus the announce stage fires, in the documented frame order, on a provider/consumer pair; the consumer additionally runs the version stage with its two hooks inside the same frame.                                                                                                                                                                                                 |
| `TestConfigStageHookAuthoritySplit`                    | The documented authority split: failing postPublish and the whole announce frame only warn (exit 0, tag exists), while a failing gating hook (postBuild) fails the package, tags nothing, and fires onFail with the stage that carried the failure.                                                                                                                                                                           |

### Goal 5: space versioning modes (`versioning_test.go`)

| Test                                                 | Claim proven                                                                                                                                                                                                                                                              |
|------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningFixedSpaceLifecycle`                  | Four runs over a fixed space next to an independent one: a change to either member releases both at one version (W210 on the rider, "no changes" changelog entry, no leaked notes), quiet runs converge, and the independent space never moves with any of it.            |
| `TestVersioningFixedSparseLifecycle`                 | Sparse across four runs: only changed members release (no W210), an unchanged member keeps its version, its first change jumps it to the space version, and a joint change lands both on one shared next version.                                                         |
| `TestVersioningThreeModesSideBySide`                 | One commit through fixed + sparse + independent spaces at once: each mode moves exactly its own set, the independent newcomer versions from its own history (`0.0.1`, not the space-aligned `0.1.1`), and all three converge together.                                    |
| `TestVersioningFixedSharedPrereleaseTrain`           | A fixed space runs a *single* train: one member's `%beta` takes the whole space to `beta.0`, later work continues it to `beta.1` for both, one member's graduation ends it for both, and the graduated space converges.                                                   |
| `TestVersioningFixedRideFailureThenAlignmentCatchUp` | A ride can fail like any release: the changed member publishes, the rider fails, and the next run aligns the rider at exactly the space's published version (W210) without re-releasing anyone; a third run converges.                                                    |
| `TestVersioningCrossSpaceDependencyIntoFixedSpace`   | A caret from an independent provider into one fixed-space member: the member gets an ordinary DueTo release (version task, `DISPAT_UPDATED_*`), its space mate rides to the same version with no version task; edges stay package-scoped where versions are space-scoped. |
| `TestVersioningFixedHoldAndResume`                   | `Release-As: none` on one member keeps only it back; the resume aligns it to the space's published version.                                                                                                                                                               |
| `TestVersioningFixedExactPinMovesTheSpace`           | An exact pin naming one member moves the whole space to the pinned version; the pin guards (E153) keep applying to the shared version afterwards.                                                                                                                         |
| `TestVersioningFixedSpaceExecutesEveryMemberScript`  | A ride is a full release at the execution level: build scripts run for the rider too.                                                                                                                                                                                     |
| `TestVersioningFixedConflictResolutions`             | The two fixed-space conflict warnings: competing exact pins resolve to the newest with W211 (the loser must not also release), and members resolving to different channels release as one channel with W212.                                                              |
| `TestVersioningFixedMajorLifecycle`                  | Six runs over a `fixedMajor` space: a patch and a minor each move only their own package (no W210), a breaking change moves the whole group to one major with a ride whose changelog entry reads "on one major version" and carries no leaked notes, the group converges, and it diverges again below the major.  |
| `TestVersioningFixedMajorSparseLifecycle`            | The sparse variant: the unchanged member never rides across a major bump (no W210), and its own next change joins it to the shared major at the start of its own line (`1.0.0`, not a continuation of `0.x`).                                                             |
| `TestVersioningFixedMajorMinorLifecycle`             | One depth further in: a patch stays with its package while a minor and a breaking change each move the whole group, the ride's entry reading "on one major and minor version".                                                                                            |
| `TestVersioningFixedMajorMinorSparseLifecycle`       | Depth two, sparse: a member left behind rejoins the shared prefix on its own next change whatever its size, the two are independent again below the minor, and a later minor leaves the other member behind in turn.                                                      |
| `TestVersioningAllModesSideBySide`                   | All seven modes through one repository and two commits: the minor separates the depths (shared under `fixed` and `fixedMajorMinor`, the package's own under `fixedMajor`), the breaking change every shared mode passes on, sparse members never ride, and all seven converge. |
| `TestVersioningFixedMajorSharedTrain`                | A train belongs to whatever it moves: a breaking change on `%beta` takes the whole group to `beta.0`, later work continues it to `beta.1` for both, one member's graduation ends it for both, and a `%beta` on a *patch* afterwards stays inside the package that started it. |
| `TestVersioningPartialPinScope`                      | An exact `Release-As` crossing the shared major moves the whole group; one inside the major releases its own package alone, collects no group-level guard (no E153) and drags nobody along.                                                                               |
| `TestVersioningFixedMajorRideFailureThenAlignment`   | A partial-mode ride fails like any release, and the next run catches the laggard up to the group's shared major (W210) at the start of its own line, without re-releasing anyone; a further run converges.                                                                |
| `TestVersioningPartialRideExecutesEveryMemberScript` | A ride under a partial mode is a full release at the execution level too: both members build.                                                                                                                                                                            |
| `TestVersioningMixedDepthGroupUsesTheDeepest`        | A package overriding its space's `versioning` stays in the space's group with a different depth: the group versions at the deepest declaration (so the minor is shared) and W213 explains the sharing the shallower member never asked for.                               |

### Goal 6: the `dispat run` command (`run_test.go`)

| Test                                               | Claim proven                                                                                                                                                                                                                                                                                                |
|----------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestRunExecutesChangedPackagesInTopologicalOrder` | The script runs once per changed package of the defining space, providers before consumers; a space without the name is skipped; nothing is tagged.                                                                                                                                                         |
| `TestRunShorthandCommand`                          | `dispat lint` is `dispat run lint` when the word is not a command name, and it runs the script rather than releasing.                                                                                                                                                                                       |
| `TestRunReceivesTheFullPackageEnvironment`         | The run script sees the stage environment (`DISPAT_PACKAGE`, `DISPAT_NEW_VERSION`, `DISPAT_TAG`, the workspace listing) with `DISPAT_STAGE=run:<name>`.                                                                                                                                                     |
| `TestRunUnknownScriptFails`                        | A name nothing defines exits 1 instead of silently running nothing.                                                                                                                                                                                                                                         |
| `TestRunTopLevelScriptReachesEveryPackage`         | A name defined at the top level resolves in every package, so the run covers every changed package of every space, including a space with no scripts of its own.                                                                                                                                            |
| `TestRunWhenDiscoveryItselfFails`                  | A space pointing at a missing folder leaves both halves of the command with nothing to read: the name-exists guard (which consults discovery) and the plan both report a plain exit 1, and no script runs.                                                                                                  |
| `TestRunSpaceScriptStaysInItsSpace`                | The same name defined on one space instead reaches that space's changed packages alone; the others are no-ops.                                                                                                                                                                                              |
| `TestRunPackageScriptRunsInThatPackageAlone`       | A name only one package defines reaches that package and no other, even though its space mate is changed too.                                                                                                                                                                                              |
| `TestRunResolvesTheMostLocalScript`                | One name defined at all three levels resolves per package: the package's own wins, then its space's, then the file's, each changed package recording which level answered.                                                                                                                                  |
| `TestRunNoSelectedPackageDefinesIt`                | A name that exists but nowhere in the selection exits 1 rather than reporting a clean run of nothing; the same name over a selection that does contain its package succeeds.                                                                                                                                |
| `TestRunFilterRunsATopLevelScriptInOnePackage`     | A filtered run executes one top-level script inside one package's folder under the release environment (`DISPAT_STAGE=run:<name>`, the baseline as the new version), releasing nothing, an unchanged package reached through `--since all`; unknown script, unknown package and a failing script all exit 1. |
| `TestRunOnErrorPolicies`                           | Under the default `--on-error=skip` a failed provider's dependents are skipped; under `continue` they still run; both exit 1; an unknown policy is a usage error (exit 2).                                                                                                                                  |
| `TestRunConcurrencyBudget`                         | Independent packages' scripts overlap under `--concurrency 3` (measured, three independent checks) and serialise under the config's budget of 1.                                                                                                                                                            |
| `TestRunGraphOrderingUnderConcurrency`             | Both scheduling promises in one graph: three providers' scripts pairwise overlap at the full budget while their shared consumer's script never starts before every provider's ended.                                                                                                                        |
| `TestRunCarriesOutputsAcrossPackages`              | A provider's `$DISPAT_OUTPUT` export (written with the `DISPAT_OUTPUT_` prefix spelling) reaches its consumers as `DISPAT_OUTPUT_<NAME>` with `DISPAT_OUTPUT_SOURCE_<NAME>` naming the exporting script, transitively, through a middle package whose space defines no script at all.                       |
| `TestRunCarriesOutputsFromAFailedProvider`         | Under `--on-error continue` a failed provider's dependents still run and still receive what the failed script exported before dying.                                                                                                                                                                        |
| `TestRunSkipsUnchangedPackages`                    | After a release nothing is changed and the script runs zero times (an empty selection is a success, unlike one no package resolves the name in); a fresh change narrows the run to exactly the changed package.                                                                                             |
| `TestRunInFixedSpaceIncludesRides`                 | In a fixed space a ride is a changed package, so the run script executes in every member.                                                                                                                                                                                                                   |
| `TestRunFilterNarrowsToANamedPackage`              | `--package` runs exactly the named package within the window and errors on an unknown package or one that does not resolve the script; an unchanged package needs `--since all`, because the filter narrows the window rather than replacing it.                                                            |
| `TestRunNarrowsToTheInvokedPackage`                | Both spellings from inside a package folder (or a nested subdirectory) run only that package, riding the config ascent; from the monorepo top they still cover every changed package; an explicit `-p` beats the folder it was typed in.                                                                    |
| `TestRunSinceSelectsByCommitScopes`                | `--since HEAD~1` narrows the run to what the last commit addressed (the written scope wins over the changed files, a scopeless unit derives from its files, §6.2), `--since all` selects every package, an unknown revision exits 1, and a package filter narrows whichever window the flag chose — to nothing, honestly, when the two do not meet. |
| `TestRunConsumersExpandTransitively`               | `--consumers` widens a `--since` window with every transitive dependent (the far end of a three-link chain is reached through the middle package, providers still first) while packages nothing depends on stay out, and `--since all --consumers` is a no-op expansion.                                    |
| `TestRunConsumersOnReleaseWindow`                  | The default release window has the same gap under depth-0 propagation, and `--consumers` closes it there too: a `feat(core)` window runs core alone plainly, core + its transitive consumers with the flag.                                                                                                 |
| `TestRunConsumersSkipCascade`                      | An expanded consumer is a full member of the run: a failing provider script skips it transitively under the default `--on-error skip` (exit 1), and `--on-error continue` runs it anyway.                                                                                                                   |
| `TestRunConsumersComposeWithAFilter`               | `--consumers` expands a filtered selection instead of refusing it, and the expansion is not filtered back out: `-p mid --consumers` runs mid and its dependents, in graph order, with core and extra staying out. The folder spelling behaves identically.                                                  |

### Goal 7: release records (`records_test.go`)

| Test                                                              | Claim proven                                                                                                                                                                                                                                                                                                                                                   |
|-------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestRecordsChangelogAccumulatesAcrossReleases`                   | Entries prepend newest first under one never-duplicated title; a changelog that predated dispat keeps its content below every generated entry; a multi-unit commit groups its sections by bump (Breaking Changes above Fixes, run 1's unit staying in its own entry); a consumer's entry carries the provider's version *movement* (`- core: 0.1.0 -> 1.0.0`). |
| `TestRecordsChangelogCustomFileTitleAndSections`                  | `changelog.file`, `changelog.fileTitle` and the section-title options change the artefact on disk, and the default `CHANGELOG.md` is not written next to the configured file.                                                                                                                                                                                  |
| `TestRecordsTagsAreAnnotatedWithReleaseMessages`                  | A release tag is an annotated tag *object* (`cat-file -t` = `tag`), its message is `release <tag>`, and it peels to the commit that was released.                                                                                                                                                                                                              |
| `TestRecordsReleaseCommitTagsAndPush`                             | Commit mode against a real bare remote: one `chore(release): ...` commit carrying every published changelog, tags placed on that commit (not the source commit), branch + tags actually on the remote after the push, and a re-run converging because the release-commit scope is exempt by default.                                                           |
| `TestRecordsCommitModeLeavesHistoryUntouchedWhenNothingPublished` | A run where every package fails leaves the history exactly as it was: no release commit, no tags.                                                                                                                                                                                                                                                              |
| `TestRecordsPushSkipsExistingRemoteTags`                          | A tag already present on the remote (a partially pushed earlier run) is skipped while the branch, the release commit and every new tag still arrive; the pre-existing remote tag keeps its original target.                                                                                                                                                    |
| `TestRecordsExportedPackageCommitPinsTheTag`                      | A release script exporting `PACKAGE_<KEY>=<commitHash>` pins its package's tag to that commit; the release commit still carries the changelog on top.                                                                                                                                                                                                          |
| `TestRecordsExportedCommitExcludesTagFromReleaseCommitAndPushes`  | Mixed run: only the exporting package's tag moves to the exported commit and no tag for it is created on the release commit (`tag --points-at` names only the space mate's); the push delivers both tags with their targets intact.                                                                                                                            |
| `TestRecordsExportedCommitPinsTagOutsideCommitMode`               | Without commit mode the export redirects the tag from HEAD to the exported commit: the tag lands there, no tag exists at HEAD, and HEAD does not move.                                                                                                                                                                                                         |
| `TestRecordsPushVerifyDisabled`                                   | `commit.verify=false` switches the upfront ls-remote check off: the release work happens and only the push itself fails; asserted against the default in the same test, which fails fast before any work (no tags, no changelog).                                                                                                                              |
| `TestRecordsChangelogDisabled`                                    | `changelog.enabled=false` switches the file recorder off without touching anything else: the release still publishes and tags, no changelog appears.                                                                                                                                                                                                           |
| `TestRecordsCommitModeGithubFinalize`                             | GitHub in commit mode: releases created in the finalize phase, the body documenting the exact commit and tag, the recorder opt-in per package (no export, no release), a `PACKAGE_<KEY>` export overriding commit and `target_commitish`, and `commit.messageFormat` rendering `{packages}`/`{tags}`.                                                          |
| `TestRecordsGitHubAllPackages`                                    | `github.allPackages` gives every published package a release without exporting `DISPAT_EXPORT_GITHUB`, leaving the export to add assets only; the default keeps the export as the per-package opt-in.                                                                                                                                                          |
| `TestRecordsPrereleaseOptOut`                                     | `changelog.prerelease` / `github.prerelease` set to false leave a beta tagged and published but unrecorded, while the graduation to stable writes the one entry and the one release covering the window; a per-package override opts back in.                                    |
| `TestRecordsGitHubReleaseExistsIsASkip`                           | A release the repository already carries is a W224 skip rather than the API's 422, so a repeated `dispat github` and the release that follows both converge instead of failing.                                                                                                 |
| `TestRecordsHeaderAndFooterPerEntry`                              | `header` and `footer` belong to the entry, not the file: two releases leave two of each, bracketing that entry's sections, while a multi-line `fileTitle` heads the file exactly once.                                                                                          |
| `TestRecordsReleaseNameSubHeader`                                 | `releaseName` writes an interpolated sub-header under the entry's date line, and the entry stays recognisable by its tag line, so a re-run still skips it.                                                                                                                      |
| `TestRecordsLineFiltersSelectPackages`                            | One configured list serves a whole workspace: `package`, `space` and `group` filters each write to their own packages and to no others, an unfiltered line reaches all of them, and an independently versioned space belongs to no group.                                       |
| `TestRecordsTextExpandsVariables`                                 | Record text interpolates the release's own variables, a value a build script exported, and the process environment, in the changelog file and the GitHub body alike; an undefined name expands to nothing.                                                                      |
| `TestRecordsGitHubBodyOrder`                                      | In a GitHub release the name is `releaseName` while `tag_name` stays the tag, and the body reads header, sections, the `### Release` block, footer.                                                                                                                             |
| `TestRecordsLineOverrideReplacesInherited`                        | A package's own list states what that package writes and does not extend the inherited one, which the other packages still get.                                                                                                                                                |
| `TestRecordsLineWithoutTextIsAConfigError`                        | A line object that selects packages and writes nothing to them fails the load (exit 1), named by its list and index.                                                                                                                                                            |
| `TestRecordsLineShorthandsInAPackageFolder`                       | The three element shapes — a string, an array of strings, an object — decode the same in an in-folder package config as in the root config.                                                                                                                                     |

### Goal 8: the init and preview commands (`commands_test.go`)

| Test                                | Claim proven                                                                                                                                                                                                                                                                                 |
|-------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestCommandsInitThenStatusCompose` | `dispat init --format toml` then a plain `dispat status`: the fallback finds `dispat.toml` with no `--config` anywhere, the starter config loads and discovers the package, and a second `init` refuses to overwrite (exit 1).                                                               |
| `TestCommandsPreviewNotesWindowing` | `dispat preview --package <name>` prints the pending notes (header, sections, entries), reports "no pending changes" once released, errors on an unknown package; and across a prerelease train the preview and each entry narrow to the fresh changeset while the graduation collects the whole train. |
| `TestCommandsPreviewAllPackages`    | `dispat preview` with no filter renders every package with something pending in publish order, keeps quiet packages out, reports "no pending changes" once nothing is pending, and rejects a positional package name (exit 2).                                                               |
| `TestCommandsHelpIsScopedToTheCommand` | `dispat <command> --help` prints that command's synopsis and its own flags only; the program help lists every command with the global flags alone. Both exit 0 with no config file or repository.                                            |
| `TestCommandsVersionNamesThePlatform`  | `--version` reports the platform alongside the version, so a bug report says which of the release's binaries is running.                                                                                                                     |

### Goal 9: repository-scoped fatal errors (`fatal_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                  |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFatalDependencyCycle`      | A cyclic dependency graph loads as config but refuses to plan: exit 1, E200 in the events, no tag, no script, and `status` and `dispat run` refuse too.                                                       |
| `TestFatalDuplicateVersionTags` | Two reachable tags parsing to the same version of one package on different commits (`core@0.1.0` next to a hand-planted `core@0.1.0+dup`) make the baseline ambiguous: exit 1, E191, pending work unreleased. |
| `TestFatalShallowRepository`    | A `git clone --depth 1` of the repository refuses to release: exit 1, E196 in the events, instead of silently planning over truncated history.                                                                |

### Goal 10: the compute command (`compute_test.go`)

| Test                                  | Claim proven                                                                                                                                                                                                  |
|---------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestComputeDetectApplyStatus`        | The full loop through the binary: a preview writes nothing, `--check` exits 1 on drift, `--write` applies with a byte-identical `.backup`, the next `status` orders by the new edge, and `--check` converges. |
| `TestComputeKeepAndRemoval`           | A stale edge is suggested for removal; `keep: true` silences it, survives `--write`, and the config still loads.                                                                                              |
| `TestComputeAmbiguousNameReportsW220` | Two packages declaring one manifest name derive no edges and the ambiguity reaches the JSON events as W220.                                                                                                   |
| `TestComputeEditsSpaceLayerDeclarations` | A stale edge declared in a space's `packages` entry is removed from the root config and one declared in a space folder's file from that file, each source named in the listing and each edited file keeping its own `.backup`. |
| `TestComputeSeedsInitialsFromManifests`  | Adoption end to end: the manifests' versions are offered as `initials`, `--check` gates on them, `--write` lands them and the edges in one write with a single backup from before both, the next `status` plans `1.4.2 -> 1.5.0` with `baselineFromInitials`, and a second run is in sync. |
| `TestComputeKeepsExistingInitials`       | An entry already in the config is never rewritten, whatever the manifest says: a mixed-case key survives an unrelated `--write` with its spelling, `0.0.0` counts as a decision, and the plan follows the entry rather than the manifest. |
| `TestComputeSkipsPackagesWithReleaseTags` | A baseline is proposed exactly where the planner would read one: silence for a package with a parseable release tag, a suggestion naming the tag for one whose newest tag is not a version, and a suggestion for one with no tag at all. |
| `TestComputeSeedsInitialsBeforeTheFirstCommit` | A repository with no commits yet (where adoption often starts) gets its baselines without a tag query, and the first release after committing continues from the manifest's version. |
| `TestComputeVersionsItCannotUse`         | Two manifests disagreeing about one package's version report W225 and derive nothing, while a `1.0.0-SNAPSHOT` prerelease and a version that is not semver are passed over in silence. |
| `TestComputeInitialsInYAMLAndTOMLConfigs` | The baselines are written like every other config edit: a YAML config gains the key and keeps its comments, a TOML config prints the paste-ready `[initials]` block, fails, and is left byte-identical with no backup. |
| `TestComputeInteractiveChoosesAmongBothKinds` | `--interactive` over the real stdin walks edges and baselines in one pass, each answer standing alone: two accepted are written, the declined one is not, and it is offered again on the next run. |

(The command's finer grain, meaning cross-ecosystem matching, interactive selection, the TOML snippet fallback,
stale-endpoint removals, the manifest-rank and version-shape rules behind a baseline, and error paths, is unit-tested
in `services/dispat/internal/app`, where each case is one in-memory monorepo away instead of one binary invocation.)

### Goal 11: native auto-versioning (`autoversion_test.go`)

| Test                                         | Claim proven                                                                                                                                                                                                                                                                                                                      |
|----------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoVersionReleaseRewritesManifests`    | A `workspace:*` range is reconciled to the provider's released version, a hand-pin outside the match globs survives, both own versions advance, and the syncLock snapshot proves it ran after the rewrite.                                                                                                                        |
| `TestAutoVersionSyncLockSerialised`          | Several packages' syncLock scripts never overlap under the default budget of 1 while builds keep the build budget: the corrupted-shared-lockfile guard over the real scheduler.                                                                                                                                                   |
| `TestAutoVersionDiagnosticsAndCommitInclude` | Three runs: W221 for a rewritten edge with no configured counterpart (and `commit.include` staging the regenerated root package-lock.json into the release commit); W192+W197 after the manifest was hand-edited backwards; W203 when the provider goes to beta under a stable consumer. All asserted as JSON events per package. |
| `TestAutoVersionReplaceStrategy`             | A Gradle-shaped space where nothing parses: `manifests: none` plus two replace rules reconcile the provider's coordinate in a build script and the package's own version in a README, every occurrence and no other byte, while a PNG the glob also reached is skipped rather than corrupted.                                     |
| `TestAutoVersionReplaceRuleMatchedNothing`   | A mistyped rule reconciles nothing and says so (W222) instead of failing silently release after release.                                                                                                                                                                                                                          |
| `TestAutoVersionManifestNamesMakeAnEdgeVisible` | A package whose files declare no readable identity becomes visible once `manifestNames` states it: `dispat compute` derives the edge and auto-versioning reconciles the coordinate, from the one index the two share.                                                                                                           |
| `TestAutoVersionSyncLockOnly`                | An `autoVersion` block with neither strategy is how a space asks for lock regeneration alone: the scripts run every release (there is no change to key off) and the budget of 1 still serialises them on the tsmark timeline, with no file rewritten.                                                                              |
| `TestAutoVersionSyncLockOnlyStandalone`      | The same mode through `dispat autoversion`, where the serial loop is the budget by construction: two invocations, two lock regenerations.                                                                                                                                                                                          |
| `TestAutoVersionPolicyFlagsStillRunSyncLock` | A flag-overridden policy and the syncLock loop read the same resolved block, so a reconciliation the flags caused still regenerates the lock.                                                                                                                                                                                       |
| `TestAutoVersionNoReplaceFlag`               | `--no-replace` skips the rules for one invocation and leaves the parsing strategy to do its half; without the flag the same invocation finishes the job.                                                                                                                                                                            |
| `TestAutoVersionManifestsNoneFlag`           | `--manifests none` turns the parsing strategy off for one invocation, and a value outside the three is a usage error (2).                                                                                                                                                                                                          |

### Goal 12: per-package overrides, versioning groups and `.dispatignore` (`overrides_test.go`)

| Test                                         | Claim proven                                                                                                                                                                                                        |
|----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOverridesFlowBuildPerPackage`           | A `packages` entry replaces one flow entry for one package alone: the override's build runs for it, the space's for the sibling, the un-named stages inherit (both publish and tag), and a second run converges.    |
| `TestOverridesFlowScriptResolvesPerPackage`  | One `flow.build: build` reaches three different commands in one release: the package's own `scripts`, its space's, and the file's, resolved most-local-first for whichever package the stage runs for.             |
| `TestOverridesFlowScriptSuppliedByEveryPackage` | A space's flow entry may name a script only its packages define, and the release runs each package's own; removing one package's entry fails the config with an error naming that package.                       |
| `TestOverridesInFolderFileWins`              | The package folder's own dispat.json is the most local layer: its `tagFormat` beats the `packages` entry's, proven by the tag the release actually creates, while the sibling keeps the repository default.         |
| `TestOverridesDispatignore`                  | A folder listed in `.dispatignore` is not a package: never released, and a commit scoping it draws the unknown-scope diagnostic (E130) like any non-package name.                                                   |
| `TestOverridesVersionGroupSpansSpaces`       | A declared `versionGroups` group joined by two spaces versions as one: a change in one space rides the other space's package to the same version (W210 on the rider), and aligned members converge on the next run. |
| `TestOverridesVersionGroupSharesOnlyTheMajor` | The same two spaces under a `fixedMajor` declaration: a minor stays inside its own space, and only a breaking change brings both spaces to one major (W210 on the rider), converging afterwards. |
| `TestOverridesPerPackageRecords`             | Record policies resolve per package: one package writes its changelog under an overridden file name, its sibling disables both records, and the GitHub fake receives exactly the enabled package's release.         |
| `TestOverridesPackageConcurrencyWeight`      | A package whose `concurrency` equals the build budget occupies it whole: its build overlaps no other build on the tsmark timeline while the ordinary packages stay free to overlap each other.                      |
| `TestOverridesRunShorthandFromPackageFolder` | The config ascent walks past the package's own (spaces-less) override file to the monorepo root, so the run shorthand keeps working from inside a package folder that carries one.                                  |
| `TestOverridesScriptsAcrossTheLayers`        | A script defined only in a package's in-folder file (found through discovery) or only in a `packages` entry (found in the loaded config) runs in that package alone, while the space's and the file's own scripts reach both packages; a name defined nowhere stays a hard error. |
| `TestOverridesSpacePackagesEntry`            | A space configures one of its own packages through its `packages` map, with no top-level entry involved: the override's tag format reaches that package alone and the sibling keeps the repository default.        |
| `TestOverridesSpaceFile`                     | A dispat config file in the space folder is the space said again and nearer: it replaces the stages and options it names for every package of the space, inherits the rest, and leaves the other space untouched.  |
| `TestOverridesLadderNearestWins`             | All six layers name one package and one key, and the nearest wins (the package's own file); a package no entry names still takes the space file's value, and a farther layer still supplies what nearer ones omit. |
| `TestOverridesSpaceLayerDependencies`        | Edges declared in a space's `packages` entry and in the space file both reach the plan: a provider's bump carries down the chain one layer at a time, exactly as a top-level declaration would.                    |

### Goal 13: the top-level `packages` section (`packages_test.go`)

| Test                                    | Claim proven                                                                                                                                                                                                                                  |
|-----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestPackagesStandalonePath`            | An entry with a `path` releases as a full package outside the space folders: its own flow runs in its own folder, it tags under the repository format, writes its changelog, leaves the space packages untouched, and a second run converges. |
| `TestPackagesStandaloneConfigErrors`    | A standalone path escaping the repository fails the load; a path naming no folder fails discovery with the folder named; both happen before anything is released.                                                                             |
| `TestPackagesDependencyEdges`           | Provider lists declared in a `packages` entry and in an in-folder config file order the graph exactly like top-level edges (`status` dependsOn proves it).                                                                                    |
| `TestPackagesComputeRemoveFromInFolder` | A stale in-folder edge is suggested with its declaring file named, `--check` gates on it, and `--write` removes it from that file (other keys intact, own `.backup`) while a manifest-detected addition still lands in the root list.         |
| `TestPackagesSrcNarrowsChangeDetection` | A package's `src` narrows file-derived change detection: a scopeless commit touching only what lies outside src releases nothing, one touching src releases as before, a package without src keeps its whole folder, and a commit naming the package by scope reaches it wherever its files are. |
| `TestPackagesSrcMustNameAFolder`        | A `src` that could never match — a missing folder, a path leaving the package, the package folder itself — fails the load rather than narrowing the package to nothing.                                                                                                                          |
| `TestPackagesComputeRemoveFromEntry`    | A stale edge under `packages.<name>.dependencies` in the root config is emptied in place (the entry's other keys and the rest of the file survive) and the gate converges.                                                                    |

### Goal 14: interruption (`interrupt_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                                                                                                                                                                                                |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestInterruptGracefulShutdown` | A SIGINT delivered mid-build (via `harness.StartRelease`/`Proc`) exits non-zero with both packages `cancelled` in the summary events (the killed build is an interruption, not a failure; the never-launched consumer is not `skipped`), nothing is tagged, and the next run releases both at the version they were owed (one tag each, the consumer's build completing on the tsmark log). |
| `TestInterruptStopsARunCommand`  | `dispat run` shares the release scheduler, so a SIGINT mid-script exits non-zero, the package behind the interrupted one never launches, and nothing is released.                                                                                                                                                                                                                          |

### Goal 15: the standalone step commands (`standalone_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                                                 |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestStandaloneChangelogWritesAndIsIdempotent`  | The changelog command writes the pending entry without releasing; a second invocation is a byte-identical W222 skip; a following release keeps exactly one entry header; an unknown package errors, a converged one is a clean no-op.                        |
| `TestStandaloneStepsInsideAReleaseFlow`         | The dogfood flow: nested `dispat changelog` + `dispat commit --tag` in beforePublish land the changelog inside the tagged commit (the fix under test); the outer run reports W222 and W223 with exit 0, one tag, and a quiet convergence run.                 |
| `TestStandaloneCommitPushAndNothingToCommit`    | A clean folder commits nothing but `--tag` still tags HEAD (annotated, with the flag-supplied identity) and `--push` delivers branch and tag; a re-run converges before any git work.                                                                        |
| `TestStandaloneCommitExportsPinWhenDispatOutputSet` | With DISPAT_OUTPUT in the environment, the commit command exports `PACKAGE_<KEY>=<sha>` for the outer run's tag and GitHub-release pin.                                                                                                                  |
| `TestStandaloneCommitFolderNarrowing`           | Invoked inside a package folder the command narrows to that package; from the root it covers every releasing package.                                                                                                                                        |
| `TestStandaloneCommitTagName`                   | `--tag-name` names the annotated tag and the release commit instead of computing them, which is what the nested case needs when a fixed group's shared version moves mid-run; naming one tag while covering several packages is refused before any git work; without the flag the tag is computed as before.  |
| `TestStandaloneAutoversionReconcilesAndSyncLocks` | Ranges and own versions reconcile to the planned versions, syncLock runs once per changed package, and a second invocation rewrites nothing and regenerates nothing.                                                                                       |
| `TestStandaloneChangelogOverrideFlags`          | `--file`, `--title` and `--date-format` override the matching `changelog.*` values for the invocation, and the default `CHANGELOG.md` is not written beside the configured file.                                                                             |
| `TestStandaloneChangelogRespectsDisabledConfig` | `changelog.enabled=false` makes the command a clean no-op (exit 0, no file), not an error.                                                                                                                                                                   |
| `TestStandaloneAutoversionPolicyFlags`          | The `autoVersion` policy flags override the space's block for the invocation, and impose the defaults on a space with no block at all.                                                                                                                       |
| `TestStandaloneAutoversionFlagOverridesExistingBlock` | A flag override starts from the space's own block and replaces only the flagged field: `--range exact` changes the range while the block's `writeVersion` default still applies.                                                                       |
| `TestStandaloneCommitMessageAndIncludeFlags`    | `--message-format` renders `{packages}`/`{tags}` into the subject and `--include` stages an extra path outside the package folder alongside the folder's own changes.                                                                                        |
| `TestStandaloneGithubPublishesFromAStageScript` | The github step inside an announce stage: the build's `DISPAT_EXPORT_GITHUB` reaches the command through the stage environment, one release is created for the package `DISPAT_PACKAGE` names, and the exported file is attached.        |
| `TestStandaloneGithubSelection`                 | The github command selects like every other step command: no opt-in publishes nothing (exit 0), the exported opt-in publishes, a non-releasing package is a logged no-op, an unknown term exits 1 and a positional argument exits 2. |
| `TestStandaloneGithubFailures`                  | The error paths: a prerelease held back by `github.prerelease` publishes nothing and states why; an unresolvable token and a refused verification both exit 1 before any release is created; an API that rejects the creation exits 1. |
| `TestStandaloneCommitPushWithoutRemoteFails`    | `--push` without a remote exits 1, while the local commit and tag it had already made survive.                                                                                                                                                               |
| `TestStandaloneStepsTakeTheWindowFlags`         | The steps take `dispat run`'s window: `--since` picks what a revision addressed, `--consumers` pulls the dependents in, `--on-error` is validated on every sweeping command, and a package tagged by `dispat commit --tag` falls off the recomputed window until `--since all` puts it back. |

### Goal 16: the manifest commands (`manifests_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestManifestsScannerNeedsNoConfig`             | The scanner runs with no config file, no commit and no plan: the listing carries each manifest's identity, ecosystem and declarations (local paths included), the walk descends while `--root-only` does not, installed dependencies are never scanned, the positional folder resolves against `--root` and a folder that is not there exits 1. |
| `TestManifestsScannerJSONEvents`                | `--log-format json` is this command's machine contract too: one event per manifest carrying path, ecosystem, identity and every declaration with its kind spelled out, plus the summary counts.                                       |
| `TestManifestsScannerStrictGatesBrokenManifests` | The partial-result contract reaching the exit code: a broken manifest is reported while the healthy ones are still listed and the run exits 0; `--strict` refuses the same repository with the partial result still printed.          |
| `TestManifestsWriterEditsInPlace`                | A two-ecosystem batch rewrites only the version text being changed, byte-for-byte elsewhere, in `package.json` (own version plus two fields) and `go.mod` (a require, no own version to write); re-running the same edits converges to `manifest unchanged`. |
| `TestManifestsWriterRedirects`                   | `--replace` adds the local-folder directive and an empty path removes it, and the scanner reads back what the writer just wrote, which is the pair's whole contract.                                                                  |
| `TestManifestsWriterOutcomesReachTheExitCode`    | The three outcomes mapped onto exit codes: missing is tolerated (0) until `--strict` (1); a path no writer covers exits 1 while the usable manifests of the same batch are still written; a malformed `--set`, an invocation with nothing to write, and one with no manifest are usage errors (2). |
| `TestManifestsCommandWordsKeepTheirScripts`      | The command words are reserved: the bare `dispat scanner` is the command even where the config defines a `scanner` script, while `dispat run scanner` still reaches the script.                                                       |
| `TestManifestsReplacerNeedsNoConfig`             | The replacer runs over files that are not manifests at all, with no config file and no git history: every occurrence replaced, the paths resolved against `--root`, and repeated `--sub` values applied in order, each over what the last left. |
| `TestManifestsReplacerOutcomesReachTheExitCode`  | Nothing to write, a spec with no separator and no file to work on are usage errors (2); a pattern matching nothing is tolerated (0) until `--strict` (1); an unreadable path exits 1 while the usable files of the same batch are still written. |
| `TestManifestsReplacerJSONEvents`                | `--log-format json` carries one event per file with its path and occurrence count, plus the summary splitting applied, missing and skipped.                                                                                            |
| `TestManifestsReplacerWordKeepsItsScript`        | `replacer` is reserved like the other command words, and `dispat run replacer` still reaches a script of that name.                                                                                                                   |

### Goal 17: the `--package` / `--space` / `--group` selection (`filter_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                                     |
|-------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFilterSelectsNamedPackages`                | Every `--package` spelling — one name, comma-separated, repeated, upper-case, a glob, `'*'` — narrows the window to exactly those packages, in graph order.                                                                                       |
| `TestFilterNarrowsTheWindowNeverWidensIt`       | After a release a filtered run does nothing and exits 0, while `--since all` puts every package on the table for the same filter to pick from.                                                                                                    |
| `TestFilterUnmatchedTermsAreErrors`             | A term matching no package exits 1 listing what was discovered — a literal and a glob alike — and each flag's miss names the other when the term belongs there: a space in `--package`, a package (a standalone one included) in `--space`.       |
| `TestFilterSpaceTermStaysInItsSpace`            | A `--space` term selects that space's packages and no others; several terms, a glob and `'*'` union the spaces they match; a package term unions on top.                                                                                          |
| `TestFilterStandalonePackageBelongsToNoSpace`   | A `packages` entry with a path is reachable through `--package` and `--package '*'` and never through `--space`, not even the space whose folder it sits under; naming it in `--space` exits 1.                                                   |
| `TestFilterInfersFromTheInvocationFolder`       | With no terms the folder is the selection: a package folder or any subfolder of it, a space folder, the root and a folder outside every space each select what they should, and the deepest match wins over an enclosing one.                     |
| `TestFilterExplicitTermsBeatTheFolder`          | A term typed on the command line is the whole answer, whichever folder it was typed in — for both flags.                                                                                                                                          |
| `TestFilterRefusesASelectionWithoutTheScript`   | A filter reaching only packages that resolve no command for the name exits 1, the same guard a whole-monorepo run applies.                                                                                                                        |
| `TestFilterStepCommandsSelect`                  | The step commands take the same terms and the same folder inference; a selected package the recomputed plan is no longer releasing is a logged no-op, not a failure; an unmatched term exits 1.                                                   |
| `TestFilterPreviewSelects`                      | Preview takes the same terms and folder inference, and names the selection it found nothing pending for.                                                                                                                                          |
| `TestFilterComputeScopesSuggestions`            | Compute reports and writes only the selected consumers' edges while still detecting against every package's manifests, so a declared edge onto an unselected provider is never proposed for removal; the in-sync line names the scope.            |
| `TestFilterReleaseSelectsPartOfTheGraph`        | A release takes the same terms: `-p core` tags and publishes core alone, `-s apps` that space's package, the graph marks what was left out, and a later unfiltered run releases the rest without re-releasing what is already out.                |
| `TestFilterReleaseWithholdsWhatTheOrderCannotReach` | A selected consumer whose provider is releasing and unselected is withheld (`W230`, naming the provider) and nothing is released; naming the provider too releases both; once the provider is out, the consumer alone is a fine selection.    |
| `TestFilterReleaseStrictRefusesBeforeAnythingRuns` | `--strict` turns the withholding into exit 1 with no tags, no stage scripts and the releasable half of the same selection untouched; without it that half releases; a clean selection is unaffected by the flag.                             |
| `TestFilterReleaseSplitsAVersioningGroup`       | Taking part of a `fixed` group releases it and warns (`W231`) rather than refusing; `--strict` refuses the same selection with nothing released; the next run rides the member left behind up to the group's version (`W210`).                   |
| `TestFilterReleaseInfersFromTheInvocationFolder`| A release run from inside a package folder is that package's release; from the root it is still the whole monorepo.                                                                                                                              |
| `TestFilterReleaseRecordsOnlyWhatReleased`      | The durable records follow the narrowed run: the release commit names only the created tag, only the released package's changelog is written, and no tag exists for a package left out.                                                          |
| `TestFilterStatusSelects`                       | `status` narrows the same plan while still printing every package (`⊝ not selected`, `⊘ withheld until its providers release`), reports `W230` and exits 0, exits 1 under `--strict`, is clean when a space term brings the provider along, and fails on an unmatched term. |
| `TestFilterSelectsAVersioningGroup`             | Every `--group` spelling selects the group's packages wherever they live: a group joined by a space and by a standalone package, a space that versions as its own group, comma-separated and repeated terms, a glob, `'*'`, and a union with the other two flags that selects a package named twice over once; `-g '*'` reaches no independently versioned package. |
| `TestFilterUnknownGroupTermsAreErrors`          | A `--group` term naming nothing exits 1 listing the groups there are, and the three flags name each other: a space in `--group`, a package in `--group`, a group in `--space` and in `--package`; a repository with no groups at all says so.     |
| `TestFilterGroupSelectsForEveryCommand`         | The group term narrows `preview`, `status`, `changelog`, `commit` and `compute` exactly as the other terms do, leaving the other group untouched.                                                                                                 |
| `TestFilterReleaseByGroupNeverSplitsIt`         | Naming a member of a group under `--strict` is refused (`W231`) while naming the group releases every member at once, clean under `--strict`, across a space and a standalone package alike; a later unfiltered run finishes the rest.            |
| `TestFilterPositionalPackagesAreAUsageError`    | A bare package name after `run`, `preview`, `changelog`, `autoversion`, `commit` or `compute` is a usage error (exit 2): the selection is a flag.                                                                                                 |

### Goal 18: Docker through the binary (`docker_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestDockerComputeDerivesTheImageChain`         | `compute` reads an image-to-image edge off a `FROM` line that no config declares, names the file it came from, ignores a base that is not a workspace package, writes the edge under `--write` and greens the `--check` gate; the stated `manifestNames` repository is what lets the chain resolve, since an image's identity is never its folder name. |
| `TestDockerReleaseReconcilesTagsAndCompose`     | A release reconciles both Docker formats at the version stage: the consumer's `FROM` tag and its `COPY --from` image follow the provider's new version, a `COPY --from` naming a build stage is left alone, and the compose file gets the package's own version in the service it builds and every `build.tags` entry, the provider's in the service it pulls, and nothing at all in a port mapping. |
| `TestDockerManifestCommands`                    | The config-free commands over both formats: `scanner` reports a compose file's identity and a Dockerfile's bases with no config, commit or plan; `writer` rewrites a compose tag and own version proved byte-for-byte on disk; a digest-pinned base is skipped rather than failed, and a missing edit still gates under `--strict`. |

### Goal 19: the `autoreplace` command (`autoreplace_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoReplaceEditsEveryCoveredPackage`       | One invocation reaches every package the window covers, writing the fixture back with exactly the edited bytes changed; `{version}` resolves against the plan the binary just computed, in a range and in the own-version field alike; a second pass leaves the working tree untouched. |
| `TestAutoReplaceSelectsLikeEveryOtherCommand`   | The selection is the shared one: a `--package` term, the invocation folder standing in for it, `--consumers` reaching a package that declares nothing itself, and an unmatched term exiting 1.                                          |
| `TestAutoReplaceOnlyUpdatedFollowsThePlan`      | `--only-updated` keeps an edit while the package it names is releasing and drops one naming no package of the workspace; once everything is released the same command writes nothing and says so, exiting 0.                            |
| `TestAutoReplaceManifestScope`                  | `--manifests root` stops at the package folder while `all` reaches a nested manifest, and the own-version write stays on the root manifests under either scope.                                                                        |
| `TestAutoReplaceRedirects`                      | `--replace` adds the local-folder directive across the selection and an empty path restores the declared range.                                                                                                                       |
| `TestAutoReplaceOutcomesReachTheExitCode`       | `--strict` is asked across the sweep (an edit landing in one package of two is clean, one landing nowhere exits 1, and without the flag the same run exits 0); nothing to write, a malformed spec, an unknown `--manifests` value and a positional argument are usage errors (2); an unresolvable `{version}` exits 1 with nothing written. |
| `TestAutoReplaceJSONEvents`                     | The machine contract: one event per manifest carrying its path and the package it belongs to, plus the run's applied/skipped/missing tally.                                                                                            |
| `TestAutoReplaceSyncLock`                       | The syncLock scripts run exactly where a manifest changed, not where it did not, never on a converged re-run, and not at all under `--sync-lock=false`.                                                                                |
| `TestAutoReplaceCommandWordKeepsItsScript`      | `autoreplace` is reserved like every other command word, and `dispat run autoreplace` still reaches a script of that name.                                                                                                             |

### Goal 21: the release guards (`guard_test.go`)

| Test                                       | Claim proven                                                                                                                                                                                                                                                     |
|--------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestGuardAllowBranch`                     | With `run.allowBranch` set, a release on a listed branch proceeds; one on a foreign branch exits 1 naming the branch and the globs, with nothing tagged; a `*` glob reaches slashed branch names (`release/v1`); `dispat status` works on any branch.             |
| `TestGuardAllowBranchRefusesDetachedHead`  | A detached HEAD has no branch name, so it matches nothing, a glob as broad as `*` included, and the run is refused before anything is tagged.                                                                                                                     |
| `TestGuardBehindRemote`                    | In push mode a checkout whose branch is behind the remote (another clone pushed) refuses before any release work with "behind origin/main", tagging nothing; after `git pull --rebase` the same release goes through and the tag arrives on the remote.           |
| `TestGuardBehindRemoteHonoursCommitVerify` | The behind check is another `ls-remote`, so `commit.verify: false` turns it off with the reachability check. The same run then builds, publishes and tags before git rejects the push, which is the wasted release the guard exists to prevent.                   |
| `TestGuardsAreUnsetByDefault`              | Neither guard applies unless configured: an ordinary repository on an arbitrarily named branch, pushing to a remote, releases exactly as before.                                                                                                                  |

### Goal 22: the shell helpers (`if_test.go`, `exec_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                        |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestIfChoosesABranchFromTheEnvironment`        | One chain, four environments: the matching branch runs and the later true one does not, a value that matches nothing falls to `--else`, and an absent variable reaches the same answer as a value that simply differs. |
| `TestIfConditionGrammarEndToEnd`                | All six spellings through the process boundary, including the two the others cannot say: set-but-empty is not "set", and `NAME=` is the only way to ask for empty.                                                     |
| `TestIfPropagatesTheExitCode`                   | The chosen script's code becomes the command's, `--on-failure` replaces it and runs only on failure, and nothing matching with no `--else` exits 0 having run nothing at all.                                          |
| `TestIfRunsInTheInvocationFolder`               | The chosen script runs where the command was invoked, so a relative path in it means what the caller meant.                                                                                                            |
| `TestIfNests`                                   | A branch is shell text, so another `dispat if` inside one is ordinary, which is how a chain grows past what one condition can say.                                                                                     |
| `TestIfIsReservedAndNeedsNoRepository`          | `if` is a command word, never the run shorthand; a missing condition, an unpaired `--then` and a name no environment could carry are all usage exits taken before any config is read.                                  |
| `TestExecResolvesTheSubjectsScript`             | The subject picks the level, and only that level: root, space and package each answer with their own text, a package declaring nothing is a reported miss, and standing in a package folder changes no answer.         |
| `TestExecFallbackWalksTheLayers`                | `--fallback` reaches the top level from a package, the nearer level still wins, a package with none of its own gets its space's, and a name nowhere in the chain reports the whole chain.                              |
| `TestExecEnvironmentFollowsTheSubject`          | The declared env is layered file under space under package and belongs to the subject, not to the folder the command ran in.                                                                                            |
| `TestExecScriptFromCrossesTextAndContext`       | `--script-from` moves the text and leaves the environment with the subject, which is what keeps the crossed form sayable.                                                                                              |
| `TestExecReachesTheReleaseVariablesOutsideARelease` | The reuse claim: `--env both` supplies `DISPAT_VERSION`, `DISPAT_PACKAGE` and `DISPAT_STAGE=exec` with the declared env, `--env dispat` drops the declared half, and the default supplies neither.                 |
| `TestExecComputesNoPlanUnlessAsked`             | With the repository taken away a plan is impossible, so the default scope still working proves it computed none, and `--env both` failing proves that scope is what pays for one.                                      |
| `TestExecPropagatesTheExitCode`                 | The declared script's own code becomes the command's, and `--on-failure` replaces it.                                                                                                                                  |
| `TestExecComposesInsideARunScript`              | The in-flow case: a `run` script calling `dispat exec` hands the inner script the run's `DISPAT_*` variables through the process environment, with no flag.                                                            |
| `TestExecIsReservedAndRefusesBadFlags`          | Every malformed invocation is decided by the flags alone and exits 2, while an unknown package is a runtime failure instead, because those flags were well formed.                                                     |

### Goal 23: the release lock (`lock_test.go`)

The lock is a ref on the remote, so every claim here is read from the bare repository the fixtures push to. What was
true *during* a run is read from a `beforeAll` hook, which runs while the lock is held. The harness disables the lock
for every other scenario (`DISPAT_UNSAFE_DISABLE_LOCK=true` in `runBin`), since most fixtures have no remote at all;
these tests ask for it back.

| Test                                       | Claim proven                                                                                                                                                                                                                              |
|--------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestReleaseLockRoundTrip`                 | The tag is on the remote while the run works and gone from both copies once it is over, release and all; a second run with nothing left to release takes and returns it just the same.                                                     |
| `TestReleaseLockHeldElsewhere`             | A lock already on the remote refuses the run (exit 1) with the remedy in the message, nothing built and nothing tagged, and — the part that matters — the holder's tag object is untouched. The same repository releases once it is freed.  |
| `TestReleaseLockIgnoresCommitForce`        | `commit.force` rewrites a run's own records, never another run's lock: a repository configured to force everything still bounces off a held lock.                                                                                          |
| `TestReleaseLockBlocksConcurrentRuns`      | Two real releases against one remote: the first holds the lock inside a gated hook, the second is refused while it is held and goes through once it is released. No sleeps — the second starts only once the lock is provably on the remote. |
| `TestReleaseLockIndependentOfPush`         | With no release commit configured, the lock is still taken and cleared, and the remote ends with no tag and no branch: the lock is not the release push.                                                                                   |
| `TestReleaseLockWithoutRemote`             | With the lock on, a repository with no remote cannot coordinate and does not release. The cost of the guard, stated.                                                                                                                       |
| `TestReleaseLockKillSwitch`                | Through the binary: `true`/`1`/`TRUE` release a remoteless repository unguarded, while `false`, `0`, an empty value and a typo all keep the lock on.                                                                                       |
| `TestReleaseLockClearedWhateverHappens`    | The lock is given back on every way out: a failed package, a guard refusing the run after the lock was taken, and a SIGINT mid-build.                                                                                                      |
| `TestReleaseLockStaleLocalTag`             | A lock tag left in the clone by a killed run says nothing about who holds the lock, so the next release overwrites it locally and carries on.                                                                                              |
| `TestReleaseLockCleanupFailureIsNotFatal`  | A remote that has become unreachable by the end of the run is reported with the remedy and leaves the exit code to the release itself (0), the stranded tag confirmed on the remote.                                                       |
| `TestReleaseLockAppliesOnlyToRelease`      | `status`, `preview`, `run`, `changelog`, `autoversion`, `commit` and `scanner` take no lock, which is why they still work in a repository with no remote.                                                                                  |
| `TestReleaseLockIsNotAReleaseTag`          | The lock is on HEAD while the plan is computed, so a `{version}` tag format — the broadest there is — still reads 0.1.0 as the baseline and releases 0.2.0.                                                                                |

## Regression fences

Two planner behaviours are subtle enough to earn dedicated guard tests: each pins a property whose violation once
produced a plausible-looking but wrong plan, so a regression fails exactly one clearly-labelled test.

1. **A rejected `Release-As` pin must not swallow a sibling unit's bump.** Every pin guard reports its error and falls
   back to the ordinarily computed version (§16's unit-scoped blast radius): the sibling releases at its computed
   version, and a lone rejected pin releases nothing. Guarded by `TestPlanRejectedPinFallsBackToTheComputedBump` (and
   unit tests in `internal/plan`). The failure mode being fenced: a package publishing and tagging its unchanged
   baseline while silently dropping a `feat` that shared the commit with the bad pin.
2. **A propagated graduation transition must graduate the dependant.** Transitions bypass the graduation guard (a
   propagated *bare* `stable` is still suppressed), so `release(core)%beta>stable%%beta>stable++*` ends the whole train.
   Guarded by `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` (and unit tests in `internal/plan`). The failure
   mode being fenced: dependants left on the train (W200/W206) by the exact form the configuration page documents for
   ending one.

## Bug fences

Guard tests for defects found by review or by building the feature that exposed them. Each one names the defect, the
test that would now fail, and where that test lives, so a regression fails exactly one clearly-labelled case rather
than showing up as a puzzling behaviour change somewhere downstream.

| Defect                                                                                                                                                                                                          | Guarded by                                                                                                                    | Where |
|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|-------|
| **`syncLock` never fired for a space that reconciles nothing.** The scripts were gated on a file having changed, and a block with neither reconciling strategy produces no such signal, so the mode was unreachable. | `TestAutoVersionSyncLockOnly`, `TestAutoVersionSyncLockOnlyStandalone`; `TestSyncLockRunsWithNeitherStrategy`, `TestSyncLockOnlyStaysSerialised`, `TestSyncLockOnlyBudgetConfigurable` | `autoversion_test.go`; `internal/release` |
| **`dispat autoversion --sync-lock` read the space's `autoVersion` block instead of the policy the flags resolved.** A flag-forced reconciliation wrote files whose lock nothing then regenerated.                   | `TestAutoVersionPolicyFlagsStillRunSyncLock`                                                                                  | `autoversion_test.go` |
| **A folder the walk could not enter failed the whole release.** A replace rule's file walk returned the error instead of stepping over the sub-tree, so an unreadable `node_modules` aborted a release no rule was going to touch. | `TestReplaceRuleStepsOverAnUnreadableFolder`, and `TestReplaceRuleFailsOnAnUnreadablePackageFolder` for the case that *must* still fail | `internal/release` |
| **A stale-rule warning (W222) on every re-run.** After the first pass the text a rule looks for is gone, which is indistinguishable from "never matched" without checking whether the file already reads the way the rule wants. | `TestAutoVersionReplaceStrategy` (silent), `TestAutoVersionReplaceRuleMatchedNothing` (loud); `TestReplaceRuleIsQuietOnAnAlreadyReconciledFile` | `autoversion_test.go`; `internal/release` |
| **W222 for every package a space-wide rule does not apply to.** One rule over `README.md` warned once per package without one, drowning the case that matters. Found by running a release on a scratch repository. | `TestReplaceRuleSelectingNoFileIsNotStale`                                                                                     | `internal/release` |
| **A provider-scoped rule reading files in a package with no providers.** The rule expanded into nothing, but its globs still selected files to substitute nothing into. | `TestReplaceRuleWithNoProvidersSelectsNothing`                                                                                 | `internal/release` |
| **One run rewrote the config twice and lost the pre-edit backup.** `compute --write` wrote each affected key on its own, so accepting a top-level edge next to a `packages` entry's removal saved the first write's output as the "previous" copy. Found while adding the baselines, which touch a second key of the same file. | `TestComputeWritesOneBackupPerFile`, `TestComputeSeedsInitialsFromManifests`; `TestReplaceKeysOneWritePerFile` | `internal/app`, `compute_test.go`; `internal/config` |
| **A package with no parseable manifest never became a name owner**, so `manifestNames` could not make it visible to `dispat compute`.                                                                             | `TestAutoVersionManifestNamesMakeAnEdgeVisible`; `TestComputeStatedManifestNamesDeriveEdges`, `TestComputeStatedNameOutranksADeclaredOne` | `autoversion_test.go`; `internal/app` |
| **A replacement landing inside a binary file.** A glob reaching a PNG that happens to contain the version text would have corrupted it.                                                                            | `TestAutoVersionReplaceStrategy`; `TestReplaceRuleSkipsBinaryAndOversizedFiles`, `TestSubstituteRefusesABinaryFile`             | `autoversion_test.go`; `internal/release`, `pkg/writer` |
| **An empty `find` matching at every position.** Both the API and the command line refuse it rather than shredding the file.                                                                                        | `TestSubstituteRefusesAnEmptyFind`, `TestSubstituteBytesIgnoresAnEmptyFind`, `TestParseSubSpec`                                | `pkg/writer`, `internal/cli` |
| **Two span replacements covering the same bytes**, or spans queued against a file a writer also regenerated whole: the result would have depended on the order they were queued in.                                | `TestReplacerRefusesOverlappingPatches`, `TestReplacerRefusesSpansOnARegeneratedFile`                                          | `pkg/writer` |

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
