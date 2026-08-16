# Integration test plan

This module (`tests/integration`) is the black-box integration suite for the dispat CLI. It compiles the real binary
from `services/dispat`, drives it against disposable git repositories exactly as a user's shell would, and asserts on
the three outputs a release run actually has: **git state** (tags, commits, file contents), **JSON log events**
(`--log-format json`, the machine-readable contract CI ingests), and, where *timing* rather than mere ordering is the
claim, **nanosecond-resolution execution timelines** recorded by a purpose-built probe.

## Goals

Thirty-five goals across thirty-seven test files, one file each except goal 21, which the two shell helpers split
between `if_test.go`, `if_changed_test.go` and `exec_test.go`. They are grouped by what they are about rather than by the order they were written
in, so a reader looking for "how does a plan get computed" or "which command does what" lands in one place.

### Planning and versioning

1. **Plan logic** (`plan_test.go`): prereleases, cancels, holds, catch-up, provider-failed and consumer-failed runs,
   and as many weird cases as earn their keep, including that scripts execute *according to* the plan (a held or
   cancelled package runs nothing; a resumed one runs exactly once).
2. **Space versioning modes** (`versioning_test.go`): all seven modes driven side by side across multiple runs:
   `independent`, the full-version `fixed`/`fixedSparse`, and the partial `fixedMajorMinor`/`fixedMajor` pairs that
   hold only a prefix in common. Rides and their "no changes" changelog entries, sparse alignment, versions diverging
   again below the shared part, the single shared prerelease train against a train that stays local, failed-ride
   catch-up on the stable line and mid-train, holds and pins under a shared version, mixed shared depths in one
   group, and no bleed between modes.
3. **Repository-scoped fatal errors** (`fatal_test.go`): the §16 bucket that aborts a run whatever `commitErrors`
   says, each constructed for real: a dependency cycle (E200), duplicate version tags (E191), and a shallow clone
   (E196). These are the cases where a partial release would be worst, so each asserts the non-zero exit, the code in
   the events, and that nothing was released or executed.
4. **Change-scope ignore** (`ignorescope_test.go`): which of a package's own files make a scopeless commit address it.
   A commit touching only ignored files releases nothing and says so (W131), one ordinary file among them brings the
   package back, and the levels (repository, space, package) add up, with only the package able to re-include what a
   broader level excluded and only for itself.
5. **Release edge cases** (`edgecases_test.go`): the cases that sit between two features, where each feature is right
   on its own and the interaction is the question. An exact `Release-As` naming a prerelease, where the pin guards
   meet the channel rules; a window where a provider and its consumer each changed for their own reasons with no
   propagation syntax written, which both auto-versioning strategies, the version scripts and the changelog have to
   account for with no `DueTo` link to follow;
   a package joining a versioning group with no version, and one joining with a version that outranks the group's;
   and the boundary where `revertOnFail` stops. Each one fails by producing a *plausible* release rather than an
   error, which is what makes them worth a file of their own.

34. **Versioning `none`** (`versioning_none_test.go`): the mode that leaves the release flow entirely. A `none`
    package is never versioned, tagged, changelogged or published, runs scripts from the default `dispat run`
    window whenever it has pending changes (it always does: nothing consumes its window), may depend on releasable
    packages (including through a permanent local link) and is refused as a provider to any releasable package
    at config load. Also: the graph's script-only line, an inert `Release-As` reported as W238, inert release-only
    settings, and explicit `--package` selection answered out loud.
35. **Spaces spanning several folders** (`spacepaths_test.go`): the list form of a space's `path`. Discovery and
    release cover every listed folder's packages; each folder's space config file loads, merging in list order;
    the first folder anchors the login and `exec --in`; config resolution from inside a later folder still finds
    the root; `--space` and folder inference reach every folder; the list's refusals (duplicates, nesting)
    surface through the binary; and a `none` space spanning two folders runs scripts in both without ever
    tagging. Per-folder `.dispatexclude` and the package-name collision across folders are pinned by unit tests
    in `internal/config`.
36. **Declared version groups across spaces** (`versiongroups_test.go`): the sparse and partial-sparse modes as
    declared cross-space groups, the override ladder detaching a member that sets its own `versioning`, a shared
    prerelease train with one counter and a W236 channel conflict, the versioning-`none` refusal through the
    binary, `--group` selection under a partial mode, and divergent per-member `tagFormat` as defined behavior:
    one shared version, each member spelling its tag its own way.

### Scheduling and execution

6. **Concurrency** (`concurrency_test.go`): stable tests *guaranteeing* the budgets work. With concurrency 4 and five
   packages, the fifth's work starts exactly after one of the first four finishes; independent packages are picked up
   concurrently while dependants are awaited.
7. **Execution order by dependency graph** (`order_test.go`): scripts run in the order the graph dictates, under both
   `isBuildWaitingPublish` settings.
8. **Interruption** (`interrupt_test.go`): a SIGINT mid-run shuts the run down gracefully through the real signal
   handler: the in-flight script is killed, remaining packages report `cancelled` rather than `failed` or `skipped`,
   nothing is tagged for work that did not finish, and the next run releases the cancelled packages at the version
   they were owed.
9. **The script frames** (`hooks_test.go`): every stage sits inside a frame of hooks, and the frames nest: nine
   per-package hooks around the version, build and publish stages, the announce frame after a publish, the
   `flow.onFail` / `flow.onSkip` outcome scripts, the once-per-space login gate, and the run-level bracket around the
   whole thing. What these prove is *authority* rather than mere ordering: a hook before the point of no return may
   fail its package, one after it may only warn, and the same split decides where `revertOnFail` applies, how far a
   login failure reaches, and how script outputs accumulate across stages and hooks.

### Configuration

10. **Config loading, resolution and options** (`config_test.go`): which file the binary picks when no `--config`
    names one and how far it climbs to find it, a flag beating the file at *runtime* rather than in the parsed
    struct, a custom shell actually being invoked, an unknown key stopping the run, the `commitErrors` policy, the
    parser options, initials baselines, a fused prerelease tag format written and read back, a configuration split
    across files with `$ref`, and the rejections the layers carry, each landing before any work is done.
11. **The static `env` layers** (`env_test.go`): plain environment variables declared at the top level, on a space
    and on a package, merging with the most local winning. The part only the binary can witness is that a key arrives
    with its case intact and that a `$DISPAT_VERSION` reference expands against the package the script runs for. The
    refusals are the load-bearing half: a script may trust `DISPAT_VERSION` precisely because no static key is
    allowed to shadow it. The `.env` file sits beside them: read from the current directory into the run's own
    environment, under the environment and under the config's `env`, and reaching dispat's own reads as well as the
    scripts'.
12. **The configuration ladder from the root down** (`levels_test.go`): the root file is the bottom layer of the same
    fold a space and a package go through, so a space-shaped setting written once at the top reaches every space and
    every standalone package. Each level below can still say otherwise, including saying `false` against a `true`,
    which is what makes the boolean options three-state rather than plain.
13. **Per-package overrides, versioning groups and `.dispatexclude`** (`overrides_test.go`): the layered configuration
    through the binary: a `packages` entry replacing one flow entry while the sibling keeps the space's, the in-folder
    config file beating the entry, `.dispatexclude` exclusions, a declared `versionGroups` group spanning two spaces
    to one version and its convergence, per-package changelog/GitHub record policies, the concurrency weight, the
    config ascent, and `scripts` defined at each of the three levels.
14. **The top-level `packages` section** (`packages_test.go`): a `packages` entry with a `path` releasing as a full
    package outside every space, the standalone path config errors, provider lists declared in an entry or an
    in-folder config file ordering the graph like top-level edges, `src` narrowing change detection, and
    `dispat compute` editing each declaration where it lives.
15. **Dependency edges declared by a space** (`spacedeps_test.go`): a space states the edges of its own packages next
    to the space, and every declaration merges into one graph. The rule that makes the level worth having is that an
    edge must touch the space it sits in: one touching neither end is refused before anything runs.

### The commands

16. **Release records** (`records_test.go`): the durable artefacts themselves: changelog files accumulating across
    releases above pre-dispat content, annotated tags with their messages and targets, alias tags, GitHub releases,
    and commit mode's release commit, tag placement and push against a real bare remote. It is also the home of the
    failures *after* the point of no return, where the artefact is already in a registry and nothing dispat does can
    take it back: a tag (E220), a tag at a foreign commit (E221), a record (E222), the release commit (E223) and the
    push (E224) each failing there, plus the alias tag (W232) that deliberately is not one of them. None of them
    fails a package or stops the run; each is recorded and the run finishes what else it owed.
17. **The `init` and `preview` commands** (`commands_test.go`): the starter config the very next `status` can load,
    the pending release notes on stdout, and the CLI surface itself (per-command `--help`, the platform in
    `--version`).
18. **The `dispat run` command** (`run_test.go`): a script executed inside changed packages over the dependency graph
    with the full environment, resolved per package through the three `scripts` levels, the `dispat <script>`
    shorthand, the `--package`/`--space` selection, the `--since` window and the `--consumers` expansion and how they
    compose, the `--on-error` policies, the concurrency budget, and cross-package output carrying.
19. **The standalone step commands** (`standalone_test.go`): `dispat changelog`, `dispat autoversion`,
    `dispat commit` and `dispat github` through the binary: the shared package selection, changelog idempotence
    (W226), the in-flow scenario where nested step commands land the changelog inside the tagged commit, the
    `--tag`/`--push` committer identity and remote delivery, and the window flags the steps share with `dispat run`.
20. **The `--package` / `--space` / `--group` selection** (`filter_test.go`): the one selection every package command
    shares: the term spellings and their globs, the invocation folder standing in for the terms nobody typed, the
    filter narrowing a window and never widening it, and partial releases, where publish order withholds a consumer
    whose provider was left out (`W230`) and a split versioning group is warned about and released (`W231`).
21. **The shell helpers** (`if_test.go`, `if_changed_test.go`, `exec_test.go`): the two commands that run one script
    instead of sweeping a selection. `dispat if` picks a shell string from a condition on the environment, the
    filesystem (`--file`/`--dir`) or the repository (`--changed`); `dispat exec` runs one *declared* script, where one
    subject decides both which level is read and whose environment the script gets. The pair's load-bearing claim is
    that a declared script reading `DISPAT_*` becomes runnable outside a release. Both take a place in the monorepo
    the same way, `pkg:`, `space:`, `root` or `cwd`, on the subject, on the script source and on the folder the script
    runs in, so the second claim is that each of the three moves only its own half: `--for cwd` infers a subject
    without a plan, `--script-from` still leaves the environment alone, and `--in` changes nothing about resolution.
    The third is `dispat if`'s cost, which stays nil until an `--in` names something only a configuration can place or
    `--changed` asks about the repository itself. The fourth is `--changed`'s selection: the same window, filter and
    consumer expansion every sweeping command uses, composed so that `--consumers` reaches downstream of the changes.
22. **Self-update** (`selfupdate_test.go`): dispat replacing its own binary, which is the one thing no other area can
    witness, because it is the one command that overwrites the file it is running from. Two binaries are built at two
    versions and a fake releases API hands one out.

### Manifests and editing

23. **The `compute` command** (`compute_test.go`): everything the binary derives from the manifests. The dependency
    graph: the detect/apply/check loop with its backup and convergence, `keep` and removal semantics, and the W220
    ambiguity reaching the JSON events. The baselines: `initials` entries seeded from the versions the manifests
    declare, against real tags. And the writes a `$ref` redirects: into the fragment that holds the key, or refused
    when a fragment and the keys beside it compose one value.
24. **Native auto-versioning** (`autoversion_test.go`): files reconciled by the binary at the version stage, under
    either of the two strategies or neither: range reconciliation under the match policy, own-version writes, the
    W192/W197/W203/W221 diagnostics, literal replacement over files nothing parses, and the serialised `syncLock`
    slot.
25. **The manifest commands** (`manifests_test.go`): `dispat scanner`, `dispat writer` and `dispat replacer`, the
    `pkg/scanner` and `pkg/writer` libraries exposed as commands. What only the binary can witness: that they run
    with no config file, no commit and no plan at all, that their outcomes reach the process exit code, and that the
    verify gates (`--verify-unlinked`, `--verify-linked`, `--forbid-range`, `--require-range`), the link sweep
    (`--drop-links`) and the build-counter write (`--set-build`) hold their contracts over a process boundary.
26. **The `autowriter` command** (`autowriter_test.go`): `dispat writer`'s edits applied to the packages the plan
    selects, including the edits derived from the workspace itself (`--set-local`, `--link-local`).
27. **The `autoreplacer` command** (`autoreplacer_test.go`): a replacement fanned out across the packages the
    plan selects, rendered once per workspace provider a covered package declares.
28. **Docker through the binary** (`docker_test.go`): the ecosystem dispat was built around and the last one it could
    read: an image-to-image edge derived from a `FROM` line nobody wrote into the config, and a release reconciling
    the consumer's `FROM` and `COPY --from` tags and a compose file's `image` and `build.tags`.

### The guards

29. **The release guards** (`guard_test.go`): the two refusals that stop a release before it starts.
    `run.allowBranch` turns a branch list into a precondition; the push-mode behind-remote check compares the
    checkout against the branch it would push to and refuses a stale one. The pair is also proven to be off unless
    asked for.
30. **The release lock** (`lock_test.go`): one tag on the remote decides who releases. Two runs against one repository
    is not a race dispat can win by being careful, so it refuses to enter it: the first to push the lock tag releases,
    the second is told to come back later, and the tag is gone by the time either exits.

### Correcting the record

31. **Corrections and reverted changelogs** (`corrections_test.go`): a release record is written in a commit message,
    and a commit message cannot be rewritten once it is pushed. `Edits` restates a named record and `Deletes`
    discards it, both reaching only work no package has released yet. The claims a release depends on: the corrected
    record decides the version and the changelog, a correction of released work is a visible no-op nothing can hide,
    the newest correction of a target wins, a correction narrows a record but never widens it, and a correction of a
    correction undoes it. `Reverts` closes the file: it takes a reverted entry and its revert out of the changelog
    while both still count toward the bump.

