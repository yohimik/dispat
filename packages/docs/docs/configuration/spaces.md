# Spaces

A space is a group of packages sharing build/publish behaviour. Every direct sub-folder of the space's `path` is a
package named after the folder, unless a [`.dispatignore`](#dispatignore) file in the space folder excludes it. A single
package can depart from its space's configuration through a top-level [`packages` entry](./packages.md), so one-off
exceptions do not require carving the package out into a space of its own. A package living outside every space is
declared through a [standalone entry](./packages.md#standalone-packages-path).

## Space options

| Key                     | Type                     | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|--------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string                   | yes        | Folder relative to the root. Every direct sub-folder is a package named after the folder (hidden folders are skipped, and [`.dispatignore`](#dispatignore) excludes more). Package names must be unique across all spaces.                                                                                                                                                              |
| `isBuildWaitingPublish` | bool                     | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool                     | no (false) | When `true`, all local changes inside the package folder are rolled back (tracked files restored from HEAD, untracked files removed) if the package fails at any stage, or is skipped after its version stage already modified files.                                                                                                                                                   |
| `flow`                  | object                   | no         | What the space runs at which stage; see the table below.                                                                                                                                                                                                                                                                                                                                |
| `tagFormat`             | string                   | no         | Overrides the repository-wide [`tagFormat`](./versions.md#tagformat) for this space.                                                                                                                                                                                                                                                                                                    |
| `versioning`            | string                   | no         | How versions relate across the space's packages: `independent` (default), `fixed` or `fixedSparse`; see [`versioning`](#versioning). Mutually exclusive with `versionGroup`.                                                                                                                                                                                                            |
| `versionGroup`          | string                   | no         | Joins the space's packages to a shared-versioning group by name: a top-level [`versionGroups`](#versioning-groups) entry, or another space with `fixed`/`fixedSparse` versioning. The group's versioning mode is authoritative, so a space naming one must not set `versioning` itself.                                                                                                 |
| `runScripts`            | map name → shell command | no         | Named commands for `dispat run <name>`. Values are shell commands themselves, **not** references into `scripts`; see [`runScripts` and `dispat run`](#runscripts-and-dispat-run).                                                                                                                                                                                                       |
| `autoVersion`           | object                   | no         | Native manifest rewriting at the version stage: dispat itself reconciles declared workspace ranges and the package's own version in `package.json` and `go.mod`, before any `flow.version` script. Absent means off; see [`autoVersion`](#autoversion).                                                                                                                                 |

A single package's departures from these options live in the top-level [`packages`](./packages.md) map, not on the
space.

## Stages and hooks

The space's `flow` object, keyed by stage or hook name (every entry a script name or an array of names; see the
[sequence rules](./README.md#script-sequences)):

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
[run outcome listing](../environment.md#run-outcome-data) carries the same information for every package at once.

## `versioning`

How the versions of a space's packages relate to each other.

| Value                     | Effect                                                                                                                                                                                                                             |
|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `independent` *(default)* | Every package's version is computed from its own history alone: the behaviour described everywhere else in this documentation.                                                                                                     |
| `fixed`                   | One shared version for the whole space: a change to any member (by commit scope or changed files) releases every member at the same next version.                                                                                  |
| `fixedSparse`             | The shared version is computed exactly like `fixed`, but a member with no changes of its own keeps its previous version and is not released; changed members release at the shared version, aligning to it the moment they change. |

A space with shared versioning is a versioning group whose name is the space's own. The general mechanism, groups that
cut across the filesystem included, is described under [Versioning groups](#versioning-groups). Everything below holds
for any group, whichever way its members joined.

Under `fixed` and `fixedSparse` the group versions **as if it were one package**: the shared next version is computed
over the group's highest baseline with the max bump across all members, the group runs a single prerelease train (a
channel directive on one member moves the whole group; a graduation ends the train for all of it), and an exact
`Release-As` naming one member pins the group's single version, with the usual pin guards applied to it. A
`Release-As: none` hold still applies to the member it names: the held member stays behind and catches up when resumed.

Scopes (commit scope-sets and changed files) keep exactly one job in a versioning group: deciding which changelog
entries a package receives (and what its GitHub release announces). A member released only because of the shared version
gets a single "no changes" entry instead of borrowing its neighbour's notes, and its presence in the plan is reported as
`W210` (non-suppressible, like the catch-up codes: nothing in the commit log alone explains it). Such a ride is a full
release at the execution level: its version/build/publish scripts, hooks, tag and records all run.

Two convergence properties are worth knowing. A group whose members all carry the shared version releases nothing on a
quiet run, exactly like independent packages. And a `fixed` member left *behind* the group's published baseline (its
ride failed in an earlier run, or the group formed with unequal versions) is caught up at exactly that baseline on the
next run (also `W210`), restoring the one-version invariant. `fixedSparse` deliberately never does this, since staying
behind is its point.

Dependency edges stay package-scoped either way: a provider propagating into one member bumps that member (which then
carries its group along under `fixed`), and only the member with provider updates runs a version task.

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

A declaration carries one key: `versioning`, `fixed` or `fixedSparse`. `independent` is invalid there, because a group
exists to share versions. The declaration's mode is authoritative for everyone who joins, which is why `versionGroup`
and
`versioning` are mutually exclusive on the same space or package: a member cannot contradict its group. A declared group
nobody joins is inert configuration, like a disabled block.

The joining rules:

- `versionGroup` is settable on a space (all its packages join) and on a single package (through a top-level
  [`packages` entry](./packages.md) or an
  [in-folder config file](./packages.md#in-folder-configuration-files)); the
  [most local layer wins](./packages.md#package-options).
- The reference may also name another space whose own `versioning` is `fixed`/`fixedSparse`, joining that space's
  implicit group. A space that itself joined a declared group has no group of its own to reference; name the declared
  group directly. Group and space names share one namespace (a declaration shadowing a space name is rejected), and an
  unknown reference is an error, the same typo protection as an unknown dependency endpoint.
- A member's assignment mode is its own: a `fixed` space and a `fixedSparse` package can join the same group. The shared
  version is computed once, then each member follows its own mode (ride along, or stay behind when unchanged).

Group diagnostics (`W210` rides, `W211` competing pins, `W212` channel conflicts) name the group; the synthetic package
they are raised against is `group:<name>`.

## `autoVersion`

With an `autoVersion` object present, dispat keeps the space's manifests in sync with the released versions itself. No
`flow.version` script is required; one may still run afterwards and sees the already-reconciled files.

What happens at the version stage: dispat scans the package's manifests, matches each declared dependency against the
workspace (by manifest name, or by a declared local path such as `file:`, a relative `replace` or `path =`), and
rewrites the declaration to the provider's end-of-run version. That is the planned version when the provider is
releasing and has not failed, and its baseline otherwise.

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
| `manifests`           | string           | `root`   | `root`: only manifests directly in the package folder; `all`: every manifest found under it (dependency, virtual-env and build-output folders such as `node_modules`, `vendor`, `dist` and `venv`, plus every dot-folder, are never entered).                                                                                                                                                                                                                                                      |
| `kinds`               | array of strings | all four | Restrict rewriting to the named manifest fields (`dependencies`, `devDependencies`, `peerDependencies`, `optionalDependencies`).                                                                                                                                                                                                                                                                                                                                                                   |
| `only`                | array of strings | all      | Restrict rewriting to declarations of the named provider packages; every name must be a discovered package.                                                                                                                                                                                                                                                                                                                                                                                        |
| `nameMatch`           | string           | `exact`  | How a declared name finds its workspace package when no manifest declares that name and no local path matches: `exact` (such declarations are simply not workspace dependencies) or `substring`; see the note below the table.                                                                                                                                                                                                                                                                     |
| `match`               | array of globs   | any      | Rewrite only declared ranges matching one of the globs, e.g. `["workspace:*"]`, so a range pinned by hand is never overridden. `*` matches any run of characters (slashes included, same as scope globs), so `file:../core` is matched by `file:*` and by `*`.                                                                                                                                                                                                                                     |
| `range`               | string           | `caret`  | The write policy: `caret` (`^1.2.3`), `tilde` (`~1.2.3`), `exact` (`1.2.3`), a `{version}` template (`>={version}`), or any other literal written verbatim (`workspace:*`). Ecosystems with their own version spelling override the keywords: `go.mod` always receives exact canonical `vX.Y.Z`, Python files always receive `==X.Y.Z`; templates and literals pass through everywhere.                                                                                                            |
| `writeVersion`        | bool             | `true`   | Also write the package's own new version into its manifest's version field (§12.4). Applies to the package's **root** manifests only: a nested manifest (an example, a fixture) keeps its own version even under `manifests: all`.                                                                                                                                                                                                                                                                 |
| `syncLock`            | array of names   | none     | References into `scripts`, run inside the package folder after its manifests were rewritten and before its build: the slot for `npm install` and friends, so lock files follow the manifests. Skipped for a package whose version stage changed nothing, so a quiet release does not regenerate locks for no reason. A lock file living at the repo root is outside every package folder: list it under [`commit.include`](./records.md#commit) so the release commit carries it. That is optional for npm and Yarn workspaces and unavoidable for pnpm, whose `pnpm-lock.yaml` is always the workspace root's. |
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
[`dispat autoversion`](../cli.md#the-step-commands), with flags overriding the block's policy for the invocation; a
custom flow uses it to reconcile at the moment it needs, and the version stage later finds nothing left to rewrite.

Four warnings narrate what the rewrite did that the commit log alone cannot explain. `W192`: the manifest's declared own
version disagreed with the baseline (tags are authoritative; the computed version is written over it). `W197`: a range
was caught up to a provider released outside this run (the reconciliation rule's catch-up case). `W203`: a stable
release now ranges over a prerelease provider. And `W221`: a rewritten dependency has no configured `dependencies` edge
behind it, so nothing orders this package after that provider or skips it when the provider fails; the written version
is optimistic about a publish still in flight. `dispat compute` derives the missing edge.

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

Unlike the stage entries, the values are **shell commands themselves**, not references into `scripts`. Command names
are reserved: a run script named after any dispat command (`status`, `commit`, `changelog`, ...) is shadowed by the
command in the `dispat <name>` shorthand and only reachable as `dispat run <name>`. `dispat run
<name>` (or the shorthand `dispat <name>`, whenever `<name>` is not a command name) computes the plan and executes the
named script inside each **changed** package (the packages a release would process), honouring the dependency graph:
a package's script starts only after every changed provider's finished, and independent packages run concurrently within
the build concurrency budget (`--concurrency`'s first value). Each script gets the package's full
[DISPAT_* environment](../environment.md) (`DISPAT_STAGE` is `run:<name>`), so a script moves freely between a stage and
a run script. A changed package whose space does not define the name completes as a no-op; a name no space or
[package entry](./packages.md) defines is an error (running nothing silently is how a typo hides).

The run can also be narrowed to a single package, in two ways. `dispat run <name> <package>` runs the script in exactly
that package (changed or not, with no graph) and errors on an unknown package or on one whose space does not define the
script, because a *targeted* run that runs nothing would be a typo hiding. And the shorthand, invoked from inside a
package's folder (or any subdirectory of it), narrows to that package the same way (config resolution finds the monorepo
root by ascending parent directories, so `cd packages/core && dispat lint` just works), while from the monorepo top it
covers every changed package as usual. The shorthand takes no package argument.

A third selection axis is `--since <rev>` (`-s`): instead of the release window, select the packages the commits in
`rev..HEAD` address: `-s HEAD~1` runs the script over what the last commit addressed (per-commit CI),
`-s origin/main` over this branch's own commits (PR pipelines; the base moving on does not widen the set),
`-s <tag>` since a release, and the reserved `-s all` selects every package. Selection follows the same scope semantics
as planning: a commit's written scopes are authoritative (globs, exclusions and `nonPackageScopes`
included), and only a unit with no scope-set falls back to the files it changed (longest path prefix; see
[scope sets](../commits.md#scope-sets)). Ordering, concurrency and output carrying apply to the selected set exactly as
to the changed one. `--since` is mutually exclusive with an explicit `<package>` and overrides the shorthand's folder
inference.

A window (`--since` or the release window) covers only the packages the commits **address**, never the packages a change
*affects*: `dispat test -s HEAD~1` re-tests the changed provider, not the consumers that depend on it. The
`--consumers` flag closes that gap: it additionally selects every package that **transitively depends** on a selected
one; a consumer pulled in brings its own consumers, all the way down the graph. The added packages run whether or not
they changed, after their selected providers, and a failing provider's script skips them under the default
`--on-error skip` exactly like any selected dependent. `--consumers` is mutually exclusive with an explicit
`<package>` (a targeted run is exactly one package) and, like `--since`, overrides the shorthand's folder inference.

What a failure does is the `--on-error` flag: under `skip` (the default) the failed package's changed dependents are
skipped, transitively (the same shape a release gives a failed provider), while independent packages keep running; under
`continue` the dependents run anyway. Any failure makes the command exit `1` either way. Nothing is released, tagged or
written. Names are matched case-insensitively (viper lowercases map keys).

Run scripts take part in [script outputs](../environment.md#script-outputs) too, with one extra rule: outputs carry
across packages, down the dependency graph. Each run script gets `$DISPAT_OUTPUT` to export through, and a package's
script receives the exports of its changed providers' scripts (transitively; a script-less package in the middle still
carries them through) as `DISPAT_OUTPUT_<NAME>`, with `DISPAT_OUTPUT_SOURCE_<NAME>` still naming the original exporter
(`base:run:lint`). Providers merge in name order, the package's own re-export overrides, and under
`--on-error continue` a failed provider's exports still reach its dependents, mirroring what the pipeline's `onFail`
hooks receive.

## `.dispatignore`

A plain-text file inside a space folder listing direct sub-folders that are not packages, such as scratch areas,
fixtures or a vendored repository:

```
# not packages
sandbox
tmp-*
```

One pattern per line; blank lines and `#` comments are skipped, and `*` matches any run of characters (the same glob the
scope terms and `autoVersion.match` use). Patterns match the space folder's direct sub-folder names only, and each space
folder carries its own file. An excluded folder is invisible to discovery (never released, never scanned by
`compute` or `autoVersion`, its name an unknown scope in commits), and a [`packages` entry](./packages.md) naming one is
rejected with the exclusion spelled out.
