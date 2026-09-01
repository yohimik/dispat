# Integration test plan

This module (`tests/integration`) is the black-box integration suite for the dispat CLI. It compiles the real binary
from `services/dispat`, drives it against disposable git repositories exactly as your shell would, and asserts on the
three outputs a release run actually has: **git state** (tags, commits, file contents), **JSON log events**
(`--log-format json`, the machine-readable contract CI ingests), and, where *timing* rather than mere ordering is the
claim, **nanosecond-resolution execution timelines** recorded by a purpose-built probe.

## Goals

Forty-five goals across forty-nine test files, one file each except goal 21, which the three shell helpers split
between `if_test.go`, `if_changed_test.go`, `for_test.go`, `for_changed_test.go` and `exec_test.go`. They are grouped
by what they are about rather than by the order they were written in, so you land in one place when looking for how a
plan gets computed or which command does what.

### Planning and versioning

1. **Plan logic** (`plan_test.go`): prereleases, cancels, holds, catch-up, provider-failed and consumer-failed runs,
   and as many edge cases as earn their keep, including that scripts execute *according to* the plan (a held or
   cancelled package runs nothing; a resumed one runs exactly once).
2. **Space versioning modes** (`versioning_test.go`): all seven modes driven side by side across multiple runs:
   `independent`, the full-version `fixed`/`fixedSparse`, and the partial `fixedMajorMinor`/`fixedMajor` pairs that
   hold only a prefix in common. Rides and their "no changes" changelog entries, sparse alignment, versions diverging
   again below the shared part, the single shared prerelease train against a train that stays local, failed-ride
   catch-up on the stable line and mid-train, holds and pins under a shared version, mixed shared depths in one group,
   and no bleed between modes.
3. **Repository-scoped fatal errors** (`fatal_test.go`): the §16 bucket that aborts a run whatever `commitErrors` says,
   each constructed for real: a dependency cycle (E200), duplicate version tags (E191), and a shallow clone (E196).
   These are the cases where a partial release would be worst, so each asserts the non-zero exit, the code in the
   events, and that nothing was released or executed.
4. **Change-scope ignore** (`ignorescope_test.go`): which of a package's own files make a scopeless commit address it.
   A commit touching only ignored files releases nothing and says so (W131), one ordinary file among them brings the
   package back, and the levels (repository, space, package) add up, with only the package able to re-include what a
   broader level excluded and only for itself.
5. **Release edge cases** (`edgecases_test.go`): the cases that sit between two features, where each feature is right
   on its own and the interaction is the question. An exact `Release-As` naming a prerelease, where the pin guards meet
   the channel rules; a window where a provider and its consumer each changed for their own reasons with no propagation
   syntax written, which both auto-versioning strategies, the version scripts and the changelog have to account for
   with no `DueTo` link to follow; a package joining a versioning group with no version, and one joining with a version
   that outranks the group's; and the boundary where `revertOnFail` stops. Each one fails by producing a *plausible*
   release rather than an error, which is what makes them worth a file of their own.

34. **Versioning `none`** (`versioning_none_test.go`): the mode that leaves the release flow entirely. A `none` package
    is never versioned, tagged, changelogged or published, runs scripts from the default `dispat run` window whenever
    it has pending changes (it always does: nothing consumes its window), may depend on releasable packages (including
    through a permanent local link) and is refused as a provider to any releasable package at config load. Also: the
    graph's script-only line, an inert `Release-As` reported as W238, inert release-only settings, and explicit
    `--package` selection answered out loud.
35. **Spaces spanning several folders** (`spacepaths_test.go`): the list form of a space's `path`. Discovery and
    release cover every listed folder's packages; each folder's space config file loads, merging in list order; the
    first folder anchors the login and `exec --in`; config resolution from inside a later folder still finds the root;
    `--space` and folder inference reach every folder; the list's refusals (duplicates, nesting) surface through the
    binary; and a `none` space spanning two folders runs scripts in both without ever tagging. Per-folder
    `.dispatexclude` and the package-name collision across folders are pinned by unit tests in `internal/config`.
36. **Declared version groups across spaces** (`versiongroups_test.go`): the full and partial lifecycles, the sparse
    and partial-sparse modes as declared cross-space groups, the override ladder detaching a member that sets its own
    `versioning`, a shared prerelease train with one counter and a W236 channel conflict, what exact pins do to a group
    mid-train and to a sparse member, the mixed-depth resolution (W237) along a train, the polyglot script-only member,
    the versioning-`none` refusal through the binary, `--group` selection under a partial mode, and divergent
    per-member `tagFormat` as defined behavior: one shared version, each member spelling its tag its own way.
38. **The longitudinal fence** (`longitudinal_test.go`): one repository modelled on dispat's own shape (a declared
    group across two spaces, a wired publish leg, a caret provider, alias tags, a remote, and the GitHub recorder)
    released through a whole rc-train lifecycle with every record and every status line asserted at every step. The
    fence over the train-wide-versus-fresh seam that produced the pre-1.0.0 planner bugs.
39. **A record entry is never empty** (`entrybodies_test.go`): the release shapes with nothing for their notes to group
    (a pin, a channel transition, work its reverts cancel out) each state their cause in the changelog entry and the
    GitHub body instead of rendering an empty record.
44. **Authors in release records** (`authors_test.go`): the entry-format `authors` object, off by default and
    attributing an entry to the people who wrote it from git's own identities alone. The two placements, the two
    identity formats, `ccme` against `all` where the second reaches commits that carry no release record, the
    include-then-exclude glob filters, the configuration ladder and the six flags that beat it, the narrowing that
    keeps attribution aligned with the notes across a prerelease train, a correction and a revert, and the proof
    that two packages configured differently do not share one GitHub releaser.
40. **The e2e smoke walk** (`smoke_test.go`): the live release-verification protocol as a test: a toy polyglot monorepo
    (Go, npm, Docker; real manifests; a version group; edges in both ecosystems) released through six cycles with the
    full status graph, the tags, every entry and every manifest byte asserted at each step, and convergence proven
    between steps. Includes the reconciliation pickup (W197) and its deliberately silent changelog.

### Scheduling and execution

6. **Concurrency** (`concurrency_test.go`): stable tests *guaranteeing* the budgets work. With concurrency 4 and five
   packages, the fifth's work starts exactly after one of the first four finishes; independent packages are picked up
   concurrently while dependants are awaited.
7. **Execution order by dependency graph** (`order_test.go`): scripts run in the order the graph dictates, under both
   `isBuildWaitingPublish` settings.
8. **Interruption** (`interrupt_test.go`): a SIGINT mid-run shuts the run down gracefully through the real signal
   handler: the in-flight script is killed, remaining packages report `cancelled` rather than `failed` or `skipped`,
   nothing is tagged for work that did not finish, and the next run releases the cancelled packages at the version they
   were owed.
9. **The script frames** (`hooks_test.go`): every stage sits inside a frame of hooks, and the frames nest: nine
   per-package hooks around the version, build and publish stages, the announce frame after a publish, the
   `flow.onFail` / `flow.onSkip` outcome scripts, the once-per-space login gate, and the run-level bracket around the
   whole thing. What these prove is *authority* rather than mere ordering: a hook before the point of no return may
   fail its package, one after it may only warn, and the same split decides where `revertOnFail` applies, how far a
   login failure reaches, and how script outputs accumulate across stages and hooks.

### Configuration

10. **Config loading, resolution and options** (`config_test.go`): which file the binary picks when no `--config` names
    one and how far it climbs to find it, a flag beating the file at *runtime* rather than in the parsed struct, a
    custom shell actually being invoked, an unknown key stopping the run, the `commitErrors` policy, the parser
    options, initials baselines, a fused prerelease tag format written and read back, a configuration split across
    files with `$ref`, and the rejections the layers carry, each landing before any work is done.
11. **The static `env` layers** (`env_test.go`): plain environment variables declared at the top level, on a space and
    on a package, merging with the most local winning. The part only the binary can witness is that a key arrives with
    its case intact and that a `$DISPAT_VERSION` reference expands against the package the script runs for. The
    refusals are the load-bearing half: a script may trust `DISPAT_VERSION` precisely because no static key is allowed
    to shadow it. The `.env` file sits beside them: read from the current directory into the run's own environment,
    under the environment and under the config's `env`, and reaching dispat's own reads as well as the scripts'.
12. **The configuration ladder from the root down** (`levels_test.go`): the root file is the bottom layer of the same
    fold a space and a package go through, so a space-shaped setting written once at the top reaches every space and
    every standalone package. Each level below can still say otherwise, including saying `false` against a `true`,
    which is what makes the boolean options three-state rather than plain.
13. **Per-package overrides, versioning groups and `.dispatexclude`** (`overrides_test.go`): the layered configuration
    through the binary: a `packages` entry replacing one flow entry while the sibling keeps the space's, the in-folder
    config file beating the entry, `.dispatexclude` exclusions, a declared `versionGroups` group spanning two spaces to
    one version and its convergence, per-package changelog/GitHub record policies, the concurrency weight, the config
    ascent, and `scripts` defined at each of the three levels.
14. **The top-level `packages` section** (`packages_test.go`): a `packages` entry with a `path` releasing as a full
    package outside every space, the standalone path config errors, provider lists declared in an entry or an in-folder
    config file ordering the graph like top-level edges, `src` narrowing change detection, and `dispat compute` editing
    each declaration where it lives.
15. **Dependency edges declared by a space** (`spacedeps_test.go`): a space states the edges of its own packages next
    to the space, and every declaration merges into one graph. The rule that makes the level worth having is that an
    edge must touch the space it sits in: one touching neither end is refused before anything runs.

### The commands

16. **Release records** (`records_test.go`): the durable artefacts themselves: changelog files accumulating across
    releases above pre-dispat content, annotated tags with their messages and targets, alias tags, GitHub releases, and
    commit mode's release commit, tag placement and push against a real bare remote. It is also the home of the
    failures *after* the point of no return, where the artefact is already in a registry and nothing dispat does can
    take it back: a tag (E220), a tag at a foreign commit (E221), a record (E222), the release commit (E223) and the
    push (E224) each failing there, plus the alias tag (W232) that deliberately is not one of them. None of them fails
    a package or stops the run; each is recorded and the run finishes what else it owed.
46. **Draft GitHub releases** (`draft_test.go`): `github.draft` and the `--draft` flag that overrides it either way.
    What is pinned here is the half a draft makes hard: a draft carries no tag ref, so GitHub's by-tag lookup cannot
    see one, and the re-run skip every other release relies on has to come from the release listing instead. The fake
    keeps drafts apart from published releases for exactly that reason, which is what makes the skip, and the flip
    that abandons a stale draft rather than searching for it, claims about behaviour rather than about the fixture.
17. **The `init` and `preview` commands** (`commands_test.go`): the starter config the very next `status` can load, the
    pending release notes on stdout, and the CLI surface itself (per-command `--help`, the platform in `--version`).
18. **The `dispat run` command** (`run_test.go`): a script executed inside changed packages over the dependency graph
    with the full environment, resolved per package through the three `scripts` levels, the `dispat <script>`
    shorthand, the `--package`/`--space` selection, the `--since` window and the `--consumers` expansion and how they
    compose, the `--on-error` policies, the concurrency budget, and cross-package output carrying.
19. **The standalone step commands** (`standalone_test.go`): `dispat changelog`, `dispat autoversion`, `dispat commit`
    and `dispat github` through the binary: the shared package selection, changelog idempotence (W226), the in-flow
    scenario where nested step commands land the changelog inside the tagged commit, the `--tag`/`--push` committer
    identity and remote delivery, and the window flags the steps share with `dispat run`.
20. **The `--package` / `--space` / `--group` selection** (`filter_test.go`): the one selection every package command
    shares: the term spellings and their globs, the invocation folder standing in for the terms nobody typed, the
    filter narrowing a window and never widening it, and partial releases, where publish order withholds a consumer
    whose provider was left out (`W230`) and a split versioning group is warned about and released (`W231`).
21. **The shell helpers** (`if_test.go`, `if_changed_test.go`, `for_test.go`, `for_changed_test.go`, `exec_test.go`):
    the three commands that run one script instead of sweeping a selection. `dispat if` picks a shell string from a
    condition on the environment, the filesystem (`--file`/`--dir`) or the repository (`--changed`); `dispat for` runs
    a script once per item of a list; `dispat exec` runs one *declared* script, where one subject decides both which
    level is read and whose environment the script gets. The group's load-bearing claim is that a declared script
    reading `DISPAT_*` becomes runnable outside a release. All three take a place in the monorepo the same way, `pkg:`,
    `space:`, `root` or `cwd`, on the subject, on the script source and on the folder the script runs in, so the second
    claim is that each of the three moves only its own half: `--for cwd` infers a subject without a plan,
    `--script-from` still leaves the environment alone, and `--in` changes nothing about resolution. The third is the
    cost of `dispat if` and `dispat for`, which stays nil until an `--in` names something only a configuration can
    place, or the command asks about the repository itself. The fourth is `--changed`'s selection: the same window,
    filter and consumer expansion every sweeping command uses, composed so that `--consumers` reaches downstream of the
    changes. `dispat for` adds two of its own: that its list comes from exactly one source, where the same `-p`/`-s`/
    `-g` flags are the source without a window flag and a narrowing with one; and that `--changed` and `--unchanged`
    partition the repository, so every package is in exactly one of the two loops.
22. **Self-update** (`selfupdate_test.go`): dispat replacing its own binary, which is the one thing no other area can
    witness, because it is the one command that overwrites the file it is running from. Two binaries are built at two
    versions and a fake releases API hands one out.
45. **Installing a tool** (`install_test.go`): the same machinery pointed at somebody else's repository. A fictional
    tool is published as a release asset and installed onto a folder that stands in for one on `PATH`, which is what
    makes "did the right file land" answerable by running it. What is pinned here is everything that could be assumed
    about dispat and cannot be assumed about a stranger's binary: which repository the argument names, which of the
    release's files is the binary, what the result is called and where it goes, whether it is a binary at all
    (`--pipe`), and the idempotence the destination's own checksum decides. The idempotence and the usage exits are
    what make a list of pinned installs a shell script, which one scenario runs as one.

### Manifests and editing

23. **The `compute` command** (`compute_test.go`): everything the binary derives from the manifests. The dependency
    graph: the detect/apply/check loop with its backup and convergence, `keep` and removal semantics, and the W220
    ambiguity reaching the JSON events. The baselines: `initials` entries seeded from the versions the manifests
    declare, against real tags. And the writes a `$ref` redirects: into the fragment that holds the key, or refused
    when a fragment and the keys beside it compose one value.
24. **Native auto-versioning** (`autoversion_test.go`): files reconciled by the binary at the version stage, under
    either of the two strategies or neither: range reconciliation under the match policy, own-version writes, the
    W192/W197/W203/W221 diagnostics, literal replacement over files nothing parses, and the serialised `syncLock` slot.
25. **The manifest commands** (`manifests_test.go`): `dispat scanner`, `dispat writer` and `dispat replacer`, the
    `pkg/scanner` and `pkg/writer` libraries exposed as commands. What only the binary can witness: that they run with
    no config file, no commit and no plan at all, that their outcomes reach the process exit code, and that the verify
    gates (`--verify-unlinked`, `--verify-linked`, `--forbid-range`, `--require-range`), the link sweep
    (`--drop-links`) and the build-counter write (`--set-build`) hold their contracts over a process boundary.
26. **The `autowriter` command** (`autowriter_test.go`): `dispat writer`'s edits applied to the packages the plan
    selects, including the edits derived from the workspace itself (`--set-local`, `--link-local`).
27. **The `autoreplacer` command** (`autoreplacer_test.go`): a replacement fanned out across the packages the plan
    selects, rendered once per workspace provider a covered package declares.