### Composing the configuration, and choosing what records

32. **References naming several files** (`multiref_test.go`): a `$ref` may name a list of files, read in order and
    merged: objects key by key with the later file winning, lists end to end. What a split configuration must keep:
    every fragment on the traced record, environment variable case through the merge, the keys beside the reference
    outranking every file it named, and record lines from two fragments arriving as one list in order. The refusals
    are configuration errors, so they stop the run before a tag, a commit or a script: no files at all, a name that is
    not a file, files holding different kinds, a missing file, and a cycle closed by any of them. `compute --write`
    refuses a key merged from several files rather than choosing one to write to.
33. **The channels a record reaches** (`channels_test.go`): `changelog.channels` and `github.channels` choose which
    releases are recorded at all (the stable line, one named prerelease channel, or every prerelease) while the
    releases themselves are still planned, tagged and published. A line inside an entry carries its own `channels`, so
    one footer says one thing on the betas and another on the stable release, with the sections between them
    unfiltered. The claims that keep it honest: a line's channels combine with its package filters, two policies
    differing only in a line's channels do not share a releaser, the skip is an info event naming the channel, and
    `dispat preview --changelog/--github` shows each body under its own entry format before anything is released.

### Where areas deliberately meet

Five subjects are asserted from more than one goal, on purpose, because a property and the feature that carries it
are different claims:

- **Versioning groups** appear in goal 2 (the modes themselves, per space) and goal 13 (a declared group spanning
  spaces). Goal 5 adds what happens when a package *joins* one, and goal 20 what happens when a selection splits one.
- **The configuration ladder** appears in goal 12 (the root as the bottom layer) and goal 13 (the six layers decided
  by the nearest). The first is about the fold, the second about who wins.
- **GitHub releases** appear in goal 16 (the recorder as a release artefact) and goal 19 (the `dispat github` step
  command). Nothing else touches the API.
- **Folder inference** appears in goal 20 (the resolver, table-driven over every folder a command can be invoked from),
  goal 18 (a `run` invoked from a nested subdirectory) and goal 13 (the shorthand from a package folder). The first is
  the rule; the other two are the two spellings that reach it by different routes.
- **The auto-versioning policy flags** appear in goal 24 (as the release's own version stage, where the claim is that
  overriding the policy still runs `syncLock`) and goal 19 (as `dispat autoversion`, where the claim is that the flags
  reach a space with no `autoVersion` block at all). The rewrite they both observe is the same one; what each asks of
  it is not.

Configs are authored as **typed models** from the public `pkg/models` module and marshalled to JSON by
`harness.WriteConfigModel`. The schema lives in one place, and a test that compiles is a test whose config loads. The
one shape the model deliberately cannot express, an unknown key, is written as `map[string]any` through
`harness.WriteConfigRaw`, because a typo'd key must be *rejected* at load, not silently ignored into a script-less
release.

It deliberately duplicates as little as possible of the unit suites listed in
[`Architecture`](https://yohimik.github.io/dispat/internals/architecture#testing): those cover each
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
- It keeps the slower end-to-end tests out of `go test ./...` for the production modules, while `go.work` keeps builds
  and IDE navigation working across the module boundary.
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

  planning and versioning
  plan_test.go              goal 1
  versioning_test.go        goal 2
  fatal_test.go             goal 3
  ignorescope_test.go       goal 4
  edgecases_test.go         goal 5

  scheduling and execution
  concurrency_test.go       goal 6
  order_test.go             goal 7
  interrupt_test.go         goal 8
  hooks_test.go             goal 9

  configuration
  config_test.go            goal 10
  env_test.go               goal 11
  levels_test.go            goal 12
  overrides_test.go         goal 13
  packages_test.go          goal 14
  spacedeps_test.go         goal 15
  versiongroups_test.go     goal 36
  stepwiring_test.go        goal 37

  the commands
  records_test.go           goal 16
  commands_test.go          goal 17
  run_test.go               goal 18
  standalone_test.go        goal 19
  filter_test.go            goal 20
  if_test.go                goal 21 (dispat if)
  if_changed_test.go        goal 21 (dispat if --changed)
  exec_test.go              goal 21 (dispat exec)
  selfupdate_test.go        goal 22

  manifests and editing
  compute_test.go           goal 23
  autoversion_test.go       goal 24
  manifests_test.go         goal 25
  autowriter_test.go        goal 26
  autoreplacer_test.go    goal 27
  docker_test.go            goal 28

  the guards
  guard_test.go             goal 29
  lock_test.go              goal 30

  correcting the record
  corrections_test.go       goal 31

  composing the configuration, and choosing what records
  multiref_test.go          goal 32
  channels_test.go          goal 33

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
does not, whatever any script's duration, so they cannot flake. *Overlap* assertions rest on sleeps (100-400
ms) one to two orders of magnitude above process-launch jitter. The suite passes repeated `-count` runs and
`-race`.

## Coverage matrix

### Goal 1: plan logic (`plan_test.go`)

