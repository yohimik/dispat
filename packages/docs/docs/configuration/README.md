# Configuration file reference

One file at the monorepo root describes everything dispat does; `dispat init` writes a starter one, and
[Getting started](../getting-started.md) walks through a first configuration.

The format is inferred from the file extension, so JSON, YAML and TOML all work. With no `--config` flag the file is
**discovered**: the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml` that exists in the root is used
(the names [`dispat init`](../cli/init.md) writes under its formats). When the root has none, dispat ascends the parent
directories. A found file only ends the ascent when it declares `spaces` or `packages`, because a package folder's own
[in-folder config file](./packages.md#in-folder-configuration-files) is an override, not a root. When no candidate
exists anywhere, the run fails with an error naming every name it tried. An explicitly passed `--config` is used as-is,
with no fallback, so a typo there fails loudly instead of silently loading a different file. Unknown keys are rejected
as typo protection; [`custom`](./custom.md) is the one place to put keys dispat does not know. Keys are matched
case-insensitively and map keys are lowercased, so script and space names are effectively case-insensitive. The
[`env`](./env.md) objects are the exception, because environment variable names are case-sensitive: their keys keep
the spelling you write. This page is the one home of these resolution rules; the CLI and packages pages link back
here.

This page covers the top level; the larger objects have their own pages:

| Page                                  | Covers                                                                                                                                      |
|---------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| [Spaces](./spaces.md)                 | Space options, stages and hooks, login, announce, outcome scripts, the seven `versioning` modes and versioning groups, `scripts`, the space's `packages` map and `dependencies`, the space configuration file, `.dispatexclude`. |
| [Packages](./packages.md)             | The `packages` maps: per-package overrides and the ladder that orders them, standalone packages via `path`, package-declared dependencies, in-folder config files. |
| [What counts as a change](./change-scope.md) | `src` and `ignore`: which of a package's files make a scopeless commit address it, and the `.dispatignore` file. |
| [Tags and baselines](./versions.md)   | `tagFormat`, `initials`.                                                                                                                    |
| [Alias tags](./alias-tags.md)         | `aliasTags`: the extra names a release is written under, beside its real tag.                                                               |
| [Release records](./records.md)       | `changelog`, `github`, `commit`, the shared entry format options.                                                                           |
| [Commit parsing options](./parser.md) | `commitErrors`, `nonPackageScopes`, `parser`.                                                                                               |
| [`dependencies`](./dependencies.md)   | Consumer → provider relations between packages.                                                                                             |
| [Script sequences](./scripts.md)      | `scripts`, and what a failure inside a sequence does to the rest of it.                                                                     |
| [Run-level hooks](./run-hooks.md)     | The top-level `run` object: the hooks that observe the run as a whole, the branch guard, the stale-checkout guard.                          |
| [Static env](./env.md)                | `env`: fixed environment variables added to every script the run executes.                                                                  |
| [custom](./custom.md)                 | `custom`: free-form data dispat never reads.                                                                                                |

Related references: the [CLI](../cli/README.md), the [commit message format](../reference/commits.md) and the
[script environment variables](../reference/environment.md). Annotated full examples:
[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json), [`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml).

## Top-level options