28. **Docker through the binary** (`docker_test.go`): the ecosystem dispat was built around and the last one it could
    read: an image-to-image edge derived from a `FROM` line nobody wrote into the config, and a release reconciling the
    consumer's `FROM` and `COPY --from` tags and a compose file's `image` and `build.tags`.

### The guards

29. **The release guards** (`guard_test.go`): the two refusals that stop a release before it starts, and the recovery
    at the other end. `run.allowBranch` turns a branch list into a precondition; the push-mode behind-remote check
    compares the checkout against the branch it would push to and refuses a stale one. The pair is also proven to be
    off unless asked for. The guard closes before the plan exists, so a commit pushed while the run is working reaches
    the finalize push instead, where the release merges itself with what landed or says why it cannot.
30. **The release lock** (`lock_test.go`): one tag on the remote decides who releases. Two runs against one repository
    is not a race dispat can win by being careful, so it refuses to enter it: the first to push the lock tag releases,
    the second is told to come back later, and the tag is gone by the time either exits.

### Correcting the record

31. **Corrections and reverted changelogs** (`corrections_test.go`): a release record is written in a commit message,
    and a commit message cannot be rewritten once it is pushed. `Edits` restates a named record and `Deletes` discards
    it, both reaching only work no package has released yet. The claims a release depends on: the corrected record
    decides the version and the changelog, a correction of released work is a visible no-op nothing can hide, the
    newest correction of a target wins, a correction narrows a record but never widens it, and a correction of a
    correction undoes it. `Reverts` closes the file: it takes a reverted entry and its revert out of the changelog
    while both still count toward the bump.

### Composing the configuration, and choosing what records

32. **References naming several files** (`multiref_test.go`): a `$ref` may name a list of files, read in order and
    merged: objects key by key with the later file winning, lists end to end. What a split configuration must keep:
    every fragment on the traced record, environment variable case through the merge, the keys beside the reference
    outranking every file it named, and record lines from two fragments arriving as one list in order. The refusals are
    configuration errors, so they stop the run before a tag, a commit or a script: no files at all, a name that is not
    a file, files holding different kinds, a missing file, and a cycle closed by any of them. `compute --write` refuses
    a key merged from several files rather than choosing one to write to.
33. **The channels a record reaches** (`channels_test.go`): `changelog.channels` and `github.channels` choose which
    releases are recorded at all (the stable line, one named prerelease channel, or every prerelease) while the
    releases themselves are still planned, tagged and published. A line inside an entry carries its own `channels`, so
    one footer says one thing on the betas and another on the stable release, with the sections between them
    unfiltered. The claims that keep it honest: a line's channels combine with its package filters, two policies
    differing only in a line's channels do not share a releaser, the skip is an info event naming the channel, and
    `dispat preview --changelog/--github` shows each body under its own entry format before anything is released.

### Where areas deliberately meet

Five subjects are asserted from more than one goal, on purpose, because a property and the feature that carries it are
different claims:

- **Versioning groups** appear in goal 2 (the modes themselves, per space) and goal 13 (a declared group spanning
  spaces). Goal 5 adds what happens when a package *joins* one, and goal 20 what happens when a selection splits one.
- **The configuration ladder** appears in goal 12 (the root as the bottom layer) and goal 13 (the six layers decided by
  the nearest). The first is about the fold, the second about who wins.
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
`harness.WriteConfigRaw`, because a mistyped key must be *rejected* at load, not silently ignored into a script-less
release.

The suite avoids duplicating the unit suites listed in
[`Architecture`](https://dispat.dev/internals/architecture/#testing). Those cover each package against
in-memory fakes, and `internal/cli`'s tests cover the controller itself (flags, arities, exit-code mapping, the init
starters). `services/dispat` hosts no end-to-end tests; this module is the sole home of every composition claim,
testing real binary builds, scheduling across real processes, config files on disk, and exit codes across process
boundaries.

## Why a separate Go module

- The suite must not import `services/dispat/internal/*`, and as a separate module it structurally *cannot* under Go's
  `internal` rule. This keeps the black-box boundary clean. The only dispat import is the **public** `pkg/models`
  module, so tests cannot reach around and inspect internal structs directly. The unit-tested git and shell code runs
  compiled inside the binary under test, driven through the `harness.Repo` fixture.
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
  draft_test.go             goal 46
  commands_test.go          goal 17
  run_test.go               goal 18
  standalone_test.go        goal 19
  filter_test.go            goal 20
  if_test.go                goal 21 (dispat if)
  if_changed_test.go        goal 21 (dispat if --changed)
  for_test.go               goal 21 (dispat for)
  for_changed_test.go       goal 21 (dispat for --changed/--unchanged)
  exec_test.go              goal 21 (dispat exec)
  selfupdate_test.go        goal 22
  install_test.go           goal 45

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

  the sequences
  longitudinal_test.go      goal 38
  entrybodies_test.go       goal 39
  smoke_test.go             goal 40
  smoke_features_test.go    goal 43

  main_test.go              TestMain: removes the shared binary build dir at the
                            end of the whole run (a sync.Once cache no t.Cleanup
                            can own)
  docs/test-plan.md         this document
```

### The tsmark probe, and why timing assertions are trustworthy

dispat's JSON logs carry RFC3339 timestamps with one-second resolution, which cannot distinguish tasks running
concurrently from tasks running back to back within the same second. Instead of scraping logs, every timing-sensitive
script wires to `tsmark`. This dependency-free Go binary appends `<label> start <unixnano>` and
`<label> end <unixnano>` lines to a shared file using atomic O_APPEND single writes, sleeping between events.

The shared log proves whether the scheduler launched one process while another was still sleeping. It bypasses shell
inconsistencies like `date +%N` and avoids host clock drift.

Every concurrency claim is verified **three independent ways** via `harness.AssertConcurrencyBudget`:

1. a sweep-line max-overlap count,
2. a brute-force O (n²) pairwise overlap count that must match the sweep line,
3. a start-order verification ensuring the (budget+1)-th task begins only after one of the first *budget* tasks
   finishes.

The peak must equal **exactly** `min(budget, tasks)`. Testing only "at most N" passes a broken scheduler that runs
serially, while checking only "N reached" passes one that ignores limits.

Flakiness posture: *ordering* assertions (`AssertSequential`) are structural and cannot flake regardless of task
duration. *Overlap* assertions rely on 100-400 ms sleeps, sitting well above process-launch jitter. The suite passes
repeated runs under `-count` and `-race`.

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
| `TestPlanTrainCatchUpStaysACatchUp`                       | The same discharge on a prerelease train: the consumer's own feature is published train history, its whole fresh cause is the provider's already-published propagation, and W193 plus the catch-up verdict and reason survive the train-wide own bump.       |
| `TestPlanFailedProviderSkipsTrainConsumer`                | The skip cascade on a train: with its own work published train history and its only fresh cause the failing provider's propagation, the consumer is skipped rather than released, because a prerelease whose entire content is an unreleased movement would record an invalid version. The healing run releases both as ordinary propagation, no W193. |
| `TestPlanProviderBuildFailureBlocksConsumerThenHeals`     | Provider fails to build; consumer is blocked (W194), never attempted; after the fix both release in one run, with neither W194 nor W193.                                                                                                                    |
| `TestPlanCatchUpWholeHistoryForNeverReleasedConsumer`     | A package created *after* a provider's propagating commit still catches up on its first ever run; an untagged package's window is the whole history.                                                                                                        |
| `TestPlanPrereleaseTrainWeirdCases`                       | `^%beta` cannot drag a stable consumer (W208); `^%beta++1` brings it onto the train; a multi-package direct transition graduates the whole train; the graduated train converges.                                                                            |
| `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` | A propagated `beta>stable` *transition* graduates the dependants still on the named train (the `release(core)%beta>stable%%beta>stable++N` form configuration.md documents), and the graduated train converges (a regression fence; see Regression fences). |
| `TestPlanChannelOnlyReleaseAndEntryPatch`                 | A release directive that only moves the channel is still a release, explained by W202; entering a prerelease channel with nothing pending takes the §11.4 entry patch, explained by W204, and its scripts execute.                                          |

### Goal 2: space versioning modes (`versioning_test.go`)

| Test                                                 | Claim proven                                                                                                                                                                                                                                                              |
|------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningFixedSpaceLifecycle`                  | Run four passes over a fixed space alongside an independent space. A change to either fixed member releases both at one version (W234 on the rider, a "no changes" changelog entry, and no leaked notes). Quiet runs converge, and the independent space never moves. |
| `TestVersioningFixedSparseLifecycle`                 | Test sparse mode across four runs. Only changed members release (no W234), while unchanged members keep their version. A member's first change jumps it to the shared space version, and a joint change lands both on a shared next version. |
| `TestVersioningFixedSharedPrereleaseTrain`           | A fixed space runs a *single* train: a `%beta` change in one member takes the whole space to `beta.0`, later work advances both to `beta.1`, one member graduating ends the train for both, and the graduated space converges. |
| `TestVersioningFixedRideFailureMidTrainHealsOntoTheTrain` | A ride can fail during any release leg. Here, the changed member continues the train while the failed rider stays untagged. The healing run joins the laggard to the train at its owed position instead of jumping to an unpublished stable core, and graduation later carries it off the train with the rest of the group. (The plain stable failure and alignment match runs 2 and 3 without the train; selection rides keep their own fence in goal 20.) |
| `TestVersioningCrossSpaceDependencyIntoFixedSpace`   | A caret dependency from an independent provider into a fixed-space member triggers an ordinary DueTo release (a version task and `DISPAT_UPDATED_*`). Its space mate rides to the same version without a version task: dependency edges remain package-scoped while versions are space-scoped. |
| `TestVersioningFixedHoldAndResume`                   | Setting `Release-As: none` on one member holds back only that member. Resuming aligns it with the published version of the space. |
| `TestVersioningFixedExactPinMovesTheSpace`           | An exact pin on one member moves the entire space to the pinned version. The pin guards (E153) continue to protect the shared version afterwards. |
| `TestVersioningRideExecutesEveryMemberScript`        | A ride executes as a full release across full and partial modes alike: build scripts run for the riding package too. |
| `TestVersioningFixedConflictResolutions`             | Two fixed-space conflicts produce warnings: competing exact pins resolve to the newest version with W235 (preventing the loser from releasing), while members targeting different channels unify on one channel with W236. |
| `TestVersioningFixedMajorLifecycle`                  | Run six cycles over a `fixedMajor` space. Patches and minors move only their target package without W234. A breaking change moves the group to a shared major with a rider changelog reading "on one major version" without leaked notes, after which the group converges and diverges again below the major. |
| `TestVersioningFixedMajorSparseLifecycle`            | In sparse mode, an unchanged member never rides across a major bump (no W234). Its next change aligns it with the shared major at the beginning of its line (`1.0.0` rather than continuing `0.x`). |
| `TestVersioningFixedMajorMinorLifecycle`             | At depth two, patches stay local while minors and breaking changes advance the entire group, recording a changelog entry that reads "on one major and minor version". |
| `TestVersioningFixedMajorMinorSparseLifecycle`       | At depth two in sparse mode, a lagging member rejoins the shared prefix on its next change of any size. The members move independently below the minor, and a later minor leaves the other member behind. |
| `TestVersioningAllModesSideBySide`                   | Run all seven modes in one repository. Minor bumps separate depths (shared under `fixed` and `fixedMajorMinor`, local under `fixedMajor`). Every shared mode applies breaking changes, sparse members never ride, and an independent newcomer versions from its own empty history (`0.0.1` instead of `1.0.0`) while shared modes adopt the group position before converging. |
| `TestVersioningFixedMajorSharedTrain`                | A train follows whatever it moves: a breaking change on `%beta` brings the whole group to `beta.0`, later commits advance both to `beta.1`, one member graduating ends the train for both, and a `%beta` on a *patch* afterwards stays inside its originating package. |
| `TestVersioningPartialPinScope`                      | An exact `Release-As` crossing the shared major advances the whole group. A pin inside the major releases only its target package, sets no group guard (no E153), and moves no siblings. |
| `TestVersioningFixedMajorRideFailureThenAlignment`   | When a partial-mode ride fails, the next run brings the laggard up to the shared major (W234) at the start of its line without re-releasing siblings. A subsequent run converges. |
| `TestVersioningMixedDepthGroupUsesTheDeepest`        | When a package overrides space `versioning`, it remains in the space group at a different depth. The group uses the deepest declaration (sharing the minor), and W237 documents the shared behavior that the shallower member did not request. |

### Goal 3: repository-scoped fatal errors (`fatal_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                  |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestFatalDependencyCycle`      | A cyclic dependency graph passes config validation but fails during planning. dispat exits 1, logs E200 events, writes no tags or scripts, and aborts `status` and `dispat run`. |
| `TestFatalDuplicateVersionTags` | Two reachable tags on different commits resolving to the same package version (`core@0.1.0` alongside a manual `core@0.1.0+dup`) create an ambiguous baseline: dispat exits 1 with E191 and leaves pending work unreleased. |
| `TestFatalShallowRepository`    | Running against a `git clone --depth 1` shallow clone aborts release with exit 1 and E196 events rather than planning against truncated git history. |

### Goal 4: change-scope ignore (`ignorescope_test.go`)

Every claim tests what a *scopeless* commit resolves to, because that is the only trigger affected by ignore patterns.
The negative cases matter as much as the positive ones: this feature narrows release scope rather than hiding a
package.

