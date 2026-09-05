# Concepts

How dispat decides what to release, at what version, and what happens when something fails. This is the mental model
behind every command: semantic versioning driven by conventional commits, across a monorepo where the packages depend
on each other. The reference pages ([configuration](./configuration/README.md), [commits](./reference/commits.md),
[environment](./reference/environment.md)) spell out the details.

## Versions live in tags

Versions live exclusively in annotated git tags: `package%MAJOR.MINOR.PATCH` by default, or whatever the space's
[`tagFormat`](./configuration/versions.md#tagformat) says. There is no version database and no state file. For each
package the planner resolves two tags:

- **baseline**: the highest tag by semver precedence, prereleases included. This is what the package last published,
  and the channel it is on is read from it, so `1.5.0-beta.3` is on `beta` and `1.4.2` on `stable`.
- **stable baseline**: the highest tag with no prerelease component. This is where versions are computed from, and it
  is what the *pending window* is measured from: every commit from that tag to `HEAD`.

The two coincide for a package on the stable channel. They differ on a prerelease train, and that is the point: the
window then spans the whole train, so a breaking change arriving mid-train moves the whole train rather than continuing
under a version that no longer describes the content.

When the newest tag exists but cannot be parsed, a stray `core@0.0.1.0` for instance, older parseable tags are not
trusted either. The baseline then comes from the optional top-level `initials` config map, which maps a package name to
a version and defaults to `0.0.0`, while commits are still scanned from the unparseable tag.

### Which is also why there is no cache

Everything dispat needs in order to decide what to do is already in the repository: the tags say what was published,
and the commits since them say what changed. The plan is a pure function of those two things plus your config, so it is
recomputed from scratch on every run, in milliseconds, and two runs on the same repository always agree.

That is what removes the need for a task cache. A tool that caches task results has to run the task, hash its inputs,
decide whether the hit is still valid, and offer you a way to clear the cache when it decides wrong. dispat skips a
different way: a package with nothing in its window is not in the plan, so its scripts never start. There is nothing to
cache because there is nothing to skip, and nothing to go stale because nothing was stored. No cache directory, no
state file, no daemon.

The practical consequence is that dispat composes with whatever you already cache rather than replacing it. BuildKit
layers, an Nx, Turborepo or Bazel cache, ccache and the Gradle build cache all live *inside* a stage script, where they
make a build dispat did schedule faster. None of them can affect which versions get computed, what publishes in which
order, or what gets tagged, because none of that is downstream of a build.

## Commits carry the intent

Commit messages are parsed by [`pkg/ccme`](./go/ccme.md), which implements Conventional Commits with a monorepo
extension, regex-free and in a single pass. A commit may hold several units separated by `---`, each with its own
scope, directives and footers:

| Header                    | Own bump | Reaches                                    |
|---------------------------|----------|--------------------------------------------|
| `fix(core): ...`          | patch    | `core` only                                |
| `feat(core): ...`         | minor    | `core` only                                |
| `feat(core)!: ...`        | major    | `core` only                                |
| `feat(core)^: ...`        | minor    | `core` + its direct consumers              |
| `feat(core)^^: ...`       | minor    | `core` + every transitive consumer         |
| `feat(core)+2: ...`       | minor    | `core` + consumers up to two edges away    |
| `feat(core)^minor: ...`   | minor    | consumers take `minor` instead of `patch`  |
| `feat(core)^none: ...`    | minor    | `core` only; the caret is explicitly inert |
| `fix(core,utils): ...`    | patch    | both packages                              |
| `fix(*,-app): ...`        | patch    | every package except `app`                 |
| `fix: ...` (no scope)     | patch    | the packages owning the commit's files     |
| `cancel(core): ...`       | none     | discards `core`'s unreleased metadata      |
| `release(core)%beta: ...` | none     | moves `core` onto the beta line            |

Scopes must name discovered packages. `*` means the whole workspace, a term containing `*` is a glob, `.` is the
file-derived set, and a leading `-` excludes. A typo in an *include* is an error, because it would otherwise silently
drop a release, while excluding a package that no longer exists is only a warning.

### Propagation is opt-in

A plain `feat(core): ...` releases `core` and nothing else. Reach is stated per commit with `^` for one edge, `^^` for
all edges, or `+N` for exactly N.

The bump dependants take defaults to `patch`. `^minor` or a `Propagate:` footer changes it, and `^none` disables it.
`Propagate-Scope` intersects the reached set with a scope set, so an internal app can be kept out of a workspace-wide
change.

Bumps merge by `max()`, so two changed providers still produce one patch, and a consumer's own `feat` beats an incoming
`patch`. A package never propagates onward from a bump it received: the originating commit's own depth is the only
control, which is what keeps blast radius readable from the message.

### Prereleases and channels

A package's channel is derived from its baseline tag alone, with no side file and no config. `%beta` on a unit puts its
packages on the beta line, `%%` and `++N` propagate a channel to dependants, and `%beta>stable` is a transition that
graduates whatever still matches.

A stable consumer is not dragged into a release by a provider's prerelease, since it could not resolve it anyway.
`feat(core)^%beta` releases `core` alone and reports why; to take the consumers along, put them on the line too with
`feat(core)^%beta++1`. Trains converge on their own, so once a package is on `beta` a directive saying `beta` proposes
nothing.

A typical train, in tags:

```
feat(core)%beta: try streaming     ->  core@1.3.0-beta.0
fix(core): edge case               ->  core@1.3.0-beta.1
feat(core): second feature         ->  core@1.4.0-beta.0   (the train's target recomputes)
release(core)%stable: promote      ->  core@1.4.0
```

Release notes follow the train's shape. Each prerelease's changelog entry and GitHub release document only its own
changeset, so `beta.1` does not repeat `beta.0`'s notes, and the graduation then collects the whole train into the one
entry the stable line's readers see. The version, by contrast, is always computed over the whole train.

### Space versioning modes

A space may declare how much of its packages' versions is held in common.
[`versioning`](./configuration/spaces.md#versioning) is `independent`, the default and everything described above, or
one of six shared modes.

Two axes decide them. **How much is shared:** the whole version (`fixed`), the major and minor (`fixedMajorMinor`), or
the major alone (`fixedMajor`). **What an unchanged member does when the shared part moves:** release along with it
(the plain modes), or stay put until it next has something of its own (the `Sparse` variants).

A release moves the whole group when it reaches the shared part, and belongs to one package alone when it stays below
it. So under `fixed` the space versions as one package: any change releases every member at one shared next version,
computed over the space's highest baseline with the max bump, the space runs a single prerelease train, and an exact
`Release-As` on one member pins the space. Under `fixedMajor` the same is true of a breaking change, while a fix or a
feature moves only its own package, along with its own train and its own pins.

A member released with nothing of its own gets one "no changes" changelog entry naming what is shared, labelled `W234`
in the plan, and under a plain mode a member left behind by a failed ride is re-aligned on the next run. Commit and
file scopes keep exactly one job in every shared mode: deciding which changelog entries, and which GitHub release
notes, each package receives.

Two commits under each mode, for a space of `a` and `b` both at `1.0.0`, where only `a` ever changes:

| Mode                    | `feat(a)` gives `b`                         | `feat(a)!` gives `b`                        |
|-------------------------|---------------------------------------------|---------------------------------------------|
| `independent`           | stays `1.0.0`, not released                 | stays `1.0.0`, not released                 |
| `fixed`                 | `1.1.0`, a "no changes" release (W234)      | `2.0.0`, a "no changes" release (W234)      |
| `fixedSparse`           | stays `1.0.0` until its own next change     | stays `1.0.0` until its own next change     |
| `fixedMajorMinor`       | `1.1.0`, a "no changes" release (W234)      | `2.0.0`, a "no changes" release (W234)      |
| `fixedMajorMinorSparse` | stays `1.0.0` until its own next change     | stays `1.0.0` until its own next change     |
| `fixedMajor`            | stays `1.0.0`: a minor is not shared        | `2.0.0`, a "no changes" release (W234)      |
| `fixedMajorSparse`      | stays `1.0.0`: a minor is not shared        | stays `1.0.0` until its own next change     |

`a` releases `1.1.0` and `2.0.0` in every row. The walkthrough, with worked examples and the rules for groups that span
spaces, is [Shared versions](./reference/releasing/versioning.md).

### Release control

`Release-As: none` holds a package: its bump is retained and reported rather than released, until a later
`Release-As: auto` resumes it at the `max()` of everything accumulated.

`Release-As: <version>` pins an exact version, guarded against going backwards, against undershooting what the commits
require, and against a major jump of more than one. `cancel(pkg)` discards unreleased metadata for a package, and never
reaches an already-published tag.

## Failure and recovery

### When a provider fails or is skipped

Failures never abort the run, and once a package publishes nothing can fail it at all.

A provider that failed at any stage, meaning version, build or publish, or that was skipped, taints its consumers. They
are skipped unless they have a release reason of their own, which means either their own conventional commits or
another changed provider that did publish successfully.

This holds in both `isBuildWaitingPublish` modes, because a consumer's publish always waits for its providers'
publishes. Even a consumer that already built, which `isBuildWaitingPublish: false` allows while the provider is still
publishing, is skipped at its publish once the provider's publish failure is known. Nothing is ever published against
an unpublished provider version, and skips cascade down the dependency chain by the same rule.

A consumer that proceeds on its own reason runs its pipeline normally, with two adjustments: failed and skipped
providers are filtered out of the `DISPAT_UPDATED_*` variables, and if it had providers to pick up and none survive,
the version script is not executed at all.

Spaces with `revertOnFail: true` additionally roll back every local change inside a failing package's folder, restoring
tracked files and removing untracked ones, so a half-finished release leaves no residue in the worktree.

### Catch-up: failed consumers are never lost

Publishing is not atomic, so a run can end with some packages published and others not. Catch-up is not a repair pass
bolted on for that case. It is what the ordinary rule does when asked against the right window.

A commit propagates to a dependant exactly while *the dependant's own* fresh window still contains it, and that does not
change when the provider releases. So a consumer that missed a run is still owed its release on the next one, with no
state file, no timestamp comparison and no second traversal.

Four properties explain safe [failure recovery](#failure-and-recovery):

- **No orphans.** A contribution remains owed until the dependant releases it, while its corrected source set and
  channel eligibility remain unchanged.
- **Once in the release ledger.** The successful baseline tag removes the contribution from the dependant's fresh
  window. Publication and tagging are separate external operations, so recovery may reconcile an artefact that already
  exists rather than promise exactly-once network delivery.
- **Same version under the same inputs.** A catch-up keeps its reviewed version while its baseline, corrected sources,
  and resolved source and target channels remain unchanged. If those inputs move, dispat surfaces the recomputed plan.
- **No widening under the same admission inputs.** Targets only shrink while sources, traversal, and channel eligibility
  stay fixed. A newly eligible source or channel can expose a finite follow-up, which must be reviewed before publishing.

Such a release is labelled a **catch-up** in the plan and reported with the origin's *published* version, because a
package appearing with no commits of its own and no releasing dependency is otherwise baffling to review. Its version
stage receives the provider's already-released version in the `DISPAT_UPDATED_*` variables, so manifests still sync.

To stop a catch-up you no longer want, act on the **consumer**: `cancel(<consumer>)` drops it and a hold defers it.
Acting on the provider does nothing, because its version is already public and cancellation never reaches a published
release.

## The pipeline

**Per released package**, up to four stages, each optional to script:

1. **version**: runs when any provider of the package moved in this run, and for every releasing package of a space
   with [`autoVersion`](./configuration/autoversion.md), where native reconciliation checks the baselines too. It runs
   right before the build. With `isBuildWaitingPublish: true` on the provider's space it waits for that provider's
   build *and publish*; with `false` it waits for the provider's *build* only.
2. **build**: the package's build command. Like every script it may export outputs, by appending
   `DISPAT_OUTPUT_NAME=value` (or bare `NAME=value`) lines to the file `$DISPAT_OUTPUT` points at. Each value travels
   to every later script of the package as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` naming the
   exporter. One export is special: `DISPAT_EXPORT_GITHUB`, holding absolute file paths, opts the package into a GitHub
   release with those files as assets.
3. **publish**: waits for the package's own build and always for its providers' publishes. A space with a `flow.login`
   authenticates **once per space** before its first publish, every other publish of the space waits for it, and a
   login failure fails them all; the login's exports reach every package of the space from its publish onward. On
   success the release recorders run (the changelog file, and a GitHub release for packages that exported
   `DISPAT_EXPORT_GITHUB`), then the annotated tag is created, with pushing left to CI by default. The publish script
   succeeding is the point of no return: from there nothing can fail the package, and a record or a tag that cannot be
   written is [reported instead](./internals/architecture.md#after-the-point-of-no-return).
4. **announce**: runs after the publish frame, for pushing the release out to update channels such as a Slack message,
   a webhook or a docs feed. It gets the release notes as `DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES` and
   `DISPAT_FIXES`, the same grouped data the changelog and the GitHub release render, and the whole frame, its
   `flow.beforeAnnounce` and `flow.postAnnounce` hooks included, only warns on failure, because the release is already
   out.

Every script option accepts a single script name or an array of names run in sequence. A failing command in a
release-gating sequence stops it and fails the package, while warn-only sequences keep running and only log.

The stages can also be bracketed with per-space hooks. `flow.beforeAll`, `flow.beforeVersion`, `flow.postVersion`,
`flow.beforeBuild`, `flow.postBuild` and `flow.beforePublish` all *gate* the release, so their failure fails the
package, while `flow.postPublish` only warns since the release is already out.

Run-level hooks observe the whole run instead. A gating `run.beforeAll` runs once before the task graph and its failure
aborts the run, and the warn-only `run.postAll` and commit and push hooks run after, with the outcome exported as
`DISPAT_PUBLISHED/FAILED/SKIPPED/CANCELLED/UNPLANNED_PACKAGES` and per-package `DISPAT_RESULT_*` variables.

Everything released is versioned and tagged, whatever its build produces. An exception there would cost convergence,
because a package whose window never advances reappears in every plan for ever. Packages held by `Release-As: none`
are the one thing that legitimately persists across runs, and they are excluded from the pipeline entirely.

Optionally the run can end with a *finalize phase*, disabled by default. The `commit` option creates one release commit
capturing every published package's changelog and manifest changes. Tags then point at that commit, and GitHub releases
move to the end of the run. `commit.push` pushes the commit and the tags, skipping any tag already on the remote, so a
partially pushed run converges.

Two guards protect that phase. Git and GitHub access are verified up front, before any work starts. And a push-mode run
refuses a checkout that is behind the remote branch, because its plan was computed from stale tags and its push would
be rejected anyway.

The phase is bracketed by the warn-only run hooks `run.beforeCommit`, `run.afterCommit`, `run.postCommit` (after the
commit and the tags) and `run.beforePush` and `run.afterPush`.

Build and publish have independent concurrency budgets (`concurrency: [build, publish]`), and version tasks share the
build budget. A stage with no configured script still runs, keeping ordering, statuses, tags and release records
intact; it simply executes no shell command.

### Running scripts outside a release

`dispat run <name>`, or just `dispat <name>`, runs the
[script](./configuration/spaces.md#scripts-and-dispat-run) of that name inside each changed package that has one. The
[`--package`, `--space` and `--group`](./cli/run.md#choosing-the-packages) flags pick a different set instead, and the
folder you stand in counts as one of those terms.

The run honours the dependency graph and stays inside the build concurrency budget, which is the configured value or
`--concurrency`'s first value when given. `--on-error` decides whether a failure skips the dependents. Scripts get the
same `DISPAT_*` environment they get during a release, but nothing is released or tagged.

It uses the same three-level `scripts` lookup the stages use, so where you define a name, whether in the file, on a
space or on one package, is what decides how far the run reaches.
