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

For a [polyrepository workspace](../control-repository.md#source-history-mode), this file remains the control file.
Set `polyrepo: true` when it declares source-owned paths directly, or list source configuration files in `configs`.
You can also repeat `--configs`. Imports are explicit: dispat never discovers a `dispat.*` file merely because a
package path enters a linked repository. Once a source root is explicitly imported, its ordinary root, space, and
package configuration layers apply within that repository. A package and its `src` must belong to the closest listed
Git worktree; an unlisted nested repository is an `E331` ownership error rather than another implicit source.

**Unknown keys are rejected** as typo protection. Put keys dispat does not know in [`custom`](./custom.md). A rejected
key that looks like a real setting can also mean the file was written for a newer dispat than the one reading it, and
`dispat self-update --check` reports whether a newer one exists.

**Case.** Every key keeps the spelling you write. dispat matches ordinary configuration names case-insensitively. A
script, space, package or versioning group is therefore named once, in the case you chose, and reached from anywhere by any spelling: a
`--package` flag, a commit scope, a flow entry and a dependency edge all match without being asked to agree with the
map. The name itself travels as written, so it is what a tag, an event and the `DISPAT_*` variables report.

Repository identities are the exception. A `repositoryOverrides` key must exactly match its `.gitmodules` name,
including case, because that external name identifies the owning Git history. A key matching no linked repository is
refused whether it enables or disables one, so a misspelled exclusion cannot release the repository it was written to
hold back.

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
| [`execution`](./execution.md)         | `execution`. This is the node's role, its capacity and the worker nodes a release may delegate build and publish tasks to. See [Distributed execution](../distributed-execution.md). |
| [Static env](./env.md)                | `env`. These are fixed environment variables added to every script the run executes.                                                                  |
| [The `.env` file](./dotenv.md)        | The environment file read from the current directory into the run. This covers `--env-file` and what wins over what.                                    |
| [custom](./custom.md)                 | `custom`. This is free-form data dispat never reads.                                                                                                |
| [Splitting the file](./refs.md)       | `$ref`. This covers moving any part of the configuration into a file of its own, and what a path inside one means.                                       |

See the [CLI](../cli/README.md), the [commit message format](../reference/commits.md), and the
[script environment variables](../reference/environment.md) for related references. Read
[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json) or
[`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml) for annotated
full examples. Four smaller files annotate one arrangement each:
[`dispat.example.control.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.control.yaml),
[`dispat.example.peer.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.peer.yaml),
[`dispat.example.orchestrator.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.orchestrator.yaml)
and
[`dispat.example.worker.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.worker.yaml).

## Top-level options

| Key                | Type                                       | Required | Description                                                                                                                                                            |
|--------------------|--------------------------------------------|----------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `scripts`          | map name → command or `[command, ...]`     | no       | Named shell commands, like package.json scripts. A name binds one command, or an array of commands run in order. See [One name, several commands](./scripts.md#one-name-several-commands). The same key also exists on a space and on a package, and a package looks up a name in the closest level first. See [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run). |
| `spaces`           | map name → space                           | see note | Package groups sharing build and publish behaviour. See [Spaces](./spaces.md). At least one space **or** one `packages` entry is required.                                 |
| `packages`         | map name → package                         | no       | Per-package configuration. This holds overrides for space packages (where the key is the folder name) and standalone packages outside every space via `path`. See [Packages](./packages.md).    |
| `versionGroups`    | map name → `{versioning}`                  | no       | Shared-versioning groups that cut across spaces. A space's or package's `versionGroup` key joins a group by name. A group may share the whole version, the major and minor, or the major alone. See [Versioning groups](./spaces.md#versioning-groups) and the [Shared versions](../reference/releasing/versioning.md) walkthrough. |
| `dependencies`     | map consumer → providers                   | no       | Consumer → provider relations between packages. See [`dependencies`](./dependencies.md) below. Spaces and packages declare their own too.                                                                                |
| `polyrepo`         | bool                                        | no       | Read each explicitly linked source repository's own Git history. A non-empty repository identity enables this automatically; a control repository can enable it with `polyrepo: true` or non-empty `configs`. See [Source-history mode](../control-repository.md#source-history-mode). |
| `repository`       | string                                      | see note | This repository's identity in a linked peer fleet, written from letters, digits, dots, underscores and hyphens. A non-empty value activates peer composition; `control` is reserved. See [The identity and roster](../choreographed-repositories.md#the-identity-and-roster). |
| `repositories`     | array of objects                            | no       | The other members of a linked peer fleet: `{name, url, path, branch}` per peer, where `path` defaults to `.links/<name>`. It may be omitted or empty for a one-member fleet, but a non-empty roster without `repository` is `E339`. See [The identity and roster](../choreographed-repositories.md#the-identity-and-roster). |
| `configs`          | array of strings                            | no       | Source configuration files imported by the control file into the combined workspace. Paths are relative to the control file. Imports enable polyrepository mode and are never discovered implicitly; imported roots cannot declare another fleet import list. |
| `repositoryOverrides` | map repository name → object             | no       | Participation (`enabled`, default `true`) and release-commit policy overrides keyed by the exact `.gitmodules` name. An excluded repository contributes no packages, commits, tags, baselines, scripts, records or locks, keeps its filesystem boundary, and takes the control space paths inside it out of the run. See [Repository commit policy](../control-repository.md#repository-commit-policy) and [Repository participation](../control-repository.md#repository-participation). |
| `repositoryBaselines` | array of objects                          | no       | Explicit `{consumer, releaseTag, repository, revision}` boundaries for cross-repository history that a normal release checkpoint cannot prove. See [Ambiguous boundaries](../control-repository.md#when-a-boundary-needs-help). |
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
| `autoSign`         | object                                     | no       | Default own-version policy of the sign stage, which writes each package's own version before the version stage and the build. A level that states one replaces it whole. See [`autoSign`](./autosign.md). |
| `autoPropagate`    | object                                     | no       | `autoVersion` under the propagate stage's name, with the same options. One object may state only one of the two, and a level stating either replaces both. See [Two names for one block](./autoversion.md#two-names-for-one-block). |
| `isBuildWaitingPublish` | bool or object                        | no       | Default for every space: what the consumers of a package wait for, and what a failed provider does to them. See [The provider relation](./spaces.md#the-provider-relation). The default is `false`.                                                                              |
| `revertOnFail`     | bool                                       | no       | Default for every space. See [Space options](./spaces.md#space-options). The default is `false`.                                                                              |
| `versioning`       | string                                     | no       | Default versioning mode, applied under each space's **own** group. Writing `fixed` here means every space versions its packages as one, not that all spaces share a version. Joining spaces into one group is what `versionGroups` is for. The default is `independent`.  |
| `parser`           | object                                     | no       | Commit-message parser options. See [`parser`](./parser.md#parser). Everything unset keeps the specification default.                                                   |
| `updateCheck`      | bool                                       | no       | Whether dispat looks for a newer release of itself and mentions one on a command's way out. The default is `true`. This never runs under `logFormat: json`, and it delays a command only when `DISPAT_UPDATE_CHECK=1` explicitly asks it to wait. See [Updating dispat](../reference/self-update.md#being-told-there-is-an-update).  |
| `execution`        | object                                     | no       | This node's role, its capacity and the worker nodes a release may delegate build and publish tasks to. It exists at the root only and is read from the configuration the run was started with. With the key absent, or `workers` empty, a release runs on one machine as it always did. See [`execution`](./execution.md) and [Distributed execution](../distributed-execution.md). |
| `buildOutputs`     | array of strings                           | no       | The folders a package's build leaves behind, relative to the package folder, which is what travels to the nodes that consume them. You can override this per space and per package. See [Space options](./spaces.md#space-options). |
| `buildPlatforms`   | array of strings                           | no       | The `os/arch` values a package's build may run on, in Go's spelling. Empty or absent means any node. You can override this per space and per package. See [Space options](./spaces.md#space-options). |
| `runOnly`          | string or `[string, string]`               | no       | Where a package's build and publish may run: `both` (the default), `worker` or `orchestrator`, as one value or a `[build, publish]` pair. You can override this per space and per package. See [Where a stage runs](../distributed-execution.md#where-a-stage-runs). |
| `runOutputs`       | object of arrays of strings                | no       | The folders a `dispat run` sweep of a script writes, keyed by script name, relative to the root of each package's repository. A sweep executed on worker nodes carries them back and merges them into this checkout. It exists at the root only and is read from the configuration the run was started with. See [Running scripts on workers](../distributed-execution.md#running-scripts-on-workers). |
| `unsafeDisableLock`| bool                                       | no       | Release without the [release lock](../reference/releasing/release-lock.md). The lock is the tag a release pushes to the remote so that two runs at once are refused rather than raced. The default is `false`. Use this for repositories with no remote to coordinate through. Set `DISPAT_UNSAFE_DISABLE_LOCK=true` to say the same for one invocation. In an orchestrated fleet only the control configuration's value counts, and a source's own is reported as ignored (`W331`); in a choreographed fleet each peer's value is its own.  |

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

Pass `--log-level` to override the configured value for one invocation. Inspect a puzzling plan with
`dispat status --log-level trace`. Debug and trace output include a `planning workload` event with elapsed time and
history-operation counts to help diagnose slow planning.

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
| `autoVersion`, `autoPropagate` | yes | yes | yes |
| `autoSign` | yes | yes | yes |
| `isBuildWaitingPublish` | yes | yes | yes |
| `revertOnFail` | yes | yes | yes |
| `versioning` | yes | yes | yes |
| `versionGroup` | no | yes | yes |
| `dependencies` | yes | yes | yes |
| `changelog`, `github` | yes | yes | yes |
| `src`, `ignore` | yes | yes | yes |
| `buildOutputs` | yes | yes | yes |
| `buildPlatforms` | yes | yes | yes |
| `runOnly` | yes | yes | yes |
| `runOutputs` | yes | no | no |
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
- **Replaced whole.** `autoVersion` (or `autoPropagate`), `autoSign`, `aliasTags`, `webhooks`, `buildOutputs`, `buildPlatforms`, `runOnly`, and `manifestNames`. Their empty fields carry meaning against their
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

There is a third number with a similar name and a different job. [`execution.concurrency`](./execution.md#concurrency)
is one node's **capacity**: how many assigned tasks that machine runs at once, across runs. The root budget still
bounds the whole run and is never multiplied by the number of worker nodes, so a task waiting for a node is holding
its stage slot while it waits.

Everything else is repository-wide and only exists at the root. This includes `spaces`, `versionGroups`,
`initials`, `commit`, `shell`, `run`, `parser`, `commitErrors`, `nonPackageScopes`, `logLevel`, `logFormat`,
`updateCheck`, `unsafeDisableLock`, `polyrepo`, `repository`, `repositories`, `configs`,
`repositoryOverrides`, `repositoryBaselines`, `execution`, and `runOutputs`. `execution` and `runOutputs` are narrower
still: they are read from the configuration the run was started with, so an imported or linked repository's own
object is ignored.

Read [the override ladder](./packages.md#the-override-ladder) to see the full order for one package from weakest to
strongest.

The loader itself, including `$ref` composition, case-preserving keys, and the upward search for the root, is published as
[`pkg/config`](../go/config.md), so a program of your own can read its configuration the way dispat reads this one.