| Test                                                | Claim proven                                                                                                                                                                       |
|-----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestIgnoreScopeKeepsAFolderFromTriggeringARelease` | A commit touching only ignored files releases nothing and reports an inert unit with W131. Adding one unignored file triggers the normal release. |
| `TestIgnoreScopeDoesNotHideThePackage`              | A commit that explicitly scopes the package releases it regardless of files touched. The release commit stages ignored files, leaving a clean working tree. |
| `TestIgnoreScopeLevelsConcatenate`                  | Repository, space, and package ignore patterns all apply. A package can re-include a repository exclusion with `!`, and sibling packages do not inherit that override. |
| `TestIgnoreScopeFileAndKeyAgree`                    | A `.dispatignore` file at the repository root or package folder behaves identically to the `ignore` configuration key at those levels. |
| `TestIgnoreScopeAppliesToSince`                     | The `--since` flag uses the same file resolution logic, so packages with only ignored changes are skipped and their scripts never run. |
| `TestIgnoreScopeRefusesAPatternItCannotCarryOut`    | An invalid pattern such as a bare `!` fails configuration loading immediately with exit 1 instead of releasing packages. |

### Goal 5: release edge cases (`edgecases_test.go`)

Each test checks an *interaction* between two features that work on their own. When an interaction fails, it produces a
plausible release instead of an error, so dispat tracks them together in one suite.

| Test | Claim proven |
|------|--------------|
| `TestEdgePinPrereleaseOfTheRequiredBump` | Setting `Release-As: 1.1.0-rc.0` on a `feat` releases the release candidate and enters its named channel for future train steps. Because prereleases sort below their core version, full comparisons would treat this as a downgrade and reject it (E156); evaluating cores avoids this trap and ships the RC instead of the unready stable version. |
| `TestEdgePinPrereleaseBelowTheBumpIsStillRefused` | Checking guards against core versions does not permit shipping a breaking change as a minor RC. dispat emits E156 and publishes the computed `2.0.0` instead. |
| `TestEdgePinPrereleaseMovesAWholeGroup` | A versioning group shares a single train, so a prerelease pin on one member puts all members on the train (emitting W234 for riders) rather than graduating them to stable. |
| `TestEdgeAutoVersionSyncsWithoutPropagation` | With propagation depth defaulting to 0, separate commits to a provider and consumer trigger releases without a `DueTo` link. The parsing strategy still updates declared ranges because it inspects manifest dependencies rather than bump triggers. |
| `TestEdgeAutoReplaceSyncsWithoutPropagation` | The replacement strategy reaches the same outcome by expanding `{provider}` patterns across the package's *configured* providers. |
| `TestEdgeVersionScriptSeesEveryUpdatedProvider` | Scripts receive all updated providers: `DISPAT_UPDATED_*` lists every provider version the package consumes, not just those that propagated a bump. Hand-written `flow.version` scripts and `autoVersion` configurations reconcile consistently across spaces. |
| `TestEdgeChangelogRecordsEveryUpdatedProvider` | Changelogs record all updated dependencies: a consumer released beside its provider adds a `### Dependencies` section (`- core: 1.0.0 -> 1.1.0`), while packages without providers omit the section. |
| `TestEdgeGroupNewcomerWithNoVersionJoinsAtTheGroupVersion` | An untagged package joining an existing group releases at the group version instead of `0.0.1`. dispat logs W234, avoids W233, and converges. |
| `TestEdgeGroupMemberOnAnotherMajorIsReported` | If a member has a stray `9.0.0` tag, it moves the entire `1.x` group to `9.x` because groups version from their highest member. The release succeeds since all versions are published, and W233 identifies the deciding package. |
| `TestEdgeGroupMinorSpreadIsNotReported` | Members separated by a minor version reflect the normal outcome of a failed ride. They are covered by W234 and do not trigger W233. |
| `TestEdgeGroupSparseMemberDecidingTheMajorIsReported` | When a package overrides space versioning, it remains in the group. Because the group baseline inspects all member tags regardless of mode, a sparse member can force a major bump for the whole group, and W233 reports this attribution. |
| `TestEdgeRevertOnFailStopsAtThePublish` | If a package fails its build while another publishes, dispat rolls back the failed package directory and keeps build artifacts in the published one. Once published, changes cannot be rolled back by later failures. |
| `TestEdgeRevertOnFailIsThreeStateAtThePackageLevel` | When the root sets `true`, a package setting `revertOnFail: false` preserves its files while its unspecified sibling rolls back, exercising tri-state boolean inheritance. |
| `TestEdgeRevertOnFailNeverReachesAFailedCommit` | If a release commit fails after all packages publish, `revertOnFail` leaves the working tree modified. Restoring files would mismatch published artifacts, so dispat logs E223, writes tags, and skips directory rollback. |

### Goal 6: concurrency (`concurrency_test.go`)

| Test                                                             | Claim proven                                                                                                                                        |
|------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestConcurrencyBuildBudgetEnforced`                             | With a concurrency budget of 4 across five independent packages, active builds peak at 4. The 5th build starts only after one of the first four finishes. |
| `TestConcurrencyPublishBudgetIsIndependentOfBuild`               | Build and publish stages maintain separate concurrency budgets. In one run, builds reach an overlap of 5 while publishes stay capped at 2. |
| `TestConcurrencyIndependentPickedUpConcurrentlyDependantAwaited` | Three independent provider builds run in parallel. Their shared consumer build starts only after all three provider builds finish. |

### Goal 7: execution order by dependency graph (`order_test.go`)

| Test                                                      | Claim proven                                                                                                                                                                              |
|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOrderChainRunsInTopologicalOrder`                    | For a dependency chain `base <- mid <- top`, build and publish stages execute in topological order based strictly on `dependencies` edges. |
| `TestOrderBuildWaitsForPublishWhenConfigured`             | When `isBuildWaitingPublish: true`, the consumer build starts only after the provider *publish* completes. |
| `TestOrderBuildDoesNotWaitForPublishByDefault`            | By default, the consumer build runs *during* the provider publish, while the consumer publish still waits for the provider publish to finish. |
| `TestOrderDiamondDependencyConverges`                     | In a diamond dependency graph (`a -> b,c -> d`), packages `b` and `c` build in parallel, and `d` waits for both during build and publish stages. |
| `TestOrderVersionTaskPrecedesBuildWithUpdatedProviderEnv` | A `DueTo` consumer executes a version task where `DISPAT_UPDATED_*` points to the active provider. A directly released package in the *same space with the same versionScript* skips the task. |
| `TestOrderProviderFailureSkipsTheWaitingConsumer`         | A failed `isBuildWaitingPublish` provider skips its consumers unconditionally (W194), their own work notwithstanding — the build consumes the publish that never happened — while without the flag the own-reason rule stands and the consumer releases. |

### Goal 8: interruption (`interrupt_test.go`)

| Test                            | Claim proven                                                                                                                                                                                                                                                                                                                                                                                |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestInterruptGracefulShutdown` | Sending a SIGINT mid-build (via `harness.StartRelease`/`Proc`) exits non-zero and marks both packages as `cancelled` in summary events. dispat creates no tags, treats killed builds as interruptions rather than failures, and releases both packages at their owed version on the next run. |
| `TestInterruptStopsARunCommand`  | Because `dispat run` shares the release scheduler, a SIGINT during script execution exits non-zero, prevents subsequent packages from starting, and tags nothing. |

### Goal 9: the script frames (`hooks_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestHooksLoginOncePerSpaceAcrossSpaces`              | When two spaces share identical login *script text*, dispat executes the login once **each** because gates are keyed by space rather than script content. |
| `TestHooksLoginFailureIsolatedToItsSpace`             | A login failure aborts all publishes within that space without affecting publishes in other spaces. |
| `TestHooksLoginRunsInTheSpaceFolder` | The login script runs in the space directory rather than the directory of the package that triggered the gate first. Racing members write their working directory to that space folder to verify location consistency for local credentials. |
| `TestHooksLoginOfAStandalonePackageRunsInItsOwnFolder` | A standalone package acts as its own space, so its login runs inside its own package directory rather than an unowned parent folder. The hook resolves through the root `flow` because `flow.login` cannot be declared on package entries. |
| `TestHooksOnFailAndOnSkipOutcomeScripts`              | When a run fails, `flow.onFail` runs for the failed package with `DISPAT_FAILED_STAGE` and `DISPAT_ERROR`, `flow.onSkip` runs for blocked consumers with `DISPAT_BLOCKED_BY`, and published packages trigger neither. An `onFail` sequence continues even if its initial command fails. |
| `TestHooksRevertOnFailAppliesAfterVersionStageOnSkip` | When a consumer version script modifies its folder and the provider publish fails, dispat rolls back the skipped consumer directory. |
| `TestHooksScriptOutputsCarryAcrossStagesAndHooks`     | Environment variables export across stages and hooks: a `beforeBuild` *hook* export passes to build and publish stages, build exports pass to publish with `DISPAT_OUTPUTS` preserving order, and `onFail` receives the hook export **and** exports from the failed build. |
| `TestHooksRunLevelHooks`                              | Run-level hooks execute in order from the monorepo root against a real remote. The `postAll` hook inspects run outcomes and workspaces, while quiet runs skip commit and push hooks because those phases never execute. |
| `TestHooksRunLevelHooksAreTheReleasesOwn` | Running `dispat commit --tag --push` creates commits, tags, and pushes without triggering run-level hooks, while `dispat release` executes all seven hooks once. Run hooks attach to the release lifecycle, whereas step commands are directly invoked by external flows. |
| `TestHooksRunLevelHookFailureSemantics`               | A failure in a warn-only hook like `postAll` logs a warning and allows the sequence to finish, whereas a failure in a gating hook like `beforeAll` halts the run before release work starts. |
| `TestHooksAllStageHooksFireInOrder`                   | All nine per-package hooks and the announce stage run in documented order across a provider and consumer pair. The consumer also runs the version stage and its two hooks within that frame. |
| `TestHooksStageHookAuthoritySplit`                    | Failures in `postPublish` and announce hooks log warnings (exiting 0 and preserving tags), whereas failures in gating hooks like `postBuild` fail the package, prevent tagging, and invoke `onFail` with the failing stage. |

### Goal 10: config loading, resolution and options (`config_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestConfigUnknownKeyIsRejected`                       | Misspelled top-level keys cause dispat to exit 1 immediately instead of ignoring unknown fields. |
| `TestConfigFileFallbackResolution`                     | Without `--config`, dispat loads the first file found among `dispat.json`, `dispat.yaml`, `dispat.yml`, and `dispat.toml`. If none exist, it exits 1 and lists the attempted file names. |
| `TestConfigResolutionAscendsToTheMonorepoRoot`         | Without `--config`, configuration lookup searches parent directories starting from `--root`. Running from `packages/core` tags and updates changelogs identically to running from the repository root, while passing `--config` disables directory ascent. |
| `TestConfigGitRepositoryGuard`                         | Running `status` outside a git repository exits with a descriptive error before executing commands, and `init` rejects `--root` paths that are not repository roots. |
| `TestConfigConcurrencyFlagOverridesFile`               | The `--concurrency` flag overrides file configuration *at runtime*, verified by measuring active task overlap. |
| `TestConfigCustomShellIsUsed`                          | Setting `"shell": ["/bin/bash", "-c"]` switches the script interpreter, enabling bash-specific syntax that fails under `/bin/sh`. |
| `TestConfigCustomObjectIsIgnored`                      | A `custom` object placed at the root, space, or package level loads cleanly without triggering unknown key errors or altering release execution. |
| `TestConfigNonPackageScopesReplacesDefault`            | Configuring `nonPackageScopes` **replaces** the default `["release"]` list: the custom scope becomes exempt and `release` loses its exemption. |
| `TestConfigFusedPrereleaseTagFormatRoundTrips`         | Setting `{name}%v{version}-{channel}{counter}` writes tags like `beta0` and increments to `beta1` over three runs. A second space using standard formatting keeps its own style, proving tag formats are per-space properties. |
| `TestConfigParserOptions`                              | The top-level `parser` block controls commit parsing: custom types trigger releases, default propagation depth reaches consumer manifests without carets, `strictTypes` raises E140 warnings, and invalid parser settings exit 1. |
| `TestConfigCommitErrorsPolicy`                         | Under the default `warn` setting, commit errors (E130) log warnings while releasing sibling packages. Under `error`, dispat aborts releases with exit 1 while `status` still exits 0 to display the plan. |
| `TestConfigParserQuiet`                                | The `parser.quiet` setting hides parser warnings (E140) while keeping planner errors (E130) visible. It counts hidden diagnostics, enforces `commitErrors: error` failures, and can be overridden in either direction with `--quiet-parser`. |
| `TestConfigInitialsBaselines`                          | Initials configure the baseline version for packages with unparseable latest tags without falling back to older tags. Unmatched initial keys log warnings, and future releases resume tracking real tags. |
| `TestConfigFormatsSmoke`                               | Verify configuration compatibility across `dispat.json`, `dispat.yaml`, and `dispat.toml` generated by `init`. The JSON test also confirms that `status` plans without tagging. |
| `TestConfigNamesKeepTheirCaseEndToEnd`                 | A map key keeps the case its file wrote, so a package, its space and a standalone package's synthetic space are reported and tagged under the names the author chose, and reach a script as `DISPAT_PACKAGE`, `DISPAT_SPACE` and the workspace listing. The selectors that address them — a `--package` flag, a flow entry, a commit scope — may spell them any other way. |
| `TestConfigRefusesTwoSpellingsOfOneName`               | Two keys of one object that fold together have no lookup that could choose between them, so the load refuses them by name and nothing runs. |
| `TestConfigDispatexcludeSelectsTheConfigFile`           | When multiple configuration files exist in a directory, `.dispatexclude` names files to ignore. This behavior applies at the root, space, and package levels, validated by checking format-specific tag output. |
| `TestConfigResolutionAscendsPastASpaceFile`            | When a space configuration file declares `packages`, invoking dispat inside the space directory still ascends to the root configuration that manages the space. |
| `TestConfigSpaceLayerRejections`                       | dispat rejects invalid layer declarations before planning: `path` inside space package lists, `path` or `spaces` in space files, `packages` in package files, and space package patterns matching no folders. |
| `TestConfigRefSplitsTheFile`                           | Splitting configuration across files with `$ref` produces identical releases to single-file setups. Running with `--log-level trace` outputs every referenced path. |
| `TestConfigRefCycleFailsBeforeAnyWork`                 | Circular `$ref` references cause dispat to exit 1 and print the reference cycle before running scripts or tagging releases. |
| `TestConfigRefMissingFragmentIsNamed`                  | A missing `$ref` target causes dispat to exit 1, naming the referencing file, the problematic key, and the missing file path. |

### Goal 11: the static `env` layers (`env_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestStaticEnvReachesScripts`                          | Environment variables merge across top-level, space, and package layers, with local values taking precedence. Variable names maintain exact casing, `$DISPAT_VERSION` expands to the package version, and run hooks access only top-level variables. |
| `TestStaticEnvCannotShadowComputedVariables`           | Defining static variables with the reserved `DISPAT_` prefix fails configuration loading rather than being ignored, ensuring scripts can safely rely on `DISPAT_VERSION`. |
| `TestReleaseGroupVariableReachesScripts`               | `DISPAT_GROUP` names the versioning group whose versions move together, the package's third address beside its name and its space. A grouped package carries the group name, an independently versioned one leaves the variable unset rather than empty, and a static `env` naming it is refused at load like every other computed variable. |
| `TestStaticEnvRefusesUnusableKeys`                     | dispat rejects environment variable keys containing `=` or empty names during configuration loading and reports the reason. |
| `TestStaticEnvFromFolderConfigFiles`                   | Environment variables defined in space and package configuration files reach scripts with exact casing, local definitions taking precedence. |
| `TestStaticEnvReachesTheLoginScript`                   | Space-level `env` variables pass to login scripts, which execute once per space in the space directory without package context. |
| `TestStaticEnvFromARefKeepsKeyCase`                    | Environment variables imported through `$ref` fragments preserve their original casing in script environments. |
| `TestDotenvReachesScriptsAndDispat`                    | Variables from a root `.env` file reach scripts. Process environment variables override `.env` values, config `env` overrides both, and variable values are never written to logs. |
| `TestDotenvFileFlag`                                   | The `--env-file` flag overrides the default `.env` path, merges multiple files with later flags winning, and exits 1 if a specified file is missing. |
| `TestDotenvSteersDispatItself`                         | Variables defined exclusively in environment files expand inside dispat templates, such as changelog footers. |

### Goal 12: the configuration ladder from the root down (`levels_test.go`)

Each test validates an effect that only a specific layer could produce. Examples include a log line from one script, a
version from one versioning mode, or a file preserved by one `revertOnFail` setting.

| Test                                      | Claim proven                                                                                                                                                                                    |
|-------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestLevelsRootFlowReachesEverySpace`     | A root `flow` applies to all packages across spaces. Spaces and packages can override specific step commands while inheriting others, allowing three packages to run distinct builds alongside a shared publish step. |
| `TestLevelsRootBooleansAreThreeState`     | Setting `revertOnFail: true` at the root applies to spaces without explicit settings, while a space setting `false` keeps its build artifacts, demonstrating tri-state boolean inheritance. |
| `TestLevelsRootVersioningAppliesPerSpace` | Setting `versioning: fixed` at the root groups packages per space: packages in one space bump together while an opted-out space remains unaffected, unlike repository-wide `versionGroups`. |
| `TestLevelsRootReachesAStandalonePackage` | Standalone packages outside spaces act as their own space, inheriting root `tagFormat` and `flow` configurations. |
| `TestLevelsSpaceRecordsAndSrc`            | Spaces configure `changelog` defaults (which packages can override) and `src` boundaries (where changes outside the path leave the package inert with W131). |

### Goal 13: per-package overrides, versioning groups and `.dispatexclude` (`overrides_test.go`)

