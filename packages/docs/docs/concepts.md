# Concepts

A release starts with three inputs: your configuration, Git commits, and release tags. dispat uses them to decide
which packages need work, compute their versions, and order the build and publish steps.

Keep these terms in mind:

| Term | Meaning |
| --- | --- |
| Package | A configured folder with its own version and release work. |
| Space | A group of packages that share settings. Standalone packages do not need one. |
| Provider | A package another package depends on. |
| Consumer | A package using a provider. In `app → core`, `app` consumes `core`. |
| Plan | The packages selected for a release, their proposed versions, and the reasons they were selected. |
| Stage | One part of a package's release, such as build or publish. Your configuration supplies its commands. |

For a first read, follow [versions](#versions-live-in-tags), [commits](#commits-carry-the-intent), and
[the pipeline](#the-pipeline). If you are dealing with a stopped release, go to
[failure and recovery](#failure-and-recovery). The reference pages cover the individual settings and commands.

## Versions live in tags

Annotated Git tags record published package versions. The default name is `package@MAJOR.MINOR.PATCH`; change it with
[`tagFormat`](./configuration/versions.md#tagformat). Planning does not need a separate version database.

For each package, the planner looks up two baselines:

- **baseline**: the highest tag by semver precedence, prereleases included. This marks what the package last published.
  dispat reads the channel from it, so `1.5.0-beta.3` is on `beta` and `1.4.2` is on `stable`.
- **stable baseline**: the highest tag with no prerelease component. dispat computes versions from here. It also
  measures the *pending window* from here, containing every commit from that tag to `HEAD`.

The two coincide for a package on the stable channel, but differ on a prerelease train. The window then spans the whole
train. A breaking change arriving mid-train moves the whole train rather than continuing under a version that no longer
describes the content.

When the newest tag exists but cannot be parsed, like a stray `core@0.0.1.0`, dispat does not trust older parseable
tags either. The baseline then comes from the optional top-level `initials` config map. This maps a package name to a
version and defaults to `0.0.0`, while commits are still scanned from the unparseable tag.

### Planning and build caches {#which-is-also-why-there-is-no-cache}

The planner reads the selected history, release records, and resolved configuration. The same inputs produce the same
versions. Packages with no release work are left out, so their stage scripts do not run. Planning cost depends on the
history and dependency graph.

That selection is separate from caching build output. Keep using BuildKit layers, Nx, Turborepo, Bazel, ccache, or
Gradle caches inside your stage commands. They can speed up a scheduled build; they do not decide dispat's version
plan or release order.

## Commits carry the intent

Commit messages tell dispat what changed and which packages should release. The
[`pkg/ccme`](./go/ccme.md) parser reads Conventional Commits plus dispat's package and propagation directives.

A commit can contain several **units**, separated by a line of `---`. Each unit has its own header, body, and footers.
These headers cover the common cases:

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
file-derived set, and a leading `-` excludes. A typo in an *include* is an error to prevent silently dropping a
release, but excluding a package that no longer exists is only a warning.

### Propagation is opt-in

A plain `feat(core): ...` releases `core` and nothing else. You state reach per commit with `^` for one edge, `^^` for
all edges, or `+N` for up to N.

The bump that dependants take defaults to `patch`. You can change this with `^minor` or a `Propagate:` footer, or
disable it with `^none`. `Propagate-Scope` intersects the reached set with a scope set, so you can keep an internal app
out of a workspace-wide change.

Bumps merge by `max()`, so two changed providers still produce one patch, and a consumer's own `feat` beats an incoming
`patch`. A package never propagates onward from a bump it received. The originating commit's own depth is the only
control, keeping the blast radius readable from the message.

### Prereleases and channels

dispat derives a package's channel from its baseline tag alone, using no side file and no config. `%beta` on a unit
puts its packages on the beta line, while `%%` and `++N` propagate a channel to dependants. The `%beta>stable` syntax
is a transition that graduates whatever still matches.

A provider's prerelease does not automatically move a stable consumer onto that prerelease channel.
`feat(core)^%beta` releases `core` alone and reports why, but you can take the consumers along by putting them on the
line too with `feat(core)^%beta++1`. Trains converge on their own, so once a package is on `beta`, a directive saying
`beta` proposes nothing.

A typical train looks like this in tags:

```
feat(core)%beta: try streaming     ->  core@1.3.0-beta.0
fix(core): edge case               ->  core@1.3.0-beta.1
feat(core): second feature         ->  core@1.4.0-beta.0   (the train's target recomputes)
release(core)%stable: promote      ->  core@1.4.0
```

Release notes follow the train's shape. Each prerelease's changelog entry and GitHub release document only its own
changeset, so `beta.1` does not repeat `beta.0`'s notes. The graduation then collects the whole train into the one
entry the stable line's readers see, while the version is always computed over the whole train.

### Space versioning modes

A space may declare how much of its packages' versions is held in common. The
[`versioning`](./configuration/spaces.md#versioning) field is `independent` by default, which behaves as described
above, or you can set it to one of six shared modes.

Two axes decide these modes. **How much is shared:** the whole version (`fixed`), the major and minor
(`fixedMajorMinor`), or the major alone (`fixedMajor`). **What an unchanged member does when the shared part moves:**
release along with it (the plain modes), or stay put until it next has something of its own (the `Sparse` variants).

A release moves the whole group when it reaches the shared part, and belongs to one package alone when it stays below
it. Under `fixed`, the space versions as one package: any change releases every member at one shared next version, the
space runs a single prerelease train, and an exact `Release-As` on one member pins the space. Under `fixedMajor`, the
same is true of a breaking change, while a fix or a feature moves only its own package, along with its own train and
its own pins.

Whether the members also share the prerelease counter and the channel is two further choices, declared on a
[`versionGroups` entry](./configuration/spaces.md#the-sharing-axes) and shared by default. The paragraphs below and the
page they link to describe that default.

When a group member releases only to follow the shared version, the plan reports `W234` and its changelog explains
that there are no changes of its own. Under a plain shared mode, a member that missed the release is brought back
into alignment on a later run. Commit and file scopes still decide which release-note entries belong to each package.

Here are two commits under each mode, for a space of `a` and `b` both at `1.0.0`, where only `a` ever changes:

| Mode                    | `feat(a)` gives `b`                         | `feat(a)!` gives `b`                        |
|-------------------------|---------------------------------------------|---------------------------------------------|
| `independent`           | stays `1.0.0`, not released                 | stays `1.0.0`, not released                 |
| `fixed`                 | `1.1.0`, a "no changes" release (W234)      | `2.0.0`, a "no changes" release (W234)      |
| `fixedSparse`           | stays `1.0.0` until its own next change     | stays `1.0.0` until its own next change     |
| `fixedMajorMinor`       | `1.1.0`, a "no changes" release (W234)      | `2.0.0`, a "no changes" release (W234)      |
| `fixedMajorMinorSparse` | stays `1.0.0` until its own next change     | stays `1.0.0` until its own next change     |
| `fixedMajor`            | stays `1.0.0`: a minor is not shared        | `2.0.0`, a "no changes" release (W234)      |
| `fixedMajorSparse`      | stays `1.0.0`: a minor is not shared        | stays `1.0.0` until its own next change     |

Package `a` releases `1.1.0` and `2.0.0` in every row. You can find worked examples and the rules for groups that span
spaces in [Shared versions](./reference/releasing/versioning.md).

### Release control

Use `Release-As: none` to hold a package. dispat retains and reports the bump rather than releasing it, until a later
`Release-As: auto` resumes it at the `max()` of everything accumulated.

`Release-As: <version>` pins an exact version. This guards against going backwards, undershooting what the commits
require, and making a major jump of more than one. `cancel(pkg)` discards unreleased metadata for a package, and never
reaches an already-published tag.

## Failure and recovery

### When a provider fails or is skipped

A package failure can leave independent branches of the release running. A successful publication cannot be undone
by a later error; failures in recording or announcements are reported separately. Errors that prevent a valid run,
such as a failed gating run hook, can still stop the whole run.

For a failed or skipped provider, the consumer's next step depends on its release reasons and the
[provider relation](./configuration/spaces.md#the-provider-relation):

| Relation | How the failure affects the consumer |
| --- | --- |
| `publish` | The consumer needs the provider's published output before building. It is blocked even if it has changes of its own. |
| `none` | The consumer can build without waiting. By default, an independent release reason can keep it eligible after a provider fails. |
| `build` | An independent reason can keep the consumer eligible to proceed. The publication and manifest checks below still apply. |

For `none` and `build`, set `isBlocking: true` if a provider failure must always block consumers. Under `publish`,
blocking is required and cannot be disabled.

An independent reason can be the consumer's own commits, a channel change, or a version it needs to adopt from another
successful provider. If every reason came from failed or blocked providers, the consumer is skipped.

Publication always respects provider publication order, including dependencies reached through a package that has no
release this time. An unchanged intermediate package does not break the chain. When a provider failure blocks a
consumer, `blockedBy` names the provider that failed. An indirect provider can order or block work, but it does not
create a new release reason for a consumer that does not declare it.

When a consumer remains eligible, dispat checks the versions it is about to publish:

1. Failed and skipped providers are removed from `DISPAT_UPDATED_*`.
2. If no provider update remains, a version script that existed only to pick up those updates is skipped.
3. Before publishing, manifests are reconciled against the providers that actually published. Native auto-versioning
   restores a failed provider's baseline; script-based reconciliation reruns `flow.version` with the updated provider
   set.

Rewriting a manifest cannot repair an artifact that already embedded an unavailable provider version. In that case,
dispat blocks the consumer with `W194` rather than rebuilding it implicitly. The next run can build against the
provider versions that are available.

With `revertOnFail: true`, dispat also restores tracked files and removes untracked files inside the failing package's
folder. This local cleanup does not undo uploads to registries or changes outside that package folder.

### Catch-up: failed consumers are never lost

A release can publish some packages and stop before others finish. The next plan uses release tags to identify work
that still needs to reach a consumer. The plan calls this **catch-up**.

For example, `feat(core)^` requests a release of `core` and its direct consumers. If `core` publishes but `app` fails,
`app` still needs the change on the next run. dispat passes `core`'s already-published version to `app` through
`DISPAT_UPDATED_*`, so its manifest can adopt the right version.

The rule also covers a less obvious case: `app` releases for its own changes while `core` is held or fails. `app`'s
newer tag does not prove it received the pending provider change. When `core` eventually publishes, dispat checks
whether a consumer release actually incorporated that provider release and schedules catch-up if needed.

This check uses commit ancestry, not tag dates. If a provider is released alone on the exact commit where its consumer
already released, ancestry cannot establish the delivery order. dispat refuses that case with
[`E201`](./reference/plan-errors.md#after-the-plan-before-any-releasing). Release both together, or release the provider
after a new commit.

The recovery rules have four practical limits:

- **Pending delivery stays pending.** A consumer still needs a propagated contribution until it receives the provider
  release, provided the corrected source set and channel eligibility have not changed.
- **A recorded contribution is not repeated.** Its successful baseline tag removes it from the consumer's fresh window.
  Uploading and tagging are separate operations, though: an interrupted upload may already exist at the destination
  without a corresponding tag. Check that destination before retrying.
- **Unchanged inputs keep the reviewed version.** If the baseline, corrected sources, or source and target channels
  change, review the recomputed plan.
- **Unchanged selection inputs do not add new targets.** New source or channel eligibility can expose follow-up work;
  it needs review before publication.

To stop unwanted catch-up, act on the **consumer**: `cancel(<consumer>)` discards its pending contribution, while a
hold defers it. Cancelling the provider cannot retract a version it already published. See the
[recovery guide](./reference/releasing/recovery.md) for commands and worked failures.

## The pipeline

**Per released package**, dispat runs the stages of its flow's graph: a version stage and a build for every package,
the other built-in ones where the flow configures them, and any
[stages of your own](./configuration/spaces.md#stages-of-your-own). By default they run in this order:

1. **sign**: runs only for a package whose space configures it, with [`autoSign`](./configuration/autosign.md) or a
   `flow.sign`, `flow.beforeSign` or `flow.postSign` entry. It writes the package's own version wherever that version
   lives, before anything else of the release runs. A package without that configuration has no sign stage at all.
2. **version** (also called **propagate**): runs when any provider of the package moved in this run, and for every
   releasing package of a space with [`autoVersion`](./configuration/autoversion.md) (or `autoPropagate`), where native
   reconciliation checks the baselines too. It runs right before the build, after the sign stage when there is one.
   It runs and reports under the name its configuration gave it: `propagate` for `flow.propagate` or `autoPropagate`,
   `version` otherwise. It writes planned versions and waits for no provider.
   What every later stage of the package waits for on each moving provider is that provider's
   [relation](./configuration/spaces.md#the-provider-relation): the provider's build *and publish* under
   `isBuildWaitingPublish: true`, the provider's *build* only under `false`, and nothing of the provider at all under
   `{build: none}`. A stage that reads a provider build that never happened fails there, naming the provider.
3. **build**: the package's build command. Every package has a build stage, and one with no `build` entry runs no
   command there. Like every script, it may export outputs by appending
   `DISPAT_OUTPUT_NAME=value` (or bare `NAME=value`) lines to the file `$DISPAT_OUTPUT` points at. Each value travels
   to every later script of the package as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` naming the
   exporter. One export is special: `DISPAT_EXPORT_GITHUB` holds absolute file paths and opts the package into a GitHub
   release with those files as assets.
4. **test**: runs only for a package whose space configures `flow.test`, `flow.beforeTest` or `flow.postTest`. It
   checks what the build produced, after the build and before the publish, so a package whose artefact fails its
   checks is never published. It reads the build's exports and hands its own to the publish. A package without that
   configuration has no test stage at all.
5. **publish**: for a package whose flow configures a publish or one of its hooks. It waits for every stage before it, and always for its
   providers' publishes. A space with a `flow.login`
   authenticates **once per space** before its first publish, failing all publishes on error, while successful login
   exports reach every package of the space. On success, the release recorders run (the changelog file, and a GitHub
   release for packages that exported `DISPAT_EXPORT_GITHUB`) and the annotated tag is created. With `commit.push`
   on, the tag is pushed right away, or, in release-commit mode, a publication receipt stands in for it until the
   release commit exists, so a run stopped anywhere after the publish leaves the package recorded on the remote. With
   it off, pushing is left to CI. The publish script succeeding is the point of no return: from there nothing can fail the package, and a
   record or a tag that cannot be written is
   [reported instead](./internals/architecture.md#after-the-point-of-no-return).
6. **announce**: runs after the publish frame, pushing the release out to update channels such as a Slack message, a
   webhook, or a docs feed. It gets the release notes as `DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES` and
   `DISPAT_FIXES`, which is the same grouped data the changelog and the GitHub release render. The whole frame,
   including its `flow.beforeAnnounce` and `flow.postAnnounce` hooks, only warns on failure because the release is
   already out.

Every script option accepts a single script name or an array of names run in sequence. A failing command in a
release-gating sequence stops it and fails the package, while warn-only sequences keep running and only log.

A package's stages form a graph: every package has a version stage and a build, the other stages are there where
the flow gives them something to do, and a stage you define sits beside the test stage, unordered with it, so a run with
room starts them at once. A package with no publish stage is recorded, tagged and reported published once its other
stages have completed. A stage entry written as an object places its stage with `before` or `after`, and `parallel` says two stages are
unordered: `announce: { script: announce, after: build }` runs the announce before the publication, where it gates. Everything before the release records gates the release; everything after
them observes a release that is out and only warns. A configuration no order realises is refused before any command
runs. See [the order of the stages](./configuration/spaces.md#the-order-of-the-stages).

The build, test and publish frames, and the frames of your own stages before the records, can run on
[worker nodes](./distributed-execution.md). The orchestrator keeps the preparing stages, the stages after the records
and release coordination. Without configured workers, all stages run on the machine that started the release.

You can bracket the stages with per-space hooks. `flow.beforeAll`, `flow.beforeSign`, `flow.postSign`,
`flow.beforeVersion`, `flow.postVersion`, `flow.beforeBuild`, `flow.postBuild`, `flow.beforeTest`, `flow.postTest`
and `flow.beforePublish` all *gate* the
release, so their failure fails the package. `flow.postPublish` only warns since the release is already out.

Run-level hooks observe the whole run instead. A gating `run.beforeAll` runs once before the task graph, and its
failure aborts the run. The warn-only `run.postAll` and commit and push hooks run after, exporting the outcome as
`DISPAT_PUBLISHED/FAILED/SKIPPED/CANCELLED/UNPLANNED_PACKAGES` and per-package `DISPAT_RESULT_*` variables.

Every successful package release needs its version tag, regardless of the artifact it produces. That tag advances
the package's baseline so later plans can distinguish completed work from pending work. A package held with
`Release-As: none` keeps its pending changes and does not enter the pipeline.

The run can optionally end with a *finalize phase*, which is disabled by default. The `commit` option creates one
release commit capturing every published package's changelog and manifest changes, pointing tags at that commit and
moving GitHub releases to the end of the run. `commit.push` pushes the commit and the tags, skipping any tag already on
the remote so a partially pushed run converges.

Two guards protect that phase. dispat verifies Git and GitHub access up front, before any work starts. A push-mode run
also refuses a checkout that is behind the remote branch, because its plan was computed from stale tags and its push
would be rejected anyway.

The warn-only run hooks `run.beforeCommit`, `run.afterCommit`, `run.postCommit` (after the commit and the tags),
`run.beforePush`, and `run.afterPush` bracket this phase.

Build and publish have independent concurrency budgets (`concurrency: [build, publish]`). The sign, version and test
stages and your own stages share the build budget wherever they sit; the publish, the records and the announce share
the publish budget.

### Running scripts outside a release

Run `dispat run <name>`, or just `dispat <name>`, to run the [script](./configuration/spaces.md#scripts-and-dispat-run)
of that name inside each changed package that has one. You can use the
[`--package`, `--space` and `--group`](./cli/run.md#choosing-the-packages) flags to pick a different set instead. The
folder you stand in counts as one of those terms.

The run honours the dependency graph and stays inside the build concurrency budget, which is the configured value or
the first value given to `--concurrency`. The `--on-error` flag decides whether a failure skips the dependents. Scripts
get the same `DISPAT_*` environment they get during a release, but nothing is released or tagged.

Scripts are resolved at the configuration, space, and package levels. Define a script at the level whose packages
should use it; a package can override a shared definition.
