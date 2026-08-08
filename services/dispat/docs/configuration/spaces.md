# Spaces

A space is a group of packages sharing build/publish behaviour. Every direct sub-folder of the space's `path` is a
package named after the folder.

## Space options

| Key                     | Type                     | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|--------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string                   | yes        | Folder relative to the root. Every direct sub-folder is a package named after the folder (hidden folders are skipped). Package names must be unique across all spaces.                                                                                                                                                                                                                  |
| `isBuildWaitingPublish` | bool                     | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool                     | no (false) | When `true`, all local changes inside the package folder are rolled back (tracked files restored from HEAD, untracked files removed) if the package fails at any stage, or is skipped after its version stage already modified files.                                                                                                                                                   |
| `flow`                  | object                   | no         | What the space runs at which stage; see the table below.                                                                                                                                                                                                                                                                                                                                |
| `tagFormat`             | string                   | no         | Overrides the repository-wide [`tagFormat`](./versions.md#tagformat) for this space.                                                                                                                                                                                                                                                                                                    |
| `versioning`            | string                   | no         | How versions relate across the space's packages: `independent` (default), `fixed` or `fixedSparse`; see [`versioning`](#versioning).                                                                                                                                                                                                                                                    |
| `runScripts`            | map name → shell command | no         | Named commands for `dispat run <name>`. Values are shell commands themselves, **not** references into `scripts`; see [`runScripts` and `dispat run`](#runscripts-and-dispat-run).                                                                                                                                                                                                       |

## Stages and hooks

The space's `flow` object, keyed by stage or hook name (every entry a script name or an array of names; see the
[sequence rules](./README.md#script-sequences)):

| Key              | Kind    | Description                                                                                                                                                            |
|------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `build`          | stage   | Build stage command(s).                                                                                                                                                |
| `publish`        | stage   | Publish stage command(s).                                                                                                                                              |
| `version`        | stage   | Manifest-sync stage command(s); runs right before the build, only for packages bumped due to provider updates.                                                         |
| `login`          | stage   | Authentication command(s), run **once per space** before the space's first publish; see below.                                                                         |
| `announce`       | stage   | Fourth stage, run after a successful publish: pushing the release out to update channels, with the release-notes variables. The whole frame only **warns**; see below. |
| `beforeAll`      | hook    | Before the package's first stage (its version stage when it has one, its build otherwise). Failure fails the package's release.                                        |
| `beforeVersion`  | hook    | Before the version stage. Failure fails the release.                                                                                                                   |
| `postVersion`    | hook    | After the version stage. Failure fails the release.                                                                                                                    |
| `beforeBuild`    | hook    | Before the build stage. Failure fails the release.                                                                                                                     |
| `postBuild`      | hook    | After the build stage. Failure fails the release.                                                                                                                      |
| `beforePublish`  | hook    | Before the publish stage (after the login). Failure fails the release.                                                                                                 |
| `postPublish`    | hook    | After a successful publish. Failure only **warns**; the release is already out.                                                                                        |
| `beforeAnnounce` | hook    | Before the announce stage. Failure only **warns** and does not stop the announce.                                                                                      |
| `postAnnounce`   | hook    | After the announce stage. Failure only **warns**.                                                                                                                      |
| `onFail`         | outcome | Runs once when the package **fails** at any stage. Warn-only; see below.                                                                                               |
| `onSkip`         | outcome | Runs once when the package is **skipped** because a provider failed. Warn-only; see below.                                                                             |

All script references are optional. A stage without a script still runs (ordering, skip semantics, statuses, tags and
release records are fully preserved); it just executes no shell command. An unconfigured hook is a no-op. Scripts run
through the configured `shell` (default `/bin/sh -c`) with the package folder as the working directory.

The hooks bracket the stages of every package of the space, each with the full stage environment (`DISPAT_STAGE`
carries the hook's name). Everything up to `flow.beforePublish` exists to *gate* the release, so a failure there fails
the package exactly like a failing stage script: the pipeline stops, nothing is published or tagged, `revertOnFail`
applies. `flow.postPublish` and the announce hooks run after the package's status has settled and only warn: failing the
package then would report an unpublished release for a published one. The version hooks share the version stage's skip
rule: when every provider a package was bumped for failed, neither the version script nor its hooks run.

### `flow.login`

Authentication (`npm login`, `docker login`, ...) is a property of the space, not of any one package, so the login runs
**once per space and run**: the space's first publish triggers it, every other publish of the space **waits**
until it finishes, and it is never re-run within the run. Two spaces referencing the *same* script still log in once
each (n spaces, n logins), because credentials and registries belong to the space. A failing login fails the publish of
**every** package in the space (none of them could have succeeded without it); other spaces are unaffected. The login
runs in the **space folder** (the parent of every member package), so a script reading a local file sees the same
folder on every run, and gets the space-scoped environment:
`DISPAT_SPACE`, `DISPAT_STAGE=login`, the [workspace listing](../environment.md#workspace-data) and `DISPAT_OUTPUT`. No
package variables, since which package's publish triggered it is a scheduling accident. What it
[exports](../environment.md#script-outputs) is space-scoped too: every package of the space receives the login's exports
from its publish stage onward, sourced `<space>:login`.

### `flow.announce`

A fourth per-package stage, run after the publish frame completes (publish script, release records, tag,
`flow.postPublish`). Its job is pushing the release out to update channels (a Slack or Discord message, a webhook, a
docs feed), so alongside the full stage environment it is the natural consumer of the
[release-notes variables](../environment.md#release-notes-data) (`DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES`,
`DISPAT_FIXES`) and the channel variables (`DISPAT_CHANNEL`, `DISPAT_OLD_CHANNEL`, `DISPAT_IS_PRERELEASE`) for choosing
where and how to announce. It has the same hook structure as the other stages (`flow.beforeAnnounce` /
`flow.postAnnounce`) but none of their authority: the release is already out, so an error in the stage **or either
hook**
only warns, the package stays published, and no failure among the three sequences stops the others from running. The
frame is skipped entirely when the publish failed; there is nothing to announce.

### `flow.onFail` and `flow.onSkip`

Two outcome scripts, the failure-side counterparts of the announce stage. `flow.onFail` runs once when a package of the
space **fails** at any stage (a failing gating hook, release recorder or tag included), after its status has settled and
after `revertOnFail`'s rollback, so the script sees the folder's final state. `flow.onSkip` runs once when the package
is **skipped** because a provider failed or was skipped. Both observe an outcome that has already happened, so an error
in either only warns; both receive the full package environment (`DISPAT_STAGE` is `onFail` /
`onSkip`) plus the specifics:

| Variable              | Set for  | Meaning                                                 |
|-----------------------|----------|---------------------------------------------------------|
| `DISPAT_FAILED_STAGE` | `onFail` | The stage that failed: `version`, `build` or `publish`. |
| `DISPAT_ERROR`        | `onFail` | The error message of the failing command or operation.  |
| `DISPAT_BLOCKED_BY`   | `onSkip` | The provider whose failure caused the skip.             |

Neither runs for a package that published (that is `flow.postPublish` and the announce frame), and the run-level
[run outcome listing](../environment.md#run-outcome-data) carries the same information for every package at once.

## `versioning`

How the versions of a space's packages relate to each other.

| Value                     | Effect                                                                                                                                                                                                                             |
|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `independent` *(default)* | Every package's version is computed from its own history alone: the behaviour described everywhere else in this documentation.                                                                                                     |
| `fixed`                   | One shared version for the whole space: a change to **any** member (by commit scope or changed files) releases **every** member at the same next version.                                                                          |
| `fixedSparse`             | The shared version is computed exactly like `fixed`, but a member with no changes of its own keeps its previous version and is not released; changed members release at the shared version, aligning to it the moment they change. |

Under `fixed` and `fixedSparse` the space versions **as if it were one package**: the shared next version is computed
over the space's highest baseline with the max bump across all members, the space runs a **single prerelease train**
(a channel directive on one member moves the whole space; a graduation ends the train for all of it), and an exact
`Release-As` naming one member pins the space's single version, with the usual pin guards applied to it. A
`Release-As: none` hold still applies to the member it names: the held member stays behind and catches up when resumed.

Scopes (commit scope-sets and changed files) keep exactly one job in a fixed space: deciding **which changelog entries a
package receives** (and what its GitHub release announces). A member released only because of the shared version gets a
single "no changes" entry instead of borrowing its neighbour's notes, and its presence in the plan is reported as `W210`
(non-suppressible, like the catch-up codes: nothing in the commit log alone explains it). Such a ride is a full release
at the execution level: its version/build/publish scripts, hooks, tag and records all run.

Two convergence properties are worth knowing. A fixed space whose members all carry the shared version releases nothing
on a quiet run, exactly like independent packages. And a `fixed` member left *behind* the space's published baseline
(its ride failed in an earlier run, or the space adopted `fixed` with unequal versions) is caught up at exactly that
baseline on the next run (also `W210`), restoring the one-version invariant. `fixedSparse` deliberately never does this,
since staying behind is its point.

Dependency edges stay package-scoped either way: a provider propagating into one member bumps that member (which then
carries its space along under `fixed`), and only the member with provider updates runs a version task.

## `runScripts` and `dispat run`

Each space may define `runScripts`: named shell commands for ad-hoc work over the packages a release *would* touch, like
linting what is about to ship, printing diffs, or smoke-checking artefacts.

```yaml
spaces:
  libs:
    path: packages
    flow: { build: build, publish: publish }
    runScripts:
      lint: "npm run lint"
      preview: "echo \"$DISPAT_PACKAGE -> $DISPAT_NEW_VERSION\""
```

Unlike the stage entries, the values are **shell commands themselves**, not references into `scripts`. `dispat run
<name>` (or the shorthand `dispat <name>`, whenever `<name>` is not a command name) computes the plan and executes the
named script inside each **changed** package (the packages a release would process), **honouring the dependency graph**:
a package's script starts only after every changed provider's finished, and independent packages run concurrently within
the build concurrency budget (`--concurrency`'s first value). Each script gets the package's full
[DISPAT_* environment](../environment.md) (`DISPAT_STAGE` is `run:<name>`), so a script moves freely between a stage and
a run script. A changed package whose space does not define the name completes as a no-op; a name **no** space defines
is an error (running nothing silently is how a typo hides).

The run can also be narrowed to a single package, in two ways. `dispat run <name> <package>` runs the script in exactly
that package (changed or not, with no graph) and errors on an unknown package or on one whose space does not define
the script, because a *targeted* run that runs nothing would be a typo hiding. And the shorthand, invoked from inside a
package's folder (or any subdirectory of it), narrows to that package the same way (config resolution finds the
monorepo root by ascending parent directories, so `cd packages/core && dispat lint` just works), while from the monorepo
top it covers every changed package as usual. The shorthand takes no package argument.

A third selection axis is `--since <rev>` (`-s`): instead of the release window, select the packages **the commits in
`rev..HEAD` address**: `-s HEAD~1` runs the script over what the last commit addressed (per-commit CI),
`-s origin/main` over this branch's own commits (PR pipelines; the base moving on does not widen the set),
`-s <tag>` since a release, and the reserved `-s all` selects every package. Selection follows the same scope semantics
as planning: a commit's written scopes are authoritative (globs, exclusions and `nonPackageScopes`
included), and only a unit with **no scope-set falls back to the files it changed** (§6.2, longest path prefix).
Ordering, concurrency and output carrying apply to the selected set exactly as to the changed one. `--since` is mutually
exclusive with an explicit `<package>` and overrides the shorthand's folder inference.

What a failure does is the `--on-error` flag: under `skip` (the default) the failed package's changed dependents are
skipped, transitively (the same shape a release gives a failed provider), while independent packages keep running; under
`continue` the dependents run anyway. Any failure makes the command exit `1` either way. Nothing is released, tagged or
written. Names are matched case-insensitively (viper lowercases map keys).

Run scripts take part in [script outputs](../environment.md#script-outputs) too, with one extra rule: outputs carry
**across packages, down the dependency graph**. Each run script gets `$DISPAT_OUTPUT` to export through, and a package's
script receives the exports of its changed providers' scripts (transitively; a script-less package in the middle still
carries them through) as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` still naming the original exporter
(`base:run:lint`). Providers merge in name order, the package's own re-export overrides, and under
`--on-error continue` a failed provider's exports still reach its dependents, mirroring what the pipeline's `onFail`
hooks receive.
