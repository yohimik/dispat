# Spaces

A space is a group of packages sharing build/publish behaviour. The space's `path` names one folder or a list of
folders. Every direct sub-folder of every listed folder is a package named after it, unless a
[`.dispatexclude`](#dispatexclude) file in that folder excludes it. Configure single-package exceptions through a
top-level [`packages` entry](./packages.md). You do not need to carve the package out into a space of its own. Declare
a package living outside every space through a [standalone entry](./packages.md#standalone-packages-path).

## Space options

| Key                     | Type                     | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|--------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string or `[string, ...]` | yes        | One folder, or a list of folders, relative to the root. Every direct sub-folder of every listed folder is a package named after it (hidden folders are skipped, and [`.dispatexclude`](#dispatexclude) excludes more). Package names must be unique across all spaces and across the folders of one space. Listed folders must not repeat or contain one another. The first folder is the space's primary one. The [login script](#flowlogin) runs there, and [`dispat exec --in space:`](../cli/exec.md) resolves there.                                                    |
| `isBuildWaitingPublish` | bool                     | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool                     | no (false) | When `true`, dispat rolls back all local changes inside the package folder if the package fails at any stage, or is skipped after its version stage already modified files. Tracked files are restored from HEAD, and untracked files are removed.                                                                                                                                                   |
| `flow`                  | object                   | no         | What the space runs at which stage. See the table below.                                                                                                                                                                                                                                                                                                                                |
| `tagFormat`             | string                   | no         | Overrides the repository-wide [`tagFormat`](./versions.md#tagformat) for this space.                                                                                                                                                                                                                                                                                                    |
| `versioning`            | string                   | no         | How much of the version the space's packages hold in common. Valid values are `independent` (default), `fixed`, `fixedSparse`, `fixedMajorMinor`, `fixedMajorMinorSparse`, `fixedMajor`, `fixedMajorSparse` or `none`. See [`versioning`](#versioning). This is mutually exclusive with `versionGroup`.                                                                                                                  |
| `versionGroup`          | string                   | no         | Joins the space's packages to a shared-versioning group by name. This names a top-level [`versionGroups`](#versioning-groups) entry, or another space whose own versioning is shared. The group's versioning mode is authoritative, so a space naming one must not set `versioning` itself.                                                                                                        |
| `scripts`               | map name → command or `[command, ...]` | no         | Named commands for this space's packages, sitting on top of the file's own [`scripts`](./README.md#top-level-options). A name binds one command or an array of them run in order. `flow` entries name them, and so does `dispat run <name>`. See [`scripts` and `dispat run`](#scripts-and-dispat-run).                                                                                                                                             |
| `autoVersion`           | object                   | no         | Native manifest rewriting at the version stage. dispat itself reconciles declared workspace ranges and the package's own version, in every [manifest format it reads](../editing/manifests.md#supported-formats), before any `flow.version` script. Absent means off. See [`autoVersion`](./autoversion.md).                                                                                 |
| `env`                   | map name → value         | no         | Fixed environment variables for every script of the space's packages, its login script included. These merge over the top-level map key by key. See [Static env](./env.md).                                                                                                                                                                                                     |
| `custom`                | object                   | no         | Free-form data dispat never reads. See [`custom`](./custom.md).                                                                                                                                                                                                                                                                                                                   |
| `changelog`             | object                   | no         | Changelog options for this space's packages. These overlay the top-level object field by field, and a package's own overlay sits on top. See [`changelog`](./records.md#changelog).                                                                                                                            |
| `github`                | object                   | no         | GitHub release options for this space's packages, overlaying the top-level object field by field. See [`github`](./records.md#github).                                                                                                                                                                  |
| `src`                   | string                   | no         | Scope folder for this space's packages, resolved against each package's own folder. See [What counts as a change](./change-scope.md#src-only-this-folder-is-the-package).                                                                                                                              |
| `ignore`                | array of strings         | no         | Change-scope ignore patterns for this space's packages, added to the repository's. See [What counts as a change](./change-scope.md#ignore-everything-except-these).                                                                                                                                    |
| `concurrency`           | int or `[int, int]`      | no         | Stage-budget **weight** for this space's packages. This has the same meaning as a package's own weight and not the top-level budget. See [Package weights](./packages.md#package-weights-concurrency).                                                                                                                  |
| `dependencies`          | map consumer → providers | no         | Consumer → provider edges written next to the space they describe, in the same shape as the top-level [`dependencies`](./dependencies.md). Every edge must touch this space. See [the space's `dependencies`](#the-spaces-dependencies).                                                                                                                                          |
| `packages`              | map name → entry         | no         | Per-package configuration for this space's own packages, in the same entry shape as the top-level [`packages`](./packages.md) map. See [the space's `packages` map](#the-spaces-packages-map).                                                                                                                                                                                          |

You can write every option except `path`, `packages` and `dependencies` at the top level. That value becomes the
default for every space. See [Where a setting can live](./README.md#where-a-setting-can-live). A space inherits the
top-level value unless it states its own.

Write a single package's departures from these options in a `packages` map. Never put them in the space's own keys. You
can use either this space's map or the top-level one. Configure the space itself from inside its folder by creating
[the space configuration file](#the-space-configuration-file).

## Stages and hooks

The space's `flow` object configures stages and hooks. Key it by stage or hook name, and pass a script name or an array
of names. See the [sequence rules](./scripts.md). dispat looks up each name against the package the stage is running
for. It checks that package's `scripts`, then the space's, then the file's. Writing `flow.build: build` can mean a
single shared command or a different command per package, depending on where you write `build`. See
[`scripts` and `dispat run`](#scripts-and-dispat-run).

A releasing package runs the stages below from top to bottom. Two of them are not scripts at all. They appear here
because you need them to understand the ordering.

| # | Key | Kind | Runs |
|---|-----|------|------|
| 1 | `beforeAll` | hook | Before the package's first stage, whichever that is. This is its version stage when it has one, or its build otherwise. Fails the package's release. |
| 2 | `beforeVersion` | hook | Before the version stage. Fails the release. |
| 3 | *(native reconciliation)* | - | Not a script. When the space sets [`autoVersion`](./autoversion.md), dispat rewrites the manifests itself here, before any `version` script. |
| 4 | `version` | stage | Manifest-sync stage command(s). Runs for every package that picks a version up from a provider moving in this run, and for every releasing package when the space has [`autoVersion`](./autoversion.md). |
| 5 | `postVersion` | hook | After the version stage. Fails the release. |
| 6 | `autoVersion.syncLock` | stage | Lock-file regeneration (`npm install`), between the version and the build. Lives on [`autoVersion`](./autoversion.md), not on `flow`. Runs only where a manifest actually changed. |
| 7 | `beforeBuild` | hook | Before the build stage. Fails the release. |
| 8 | `build` | stage | Build stage command(s). |
| 9 | `postBuild` | hook | After the build stage. Fails the release. |
| 10 | `login` | stage | Authentication command(s). Runs once **per space**, before that space's first publish. Every other publish of the space waits on it. See [`flow.login`](#flowlogin). |
| 11 | `beforePublish` | hook | Before the publish stage, after the login. The last **hook** that can still stop a release. Fails the release. |
| 12 | `publish` | stage | Publish stage command(s). Still gating. A publish script that exits non-zero has not released the package, so it fails exactly like a failed build and nothing below runs. |
| 13 | *(records and tag)* | - | Not a script. Writes the changelog entry, the GitHub release and the annotated tag. Reached only once the publish **succeeded**, which is the point of no return. From here nothing can fail the package. |
| 14 | `postPublish` | hook | After a successful publish. Only **warns**. |
| 15 | `beforeAnnounce` | hook | Before the announce stage. Only **warns**, and does not stop the announce. |
| 16 | `announce` | stage | Pushing the release out to update channels, with the release-notes variables. Only **warns**. |
| 17 | `postAnnounce` | hook | After the announce stage. Only **warns**. |
| - | `onFail` | outcome | Instead of the rest. Runs once when the package **fails** at any stage above, in the folder's final state (after `revertOnFail`). Warn-only. See below. |
| - | `onSkip` | outcome | Instead of the rest. Runs once when the package is **skipped** because a provider failed. Warn-only. See below. |

Steps 1 to 12 are the **gating** half. A failure anywhere in them fails the package. Nothing is tagged or recorded,
`revertOnFail` applies, and `onFail` runs instead of the rest. The publish stage itself is part of that half. A publish
script that exits non-zero has *not* released the package, so the run treats it exactly like a failed build.

The line falls between 12 and 13. Once the publish script has **succeeded**, the artefact is on its registry. No later
failure can take it back, so 14 to 17 only warn. Every one of them runs even after an earlier one failed. Step 13 makes
the release irreversible. A tag or a record that cannot be written there is reported as a
[critical](../internals/architecture.md#after-the-point-of-no-return) and the package stays published. That split is
the whole reason there are two kinds of hook.

All script references are optional. A stage without a script still runs and preserves ordering, skip semantics,
statuses, tags and release records. It just executes no shell command. An unconfigured hook is a no-op. Scripts run
through the configured `shell` (default `/bin/sh -c`) with the package folder as the working directory.

The hooks bracket the stages of every package of the space. Each hook gets the full stage environment, and
`DISPAT_STAGE` carries the hook's name. Everything up to `flow.beforePublish` exists to *gate* the release. A failure
there fails the package exactly like a failing stage script. The pipeline stops, nothing is published or tagged, and
`revertOnFail` applies. `flow.postPublish` and the announce hooks run after the package's status has settled and only
warn. Failing the package then would report an unpublished release for a published one. The version hooks share the
version stage's skip rule. When a package had providers to pick a version up from and every one of them failed, neither
the version script nor its hooks run.

### `flow.login`

Authentication is a property of the space rather than of any one package. The login runs **once per space and run**.
The space's first publish triggers it. Every other publish of the space waits until it finishes, and it is never re-run
within the run. Two spaces referencing the same script still log in once each, because credentials and registries
belong to the space.

A failing login fails the publish of every package in the space, because none of them could succeed without it. Other
spaces are unaffected.

The login runs in the space's primary folder, the first entry of its `path`. A script reading a local file sees the
same folder on every run, wherever the triggering package lives. It gets the space-scoped environment: `DISPAT_SPACE`,
`DISPAT_STAGE=login`, the [workspace listing](../reference/environment.md#workspace-data) and `DISPAT_OUTPUT`. There
are no package variables, because the triggering package is a scheduling accident.

What it [exports](../reference/environment.md#script-outputs) is space-scoped too. Every package of the space receives
the login's exports from its publish stage onward, sourced `<space>:login`.

### `flow.announce`

This is a fourth per-package stage. It runs after the publish frame completes (publish script, release records, tag,
`flow.postPublish`). Use it to push the release out to update channels like a Slack message, a webhook, or a docs feed.

This stage is the natural consumer of the release-notes and channel variables. It gets them alongside the full stage
environment. The [release-notes variables](../reference/environment.md#release-notes-data) are
`DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES` and `DISPAT_FIXES`. The channel variables are `DISPAT_CHANNEL`,
`DISPAT_OLD_CHANNEL` and `DISPAT_IS_PRERELEASE`. Read them to decide where and how to announce.

It has the same hook structure as the other stages (`flow.beforeAnnounce` / `flow.postAnnounce`) but none of their
authority. The release is already out. An error in the stage or either hook only warns, and the package stays
published. No failure among the three sequences stops the others from running. dispat skips the frame entirely when the
publish failed, because there is nothing to announce.

### `flow.onFail` and `flow.onSkip`

These two outcome scripts are the failure-side counterparts of the announce stage. `flow.onFail` runs once when a
package of the space fails at any stage. This includes a failing gating hook, release recorder or tag. It runs after
the package status has settled and after `revertOnFail`'s rollback, so the script sees the folder's final state.
`flow.onSkip` runs once when the package is skipped because a provider failed or was skipped. Both observe an outcome
that has already happened, so an error in either only warns. Both receive the full package environment (`DISPAT_STAGE`
is `onFail` / `onSkip`) plus the specifics:

| Variable              | Set for  | Meaning                                                 |
|-----------------------|----------|---------------------------------------------------------|
| `DISPAT_FAILED_STAGE` | `onFail` | The stage that failed: `version`, `build` or `publish`. |
| `DISPAT_ERROR`        | `onFail` | The error message of the failing command or operation.  |
| `DISPAT_BLOCKED_BY`   | `onSkip` | The provider whose failure caused the skip.             |

Neither runs for a package that published. That job belongs to `flow.postPublish` and the announce frame. The run-level
[run outcome listing](../reference/environment.md#run-outcome-data) carries the same information for every package at
once.

## `versioning`

This key decides how the versions of a space's packages relate to each other. Two axes decide it. The first is how much
of the version the group holds in common. The second is what happens to a member that has nothing of its own to release
when the shared part moves. The walkthrough with worked examples is
[Shared versions](../reference/releasing/versioning.md). This section is the reference.

| Value                     | Shares          | Effect                                                                                                                                                                                                                             |
|---------------------------|-----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `independent` *(default)* | nothing         | Every package's version is computed from its own history alone. This is the behaviour described everywhere else in this documentation.                                                                                                     |
| `fixed`                   | the whole version | One shared version for the whole space. A change to any member (by commit scope or changed files) releases every member at the same next version.                                                                                  |
| `fixedSparse`             | the whole version | The shared version is computed exactly like `fixed`. A member with no changes of its own keeps its previous version and is not released. Changed members release at the shared version, aligning to it the moment they change. |
| `fixedMajorMinor`         | `MAJOR.MINOR`   | Patch releases stay each package's own and move nobody else. A minor or major release moves the whole group to one shared next version, riding unchanged members as `fixed` does.                                                  |
| `fixedMajorMinorSparse`   | `MAJOR.MINOR`   | The same, with `fixedSparse` assignment. An unchanged member keeps its previous version and adopts the shared major and minor on its own next release.                                                                             |
| `fixedMajor`              | `MAJOR`         | Minor and patch releases stay each package's own. Only a major release moves the whole group, riding unchanged members as `fixed` does.                                                                                            |
| `fixedMajorSparse`        | `MAJOR`         | The same, with `fixedSparse` assignment. An unchanged member keeps its previous version and adopts the shared major on its own next release.                                                                                       |
| `none`                    | not applicable  | The space's packages are never released. They have no versions, tags, changelogs or publishes. They exist to run scripts, and a changed package sits in the default [`dispat run`](../cli/run.md) window. See [Packages that never release](../reference/releasing/versioning.md#packages-that-never-release-none). |

A `none` space cannot join or form a versioning group. A releasable package cannot declare a dependency on a `none`
package. Such a provider never has a version for auto-versioning to write, so dispat refuses the configuration at load.
The reverse direction is fine. A `none` package may depend on releasable packages, for example through a permanent
[local link](../editing/manifests.md). Release-only settings on a `none` space load without error and never take
effect. These include `tagFormat`, `aliasTags`, publish stages, changelog and GitHub blocks.

A space with shared versioning is a versioning group whose name is the space's own. Read about the general mechanism
under [Versioning groups](#versioning-groups). That section covers groups that cut across the filesystem. Everything
there holds for any group, whichever way its members joined.

Group behaviour requires a full page to explain. [Shared versions](../reference/releasing/versioning.md) works through
what moves a group and what stays a single package's business. It explains how the shared version is computed. It shows
what a prerelease train and an exact `Release-As` do to a group, how a sparse member rejoins, and what the log shows.

Two things belong here because they are about the space rather than the group. A ride is a **full release at the
execution level**. The member's version, build and publish scripts, its hooks, its tag and its records all run. Only
its changelog entry says it had no changes of its own. Dependency edges stay **package-scoped** whichever mode is in
force. A provider propagating into one member bumps that member. That member then carries its group along if the bump
reaches the shared part. Only the member with provider updates runs a version task.

## Versioning groups

Version relationships do not always follow the filesystem. Two folders that build differently might live in different
spaces but still want one version. One space might hold packages of several release cadences. The top-level
`versionGroups` map declares such groups by name. Spaces and packages join a group by naming it in their `versionGroup`
key.

```json
{
  "versionGroups": {
    "platform": {
      "versioning": "fixed"
    }
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "versionGroup": "platform"
    },
    "apps": {
      "path": "apps"
    }
  },
  "packages": {
    "shell": {
      "versionGroup": "platform"
    }
  }
}
```

Here every package of `libs` plus the single `shell` package version together as group `platform`. The rest of `apps`
stays independent.

A declaration carries one key: `versioning`. You can set this to any of the shared modes. `independent` is invalid
there, because a group exists to share versions. The declaration's mode is authoritative for everyone who joins. This
makes `versionGroup` and `versioning` mutually exclusive on the same space or package. A member cannot contradict its
group. A declared group nobody joins is inert configuration, like a disabled block.

A group shares the version, not its spelling. Each member keeps its own [`tagFormat`](./versions.md#tagformat) and
[alias tags](./alias-tags.md). One group release might tag `lib1-v1.2.0` in one space and `app1@1.2.0` in another.

The joining rules:

- Set `versionGroup` on a space to join all its packages. Set it on a single package through a top-level
  [`packages` entry](./packages.md) or an [in-folder config file](./packages.md#in-folder-configuration-files). The
  [most local layer wins](./packages.md#package-options).
- The reference may also name another space whose own `versioning` is shared. This joins that space's implicit group. A
  space that joined a declared group has no group of its own to reference. You must name the declared group directly.
  Group and space names share one namespace, so dispat rejects a declaration shadowing a space name. An unknown
  reference is an error. This offers the same typo protection as an unknown dependency endpoint.
- A member's assignment mode is its own. A `fixed` space and a `fixedSparse` package can join the same group. dispat
  computes the shared version once. Each member then follows its own mode to ride along or stay behind when unchanged.
  The computation reads every member's published version, sparse ones included. A sparse member can decide where the
  group lands without releasing anything itself. See
  [joining with a versioning of its own](../reference/releasing/versioning.md#joining-with-a-versioning-of-its-own).
- The *depth* belongs to the group, not the member. There is one shared version to compute. Members asking for
  different depths resolve to the deepest any of them asked for. Sharing the major and minor also shares the major. You
  can reach this state by overriding one package's `versioning` without changing its group. dispat reports the
  resolution as `W237`.

Group diagnostics name the group. These include `W234` rides, `W235` competing pins, `W236` channel conflicts, and
`W237` mixed depths. The synthetic package they are raised against is `group:<name>`.

## `scripts` and `dispat run`

A script is a name bound to a shell command, or to several commands run in order. You can write that binding at three
levels. All three use the same `scripts` key. These levels are the config file itself, a space, and a single package.

```yaml
scripts:
  build: "npm run build"
  publish: "npm publish --access public"
  audit: "npm audit --omit=dev"          # every package has this one
  verify:                                # several commands, run in order
    - npm run lint
    - npm run test

spaces:
  libs:
    path: packages
    flow: { build: build, publish: publish }
    scripts:
      lint: "npm run lint"               # only this space's packages have it
      preview: "echo \"$DISPAT_PACKAGE -> $DISPAT_NEW_VERSION\""

packages:
  core:
    scripts:
      lint: "npm run lint -- --strict"   # core's own lint, replacing the space's
      bench: "npm run bench"             # only core has it
```

When dispat needs the commands behind a name, it asks the package that is about to run it. It looks in three places in
order:

1. the package's own `scripts`,
2. its space's `scripts`,
3. the file's `scripts`.

The first hit wins. The lookup happens one name at a time. Redefining `lint` for `core` leaves `audit` and `preview`
exactly as they were. A name is replaced whole. Restating `verify` somewhere creates a new sequence rather than adding
to the existing one. This is the only resolution rule in dispat. Everything that names a script uses it, including
`flow` stages and hooks, `autoVersion.syncLock`, and `dispat run`. See
[One name, several commands](./scripts.md#one-name-several-commands) to understand what an array binding means when it
runs.

### Running one of them: `dispat run`

Run `dispat run <name>` to compute the plan. dispat then runs that script inside every changed package that has one, in
dependency order. `dispat <name>` is the same thing whenever `<name>` is not a command name. Nothing is released or
tagged. A covered package without the name does nothing. A name no level defines is an error, and what a selection that
resolves the name nowhere does depends on whether you named it; read the rule in [The run command](../cli/run.md).

Package selection is a shared subject across commands. This covers which packages run, how `--package` / `--space` /
`--group` and the invocation folder narrow the target, and what `--since` and `--consumers` do to the window. Read
about it in [The run command](../cli/run.md).


## The space's `packages` map

A space can configure its own packages. Key them by folder name:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": { "build": "build", "publish": "publish" },
      "packages": {
        "core": { "revertOnFail": true },
        "reports": { "flow": { "build": "build-reports" } }
      }
    }
  }
}
```

Every entry is exactly a [package entry](./packages.md#package-options). It follows the same merge rules, with two
restrictions based on where it is written:

- **The key must name a folder of this space.** A key matching nothing fails the load. This is the same class of typo
  as an unknown dependency endpoint. dispat refuses a key naming a folder this space [excluded](#dispatexclude) and
  spells the exclusion out. Configure a package of another space through that space or the top-level map.
- **`path` is not allowed.** A space package's location is its folder. Only the top-level map declares a package
  somewhere else, through a [standalone entry](./packages.md#standalone-packages-path).

An entry here and a top-level entry can name the same package. The space's entry is the nearer statement, so it wins
where the two disagree. The top-level entry still supplies what the space leaves unset. Read the full order in
[the override ladder](./packages.md#the-override-ladder).

## The space's `dependencies`

A space declares the edges of its own packages. Key them by consumer exactly as the top-level object does. Every edge
must touch the space. See [Where an edge can be written](./dependencies.md#where-an-edge-can-be-written).


## The space configuration file

A space folder can carry a dispat config file of its own. It uses the same names and formats the root config resolves
through (`dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml`). The first match wins. Its top-level object is the
space. You can write everything the root file's `spaces` entry could say about it, said again and nearer. A space
listing several folders can carry one file in each. They load in the order the folders are listed. A later file
overrides an earlier one's values under the same merge rules.

```json
// packages/dispat.json
{
  "tagFormat": "libs/{name}@{version}",
  "flow": { "build": "build-libs" },
  "packages": {
    "core": { "revertOnFail": true }
  }
}
```

The file replaces what it names and inherits the rest, field by field. This follows the same merge rules a package
override uses. `flow` merges entry by entry, and `scripts` merges name by name. `autoVersion` replaces wholesale. A
boolean written here overrides the root file's value in either direction. This file is only a layer, never a
declaration. The space still has to exist in the root config, because that is where its `path` and its name live.

dispat refuses two keys:

- **`path`**, because the file sits in a space folder. The folders the space spans are already settled. A file able to
  redefine them could point a space somewhere it is not.
- **`spaces`**, because a file declaring spaces is a monorepo root of its own. Leave a nested or vendored repository
  out of the root config rather than half-merging it.

`packages` is the one map key the file may hold. It is a layer of its own. It sits nearer than the space's `packages`
map in the root file, and still under the package's own folder file. You can also write `dependencies` here. It adds to
what the root file's space entry declares rather than replacing it. It follows the same rule: every edge must touch the
space.

Running the CLI from inside such a space keeps working. A space file declares `packages`. A monorepo of standalone
packages declares the same thing, so the two look alike. Resolution settles it by asking the root above whether it
claims the folder. If it does, the file was a space layer and the root is the config. If nothing above claims it, the
folder is a root in its own right.

## `.dispatexclude`

Create a plain-text file listing names in its own folder that dispat must skip **when it looks around**. In a space
folder, those are the direct sub-folders that are not packages. This includes scratch areas, fixtures or a vendored
repository:

```
# not packages
sandbox
tmp-*
```

Write one pattern per line. dispat skips blank lines and `#` comments. `*` matches any run of characters, using the
same glob the scope terms and `autoVersion.match` use. Each space folder carries its own file. An excluded folder is
invisible to discovery. It is never released, and `compute` or `autoVersion` never scan it. Its name is an unknown
scope in commits. dispat rejects a [`packages` entry](./packages.md) naming one and spells the exclusion out.

This file decides **what is a package**. You might want to keep some of a package's own files from counting as changes
to it, without taking the folder away. Use [`.dispatignore` and `ignore`](./change-scope.md) instead.

### Choosing between two config files

The same patterns also hide **config file names**. This is how a folder holding more than one of them says which is
real:

```
# packages/core/.dispatexclude
dispat.json
```

With that file in place, `packages/core/dispat.yaml` is the package's configuration. dispat ignores the generated
`dispat.json` next to it. The rule holds in every folder dispat looks for a config in. It always applies to that folder
alone:

| Folder            | What the exclude file decides                                               |
|-------------------|-----------------------------------------------------------------------------|
| repository root   | Which file is the root config, before the search climbs to the parents.     |
| a space folder    | Which file is the [space configuration file](#the-space-configuration-file). |
| a package folder  | Which file is the package's [in-folder layer](./packages.md#in-folder-configuration-files). |

Hiding every candidate is not an error. The folder simply has nothing to say. A package or space folder falls back to
what it inherits. At the repository root, it means no config was found at all. The error names every name tried.

Watch out for broad patterns. Patterns do not know what they are matching, so a pattern written for a folder can reach
a config file too. `dispat*`, meant for a `dispat-sandbox` folder, also hides `dispat.json`. Name the folders you mean,
or keep the folder patterns specific enough not to collide.

Pass an explicit `--config` to bypass filters. Naming an ignored file loads it.
