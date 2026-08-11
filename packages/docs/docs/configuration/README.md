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
as typo protection. Keys are matched case-insensitively and map keys are lowercased, so script and space names are
effectively case-insensitive. This page is the one home of these resolution rules; the CLI and packages pages link back
here.

This page covers the top level; the larger objects have their own pages:

| Page                                  | Covers                                                                                                                                      |
|---------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| [Spaces](./spaces.md)                 | Space options, stages and hooks, login, announce, outcome scripts, `versioning` and versioning groups, `scripts`, the space's `packages` map, the space configuration file, `.dispatignore`. |
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
| `versionGroups`    | map name → `{versioning}`                  | no       | Shared-versioning groups that cut across spaces, joined by name via a space's or package's `versionGroup` key; see [Versioning groups](./spaces.md#versioning-groups). |
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
| `run`              | object                                     | no       | The run-level hooks (`beforeAll` ... `afterPush`), keyed by hook name; see [Run-level hooks](#run-level-hooks).                                                        |
| `parser`           | object                                     | no       | Commit-message parser options; see [`parser`](./parser.md#parser). Everything unset keeps the specification default.                                                   |

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
