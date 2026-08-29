# Configuration file reference

One file at the monorepo root describes everything dispat does. Run `dispat init` to write a starter one, or follow
[Getting started](../getting-started.md) to walk through a first configuration. This page documents the top level and
is the one home of the resolution rules below. The CLI and packages pages link back here.

**The format** is inferred from the file extension. JSON, YAML, and TOML are the three formats dispat reads.

**Finding the file.** dispat uses the first of `dispat.json`, `dispat.yaml`, `dispat.yml`, or `dispat.toml` that exists
in the root when you omit the `--config` flag. These are the names [`dispat init`](../cli/init.md) writes under its
formats. dispat ascends the parent directories when the root has none.

A file it finds higher up only ends the ascent when it declares `spaces` or `packages`. This happens because a package
folder's own [in-folder config file](./packages.md#in-folder-configuration-files) is an override rather than a root.
dispat fails with an error naming every name it tried when no candidate exists anywhere.

Pass an explicit `--config` to use a specific file with no fallback. A typo there fails loudly instead of quietly
loading a different file.

**Unknown keys are rejected** as typo protection. Put keys dispat does not know in [`custom`](./custom.md).

**Case.** Every key keeps the spelling you write, and dispat matches keys case-insensitively. A script, space, package
or versioning group is therefore named once, in the case you chose, and reached from anywhere by any spelling: a
`--package` flag, a commit scope, a flow entry and a dependency edge all match without being asked to agree with the
map. The name itself travels as written, so it is what a tag, an event and the `DISPAT_*` variables report.

Two keys of one object that differ only by case are refused when the file loads, and the error names both. There is no
lookup anywhere in dispat that could choose between them. The one exception is [`custom`](./custom.md), whose contents
dispat never reads.

**Splitting the file.** Any value may be a [`$ref`](./refs.md) naming another file. You can use this to spread a long
configuration across several files. The referenced file's content becomes the value, and everything on this page holds
for the result.

The larger objects have their own pages:

| Page                                  | Covers                                                                                                                                      |
|---------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| [Spaces](./spaces.md)                 | Space options, stages and hooks, login, announce, and outcome scripts. This covers the `versioning` modes (shared versions and `none`), versioning groups, `scripts`, the space's `packages` map, `dependencies`, the space configuration file, and `.dispatexclude`. |
| [Packages](./packages.md)             | The `packages` maps. This covers per-package overrides and the ladder that orders them, standalone packages via `path`, package-declared dependencies, and in-folder config files. |
| [What counts as a change](./change-scope.md) | `src` and `ignore`. These define which of a package's files make a scopeless commit address it. This also covers the `.dispatignore` file. |
| [Tags and baselines](./versions.md)   | `tagFormat` and `initials`.                                                                                                                    |
| [Alias tags](./alias-tags.md)         | `aliasTags`. These are the extra names a release is written under beside its real tag.                                                               |
| [Release records](./records.md)       | `changelog`, `github`, `commit`, and the shared entry format options.                                                                           |
| [Commit parsing options](./parser.md) | `commitErrors`, `nonPackageScopes`, and `parser`.                                                                                               |
| [`dependencies`](./dependencies.md)   | Consumer → provider relations between packages.                                                                                             |
| [Script sequences](./scripts.md)      | `scripts`. This covers binding a name to one command or to several, and what a failure inside a sequence does to the rest of it.                        |
| [Run-level hooks](./run-hooks.md)     | The top-level `run` object. This includes the hooks that observe the run as a whole, the branch guard, and the stale-checkout guard.                          |
| [Webhooks](./webhooks.md)             | `webhooks`. These are the HTTP endpoints a release run notifies of its progress, asynchronously and without ever gating the run.                                |
| [Static env](./env.md)                | `env`. These are fixed environment variables added to every script the run executes.                                                                  |
| [The `.env` file](./dotenv.md)        | The environment file read from the current directory into the run. This covers `--env-file` and what wins over what.                                    |
| [custom](./custom.md)                 | `custom`. This is free-form data dispat never reads.                                                                                                |
| [Splitting the file](./refs.md)       | `$ref`. This covers moving any part of the configuration into a file of its own, and what a path inside one means.                                       |

