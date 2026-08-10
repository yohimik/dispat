# Concepts

How dispat decides what to release, at what version, and what happens when things fail. This is the mental model behind
every command; the reference pages ([configuration](./configuration/README.md), [commits](./commits.md),
[environment](./environment.md)) spell out the details.

## Versions live in tags

Versions live exclusively in annotated git tags: `package%MAJOR.MINOR.PATCH` by default, or whatever the space's
[`tagFormat`](./configuration/versions.md#tagformat) says. For each package the planner resolves two tags:

- **baseline**: the highest tag by semver precedence, prereleases included. This is what the package last published, and
  the channel it is on is read from it (`1.5.0-beta.3` is on `beta`, `1.4.2` on `stable`).
- **stable baseline**: the highest tag with no prerelease component. This is where versions are computed from, and it is
  what the *pending window* is measured from: every commit from that tag to `HEAD`.

The two coincide for a package on the stable channel. They differ on a prerelease train, and that is the point: the
window then spans the whole train, so a breaking change arriving mid-train moves the whole train rather than continuing
under a version that no longer describes the content.

When the newest tag exists but cannot be parsed (e.g. a stray `core@0.0.1.0`), older parseable tags are not trusted
either: the baseline comes from the optional top-level `initials` config map (package name to version), defaulting to
`0.0.0`, while commits are still scanned from the unparseable tag.

## Commits carry the intent

Commit messages are parsed by [`pkg/ccme`](https://github.com/yohimik/dispat/tree/main/pkg/ccme): Conventional Commits with a monorepo extension,
regex-free and single-pass. A commit may hold several `---`-separated units, each with its own scope, directives and
footers:

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

Scopes must name discovered packages; `*` means the whole workspace, a term with `*` is a glob, `.` is the file-derived
set, and a leading `-` excludes. A typo in an *include* is an error, because it would otherwise silently drop a release;
excluding a package that no longer exists is only a warning.

### Propagation is opt-in

A plain `feat(core): ...` releases `core` and nothing else. Reach is stated per commit with
`^` (one edge), `^^` (all edges) or `+N`. The bump dependants take defaults to `patch`; `^minor` or a
`Propagate:` footer changes it, `^none` disables it. `Propagate-Scope` intersects the reached set with a scope-set, so
an internal app can be kept out of a workspace-wide change. Bumps merge by `max()`: two changed providers still produce
one patch, and a consumer's own `feat` beats an incoming `patch`. A package never propagates onward from a bump it
received. The originating commit's own depth is the only control, so blast radius is readable from the message.

### Prereleases and channels

A package's channel is derived from its baseline tag alone, with no side file and no config. `%beta` on a unit puts its
packages on the beta line; `%%`/`++N` propagate a channel to dependants;
`%beta>stable` is a transition that graduates whatever still matches. A stable consumer is not dragged into a release by
a provider's prerelease (it could not resolve it anyway): `feat(core)^%beta` releases `core` alone and reports why; to
take the consumers along, put them on the line too with `feat(core)^%beta++1`. Trains converge on their own:
once a package is on `beta`, a directive saying `beta` proposes nothing.

A typical train, in tags:

```
feat(core)%beta: try streaming     ->  core@1.3.0-beta.0
fix(core): edge case               ->  core@1.3.0-beta.1
feat(core): second feature         ->  core@1.4.0-beta.0   (the train's target recomputes)
release(core)%stable: promote      ->  core@1.4.0
```

Release notes follow the train's shape: each
prerelease's changelog entry and GitHub release document only its own changeset (`beta.1` does not repeat `beta.0`'s
notes), and the graduation collects the whole train into the one entry the stable line's readers see, while the version
is always computed over the whole train.

### Space versioning modes

A space may declare how its packages' versions relate:
[`versioning`](./configuration/spaces.md#versioning) is `independent` (the default, and everything described above),
`fixed` or `fixedSparse`. Under `fixed` the space versions as one package: a change to any member releases every member
at one shared next version (computed over the space's highest baseline with the max bump), the space runs a single
prerelease train, an exact `Release-As` on one member pins the space, and a member released with nothing of its own gets
one "no changes" changelog entry, labelled `W210` in the plan. A member left behind (a failed ride) is re-aligned to the
space's published version on the next run. `fixedSparse` computes the same shared version but releases only changed
members; the rest keep their previous versions until they change, at which point they jump to the shared version. Commit
and file scopes keep exactly one job in these modes: deciding which changelog entries (and GitHub release notes) each
package receives.

The same commit under each mode, for a space of `a` (changed by `feat(a)`) and `b` (unchanged, at `1.0.0`):

| Mode          | `a`              | `b`                                             |
|---------------|------------------|-------------------------------------------------|
| `independent` | `1.1.0`          | stays `1.0.0`, not released                     |
| `fixed`       | `1.1.0`          | `1.1.0`, released with a "no changes" entry (W210) |
| `fixedSparse` | `1.1.0`          | stays `1.0.0` until its own next change         |

### Release control

`Release-As: none` holds a package: its bump is retained and reported, not released, until a later
`Release-As: auto` resumes it at the `max()` of everything accumulated. `Release-As: <version>` pins an exact version,
guarded against going backwards, against undershooting what the commits require, and against a major jump of more than
one. `cancel(pkg)` discards unreleased metadata for a package; it never reaches an already-published tag.

## Failure and recovery

### When a provider fails or is skipped

Failures never abort the run. A provider that failed at any stage (version,
build or publish) or was skipped taints its consumers: they are skipped unless they have a release reason of their own,
meaning their own conventional commits or another changed provider that did publish successfully. This holds in both
`isBuildWaitingPublish` modes; a consumer's publish always waits for its providers' publishes, so even a consumer that
already built (allowed by `isBuildWaitingPublish: false` while the provider was still publishing) is skipped at its
publish once the provider's publish failure is known. Nothing is ever published against an unpublished provider version.
Skips cascade down the dependency chain by the same rule. A consumer that proceeds on its own reason runs its pipeline
normally, except that failed and skipped providers are filtered from the `DISPAT_UPDATED_*`
variables, and if none remain the version script is not executed at all. Spaces with `revertOnFail: true`
additionally roll back all local changes inside a failing package's folder (tracked files restored, untracked files
removed), so a half-finished release leaves no residue in the worktree.

### Catch-up: failed consumers are never lost

Publishing is not atomic, so a run can end with some packages published
and others not. Catch-up is not a repair pass bolted on for that case; it is what the ordinary rule does when asked
against the right window. A commit propagates to a dependant exactly while *the dependant's own* window still contains
it, which does not change when the provider releases. So a consumer that missed a run is still owed its release on the
next one, with no state file, no timestamp comparison and no second traversal.

Four properties follow, and they are what make re-running safe:

- **No orphans.** A contribution survives every run until the dependant releases at a commit containing it.
- **Exactly once.** Once it does, the commit leaves its window and the contribution is gone. No double release.
- **Same version.** A package caught up on run 5 gets the version it was planned at on run 1, since its baseline never
  moved and the bump is a `max()` over the same commits.
- **No widening.** A later run's targets are always a subset of the first run's, so a failed publish can never enlarge
  what a commit releases.

Such a release is labelled a **catch-up** in the plan and reported with the origin's *published* version, because a
package appearing with no commits of its own and no releasing dependency is otherwise baffling to review. Its version
stage receives the provider's already-released version in the `DISPAT_UPDATED_*` variables, so manifests still sync. To
stop a catch-up you no longer want, act on the **consumer**: `cancel(<consumer>)` to drop it, or a hold to defer it.
Acting on the provider does nothing: its version is already public, and cancellation never reaches a published release.

## The pipeline

**Per released package**, up to four stages, each optional to script:

1. **version**: when the package is bumped due to provider updates, and for every releasing package of a space with
   [`autoVersion`](./configuration/spaces.md#autoversion) (native reconciliation checks the baselines too); runs right
   before the build. With
   `isBuildWaitingPublish: true` on the provider's space it waits for that provider's build *and publish*; with
   `false` it waits for the provider's *build* only.
2. **build**: the package's build command. Like every script it may export outputs by appending
   `DISPAT_OUTPUT_NAME=value` (or bare `NAME=value`) lines to the file `$DISPAT_OUTPUT` points at: each value travels to
   every later script of the package as `DISPAT_OUTPUT_<NAME>` (with `DISPAT_OUTPUT_SOURCE_<NAME>` naming the exporter),
   and the `DISPAT_EXPORT_GITHUB` export (absolute file paths) opts the package into a GitHub release with those files
   as assets.
3. **publish**: waits for the package's own build and always for its providers' publishes. A space with a
   `flow.login` authenticates **once per space** before its first publish (every other publish of the space waits for
   it; a login failure fails them all); the login's exports reach every package of the space from its publish onward. On
   success the release recorders run (changelog file, GitHub release for packages that exported
   `DISPAT_EXPORT_GITHUB`), then the annotated tag is created (pushing is left to CI by default).
4. **announce**: after the publish frame, for pushing the release out to update channels (a Slack message, a webhook, a
   docs feed). It gets the release notes as `DISPAT_BREAKING_CHANGES` / `DISPAT_FEATURES` /
   `DISPAT_FIXES`, the same grouped data the changelog and GitHub release render, and the whole frame (its
   `flow.beforeAnnounce` / `flow.postAnnounce` hooks included) only warns on failure: the release is already out.

Every script option accepts a single script name or an array of names run sequentially; a failing command in a
release-gating sequence stops it and fails the package, while warn-only sequences keep running and only log. The stages
can also be bracketed with per-space hooks: `flow.beforeAll`, `flow.beforeVersion`/`flow.postVersion`,
`flow.beforeBuild`/`flow.postBuild` and `flow.beforePublish` all *gate* the release (their failure fails the package),
while `flow.postPublish` only warns since the release is already out. Run-level hooks observe the whole run: a gating
`run.beforeAll` runs once before the task graph (its failure aborts the run), and the warn-only `run.postAll` plus the
commit/push hooks run after, with the outcome exported as
`DISPAT_PUBLISHED/FAILED/SKIPPED/CANCELLED/UNPLANNED_PACKAGES` and per-package `DISPAT_RESULT_*` variables.

Everything released is versioned and tagged, whatever its build produces. An exception there would cost convergence,
because a package whose window never advances reappears in every plan for ever. Packages held by `Release-As: none`
are the one thing that legitimately persists across runs, and they are excluded from the pipeline entirely.

Optionally the run can end with a *finalize phase* (disabled by default): the `commit` option creates one release commit
capturing all published packages' changelog and manifest changes. Tags then point at that commit, GitHub releases move
to the end of the run, and `commit.push` pushes the commit and tags, skipping any tag already on the remote so a
partially pushed run converges (git and GitHub access are verified up front, before any work starts). The phase is
bracketed by the warn-only run hooks
`run.beforeCommit`/`run.afterCommit`, `run.postCommit` (after commit and tags) and `run.beforePush`/`run.afterPush`.

Build and publish have independent concurrency budgets (`concurrency: [build, publish]`); version tasks share the build
budget. A stage without a configured script still runs, keeping ordering, statuses, tags and release records intact; it
just executes no shell command.

Outside the release pipeline, `dispat run <name>` (or just `dispat <name>`) runs the
[script](./configuration/spaces.md#scripts-and-dispat-run) of that name inside each changed package that has one,
honouring the dependency graph within the build concurrency budget (the configured value, or `--concurrency`'s first
value when given; `--on-error` decides whether a failure skips the dependents), with the same `DISPAT_*` environment
and nothing released or tagged. It uses the same three-level `scripts` lookup the stages use, so where you define a
name (the file, a space, or one package) is what decides how far the run reaches.
