# Packages

A `packages` map holds per-package configuration. You key these entries by package name. The key keeps the spelling you
write, and dispat matches it against the package folder case-insensitively, like every config map key. You can write
this map at the top level of your root file, inside a space for
[its own packages](./spaces.md#the-spaces-packages-map), or in a
[space configuration file](./spaces.md#the-space-configuration-file). All three places take the same entry shape, and
[the override ladder](#the-override-ladder) decides which one wins.

A top-level entry plays one of two roles:

- **An override for a space package.** An entry *without* a `path` adjusts the configuration of the package whose
  folder name matches the entry key. This key must match exactly one package folder across all [spaces](./spaces.md).
  Two package folders whose names differ only by case are refused, because a name matched case-insensitively cannot
  address either of them.
  dispat rejects an unmatched key as a typo, and rejects a key matching a
  [`.dispatexclude`](./spaces.md#dispatexclude)d folder with the exclusion spelled out. You can handle one-off
  exceptions this way without carving the package out into a space of its own.
- **A standalone package.** An entry *with* a [`path`](#standalone-packages-path) declares a package living outside
  every space at that root-relative folder. The entry key becomes the package name. The entry itself holds the
  package's whole configuration.

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  },
  "packages": {
    "core": {
      "revertOnFail": false,
      "changelog": {
        "file": "HISTORY.md"
      }
    },
    "cli": {
      "path": "tools/cli",
      "flow": {
        "build": "build-go"
      },
      "dependencies": [
        "core"
      ]
    }
  }
}
```

## Package options

A package entry mirrors the [space options](./spaces.md#space-options) minus the space-defining keys. It also adds
these package-only keys:

| Key            | Type            | Effect                                                                                                                                                                                                                                                                                                |
|----------------|-----------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`         | string          | Declares a [standalone package](#standalone-packages-path) at this root-relative folder. This is always exactly one folder, unlike a space's `path`. You can only set this on an entry whose key matches no space folder, because a space package's location *is* its folder, and never in an [in-folder file](#in-folder-configuration-files). |
| `changelog`    | object          | Overlays the top-level [`changelog`](./records.md#changelog) **field by field** for this package's release records. You can flip `enabled`, rename the file, or retitle a section. Unset fields keep the global values, but a [line list](./records.md#overriding-a-list) set here replaces the inherited one rather than adding to it.        |
| `github`       | object          | Overlays the top-level [`github`](./records.md#github) the same way. A package can disable its GitHub releases or target another repository while keeping the global `tokenEnv`. Distinct effective targets each get their own up-front verification.                                                 |
| `concurrency`  | int or `[b, p]` | The package's *weight*: how many slots of the stage [concurrency budgets](./README.md#top-level-options) its tasks occupy. See [package weights](#package-weights-concurrency) below.                                                                                                                 |
| `versioning`   | string          | How much of the version this one package holds in common with its group. See [`versioning`](./spaces.md#versioning). You will mostly use `independent` to opt one package out of a shared space, but you can set any mode and the package stays in its space's group.                             |
| `versionGroup` | string          | Joins this one package to a [versioning group](./spaces.md#versioning-groups).                                                                                                                                                                                                                        |
| `dependencies` | string or array | Provider names this package [depends on](#package-dependencies). The consumer is the package itself.                                                                                                                                                                                                  |
| `manifestNames` | array of strings | The manifest names this package answers to, stated here rather than read from its files. See [`manifestNames`](#manifestnames) below.                                                                                                                                                                     |
| `src`          | string          | A folder-relative path narrowing which of the package's files count as changes to it. You can also set this on a space or at the root. See [`src`](#src) below.                                                                                                                                                                                        |
| `ignore`       | array of strings | Patterns keeping some of the package's own files from counting as changes to it. You can also set this on a space or at the root, and the levels add up. See [What counts as a change](./change-scope.md).                                                                                                                                                                                        |
| `env`          | map name → value | Fixed environment variables for this package's scripts. dispat merges these key by key over the space's map and the top-level one. See [Static env](./env.md).                                                                                                                                      |
| `custom`       | object          | Free-form data dispat never reads. See [`custom`](./custom.md). Nothing merges this data, so an entry's object and an in-folder file's object are independent.                                                                                                                                           |

For an entry overriding a space package, a field left unset **inherits** from the space. A field you set overrides it.
The per-field rules follow from what each object means:

- The boolean options (`isBuildWaitingPublish`, `revertOnFail`) are tri-state in an override. An absent field inherits.
  An explicit `false` overrides a space's `true`.
- `flow` merges **entry by entry**. An overridden stage or hook replaces that entry's list, every other entry inherits,
  and an explicit empty array (`"build": []`) clears an inherited entry. dispat looks up the names inside against this
  package first, then its space, then the file, so a package can keep its space's `flow.build: build` and still supply
  its own `build` command. A name missing from all three levels is an error naming the package. You cannot set
  `flow.login` per package because login runs [once per space, in the space folder](./spaces.md#flowlogin) and gates
  every publish of the space. A per-package login would contradict all three facts.
- `scripts` merges **name by name**. A name set here wins, the space's other names survive, and the file's names stay
  under both. dispat replaces the bound commands whole, so restating a
  [multi-command script](./scripts.md#one-name-several-commands) here creates a new sequence rather than adding to the
  inherited one. A name only this package defines belongs to this package alone, meaning `dispat run <name>` reaches no
  other package with it; see [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run).
- `versioning`/`versionGroup` are one axis. A layer setting either supersedes both inherited values, so a package sets
  `versioning: independent` to opt out of its space's group or `versionGroup: <name>` to join another. dispat rejects
  setting both in one layer as a contradiction. Setting `versioning` to another *shared* mode does not leave the
  space's group, but asks to share a different amount that the group resolves to the deepest any member asked for
  (`W237`). Overriding to a *sparse* mode changes only when the package releases, not whether its version counts. The
  group's next version is computed from every member's published version either way, so a sparse member's tag can still
  decide where the rest of the group lands; see
  [joining with a versioning of its own](../reference/releasing/versioning.md#joining-with-a-versioning-of-its-own).
- `autoVersion` replaces **wholesale**. Its empty fields already carry meaning relative to their siblings (no `kinds`
  means all four), so a field-level overlay could never express them against a non-empty base. Write an override of
  `{"enabled": false}` to switch the space's block off for the package.
- `manifestNames` replaces **wholesale**, like every other list. The layer nearest the package states what the package
  is called. Adding to an inherited list could never take a name away again.
- `tagFormat` overrides like everywhere else: package over space over repository.

dispat refuses two keys on an entry wherever you write it: `packages` and `spaces`. An entry configures one package, so
it holds neither packages nor spaces of its own. dispat refuses `path` everywhere except the file's top-level map,
where it declares a [standalone package](#standalone-packages-path).

## The override ladder

You can configure one package from six places. They apply in this order, each overlaying the one before it field by
field. The layer nearest the package wins:

| # | Layer                     | Where it lives                                    |
|---|---------------------------|----------------------------------------------------|
| 0 | root defaults             | root file, the top-level space-shaped keys        |
| 1 | space config              | root file, `spaces.<space>`                        |
| 2 | space configuration file  | `<space folder>/dispat.json`, its top-level object |
| 3 | root package entry        | root file, `packages.<package>`                    |
| 4 | space package entry       | root file, `spaces.<space>.packages.<package>`     |
| 5 | space file package entry  | `<space folder>/dispat.json`, `packages.<package>` |
| 6 | package configuration file| `<space folder>/<package>/dispat.json`             |

Layer 0 is the repository's own defaults for the keys a space could state (`flow`, `autoVersion`, `versioning`,
`tagFormat`, `aliasTags`, `webhooks`, `src`, `ignore`, `isBuildWaitingPublish`, `revertOnFail`). You write a setting every space
shares once here. See [Where a setting can live](./README.md#where-a-setting-can-live).

Layers 1 and 2 are the space, and they describe every package in it. Layers 3 to 6 each name one package, ordered by
how close to it you write them. The order is the repository as a whole, then the space, then the space's own folder,
then the package's own folder.

"Nearest wins" applies per field, not per layer. A farther layer still supplies everything the nearer ones leave unset.
Setting `changelog.file` at the top level and `revertOnFail` in the package's own file gives the package both.

A [standalone package](#standalone-packages-path) has no space. Only layers 3 and 6 apply over the root defaults,
making it its own space.

```json title="root dispat.json"
{
  "spaces": {
    "libs": {
      "path": "packages",
      "tagFormat": "libs/{name}@{version}",
      "packages": { "core": { "revertOnFail": true } }
    }
  },
  "packages": { "core": { "changelog": { "file": "HISTORY.md" } } }
}
```

```json title="packages/core/dispat.json"
{ "tagFormat": "core/{name}@{version}" }
```

`core` releases under `core/core@1.2.3` because layer 6 beat layer 1. It runs with `revertOnFail` on from layer 4. Its
changelog sits in `HISTORY.md` from layer 3, which nothing nearer contradicted.

## `manifestNames`

dispat works out which package a dependency refers to by reading the name each package's manifests declare. A
`package.json` says `"name": "@acme/core"`, so a sibling depending on `@acme/core` depends on that folder. This covers
most repositories without any configuration at all.

Some packages declare no name dispat can read. A Gradle module keeps its coordinate in a build script that is a program
rather than a manifest, and a folder built by a Makefile declares nothing. A project in an ecosystem dispat lacks a
parser for is opaque by definition. Nothing points at these packages, so `dispat compute` derives no edges into them
and auto-versioning never reconciles the declarations that name them.

Set `manifestNames` to say what such a package is called:

```yaml
packages:
  core:
    manifestNames: [ "com.acme:core" ]
```

From then on, a dependency spelled `com.acme:core` anywhere in the workspace resolves to the `core` package. This works
for `dispat compute` and for [auto-versioning](./autoversion.md) alike. The two share one index, so they cannot
disagree about what a name means.

Two rules keep this honest. A stated name **outranks** one a manifest declares, because you are stating a fact rather
than a file happening to say it.

No two packages may state the same name. A manifest name identifies one package. A collision here is a typo in your
configuration, so dispat fails to load it.

The key belongs to a package, not to a space. You write it in a `packages` entry or in the package's own
[in-folder file](#in-folder-configuration-files).

## `src`

The files a commit touches attribute it when it names no scope. Whichever package owns a changed path is the package
the commit addresses. Ownership means the package folder, so *everything* in the folder counts.

This is usually right, but occasionally it is not. A package whose folder also holds a docs site, a fixtures tree, or a
scratch directory releases on a typo fix in prose.

Set `src` to narrow that to one sub-folder:

```yaml
packages:
  core:
    src: lib
```

A changed file now has to sit under `packages/core/lib` to make a scopeless commit address `core`. Anything else in the
folder belongs to whichever package encloses it, or to no package at all.

You should know what `src` does *not* change, because it is most of the package:

- **The package folder is still the package.** Scripts run there, the changelog is written there, and the release
  commit stages all of it, `src` or not.
- **Manifests are still found in the whole folder.** A `package.json` or `go.mod` usually sits at the package root
  outside `src`. [auto-versioning](./autoversion.md) and `dispat compute` must still reach it.
- **A scope always wins.** `fix(core): ...` addresses `core` wherever the commit's files are. `src` narrows the
  file-derived fallback that runs when a commit names no scope at all. See
  [scope sets](../reference/commits.md#scope-sets).

dispat refuses a `src` that could never match at load. This includes a folder that is not there, a path leaving the
package, or the package folder itself. Each of those would narrow the package to nothing, and this check prevents a
package from quietly stopping its releases.

You can also write `src` on a space or at the root, where it becomes the default for every package it reaches. dispat
still resolves it against each package's own folder. See [`ignore`](./change-scope.md#ignore-everything-except-these)
to exclude some files rather than pick one folder, and the two work together.

## Package weights: `concurrency`

A package entry's `concurrency` is a *weight*, written as a scalar or a `[build, publish]` pair, defining how many
slots of the stage budgets the package's tasks occupy. Absent and `0` mean `1`, the ordinary cost. This deliberately
differs from the top-level key where `0` means the CPU count, because a weight has no CPU reading.

A package whose weight reaches a stage's budget runs that stage **alone**. You use this for the Android build that
would starve every neighbour of memory. Weights change slot accounting but never ordering, so a waiting heavy package
is never overtaken by lighter ones that became ready after it.

## Standalone packages: `path`

Add a `path` to an entry to create a package **outside every space**. This could be a tools folder next to the
workspaces, a deploy bundle at the repository top, or anything else that releases like a package but shares no parent
folder with one. The path is relative to the monorepo root, must stay inside the repository without absolute paths or
`..`, and must name an existing folder.

A standalone package is a full package in every respect. It plans, versions, builds, publishes, tags, and writes
records exactly like a space package. dispat builds its effective configuration through the same layers as an override,
starting from an empty base instead of a space, then applying the package's own
[in-folder file](#in-folder-configuration-files) field by field.

Having no space has three consequences:

- The package is its own single-package space, named after the entry key. Its implicit
  [versioning group](./spaces.md#versioning-groups) is its own name, and `versionGroup` joins it to any other group.
- There is no `flow.login`, because login is a space-level stage. A standalone package that needs authentication puts
  it in `flow.beforePublish`.
- `.dispatexclude` does not apply. The entry alone decides that the folder is a package.

A standalone package's name is the entry key exactly as you wrote it, capitals included. That name is what its tags,
its events and its `DISPAT_*` variables carry, so [renaming it](./versions.md#renaming-a-package) is a decision about
its release history rather than a change of spelling.

## Package dependencies

A package may declare the providers it depends on directly in its entry or in its in-folder file. This keeps its
dependencies next to the rest of its configuration:

```json
{
  "packages": {
    "web": {
      "dependencies": [
        "core",
        { "provider": "utils", "keep": true },
        { "provider": "tooling", "kind": "devDependencies" }
      ]
    }
  }
}
```

These entries match the ones a consumer lists in the top-level [`dependencies`](./dependencies.md) object. An edge
reads the same wherever you declare it, and moving one between the two places is a cut and a paste. The consumer is the
package itself, and one provider needs no array: `"dependencies": "core"`.

All declarations merge **into one list**. This includes the top-level object, every entry's list, and every in-folder
list. Where you declare an edge changes nothing about how it plans.

`dispat compute` treats every declaration source as one merged list, then edits each declaration **in the entry that
holds it**, whichever layer that is. It removes a stale edge declared in a space's `packages` entry from the root
config, one declared in a space file from that file, and one declared in a package's own file from there.

Every suggestion names its source, so `spaces["libs"]: packages["core"]: dependencies[0]` says exactly what an applied
change would touch. dispat applies a kind correction in place, since a package's list carries a kind as readily as the
top-level object does.

A **detected addition goes where its consumer already declares its providers**, and to the top-level object when it
declares none. A config that keeps each package's dependencies in that package's entry stays that way instead of
growing a second home for the edges the next `compute` finds. Each edited file gets its own `.backup`.

## In-folder configuration files

A package folder may carry a dispat config file of its own. It uses the same names and formats the root config resolves
through (`dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml`, first match wins, and a
[`.dispatexclude`](./spaces.md#choosing-between-two-config-files) in the folder chooses between them). Its top-level
object is exactly the package entry object above minus `path`, because a file cannot move the folder it lives in.

This file is the **most local** layer, the last rung of [the ladder](#the-override-ladder). The same merge rules apply,
and dispat rejects unknown keys with the file named. The file travels with the package, so a package moved between
spaces keeps its exceptions.

```json
// packages/core/dispat.json
{
  "revertOnFail": true,
  "versionGroup": "platform"
}
```

dispat refuses a package folder's file that declares `spaces` or `packages` and prints guidance. The folder holds a
monorepo root of its own, like a vendored or nested repository, and you must exclude it via
[`.dispatexclude`](./spaces.md#dispatexclude) rather than half-merge it. A space folder's file is the one place
`packages` belongs outside the root config, which you can read about in
[the space configuration file](./spaces.md#the-space-configuration-file).

Config resolution is aware of every one of these files. Running the CLI from inside a package folder ascends **past**
the package's own file and past its space's file to the monorepo root. This means `cd packages/core && dispat lint`
works whatever the folders on the way carry.