| Test                                         | Claim proven                                                                                                                                                                                                        |
|----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestOverridesFlowBuildPerPackage`           | A `packages` entry replaces one flow entry for one package alone: the override's build runs for it, the space's for the sibling, the un-named stages inherit (both publish and tag), and a second run converges.    |
| `TestOverridesFlowScriptResolvesPerPackage`  | One `flow.build: build` reaches three different commands in one release: the package's own `scripts`, its space's, and the file's, resolved most-local-first for whichever package the stage runs for.             |
| `TestOverridesFlowScriptSuppliedByEveryPackage` | A space's flow entry may name a script only its packages define, and the release runs each package's own; removing one package's entry fails the config with an error naming that package.                       |
| `TestOverridesInFolderFileWins`              | The package folder's own dispat.json is the most local layer: its `tagFormat` beats the `packages` entry's, proven by the tag the release actually creates, while the sibling keeps the repository default.         |
| `TestOverridesDispatexclude`                 | A folder listed in `.dispatexclude` is not a package: never released, and a commit scoping it draws the unknown-scope diagnostic (E130) like any non-package name.                                                   |
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
| `TestSpaceFileDependenciesThroughTheBinary`                       | The space folder's own config file declares edges too, and they add to the root file's space entry rather than replacing it: only the file's edge can explain the consumer's release.                           |
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
| `TestRecordsCommitModeGithubFinalize`                             | GitHub in commit mode: releases created in the finalize phase, the body documenting the exact commit and tag, the recorder opt-in per package (no export, no release), a `PACKAGE_<KEY>` export overriding commit and `target_commitish`, and `commit.messageFormat` rendering `{packages}`/`{tags}` for the releases the commit itself records (an exported-commit package is its own commit's claim).                                                          |
| `TestRecordsCatchUpGithubBodySpansTheProvidersMovement`           | A catch-up's GitHub release body spans the provider's movement from this package's previous release — never the provider's collapsed before-and-after ("0.1.1 -> 0.1.1").                                              |
| `TestRecordsGitHubAllPackages`                                    | `github.allPackages` gives every published package a release without exporting `DISPAT_EXPORT_GITHUB`, leaving the export to add assets only; the default keeps the export as the per-package opt-in.                                                                                                                                                          |
| `TestRecordsChannelsHoldPrereleasesBack`                          | `changelog.channels` / `github.channels` naming the stable line alone leave a beta tagged and published but unrecorded, while the graduation to stable writes the one entry and the one release covering the window; a per-package override naming the stable line and every prerelease opts back in. |
| `TestRecordsGitHubReleaseExistsIsASkip`                           | A release the repository already carries is a W224 skip rather than the API's 422, so a repeated `dispat github` and the release that follows both converge instead of failing.                                                                                                 |
| `TestRecordsHeaderAndFooterPerEntry`                              | `header` and `footer` belong to the entry, not the file: two releases leave two of each, bracketing that entry's sections, while a multi-line `fileTitle` heads the file exactly once.                                                                                                                         |
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
| `TestRecordsAliasBesideABareVersionTag` | The single-package convention a GitHub composite is consumed through: releases tagged `v1.4.2` and the `v1` a caller pins, which shares the release format's prefix. The configuration loads, the release writes both refs at one commit, and the run after it still reads the release as the baseline rather than the alias sitting beside it. Across a major the moving alias follows the new line and leaves the old one where it was. |
| `TestRecordsAliasTagFailureIsOnlyAWarning` | An alias that cannot be written (force off, and a ref already holding the name) is W232 and nothing more: the run exits **green**, the release tag is unaffected, the existing ref is left alone, and no critical is counted. An alias is a convenience pointer at a release, not the record of one. |
| `TestRecordsTagAtAnotherCommitIsLeftAlone` | A tag dispat can see at the wrong commit is reported (E221) and left there, because a tag moved here would be force-pushed over the copy on the remote and turn one local mistake into everyone's. Reached through `dispat commit --tag-name`, since a release plans its version *from* the tags. |
| `TestRecordsForceRewritesAnUnreachableTag` | A tag on a commit this branch cannot reach is invisible to the planner, so it records nothing dispat can plan around: with force on (the default) the write succeeds and the tag names this release. Force means "do not fail because the ref exists", not "overwrite whatever is there". |
| `TestRecordsPushForceReplacesExistingRemoteTags` | A tag the remote already carries is overwritten rather than skipped forever, closing the window between the check and the push and giving a moving tag its only way to move. The replacement is reported, not silent. |
| `TestRecordsTagFailureDoesNotUnpublishTheRelease` | The post-publish failure model end to end: the run publishes, says so, refuses to move a tag sitting at a foreign commit, carries on through the packages after it, and exits non-zero, without ever calling the published package failed. |

### Goal 46: draft GitHub releases (`draft_test.go`)

| Test | Claim proven |
|------|--------------|
| `TestDraftReleasesWaitForAHumanToPublish` | `github.draft` creates the release as a draft, and every later pass over it skips (W224) instead of leaving a second draft: the by-tag lookup cannot see a draft, so the skip is the release listing's answer, end to end through the binary. |
| `TestDraftFlagHoldsBackAndTheFlipAbandonsTheDraft` | `--draft` drafts a release the configuration would have published; turning drafting off again creates the published release beside the stale draft rather than searching for it, which is what keeps the calls of everybody who does not draft exactly as they were. |
| `TestDraftFlagPublishesOverAConfiguredDraft` | `--draft=false` publishes over `github.draft`, and the release it created is what the run's own drafting recorder finds through the ordinary by-tag lookup. |

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
| `TestRunWindowSelectionWithoutTheScriptIsANoOp`    | A name that exists but nowhere in a selection only the window assembled exits 0 as a reported no-op — the info event names the script and the covered packages — and the same name over a window that does contain its package runs it.                                                                     |
| `TestRunSinceWindowWithoutTheScriptIsANoOp`        | The sweep-in-CI shape of the rule: a commit touching only a standalone package without the script makes `--since HEAD~1` cover it alone, and the sweep exits 0 with the report instead of demanding a dummy script.                                                                                          |
| `TestRunSpaceFileScriptInAnEmptySpace`             | A script written only in an empty space's own folder file counts as defined — the typo guard reads the space files, not just the discovered packages — so the run is the window no-op rather than the unknown-name error, which an actually unknown name still gets.                                        |
| `TestRunConsumersWindowWithoutTheScriptIsANoOp`    | `--consumers` expands a window-only selection without making it explicit: a script the expanded selection still cannot resolve stays a reported no-op, the event listing the expanded coverage.                                                                                                              |
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
| `TestStandaloneGithubPublishesFromAStageScript` | The github step inside an announce stage: the build's `DISPAT_EXPORT_GITHUB` reaches the command through the stage environment, one release is created for the package `DISPAT_PACKAGE` names, and the exported file is attached. The run's own recorder finds that release already there (W224) and creates no second one. |
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
| `TestFilterRefusesASelectionWithoutTheScript`   | An explicit selection reaching only packages that resolve no command for the name exits 1 in every spelling: a package term, a space term, the invocation folder, and a term composed with `--since all`.                                          |
| `TestRunMixedSelectionRunsTheResolvers`         | An explicit selection refuses only when nothing resolves the name: one resolver among the named packages runs, the rest complete as no-ops, and the command exits 0.                                                                              |
| `TestFilterStepCommandsSelect`                  | The step commands take the same terms and the same folder inference; a selected package the recomputed plan is no longer releasing is a logged no-op, not a failure; an unmatched term exits 1.                                                   |
| `TestFilterPreviewSelects`                      | Preview takes the same terms and folder inference, and names the selection it found nothing pending for.                                                                                                                                          |
| `TestFilterComputeScopesSuggestions`            | Compute reports and writes only the selected consumers' edges while still detecting against every package's manifests, so a declared edge onto an unselected provider is never proposed for removal; the in-sync line names the scope.            |
| `TestFilterReleaseSelectsPartOfTheGraph`        | A release takes the same terms: `-p core` tags and publishes core alone, `-s apps` that space's package, the graph marks what was left out, and a later unfiltered run releases the rest without re-releasing what is already out.                |
| `TestFilterReleaseWithholdsWhatTheOrderCannotReach` | A selected consumer whose provider is releasing and unselected is withheld (`W230`, naming the provider) and nothing is released; naming the provider too releases both; once the provider is out, the consumer alone is a fine selection.    |
| `TestFilterReleaseStrictRefusesBeforeAnythingRuns` | `--strict` turns the withholding into exit 1 with no tags, no stage scripts and the releasable half of the same selection untouched; without it that half releases; a clean selection is unaffected by the flag.                             |
| `TestFilterReleaseSplitsAVersioningGroup`       | Taking part of a `fixed` group releases it and warns (`W231`) rather than refusing; `--strict` refuses the same selection with nothing released; the next run makes the group whole with the member left behind releasing the split-off work at the version that already carries it (no ride, no W234), and then converges.                   |
| `TestFilterReleaseInfersFromTheInvocationFolder`| A release run from inside a package folder is that package's release; from the root it is still the whole monorepo.                                                                                                                              |
| `TestFilterReleaseRecordsOnlyWhatReleased`      | The durable records follow the narrowed run: the release commit names only the created tag, only the released package's changelog is written, and no tag exists for a package left out.                                                          |
| `TestFilterStatusSelects`                       | `status` narrows the same plan while still printing every package (`⊝ not selected`, `⊘ withheld until its providers release`), reports `W230` and exits 0, exits 1 under `--strict`, is clean when a space term brings the provider along, and fails on an unmatched term. |
| `TestFilterSelectsAVersioningGroup`             | Every `--group` spelling selects the group's packages wherever they live: a group joined by a space and by a standalone package, a space that versions as its own group, comma-separated and repeated terms, a glob, `'*'`, and a union with the other two flags that selects a package named twice over once; `-g '*'` reaches no independently versioned package. |
| `TestFilterUnknownGroupTermsAreErrors`          | A `--group` term naming nothing exits 1 listing the groups there are, and the three flags name each other: a space in `--group`, a package in `--group`, a group in `--space` and in `--package`; a repository with no groups at all says so.     |
| `TestFilterGroupSelectsForEveryCommand`         | The group term narrows `preview`, `status`, `changelog`, `commit` and `compute` exactly as the other terms do, leaving the other group untouched.                                                                                                 |
| `TestFilterReleaseByGroupNeverSplitsIt`         | Naming a member of a group under `--strict` is refused (`W231`) while naming the group releases every member at once, clean under `--strict`, across a space and a standalone package alike; a later unfiltered run finishes the rest.            |
| `TestFilterPositionalPackagesAreAUsageError`    | A bare package name after `run`, `preview`, `changelog`, `autoversion`, `commit` or `compute` is a usage error (exit 2): the selection is a flag.                                                                                                 |

### Goal 21: the shell helpers (`if_test.go`, `for_test.go`, `exec_test.go`)

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
| `TestForIteratesALiteralList`                   | A list of words, one script per word, in the order they were typed, with `DISPAT_ITEM`, `DISPAT_INDEX` and `DISPAT_TOTAL` per iteration, in a repository holding no config file at all — so anything that works there provably read none. An item is one argument whatever is inside it, spaces and quotes included: the shell that typed the line already decided where the words end. |
| `TestForPropagatesTheExitCode`                  | The failing item's own code becomes the command's and the items after it never start; `--keep-going` finishes the list and still reports that first code even though a later item failed worse; `--on-failure` replaces the code and runs once for the loop rather than once per failing item, and stays quiet when nothing failed. |
| `TestForRunsEveryDoScriptPerItem`               | Several `--do` scripts are one item's sequence: they run in order per item, an item stops inside its own sequence at the first failure, and that failure stops the loop.                                        |
| `TestForOverNothingSucceedsUnlessItemsAreRequired` | An empty list runs the body zero times and exits 0, which is what `for x in $EMPTY` does; `--require-items` turns that into exit 1, for the literal list and for a domain whose window is legitimately empty alike, and leaves a non-empty iteration alone. |
| `TestForIteratesPackages`                       | `-p` iterates over the packages the terms name, in discovery order, describing each with the release environment's own variable names: a declared group, a space that versions as one lending its members its name, and `DISPAT_GROUP` unset rather than empty for an independent package. A term matching nothing is an error, never a loop that ran zero times. |
| `TestForRunsEveryItemWhereTheCommandWasInvoked` | The two halves of one decision: a relative path in the script resolves in the invocation folder for every item, so one path means one file however long the list, while `DISPAT_DIR` carries the item's own folder as an absolute path for a script that wants it. |
| `TestForIteratesSpacesAndGroups`                | `-s` and `-g` iterate over the spaces and the versioning groups themselves rather than over the packages inside them; a space carries its primary folder, a group carries no folder because it is a versioning relationship; and an unknown term fails with the filter's own message, cross-flag hint included. |
| `TestForReadsTheConfigOnlyWhenTheListNeedsIt`   | The command's cost rule as one comparison: a path and `cwd` place the loop with nothing read, `pkg:` places it correctly, and then the config file is broken and only the invocations that had to look something up notice. `--in` moves every iteration, not only the first. |
| `TestForCannotBeToldWhichItemItIsOn`            | The iterator variables are appended last, so an outer `DISPAT_ITEM`, `DISPAT_INDEX`, `DISPAT_TOTAL` or `DISPAT_PACKAGE` inherited from an enclosing run loses. That is what makes a loop safe to nest inside a release stage, which is its natural home. |
| `TestForUsesTheConfiguredShell`                 | The reason the command exists: a bashism invalid under `/bin/sh -c` succeeds once `shell` names bash, so the loop body runs through the shell the repository configured rather than through a fixed one.        |
| `TestForIsReservedAndRefusesBadFlags`           | `for` is a command word, never the run shorthand; a missing `--do`, items beside a flag source, two of `-p`/`-s`/`-g` without a window, `--changed` with `--unchanged`, `--consumers` without a window, a malformed `--in` and arguments after `--` are all usage exits taken before any config is read. |
| `TestForShadowsARunScriptCalledFor`             | The cost of the word, pinned deliberately: the command wins over a run script named `for`, and `dispat run for` is the spelling that still reaches it.                                                          |
| `TestForChangedIteratesTheReleaseWindow`        | Without `--since` the loop covers what a release would, covers nothing once the release has happened, and `--unchanged` holds exactly the complement at each step.                                              |
| `TestForChangedWithSince`                       | A bare `--since` is `--changed --since`, spelled as `dispat run` spells it, and the two spellings are one source rather than two; `--since` moves the complement with it; `--since all` covers every package and empties the complement; a revision git cannot resolve is a failure naming it, never an empty list. |
| `TestForChangedNarrowsToTheSelection`           | The overload stated as one scenario: under a window flag `-p`, `-s` and `-g` narrow it, and with no window flag the same three are the source and two of them together are refused. A term matching no package is an error on either half. |
| `TestForChangedConsumersReachDownstream`        | `--consumers` expands the window downstream, moving a package out of the complement by exactly what the window gained — and unlike `dispat if --changed`, the no-terms root invocation is ordinary here rather than refused, because the flag really does change the list. |
| `TestForChangedIteratesInDependencyOrder`       | The window comes out in the plan's dependency order, so a provider is visited before its consumer whatever order the folders or the commit named.                                                               |
| `TestForChangedInfersFromTheInvocationFolder`   | Invoked inside a package folder with no terms, the window narrows to that package: the same invocation means "every changed package" at the root and "this one, if it changed" inside one, with `--unchanged` answering for the package that did not. |
| `TestForChangedNeedsTheRepositoryItAsksAbout`   | The window sources are the ones that cost a config file and a git repository, and only they pay: with the file broken or the repository gone, a literal list still runs in the same folder where `--changed` and `--unchanged` refuse. |
| `TestForChangedPropagatesTheExitCode`           | The helper stays transparent whatever the list came from: a failing item ends the loop with its own code, and `--keep-going` finishes the window while still reporting that first failure.                       |
| `TestExecResolvesTheSubjectsScriptThroughTheBinary`             | The subject picks the level, and only that level: root, space and package each answer with their own text, a package declaring nothing is a reported miss, and standing in a package folder changes no answer.         |
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

dispat builds two binaries at two versions and exercises them against a fake releases API. This tests the real
binary-swapping flow on disk rather than mocking the filesystem.

| Test | Claim proven |
|------|--------------|
| `TestSelfUpdateReplacesTheRunningBinary` | The whole path over the process boundary: the binary downloads its successor, checks it against the published size and checksum, runs it, and steps aside for it, and the same path then answers with the new version while the old one waits beside it. |
| `TestSelfUpdateRefusesWhatItCannotTrust` | A checksum that does not describe what arrived is refused with the working binary untouched and no backup created: the checks stand between a download and the only binary the user has. |
| `TestSelfUpdateInstallsANamedVersion` | `--release` reaches any published version, downgrades included, and refuses one nobody published. That is what makes a bad release recoverable after the backup's week is up. |
| `TestSelfUpdateRollsBackAndBackAgain` | `--rollback` restores the backup and rotates, so a second rollback returns to where it started and the directory is never left holding a parked file. A restore is only safe if it is itself reversible. |
| `TestSelfUpdateAndPrereleases` | A release candidate is not an update by default, `--prerelease` opts into the candidates, and `--force` is the way back off that line. |
| `TestSelfUpdateBackupExpiresOnItsOwn` | The replaced binary survives six days and is cleared by the next command after eight, with nothing else in the directory touched. |
| `TestSelfUpdateNotice` | The half that reaches somebody who was not thinking about updating: the notice rides out on an ordinary command, stays out of JSON output, and is not made at all when the configuration says no. |
| `TestSelfUpdateOverTLS` | The one scenario served over https, behind an authority generated for the run: with the authority named in `SSL_CERT_FILE` the binary completes the handshake, verifies a leaf issued for the name in the URL and reports the available release, and with no authority named it refuses and says the certificate is why. The trusted half is skipped on darwin, where stock Go verifies through the platform verifier and ignores `SSL_CERT_FILE`; the refusal is verifier-independent and runs everywhere. |
| `TestSelfUpdateWithNothingForThisPlatform` | A release cut before a platform joined the build matrix names the binaries it does have rather than leaving the reader guessing. |
| `TestSelfUpdateWithoutAStableRelease` | Where every tag is a candidate, "no matching release" naming the flag that would find one beats "you are up to date". |
| `TestSelfUpdateCommandWordKeepsItsScript` | Every command word permanently shadows a run script of the same name, which is why the word is `self-update` and not `update`. A deliberate, tested fact rather than a surprise. |
| `TestSelfUpdatePrintsWhatChanged` | An update answers the question it raises. The change sections of the release body reach the terminal, the install commands and footer links the same body carries do not, and the changelog is linked at the tag that was installed so the link keeps saying what it said today. |
| `TestSelfUpdateCheckShowsWhatWouldArrive` | `--check` shows the notes it would install while still installing nothing and still exiting 1, which is what makes it the invocation you run while deciding rather than a bare version comparison. |
| `TestSelfUpdateNotesNeverBlockTheUpdate` | An empty body, a body that is only a footer, markup dispat reads nothing in, and a body far past the parser's cap all end with the new binary in place and the link left to carry the answer. The notes are a courtesy; the binary is the point. |
| `TestSelfUpdateReadsTheNotesBeforeTheDownload` | The fake records the order it was asked, proving the notes ride off the response that chose the release rather than a second call afterwards, and that the binary is fetched exactly once. |
| `TestSelfUpdateNotesReachTheJSONStream` | Under `--log-format json` every line is an event and the `update installed` event carries the notes and the changelog as fields, so a job that updates dispat can post what changed without scraping stdout. |
| `TestSelfUpdateFromAPrivateRepository` | A fork released only inside a company: the fake publishes nothing without the credential and answers the public download URL with a sign-in page, so a binary that has actually been replaced proves `GITHUB_TOKEN` reached both the listing and the asset's API endpoint, and the public URL was never asked. |
| `TestSelfUpdateFromAPrivateRepositoryWithANamedToken` | `--token-env` names the variable holding the credential, and the conventional one is not consulted once it does: a wrong `GITHUB_TOKEN` beside the named one changes nothing, and the asset still arrives through its API endpoint. |
| `TestSelfUpdateWithoutATokenStaysOnThePublicURL` | Every release the fake publishes names an asset endpoint, as every real one does, and without a token the download still goes to the browser URL and never touches the endpoint that wants one. |

### Goal 45: installing a tool (`install_test.go`)

dispat publishes a fictional tool as a release asset and installs it onto a folder standing in for one on `PATH`. The
tool is a script that reports its own version, so every claim about which file landed is answered by running it.

| Test | Claim proven |
|------|--------------|
| `TestInstallAToolFromAnotherRepository` | The whole path over the process boundary: `--check` gates and touches nothing, then the file the release named is fetched, checked against the published size and checksum, and put on `PATH` under the repository's own name, where it runs and says which version it is. A first install keeps no backup, because there was nothing to keep. |
| `TestInstallIsIdempotent` | The destination is hashed against the checksum the release published, so a provisioning script may run the same line on every boot: the second run pays for no transfer, `--check` agrees by exiting 0, a destination somebody else overwrote is installed again, and `--force` installs over a match while keeping what it replaced. |
| `TestInstallKeepsAndRestoresWhatItReplaced` | The safety property self-update is built around, for a tool dispat knows nothing about: the replaced binary is kept, `--rollback --check` reports it without restoring it, and the restore rotates, so a second one returns and the folder is never left holding a parked file. |
| `TestInstallBackupExpiresOnItsOwn` | The kept copy survives six days and is cleared by the next install of that same tool after eight, with nothing else in the folder touched, and the refusal that follows says how to reach an old version anyway. |
| `TestInstallNamesTheFileAndTheFolder` | A project whose binary is not called after its repository, installed somewhere the reader chose: `--as` and `--bin-dir` are both honoured, the folder is created on the way, and a folder that is not on `PATH` is said out loud while one that is says nothing, because a tool the shell cannot find is a successful install that looks like a failed one. |
| `TestInstallRefusesToGuessWhichFileIsTheBinary` | Which of a release's files is the binary is never inferred, with one exception that is a convention rather than a guess: `{name}-{os}-{arch}` is looked for first and exactly, needs no `--asset`, and is the file that actually lands on `PATH` and runs. A release naming its files anything else is refused with the name dispat tried and everything it found, exactly one file needs no flag, a glob reaches a name nobody wants to type, a glob matching two is refused, and a release with nothing for this platform names what it does have. |
| `TestInstallRefusesWhatItCannotTrust` | A checksum that does not describe what arrived is refused with the folder untouched and the staged file cleaned up, and the failure names `install` rather than the package doing the work. |
| `TestInstallRefusesADestinationItMustNotReplace` | A name that is already a directory belongs to somebody. Installing over it would rename that directory aside to put a binary where it stood, so it is refused instead. |
| `TestInstallRefusesAFolderItCannotWriteTo` | The first failure a real install meets, since /usr/local/bin belongs to root on most machines: the refusal arrives before the transfer rather than after it, and says what to do about it. |
| `TestInstallWithoutAPublishedChecksum` | A release publishing no digest is installed with the size check standing alone, said out loud, and never reported as already installed: the guess that skips the install is the one that leaves a machine on an old binary forever. |
| `TestInstallReachesAnyPublishedVersion` | A prerelease is not an update by default, `--prerelease` opts into the candidates, `--release` reaches any published version going backwards, and one nobody published changes nothing. |
| `TestInstallReadsTheRepositoryTagsHoweverTheyAreSpelled` | `--tag-prefix` is what makes a foreign repository legible: a prefix nothing carries is refused with both flags that would find a release named, an empty value reaches a repository tagging `1.2.3`, and a module path reaches one release of a monorepo. |
| `TestInstallPipesAnAssetThatIsNotABinary` | `--pipe` hands the verified file to a command in the install folder, so an archive unpacks where a binary would have landed, the same file is offered by path and by name for a command that has to seek, and a pipe that fails is a failed install leaving nothing behind. |
| `TestInstallPipeSeesTheAssetUnderItsOwnName` | The staged file carries the name the release published, so a command switching on `.tar.gz` sees both suffixes rather than the temporary name it arrived under. |
| `TestInstallPipeIsAlwaysSomethingToDo` | A pipe has no destination file to compare against, so the gate says so rather than inventing an answer, and `--check` still downloads nothing. |
| `TestInstallPipeOutputStaysOutOfTheJSONStream` | Under `--log-format json` every line of the output stream is an event, so a piped command's own chatter goes to the error stream, where a person still sees it. |
| `TestInstallKeepsTheTokenAwayFromAnotherHost` | The endpoint comes from an argument rather than from a flag set on purpose, so a URL naming another host is not enough to make dispat hand it the `GITHUB_TOKEN` in the environment; `--token-env` is how a token is sent deliberately. |
| `TestInstallFromAPrivateRepository` | A tool released only inside a company: the fake publishes nothing without the credential and answers the public download URL with a sign-in page, so the tool landing on `PATH` and running proves the token reached both the listing and the asset's API endpoint, and the public URL was never asked. |
| `TestInstallFromAPrivateRepositoryNeedsTheTokenNamed` | The endpoint comes from an argument, so `GITHUB_TOKEN` alone is dropped and the private repository stays unreachable until `--token-env` names the variable; the run says which flag would have sent it rather than leaving an unexplained refusal. |
| `TestInstallWithoutATokenStaysOnThePublicURL` | Every release the fake publishes names an asset endpoint, as every real one does, and without a token the download still goes to the browser URL and never touches the endpoint that wants one. |
| `TestInstallEventsReachTheJSONStream` | The `install check` and `tool installed` events carry the repository, the tag, the asset and the destination, so a job that provisions a runner records what it installed without scraping stdout. |
| `TestInstallTracesTheDecisionsItMade` | Each of the choices a download makes is traced at debug level, which is what answers "why did it install that" after the fact. |
| `TestInstallRefusesABadCommandLineBeforeAnyRequest` | Every usage mistake is decided by the flags alone and costs the fake no question at all, a URL naming only a host among them, which used to read as the owner and send the request somewhere nobody asked for, and a flag belonging to another command. |
| `TestInstallNamesAFlagThatIsNotIts` | `--tag 1.2.0` is the mistake anyone pinning a version makes, and used to read as commit's boolean `--tag` with `1.2.0` becoming a second repository. The refusal names the flag, says whose it is, points at `--release`, and arrives before a single request. |
| `TestInstallManifestIsAShellScript` | A list of pinned installs is a shell script, run by `sh` with the binary on `PATH`: two `--release` lines under `set -e` install two tools that run and report their versions, running the same file again downloads nothing, and a line carrying a foreign flag exits 2 so the manifest stops there instead of installing something else. |
| `TestInstallCommandWordKeepsItsScript` | Every command word permanently shadows a run script of the same name, and `install` is a name a repository might well have given one. The two-word spelling still reaches it. |

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
| `TestComputeEditsTheEntryTheAuthorSpelled` | A `packages` entry spelled with capitals is listed and edited at the key the file holds, so the write lands in the entry that is there rather than adding a folded twin beside it, and the edited config still loads. |

Unit tests in `services/dispat/internal/app` cover the finer-grained rules, testing each case in memory rather than
invoking the full binary. These include cross-ecosystem matching, interactive selection, TOML snippet fallbacks,
stale-endpoint removals, manifest-rank and version-shape rules, and error paths.
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
| `TestManifestsScannerNeedsNoConfig`             | The scanner runs without a config file, commit, or plan. The listing includes each manifest's identity, ecosystem, and declarations, including local paths. A standard walk descends while `--root-only` stays at the root, installed dependencies are never scanned, positional folders resolve against `--root`, and a missing folder exits 1. |
| `TestManifestsScannerJSONEvents`                | Pass `--log-format json` to get machine-readable output. The command emits one event per manifest with path, ecosystem, identity, and explicit declaration kinds, followed by summary counts.                                       |
| `TestManifestsScannerStrictGatesBrokenManifests` | dispat reports partial results when a manifest fails. Healthy manifests are listed and the command exits 0, but passing `--strict` fails the run with exit 1 while still printing partial results.          |
| `TestManifestsWriterEditsInPlace`                | A two-ecosystem batch rewrites only the changed version text, preserving every other byte in `package.json` (own version plus two fields) and `go.mod` (a require line without an own version). Re-running identical edits converges to `manifest unchanged`. |
| `TestManifestsWriterRedirects`                   | Pass `--link` to add a local folder directive, or pass an empty path to remove it. The scanner reads back what the writer wrote.                                                                                                 |
| `TestManifestsWriterOutcomesReachTheExitCode`    | Missing files exit 0 unless you pass `--strict`, which exits 1. A path with no matching writer exits 1 while usable manifests in the same batch are written. Malformed `--set` flags, invocations with nothing to write, or missing manifests exit 2 as usage errors. |
| `TestManifestsCommandWordsKeepTheirScripts`      | Command words are reserved. Running bare `dispat scanner` executes the built-in command even if your config defines a `scanner` script. Run `dispat run scanner` to reach the script.                                                       |
| `TestManifestsReplacerNeedsNoConfig`             | The replacer modifies non-manifest files without a config file or git history. It replaces every occurrence, resolves paths against `--root`, and applies repeated `--replace` flags in order. |
| `TestManifestsReplacerOutcomesReachTheExitCode`  | Passing nothing to write, a spec without a separator, or no target file exits 2 as a usage error. Patterns matching nothing exit 0, or 1 under `--strict`. Unreadable paths exit 1 while usable files in the batch are still written. |
| `TestManifestsReplacerJSONEvents`                | Pass `--log-format json` to get one event per file with its path and occurrence count. The summary event splits counts into applied, missing, and skipped.                                                                                            |
| `TestManifestsReplacerWordKeepsItsScript`        | The `replacer` keyword is reserved like other command names. Run `dispat run replacer` to execute a custom script with that name.                                                                                                                   |
| `TestManifestsScannerDebugEventsAndDroppedEntries` | Pass `--log-level debug` to trace the scan in the log stream. Declared but unreadable entries appear in the manifest event's `dropped` array, while default logging omits the trace.                                                |
| `TestManifestsScannerVerifyGates`                | Link verification uses two dedicated flags. A clean tree passes `--verify-unlinked` and fails `--verify-linked` with E216. A block-form `go.mod` replace fails `--verify-unlinked` with E215 and names the manifest, dependency, and path. A `file:` range is treated as a declaration and never trips the gate, while passing both flags exits 2. |
| `TestManifestsWriterDropLinks`                   | Pass `--drop-links` to strip all link directives from a mixed `go.mod`, Cargo, and pubspec batch without naming individual packages. The tree then passes `--verify-unlinked`. A second sweep has nothing to do and exits 0, and combining `--drop-links` with `--link` exits 2. |
| `TestManifestsWriterLinkDropVerifyCycle`         | Run the full cycle through commands alone: apply links, confirm with `--verify-linked`, drop links, and confirm removal with `--verify-unlinked`.                                                                                              |
| `TestManifestsScannerRangeGates`                 | Range gates operate independently from link gates and each other. Passing `--forbid-range 'workspace:*'` fails on matching declarations with E217 until rewritten. Passing `--require-range` inverts the check (E218). A linked tree with clean ranges passes range gates but fails link gates, and specifying the same pattern in both flags exits 2. |
| `TestManifestsWriterSetBuild`                    | Pass `--set-build` to update mobile counters (`CFBundleVersion`, `android:versionCode`, Gradle `versionCode`, or the pubspec `+` suffix) without modifying version strings. Events report `buildWritten` without `versionWritten`, and the scanner reads the counter back. Non-integer values on Android exit 1. |

### Goal 26: the `autowriter` command (`autowriter_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoWriterEditsEveryCoveredPackageThroughTheBinary`       | One invocation reaches every package the window covers, writing the fixture back with only the edited bytes changed. The `{version}` placeholder resolves against the newly computed plan for both ranges and own-version fields. A second pass leaves the working tree untouched. |
| `TestAutoWriterSelectsLikeEveryOtherCommand`   | Package selection matches standard command conventions. Use `--package`, run from a subfolder, or pass `--consumers` to select a package that declares no dependencies. Unmatched selectors exit 1.                                          |
| `TestAutoWriterOnlyUpdatedFollowsThePlan`      | Pass `--only-updated` to keep edits for releasing packages and drop edits for unreleased workspace packages. If nothing is releasing, the command writes nothing, logs the result, and exits 0.                            |
| `TestAutoWriterManifestScope`                  | Passing `--manifests root` limits edits to package root directories, while `all` updates nested manifests. Own-version edits stay on root manifests under both settings.                                                                        |
| `TestAutoWriterRedirectsThroughTheBinary`                      | Pass `--link` to inject local folder directives across selected packages. Pass an empty path to restore declared ranges.                                                                                                                       |
| `TestAutoWriterOutcomesReachTheExitCode`       | The `--strict` flag applies across the entire sweep. An edit matching at least one package passes, but an edit matching nothing exits 1, whereas without `--strict` it exits 0. Missing write targets, malformed specs, unknown `--manifests` values, and positional arguments exit 2. Unresolvable `{version}` tokens exit 1 without writing. |
| `TestAutoWriterJSONEvents`                     | Machine logs emit one event per manifest containing its path and package name, followed by summary counts for applied, skipped, and missing edits.                                                                                            |
| `TestAutoWriterSyncLock`                       | Scripts configured in `syncLock` run only when a manifest in that package changes. They skip unchanged packages, never run on converged runs, and do not execute when you pass `--sync-lock=false`.                                                 |
| `TestAutoWriterSetLocalDerivesEveryWorkspaceRange` | Pass `--set-local` to write planned provider versions into workspace dependency declarations formatted according to `--range`. Third-party ranges remain untouched.                                            |
| `TestAutoWriterSetLocalConverges`              | Running a second `--set-local` pass computes identical ranges and applies no changes. Because nothing changed, `syncLock` scripts do not re-run.                                                                       |
| `TestAutoWriterSetLocalYieldsToTheCommandLine` | Explicit `--set` flags take precedence over derived edits for the named dependency.                                                                                                                                    |
| `TestAutoWriterSetLocalSpellsEachEcosystemItsOwnWay` | A single `--range` keyword formats output per ecosystem through the shared renderer. Go modules retain the canonical `v` prefix, while Docker tags remain bare labels without carets.                                                       |
| `TestAutoWriterSetLocalTemplateRangeIsVerbatim` | Template strings in `--range` pass through verbatim. If a template generates an invalid Docker tag across a mixed workspace, dispat rejects the edit instead of writing invalid manifests.                                             |
| `TestAutoWriterLinkLocalRoundTrips`            | Pass `--link-local` to inject relative path redirects from each manifest. Running `--unlink-local` restores original files byte for byte, confirming derived paths and removals match.                             |
| `TestAutoWriterLinkLocalResolvesFromTheManifestFolder` | Nested manifests compute relative link paths from their own directory rather than the package root.                                                                                                       |
| `TestAutoWriterLinkLocalSkipsNpm`              | Derived links skip `package.json` and log an explanation, because npm overrides require exact spec matches on declared dependencies.                                                                               |
| `TestAutoWriterLinkLocalWarnsAboutPublishing`  | Running `--link-local` warns you that release commands do not strip local links. Releasing with active local links publishes unresolvable manifests to consumers.                                                                                              |
| `TestAutoWriterSetLocalAndLinkLocalInOnePass`  | Both flags derive from the same declaration scan, so passing both applies range updates and path redirects in one pass.                                                                                                         |
| `TestAutoWriterLinkLocalLeavesTheComputedGraphAlone` | Injecting local links introduces no config changes beyond existing compute graph edges. Local checkouts do not alter dependency graphs.                                                                                                     |
| `TestAutoWriterLocalFlagsReachTheExitCode`     | Combining `--link-local` with `--unlink-local` exits 2. Providing a bare local flag completes the request, and derived edits never trip stale checks under `--strict`.                                                                     |
| `TestAutoWriterLinkLocalReachesAnIndirectRequire` | Go builds only apply replace directives found in the main module. dispat inspects indirect requires so that providers reached through intermediate modules are redirected in the consumer's `go.mod`. |
| `TestAutoWriterSetLocalLeavesAnIndirectRequireAlone` | Range updates only modify direct declarations. Indirect requires are managed by toolchains, so dispat leaves them untouched. |

### Goal 27: the `autoreplacer` command (`autoreplacer_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAutoReplacerFansOutAcrossWorkspaceProviders` | A `{provider}` pattern renders once for each workspace package declared by the target package. Coordinates update alongside providers without naming dependencies on the command line.                                                     |
| `TestAutoReplacerPackageScopedPatternRunsOnce` | A `--replace` pattern without `{provider}` targets the covered package itself and inserts its planned version.                                                                                                          |
| `TestAutoReplacerGlobsSelectWithinThePackage` | File globs match only inside the package directory passed to the sweep. Unmatched files remain untouched.                                                                                                             |
| `TestAutoReplacerOnlyUpdatedNarrowsTheFanOut` | Pass `--only-updated` to exclude providers not being released in the current run, leaving their coordinates unchanged.                                                                                                       |
| `TestAutoReplacerConsumersReachesThePackagesTheWindowLeftOut` | Packages with stale coordinates may have no code changes, which excludes them from the default window. Pass `--consumers` to pull them into the sweep.                                                                |
| `TestAutoReplacerConvergesUnderStrict`        | The replacer distinguishes previously reconciled files from files that never matched. Re-running a converged `{previous}=>{version}` pattern passes cleanly under `--strict`.                                                                               |
| `TestAutoReplacerLeavesANestedPackageToItsOwner` | A parent package skips files owned by nested child packages. Each child package updates its own files, avoiding concurrent file writes across goroutines.                                                                                |
| `TestAutoReplacerOutcomesReachTheExitCode`    | The `--strict` flag applies across the entire sweep. Missing `--replace` flags, missing `--files` flags, malformed specs, and positional arguments exit 2.                                                                        |

### Goal 28: Docker through the binary (`docker_test.go`)

| Test                                            | Claim proven                                                                                                                                                                                                                          |
|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestDockerComputeDerivesTheImageChain`         | The `compute` command reads image dependencies directly from `FROM` lines without config declarations. It identifies the source file, skips external bases, writes edges with `--write`, and satisfies `--check`. Setting `manifestNames` allows image references to resolve correctly. |
| `TestDockerReleaseReconcilesTagsAndCompose`     | Releases update both Docker formats during the version stage. Consumer `FROM` tags and `COPY --from` images update to match provider versions, build-stage copies remain untouched, and compose files update service versions and `build.tags` entries without altering port mappings. |
| `TestDockerManifestCommands`                    | Config-free commands support both Docker formats. The `scanner` reports compose identities and Dockerfile base images without a config, commit, or plan. The `writer` updates compose tags and package versions in place, digest-pinned bases are skipped, and missing edits trigger failures under `--strict`. |

