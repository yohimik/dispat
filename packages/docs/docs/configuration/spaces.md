# Spaces

A space is a group of packages sharing build/publish behaviour. Every direct sub-folder of the space's `path` is a
package named after the folder, unless a [`.dispatexclude`](#dispatexclude) file in the space folder excludes it. A single
package can depart from its space's configuration through a top-level [`packages` entry](./packages.md), so one-off
exceptions do not require carving the package out into a space of its own. A package living outside every space is
declared through a [standalone entry](./packages.md#standalone-packages-path).

## Space options

| Key                     | Type                     | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|--------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string                   | yes        | Folder relative to the root. Every direct sub-folder is a package named after the folder (hidden folders are skipped, and [`.dispatexclude`](#dispatexclude) excludes more). Package names must be unique across all spaces.                                                                                                                                                              |
| `isBuildWaitingPublish` | bool                     | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool                     | no (false) | When `true`, all local changes inside the package folder are rolled back (tracked files restored from HEAD, untracked files removed) if the package fails at any stage, or is skipped after its version stage already modified files.                                                                                                                                                   |
| `flow`                  | object                   | no         | What the space runs at which stage; see the table below.                                                                                                                                                                                                                                                                                                                                |
| `tagFormat`             | string                   | no         | Overrides the repository-wide [`tagFormat`](./versions.md#tagformat) for this space.                                                                                                                                                                                                                                                                                                    |
| `versioning`            | string                   | no         | How much of the version the space's packages hold in common: `independent` (default), `fixed`, `fixedSparse`, `fixedMajorMinor`, `fixedMajorMinorSparse`, `fixedMajor` or `fixedMajorSparse`; see [`versioning`](#versioning). Mutually exclusive with `versionGroup`.                                                                                                                  |
| `versionGroup`          | string                   | no         | Joins the space's packages to a shared-versioning group by name: a top-level [`versionGroups`](#versioning-groups) entry, or another space whose own versioning is shared. The group's versioning mode is authoritative, so a space naming one must not set `versioning` itself.                                                                                                        |
| `scripts`               | map name → shell command | no         | Named commands for this space's packages, sitting on top of the file's own [`scripts`](./README.md#top-level-options). `flow` entries name them, and so does `dispat run <name>`. See [`scripts` and `dispat run`](#scripts-and-dispat-run).                                                                                                                                             |
| `autoVersion`           | object                   | no         | Native manifest rewriting at the version stage: dispat itself reconciles declared workspace ranges and the package's own version in `package.json` and `go.mod`, before any `flow.version` script. Absent means off; see [`autoVersion`](#autoversion).                                                                                                                                 |
| `env`                   | map name → value         | no         | Fixed environment variables for every script of the space's packages, its login script included, merged over the top-level map key by key; see [Static env](./env.md).                                                                                                                                                                                                     |
| `custom`                | object                   | no         | Free-form data dispat never reads; see [`custom`](./custom.md).                                                                                                                                                                                                                                                                                                                   |
| `changelog`             | object                   | no         | Changelog options for this space's packages, overlaying the top-level object field by field; a package's own overlay sits on top. See [`changelog`](./records.md#changelog).                                                                                                                            |
| `github`                | object                   | no         | GitHub release options for this space's packages, overlaying the top-level object field by field. See [`github`](./records.md#github).                                                                                                                                                                  |
| `src`                   | string                   | no         | Scope folder for this space's packages, resolved against each package's own folder. See [What counts as a change](./change-scope.md#src-only-this-folder-is-the-package).                                                                                                                              |
| `ignore`                | array of strings         | no         | Change-scope ignore patterns for this space's packages, added to the repository's. See [What counts as a change](./change-scope.md#ignore-everything-except-these).                                                                                                                                    |
| `concurrency`           | int or `[int, int]`      | no         | Stage-budget **weight** for this space's packages, the same meaning as a package's own and not the top-level budget. See [Package weights](./packages.md#package-weights-concurrency).                                                                                                                  |
| `dependencies`          | map consumer → providers | no         | Consumer → provider edges written next to the space they describe, in the same shape as the top-level [`dependencies`](./dependencies.md). Every edge must touch this space. See [the space's `dependencies`](#the-spaces-dependencies).                                                                                                                                          |
| `packages`              | map name → entry         | no         | Per-package configuration for this space's own packages, in the same entry shape as the top-level [`packages`](./packages.md) map. See [the space's `packages` map](#the-spaces-packages-map).                                                                                                                                                                                          |

Every one of these except `path`, `packages` and `dependencies` can also be written at the top level, where it
becomes the default for every space; see [Where a setting can live](./README.md#where-a-setting-can-live). A space
that states nothing inherits it, and one that states something replaces it.

A single package's departures from these options live in a `packages` map, never in the space's own keys: either this
space's map or the top-level one. The space itself can also be configured from inside its folder; see
[the space configuration file](#the-space-configuration-file).

## Stages and hooks

The space's `flow` object, keyed by stage or hook name (every entry a script name or an array of names; see the
[sequence rules](./scripts.md)). Each name is looked up against the package the stage is running for,
first in that package's `scripts`, then the space's, then the file's. So one `flow.build: build` can mean a single
shared command, or a different command per package, depending on where you write `build`. See
[`scripts` and `dispat run`](#scripts-and-dispat-run):

| Key              | Kind    | Description                                                                                                                                                                                  |
|------------------|---------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `build`          | stage   | Build stage command(s).                                                                                                                                                                      |
| `publish`        | stage   | Publish stage command(s).                                                                                                                                                                    |
| `version`        | stage   | Manifest-sync stage command(s); runs right before the build, for packages bumped due to provider updates (and for every releasing package when the space has [`autoVersion`](#autoversion)). |
| `login`          | stage   | Authentication command(s), run once per space before the space's first publish; see below.                                                                                                   |
| `announce`       | stage   | Fourth stage, run after a successful publish: pushing the release out to update channels, with the release-notes variables. The whole frame only **warns**; see below.                       |
| `beforeAll`      | hook    | Before the package's first stage (its version stage when it has one, its build otherwise). Failure fails the package's release.                                                              |
| `beforeVersion`  | hook    | Before the version stage. Failure fails the release.                                                                                                                                         |
| `postVersion`    | hook    | After the version stage. Failure fails the release.                                                                                                                                          |
| `beforeBuild`    | hook    | Before the build stage. Failure fails the release.                                                                                                                                           |
| `postBuild`      | hook    | After the build stage. Failure fails the release.                                                                                                                                            |
| `beforePublish`  | hook    | Before the publish stage (after the login). Failure fails the release.                                                                                                                       |
| `postPublish`    | hook    | After a successful publish. Failure only **warns**; the release is already out.                                                                                                              |
| `beforeAnnounce` | hook    | Before the announce stage. Failure only **warns** and does not stop the announce.                                                                                                            |
| `postAnnounce`   | hook    | After the announce stage. Failure only **warns**.                                                                                                                                            |
| `onFail`         | outcome | Runs once when the package **fails** at any stage. Warn-only; see below.                                                                                                                     |
| `onSkip`         | outcome | Runs once when the package is **skipped** because a provider failed. Warn-only; see below.                                                                                                   |

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
**once per space and run**: the space's first publish triggers it, every other publish of the space waits until it
finishes, and it is never re-run within the run. Two spaces referencing the same script still log in once each (n
spaces, n logins), because credentials and registries belong to the space. A failing login fails the publish of every
package in the space (none of them could have succeeded without it); other spaces are unaffected. The login runs in the
space folder (the parent of every member package), so a script reading a local file sees the same folder on every run,
and gets the space-scoped environment:
`DISPAT_SPACE`, `DISPAT_STAGE=login`, the [workspace listing](../reference/environment.md#workspace-data) and `DISPAT_OUTPUT`. No
package variables, since which package's publish triggered it is a scheduling accident. What it
[exports](../reference/environment.md#script-outputs) is space-scoped too: every package of the space receives the login's exports
from its publish stage onward, sourced `<space>:login`.

### `flow.announce`

A fourth per-package stage, run after the publish frame completes (publish script, release records, tag,
`flow.postPublish`). Its job is pushing the release out to update channels (a Slack or Discord message, a webhook, a
docs feed), so alongside the full stage environment it is the natural consumer of the
[release-notes variables](../reference/environment.md#release-notes-data) (`DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES`,
`DISPAT_FIXES`) and the channel variables (`DISPAT_CHANNEL`, `DISPAT_OLD_CHANNEL`, `DISPAT_IS_PRERELEASE`) for choosing
where and how to announce. It has the same hook structure as the other stages (`flow.beforeAnnounce` /
`flow.postAnnounce`) but none of their authority: the release is already out, so an error in the stage or either hook
only warns, the package stays published, and no failure among the three sequences stops the others from running. The
frame is skipped entirely when the publish failed; there is nothing to announce.

### `flow.onFail` and `flow.onSkip`

Two outcome scripts, the failure-side counterparts of the announce stage. `flow.onFail` runs once when a package of the
space fails at any stage (a failing gating hook, release recorder or tag included), after its status has settled and
after `revertOnFail`'s rollback, so the script sees the folder's final state. `flow.onSkip` runs once when the package
is skipped because a provider failed or was skipped. Both observe an outcome that has already happened, so an error in
either only warns; both receive the full package environment (`DISPAT_STAGE` is `onFail` /
`onSkip`) plus the specifics:

| Variable              | Set for  | Meaning                                                 |
|-----------------------|----------|---------------------------------------------------------|
| `DISPAT_FAILED_STAGE` | `onFail` | The stage that failed: `version`, `build` or `publish`. |
| `DISPAT_ERROR`        | `onFail` | The error message of the failing command or operation.  |
| `DISPAT_BLOCKED_BY`   | `onSkip` | The provider whose failure caused the skip.             |

Neither runs for a package that published (that is `flow.postPublish` and the announce frame), and the run-level
[run outcome listing](../reference/environment.md#run-outcome-data) carries the same information for every package at once.

## `versioning`

How the versions of a space's packages relate to each other. Two axes decide it: how much of the version the group
holds in common, and what happens to a member that has nothing of its own to release when the shared part moves. The
walkthrough with worked examples is [Shared versions](../releasing/versioning.md); this is the reference.

| Value                     | Shares          | Effect                                                                                                                                                                                                                             |
|---------------------------|-----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `independent` *(default)* | nothing         | Every package's version is computed from its own history alone: the behaviour described everywhere else in this documentation.                                                                                                     |
| `fixed`                   | the whole version | One shared version for the whole space: a change to any member (by commit scope or changed files) releases every member at the same next version.                                                                                  |
| `fixedSparse`             | the whole version | The shared version is computed exactly like `fixed`, but a member with no changes of its own keeps its previous version and is not released; changed members release at the shared version, aligning to it the moment they change. |
| `fixedMajorMinor`         | `MAJOR.MINOR`   | Patch releases stay each package's own and move nobody else; a minor or major release moves the whole group to one shared next version, riding unchanged members as `fixed` does.                                                  |
| `fixedMajorMinorSparse`   | `MAJOR.MINOR`   | The same, with `fixedSparse` assignment: an unchanged member keeps its previous version and adopts the shared major and minor on its own next release.                                                                             |
| `fixedMajor`              | `MAJOR`         | Minor and patch releases stay each package's own; only a major release moves the whole group, riding unchanged members as `fixed` does.                                                                                            |
| `fixedMajorSparse`        | `MAJOR`         | The same, with `fixedSparse` assignment: an unchanged member keeps its previous version and adopts the shared major on its own next release.                                                                                       |

A space with shared versioning is a versioning group whose name is the space's own. The general mechanism, groups that
cut across the filesystem included, is described under [Versioning groups](#versioning-groups). Everything below holds
for any group, whichever way its members joined.

**When the group moves.** A release moves the whole group when it reaches the part the group shares, and belongs to
one package alone when it stays below it. Under `fixed` every release reaches the shared part, since the whole version
is shared; under `fixedMajorMinor` a patch does not; under `fixedMajor` neither a patch nor a minor does. A member
releasing on its own always keeps the shared part it already carries, and a member joining a moved group starts the
components below the shared part at zero, so a `fixedMajor` group reaching major 2 lands every member on `2.0.0`.

**How the group version is computed.** When the shared part moves, the group versions **as if it were one package**:
over the group's highest baseline, with the max bump across all members. It runs a single prerelease train (a channel
directive on one member moves the whole group; a graduation ends the train for all of it), and an exact `Release-As`
naming one member pins the group's version, with the usual pin guards applied to it. Under a partial mode both of those
are scoped by the same rule as everything else: a train started by a bump that reaches the shared part is the group's,
a train below it is one package's, and a pin naming a different shared part moves the group while a pin inside the
current one applies to the package that wrote it, with its own guards rather than the group's. A `Release-As: none`
hold always applies to the member it names: the held member stays behind and catches up when resumed.

Scopes (commit scope-sets and changed files) keep exactly one job in a versioning group: deciding which changelog
entries a package receives (and what its GitHub release announces). A member released only because of the shared version
gets a single "no changes" entry instead of borrowing its neighbour's notes, and that entry names what the group holds
in common ("on one major version" under `fixedMajor`, "on one version" under `fixed`). Its presence in the plan is
reported as `W210` (non-suppressible, like the catch-up codes: nothing in the commit log alone explains it). Such a ride
is a full release at the execution level: its version/build/publish scripts, hooks, tag and records all run.

Two convergence properties are worth knowing. A group whose members all agree on the shared part releases nothing on a
quiet run, exactly like independent packages. And a non-sparse member left *behind* the group's shared part (its ride
failed in an earlier run, or the group formed with unequal versions) is caught up on the next run (also `W210`),
restoring the invariant: at exactly the group's published version under `fixed`, at the start of its own line under a
partial mode. The sparse modes deliberately never do this, since staying behind is their point.

Dependency edges stay package-scoped whichever mode is in force: a provider propagating into one member bumps that
member (which then carries its group along, if the bump reaches the shared part), and only the member with provider
updates runs a version task.

## Versioning groups

Version relationships do not always follow the filesystem: two folders that build differently (and therefore live in
different spaces) may still want one version, and one space may hold packages of several release cadences. The top-level
`versionGroups` map declares such groups by name; spaces and packages join a group by naming it in their
`versionGroup` key.

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

Here every package of `libs` plus the single `shell` package version together as group `platform`, while the rest of
`apps` stays independent.

A declaration carries one key: `versioning`, any of the shared modes. `independent` is invalid there, because a group
exists to share versions. The declaration's mode is authoritative for everyone who joins, which is why `versionGroup`
and
`versioning` are mutually exclusive on the same space or package: a member cannot contradict its group. A declared group
nobody joins is inert configuration, like a disabled block.

The joining rules:

- `versionGroup` is settable on a space (all its packages join) and on a single package (through a top-level
  [`packages` entry](./packages.md) or an
  [in-folder config file](./packages.md#in-folder-configuration-files)); the
  [most local layer wins](./packages.md#package-options).
- The reference may also name another space whose own `versioning` is shared, joining that space's implicit group. A
  space that itself joined a declared group has no group of its own to reference; name the declared group directly.
  Group and space names share one namespace (a declaration shadowing a space name is rejected), and an unknown
  reference is an error, the same typo protection as an unknown dependency endpoint.
- A member's assignment mode is its own: a `fixed` space and a `fixedSparse` package can join the same group. The shared
  version is computed once, then each member follows its own mode (ride along, or stay behind when unchanged).
- The *depth* is the group's, not the member's, because there is one shared version to compute. Members asking for
  different depths (a `fixedMajor` package inside a `fixedMajorMinor` group, reachable by overriding one package's
  `versioning` without changing its group) resolve to the deepest any of them asked for, since sharing the major and
  minor also shares the major. The resolution is reported as `W213`.

Group diagnostics (`W210` rides, `W211` competing pins, `W212` channel conflicts, `W213` mixed depths) name the group;
the synthetic package they are raised against is `group:<name>`.

## `autoVersion`

With an `autoVersion` object present, dispat keeps the space's files in sync with the released versions itself. No
`flow.version` script is required; one may still run afterwards and sees the already-reconciled files.

There are two strategies, and they are independent. You can use either, both, or neither.

The **parsing** strategy is the default one described below: dispat scans the package's manifests, matches each declared
dependency against the workspace (by manifest name, by a name the configuration states through
[`manifestNames`](./packages.md#manifestnames), or by a declared local path such as `file:`, a relative `replace` or
`path =`), and rewrites the declaration to the provider's end-of-run version. That is the planned version when the
provider is releasing and has not failed, and its baseline otherwise. `manifests: none` turns it off.

The **replacing** strategy is the `replace` list: literal find-and-write over whatever files its globs select, parsing
nothing, for the versions no manifest holds (a Gradle coordinate, a README example, a CI workflow). It has
[a page of its own](../editing/replacer.md).

A package may use both, in which case its manifests are reconciled first. A block using neither still schedules a
version task, which is how a space asks for [`syncLock`](#autoversion) and nothing else.

The rewrite is byte-precise: only the version text changes, and formatting, key order and comments survive. Writers
exist for `package.json`, `go.mod` and `requirements*.txt` (only the matching line's specifier changes; spelling,
spacing and comments survive). The other ecosystems (Cargo, pyproject, Composer, Maven, .NET, Dart) still feed
`compute` and the graph, but their rewriting is `flow.version`'s job.

Two consequences worth knowing before turning it on. First, the reconciliation rule (§9.4 of the
[commit specification](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md)) covers *every* workspace dependency, including providers released
by earlier runs, so an auto-versioning space runs a version task for **every** releasing package, not only those bumped
by provider updates. Second, a rewriting failure fails the version stage, and
`revertOnFail` rolls the half-edited folder back.

| Key                   | Type             | Default  | Effect                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|-----------------------|------------------|----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`             | bool             | `true`   | Turns the block off without deleting it. The minimal opt-in block is `{"enabled": true}`: a completely empty `{}` object is pruned by the config loader and reads as absent.                                                                                                                                                                                                                                                                                                                       |
| `manifests`           | string           | `root`   | The parsing strategy's scope. `root`: only manifests directly in the package folder; `all`: every manifest found under it (dependency, virtual-env and build-output folders such as `node_modules`, `vendor`, `dist` and `venv`, plus every dot-folder, are never entered); `none`: the parsing strategy is off, leaving `replace` and `syncLock` as the whole of the version stage.                                                                                                              |
| `replace`             | array of objects | none     | The replacing strategy: literal text substitutions applied to the files each rule's globs select, parsing nothing. Each entry takes `files` (globs relative to the package folder), `find` and `write`, all required; `find` and `write` are templates over `{name}`, `{version}`, `{previous}`, `{provider}`, `{providerVersion}` and `{providerPrevious}`, and a rule naming a provider is applied once per provider. See [the replacer](../editing/replacer.md).                                        |
| `kinds`               | array of strings | all four | Restrict rewriting to the named manifest fields (`dependencies`, `devDependencies`, `peerDependencies`, `optionalDependencies`).                                                                                                                                                                                                                                                                                                                                                                   |
| `only`                | array of strings | all      | Restrict rewriting to declarations of the named provider packages; every name must be a discovered package.                                                                                                                                                                                                                                                                                                                                                                                        |
| `nameMatch`           | string           | `exact`  | How a declared name finds its workspace package when no manifest declares that name and no local path matches: `exact` (such declarations are simply not workspace dependencies) or `substring`; see the note below the table.                                                                                                                                                                                                                                                                     |
| `match`               | array of globs   | any      | Rewrite only declared ranges matching one of the globs, e.g. `["workspace:*"]`, so a range pinned by hand is never overridden. `*` matches any run of characters (slashes included, same as scope globs), so `file:../core` is matched by `file:*` and by `*`.                                                                                                                                                                                                                                     |
| `range`               | string           | `caret`  | The write policy: `caret` (`^1.2.3`), `tilde` (`~1.2.3`), `exact` (`1.2.3`), a `{version}` template (`>={version}`), or any other literal written verbatim (`workspace:*`). Ecosystems with their own version spelling override the keywords: `go.mod` always receives exact canonical `vX.Y.Z`, Python files always receive `==X.Y.Z`; templates and literals pass through everywhere.                                                                                                            |
| `writeVersion`        | bool             | `true`   | Also write the package's own new version into its manifest's version field (§12.4). Applies to the package's **root** manifests only: a nested manifest (an example, a fixture) keeps its own version even under `manifests: all`.                                                                                                                                                                                                                                                                 |
| `syncLock`            | array of names   | none     | Script names, resolved per package like every other name (see [`scripts` and `dispat run`](#scripts-and-dispat-run)), run inside the package folder after its files were reconciled and before its build: the slot for `npm install` and friends, so lock files follow the manifests. Skipped for a package whose version stage changed nothing, so a quiet release does not regenerate locks for no reason. A block that configures neither strategy has no such change to key off, so its scripts run every release instead of never: that is how a space asks for lock regeneration alone. A lock file living at the repo root is outside every package folder: list it under [`commit.include`](./records.md#commit) so the release commit carries it. That is optional for npm and Yarn workspaces and unavoidable for pnpm, whose `pnpm-lock.yaml` is always the workspace root's. |
| `syncLockConcurrency` | int              | `1`      | Run-wide cap on simultaneously running `syncLock` scripts. Shared lock files corrupt under parallel writers, hence the serial default; when spaces disagree, the smallest configured value wins.                                                                                                                                                                                                                                                                                                   |

Under `nameMatch: substring`, a declared name whose last `/`- or `:`-separated segment equals a package's folder name
also matches: package `app` matches `@core/app`, `com.acme:app` or a bare `app` line, even when the `app` package has no
parseable manifest of its own. It is opt-in because it can false-positive; a third-party `@types/app` would match a
package named `app`.

```yaml
spaces:
  js:
    path: packages
    autoVersion:
      match: [ "workspace:*" ]      # only ranges the workspace manages
      range: caret                # write ^<new version>
      syncLock: [ npm-install ]     # scripts.npm-install: "npm install"
scripts:
  npm-install: npm install --package-lock-only
  # pnpm's equivalent, resolving into pnpm-lock.yaml without touching
  # node_modules:
  pnpm-lock: pnpm install --lockfile-only
```

The same reconciliation is callable outside a release as
[`dispat autoversion`](../cli/autoversion.md), with flags overriding the block's policy for the invocation
(`--manifests none` turns the parsing strategy off, `--no-replace` skips the rules); a custom flow uses it to reconcile
at the moment it needs, and the version stage later finds nothing left to rewrite.

Three shapes cover most repositories:

```yaml
spaces:
  js:                            # manifests dispat can parse
    path: packages
    autoVersion:
      match: [ "workspace:*" ]
      syncLock: [ npm-install ]

  android:                       # nothing here parses as a manifest
    path: modules
    autoVersion:
      manifests: none
      replace:
        - files: [ "*.gradle" ]
          find:  "com.acme:{provider}:{providerPrevious}"
          write: "com.acme:{provider}:{providerVersion}"

  go:                            # nothing to reconcile, one script to run
    path: services
    autoVersion:
      manifests: none
      syncLock: [ go-mod-tidy ]
```

Four warnings narrate what the rewrite did that the commit log alone cannot explain. `W192`: the manifest's declared own
version disagreed with the baseline (tags are authoritative; the computed version is written over it). `W197`: a range
was caught up to a provider released outside this run (the reconciliation rule's catch-up case). `W203`: a stable
release now ranges over a prerelease provider. And `W221`: a rewritten dependency has no configured `dependencies` edge
behind it, so nothing orders this package after that provider or skips it when the provider fails; the written version
is optimistic about a publish still in flight. `dispat compute` derives the missing edge.

The replacing strategy raises `W197` and `W203` on the same terms, plus one of its own. `W222`: a replace rule found
its text in none of the files it selected, which usually means a mistyped template or a stale glob. Re-running a
release does not trigger it, because dispat checks whether the file already reads the way the rule wants before
deciding the rule is stale.

## `scripts` and `dispat run`

A script is a name bound to a shell command. You can write that binding at three levels, and all three use the same
`scripts` key: the config file itself, a space, and a single package.

```yaml
scripts:
  build: "npm run build"
  publish: "npm publish --access public"
  audit: "npm audit --omit=dev"          # every package has this one

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

When dispat needs the command behind a name, it asks the package that is about to run it, and looks in three places in
order:

1. the package's own `scripts`,
2. its space's `scripts`,
3. the file's `scripts`.

The first hit wins, and the lookup happens one name at a time. Redefining `lint` for `core` therefore leaves `audit`
and `preview` exactly as they were. This is the only resolution rule in dispat, and everything that names a script uses
it: the `flow` stages and hooks, `autoVersion.syncLock`, and `dispat run`.

### What a run covers

`dispat run <name>` computes the plan, then runs that script inside every **changed** package that has one. Changed
means the packages a release would process. The dependency graph is respected: a package's script starts only after
every changed provider's script has finished, and independent packages run side by side within the build concurrency
budget (the first value of `--concurrency`). `dispat <name>` is a shorthand for the same thing whenever `<name>` is not
a command name. [`--package` / `--space` / `--group`](#choosing-the-packages), or the folder you invoke it from, narrow that to
part of the monorepo.

Because the lookup is per package, the level you define a name at is what decides how far the run reaches:

| Where the name is defined | `dispat run <name>` runs it in    |
|---------------------------|-----------------------------------|
| the file                  | every changed package             |
| a space                   | that space's changed packages     |
| a package                 | that package, when it changed     |

That is the whole selection rule. Put `audit` at the top and it sweeps the release; put `bench` on `core` and only
`core` ever runs it.

A changed package that finds no command for the name simply does nothing, which is what lets one script skip the
packages it does not apply to. Two situations are errors instead, because a command that quietly runs nothing is how a
typo survives:

- **no level defines the name at all**, which is almost always a misspelling;
- **the name exists, but none of the selected packages have it**, such as asking for `bench` in a release where `core`
  did not change.

An empty selection is not an error. If nothing changed, there was nothing to look in, and the run succeeds having done
nothing.

Every script receives the package's full [DISPAT_* environment](../reference/environment.md), with `DISPAT_STAGE` set to
`run:<name>`. That is why the same script works as a stage and as a `dispat run` without being rewritten. One caveat on
naming: dispat's own command words (`status`, `commit`, `changelog` and the rest) win the shorthand, so a script called
`commit` is reachable as `dispat run commit` but never as `dispat commit`.

### Choosing the packages

Three flags narrow a run to part of the monorepo: `--package` (`-p`) names packages, `--space` (`-s`) names spaces and
selects every package they hold, `--group` (`-g`) names [versioning groups](#versioning-groups) and selects every
package that versions with the group. All three are repeatable and comma-separated, matched case-insensitively, and
all three accept `*` globs:

```sh
dispat run lint -p core             # one package
dispat run lint -p core,web         # several
dispat run lint -s libs             # every package of a space
dispat run lint -g platform         # every package of a versioning group
dispat run lint -p '@acme/*'        # a glob (quoted: the shell must not expand it)
dispat run lint -p '*'              # every package
```

No word is reserved, so a package named `all` is selected by `all` and by nothing else. A term matching nothing is an
error listing what was discovered — the same reason an unknown script name is one — and the error looks across the
other flags: a space named in `--package`, a group named in `--space`, or a package named in `--group`, says so and
points at the flag that reaches it. A [standalone package](./packages.md#standalone-packages-path) belongs to no
space, so `-p <name>` or `-p '*'` is the only way to name one unless it joined a group; `-s '*'` means every
configured space and leaves it out.

With no terms, the folder you are standing in is the selection. dispat finds the monorepo root by walking up to the
config file, so `cd packages/core && dispat lint` lints `core` and nothing else, and so does any folder below it.
Standing in a space folder — `cd packages && dispat lint` — covers that space; standing at the top of the repository,
or anywhere outside every space, covers the usual set. The deepest match wins, so a standalone package nested inside
another package's folder still selects itself, and a flag on the command line always beats the folder it was typed in.

A group is never inferred from a folder, because it is a versioning relationship rather than a place; `--group` is
the only way to name one.

Every command that acts on a subset of packages reads the same three flags and the same folder rule: `dispat release`,
`status`, `run`, `preview`, `changelog`, `autoversion`, `commit`, `github` and `compute`.

### Windows: `--since` and `--consumers`

The filter narrows; a **window** decides what there is to narrow. By default the window is the release window — the
changed packages. `--since <rev>` replaces it with the packages the commits in `rev..HEAD` address:
`--since HEAD~1` runs the script over what the last commit addressed (per-commit CI), `--since origin/main` over this
branch's own commits (PR pipelines; the base moving on does not widen the set), `--since <tag>` since a release, and
the reserved `--since all` selects every package, changed or not. Selection follows the same scope semantics as
planning: a commit's written scopes are authoritative (globs, exclusions and `nonPackageScopes` included), and only a
unit with no scope-set falls back to the files it changed (longest path prefix; see
[scope sets](../reference/commits.md#scope-sets)). Ordering, concurrency and output carrying apply to the selected set exactly as
to the changed one.

The window comes first and the filter picks from it, which is what makes the two compose:

```sh
dispat run test --since HEAD~1 -s libs    # what the last commit addressed, inside libs
dispat run build --since all -p core      # core, whether or not it changed
dispat run build -p core                  # core, and only if it changed
```

That last line is worth reading twice: a filter never widens a window, so `--since all` is how you reach a package the
window does not cover — the way to try one script by hand under exactly the environment its stage would give it,
without releasing anything.

A window covers only the packages the commits **address**, never the packages a change *affects*:
`dispat run test --since HEAD~1` re-tests the changed provider, not the consumers that depend on it. The `--consumers`
flag closes that gap: it additionally selects every package that **transitively depends** on a selected one; a consumer
pulled in brings its own consumers, all the way down the graph. The expansion happens after the filter and is not
filtered back out — `-p core --consumers` is a request for core's dependents, so it reaches packages the filter never
named. The added packages run whether or not they changed, after their selected providers, and a failing provider's
script skips them under the default `--on-error skip` exactly like any selected dependent.

What a failure does is the `--on-error` flag: under `skip` (the default) the failed package's changed dependents are
skipped, transitively (the same shape a release gives a failed provider), while independent packages keep running; under
`continue` the dependents run anyway. Any failure makes the command exit `1` either way. Nothing is released, tagged or
written. Names are matched case-insensitively (viper lowercases map keys).

`dispat run` takes part in [script outputs](../reference/environment.md#script-outputs) too, with one extra rule: outputs carry
across packages, down the dependency graph. Each script gets `$DISPAT_OUTPUT` to export through, and a package's
script receives the exports of its changed providers' scripts (transitively; a package that resolves nothing still
carries them through) as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` still naming the original exporter
(`base:run:lint`). Providers merge in name order, the package's own re-export overrides, and under
`--on-error continue` a failed provider's exports still reach its dependents, mirroring what the pipeline's `onFail`
hooks receive.

## The space's `packages` map

A space can configure its own packages, keyed by folder name:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": { "build": "build", "publish": "publish" },
      "packages": {
        "core": { "revertOnFail": true },
        "legacy": { "flow": { "build": "build-legacy" } }
      }
    }
  }
}
```

Every entry is exactly a [package entry](./packages.md#package-options) and follows the same merge rules, with two
restrictions that follow from where it is written:

- **The key must name a folder of this space.** A key matching nothing is the same class of typo as an unknown
  dependency endpoint and fails the load; a key naming a folder this space [excluded](#dispatexclude) is refused with the
  exclusion spelled out. A package of another space is configured by that space, or by the top-level map.
- **`path` is not allowed.** A space package's location is its folder. Only the top-level map declares a package
  somewhere else, through a [standalone entry](./packages.md#standalone-packages-path).

An entry here and a top-level entry can name the same package; the space's entry is the nearer statement, so it wins
where the two disagree and the top-level one still supplies what the space's leaves unset. The full order is in
[the override ladder](./packages.md#the-override-ladder).

## The space's `dependencies`

A space declares the edges of its own packages, keyed by consumer exactly as the top-level
[`dependencies`](./dependencies.md) is:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "dependencies": {
        "web": ["core", { "provider": "utils", "keep": true }]
      }
    }
  }
}
```

Every edge here must **touch the space**: its consumer or its provider is one of the space's own packages. That covers
the edges inside a space, and cross-space edges too, which belong to whichever of the two spaces you think of as
owning the relation. An edge between two packages of neither space is refused, because a reader looking for it would
have no space to look in. Those belong in the root object.

Declarations never override each other. The root object, each space's object and each package's own list all merge
into a single graph, so an edge is declared once, wherever it reads best.

`dispat compute` edits each edge where it was written, so a correction to an edge declared here is applied here. New
edges it discovers go to the root object instead: whether a space may hold a given edge is a rule about the graph,
and compute leaves that decision to you.

## The space configuration file

A space folder may carry a dispat config file of its own, under the same names and formats the root config resolves
through (`dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml`, first match wins). Its top-level object is the
space: everything the root file's `spaces` entry could say about it, said again and nearer.

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

The file replaces what it names and inherits the rest, field by field, under the same merge rules a package override
uses: `flow` merges entry by entry, `scripts` name by name, `autoVersion` replaces wholesale, and a boolean written
here overrides the root file's value in either direction. It is only a layer, never a declaration: the space still has
to exist in the root config, because that is where its `path` and its name live.

Two keys are refused:

- **`path`**, because the file sits in the space folder, so the folder it is in already is the path. A file able to
  redefine it could point a space somewhere it is not.
- **`spaces`**, because a file declaring spaces is a monorepo root of its own. A nested or vendored repository is left
  out of the root config rather than half-merged.

`packages` is the one map key the file may hold, and it is a layer of its own: nearer than the space's `packages` map
in the root file, and still under the package's own folder file. `dependencies` may also be written here, and it adds
to what the root file's space entry declares rather than replacing it, under the same rule: every edge must touch the
space.

Running the CLI from inside such a space keeps working. A space file declares `packages`, which is also what a monorepo
of standalone packages declares, so resolution asks the root above whether it claims the folder: if it does, the file
was a space layer and the root is the config; if nothing above claims it, the folder is a root in its own right.

## `.dispatexclude`

A plain-text file listing names in its own folder that dispat must skip **when it looks around**. In a space folder
those are the direct sub-folders that are not packages, such as scratch areas, fixtures or a vendored repository:

```
# not packages
sandbox
tmp-*
```

One pattern per line; blank lines and `#` comments are skipped, and `*` matches any run of characters (the same glob the
scope terms and `autoVersion.match` use). Each space folder carries its own file. An excluded folder is invisible to
discovery (never released, never scanned by `compute` or `autoVersion`, its name an unknown scope in commits), and a
[`packages` entry](./packages.md) naming one is rejected with the exclusion spelled out.

This file decides **what is a package**. To keep some of a package's own files from counting as changes to it, without
taking the folder away, use [`.dispatignore` and `ignore`](./change-scope.md) instead.

### Choosing between two config files

The same patterns also hide **config file names**, which is how a folder holding more than one of them says which is
real:

```
# packages/core/.dispatexclude
dispat.json
```

With that file in place, `packages/core/dispat.yaml` is the package's configuration and the generated `dispat.json`
next to it is ignored. The rule holds in every folder dispat looks for a config in, and always applies to that folder
alone:

| Folder            | What the exclude file decides                                               |
|-------------------|-----------------------------------------------------------------------------|
| repository root   | Which file is the root config, before the search climbs to the parents.     |
| a space folder    | Which file is the [space configuration file](#the-space-configuration-file). |
| a package folder  | Which file is the package's [in-folder layer](./packages.md#in-folder-configuration-files). |

Hiding every candidate is not an error: the folder simply has nothing to say, and a package or space folder falls back
to what it inherits. At the repository root it means no config was found at all, and the error names every name tried.

One thing to watch: patterns do not know what they are matching, so a pattern written for a folder can reach a config
file too. `dispat*`, meant for a `dispat-sandbox` folder, also hides `dispat.json`. Name the folders you mean, or keep
the folder patterns specific enough not to collide.

An explicit `--config` is exact and is never filtered: naming an ignored file loads it.