See the [CLI](../cli/README.md), the [commit message format](../reference/commits.md), and the
[script environment variables](../reference/environment.md) for related references. Read
[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json) or
[`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml) for annotated
full examples.

## Top-level options

| Key                | Type                                       | Required | Description                                                                                                                                                            |
|--------------------|--------------------------------------------|----------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → command or `[command, ...]`     | no       | Named shell commands, like package.json scripts. A name binds one command, or an array of commands run in order. See [One name, several commands](./scripts.md#one-name-several-commands). The same key also exists on a space and on a package, and a package looks up a name in the closest level first. See [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run). |
| `spaces`           | map name → space                           | see note | Package groups sharing build and publish behaviour. See [Spaces](./spaces.md). At least one space **or** one `packages` entry is required.                                 |
| `packages`         | map name → package                         | no       | Per-package configuration. This holds overrides for space packages (where the key is the folder name) and standalone packages outside every space via `path`. See [Packages](./packages.md).    |
| `versionGroups`    | map name → `{versioning}`                  | no       | Shared-versioning groups that cut across spaces. A space's or package's `versionGroup` key joins a group by name. A group may share the whole version, the major and minor, or the major alone. See [Versioning groups](./spaces.md#versioning-groups) and the [Shared versions](../reference/releasing/versioning.md) walkthrough. |
| `dependencies`     | map consumer → providers                   | no       | Consumer → provider relations between packages. See [`dependencies`](./dependencies.md) below. Spaces and packages declare their own too.                                                                                |
| `concurrency`      | int or `[int, int]`                        | no       | One value for both stages, or `[build, publish]`. The value `0` or an omitted key means the number of CPUs. More than two values is an error.                                             |
| `logLevel`         | string                                     | no       | Minimum log level. The options are `trace`, `debug`, `info` (default), `warn`, or `error`. See [what each level carries](#log-levels).                                                  |
| `logFormat`        | string                                     | no       | Logger output. The options are `pretty` (default, colored console output) or `json` (machine-readable lines for CI ingestion).                                                         |
| `tagFormat`        | string                                     | no       | Release tag template. You can override this per space and per package. The default is `{name}@{version}`. See [`tagFormat`](./versions.md#tagformat).                                   |
| `aliasTags`        | array of objects                           | no       | Extra tags each release is written under beside the one `tagFormat` produces. You can override this per space and per package. See [Alias tags](./alias-tags.md).               |
| `commitErrors`     | string                                     | no       | What an error in a commit message does to the run. The options are `warn` (default) or `error`. See [`commitErrors`](./parser.md#commiterrors).                                        |
| `nonPackageScopes` | array of strings                           | no       | Scope names that are deliberately not packages. The default is `["release"]`. See [`nonPackageScopes`](./parser.md#nonpackagescopes).                                         |
| `changelog`        | object                                     | no       | Per-package changelog file options. See [`changelog`](./records.md#changelog).                                                                                         |
| `github`           | object                                     | no       | GitHub release options. See [`github`](./records.md#github).                                                                                                           |
| `initials`         | map package → version                      | no       | Baseline versions used when a package's latest tag is missing or unparseable. See [`initials`](./versions.md#initials).                                                |
| `commit`           | object                                     | no       | End-of-run release commit, tagging, and push. This is disabled by default. See [`commit`](./records.md#commit).                                                                 |
| `shell`            | array of strings                           | no       | Command prefix scripts are appended to. Examples include `["bash", "-c"]` or `["cmd", "/C"]`. The default is `["/bin/sh", "-c"]`.                                                         |
| `env`              | map name → value                           | no       | Fixed environment variables added to every script the run executes. Spaces and packages layer their own maps on top. See [Static env](./env.md).                     |
| `custom`           | object                                     | no       | Free-form data dispat never reads. This is a place to keep your own tooling's settings without the unknown-key check rejecting them. Spaces and packages have their own `custom` blocks. See [custom](./custom.md).  |
| `run`              | object                                     | no       | The branch guard (`allowBranch`) and the run-level hooks (`beforeAll` through `afterPush`), keyed by name. See [Run-level hooks](./run-hooks.md) and [The branch guard](./run-hooks.md#the-branch-guard).  |
| `webhooks`         | array of objects                           | no       | HTTP endpoints notified of release progress. Deliveries are asynchronous and never affect the run. You can override this per space and per package. See [Webhooks](./webhooks.md).      |
| `src`              | string                                     | no       | Default scope folder for every package, resolved against each package's own folder. See [What counts as a change](./change-scope.md#src-only-this-folder-is-the-package). |
| `ignore`           | array of strings                           | no       | Default change-scope ignore patterns. See [What counts as a change](./change-scope.md#ignore-everything-except-these).                                                  |
| `flow`             | object                                     | no       | Default stages and hooks for every space. See [Stages and hooks](./spaces.md#stages-and-hooks). A space, and then a package, replaces the entries it names and keeps the rest. You can declare `login` here and it still runs once per space.  |
| `autoVersion`      | object                                     | no       | Default manifest-rewriting policy. See [`autoVersion`](./autoversion.md). A level that states one replaces it whole rather than merging into it.                 |
| `isBuildWaitingPublish` | bool                                  | no       | Default for every space. See [Space options](./spaces.md#space-options). The default is `false`.                                                                              |
| `revertOnFail`     | bool                                       | no       | Default for every space. See [Space options](./spaces.md#space-options). The default is `false`.                                                                              |
| `versioning`       | string                                     | no       | Default versioning mode, applied under each space's **own** group. Writing `fixed` here means every space versions its packages as one, not that all spaces share a version. Joining spaces into one group is what `versionGroups` is for. The default is `independent`.  |
| `parser`           | object                                     | no       | Commit-message parser options. See [`parser`](./parser.md#parser). Everything unset keeps the specification default.                                                   |
| `updateCheck`      | bool                                       | no       | Whether dispat looks for a newer release of itself and mentions one on a command's way out. The default is `true`. This never runs under `logFormat: json`, and it delays a command only when `DISPAT_UPDATE_CHECK=1` explicitly asks it to wait. See [Updating dispat](../reference/self-update.md#being-told-there-is-an-update).  |
| `unsafeDisableLock`| bool                                       | no       | Release without the [release lock](../reference/releasing/release-lock.md). The lock is the tag a release pushes to the remote so that two runs at once are refused rather than raced. The default is `false`. Use this for repositories with no remote to coordinate through. Set `DISPAT_UNSAFE_DISABLE_LOCK=true` to say the same for one invocation.  |

### Log levels

The levels are not just volume knobs. Each one answers a different question. The right level to reach for depends on
what you want to find out:

| Level | What it carries |
|-------|-----------------|
| `error` | Something failed. This could be a package that could not be built or published, or a record that could not be written after a release was already out. |
| `warn` | Something happened that you would want to know about but that did not stop the run. Every `W` diagnostic lives here. This includes a package riding a versioning group or a range caught up to a provider released in an earlier run. |
| `info` | The default, and the story of the run. This tells you what the plan is, which package published at which tag, and what the run ended with. This is enough to read a CI log and know what shipped. |
| `debug` | How the run decided. This shows which config file was read and which folder it treated as the monorepo root. It shows which folder each package is scoped to, and the plan's phases as dispat works through them. This is the level for finding out why dispat picked a specific plan. |
| `trace` | Every operation, one line each. This logs every git command with its arguments and how long it took. It shows every dependency edge. It shows every package's baseline, window size, computed bump, next version, and whether it is releasing. This is verbose on purpose, so use this level to attach to a bug report. |

Pass `--log-level` to override the configured value for one invocation. You can re-run a puzzling release with
`--log-level trace` without editing anything.

## Where a setting can live

You can write most of what configures a package at more than one level. The nearest level to the package wins:

**package → space → root**

The root file says what everything does by default. A space narrows this for its packages. A package entry or a
package's own config file settles it for one package.

A level that says nothing inherits from above. This makes the boolean options three-state. Writing `false` in a space
is not the same as leaving it out, and only writing `false` overrides a `true` above it.

| Setting | root | space | package |
|---------|------|-------|---------|
| `flow` | yes | yes | yes, except `flow.login` |
| `scripts` | yes | yes | yes |
| `env` | yes | yes | yes |
| `custom` | yes | yes | yes |
| `tagFormat` | yes | yes | yes |
| `aliasTags` | yes | yes | yes |
| `webhooks` | yes | yes | yes |
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
| `path` | no | yes, the space's own folder or list of folders | yes, one folder, for a standalone package |

How a level combines with the one below it depends on the setting:

- **Replaced.** Single values such as `tagFormat`, `versioning`, and `src`. The nearest statement is the answer.
- **Merged entry by entry.** `flow`, `scripts`, and `env`. A level replaces the entries it names and keeps the rest.
  Writing `flow: {build: build-libs}` in a space changes the build and leaves publish alone. An explicit empty array in
  `flow` clears an inherited entry. An empty array in `scripts` is an error because a name bound to no command resolves
  to nothing. dispat replaces an entry whole however many commands it binds. Restating a multi-command script creates a
  new sequence rather than adding to the inherited one.
- **Replaced whole.** `autoVersion`, `aliasTags`, `webhooks`, and `manifestNames`. Their empty fields carry meaning against their
  siblings, so a partial overlay cannot express what they mean. Write an empty `aliasTags: []` to make a package opt
  out.
- **Overlaid field by field.** `changelog` and `github`. A level can flip `enabled` and keep the titles it inherited.
  Their nested [`authors`](./records.md#attributing-an-entry-to-its-authors) object overlays the same way, so a level
  can rename the section and inherit the other five keys. Its `include` and `exclude` lists replace whole, because
  adding to an inherited list could never take a pattern away again. A level turns attribution off with an explicit
  `placement: off` rather than by omitting the key, since omitting it is how a level says nothing and inherits.
- **Merged, never overridden.** `dependencies`. Every declaration at every level adds to one graph.
- **Concatenated.** `ignore`. Later levels add patterns. A `!` pattern re-includes what an earlier level excluded.

Watch out for `concurrency`. At the root it is the **budget**, which is the number of slots a stage may use at once.
The value `0` means the number of CPUs.

On a space or a package it is a **weight**, which is the number of slots that package's task occupies. The value `0` or
an absent key means 1. They are the two sides of the same number and they are not interchangeable.

Everything else is repository-wide and only exists at the root. This includes `spaces`, `versionGroups`,
`initials`, `commit`, `shell`, `run`, `parser`, `commitErrors`, `nonPackageScopes`, `logLevel`, `logFormat`,
`updateCheck`, and `unsafeDisableLock`.

Read [the override ladder](./packages.md#the-override-ladder) to see the full order for one package from weakest to
strongest.