### Goal 29: the release guards (`guard_test.go`)

| Test                                       | Claim proven                                                                                                                                                                                                                                                     |
|--------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestGuardAllowBranch`                     | Setting `run.allowBranch` restricts releases to listed branch patterns. Running on an unlisted branch prints the branch and configured globs, exits 1, and creates no tags. Wildcard `*` patterns match branch names with slashes like `release/v1`, while `dispat status` runs on any branch.             |
| `TestGuardAllowBranchRefusesDetachedHead`  | A detached HEAD matches no branch patterns, including `*`. dispat aborts the run before creating tags.                                                                                                                     |
| `TestGuardBehindRemote`                    | In push mode, dispat checks whether local branches lag behind the remote. If behind, the run aborts with "behind origin/main" *before the plan is computed at all*: no build script runs and no planning diagnostic is reported, while the same window does report W131 once the checkout has caught up and a plan is actually built. Running `git pull --rebase` allows the release to tag and push successfully. |
| `TestGuardBehindRemoteHonoursCommitVerify` | The behind check runs `git ls-remote`. Setting `commit.verify: false` disables this check alongside reachability checks, so the run builds, publishes and tags against a plan computed from tags it could no longer see, and the push it is then rejected on is recovered by the merge, reported as `W242`. What the guard protects is the plan, not the push. |
| `TestGuardsAreUnsetByDefault`              | Guards remain inactive until configured. Unconfigured repositories on any branch name release and push to remotes without restrictions.                                                                                                                  |
| `TestReleaseMergesWhatLandedDuringTheRun` | A commit pushed from a second clone while the build runs reaches the finalize push as a rejection, after the packages have published. dispat pulls it and merges it with its release commit, rewriting nothing, then pushes the merge: exit `0` and `W242`. The branch tip is a merge whose first parent is the release commit, the tag still names that commit, and what arrived is outside its ancestry and absent from this run's changelog. The run after it releases that commit normally, while the release commit and the merge resolve to no package and raise no `W131`. |
| `TestReleaseCannotMergeWhatConflicts` | What landed touches the same file the release commit writes, so the merge conflicts. The run exits non-zero naming that commits landed during the release and could not be merged, no tag reaches the remote, the merge is undone rather than left in the working tree, and the release lock is given back. |

### Goal 30: the release lock (`lock_test.go`)

The lock is a ref on the remote, so every claim here is read from the bare repository the fixtures push to. What was
true *during* a run is read from a `beforeAll` hook, which runs while the lock is held. The harness disables the lock
for every other scenario (`DISPAT_UNSAFE_DISABLE_LOCK=true` in `runBin`), since most fixtures have no remote at all;
these tests ask for it back.

The claim is taken before anything is planned, on every release and whatever flags it was given, because whether
there is work to do is not known until after planning. A run whose plan turns out to be empty therefore round-trips
the lock; the hook probe cannot witness that one, since `beforeAll` never runs on an empty plan, so it is read from
the tag object on the remote instead.

| Test                                       | Claim proven                                                                                                                                                                                                                              |
|--------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestReleaseLockRoundTrip`                 | The lock ref remains on the remote during execution and is deleted when the run finishes. A second run with no pending releases acquires and releases the lock identically.                                                     |
| `TestReleaseLockHeldElsewhere`             | An existing lock on the remote causes the command to exit 1 with remediation instructions without building or tagging packages. The existing lock ref remains untouched, and the repository releases cleanly once the lock is freed.  |
| `TestReleaseLockIgnoresCommitForce`        | Setting `commit.force` overwrites run records but never overrides remote locks. A repository configured with force settings still stops at a held lock.                                                           |
| `TestReleaseLockBlocksConcurrentRuns`      | When two release processes target one remote, the first acquires the lock inside a hook while the second is rejected until the lock clears. The second release starts only after the lock ref exists on the remote. |
| `TestReleaseLockIndependentOfPush`         | dispat acquires and clears the lock even when release commits are disabled, leaving the remote with no tags or branches. The lock operates independently of the release push.                                                    |
| `TestReleaseLockWithoutRemote`             | When lock protection is active, repositories without configured remotes cannot coordinate and abort the release. The cost of the guard, stated.                                                                                                                       |
| `TestReleaseLockKillSwitch`                | Pass `true`, `1`, or `TRUE` via environment variables to bypass lock checks on repositories without remotes. Values like `false`, `0`, empty strings, or invalid inputs keep the lock enforced.                                                        |
| `TestReleaseLockConfigSwitch`              | Setting `unsafeDisableLock: true` in config permits releases without remotes even when environment variables set it to `false`. Reverting the config key to `false` restores the remote requirement.                          |
| `TestReleaseLockClearedWhateverHappens`    | dispat releases the lock on all exit paths, including failed packages, guard failures, and interrupt signals (SIGINT or SIGTERM). Tests verify the lock was *held* when signals arrived and removed afterward.                                                                                                      |
| `TestReleaseLockStaleLocalTag`             | Stale lock tags left in local clones from terminated runs are overwritten locally on the next run, allowing the release to continue.                                                                                              |
| `TestReleaseLockCleanupFailureIsNotFatal`  | If a remote becomes unreachable at the end of a run, dispat prints remediation steps and returns the exit code of the release (0), leaving the lock tag on the remote.                                                       |
| `TestReleaseLockAppliesOnlyToRelease`      | Commands like `status`, `preview`, `run`, `changelog`, `autoversion`, `commit`, and `scanner` acquire no remote locks and function in repositories without remotes.                                                                                  |
| `TestReleaseLockIsNotAReleaseTag`          | The lock tag targets HEAD during plan computation. Broad tag formats like `{version}` still read 0.1.0 as the baseline and increment to 0.2.0.                                                                                |
| `TestReleaseLockTakenEvenWhenNothingToRelease` | The lock is unconditional. A `--require-release` run with nothing to publish takes the lock, gives it straight back and exits 3, building and tagging nothing. A lock already held refuses such a run on the lock rather than on the empty plan, which is what proves the lock precedes planning; `dispat status --require-release` remains the lock-free way to ask the same question. |