| Test                                                      | Claims proven                                                                                                                                                                                                                                               |
|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestPlanCancelSemantics`                                 | Cancel discards pending work irreversibly (post-cancel fix releases 0.0.1, not 0.1.1); a spent cancel warns (W170); a cancelled/no-op release run executes zero scripts.                                                                                    |
| `TestPlanRequireRelease` | `--require-release` is the CI gate over the empty plan. A run with nothing pending exits 0, which is right at a terminal and wrong for a pipeline stage whose point is that this run publishes something; the flag turns it into a refusal. What counts is what will actually ship, so a package the plan holds back does not satisfy it. |
| `TestPlanHoldResumeAndReleaseAsAuto`                      | Hold reports the withheld version (W154) and excludes the package from *execution*, not just tagging (zero script runs while held); resume releases at accumulated `max()` with exactly one build; redundant `auto` warns (W158).                           |
| `TestPlanExactPinGuards`                                  | E153 (not greater), E157 (major jump > 1), E154 (multi-package pin), each in an isolated repo so a rejected pin cannot collide with earlier tags.                                                                                                           |
| `TestPlanRejectedPinFallsBackToTheComputedBump`           | A rejected pin has §16's unit-scoped blast radius: E156 fires, the bad unit contributes nothing, and the sibling `feat` still releases at its computed 0.1.0 (a regression fence; see Regression fences).                                                   |
| `TestPlanConsumerFailureCatchesUpAfterProviderPublished`  | Consumer fails while provider publishes; the next run catches the consumer up at the owed version, labelled W193, provider not re-released; a third run converges.                                                                                          |
| `TestPlanProviderBuildFailureBlocksConsumerThenHeals`     | Provider fails to build; consumer is blocked (W194), never attempted; after the fix both release in one run, with neither W194 nor W193.                                                                                                                    |
| `TestPlanCatchUpWholeHistoryForNeverReleasedConsumer`     | A package created *after* a provider's propagating commit still catches up on its first ever run; an untagged package's window is the whole history.                                                                                                        |
| `TestPlanPrereleaseTrainWeirdCases`                       | `^%beta` cannot drag a stable consumer (W208); `^%beta++1` brings it onto the train; a multi-package direct transition graduates the whole train; the graduated train converges.                                                                            |
| `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` | A propagated `beta>stable` *transition* graduates the dependants still on the named train (the `release(core)%beta>stable%%beta>stable++N` form configuration.md documents), and the graduated train converges (a regression fence; see Regression fences). |
| `TestPlanChannelOnlyReleaseAndEntryPatch`                 | A release directive that only moves the channel is still a release, explained by W202; entering a prerelease channel with nothing pending takes the §11.4 entry patch, explained by W204, and its scripts execute.                                          |

### Goal 2: space versioning modes (`versioning_test.go`)

| Test                                                 | Claim proven                                                                                                                                                                                                                                                              |
|------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningFixedSpaceLifecycle`                  | Four runs over a fixed space next to an independent one: a change to either member releases both at one version (W234 on the rider, "no changes" changelog entry, no leaked notes), quiet runs converge, and the independent space never moves with any of it.            |
| `TestVersioningFixedSparseLifecycle`                 | Sparse across four runs: only changed members release (no W234), an unchanged member keeps its version, its first change jumps it to the space version, and a joint change lands both on one shared next version.                                                         |
| `TestVersioningFixedSharedPrereleaseTrain`           | A fixed space runs a *single* train: one member's `%beta` takes the whole space to `beta.0`, later work continues it to `beta.1` for both, one member's graduation ends it for both, and the graduated space converges.                                                   |
| `TestVersioningFixedRideFailureThenAlignmentCatchUp` | A ride can fail like any release: the changed member publishes, the rider fails, and the next run aligns the rider at exactly the space's published version (W234) without re-releasing anyone; a third run converges.                                                    |
| `TestVersioningFixedRideFailureMidTrainHealsOntoTheTrain` | The same catch-up while the group is mid-prerelease-train, where a shared version, a train and a failed leg meet. The laggard joins the train at the position it was owed rather than jumping to a stable core the group never published, and the later graduation carries it off the train with everyone else, which is what proves it was on it. |
| `TestVersioningCrossSpaceDependencyIntoFixedSpace`   | A caret from an independent provider into one fixed-space member: the member gets an ordinary DueTo release (version task, `DISPAT_UPDATED_*`), its space mate rides to the same version with no version task; edges stay package-scoped where versions are space-scoped. |
| `TestVersioningFixedHoldAndResume`                   | `Release-As: none` on one member keeps only it back; the resume aligns it to the space's published version.                                                                                                                                                               |
| `TestVersioningFixedExactPinMovesTheSpace`           | An exact pin naming one member moves the whole space to the pinned version; the pin guards (E153) keep applying to the shared version afterwards.                                                                                                                         |
| `TestVersioningFixedSpaceExecutesEveryMemberScript`  | A ride is a full release at the execution level: build scripts run for the rider too.                                                                                                                                                                                     |
| `TestVersioningFixedConflictResolutions`             | The two fixed-space conflict warnings: competing exact pins resolve to the newest with W235 (the loser must not also release), and members resolving to different channels release as one channel with W236.                                                              |
| `TestVersioningFixedMajorLifecycle`                  | Six runs over a `fixedMajor` space: a patch and a minor each move only their own package (no W234), a breaking change moves the whole group to one major with a ride whose changelog entry reads "on one major version" and carries no leaked notes, the group converges, and it diverges again below the major.  |
| `TestVersioningFixedMajorSparseLifecycle`            | The sparse variant: the unchanged member never rides across a major bump (no W234), and its own next change joins it to the shared major at the start of its own line (`1.0.0`, not a continuation of `0.x`).                                                             |
| `TestVersioningFixedMajorMinorLifecycle`             | One depth further in: a patch stays with its package while a minor and a breaking change each move the whole group, the ride's entry reading "on one major and minor version".                                                                                            |
| `TestVersioningFixedMajorMinorSparseLifecycle`       | Depth two, sparse: a member left behind rejoins the shared prefix on its own next change whatever its size, the two are independent again below the minor, and a later minor leaves the other member behind in turn.                                                      |
| `TestVersioningAllModesSideBySide`                   | All seven modes through one repository: the minor separates the depths (shared under `fixed` and `fixedMajorMinor`, the package's own under `fixedMajor`), the breaking change every shared mode passes on, sparse members never ride, the independent newcomer's first change versions from its own empty history (`0.0.1`, not a step off its space mate's `1.0.0`) where every shared mode would have adopted the group's position, and all seven converge. |
| `TestVersioningFixedMajorSharedTrain`                | A train belongs to whatever it moves: a breaking change on `%beta` takes the whole group to `beta.0`, later work continues it to `beta.1` for both, one member's graduation ends it for both, and a `%beta` on a *patch* afterwards stays inside the package that started it. |
| `TestVersioningPartialPinScope`                      | An exact `Release-As` crossing the shared major moves the whole group; one inside the major releases its own package alone, collects no group-level guard (no E153) and drags nobody along.                                                                               |
| `TestVersioningFixedMajorRideFailureThenAlignment`   | A partial-mode ride fails like any release, and the next run catches the laggard up to the group's shared major (W234) at the start of its own line, without re-releasing anyone; a further run converges.                                                                |
| `TestVersioningPartialRideExecutesEveryMemberScript` | A ride under a partial mode is a full release at the execution level too: both members build.                                                                                                                                                                            |
| `TestVersioningMixedDepthGroupUsesTheDeepest`        | A package overriding its space's `versioning` stays in the space's group with a different depth: the group versions at the deepest declaration (so the minor is shared) and W237 explains the sharing the shallower member never asked for.                               |

### Goal 3: repository-scoped fatal errors (`fatal_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                  |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFatalDependencyCycle`      | A cyclic dependency graph loads as config but refuses to plan: exit 1, E200 in the events, no tag, no script, and `status` and `dispat run` refuse too.                                                       |
| `TestFatalDuplicateVersionTags` | Two reachable tags parsing to the same version of one package on different commits (`core@0.1.0` next to a hand-planted `core@0.1.0+dup`) make the baseline ambiguous: exit 1, E191, pending work unreleased. |
| `TestFatalShallowRepository`    | A `git clone --depth 1` of the repository refuses to release: exit 1, E196 in the events, instead of silently planning over truncated history.                                                                |

### Goal 4: change-scope ignore (`ignorescope_test.go`)

Every claim is about what a *scopeless* commit resolves to, since that is the only thing ignoring changes. The
negatives matter as much as the positives here: the feature is one narrowing, not a way to hide a package.

| Test                                                | Claim proven                                                                                                                                                                       |
|-----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestIgnoreScopeKeepsAFolderFromTriggeringARelease` | A commit touching only ignored files releases nothing and reports the inert unit (W131); the same commit plus one ordinary file releases as usual, so one file that counts is enough. |
| `TestIgnoreScopeDoesNotHideThePackage`              | A commit naming the package by scope releases it whatever it touched, and the release commit still stages the ignored files, and the working tree is clean afterwards.                  |
| `TestIgnoreScopeLevelsConcatenate`                  | Repository, space and package patterns all apply; the package re-includes one of the repository's exclusions with `!`, and its sibling does not inherit that.                        |
| `TestIgnoreScopeFileAndKeyAgree`                    | A `.dispatignore` at the repository root and one in a package folder do exactly what the `ignore` key does at those levels.                                                          |
| `TestIgnoreScopeAppliesToSince`                     | `--since` selects from the same file-derived resolution, so the package whose only change was ignored is not selected and its script never runs.                                     |
| `TestIgnoreScopeRefusesAPatternItCannotCarryOut`    | A pattern that means nothing as written (a lone `!`) fails the load rather than silently doing nothing, and nothing is released.                                                     |

### Goal 5: release edge cases (`edgecases_test.go`)

Each of these is a claim about an *interaction* between two features that are each right on their own, and each one
fails by producing a plausible release rather than an error, which is why they are gathered in one file instead of
filed under the areas they touch.

| Test | Claim proven |
|------|--------------|
| `TestEdgePinPrereleaseOfTheRequiredBump` | `Release-As: 1.1.0-rc.0` on a `feat` ships the rc and enters the channel the version names, and the train continues from it. A prerelease ranks below its own core, so comparing versions whole would read the pin as a downgrade, reject it (E156) and release the stable 1.1.0 the operator was holding back. |
| `TestEdgePinPrereleaseBelowTheBumpIsStillRefused` | The other half: measuring the guard on cores is not a way to ship a breaking change as an rc of a minor. E156 still fires and the computed 2.0.0 goes out instead. |
| `TestEdgePinPrereleaseMovesAWholeGroup` | A versioning group runs one train, so a prerelease pin naming one member takes every member onto it (W234 on the rider) rather than graduating the group to stable. |
| `TestEdgeAutoVersionSyncsWithoutPropagation` | Propagation depth is 0 by default, so a provider and its consumer changed by two separate commits release for their own reasons with no `DueTo` link between them. The parsing strategy still reconciles the declared range, because it resolves providers from what the manifests declare rather than from what bumped the package. |
| `TestEdgeAutoReplaceSyncsWithoutPropagation` | The same window against the replacing strategy, which reaches the same answer by a different route: its `{provider}` patterns expand over the package's *configured* providers. |
| `TestEdgeVersionScriptSeesEveryUpdatedProvider` | The same claim from the script side, and the reason the two strategies can no longer disagree: `DISPAT_UPDATED_*` names every provider whose version the package picks up, not only the ones that propagated a bump. Two spaces over one set of commits, one on a hand-written `flow.version` script and one on `autoVersion`, both reconcile. |
| `TestEdgeChangelogRecordsEveryUpdatedProvider` | The widening in the durable record: a consumer released beside its provider gets a `### Dependencies` line carrying the movement (`- core: 1.0.0 -> 1.1.0`), while a package with no providers gets no section at all. |
| `TestEdgeGroupNewcomerWithNoVersionJoinsAtTheGroupVersion` | A package joining a group with no tag of its own releases at the group's version rather than at the `0.0.1` its own history would give, is explained by W234, raises no W233, and converges. |
| `TestEdgeGroupMemberOnAnotherMajorIsReported` | A member carrying a stray `9.0.0` does not join a group on `1.x`: it takes the group to `9.x` in one run, because a group versions from its newest member and none of them may go backwards. The release proceeds, since every version involved is published, and W233 names who decided it. |
| `TestEdgeGroupMinorSpreadIsNotReported` | The negative that keeps W233 readable: members apart by a minor are the ordinary state a failed ride leaves, already explained by W234, and draw no W233. |
| `TestEdgeGroupSparseMemberDecidingTheMajorIsReported` | A package overriding its space's versioning stays in the space's group, so a group can hold members in different modes. The version it lands on comes from every member's tag whatever its mode, so a sparse member can be the one taking everybody to a new major, and that is the case W233 has to name. Sparseness excuses trailing the group, not deciding it. |
| `TestEdgeRevertOnFailStopsAtThePublish` | The boundary the failure model rests on. In one run one package fails at its build and one publishes: the failed one's folder is rolled back, the published one keeps what its build wrote. Past the publish there is nothing to decide and only things to record, so no later failure may revert a folder. |
| `TestEdgeRevertOnFailIsThreeStateAtThePackageLevel` | The bottom rung of the tri-state ladder: a package saying `false` against the root file's `true` keeps its changes while its sibling, which said nothing, is rolled back. A plain bool could not carry that. |
| `TestEdgeRevertOnFailNeverReachesAFailedCommit` | The far side of the same boundary: the release commit fails once every package has published, and with `revertOnFail` on the folder is still left exactly as the release made it. Restoring it would leave the working tree describing versions already out under different numbers, which no later run could tell. E223 is recorded, the tag is written, nothing is rolled back. |

### Goal 6: concurrency (`concurrency_test.go`)

| Test                                                             | Claim proven                                                                                                                                        |
|------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestConcurrencyBuildBudgetEnforced`                             | Budget 4, five independent packages: peak overlap exactly 4; the 5th build starts only after one of the first four ends (three independent checks). |
| `TestConcurrencyPublishBudgetIsIndependentOfBuild`               | Separate stage budgets: in one run, unconstrained builds reach overlap 5 while publishes stay capped at 2.                                          |
| `TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited` | Three independent providers pairwise overlap; their shared consumer's build starts strictly after all three provider builds end.                    |

### Goal 7: execution order by dependency graph (`order_test.go`)

| Test                                                      | Claim proven                                                                                                                                                                              |
|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOrderChainRunsInTopologicalOrder`                    | `base <- mid <- top`: builds and publishes each run in topological order, driven by `dependencies` edges alone.                                                                           |
| `TestOrderBuildWaitsForPublishWhenConfigured`             | `isBuildWaitingPublish: true`: consumer's build starts only after the provider's *publish* ends.                                                                                          |
| `TestOrderBuildDoesNotWaitForPublishByDefault`            | Flag false: consumer's build runs *during* the provider's publish (timing evidence), while the consumer's own publish still waits for it (structural, flag-independent).                  |
| `TestOrderDiamondDependencyConverges`                     | Fan-out/fan-in (`a -> b,c -> d`): `b`/`c` overlap; `d` waits for both, at build and at publish.                                                                                           |
| `TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv` | A `DueTo` consumer runs a version task whose `DISPAT_UPDATED_*` names exactly the live provider; a direct-release package in the *same space with the same versionScript* never runs one. |

### Goal 8: interruption (`interrupt_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                                                                                                                                                                                                |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestInterruptGracefulShutdown` | A SIGINT delivered mid-build (via `harness.StartRelease`/`Proc`) exits non-zero with both packages `cancelled` in the summary events (the killed build is an interruption, not a failure; the never-launched consumer is not `skipped`), nothing is tagged, and the next run releases both at the version they were owed (one tag each, the consumer's build completing on the tsmark log). |
| `TestInterruptStopsARunCommand`  | `dispat run` shares the release scheduler, so a SIGINT mid-script exits non-zero, the package behind the interrupted one never launches, and nothing is released.                                                                                                                                                                                                                          |

### Goal 9: the script frames (`hooks_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestHooksLoginOncePerSpaceAcrossSpaces`              | Two spaces sharing one login *script text* log in once **each**; the gate is keyed by space, not by script.                                                                                                                                                                                                                                                                                                                   |
| `TestHooksLoginFailureIsolatedToItsSpace`             | A failing login fails every publish of its space and none of another space's.                                                                                                                                                                                                                                                                                                                                                 |
| `TestHooksLoginRunsInTheSpaceFolder` | The login runs in the space's own folder, not the folder of whichever member's publish reached the gate first. Two members race it and the script records its working directory into that directory, so where the file lands is the assertion. A login reading a local credentials file sees the same folder on every run only if this holds. |
| `TestHooksLoginOfAStandalonePackageRunsInItsOwnFolder` | A standalone package is its own space, so the folder its login runs in is the package's, not the parent it happens to sit in, which belongs to nobody and may hold unrelated packages. The login reaches it through the root `flow`, the only route it has, since `flow.login` cannot be written on a package entry. |
| `TestHooksOnFailAndOnSkipOutcomeScripts`              | In one failing run: `flow.onFail` fires once for the failed package with `DISPAT_FAILED_STAGE`/`DISPAT_ERROR`, `flow.onSkip` once for the blocked consumer with `DISPAT_BLOCKED_BY`, neither for the package that published; and an onFail sequence whose first command fails still runs to the end (warn-only).                                                                                                              |
| `TestHooksRevertOnFailAppliesAfterVersionStageOnSkip` | The skip-after-version-stage rollback: the consumer's version script dirties its folder, the provider's publish fails, and the skipped consumer's folder is restored.                                                                                                                                                                                                                                                         |
| `TestHooksScriptOutputsCarryAcrossStagesAndHooks`     | The full accumulation contract, hooks included: a `beforeBuild` *hook* export reaches build and publish, the build's export reaches publish (with `DISPAT_OUTPUTS` listing both in export order), and the failing package's `onFail` receives the hook's export **and** what the failed build exported before dying.                                                                                                          |
| `TestHooksRunLevelHooks`                              | The run-level hook frame against a real remote: every hook fires in phase order in the monorepo root, postAll sees the run outcome and the workspace listing, and a quiet second run keeps the commit/push hooks off because their phases never happen.                                                                                                                                                                       |
| `TestHooksRunLevelHooksAreTheReleasesOwn` | The boundary the run hooks live on: `dispat commit --tag --push` makes the release commit, tags and pushes while firing none of them, and the same configuration through `dispat release` fires all seven exactly once. A run hook is a seam into a moment dispat chooses; a flow calling the step command owns that moment already, and `run` means once per run, which nesting the step commands per package could not honour. |
| `TestHooksRunLevelHookFailureSemantics`               | A failing warn-only run hook (postAll) does not fail the run and its sequence continues; the gating beforeAll aborts the run before any release work.                                                                                                                                                                                                                                                                         |
| `TestHooksAllStageHooksFireInOrder`                   | Every one of the nine per-package hooks plus the announce stage fires, in the documented frame order, on a provider/consumer pair; the consumer additionally runs the version stage with its two hooks inside the same frame.                                                                                                                                                                                                 |
| `TestHooksStageHookAuthoritySplit`                    | The documented authority split: failing postPublish and the whole announce frame only warn (exit 0, tag exists), while a failing gating hook (postBuild) fails the package, tags nothing, and fires onFail with the stage that carried the failure.                                                                                                                                                                           |

### Goal 10: config loading, resolution and options (`config_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestConfigUnknownKeyIsRejected`                       | A typo'd top-level key fails the run (exit 1) instead of being silently ignored.                                                                                                                                                                                                                                                                                                                                              |
| `TestConfigFileFallbackResolution`                     | Without `--config` the binary resolves the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists (a model-marshalled config under the yaml name loads and plans); with none present it exits 1 with an error naming what it tried.                                                                                                                                                                   |
| `TestConfigResolutionAscendsToTheMonorepoRoot`         | Without `--config`, resolution climbs from `--root` through its parents; a release invoked from `packages/core` tags and writes changelogs exactly as one from the top and converges, while an explicit `--config` never ascends.                                                                                                                                                                                             |
| `TestConfigGitRepositoryGuard`                         | A config with no git repository around it fails `status` with one clear error before any work, and `init` refuses a `--root` that is not a repository root: no raw git errors, nothing written.                                                                                                                                                                                                                               |
| `TestConfigConcurrencyFlagOverridesFile`               | `--concurrency` beats the file value *at runtime*: measured overlap, not parsed config, is the evidence.                                                                                                                                                                                                                                                                                                                      |
| `TestConfigCustomShellIsUsed`                          | `"shell": ["/bin/bash", "-c"]` actually switches the interpreter (a bashism invalid under `/bin/sh` succeeds).                                                                                                                                                                                                                                                                                                                |
| `TestConfigCustomObjectIsIgnored`                      | A `custom` object at all three levels loads without tripping the unknown-key guard and changes nothing about the release.                                                                                                                                                                    |
| `TestConfigNonPackageScopesReplacesDefault`            | Setting `nonPackageScopes` **replaces** the `["release"]` default: the custom scope becomes exempt, `release` stops being exempt.                                                                                                                                                                                                                                                                                             |
| `TestConfigFusedPrereleaseTagFormatRoundTrips`         | `{name}%v{version}-{channel}{counter}`: `beta0` is written, read back, converges, and the counter continues to `beta1` over three runs; a second space overriding with the normative format keeps its own spelling: the format is a per-space property.                                                                                                                                                                       |
| `TestConfigParserOptions`                              | The top-level `parser` object drives real parsing: a custom type table makes `docs` release, the configured default propagation depth reaches a consumer with no caret, `strictTypes` raises E140 (tolerated under the default `commitErrors`), and an invalid parser value fails the load with exit 1.                                                                                                                       |
| `TestConfigCommitErrorsPolicy`                         | A unit-scoped error (E130) under the default `warn` still releases the sibling work; under `error` the release is refused (exit 1, nothing tagged) while `status` still exits 0 and reports the plan.                                                                                                                                                                                                                         |
| `TestConfigParserQuiet`                                | `parser.quiet` hides the parser's own findings (E140) while the planner's (E130) still print, still counts every diagnostic and reports how many were hidden, still refuses the release under `commitErrors: error`, and `--quiet-parser` overrides the config in both directions. |
| `TestConfigInitialsBaselines`                          | Initials seed the version of a package whose newest tag is unparseable (the pre-last tag is NOT used and the window still starts at the broken tag), an unmatched initials key only warns, and the next release reads the new real tag back.                                                                                                                                                                                  |
| `TestConfigFormatsSmoke`                               | One minimal smoke per config format: the same monorepo releases through the binary under dispat.json, dispat.yaml and the init command's dispat.toml starter; the json leg also pins that `status` reports without tagging.                                                                                                                                                                                                   |
| `TestConfigDispatexcludeSelectsTheConfigFile`           | A folder holding two config files names the one to skip in its `.dispatexclude`, and the surviving file decides: proven at the repository root, in a space folder and in a package folder, each by the tag only that file's format could produce.                                                                              |
| `TestConfigResolutionAscendsPastASpaceFile`            | A space folder's file declares `packages`, like a monorepo of standalone packages does; run from inside the space and from the space folder itself, resolution still reaches the root above, because that root claims the folder.                                                                                             |
| `TestConfigSpaceLayerRejections`                       | What the new layers may not say, each refused before any work: `path` on a space's `packages` entry, `path` and `spaces` in a space file, `packages` on a package entry, and a space `packages` key matching no folder of that space.                                                                                          |
| `TestConfigRefSplitsTheFile`                           | A configuration split across four files with `$ref` (JSON and YAML fragments, one a folder down referencing a fourth beside it) releases exactly as the one-file version, and `--log-level trace` names every file it read.                                                                    |
| `TestConfigRefCycleFailsBeforeAnyWork`                 | A fragment referencing its way back to the root config exits 1 naming the whole chain, with nothing tagged and no script run.                                                                                                                                                                  |
| `TestConfigRefMissingFragmentIsNamed`                  | A fragment that is not there names the file that pointed at it, the key that did, and what was missing.                                                                                                                                                                                       |

### Goal 11: the static `env` layers (`env_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestStaticEnvReachesScripts`                          | The `env` layers (top level, space, package) merge with the most local winning, keys keep their exact case through the binary, a value's `$DISPAT_VERSION` reference expands to the package's own version, and a run hook sees the top-level layer alone.                                    |
| `TestStaticEnvCannotShadowComputedVariables`           | A static key under the reserved `DISPAT_` prefix is a load-time refusal, not a variable quietly ignored: that is what lets a script trust `DISPAT_VERSION`.                                                                                                                                  |
| `TestStaticEnvRefusesUnusableKeys`                     | The two other keys that could never reach a script intact, an `=` in the name and an empty name, are each refused with the reason.                                                                                                                                                           |
| `TestStaticEnvFromFolderConfigFiles`                   | The two in-folder layers, a space folder's config file and a package folder's, reach the scripts with their case intact and the most local winning.                                                                                                                                          |
| `TestStaticEnvReachesTheLoginScript`                   | A space's `env` reaches its login script, which runs once per space in the space folder with no package in view.                                                                                                                                                                             |
| `TestStaticEnvFromARefKeepsKeyCase`                    | An `env` object written in a `$ref` fragment reaches a script with its keys spelled as the fragment wrote them.                                                                                                                                                                                |
| `TestDotenvReachesScriptsAndDispat`                    | A `.env` in the current directory reaches a script, the process environment beats it, the config's `env` beats both, and no value is ever logged.                                                                                                                                             |
| `TestDotenvFileFlag`                                   | `--env-file` replaces the default file, repeats with the later file winning, and exits 1 when a named file is not there.                                                                                                                                                                      |
| `TestDotenvSteersDispatItself`                         | A variable only the environment file defines is expanded by dispat itself, into the changelog footer it writes.                                                                                                                                                                                |

### Goal 12: the configuration ladder from the root down (`levels_test.go`)

Each claim is made through something only one layer could have produced: a build log line only one script writes, a
version only one versioning mode computes, a file only one `revertOnFail` setting leaves behind.

| Test                                      | Claim proven                                                                                                                                                                                    |
|-------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestLevelsRootFlowReachesEverySpace`     | One root `flow` runs for every package of every space; a space and then a package replace the entries they name and keep the rest, so three packages run three different builds and one shared publish. |
| `TestLevelsRootBooleansAreThreeState`     | A root `revertOnFail: true` reaches the space that says nothing, and the space that says `false` against it keeps what its build wrote, the distinction a plain bool could not express.        |
| `TestLevelsRootVersioningAppliesPerSpace` | A root `versioning: fixed` applies under each space's own group: one space's packages move together while the space that opted out is untouched, which is what separates it from `versionGroups`. |
| `TestLevelsRootReachesAStandalonePackage` | A package outside every space is its own space, so the root's `tagFormat` and `flow` reach it through the same fold.                                                                            |
| `TestLevelsSpaceRecordsAndSrc`            | The space level carries `changelog` (a package overriding it, another space keeping the root's) and `src` (a change outside it leaving the package inert, W131).                                |

### Goal 13: per-package overrides, versioning groups and `.dispatexclude` (`overrides_test.go`)

| Test                                         | Claim proven                                                                                                                                                                                                        |
|----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOverridesFlowBuildPerPackage`           | A `packages` entry replaces one flow entry for one package alone: the override's build runs for it, the space's for the sibling, the un-named stages inherit (both publish and tag), and a second run converges.    |
| `TestOverridesFlowScriptResolvesPerPackage`  | One `flow.build: build` reaches three different commands in one release: the package's own `scripts`, its space's, and the file's, resolved most-local-first for whichever package the stage runs for.             |
| `TestOverridesFlowScriptSuppliedByEveryPackage` | A space's flow entry may name a script only its packages define, and the release runs each package's own; removing one package's entry fails the config with an error naming that package.                       |
| `TestOverridesInFolderFileWins`              | The package folder's own dispat.json is the most local layer: its `tagFormat` beats the `packages` entry's, proven by the tag the release actually creates, while the sibling keeps the repository default.         |
| `TestOverridesDispatexclude`                 | A folder listed in `.dispatexclude` is not a package: never released, and a commit scoping it draws the unknown-scope diagnostic (E130) like any non-package name.                                                   |
| `TestOverridesVersionGroupSpansSpaces`       | A declared `versionGroups` group joined by two spaces versions as one: a change in one space rides the other space's package to the same version (W234 on the rider), and aligned members converge on the next run. |
| `TestOverridesVersionGroupSharesOnlyTheMajor` | The same two spaces under a `fixedMajor` declaration: a minor stays inside its own space, and only a breaking change brings both spaces to one major (W234 on the rider), converging afterwards. |
| `TestOverridesPerPackageRecords`             | Record policies resolve per package: one package writes its changelog under an overridden file name, its sibling disables both records, and the GitHub fake receives exactly the enabled package's release.         |
| `TestOverridesPackageConcurrencyWeight`      | A package whose `concurrency` equals the build budget occupies it whole: its build overlaps no other build on the tsmark timeline while the ordinary packages stay free to overlap each other.                      |
| `TestOverridesRunShorthandFromPackageFolder` | The config ascent walks past the package's own (spaces-less) override file to the monorepo root, so the run shorthand keeps working from inside a package folder that carries one.                                  |
| `TestOverridesScriptsAcrossTheLayers`        | A script defined only in a package's in-folder file (found through discovery) or only in a `packages` entry (found in the loaded config) runs in that package alone, while the space's and the file's own scripts reach both packages; a name defined nowhere stays a hard error. |
| `TestOverridesSpacePackagesEntry`            | A space configures one of its own packages through its `packages` map, with no top-level entry involved: the override's tag format reaches that package alone and the sibling keeps the repository default.        |
| `TestOverridesSpaceFile`                     | A dispat config file in the space folder is the space said again and nearer: it replaces the stages and options it names for every package of the space, inherits the rest, and leaves the other space untouched.  |
| `TestOverridesLadderNearestWins`             | All six layers name one package and one key, and the nearest wins (the package's own file); a package no entry names still takes the space file's value, and a farther layer still supplies what nearer ones omit. |
| `TestOverridesSpaceLayerDependencies`        | Edges declared in a space's `packages` entry and in the space file both reach the plan: a provider's bump carries down the chain one layer at a time, exactly as a top-level declaration would.                    |

### Goal 14: the top-level `packages` section (`packages_test.go`)

| Test                                    | Claim proven                                                                                                                                                                                                                                  |
|-----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestPackagesStandalonePath`            | An entry with a `path` releases as a full package outside the space folders: its own flow runs in its own folder, it tags under the repository format, writes its changelog, leaves the space packages untouched, and a second run converges. |
| `TestPackagesStandaloneConfigErrors`    | A standalone path escaping the repository fails the load; a path naming no folder fails discovery with the folder named; both happen before anything is released.                                                                             |
| `TestPackagesDependencyEdges`           | Provider lists declared in a `packages` entry and in an in-folder config file order the graph exactly like top-level edges (`status` dependsOn proves it).                                                                                    |
| `TestPackagesComputeRemoveFromInFolder` | A stale in-folder edge is suggested with its declaring file named, `--check` gates on it, and `--write` removes it from that file (other keys intact, own `.backup`) while a manifest-detected addition still lands in the root list.         |
| `TestPackagesSrcNarrowsChangeDetection` | A package's `src` narrows file-derived change detection: a scopeless commit touching only what lies outside src releases nothing, one touching src releases as before, a package without src keeps its whole folder, and a commit naming the package by scope reaches it wherever its files are. |
| `TestPackagesSrcMustNameAFolder`        | A `src` that could never match (a missing folder, a path leaving the package, the package folder itself) fails the load rather than narrowing the package to nothing.                                                                                                                          |
| `TestPackagesComputeRemoveFromEntry`    | A stale edge under `packages.<name>.dependencies` in the root config is emptied in place (the entry's other keys and the rest of the file survive) and the gate converges.                                                                    |
| `TestPackagesDependenciesCarryKindAndKeep` | A package's own provider list holds everything the top-level object holds: a kind reaches propagation and a `keep` survives compute, both declared next to the package they belong to. |

### Goal 15: dependency edges declared by a space (`spacedeps_test.go`)

The edges are proven through what they order, not through what the config says: a consumer with nothing of its own to
release moves only because a provider's bump travelled down an edge the space declared.

| Test                                              | Claim proven                                                                                                                                                                                                    |
|---------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestSpaceDependenciesOrderTheRelease`            | An edge in `spaces.<name>.dependencies` reaches the graph: the consumer, which says nothing of its own, is carried along by the provider's bump.                                                                |
| `TestSpaceDependenciesCrossSpaceEdge`             | An edge with one end in the space is what the level is for, and either end may declare it: a library's space declares its consumer in another space, and the consumer follows.                                  |
| `TestSpaceDependenciesRefuseAnEdgeItDoesNotTouch` | An edge touching neither end of the space it is written in is refused before anything runs, naming the space, both endpoints and the root object as where it belongs. Nothing is tagged.                        |
| `TestSpaceFileDependencies`                       | The space folder's own config file declares edges too, and they add to the root file's space entry rather than replacing it: only the file's edge can explain the consumer's release.                           |
| `TestSpaceDependenciesComputeEditsThemInPlace`    | `compute --write` corrects a kind and drops a dead edge inside the space's object, keyed by consumer, and appends the newly detected edge to the root object instead. A second run has nothing left to say.     |

### Goal 16: release records (`records_test.go`)

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
| `TestRecordsPushVerifyDisabled`                                   | `commit.verify=false` switches the upfront ls-remote check off: the release work happens and only the push itself fails (E224); asserted against the default in the same test, which fails fast before any work (no tags, no changelog).                                                                                                                              |
| `TestRecordsChangelogDisabled`                                    | `changelog.enabled=false` switches the file recorder off without touching anything else: the release still publishes and tags, no changelog appears.                                                                                                                                                                                                           |
| `TestRecordsCommitModeGithubFinalize`                             | GitHub in commit mode: releases created in the finalize phase, the body documenting the exact commit and tag, the recorder opt-in per package (no export, no release), a `PACKAGE_<KEY>` export overriding commit and `target_commitish`, and `commit.messageFormat` rendering `{packages}`/`{tags}`.                                                          |
| `TestRecordsGitHubAllPackages`                                    | `github.allPackages` gives every published package a release without exporting `DISPAT_EXPORT_GITHUB`, leaving the export to add assets only; the default keeps the export as the per-package opt-in.                                                                                                                                                          |
| `TestRecordsChannelsHoldPrereleasesBack`                          | `changelog.channels` / `github.channels` naming the stable line alone leave a beta tagged and published but unrecorded, while the graduation to stable writes the one entry and the one release covering the window; a per-package override naming the stable line and every prerelease opts back in. |
| `TestRecordsGitHubReleaseExistsIsASkip`                           | A release the repository already carries is a W224 skip rather than the API's 422, so a repeated `dispat github` and the release that follows both converge instead of failing.                                                                                                 |
| `TestRecordsHeaderAndFooterPerEntry`                              | `header` and `footer` belong to the entry, not the file: two releases leave two of each, bracketing that entry's sections, while a multi-line `fileTitle` heads the file exactly once.                                                                                          |
| `TestRecordsReleaseNameSubHeader`                                 | `releaseName` writes an interpolated sub-header under the entry's date line, and the entry stays recognisable by its tag line, so a re-run still skips it.                                                                                                                      |
| `TestRecordsLineFiltersSelectPackages`                            | One configured list serves a whole workspace: `package`, `space` and `group` filters each write to their own packages and to no others, an unfiltered line reaches all of them, and an independently versioned space belongs to no group.                                       |
| `TestRecordsTextExpandsVariables`                                 | Record text interpolates the release's own variables, a value a build script exported, and the process environment, in the changelog file and the GitHub body alike; an undefined name expands to nothing.                                                                      |
| `TestRecordsGitHubBodyOrder`                                      | In a GitHub release the name is `releaseName` while `tag_name` stays the tag, and the body reads header, sections, the `### Release` block, footer.                                                                                                                             |
| `TestRecordsLineOverrideReplacesInherited`                        | A package's own list states what that package writes and does not extend the inherited one, which the other packages still get.                                                                                                                                                |
| `TestRecordsLineWithoutTextIsAConfigError`                        | A line object that selects packages and writes nothing to them fails the load (exit 1), named by its list and index.                                                                                                                                                            |
| `TestRecordsLineShorthandsInAPackageFolder`                       | The three element shapes (a string, an array of strings, an object) decode the same in an in-folder package config as in the root config.                                                                                                                                     |
| `TestRecordsGithubReleasePrereleaseFlagFollowsChannel`  | Against an httptest GitHub API: the same package's releases flip `prerelease: true -> false` across a real beta release and its graduation.                                                                                                                                                                                                                                                                                   |
| `TestRecordsGithubReleaseAttachments`                   | The whole script-output path through the real binary: the build exports `DISPAT_EXPORT_GITHUB` (two files, opting the package into the GitHub release) plus an ordinary output into `$DISPAT_OUTPUT`, later stages see the output as `DISPAT_OUTPUT_*` (plus the `DISPAT_OUTPUTS` listing) and the export under its full name, and both files arrive as assets at the endpoint the created release advertised (`upload_url`). |
| `TestRecordsAliasTags` | The alias feature end to end beside a path-prefixed release tag: the moving ref follows the newest stable release, the exact one is written per release and never moves, and a prerelease writes its own exact ref while leaving the moving one where it is. The aliases reach the remote and stay out of the release commit's subject: `{tags}` names what was released, and an alias is a moving pointer at a release rather than one of them. |
| `TestRecordsCommitFailureStillTags` | The release commit fails after every package published (its publish takes git's index lock, as a concurrent git process would). Tagging still follows, because the tags then point where they would have pointed with no release commit to make, and a published package with no tag is the one outcome the next run cannot recover from: it would read the package as never released and publish the same version again. Reported as E223, the packages stay `published`, the run exits non-zero. |
| `TestRecordsChangelogFailureIsCriticalNotFailure` | A record that cannot be written after the package published (the changelog path is a directory). The split this pins: the package stays `published`, its release tag is written regardless, the next package's changelog is still written, and the loss is reported as E222 with a non-zero exit rather than as a failed package. |
| `TestRecordsAliasTagFailureIsOnlyAWarning` | An alias that cannot be written (force off, and a ref already holding the name) is W232 and nothing more: the run exits **green**, the release tag is unaffected, the existing ref is left alone, and no critical is counted. An alias is a convenience pointer at a release, not the record of one. |
| `TestRecordsTagAtAnotherCommitIsLeftAlone` | A tag dispat can see at the wrong commit is reported (E221) and left there, because a tag moved here would be force-pushed over the copy on the remote and turn one local mistake into everyone's. Reached through `dispat commit --tag-name`, since a release plans its version *from* the tags. |
| `TestRecordsForceRewritesAnUnreachableTag` | A tag on a commit this branch cannot reach is invisible to the planner, so it records nothing dispat can plan around: with force on (the default) the write succeeds and the tag names this release. Force means "do not fail because the ref exists", not "overwrite whatever is there". |
| `TestRecordsPushForceReplacesExistingRemoteTags` | A tag the remote already carries is overwritten rather than skipped forever, closing the window between the check and the push and giving a moving tag its only way to move. The replacement is reported, not silent. |
| `TestRecordsTagFailureDoesNotUnpublishTheRelease` | The post-publish failure model end to end: the run publishes, says so, refuses to move a tag sitting at a foreign commit, carries on through the packages after it, and exits non-zero, without ever calling the published package failed. |

### Goal 17: the `init` and `preview` commands (`commands_test.go`)

| Test                                | Claim proven                                                                                                                                                                                                                                                                                 |
|-------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestCommandsInitThenStatusCompose` | `dispat init --format toml` then a plain `dispat status`: the fallback finds `dispat.toml` with no `--config` anywhere, the starter config loads and discovers the package, and a second `init` refuses to overwrite (exit 1).                                                               |
| `TestCommandsPreviewNotesWindowing` | `dispat preview --package <name>` prints the pending notes (header, sections, entries), reports "no pending changes" once released, errors on an unknown package; and across a prerelease train the preview and each entry narrow to the fresh changeset while the graduation collects the whole train. |
| `TestCommandsPreviewAllPackages`    | `dispat preview` with no filter renders every package with something pending in publish order, keeps quiet packages out, reports "no pending changes" once nothing is pending, and rejects a positional package name (exit 2).                                                               |
| `TestCommandsPreviewRecordBodies`   | `--github` renders what the releases page would receive, under the github entry format rather than the changelog's and without the release block a run adds from what it published; `--changelog --github` prints both under one header, labelled; a record switched off says so; and nothing pending stays nothing pending whichever body was asked for. |
| `TestCommandsHelpIsScopedToTheCommand` | `dispat <command> --help` prints that command's synopsis and its own flags only; the program help lists every command with the global flags alone. Both exit 0 with no config file or repository.                                            |
| `TestCommandsVersionNamesThePlatform`  | `--version` reports the platform alongside the version, so a bug report says which of the release's binaries is running.                                                                                                                     |
| `TestCommandsReservedWordsShadowTheirScripts` | A command word always wins over a run script of the same name, and `dispat run <word>` is how the script is reached instead. Table-driven over the words whose bare form needs arguments, so the command winning shows as the usage exit; the words whose bare form does something observable prove the same rule in their own areas. |

### Goal 18: the `dispat run` command (`run_test.go`)

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
| `TestRunSinceSelectsByCommitScopes`                | `--since HEAD~1` narrows the run to what the last commit addressed (the written scope wins over the changed files, a scopeless unit derives from its files, §6.2), `--since all` selects every package, an unknown revision exits 1, and a package filter narrows whichever window the flag chose, to nothing, honestly, when the two do not meet. |
| `TestRunConsumersExpandTransitively`               | `--consumers` widens a `--since` window with every transitive dependent (the far end of a three-link chain is reached through the middle package, providers still first) while packages nothing depends on stay out, and `--since all --consumers` is a no-op expansion.                                    |
| `TestRunConsumersOnReleaseWindow`                  | The default release window has the same gap under depth-0 propagation, and `--consumers` closes it there too: a `feat(core)` window runs core alone plainly, core + its transitive consumers with the flag.                                                                                                 |
| `TestRunConsumersSkipCascade`                      | An expanded consumer is a full member of the run: a failing provider script skips it transitively under the default `--on-error skip` (exit 1), and `--on-error continue` runs it anyway.                                                                                                                   |
| `TestRunConsumersComposeWithAFilter`               | `--consumers` expands a filtered selection instead of refusing it, and the expansion is not filtered back out: `-p mid --consumers` runs mid and its dependents, in graph order, with core and extra staying out. The folder spelling behaves identically.                                                  |
| `TestRunVersionComponentsOnAPrereleaseTrain` | The three version components split `DISPAT_VERSION` rather than `DISPAT_NEW_VERSION`, so a package mid-train reports the stable release it is heading for: a build tagging an image `1` off a release candidate stays deliberate. |
| `TestRunForwardsArgumentsAfterTheDash` | `dispat run show -- --watch` hands the script what followed the dash, in every covered package: the invocation is one intent about the selection rather than about whichever package the scheduler reaches first. An argument carrying a space arrives whole, a quote or a semicolon in one is text rather than syntax, nothing after the dash appends nothing, and a bare word is still the usage error it always was. |
| `TestRunShorthandForwardsArgumentsToo` | `dispat show -- --fix` is `dispat run show -- --fix`, so the shorthand is not the spelling that quietly drops them. |
| `TestRunMultiCommandScript` | A name bound to several commands runs all of them, in order, as separate shell invocations in the package folder. Two claims only the real binary can make: the commands are separate processes rather than one string dispat joined, which a `cd` in the first proves by moving that shell and nothing else, so the second still writes where the script started; and the order is the written one, per package, which is what makes a sequence worth writing as one. |
| `TestRunMultiCommandScriptArgumentsLandOnTheLast` | Arguments after `--` go to the script's work, which is its last command; the setup steps before it are left as the config wrote them. |
| `TestRunMultiCommandScriptStopsAtAFailure` | The sequence gates its own remainder: the command after a failing one never runs, and the run fails. |
### Goal 19: the standalone step commands (`standalone_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                                                 |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestStandaloneChangelogWritesAndIsIdempotent`  | The changelog command writes the pending entry without releasing; a second invocation is a byte-identical W226 skip; a following release keeps exactly one entry header; an unknown package errors, a converged one is a clean no-op.                        |
| `TestStandaloneStepsInsideAReleaseFlow`         | The dogfood flow: nested `dispat changelog` + `dispat commit --tag` in beforePublish land the changelog inside the tagged commit (the fix under test); the outer run reports W226 and W223 with exit 0, one tag, and a quiet convergence run.                 |
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
| `TestStandaloneGithubFailures`                  | The error paths: a release on a channel `github.channels` does not name publishes nothing and states why; an unresolvable token and a refused verification both exit 1 before any release is created; an API that rejects the creation exits 1. |
| `TestStandaloneCommitPushWithoutRemoteFails`    | `--push` without a remote exits 1, while the local commit and tag it had already made survive.                                                                                                                                                               |
| `TestStandaloneStepsTakeTheWindowFlags`         | The steps take `dispat run`'s window: `--since` picks what a revision addressed, `--consumers` pulls the dependents in, `--on-error` is validated on every sweeping command, and a package tagged by `dispat commit --tag` falls off the recomputed window until `--since all` puts it back. |

### Goal 20: the `--package` / `--space` / `--group` selection (`filter_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                                     |
|-------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFilterSelectsNamedPackages`                | Every `--package` spelling (one name, comma-separated, repeated, upper-case, a glob, `'*'`) narrows the window to exactly those packages, in graph order.                                                                                       |
| `TestFilterNarrowsTheWindowNeverWidensIt`       | After a release a filtered run does nothing and exits 0, while `--since all` puts every package on the table for the same filter to pick from.                                                                                                    |
| `TestFilterUnmatchedTermsAreErrors`             | A term matching no package exits 1 listing what was discovered, a literal and a glob alike, and each flag's miss names the other when the term belongs there: a space in `--package`, a package (a standalone one included) in `--space`.       |
| `TestFilterSpaceTermStaysInItsSpace`            | A `--space` term selects that space's packages and no others; several terms, a glob and `'*'` union the spaces they match; a package term unions on top.                                                                                          |
| `TestFilterStandalonePackageBelongsToNoSpace`   | A `packages` entry with a path is reachable through `--package` and `--package '*'` and never through `--space`, not even the space whose folder it sits under; naming it in `--space` exits 1.                                                   |
| `TestFilterInfersFromTheInvocationFolder`       | With no terms the folder is the selection: a package folder or any subfolder of it, a space folder, the root and a folder outside every space each select what they should, and the deepest match wins over an enclosing one.                     |
| `TestFilterExplicitTermsBeatTheFolder`          | A term typed on the command line is the whole answer, whichever folder it was typed in, for both flags.                                                                                                                                          |
| `TestFilterRequireReleaseCountsOnlyWhatShips` | `--require-release` reads the plan *after* the selection narrowed it, so a package the dependency order withheld (version waiting, on nobody's release list) does not count as a release. That is the case a filtered CI stage must not mistake for one. |
| `TestFilterRefusesASelectionWithoutTheScript`   | A filter reaching only packages that resolve no command for the name exits 1, the same guard a whole-monorepo run applies.                                                                                                                        |
| `TestFilterStepCommandsSelect`                  | The step commands take the same terms and the same folder inference; a selected package the recomputed plan is no longer releasing is a logged no-op, not a failure; an unmatched term exits 1.                                                   |
| `TestFilterPreviewSelects`                      | Preview takes the same terms and folder inference, and names the selection it found nothing pending for.                                                                                                                                          |
| `TestFilterComputeScopesSuggestions`            | Compute reports and writes only the selected consumers' edges while still detecting against every package's manifests, so a declared edge onto an unselected provider is never proposed for removal; the in-sync line names the scope.            |
| `TestFilterReleaseSelectsPartOfTheGraph`        | A release takes the same terms: `-p core` tags and publishes core alone, `-s apps` that space's package, the graph marks what was left out, and a later unfiltered run releases the rest without re-releasing what is already out.                |
| `TestFilterReleaseWithholdsWhatTheOrderCannotReach` | A selected consumer whose provider is releasing and unselected is withheld (`W230`, naming the provider) and nothing is released; naming the provider too releases both; once the provider is out, the consumer alone is a fine selection.    |
| `TestFilterReleaseStrictRefusesBeforeAnythingRuns` | `--strict` turns the withholding into exit 1 with no tags, no stage scripts and the releasable half of the same selection untouched; without it that half releases; a clean selection is unaffected by the flag.                             |
| `TestFilterReleaseSplitsAVersioningGroup`       | Taking part of a `fixed` group releases it and warns (`W231`) rather than refusing; `--strict` refuses the same selection with nothing released; the next run rides the member left behind up to the group's version (`W234`).                   |
| `TestFilterReleaseInfersFromTheInvocationFolder`| A release run from inside a package folder is that package's release; from the root it is still the whole monorepo.                                                                                                                              |
| `TestFilterReleaseRecordsOnlyWhatReleased`      | The durable records follow the narrowed run: the release commit names only the created tag, only the released package's changelog is written, and no tag exists for a package left out.                                                          |
| `TestFilterStatusSelects`                       | `status` narrows the same plan while still printing every package (`⊝ not selected`, `⊘ withheld until its providers release`), reports `W230` and exits 0, exits 1 under `--strict`, is clean when a space term brings the provider along, and fails on an unmatched term. |
| `TestFilterSelectsAVersioningGroup`             | Every `--group` spelling selects the group's packages wherever they live: a group joined by a space and by a standalone package, a space that versions as its own group, comma-separated and repeated terms, a glob, `'*'`, and a union with the other two flags that selects a package named twice over once; `-g '*'` reaches no independently versioned package. |
| `TestFilterUnknownGroupTermsAreErrors`          | A `--group` term naming nothing exits 1 listing the groups there are, and the three flags name each other: a space in `--group`, a package in `--group`, a group in `--space` and in `--package`; a repository with no groups at all says so.     |
| `TestFilterGroupSelectsForEveryCommand`         | The group term narrows `preview`, `status`, `changelog`, `commit` and `compute` exactly as the other terms do, leaving the other group untouched.                                                                                                 |
| `TestFilterReleaseByGroupNeverSplitsIt`         | Naming a member of a group under `--strict` is refused (`W231`) while naming the group releases every member at once, clean under `--strict`, across a space and a standalone package alike; a later unfiltered run finishes the rest.            |
| `TestFilterPositionalPackagesAreAUsageError`    | A bare package name after `run`, `preview`, `changelog`, `autoversion`, `commit` or `compute` is a usage error (exit 2): the selection is a flag.                                                                                                 |

### Goal 21: the shell helpers (`if_test.go`, `exec_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                        |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestIfChoosesABranchFromTheEnvironment`        | One chain, four environments: the matching branch runs and the later true one does not, a value that matches nothing falls to `--else`, and an absent variable reaches the same answer as a value that simply differs. |
| `TestIfConditionGrammarEndToEnd`                | All six spellings through the process boundary, including the two the others cannot say: set-but-empty is not "set", and `NAME=` is the only way to ask for empty.                                                     |
| `TestIfPropagatesTheExitCode`                   | The chosen script's code becomes the command's, `--on-failure` replaces it and runs only on failure, and nothing matching with no `--else` exits 0 having run nothing at all.                                          |
| `TestIfRunsInTheInvocationFolder`               | The chosen script runs where the command was invoked, so a relative path in it means what the caller meant.                                                                                                            |
| `TestIfRunsWhereItIsTold`                       | `--in` moves the chosen script: a path and `cwd` in a repository holding no config file at all, so anything that works there provably read none, plus a missing folder named rather than left to the shell and a malformed value taken as the usage exit. |
| `TestIfReadsTheConfigOnlyForAnInWithANameInIt`  | The line the flag draws, stated as one comparison: `pkg:`, `space:` and `root` place the script correctly, then the config file is broken and only the invocations that had to look a name up notice.                  |
| `TestIfNests`                                   | A branch is shell text, so another `dispat if` inside one is ordinary, which is how a chain grows past what one condition can say.                                                                                     |
| `TestIfIsReservedAndNeedsNoRepository`          | `if` is a command word, never the run shorthand; a missing condition, an unpaired `--then`, a name no environment could carry, two leading conditions at once and a selection flag without `--changed` are all usage exits taken before any config is read. |
| `TestIfFileConditions`                          | `-f` and `-d` in a repository holding no config file at all: a regular file, a folder, a missing path and the wrong kind each answer as the shell's `[ -f ]` and `[ -d ]` would, false rather than an error, and the chosen script's code still passes through. |
| `TestIfFileConditionResolvesWhereTheScriptRuns` | A relative `-f` path resolves where the chosen script runs, after `--in`, so the test and a path inside the script text mean the same file; an absolute path works from anywhere; `--in pkg:` still costs only what `--in` costs alone.                  |
| `TestIfChangedGatesOnTheReleaseWindow`          | Without `--since` the window is the release window: the gate holds while something is pending, stops holding once it is released, and holds again on the next commit, so one invocation converges with the repository's state.                             |
| `TestIfChangedWithSince`                        | `--since HEAD~1` follows what the last commit addressed, a scopeless commit touching no package answers false, `--since all` holds regardless, and a revision git cannot resolve is a failure naming it, never a silent false.                             |
| `TestIfChangedNarrowsToTheSelection`            | `-p`, `-s` and `-g` narrow the gate the way they narrow every command, a named package outside the window is a false said out loud, and a term matching no package is an error rather than a gate that never fires.                                       |
| `TestIfChangedConsumersReachDownstream`         | `--consumers` expands the window before the filter narrows it, so `-p web --consumers` holds when web or anything web transitively consumes changed, the one composition under which the flag can flip the answer at all.                                 |
| `TestIfChangedInfersFromTheInvocationFolder`    | Invoked inside a package folder with no terms, the gate narrows to that package exactly as `run` does; from the root the same invocation asks about everything.                                                                                            |
| `TestIfChangedInRunsElsewhere`                  | `--in` picks where the chosen branch runs and never narrows the window: with only core changed, `--in pkg:web` still holds the gate, and the script runs in web's folder.                                                                                  |
| `TestIfChangedConsumersNeedsASelection`         | From the root with no terms, `--consumers` can never flip the gate's answer, so it is refused as a usage error (exit 2); invoked inside a package folder the selection narrows and the same flags are answered.                                            |
| `TestIfChangedChainsWithElif`                   | `--changed` leads the chain like any other condition: true it wins even over a true `--elif` behind it, false the elifs get their ordinary turn.                                                                                                           |
| `TestIfChangedReadsTheConfigOnlyWhenAsked`      | `--changed` is the one condition that costs a config file and a git repository, and only it pays: with the file broken or the repository gone, a bare condition still runs where `--changed` refuses.                                                      |
| `TestIfChangedPropagatesTheExitCode`            | The helper stays transparent whatever the condition asked: the chosen script's code is the command's, `--on-failure` stays quiet on success, and a false answer with no `--else` runs nothing and succeeds.                                                |
| `TestExecResolvesTheSubjectsScript`             | The subject picks the level, and only that level: root, space and package each answer with their own text, a package declaring nothing is a reported miss, and standing in a package folder changes no answer.         |
| `TestExecForCwdReadsTheInvocationFolder`        | `--for cwd` reads the folder the way `dispat run` reads it: a package, a folder below one, the space holding it, the root, and a folder inside neither widening to the top level with the widening said out loud.      |
| `TestExecForCwdCarriesTheFoldersEnvironment`    | An inferred subject is a subject, so it moves the environment as well as the text, reaches the `DISPAT_*` variables from a package folder, and is refused from a space folder once the folder has been read.           |
| `TestExecScriptFromCwd`                         | `cwd` works on `--script-from` too, and there it moves the lookup alone.                                                                                                                                               |
| `TestExecRunsWhereItIsTold`                     | `--in` places the script by package, space, root, relative path, absolute path and `cwd`; `./root` reaches a folder actually called `root`; and without the flag the script still runs where the invocation stands.    |
| `TestExecInIsIndependentOfTheSubject`           | `--for` and `--in` answer different questions and are free to disagree: core's environment run at the root, and `--in root` from inside a package changing neither the config found nor the script resolved.           |
| `TestExecOnFailureRunsInTheSameFolder`          | The failure handler runs in the folder the script it follows ran in, since a cleanup tidying up somewhere else would be a trap.                                                                                        |
| `TestExecFallbackWalksTheLayers`                | `--fallback` reaches the top level from a package, the nearer level still wins, a package with none of its own gets its space's, and a name nowhere in the chain reports the whole chain.                              |
| `TestExecEnvironmentFollowsTheSubject`          | The declared env is layered file under space under package and belongs to the subject, not to the folder the command ran in.                                                                                            |
| `TestExecScriptFromCrossesTextAndContext`       | `--script-from` moves the text and leaves the environment with the subject, which is what keeps the crossed form sayable.                                                                                              |
| `TestExecReachesTheReleaseVariablesOutsideARelease` | The reuse claim: `--env both` supplies `DISPAT_VERSION`, `DISPAT_PACKAGE` and `DISPAT_STAGE=exec` with the declared env, `--env dispat` drops the declared half, and the default supplies neither.                 |
| `TestExecComputesNoPlanUnlessAsked`             | With the repository taken away a plan is impossible, so the default scope still working proves it computed none, `--for cwd` still working proves reading the folder is discovery rather than history, and `--env both` failing proves that scope is what pays for a plan. |
| `TestExecPropagatesTheExitCode`                 | The declared script's own code becomes the command's, and `--on-failure` replaces it.                                                                                                                                  |
| `TestExecComposesInsideARunScript`              | The in-flow case: a `run` script calling `dispat exec` hands the inner script the run's `DISPAT_*` variables through the process environment, with no flag.                                                            |
| `TestExecIsReservedAndRefusesBadFlags`          | Every malformed invocation is decided by the flags alone and exits 2, the replaced `--for-package` and `--for-space` among them, while an unknown package, space or folder is a runtime failure instead, because those flags were well formed. |
| `TestExecForwardsArgumentsAfterTheDash` | `dispat exec` runs one declared script once, so the arguments after `--` reach it unambiguously and a script in the config takes a terminal value with no config edit. `--on-failure` is proven not to receive them: that script is about the failure, not about the work. |
### Goal 22: self-update (`selfupdate_test.go`)

Two binaries are built at two versions and a fake releases API hands one out, so the one command that overwrites the
file it is running from is exercised for real rather than mocked.

| Test | Claim proven |
|------|--------------|
| `TestSelfUpdateReplacesTheRunningBinary` | The whole path over the process boundary: the binary downloads its successor, checks it against the published size and checksum, runs it, and steps aside for it, and the same path then answers with the new version while the old one waits beside it. |
| `TestSelfUpdateRefusesWhatItCannotTrust` | A checksum that does not describe what arrived is refused with the working binary untouched and no backup created: the checks stand between a download and the only binary the user has. |
| `TestSelfUpdateInstallsANamedVersion` | `--release` reaches any published version, downgrades included, and refuses one nobody published. That is what makes a bad release recoverable after the backup's week is up. |
| `TestSelfUpdateRollsBackAndBackAgain` | `--rollback` restores the backup and rotates, so a second rollback returns to where it started and the directory is never left holding a parked file. A restore is only safe if it is itself reversible. |
| `TestSelfUpdateAndPrereleases` | A release candidate is not an update by default, `--prerelease` opts into the candidates, and `--force` is the way back off that line. |
| `TestSelfUpdateBackupExpiresOnItsOwn` | The replaced binary survives six days and is cleared by the next command after eight, with nothing else in the directory touched. |
| `TestSelfUpdateNotice` | The half that reaches somebody who was not thinking about updating: the notice rides out on an ordinary command, stays out of JSON output, and is not made at all when the configuration says no. |
| `TestSelfUpdateWithNothingForThisPlatform` | A release cut before a platform joined the build matrix names the binaries it does have rather than leaving the reader guessing. |
| `TestSelfUpdateWithoutAStableRelease` | Where every tag is a candidate, "no matching release" naming the flag that would find one beats "you are up to date". |
| `TestSelfUpdateCommandWordKeepsItsScript` | Every command word permanently shadows a run script of the same name, which is why the word is `self-update` and not `update`. A deliberate, tested fact rather than a surprise. |

### Goal 23: the `compute` command (`compute_test.go`)

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
| `TestComputeWritesThroughARef`           | A `packages` map kept in a `$ref` fragment is edited in the fragment, at the key it holds, with the reference intact in the root config and the backup beside the file that was written.                                                                                                       |
| `TestComputeRefusesAComposedKey`         | A key composed from a fragment and the keys beside the reference is refused rather than guessed at, leaving every file and every backup untouched.                                                                                                                                             |

### Goal 24: native auto-versioning (`autoversion_test.go`)

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

### Goal 25: the manifest commands (`manifests_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestManifestsScannerNeedsNoConfig`             | The scanner runs with no config file, no commit and no plan: the listing carries each manifest's identity, ecosystem and declarations (local paths included), the walk descends while `--root-only` does not, installed dependencies are never scanned, the positional folder resolves against `--root` and a folder that is not there exits 1. |
| `TestManifestsScannerJSONEvents`                | `--log-format json` is this command's machine contract too: one event per manifest carrying path, ecosystem, identity and every declaration with its kind spelled out, plus the summary counts.                                       |
| `TestManifestsScannerStrictGatesBrokenManifests` | The partial-result contract reaching the exit code: a broken manifest is reported while the healthy ones are still listed and the run exits 0; `--strict` refuses the same repository with the partial result still printed.          |
| `TestManifestsWriterEditsInPlace`                | A two-ecosystem batch rewrites only the version text being changed, byte-for-byte elsewhere, in `package.json` (own version plus two fields) and `go.mod` (a require, no own version to write); re-running the same edits converges to `manifest unchanged`. |
| `TestManifestsWriterRedirects`                   | `--link` adds the local-folder directive and an empty path removes it, and the scanner reads back what the writer just wrote, which is the pair's whole contract.                                                                  |
| `TestManifestsWriterOutcomesReachTheExitCode`    | The three outcomes mapped onto exit codes: missing is tolerated (0) until `--strict` (1); a path no writer covers exits 1 while the usable manifests of the same batch are still written; a malformed `--set`, an invocation with nothing to write, and one with no manifest are usage errors (2). |
| `TestManifestsCommandWordsKeepTheirScripts`      | The command words are reserved: the bare `dispat scanner` is the command even where the config defines a `scanner` script, while `dispat run scanner` still reaches the script.                                                       |
| `TestManifestsReplacerNeedsNoConfig`             | The replacer runs over files that are not manifests at all, with no config file and no git history: every occurrence replaced, the paths resolved against `--root`, and repeated `--replace` values applied in order, each over what the last left. |
| `TestManifestsReplacerOutcomesReachTheExitCode`  | Nothing to write, a spec with no separator and no file to work on are usage errors (2); a pattern matching nothing is tolerated (0) until `--strict` (1); an unreadable path exits 1 while the usable files of the same batch are still written. |
| `TestManifestsReplacerJSONEvents`                | `--log-format json` carries one event per file with its path and occurrence count, plus the summary splitting applied, missing and skipped.                                                                                            |
| `TestManifestsReplacerWordKeepsItsScript`        | `replacer` is reserved like the other command words, and `dispat run replacer` still reaches a script of that name.                                                                                                                   |
| `TestManifestsScannerDebugEventsAndDroppedEntries` | `--log-level debug` narrates the scan and a declared-but-unreadable entry surfaces in the manifest event's `dropped` array; the default level keeps the narration out of the stream.                                                |
| `TestManifestsScannerVerifyGates`                | The two link gates, one flag per direction: a clean tree passes `--verify-unlinked` and fails `--verify-linked` (E216); a block-form go.mod replace fails `--verify-unlinked` (E215) with the manifest, dependency and path named; a `file:` range is a declaration rather than an injected directive and never trips the gate; both gates at once is a usage error (2). |
| `TestManifestsWriterDropLinks`                   | `--drop-links` sweeps every directive out of a mixed go.mod/Cargo/pubspec batch without being told any names, after which `--verify-unlinked` passes; a second sweep has nothing to do and exits 0; `--drop-links` with `--link` is a usage error (2). |
| `TestManifestsWriterLinkDropVerifyCycle`         | The whole bracket through the commands alone: link, prove it landed (`--verify-linked`), sweep, prove it is gone (`--verify-unlinked`).                                                                                              |
| `TestManifestsScannerRangeGates`                 | The range gates are independent of the link gates and of each other: `--forbid-range 'workspace:*'` fails per matching declaration (E217) and passes after the rewrite; `--require-range` gives the inverse answers (E218); a linked tree with clean ranges passes the range gates while failing the link gate; the same pattern in both flags is a usage error (2). |
| `TestManifestsWriterSetBuild`                    | `--set-build` writes the counter each mobile format keeps (`CFBundleVersion`, `android:versionCode`, Gradle `versionCode`, the pubspec `+` suffix) and only the counter; the events say `buildWritten` without `versionWritten`; the scanner reads the counter back; a non-integer where Android requires one exits 1. |

### Goal 26: the `autowriter` command (`autowriter_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoWriterEditsEveryCoveredPackage`       | One invocation reaches every package the window covers, writing the fixture back with exactly the edited bytes changed; `{version}` resolves against the plan the binary just computed, in a range and in the own-version field alike; a second pass leaves the working tree untouched. |
| `TestAutoWriterSelectsLikeEveryOtherCommand`   | The selection is the shared one: a `--package` term, the invocation folder standing in for it, `--consumers` reaching a package that declares nothing itself, and an unmatched term exiting 1.                                          |
| `TestAutoWriterOnlyUpdatedFollowsThePlan`      | `--only-updated` keeps an edit while the package it names is releasing and drops one naming no package of the workspace; once everything is released the same command writes nothing and says so, exiting 0.                            |
| `TestAutoWriterManifestScope`                  | `--manifests root` stops at the package folder while `all` reaches a nested manifest, and the own-version write stays on the root manifests under either scope.                                                                        |
| `TestAutoWriterRedirects`                      | `--link` adds the local-folder directive across the selection and an empty path restores the declared range.                                                                                                                       |
| `TestAutoWriterOutcomesReachTheExitCode`       | `--strict` is asked across the sweep (an edit landing in one package of two is clean, one landing nowhere exits 1, and without the flag the same run exits 0); nothing to write, a malformed spec, an unknown `--manifests` value and a positional argument are usage errors (2); an unresolvable `{version}` exits 1 with nothing written. |
| `TestAutoWriterJSONEvents`                     | The machine contract: one event per manifest carrying its path and the package it belongs to, plus the run's applied/skipped/missing tally.                                                                                            |
| `TestAutoWriterSyncLock`                       | The syncLock scripts run exactly where a manifest changed, not where it did not, never on a converged re-run, and not at all under `--sync-lock=false`.                                                                                |
| `TestAutoWriterSetLocalDerivesEveryWorkspaceRange` | `--set-local` writes the provider's planned version into every declaration naming a package here, spelled by `--range`, with no dependency typed and a third-party range untouched.                                            |
| `TestAutoWriterSetLocalConverges`              | A second `--set-local` pass computes the same ranges, writes nothing and reports nothing applied, so a converged run cannot re-trigger the syncLock scripts.                                                                       |
| `TestAutoWriterSetLocalYieldsToTheCommandLine` | A dependency named by `--set` keeps what the operator asked for; the derived edit steps aside.                                                                                                                                    |
| `TestAutoWriterSetLocalSpellsEachEcosystemItsOwnWay` | One `--range` keyword crosses ecosystems through the shared renderer: go.mod keeps its canonical `v`, and a Docker tag stays a bare label rather than growing a caret.                                                       |
| `TestAutoWriterSetLocalTemplateRangeIsVerbatim` | A `--range` template passes through untouched, so over a mixed workspace it can hand a Docker manifest something no registry accepts, and the writer refuses it rather than write it.                                             |
| `TestAutoWriterLinkLocalRoundTrips`            | `--link-local` writes the folder redirect with a path relative to the manifest, and `--unlink-local` restores the file byte for byte, which is what proves the derived paths and their removal agree.                             |
| `TestAutoWriterLinkLocalResolvesFromTheManifestFolder` | A nested manifest resolves its link from its own directory, one level deeper, rather than from the package folder.                                                                                                       |
| `TestAutoWriterLinkLocalSkipsNpm`              | A derived link leaves `package.json` alone and says why: npm refuses an override for a directly declared dependency unless the specs match exactly.                                                                               |
| `TestAutoWriterLinkLocalWarnsAboutPublishing`  | Every `--link-local` run warns that no release removes a local link, so one left in place ships a manifest consumers cannot resolve.                                                                                              |
| `TestAutoWriterSetLocalAndLinkLocalInOnePass`  | The two derive from one reading of the same declarations, so asking for both writes both.                                                                                                                                        |
| `TestAutoWriterLinkLocalLeavesTheComputedGraphAlone` | Writing a link suggests no config change compute did not already suggest, so a local checkout does not move the graph.                                                                                                     |
| `TestAutoWriterLocalFlagsReachTheExitCode`     | `--link-local` with `--unlink-local` is a usage error (2); a bare local flag is a complete request; and a derived edit never trips the `--strict` stale gate.                                                                     |
| `TestAutoWriterLinkLocalReachesAnIndirectRequire` | A Go build honours replace directives from the main module alone, so a provider reached only through another module is still redirected from the consumer's own go.mod. The link half reads the indirect requires for exactly that reason. |
| `TestAutoWriterSetLocalLeavesAnIndirectRequireAlone` | The range half stops at the declarations: an indirect require is a version the toolchain wrote, and reconciling it would be dispat editing bookkeeping it does not own. |

### Goal 27: the `autoreplacer` command (`autoreplacer_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoReplacerFansOutAcrossWorkspaceProviders` | A `{provider}` pattern is rendered once per workspace package the covered package declares, so a coordinate follows its provider with no dependency named on the command line.                                                     |
| `TestAutoReplacerPackageScopedPatternRunsOnce` | A `--replace` naming no provider is about the covered package itself and writes its own version.                                                                                                                                         |
| `TestAutoReplacerGlobsSelectWithinThePackage` | A glob reaches only what it names, inside the package folder the sweep handed over; a file no glob selected is untouched.                                                                                                             |
| `TestAutoReplacerOnlyUpdatedNarrowsTheFanOut` | `--only-updated` drops a provider released outside this run, so its coordinate is left as it is.                                                                                                                                      |
| `TestAutoReplacerConsumersReachesThePackagesTheWindowLeftOut` | The package carrying a stale coordinate is the one nothing changed in, so the window excludes it; `--consumers` is what pulls it in and closes the gap.                                                                |
| `TestAutoReplacerConvergesUnderStrict`        | The probe tells "already reconciled" apart from "never matched", so a converged re-run of a `{previous}=>{version}` pattern is clean rather than stale.                                                                               |
| `TestAutoReplacerLeavesANestedPackageToItsOwner` | A package nested inside another declines that package's files, which its owner's own turn writes, so one file is never written from two goroutines.                                                                                |
| `TestAutoReplacerOutcomesReachTheExitCode`    | `--strict` is asked across the sweep; no `--replace`, no `--files`, a malformed spec and a positional argument are usage errors (2).                                                                                                      |

### Goal 28: Docker through the binary (`docker_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestDockerComputeDerivesTheImageChain`         | `compute` reads an image-to-image edge off a `FROM` line that no config declares, names the file it came from, ignores a base that is not a workspace package, writes the edge under `--write` and greens the `--check` gate; the stated `manifestNames` repository is what lets the chain resolve, since an image's identity is never its folder name. |
| `TestDockerReleaseReconcilesTagsAndCompose`     | A release reconciles both Docker formats at the version stage: the consumer's `FROM` tag and its `COPY --from` image follow the provider's new version, a `COPY --from` naming a build stage is left alone, and the compose file gets the package's own version in the service it builds and every `build.tags` entry, the provider's in the service it pulls, and nothing at all in a port mapping. |
| `TestDockerManifestCommands`                    | The config-free commands over both formats: `scanner` reports a compose file's identity and a Dockerfile's bases with no config, commit or plan; `writer` rewrites a compose tag and own version proved byte-for-byte on disk; a digest-pinned base is skipped rather than failed, and a missing edit still gates under `--strict`. |

### Goal 29: the release guards (`guard_test.go`)

| Test                                       | Claim proven                                                                                                                                                                                                                                                     |
|--------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestGuardAllowBranch`                     | With `run.allowBranch` set, a release on a listed branch proceeds; one on a foreign branch exits 1 naming the branch and the globs, with nothing tagged; a `*` glob reaches slashed branch names (`release/v1`); `dispat status` works on any branch.             |
| `TestGuardAllowBranchRefusesDetachedHead`  | A detached HEAD has no branch name, so it matches nothing, a glob as broad as `*` included, and the run is refused before anything is tagged.                                                                                                                     |
| `TestGuardBehindRemote`                    | In push mode a checkout whose branch is behind the remote (another clone pushed) refuses before any release work with "behind origin/main", tagging nothing; after `git pull --rebase` the same release goes through and the tag arrives on the remote.           |
| `TestGuardBehindRemoteHonoursCommitVerify` | The behind check is another `ls-remote`, so `commit.verify: false` turns it off with the reachability check. The same run then builds, publishes and tags before git rejects the push, which is the wasted release the guard exists to prevent.                   |
| `TestGuardsAreUnsetByDefault`              | Neither guard applies unless configured: an ordinary repository on an arbitrarily named branch, pushing to a remote, releases exactly as before.                                                                                                                  |

### Goal 30: the release lock (`lock_test.go`)

The lock is a ref on the remote, so every claim here is read from the bare repository the fixtures push to. What was
true *during* a run is read from a `beforeAll` hook, which runs while the lock is held. The harness disables the lock
for every other scenario (`DISPAT_UNSAFE_DISABLE_LOCK=true` in `runBin`), since most fixtures have no remote at all;
these tests ask for it back.

| Test                                       | Claim proven                                                                                                                                                                                                                              |
|--------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestReleaseLockRoundTrip`                 | The tag is on the remote while the run works and gone from both copies once it is over, release and all; a second run with nothing left to release takes and returns it just the same.                                                     |
| `TestReleaseLockHeldElsewhere`             | A lock already on the remote refuses the run (exit 1) with the remedy in the message, nothing built and nothing tagged, and, the part that matters, the holder's tag object is untouched. The same repository releases once it is freed.  |
| `TestReleaseLockIgnoresCommitForce`        | `commit.force` rewrites a run's own records, never another run's lock: a repository configured to force everything still bounces off a held lock.                                                                                          |
| `TestReleaseLockBlocksConcurrentRuns`      | Two real releases against one remote: the first holds the lock inside a gated hook, the second is refused while it is held and goes through once it is released. No sleeps: the second starts only once the lock is provably on the remote. |
| `TestReleaseLockIndependentOfPush`         | With no release commit configured, the lock is still taken and cleared, and the remote ends with no tag and no branch: the lock is not the release push.                                                                                   |
| `TestReleaseLockWithoutRemote`             | With the lock on, a repository with no remote cannot coordinate and does not release. The cost of the guard, stated.                                                                                                                       |
| `TestReleaseLockKillSwitch`                | Through the binary: `true`/`1`/`TRUE` release a remoteless repository unguarded, while `false`, `0`, an empty value and a typo all keep the lock on.                                                                                       |
| `TestReleaseLockConfigSwitch`              | `unsafeDisableLock: true` releases a remoteless repository with the environment variable explicitly set to `false`, proving the config alone decides it; setting the key back to `false` brings the refusal back.                          |
| `TestReleaseLockClearedWhateverHappens`    | The lock is given back on every way out: a failed package, a guard refusing the run after the lock was taken, and both signals the binary handles mid-build (SIGINT from a terminal, SIGTERM from whatever runs the job). Each signal case asserts the lock was *held* when the signal arrived as well as gone afterwards, since a run that never took it would also end with a clean remote.                                                                                                      |
| `TestReleaseLockStaleLocalTag`             | A lock tag left in the clone by a killed run says nothing about who holds the lock, so the next release overwrites it locally and carries on.                                                                                              |
| `TestReleaseLockCleanupFailureIsNotFatal`  | A remote that has become unreachable by the end of the run is reported with the remedy and leaves the exit code to the release itself (0), the stranded tag confirmed on the remote.                                                       |
| `TestReleaseLockAppliesOnlyToRelease`      | `status`, `preview`, `run`, `changelog`, `autoversion`, `commit` and `scanner` take no lock, which is why they still work in a repository with no remote.                                                                                  |
| `TestReleaseLockIsNotAReleaseTag`          | The lock is on HEAD while the plan is computed, so a `{version}` tag format, the broadest there is, still reads 0.1.0 as the baseline and releases 0.2.0.                                                                                |
| `TestReleaseLockNotTakenWhenNothingToRelease` | `--require-release` is the one case where the plan decides whether the run happens at all, so it is answered before the lock rather than after it: a run that will publish nothing must not put the tag on the remote, and must not make a real release queue behind it, only to find it had nothing to do. |

### Goal 31: corrections and reverted changelogs (`corrections_test.go`)

Every scenario names its target the way an operator does, with `git rev-parse HEAD` after the commit it means, so the
footers under test carry real shas rather than fixtures. Most run in a two-package repository with no edge between
the packages, which is what makes "the correction reached exactly this far" assertable: the second package is the
control.

| Test                                                    | Claim proven                                                                                                                                                                                                                                       |
|---------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestCorrectionEditRestatesTheRecordBeforeRelease`      | The specification's worked example end to end: a commit misclassified as a breaking feature is restated as a fix before anything ships, so the package releases a patch rather than a major, the changelog carries the restatement and names what it corrects, the mistake is documented nowhere, and the next run converges. |
| `TestCorrectionAfterReleaseIsAVisibleNoop`              | Once the target has shipped its record is published history: the correction does nothing, W209 reports it against the right package, and the carrying unit still releases on its own account. `--quiet-parser` cannot hide W209, which is what makes it non-suppressible in practice rather than only in the registry. |
| `TestCorrectionPrecedenceAndVoiding`                    | Two corrections of one target resolve newest-first, with W210 on the loser and the restatement deciding the bump; then a delete of a correction voids it (W215), which un-does the correction and brings the original record back. |
| `TestCorrectionScopeIsContainedNotCombined`             | Narrowing and widening in one flow: a correction scoped to one package restates a record scoped `(*)` for that package while the record stands for the other, and a correction naming a package its target never claimed is E213 with the target untouched. |
| `TestCorrectionWildcardClearsAScope`                    | `Deletes: *` on a `chore` discards every pending record for the scope it names, releases nothing in its place, and leaves the package outside that scope releasing normally.                                                                       |
| `TestCorrectionTargetsMustResolve`                      | The three unit-scoped errors through the binary: a sha that is not an earlier commit (E210), a bare sha naming a commit that carries two units (E211), and a correction aimed at a `cancel` barrier (E212).                                        |
| `TestCorrectionDiscardsWhatTheRecordPropagated`         | A deleted record takes its propagated contributions with it: the consumer that would have been carried along by the caret has no reason to release at all.                                                                                          |
| `TestCorrectionRidesAVersioningGroupOnlyWhenARecordSurvives` | Discarding the record that was a versioning group's only cause stops the whole group, rather than leaving its members riding (W234) a version nothing asked for.                                                                              |
| `TestRevertTakesBothEntriesOutOfTheChangelog`           | The revert trap and its changelog half together: the bump keeps the reverted commit's major, because consumers may have seen it already, while the changelog loses both entries because the release contains neither the change nor its removal (W212). The run converges. |
| `TestRevertWithAnUnreachableTargetStaysInformational`   | The two degraded forms: a well-formed sha naming no reachable commit is W213 and the revert releases and is documented as usual, and a value that is not a sha at all is the parser's W214 with no second code from dispat.                        |
| `TestRevertSuppressionIsVoidedByACorrection`            | Discarding a revert's record voids its changelog suppression, so the entry it hid returns and there is no W212 left to report. The §7.4 voiding rule applied to §7.3.                                                                              |

### Goal 32: references naming several files (`multiref_test.go`)

`$ref` is a shape the typed model deliberately cannot express, so these configs go through `WriteConfigRaw` on top of
`rawSplitConfig()`, the raw spelling of `harness.BaseFile` plus the canonical one-space flow, exactly as the
single-file reference scenarios in goal 10 do.

| Test                                        | Claim proven                                                                                                                                                                                                       |
|---------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestMultiRefMergesObjectFragments`         | A shared fragment and the file that adjusts it become one object: the later file wins the key it shares, the key only the first file wrote survives, the merged script actually runs, and every file read is on the traced record. |
| `TestMultiRefConcatenatesLineFragments`     | The case the feature exists for: a common block of record lines and the lines a repository adds arrive as one list, in the order the files are named, across two formats, and a merged line keeps the package filter it was written with. |
| `TestMultiRefSiblingOverrideAndEnvCase`     | The keys beside a reference outrank every file it named, and an environment variable merged out of two fragments reaches a script spelled as its file spelled it, the thing a merge could plausibly lose, since the loader hands viper a lowercased copy of the tree. |
| `TestMultiRefRefusals`                      | No files at all, a name that is not a file, files holding different kinds, and a missing file are each a configuration error that stops the run before a tag, a commit or a script.                                |
| `TestMultiRefCycleNamesThePath`             | Every file a reference names is followed on its own, so a cycle closed by the second of them is refused naming the file that closed it, with nothing released.                                                      |
| `TestMultiRefComputeWriteRefuses`           | A key merged from several files is held by no one of them, so `compute --write` refuses, names the ways out and leaves every file and backup exactly as it found them; a list naming one file is written through as the plain reference it is. |

### Goal 33: the channels a record reaches (`channels_test.go`)

Two packages are compared inside one run wherever they can be, because "this one records and that one does not" is the
claim, and a single run makes it without depending on anything between runs.

| Test                                     | Claim proven                                                                                                                                                                                                        |
|------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestChannelsNamedChannelGate`           | A policy naming one prerelease channel records there and nowhere else while a policy naming every prerelease makes the complementary cut, driven across a beta, an rc and the graduation; the skip names the restriction that held the release back and the channel it was on. |
| `TestChannelsFilterRecordLines`          | A line carries its own channels, so one configured footer says one thing on the betas and another on the stable release, while the sections between the lines stay whatever the release carries; the GitHub body agrees with the changelog entry, since one entry format has one answer wherever it is rendered. |
| `TestChannelsCombineWithPackageFilters`  | Channels is one filter among the others: a line naming a package and a channel is written only where both hold.                                                                                                     |
| `TestChannelsKeepReleasersApart`         | Two packages whose GitHub policies differ only in a line's channels do not share a releaser, which is what keeps one package's entry format from rendering the other's body.                                        |
| `TestChannelsValidationRefusals`         | A restriction naming nothing is refused where it is written, on a line and on the object alike, as is a file title that varied by channel: it is written once and matched on the next release. Nothing runs.       |
| `TestChannelsPreviewShowsBothBodies`     | `dispat preview --changelog --github` prints both bodies under one header, labelled, each under its own entry format; a record the channels hold back says so instead of showing a body nothing would receive; and naming the changelog prints exactly what naming nothing prints. |
| `TestChannelsAreReportedInTheSkipEvent`  | The skip is an info-level event carrying the package, the tag and the channel, so a flow can tell "held back by configuration" from "failed" without reading prose, while the release itself is still tagged.       |

### Goal 34: versioning `none` (`versioning_none_test.go`)

| Test                                        | Claim proven                                                                                                                                                                                                     |
|---------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningNoneLifecycle`               | A change touching a releasable and a none space releases only the former; the none package gets no tag and no changelog, its graph line reads script-only with no version transition, and a converged second run reports it script-only again with no catch-up noise: the state is permanent, not pending. |
| `TestVersioningNoneRunDefaultWindow`        | The default `dispat run` window is the release plan plus every changed none package: the none package runs scripts while nothing is releasing and an unchanged released package does not, and a fresh change brings the releasable one back while the none one never left. |
| `TestVersioningNoneProviderEdgeRejected`    | A releasable consumer of a none provider fails the config load, with the error naming the declaration that carries the edge (the root list and a space's own object produce different labels); a none consumer of anything, and a none-to-none edge, load and release normally. |
| `TestVersioningNoneConsumerWithLocalLink`   | The state none packages exist for: `--link-local` over a none-only selection writes the link without the "must be removed before publishing" warning, the linked provider keeps releasing, and a `{version}` placeholder naming the none package is refused instead of expanding. |
| `TestVersioningNonePackageSelection`        | Naming a none package directly is answered by command: `release --package` says the package is never released and exits cleanly with nothing tagged; `run --package` runs the script.                             |
| `TestVersioningNoneReleaseAsInert`          | A `Release-As` footer aimed at a none package moves nothing, is reported as W238, and leaves the rest of the run untouched.                                                                                       |
| `TestVersioningNoneReleaseOnlySettingsInert` | Release-only settings on a none space (`tagFormat`, publish stages) load without error and never execute; the same build script still runs through `dispat run`.                                                 |

### Goal 35: spaces spanning several folders (`spacepaths_test.go`)

| Test                                    | Claim proven                                                                                                                                                                                                       |
|-----------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestSpacePathsMultiFolderLifecycle`    | Packages under every listed folder belong to the space: one release tags both, the login runs once and in the first folder, `exec --in space:` resolves the first folder, and a second run converges.               |
| `TestSpacePathsSpaceFilesMergeInOrder`  | Every listed folder's space config file loads; a later file overrides an earlier one's env value, and a dependency edge declared in either file reaches the plan graph.                                             |
| `TestSpacePathsRefusals`                | A folder listed twice and folders nesting one another are refused at load, each with its own message.                                                                                                               |
| `TestSpacePathsAscentFromSecondPath`    | Config-file resolution from inside a later folder finds the monorepo root even when that folder's space file carries a `packages` map, the shape that would otherwise read as a nested monorepo root.               |
| `TestSpacePathsFilterAndLocate`         | `--space` covers every folder's packages; standing in a later folder infers the space, and standing inside one of its packages narrows to that package.                                                             |
| `TestSpacePathsNoneCombined`            | A versioning-none space spanning two folders runs scripts under both and never tags anything, while the releasable space next to it releases normally.                                                              |

### Goal 36: declared version groups across spaces (`versiongroups_test.go`)

Goal 13 pins the declared-group lifecycles under `fixed` and `fixedMajor` (in `overrides_test.go`); this goal covers
the sparse and partial-sparse modes across spaces, the group-membership edges of the override ladder, a shared
prerelease train, and the defined freedom of per-member tag spellings.

| Test                                        | Claim proven                                                                                                                                                                                                        |
|---------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersionGroupSparseAcrossSpaces`        | A declared `fixedSparse` group never back-fills: an untouched member in the other space does not ride (no W234), and when it finally changes it joins at the group's next version, skipping the ones it sat out.     |
| `TestVersionGroupPartialSparseAcrossSpaces` | Under `fixedMajorMinorSparse` a patch stays inside its member, a minor moves the shared part without dragging the other space along, and the laggard joins at the shared part when it next changes.                  |
| `TestVersionGroupMemberOverrideLeavesTheGroup` | Versioning and versionGroup are one ladder axis: a package-level `versioning` on a declared group's member supersedes the membership its space joined, so the package versions on its own line instead of riding. |
| `TestVersionGroupPrereleaseTrain`           | A prerelease train runs across the whole declared group with one shared counter; graduation lands every member on the same stable version; divergent member channels while the group moves are W236.                 |
| `TestVersionGroupRefusesNone`               | A declared group with versioning `none` is refused through the binary with the loader's own message, not just in config unit tests.                                                                                  |
| `TestGroupFilterPartialMode`                | `--group` under `fixedMajor` selects the whole group, and the partial mode keeps meaning what it means inside the selection: a breaking change moves every member, a minor releases its member alone.                |
| `TestVersionGroupDivergentTagFormats`       | A group shares the version, not its spelling: each member renders the shared version through its own `tagFormat`, with no diagnostic beyond the ride's own W234: defined behavior, locked in.                       |

### Goal 37: step commands wired into a running release (`stepwiring_test.go`)

| Test                                | Claim proven                                                                                                                                                                                                          |
|-------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestStepsWiredIntoAPublishLeg`     | A publish leg whose script is `dispat changelog` + `dispat commit --tag --push` records itself mid-run: the tag reaches the remote before the gated consumer builds, the tagged tree carries the step-written changelog, finalize finds the work done (W223/W226), no W228/E219 fires, and a second run converges. |
| `TestStepsWiredSurviveAPartialRun`  | After a run that released only the provider, the next run finds the provider's step-made records, does not re-release it, and catches the consumer up at the version it was owed.                                        |

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
| **A replacement landing inside a binary file.** A glob reaching a PNG that happens to contain the version text would have corrupted it.                                                                            | `TestAutoVersionReplaceStrategy`; `TestReplaceRuleSkipsBinaryAndOversizedFiles`, `TestReplaceRefusesABinaryFile`             | `autoversion_test.go`; `internal/release`, `pkg/writer` |
| **An empty `find` matching at every position.** Both the API and the command line refuse it rather than shredding the file.                                                                                        | `TestReplaceRefusesAnEmptyFind`, `TestReplaceBytesIgnoresAnEmptyFind`, `TestParseReplaceSpec`                                | `pkg/writer`, `internal/cli` |
| **Two span replacements covering the same bytes**, or spans queued against a file a writer also regenerated whole: the result would have depended on the order they were queued in.                                | `TestLinkrRefusesOverlappingPatches`, `TestLinkrRefusesSpansOnARegeneratedFile`                                          | `pkg/writer` |
| **A correction that reached no package said nothing at all.** A correction with no scope-set takes its packages from its targets, so a target no pending window still holds left it addressing nothing, and it was skipped before it could report. This is the one shape `W209` exists to prevent: the operator writes the correction, sees no diagnostic, and believes the record was fixed. | `TestCorrectionReachingNoPackageStillReportsW209`                                                                        | `internal/plan` |
| **`E213` reported once per target rather than once per package.** Two targets that both omit the same package is one mistake in one scope-set, fixed once; reporting it per footer scaled the noise with the number of footers.                                | `TestCorrectionWideningIsReportedOncePerPackage`                                                                          | `internal/plan` |
| **A unit naming one target twice reported `W210` against itself.** §7.4.1 collapses several targets into the one carrying record, so the second mention is redundant rather than superseded; the warning told the operator a newer commit had overridden their correction and named the correction's own commit as the culprit. | `TestCorrectionNamingOneTargetTwiceIsNotSuperseded`                                                                       | `internal/plan` |

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
