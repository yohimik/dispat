# Configuration file reference

One file at the monorepo root describes everything dispat does; `dispat init` writes a starter one, and
[Getting started](../getting-started.md) walks through a first configuration.

The format is inferred from the file extension, so JSON, YAML and TOML all work. With no `--config` flag the file is
**discovered**: the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists in the root is used
(the names [`dispat init`](../cli.md) writes under its formats). When the root has none, dispat ascends the parent
directories. A found file only ends the ascent when it declares `spaces` or `packages`, because a package folder's own
[in-folder config file](./packages.md#in-folder-configuration-files) is an override, not a root. When no candidate
exists anywhere, the run fails with an error naming every name it tried. An explicitly passed `--config` is used as-is,
with no fallback, so a typo there fails loudly instead of silently loading a different file. Unknown keys are rejected
as typo protection; [`custom`](#custom) is the one place to put keys dispat does not know. Keys are matched
case-insensitively and map keys are lowercased, so script and space names are effectively case-insensitive. The
[`env`](#static-env) objects are the exception, because environment variable names are case-sensitive: their keys keep
the spelling you write. This page is the one home of these resolution rules; the CLI and packages pages link back
here.

This page covers the top level; the larger objects have their own pages:

| Page                                  | Covers                                                                                                                                      |
|---------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| [Spaces](./spaces.md)                 | Space options, stages and hooks, login, announce, outcome scripts, the seven `versioning` modes and versioning groups, `scripts`, the space's `packages` map, the space configuration file, `.dispatignore`. |
| [Packages](./packages.md)             | The `packages` maps: per-package overrides and the ladder that orders them, standalone packages via `path`, package-declared dependencies, in-folder config files. |
| [Tags and baselines](./versions.md)   | `tagFormat`, `initials`.                                                                                                                    |
| [Release records](./records.md)       | `changelog`, `github`, `commit`, the shared entry format options.                                                                           |
| [Commit parsing options](./parser.md) | `commitErrors`, `nonPackageScopes`, `parser`.                                                                                               |

Related references: the [CLI](../cli.md), the [commit message format](../commits.md) and the
[script environment variables](../environment.md). Annotated full examples:
[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json), [`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml).

## Top-level options

| Key                | Type                                       | Required | Description                                                                                                                                                            |
|--------------------|--------------------------------------------|----------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → shell command                   | no       | Named shell commands, like package.json scripts. The same key also exists on a space and on a package, and a package looks up a name in the closest level first. See [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run). |
| `spaces`           | map name → space                           | see note | Package groups sharing build/publish behaviour; see [Spaces](./spaces.md). At least one space **or** one `packages` entry is required.                                 |
| `packages`         | map name → package                         | no       | Per-package configuration: overrides for space packages (key = folder name), and standalone packages outside every space via `path`; see [Packages](./packages.md).    |
| `versionGroups`    | map name → `{versioning}`                  | no       | Shared-versioning groups that cut across spaces, joined by name via a space's or package's `versionGroup` key. A group may share the whole version, the major and minor, or the major alone; see [Versioning groups](./spaces.md#versioning-groups) and the [Shared versions](../versioning.md) walkthrough. |
| `dependencies`     | list of `{consumer, provider, kind, keep}` | no       | Package-level consumer → provider relations; see [`dependencies`](#dependencies) below.                                                                                |
| `concurrency`      | int or `[int, int]`                        | no       | One value for both stages, or `[build, publish]`. `0` (or omitted) means number of CPUs. More than two values is an error.                                             |
| `logLevel`         | string                                     | no       | Minimum log level: `trace`, `debug`, `info` (default), `warn` or `error`.                                                                                              |
| `logFormat`        | string                                     | no       | Logger output: `pretty` (default; colored console output) or `json` (machine-readable lines for CI ingestion).                                                         |
| `tagFormat`        | string                                     | no       | Release tag template, overridable per space and per package. Default `{name}@{version}`; see [`tagFormat`](./versions.md#tagformat).                                   |
| `commitErrors`     | string                                     | no       | What an error in a commit message does to the run: `warn` (default) or `error`; see [`commitErrors`](./parser.md#commiterrors).                                        |
| `nonPackageScopes` | array of strings                           | no       | Scope names that are deliberately not packages. Default `["release"]`; see [`nonPackageScopes`](./parser.md#nonpackagescopes).                                         |
| `changelog`        | object                                     | no       | Per-package changelog file options; see [`changelog`](./records.md#changelog).                                                                                         |
| `github`           | object                                     | no       | GitHub release options; see [`github`](./records.md#github).                                                                                                           |
| `initials`         | map package → version                      | no       | Baseline versions used when a package's latest tag is missing or unparseable; see [`initials`](./versions.md#initials).                                                |
| `commit`           | object                                     | no       | End-of-run release commit, tagging and push; disabled by default. See [`commit`](./records.md#commit).                                                                 |
| `shell`            | array of strings                           | no       | Command prefix scripts are appended to, e.g. `["bash", "-c"]` or `["cmd", "/C"]`. Default `["/bin/sh", "-c"]`.                                                         |
| `env`              | map name → value                           | no       | Fixed environment variables added to every script the run executes. Spaces and packages layer their own maps on top; see [Static env](#static-env).                     |
| `custom`           | object                                     | no       | Free-form data dispat never reads: somewhere to keep your own tooling's settings without the unknown-key check rejecting them. Spaces and packages have their own; see [custom](#custom).  |
| `run`              | object                                     | no       | The branch guard (`allowBranch`) and the run-level hooks (`beforeAll` ... `afterPush`), keyed by name; see [Run-level hooks](#run-level-hooks) and [The branch guard](#the-branch-guard).  |
| `parser`           | object                                     | no       | Commit-message parser options; see [`parser`](./parser.md#parser). Everything unset keeps the specification default.                                                   |
| `updateCheck`      | bool                                       | no       | Whether dispat looks for a newer release of itself and mentions one on a command's way out. Default `true`; never runs under `logFormat: json`, and never delays a command. See [Updating dispat](../self-update.md#being-told-there-is-an-update).  |

## dependencies

The `dependencies` list declares the consumer → provider relations the graph orders releases by:

```yaml
dependencies:
  - consumer: app
    provider: core
  - web: [core, utils]   # shorthand: consumer key, provider name or array
```

Both packages must exist; self-dependencies and cycles are rejected; duplicates are ignored. The shorthand form is
expanded at load. Packages can also declare their own providers in their `packages` entry or in-folder file (see
[package dependencies](./packages.md#package-dependencies)), and every declaration merges into one list.

The optional `kind` names the manifest field the edge stands for: `dependencies` (default), `devDependencies`,
`peerDependencies` or `optionalDependencies`. Propagation follows or ignores the edge according to
`parser.propagation.kinds`, whose default is every kind except `devDependencies`.

`keep: true` marks an edge [`dispat compute`](../cli.md#commands) must never suggest removing: a deliberate relation no
manifest declares, such as a Docker base-image chain. The planner treats kept edges like any other.

## Script sequences

`scripts` defines named commands; the top-level `run` object and each space's `flow` object say **what runs when**,
referencing those names. A `flow` name is looked up against the package the stage is running for, closest level first:
the package's `scripts`, then its space's, then the file's. The `run` hooks are different, because they execute once at
the repository root with no package involved, so they only ever see the file's `scripts`. If a `flow` name is missing
from all three of a package's levels, the config is rejected with an error naming that package, which is how a script
defined only in some *other* space or package gets caught. Every entry of either object accepts a single script name or
an array of names executed
**sequentially, in order**; a scalar is simply a one-element sequence. How a failure inside a sequence behaves depends
on what the sequence gates:

- **Release-gating scripts** (the stage scripts, the login, every hook up to `beforePublish`, and the run-level
  `beforeAll`) are fail-fast: the first failing command stops the sequence and fails the package's release, or, for the
  run-level `beforeAll`, the whole run.
- **Warn-only scripts** (`postPublish`, the whole announce frame of `beforeAnnounce`, `announce` and `postAnnounce`, the
  outcome scripts `onFail` / `onSkip`, and every other run-level hook) never fail anything: a failing command is logged
  as a warning and **the remaining commands of the sequence still run**. These hooks observe work that has already
  happened, so stopping the sequence could not undo it.

## Run-level hooks

Seven hooks observe the run as a whole rather than any one package. They live in the **top-level `run` object**, run in
the **monorepo root**, and each is a no-op when not configured:

```yaml
run:
  beforeAll: check-preconditions
  postAll: notify
```

| Hook               | Runs                                                                              |
|--------------------|-----------------------------------------------------------------------------------|
| `run.beforeAll`    | Once, after planning and verification, before the task graph starts.              |
| `run.postAll`      | Once, after the whole task graph finishes, even when nothing released.            |
| `run.beforeCommit` | Before the release commit. `commit` mode only, and only when something published. |
| `run.afterCommit`  | After the release commit succeeded.                                               |
| `run.postCommit`   | After the release commit **and** the tags.                                        |
| `run.beforePush`   | Before the push. `commit.push` mode only.                                         |
| `run.afterPush`    | After the push succeeded.                                                         |

`run.beforeAll` is the one **gating** run hook: it fires before any release work, when failing it can still stop
everything, so it does, fail-fast, aborting the run with exit `1` before anything is built, published or tagged. Every
other run hook only **warns** on failure (a warn-only sequence: every command runs even when an earlier one failed),
because it runs after the work it observes. The "after" hooks additionally only run when the operation they bracket
succeeded: a hook observing a commit or push that never happened would be reporting a lie.

All seven receive `DISPAT_STAGE` naming the hook and the [workspace listing](../environment.md#workspace-data);
`run.postAll` and everything after it additionally receive the
[run outcome listing](../environment.md#run-outcome-data) reporting which packages published, failed, were skipped or
were never planned to release (`run.beforeAll` fires before any outcome exists).

## The branch guard

`run.allowBranch` is not a hook but a guard. When you set it, a release run refuses to start unless the branch you have
checked out matches one of the patterns you listed:

```yaml
run:
  allowBranch: [main, release/*]
```

That is the whole feature. A run from `main` proceeds; a run from `feature/tryout` stops with exit `1` and a message
naming both the branch and the patterns, before any verification, any hook, and any release work. Nothing is built,
nothing is tagged, and the repository is left exactly as it was found.

A `*` matches any run of characters, separators included, so `release/*` reaches `release/v2/hotfix`. A detached HEAD
has no branch name, so it matches nothing, including a pattern as broad as `*`.

The guard is about releasing, not about looking. Read-only commands (`status`, `preview`, `compute`) work on any branch,
which is what you want on a pull request. The [step commands](../steps.md) are not guarded either: they are built to run
inside a release stage that this guard has already cleared.

Leave the list unset to release from any branch. That suits single-branch repositories and disposable clones, and it is
the default.

### Releasing from a stale checkout

There is a second guard, and it needs no configuration. When the finalize phase is set to push
([`commit.push`](./records.md#commit)), dispat checks that your checkout is not behind the branch it would push to, and
refuses if it is:

```console
$ dispat
ERR refusing to release  error="the checkout is behind origin/main; pull before releasing"
```

The reason is that the plan is computed from the tags this clone can see. If someone else has released in the meantime,
your checkout is planning versions that may already exist, and the push at the end would be rejected anyway, after
everything was built, published and tagged. Failing at the start costs nothing; failing at the end costs a release.

`git pull` and run again. Two cases are deliberately not treated as behind: a branch the remote does not have yet,
because the first push is what creates it, and a detached HEAD, where there is no branch to compare. The check is an
`ls-remote`, so `commit.verify: false` — the escape hatch for remotes that reject `ls-remote` but accept pushes — turns
it off along with the reachability check.

## Static env

The `env` objects add fixed environment variables to the scripts dispat runs. The simplest use is one map at the top of
the file, reaching everything:

```yaml
env:
  NPM_CONFIG_REGISTRY: https://npm.corp.example
```

Spaces and packages can add their own, and the three layers merge key by key with the most local one winning. A key set
in only one layer reaches every script under it; a key set in two is decided by the nearer:

```yaml
env:
  NPM_CONFIG_REGISTRY: https://npm.corp.example   # every script
spaces:
  libs:
    env:
      GOFLAGS: -mod=mod                            # every script of the libs packages
packages:
  core:
    env:
      CGO_ENABLED: "1"                             # core's scripts only
```

A package's scripts here see all three: the registry, the Go flags, and `CGO_ENABLED`. The other packages of `libs` see
the first two. The run-level hooks, which execute at the repository root with no package in view, see the top-level map
alone, because no space or package applies to them.

Where you can write an `env` map, you can write it in a [space folder's or package folder's own config file](./packages.md)
too. Those count as layers like any other, the in-folder file being more local than the entry that names it.

### Values can reference other variables

`$NAME` and `${NAME}` in a value are expanded before the script runs, against the script's computed
[`DISPAT_*` variables](../environment.md) first and the process environment second:

```yaml
env:
  CUSTOM_TAG: custom_$DISPAT_VERSION
```

Each package's scripts get their own version, so `core` at 1.4.0 sees `CUSTOM_TAG=custom_1.4.0`. dispat does this
expansion itself, because exported variables never pass through a shell. Write `$$` for a literal dollar sign; an
unknown name expands to nothing, the same as in a shell.

### Two things you cannot do

Keys keep the exact case you write them in, which is worth knowing because the rest of the configuration is
case-insensitive: script names, space names and package names all match regardless of case. Environment variables do
not, so dispat reads the `env` objects back from your file to preserve their spelling. Two keys differing only in case
are rejected, since there would be no way to tell which one you meant.

A static variable also cannot override a computed one. The `DISPAT_` prefix is reserved, and setting a key that starts
with it is a configuration error rather than something quietly ignored. That is what lets a script read
`DISPAT_VERSION` and trust it.

## custom

`custom` is a free-form object dispat never reads. Everything else in the file is checked against the known keys, and an
unrecognised one is an error that catches typos. `custom` is the exception: put whatever your own tooling needs in it
and dispat will carry it without looking inside.

```yaml
custom:
  team: platform
  dashboard: https://grafana.example/d/releases
```

Spaces and package entries have their own `custom` objects. Unlike `env`, nothing merges: each one belongs to the level
that wrote it.