### Goal 31: corrections and reverted changelogs (`corrections_test.go`)

Every scenario names its target the way an operator does, with `git rev-parse HEAD` after the commit it means, so the
footers under test carry real shas rather than fixtures. Most run in a two-package repository with no edge between the
packages, which is what makes "the correction reached exactly this far" assertable: the second package is the control.

| Test                                                    | Claim proven                                                                                                                                                                                                                                       |
|---------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestCorrectionEditRestatesTheRecordBeforeRelease`      | A commit misclassified as a breaking change can be restated as a fix before release. The package bumps a patch version instead of a major, changelogs document the restated fix, and subsequent runs converge. |
| `TestCorrectionAfterReleaseIsAVisibleNoop`              | Modifying an already released commit has no effect on history. dispat emits W209 for the package while the current release proceeds. Passing `--quiet-parser` cannot suppress W209. |
| `TestCorrectionPrecedenceAndVoiding`                    | Multiple corrections on one target resolve newest first, emitting W210 on superseded entries. Deleting a correction voids it (W215) and restores the original record. |
| `TestCorrectionScopeIsContainedNotCombined`             | A package-scoped correction restates records scoped to `(*)` for that package while preserving records for other packages. Pointing a correction at a package the target never modified triggers E213 and leaves the target untouched. |
| `TestCorrectionWildcardClearsAScope`                    | Adding `Deletes: *` to a `chore` discards pending records for that scope without releasing replacement versions. Packages outside the scope release normally.                                                                       |
| `TestCorrectionTargetsMustResolve`                      | Unit errors exit with specific codes: E210 for SHAs not in ancestor history, E211 for bare SHAs matching multiple units, and E212 for corrections targeting `cancel` barriers.                                        |
| `TestCorrectionDiscardsWhatTheRecordPropagated`         | Deleting a record removes its propagated bumps. Dependent packages that would have bumped due to carets skip releasing if no changes remain.                                                                                          |
| `TestCorrectionRidesAVersioningGroupOnlyWhenARecordSurvives` | Discarding the only record driving a versioning group halts the entire group, preventing members from riding (W234) an unprompted version bump.                                                                              |
| `TestRevertTakesBothEntriesOutOfTheChangelog`           | Reverting a commit preserves major bump calculations in case consumers observed the change, but strips both entries from the changelog (W212) because the code is unchanged. The run converges. |
| `TestRevertWithAnUnreachableTargetStaysInformational`   | Reverts with unreachable SHAs emit W213 and release normally. Values that are not valid SHAs emit parser warning W214 without additional error codes.                        |
| `TestRevertSuppressionIsVoidedByACorrectionThroughTheBinary`            | Deleting a revert record un-suppresses the original changelog entry and eliminates W212 warnings. The §7.4 voiding rule applied to §7.3.                                                                              |
| `TestCorrectionEditOfPublishedTrainWorkIsANoOp`         | In release trains, any prerelease counts as published history. Editing a unit shipped in `beta.0` acts as a no-op (W209), fixes bump to the next train step, and existing records remain unchanged.  |
| `TestCorrectionDeleteStopsATrainAdvance`                | Deleting the only new record driving a versioning group stops train advancement. No packages release, no W234 warnings appear, and prerelease counters remain in place.                                   |
| `TestRevertPairOnATrainRendersCancelLine`               | Reverting a feature within the same train step removes both from changelog notes (W212) while counting toward targets (§7.3). Prerelease notes state the work cancelled out rather than leaving an empty body.                       |

### Goal 32: references naming several files (`multiref_test.go`)

`$ref` is a shape the typed model deliberately cannot express, so these configs go through `WriteConfigRaw` on top of
`rawSplitConfig()`, the raw spelling of `harness.BaseFile` plus the canonical one-space flow, exactly as the
single-file reference scenarios in goal 10 do.

| Test                                        | Claim proven                                                                                                                                                                                                       |
|---------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestMultiRefMergesObjectFragments`         | Shared fragments merge with overriding files into a single object. Keys in later files take precedence, unique keys from base files survive, merged scripts execute, and all source files appear in traces. |
| `TestMultiRefConcatenatesLineFragments`     | Common record lines concatenate with repository-specific lines across multiple formats in the order specified. Merged lines preserve their original package filters. |
| `TestMultiRefSiblingOverrideAndEnvCase`     | Sibling keys defined alongside a `$ref` take precedence over referenced files. Environment variables merged across fragments preserve exact casing when passed to scripts. |
| `TestMultiRefRefusals`                      | Providing empty file lists, invalid file names, mismatched object types, or missing files triggers configuration errors before tagging, committing, or running scripts.                                |
| `TestMultiRefCycleNamesThePath`             | Circular references are detected when following files individually. dispat aborts the run, names the file that completed the cycle, and releases nothing.                                                      |
| `TestMultiRefComputeWriteRefuses`           | Running `compute --write` fails if merged keys span multiple files, because no single file owns the result. dispat reports options and leaves files and backups unchanged. A list referencing a single file writes through directly. |