| Key                | Type                                       | Required | Description                                                                                                                                                            |
|--------------------|--------------------------------------------|----------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → shell command                   | no       | Named shell commands, like package.json scripts. The same key also exists on a space and on a package, and a package looks up a name in the closest level first. See [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run). |
| `spaces`           | map name → space                           | see note | Package groups sharing build/publish behaviour; see [Spaces](./spaces.md). At least one space **or** one `packages` entry is required.                                 |
| `packages`         | map name → package                         | no       | Per-package configuration: overrides for space packages (key = folder name), and standalone packages outside every space via `path`; see [Packages](./packages.md).    |
| `versionGroups`    | map name → `{versioning}`                  | no       | Shared-versioning groups that cut across spaces, joined by name via a space's or package's `versionGroup` key. A group may share the whole version, the major and minor, or the major alone; see [Versioning groups](./spaces.md#versioning-groups) and the [Shared versions](../cookbook/releasing/versioning.md) walkthrough. |
| `dependencies`     | map consumer → providers                   | no       | Consumer → provider relations between packages; see [`dependencies`](./dependencies.md) below. Spaces and packages declare their own too.                                                                                |
| `concurrency`      | int or `[int, int]`                        | no       | One value for both stages, or `[build, publish]`. `0` (or omitted) means number of CPUs. More than two values is an error.                                             |
| `logLevel`         | string                                     | no       | Minimum log level: `trace`, `debug`, `info` (default), `warn` or `error`. See [what each level carries](#log-levels).                                                  |
| `logFormat`        | string                                     | no       | Logger output: `pretty` (default; colored console output) or `json` (machine-readable lines for CI ingestion).                                                         |
| `tagFormat`        | string                                     | no       | Release tag template, overridable per space and per package. Default `{name}@{version}`; see [`tagFormat`](./versions.md#tagformat).                                   |
| `aliasTags`        | array of objects                           | no       | Extra tags each release is written under, beside the one `tagFormat` produces. Overridable per space and per package; see [Alias tags](./alias-tags.md).               |
| `commitErrors`     | string                                     | no       | What an error in a commit message does to the run: `warn` (default) or `error`; see [`commitErrors`](./parser.md#commiterrors).                                        |
| `nonPackageScopes` | array of strings                           | no       | Scope names that are deliberately not packages. Default `["release"]`; see [`nonPackageScopes`](./parser.md#nonpackagescopes).                                         |
| `changelog`        | object                                     | no       | Per-package changelog file options; see [`changelog`](./records.md#changelog).                                                                                         |
| `github`           | object                                     | no       | GitHub release options; see [`github`](./records.md#github).                                                                                                           |
| `initials`         | map package → version                      | no       | Baseline versions used when a package's latest tag is missing or unparseable; see [`initials`](./versions.md#initials).                                                |
| `commit`           | object                                     | no       | End-of-run release commit, tagging and push; disabled by default. See [`commit`](./records.md#commit).                                                                 |
| `shell`            | array of strings                           | no       | Command prefix scripts are appended to, e.g. `["bash", "-c"]` or `["cmd", "/C"]`. Default `["/bin/sh", "-c"]`.                                                         |
| `env`              | map name → value                           | no       | Fixed environment variables added to every script the run executes. Spaces and packages layer their own maps on top; see [Static env](./env.md).                     |
| `custom`           | object                                     | no       | Free-form data dispat never reads: somewhere to keep your own tooling's settings without the unknown-key check rejecting them. Spaces and packages have their own; see [custom](./custom.md).  |
| `run`              | object                                     | no       | The branch guard (`allowBranch`) and the run-level hooks (`beforeAll` ... `afterPush`), keyed by name; see [Run-level hooks](./run-hooks.md) and [The branch guard](./run-hooks.md#the-branch-guard).  |
| `src`              | string                                     | no       | Default scope folder for every package, resolved against each package's own folder; see [What counts as a change](./change-scope.md#src-only-this-folder-is-the-package). |
| `ignore`           | array of strings                           | no       | Default change-scope ignore patterns; see [What counts as a change](./change-scope.md#ignore-everything-except-these).                                                  |
| `flow`             | object                                     | no       | Default stages and hooks for every space; see [Stages and hooks](./spaces.md#stages-and-hooks). A space, and then a package, replaces the entries it names and keeps the rest. `login` may be declared here and still runs once per space.  |
| `autoVersion`      | object                                     | no       | Default manifest-rewriting policy; see [`autoVersion`](./spaces.md#autoversion). A level that states one replaces it whole rather than merging into it.                 |
| `isBuildWaitingPublish` | bool                                  | no       | Default for every space; see [Space options](./spaces.md#space-options). Default `false`.                                                                              |
| `revertOnFail`     | bool                                       | no       | Default for every space; see [Space options](./spaces.md#space-options). Default `false`.                                                                              |
| `versioning`       | string                                     | no       | Default versioning mode, applied under each space's **own** group: `fixed` here means every space versions its packages as one, not that all spaces share a version. Joining spaces into one group is what `versionGroups` is for. Default `independent`.  |
| `parser`           | object                                     | no       | Commit-message parser options; see [`parser`](./parser.md#parser). Everything unset keeps the specification default.                                                   |
| `updateCheck`      | bool                                       | no       | Whether dispat looks for a newer release of itself and mentions one on a command's way out. Default `true`; never runs under `logFormat: json`, and never delays a command. See [Updating dispat](../reference/self-update.md#being-told-there-is-an-update).  |
| `unsafeDisableLock`| bool                                       | no       | Release without the [release lock](../cookbook/releasing/release-lock.md), the tag a release pushes to the remote so that two runs at once are refused rather than raced. Default `false`. For repositories with no remote to coordinate through; `DISPAT_UNSAFE_DISABLE_LOCK=true` says the same for one invocation.  |

### Log levels

The levels are not just volume knobs. Each one answers a different question, so the right level to reach for depends
on what you are trying to find out:

| Level | What it carries |
|-------|-----------------|
| `error` | Something failed. A package that could not be built or published, a record that could not be written after a release was already out. |
| `warn` | Something happened that you would want to know about but that did not stop the run: every `W` diagnostic lives here, from a package riding a versioning group to a range caught up to a provider released in an earlier run. |
| `info` | The default, and the story of the run: what the plan is, which package published at which tag, what the run ended with. Enough to read a CI log and know what shipped. |
| `debug` | How the run decided. Which config file was read and which folder it treated as the monorepo root, which folder each package is scoped to, and the plan's phases as it works through them. This is the level for "why did it pick that". |
| `trace` | Every operation, one line each. Every git command with its arguments and how long it took, every dependency edge, and every package's baseline, window size, computed bump and next version, releasing or not. Verbose on purpose: this is the level to attach to a bug report. |

`--log-level` overrides the configured value for one invocation, so you can re-run a puzzling release with
`--log-level trace` without editing anything.

## Where a setting can live

Most of what configures a package can be written at more than one level, and the nearest one to the package wins:

**package → space → root**

The root file says what everything does by default, a space narrows it for its packages, and a package entry (or a
package's own config file) settles it for one package. A level that says nothing inherits, which is why the boolean
options are three-state: writing `false` in a space is not the same as leaving it out, and only the first of those
overrides a `true` above it.

| Setting | root | space | package |
|---------|------|-------|---------|
| `flow` | yes | yes | yes, except `flow.login` |
| `scripts` | yes | yes | yes |
| `env` | yes | yes | yes |
| `custom` | yes | yes | yes |
| `tagFormat` | yes | yes | yes |
| `aliasTags` | yes | yes | yes |
| `autoVersion` | yes | yes | yes |
| `isBuildWaitingPublish` | yes | yes | yes |
| `revertOnFail` | yes | yes | yes |
| `versioning` | yes | yes | yes |
| `versionGroup` | no | yes | yes |
| `dependencies` | yes | yes | yes |
| `changelog`, `github` | yes | yes | yes |
| `src`, `ignore` | yes | yes | yes |
| `concurrency` | yes, as the budget | yes, as a weight | yes, as a weight |
| `manifestNames` | no | no | yes |
| `path` | no | yes, the space's own | yes, for a standalone package |

How a level combines with the one below it depends on the setting:

- **Replaced.** Single values such as `tagFormat`, `versioning` and `src`. The nearest statement is the answer.
- **Merged entry by entry.** `flow`, `scripts`, `env`. A level replaces the entries it names and keeps the rest, so
  `flow: {build: build-libs}` in a space changes the build and leaves publish alone. An explicit empty array clears an
  inherited entry.
- **Replaced whole.** `autoVersion`, `aliasTags`, `manifestNames`. Their empty fields carry meaning against their
  siblings, so a partial overlay could not express what they mean. An empty `aliasTags: []` is how a package opts out.
- **Overlaid field by field.** `changelog` and `github`. A level can flip `enabled` and keep the titles it inherited.
- **Merged, never overridden.** `dependencies`. Every declaration at every level adds to one graph.
- **Concatenated.** `ignore`. Later levels add patterns, and a `!` pattern re-includes what an earlier level excluded.

One warning about `concurrency`: at the root it is the **budget**, the number of slots a stage may use at once, and
`0` means the number of CPUs. On a space or a package it is a **weight**, the number of slots that package's task
occupies, and `0` or absent means 1. They are the two sides of the same number and they are not interchangeable.

Everything else is repository-wide and only exists at the root: `spaces`, `packages`, `versionGroups`, `initials`,
`commit`, `shell`, `run`, `parser`, `commitErrors`, `nonPackageScopes`, `logLevel`, `logFormat`, `updateCheck` and
`unsafeDisableLock`.

The full order for one package, from weakest to strongest, is in
[the override ladder](./packages.md#the-override-ladder).