### Goal 33: the channels a record reaches (`channels_test.go`)

Two packages are compared inside one run wherever they can be, because "this one records and that one does not" is the
claim, and a single run makes it without depending on anything between runs.

| Test                                     | Claim proven                                                                                                                                                                                                        |
|------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestChannelsNamedChannelGate`           | Policies targeting specific prerelease channels record only in those channels, while generic policies cover all prereleases. Skipped packages log the restricting policy and current channel. |
| `TestChannelsFilterRecordLines`          | Individual lines specify target channels, allowing footers to render differently across beta and stable releases while shared sections remain unchanged. GitHub release bodies match changelog entries generated from the same format. |
| `TestChannelsCombineWithPackageFilters`  | Channel filters combine with package filters, so lines render only when both conditions match.                                                                                                     |
| `TestChannelsKeepReleasersApart`         | Packages with distinct GitHub channel policies use separate releasers, preventing entry formats from leaking across packages.                                        |
| `TestChannelsValidationRefusals`         | Empty channel restrictions and channel-varying file titles are rejected during validation before execution starts. Nothing runs.       |
| `TestChannelsPreviewShowsBothBodies`     | Running `dispat preview --changelog --github` prints changelog and GitHub release bodies under clear headers. Suppressed channels display explanatory messages instead of blank bodies. |
| `TestChannelsAreReportedInTheSkipEvent`  | Channel skips emit info-level events with package, tag, and channel details. This lets automation distinguish configuration skips from failures while releasing other packages.       |

### Goal 34: versioning `none` (`versioning_none_test.go`)

| Test                                        | Claim proven                                                                                                                                                                                                     |
|---------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersioningNoneLifecycle`               | Modifying releasable and `none` spaces together releases only the releasable package. The `none` package creates no tags or changelogs, displays script-only graph lines, and avoids catch-up updates on subsequent runs. |
| `TestVersioningNoneRunDefaultWindow`        | The default `dispat run` window includes the planned release packages plus changed `none` packages. Unchanged released packages are excluded, and `none` packages run scripts even when no releases occur. |
| `TestVersioningNoneProviderEdgeRejected`    | Releasable packages cannot depend on packages versioned as `none`. Config validation fails and names the offending declaration, whereas `none` consumers and `none`-to-`none` edges load normally. |
| `TestVersioningNoneConsumerWithLocalLink`   | Running `--link-local` on selections containing only `none` packages creates local links without publishing warnings. Linked providers continue releasing, while `{version}` tokens targeting `none` packages trigger errors. |
| `TestVersioningNonePackageSelection`        | Selecting a `none` package explicitly behaves according to the command: `release --package` reports the package does not release and exits 0, while `run --package` executes configured scripts.                             |
| `TestVersioningNoneReleaseAsInert`          | Adding `Release-As` footers targeting `none` packages produces warning W238 and performs no version bump.                                                                                       |
| `TestVersioningNoneReleaseOnlySettingsInert` | Release settings like `tagFormat` and publish stages are ignored on `none` spaces without raising config errors. Build scripts continue to execute under `dispat run`.                                                 |

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

Everything a declared `versionGroups` group does across two spaces lives here. This suite covers full and partial
lifecycles, sparse modes, group-membership edges on the override ladder, shared prerelease trains with pins, polyglot
script-only members, and per-member tag spellings.

| Test                                        | Claim proven                                                                                                                                                                                                        |
|---------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestVersionGroupSparseAcrossSpaces`        | A declared `fixedSparse` group never back-fills: an untouched member in the other space does not ride (no W234), and when it finally changes it joins at the group's next version, skipping the ones it sat out.     |
| `TestVersionGroupPartialSparseAcrossSpaces` | Under `fixedMajorMinorSparse` a patch stays inside its member, a minor moves the shared part without dragging the other space along, and the laggard joins at the shared part when it next changes.                  |
| `TestVersionGroupMemberOverrideLeavesTheGroup` | Versioning and versionGroup are one ladder axis: a package-level `versioning` on a declared group's member supersedes the membership its space joined, so the package versions on its own line instead of riding. |
| `TestVersionGroupPrereleaseTrain`           | A prerelease train runs across the whole declared group with one shared counter; graduation lands every member on the same stable version; divergent member channels while the group moves are W236.                 |
| `TestVersionGroupRefusesNone`               | A declared group with versioning `none` is refused through the binary with the loader's own message, not just in config unit tests.                                                                                  |
| `TestGroupFilterPartialMode`                | `--group` under `fixedMajor` selects the whole group, and the partial mode keeps meaning what it means inside the selection: a breaking change moves every member (W234 on the rider), a minor releases its member alone with no ride to explain, and the aligned group converges. |
| `TestVersionGroupDivergentTagFormats`       | A group shares the version, not its spelling: each member renders the shared version through its own `tagFormat`, with no diagnostic beyond the ride's own W234. On the stable line and along a whole train, each member reads its baselines back out of its own spelling. |
| `TestVersionGroupExactPinMidTrain`          | An exact pin naming one member mid-train moves the whole group onto the pinned version. The naive graduation afterwards is E185 (the pin lives in the baseline tag, not the window) and nothing releases; pinning the graduation lands every member where the pin put the train, and the group converges. |
| `TestVersionGroupSparseMemberPin`           | Under a sparse mode a pin moves the shared version without back-filling: the untouched member stays put, and its next change joins above the pin, skipping everything it sat out.                                    |
| `TestVersionGroupNoneMemberIsScriptOnly`    | The polyglot shape: a `versioning: none` override on a package inside a group-joined space makes it script-only: it is never tagged, the group moves without it, the graph names it, and its work still runs through the run window. |
| `TestVersionGroupMixedDepthTrain`           | Mixed shared depth (a member's `fixedMajorMinor` inside a `fixedMajor` space, the implicit-group shape) resolves to the deepest declaration with W237, and the resolution holds along a whole train: one shared counter, one graduation. |
| `TestVersionGroupPartialReleaseCatchesUp`   | A run that dies between two members' publishes leaves the group split; the retry lands the laggard at the version that already carries the shared work, as its own release (no W234), without re-releasing the member that published, and a third run converges. |
| `TestVersionGroupPartialReleaseCatchUpAcrossModes` | The same partial-release catch-up in every shared mode — `fixed`, `fixedSparse`, `fixedMajorMinor`, `fixedMajorMinorSparse`, and the major-only modes under a breaking change — with the holder never re-released and the group converged after one retry. |
| `TestVersionGroupCauselessLaggardRidesToThePublishedVersion` | The other catch-up flavour: the failed leg was a ride, so the retry rides the cause-less laggard up to the group's published version with W234 explaining it — and its entry documents the movement it rode for, spanned from the laggard's last release. |
| `TestVersionGroupPartialReleaseTwoLaggards` | One holder, two failed legs: both laggards catch up at the published version in a single retry.                                                                                                                     |
| `TestVersionGroupPartialReleaseNewerWorkMovesOn` | The mask reaches exactly as far as the published tag: work landing after the partial release moves the prefix, the laggard releases everything at the next minor without ever landing on the version it skipped past, and the erstwhile holder rides up (W234). |
| `TestVersionGroupTrainPartialReleaseAdvancesTheTrain` | On a prerelease train the stable-line masking stays out (§11.4 owns the window): the retry advances the train, the laggard boards at the next prerelease and the holder rides beside it.                       |

### Goal 37: step commands wired into a running release (`stepwiring_test.go`)

| Test                                | Claim proven                                                                                                                                                                                                          |
|-------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestStepsWiredIntoAPublishLeg`     | A publish leg whose script is `dispat changelog` + `dispat commit --tag --push` records itself mid-run: the tag reaches the remote before the gated consumer builds, the tagged tree carries the step-written changelog, finalize finds the work done (W223/W226), no W228/E219 fires, and a second run converges. A finalize-recorded package's tag lands on the release commit itself, one commit carrying both the tracked artifact its build wrote (the docs slice's shape) and the tag. |
| `TestStepsWiredSurviveAPartialRun`  | After a run that released only the provider, the next run finds the provider's step-made records, does not re-release it, and catches the consumer up at the version it was owed.                                        |
| `TestStepsCatchUpEntrySpansTheProvidersMovement` | The step commands recompute the plan, and the catch-up entry they write spans the provider's movement from the consumer's last release; the same span reaches scripts as `DISPAT_UPDATED_*_OLD_VERSION`.  |
| `TestStepsDuplicatedCollapseIntoSkips` | A flow listing the record steps twice produces one record set: the second pass is W226/W223 skips, one changelog entry, one tag.                                                                                     |
| `TestStepsWiredRecordTheRunsDependencies` | A wired record's dependencies section states the run's provider movements. The consumer's changelog step replans after the provider's tag landed and after their shared fixed group would read that tag as a floor; the masked replan reproduces the run (no W228), and the entry names the movement the run actually made, not a version it never released. |
| `TestStepsGithubBeforeCommitWarns`  | A github step ordered before the commit step is the W229 smell, said before anything is created; one release is created at the run's tag, and the correctly placed second github step finds it and skips (W224).       |

### Goal 38: the longitudinal fence (`longitudinal_test.go`)

This test models one repository on dispat's own shape (a declared version group spanning two spaces, one member
publishing through wired step commands, a caret provider outside the group, alias tags, a bare remote, and the GitHub
recorder). You release it through a complete prerelease-train lifecycle: stable baseline, `rc.0`, `rc.1`, `rc.2`,
graduation, and convergence. Every step asserts the durable records (tags, isolated changelog entries, and GitHub
bodies) alongside the reported plan (graph verdicts, reasons, `ownCommits`, and diagnostics). A single release cannot
distinguish train history from a changeset, so this multi-release sequence exercises the seam between train-wide
accounting and fresh-changeset reporting.

| Test                                  | Claim proven                                                                                                                                                                                                              |
|---------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestLongitudinalGroupTrainLifecycle` | The whole walk. Along the way: `ownCommits` counts the fresh changeset (through `status` itself, mid-train); a rider's entry is the ride line at every step it has no cause, and the dependencies section when a provider moved; `rc.1` does not repeat `rc.0`'s notes; the graduation's one entry collects the train's features, fixes *and* provider movement; the prerelease flag follows the channel on every GitHub release and no body is empty; the moving alias ignores the train and follows the graduation; the wired records never drift (no W228/E219) and nothing is ever a catch-up (no W193). |

### Goal 39: a record entry is never empty (`entrybodies_test.go`)

Three release shapes enter the plan without pending notes to group. Each must state its cause in the changelog entry
and the GitHub body, because an empty record looks like a broken write rather than an intentional release. Goal 14,
goal 36, and goal 38 test the fourth empty shape (the group ride); goal 31's `TestRevertPairOnATrainRendersCancelLine`
covers the train revert pair.

| Test                                    | Claim proven                                                                                                                             |
|-----------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------|
| `TestEntryBodyOfAPinOnlyRelease`        | An exact `Release-As` with no pending bump releases with "No changes: a version set by Release-As." in both records.                     |
| `TestEntryBodyOfAChannelOnlyRelease`    | A channel-only release (W202, entry-patch W204) names its transition: "No changes: a channel transition, stable -> rc."                  |
| `TestEntryBodyOfACancelledOutRelease`   | A feature and its revert releasing the owed bump (W212) render "No changes: the pending work and its reverts cancel out.", so it is never empty. |

### Goal 40: the e2e smoke walk (`smoke_test.go`)

This test executes the live pre-1.0.0 release verification protocol against a toy polyglot monorepo (a Go library and
CLI with real `go.mod` files, an npm package with a real `package.json`, a Docker image with a Dockerfile and compose
file, a `fixedMajorMinor` version group across the npm and docker spaces, and dependency edges in both ecosystems). The
walk steps through every standard workspace release shape. Each cycle asserts the full status graph (package verdicts,
version transitions, and reasons), tags, changelog entries, and exact manifest contents, proving convergence before the
next cycle starts.

| Test                     | Claim proven                                                                                                                                                                                                     |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestSmokeReleaseCycles` | Nine cycles in sequence: bootstrap (everything direct, every manifest written); a shared minor riding the image with its FROM and compose tags following; a provider releasing alone with the consumer's manifest deliberately left behind; the consumer's own next release performing the reconciliation pickup (W197, manifest moves, changelog deliberately silent); a caret propagating the provider's fix (manifest and dependencies section both move); a whole rc train over the group, with prerelease versions in the manifests, fresh-only rc entries, and a graduation whose entry documents the provider's movement over the whole train; a run dying between the group's publishes with the retry catching the failed leg up at the published version as its own release; a provider leg dying under `isBuildWaitingPublish` with the consumer skipped (W194) rather than shipping against the missing publish; and a dead ride with the retry riding the cause-less laggard up at the published version (W234), its entry spanning the movement it rode for, the manifest pickup intact throughout. |

### Goal 41: game engine manifests (`engines_test.go`)

Unity, Godot, Unreal, Defold, and O3DE store versions in custom manifest files that standard package managers do not
parse. This goal proves that dispat reads and writes all twelve engine formats like standard manifests using the same
commands, exit codes, and event streams without requiring a `replace` rule. The test fixture uses a single engine
monorepo containing a project for each engine alongside their generated folders.

| Test                                             | Claim proven                                                                                                                                                                                                    |
|--------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestEnginesScannerReadsEveryEngineFormat`       | One walk lists every engine manifest with its identity, version and ecosystem, and the folders the engines generate contribute nothing: Unity's `Library/PackageCache` holds a real `package.json` per resolved package, and a scan that entered it would report a few hundred third-party packages as members of the workspace. |
| `TestEnginesScannerReadsTheDependencyEdges`      | The graph an engine repository has: a Unity dependency declared by folder yields the local path that makes it a workspace edge, and an Unreal plugin declared by name with no version at all is still an edge the release order respects. |
| `TestEnginesPathQualifiedFormatsResolve`         | The four formats told apart by the folder they sit in, over a process boundary. `Packages/manifest.json` is Unity's and `public/manifest.json` is a web app manifest, and the writer refuses the second rather than guessing. |
| `TestEnginesWriterWritesEachVersion`             | One batch spanning all five ecosystems rewrites each format's own version field, leaves every build counter where it was, and converges: a second pass applies nothing and the files come back byte for byte.   |
| `TestEnginesWriterSetBuildWritesEveryCounter`    | `--set-build` moves the counter each engine keeps and only the counter, across every Unity platform and every Godot preset; an Unreal plugin's `Version` stays a bare integer; the scanner reads them back; and a version where an integer is required exits 1 for every one of them. |
| `TestEnginesUnrealVersionlessPluginsAreSkipped`  | The Missing/Skipped split over the process boundary: a plugin the descriptor lists is skipped and passes `--strict`, one it does not list is missing and fails it.                                              |
| `TestEnginesGracefulOnPartialProjects`           | The states a healthy engine repository is routinely in are not errors and write nothing: a Godot project that never set `config/version` does not gain one, and a build stamp against a manifest that declares no counter warns and leaves the file alone. |
| `TestEnginesAutoVersionWritesTheEngineVersion`   | The point of the feature. A release run computes a version from the commits and writes it into `project.godot` with no `replace` rule configured, and everything else in the file survives.                      |
| `TestEnginesEventsNameTheFormat`                 | Five ecosystems now cover twelve formats, so the machine contract carries the format beside the ecosystem: the project file and the export presets are told apart.                                              |
| `TestEnginesUnityRangesArePinned`                | Unity's package manager resolves an exact version and nothing else, so a range write pins rather than writing a caret the project could not open, and a folder range beside it is untouched.                     |
| `TestEnginesScannerStrictGatesBrokenEngineManifests` | The partial-result contract reaching the exit code for the engine formats too: a broken `Packages/manifest.json` is reported while the healthy manifests are still listed, and `--strict` refuses the same repository. |
| `TestEnginesComputeReadsTheEngineGraph`          | `dispat compute` derives its edges from the same manifests, so a versionless Unreal plugin edge is one it suggests.                                                                                             |

### Goal 42: external webhooks (`webhooks_test.go`)

The `webhooks` config declares HTTP endpoints a release run notifies of its progress: the run brackets
(`release.started` / `release.finished`), per-package stage transitions and per-package outcomes. The contract under
test is isolation — deliveries are asynchronous, and no endpoint behaviour may change what the release does or what
the command exits with — plus the wire details only a real HTTP server can witness.

| Test                                                | Claim proven                                                                                                                                                                                                 |
|-----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestWebhookHappyPathSequence`                      | The whole stream of a two-package release, in order: `release.started` opens with the plan snapshot, each package's stages bracket exactly, `package.published` carries the tag and versions with no unexpected field, and `release.finished` closes with the counts. |
| `TestWebhookEventFilters`                           | Subscriptions are per webhook: an events list admits what it names, `package.*` admits the family, and two webhooks sharing one event each receive their own delivery.                                        |
| `TestWebhookFailingEndpointNeverAffectsTheRelease`  | An endpoint answering 500 to everything costs the run nothing: the release publishes, tags and exits 0, and every failed delivery warns as W239 naming the endpoint url.                                      |
| `TestWebhookUnreachableEndpointNeverAffectsTheRelease` | The same isolation with no server at all: connection refused is only a W239.                                                                                                                              |
| `TestWebhookSlowEndpointIsBounded`                  | A hanging endpoint is bounded by the webhook's own timeout and the end-of-run flush deadline; the command still finishes promptly.                                                                            |
| `TestWebhookSignature`                              | With `secretEnv` set, every delivery's `X-Dispat-Signature` verifies as the standard `sha256=` HMAC over the exact captured body bytes.                                                                       |
| `TestWebhookHeadersAndMethod`                       | The configured method and headers reach the wire, with a `$VAR` header value expanded from the process environment.                                                                                           |
| `TestWebhookConfigRejections`                       | A broken declaration — unknown event, missing url, bad method — stops the load naming `webhooks[0]`, and nothing is released.                                                                                 |
| `TestWebhookInterruptedRunStillReportsTheOutcome`   | The flush is detached from cancellation: a SIGINT mid-build still delivers the `package.cancelled` events and a `release.finished` saying `interrupted` before the process exits.                             |
| `TestWebhookFailedRunKeepsItsExitCode`              | The exit-code fence: a run with a failing package exits with exactly the code its webhook-less twin exits with, while `release.finished` reports the failure honestly.                                        |
| `TestWebhookRefusedRunEmitsNothing`                 | A run refused before execution (the branch guard) makes no delivery at all: webhooks begin only once the run is committed to execute.                                                                         |
| `TestWebhookPackageOverrideRouting`                 | The ladder's replace-wholesale rule on the wire: a package stating its own list routes its events to its endpoint alone, the root endpoint keeps the run brackets and the other packages.                     |
| `TestWebhookEmptyListOptsOutOnTheWire`              | `webhooks: []` at a package silences that package while the root endpoint keeps the brackets and the sibling — raw config, because the typed model's omitempty cannot write an empty list.                    |
| `TestWebhookCustomFormat`                           | A `format` template replaces the payload byte for byte, tokens rendered from the event, and the `X-Dispat-Event` header still names what a formatted body may not.                                            |
| `TestWebhookEnvGate`                                | `env: DISPAT_IT_CI=true` keeps the webhook silent until the condition holds: the same trigger delivers nothing without the variable and everything with it.                                                   |
| `TestWebhookTriggerCustomEvent`                     | `dispat trigger <word>` raises `script.<word>`: subscribable by exact name, attributed to the raising package and stage, and an unsubscribed word arrives nowhere.                                            |
| `TestWebhookScriptProgressTrigger`                  | `dispat trigger progress` raised from a stage script lands its `script.progress` deliveries between the stage's own bracket events, attributed to the raising package, stage and version, with the value (including a genuine 0) and the message intact. |
| `TestWebhookTriggerOutsideARunIsHarmless`           | The trigger command by hand: it delivers without the package fields, exits 0, and a dead endpoint is a W239 warning rather than an exit code — a script cannot fail its stage by reporting progress.          |

### Goal 43: the key-features smoke walk (`smoke_features_test.go`)

Goal 40's companion in breadth: where the release-cycle walk proves the release protocol in depth, this walk takes one
toy workspace through the commands CI pipelines and day-to-day use lean on, asserting each command's observable
artefact and exit code over the process boundary. The assertions are deliberately happy-path — the per-feature suites
own the deep cases — because both smoke walks are what the release build runs against the exact binaries it is about
to export: `services/dispat/Dockerfile`'s test stage selects `-run 'TestSmoke'` with `DISPAT_TEST_BINARY` pointing the
harness at the freshly cross-compiled binary, so the gate answers "do the shipped bytes work" rather than re-proving
the feature matrix.

| Test                     | Claim proven                                                                                                                                                                                                     |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestSmokeKeyFeatures`   | One walk over the key commands: `init` creates a loadable starter and refuses to overwrite it; `status` reports the pending graph and `--require-release` answers 0 pending / 3 converged (the contract the release workflow's plan job gates on); `preview` names the pending tag; `compute` detects the edge the manifests express, fails `--check` while the config lags, and `--write` adopts it; the `if --changed` gate answers from the release window and an explicit one; `run` sweeps a window with `--consumers`; `exec` runs the named root script; the release itself tags, writes changelogs and rewrites manifests, and a caretless fix does not propagate; the post-release `changelog` step command is a logged no-op; and the scanner lists the workspace's manifests. |

### Goal 44: authors in release records (`authors_test.go`)

The attribution the entry-format `authors` object adds to a changelog entry and a GitHub release body. Every fixture
commits under named identities through `harness.Repo.CommitAs`, because nothing about attribution can be observed
until two commits are by two different people; the repository's own fixed identity stays the default everywhere else.

| Test                                       | Claim proven                                                                                                                                                                                                                              |
|--------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TestAuthorsOffByDefault`                  | The feature is invisible until asked for. A repository configuring nothing records exactly what it recorded before, and an explicit `placement: off` produces a byte-identical file — which is what lets a package defeat a broader layer without a fourth spelling of absence. |
| `TestAuthorsInlinePlacement`               | The per-line suffix names that line's own people, the git author first and the `Co-authored-by` trailers after, and `inline` alone writes no section.                                                                                       |
| `TestAuthorsSectionPlacement`              | The section is one deduplicated list under its configured title, written into the changelog file and into the GitHub body after the sections it attributes and before the footer, so self-update's `---` cut is unmoved. A non-ASCII name survives the whole path from git to the record. |
| `TestAuthorsAllCommitsIncludeInvalid`      | `commits: ccme` names the people behind the entry's own lines; `commits: all` also credits the author of a commit whose message is not a release record at all.                                                                             |
| `TestAuthorsOnlyInvalidCommitsStillCredits`| A package releasing on an exact pin has no grouped line to attribute, and under `all` still says who moved the repository.                                                                                                                  |
| `TestAuthorsFiltersAndFormat`              | `exclude` wins over a wide-open `include`; patterns reach the full name, the username and the email alike; `format: username` writes the local part, and an address with no `@` is returned whole rather than attributed to nobody.          |
| `TestAuthorsLadderAndFlags`                | The object rides the configuration ladder field by field — a package's `off` defeats the root's `both` while inheriting the title — and `--authors`, `--authors-format` and `--authors-title` beat whatever the ladder resolved.             |
| `TestAuthorsGitHubCommandFlags`            | The same six flags on `dispat github`, where they must reach the release body the step command posts rather than a file it writes.                                                                                                           |
| `TestAuthorsFlagRejectsAnUnknownValue`     | A bad enum flag on `changelog` or `github` is refused in the config validator's own words, before anything is planned or written.                                                                                                            |
| `TestAuthorsSeparateReleasers`             | Two packages releasing to one GitHub repository under different attribution get different bodies. They share every field a releaser is addressed by, so this is the end-to-end proof that the six settings reach `GitHubSpec.Key`.           |
| `TestAuthorsCorrectionsAndSuppression`     | A restatement is attributed to whoever restated it, not to the commit it corrects (§7.4.2); a revert takes both entries and their attribution out of the notes (§7.3).                                                                       |
| `TestAuthorsPrereleaseFreshWindow`         | Attribution narrows exactly as the notes do: each prerelease credits its own changeset, and the stable graduation collecting the train credits the whole train.                                                                             |
| `TestAuthorsPreviewRendersTheBlocks`       | `dispat preview` prints the record bodies a release would write, so it gains both blocks by construction.                                                                                                                                    |

## Regression fences

Dedicated guard tests pin subtle planner properties so regressions fail exactly one distinct test:

1. **A rejected `Release-As` pin must not swallow a sibling unit's bump.** Every pin guard reports its error and falls
   back to the ordinarily computed version (see section 16's unit-scoped blast radius). The sibling releases at its
   computed version, while a lone rejected pin releases nothing. `TestPlanRejectedPinFallsBackToTheComputedBump` and
   unit tests in `internal/plan` guard against publishing an unchanged baseline and dropping a `feat` that shared a
   commit with a bad pin.
2. **A propagated graduation transition must graduate the dependant.** Transitions bypass the graduation guard,
   although dispat suppresses a propagated *bare* `stable`. This allows `release(core)%beta>stable%%beta>stable++*` to
   terminate the entire train. Guarded by `TestPlanPropagatedGraduationTransitionGraduatesTheTrain` and unit tests in
   `internal/plan`, preventing dependants from being abandoned on the train (W200/W206).
3. **Train-wide accounting must never leak into fresh-changeset reporting, and the graduation must widen back.**
   Planner accounting fields (`Units`, `OwnBump`, the window) span the full prerelease train. User-facing outputs
   (`ownCommits`, `Reason()`, the catch-up scan, the skip cascade, and entry bodies) read the fresh subset, except the
   graduation entry which aggregates the full train and its provider movement. Guarded by
   `TestLongitudinalGroupTrainLifecycle` (goal 38), `TestPlanTrainCatchUpStaysACatchUp` (goal 1), `internal/plan`, and
   `internal/changelog`.

   Why six instances of this bug class survived earlier reviews:
   * The seam is invisible off the train, because `Units == FreshUnits` for every stable-line release. Only a
     multi-release prerelease sequence distinguishes the two, and none existed prior to goal 38.
   * Hand-built fixtures encoded the implementer's assumption: a `Release` with `Units` set and `FreshUnits` empty
     passes whichever field the code reads. Stable-line fixtures must set both to the same slice, as documented in
     `plan.go`.
   * Reviews checked line coverage instead of matching behavior against documentation, which showed `ownCommits=0` on
     catch-ups while the code emitted the train-wide count.
   * Reviewers patched each discovered defect in isolation without auditing the entire seam. Grep every consumer of
     these fields before resolving tickets.
   * No tests modelled this repository's topology (group x train x wired records x propagation) until the first
     dedicated test caught the sixth instance immediately.

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

dispat builds binaries once per `go test` invocation and shares them across all tests. Each test creates its repository
in a fresh `t.TempDir()`, keeping tests isolated and safe to run in any order or subset.

## Conventions for new tests

- Assert against JSON events (`res.Events`, `harness.HasCodeForPackage`) and git state, never against pretty log text.
  Prefer `HasCodeForPackage` over `HasCode` whenever the diagnostic names a package.
- Author configs as `pkg/models` values starting from `harness.BaseFile(concurrency...)` and write them with
  `r.WriteConfigModel(cfg)`. Fall back to `harness.WriteConfigRaw` only for shapes the model cannot express, such as
  unknown keys. Reuse shared fixtures in `helpers_test.go` (`singlePackageRepo`, `linkedRepo`, `libsConfig`,
  `markerBuild`/`buildRuns` for script-execution claims). If only one test uses a config, write it out fully inside
  that test to keep the input visible.
- Use `r.ReleaseOK()` and `r.StatusOK()` for runs that must succeed. Use plain `Release()` or `Status()` with an
  explicit code assertion when you expect a non-zero exit code.
- `HasTag` requires an exact match. Use `TagCount("pkg@")` to check whether a package was tagged at all, because an
  exact match against a bare prefix passes vacuously.
- Use `tsmark` for assertions about *when* something ran. Use `AssertSequential` for flake-free claims about execution
  *order*. Reserve `AssertOverlaps` and `AssertConcurrencyBudget` with generous sleeps for tests that require actual
  concurrency.
- Build one multi-run scenario per behaviour cluster. Adding extra runs to an existing fixture is faster than creating
  a new one, and testing convergence (running again changes nothing) validates stability.
- `harness.BaseFile` disables GitHub by default. Re-enable it only when testing recorders by overriding `GitHub`
  against an httptest server.
