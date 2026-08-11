# Packages

A `packages` map holds per-package configuration, keyed by package name (matched case-insensitively, like every config
map key). The file's own top-level map is the broadest place to write one; a space can hold the same entries for
[its own packages](./spaces.md#the-spaces-packages-map), and so can a [space configuration
file](./spaces.md#the-space-configuration-file). They all take the same entry shape, and
[the override ladder](#the-override-ladder) says which one wins.

A top-level entry plays one of two roles:

- **An override for a space package.** An entry *without* a `path` adjusts the configuration of the package whose folder
  name matches the entry key. Every such key must match exactly one package folder across all
  [spaces](./spaces.md). An unmatched key is the same class of typo as an unknown dependency endpoint, and a key
  matching a [`.dispatignore`](./spaces.md#dispatignore)d folder is rejected with the exclusion spelled out. One-off
  exceptions do not require carving the package out into a space of its own.
- **A standalone package.** An entry *with* a [`path`](#standalone-packages-path) declares a package living outside
  every space, at that root-relative folder; the entry key is the package name and the entry itself is the package's
  whole configuration.

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

An entry mirrors the [space options](./spaces.md#space-options) minus the space-defining keys, and adds the package-only
keys:

| Key            | Type            | Effect                                                                                                                                                                                                                                                                                                |
|----------------|-----------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`         | string          | Declares a [standalone package](#standalone-packages-path) at this root-relative folder. Only valid on an entry whose key matches no space folder (a space package's location *is* its folder, so redefining it is rejected), and never valid in an [in-folder file](#in-folder-configuration-files). |
| `changelog`    | object          | Overlays the top-level [`changelog`](./records.md#changelog) **field by field** for this package's release records: flip `enabled`, rename the file, retitle a section; unset fields keep the global values.                                                                                          |
| `github`       | object          | Overlays the top-level [`github`](./records.md#github) the same way: a package can disable its GitHub releases or target another repository while keeping the global `tokenEnv`. Distinct effective targets each get their own up-front verification.                                                 |
| `concurrency`  | int or `[b, p]` | The package's *weight*: how many slots of the stage [concurrency budgets](./README.md#top-level-options) its tasks occupy. See [package weights](#package-weights-concurrency) below.                                                                                                                 |
| `versionGroup` | string          | Joins this one package to a [versioning group](./spaces.md#versioning-groups).                                                                                                                                                                                                                        |
| `dependencies` | string or array | Provider names this package [depends on](#package-dependencies); the consumer is the package itself.                                                                                                                                                                                                  |
| `manifestNames` | array of strings | The manifest names this package answers to, stated rather than read from its files. See [`manifestNames`](#manifestnames) below.                                                                                                                                                                     |

For an entry overriding a space package, a field left unset **inherits** from the space; a field set overrides it. The
per-field rules follow from what each object means:

- The boolean options (`isBuildWaitingPublish`, `revertOnFail`) are tri-state in an override: absent inherits, an
  explicit `false` overrides a space's `true`.
- `flow` merges **entry by entry**: an overridden stage or hook replaces that entry's list, every other entry inherits,
  and an explicit empty array (`"build": []`) clears an inherited entry. The names inside are looked up against this
  package first, then its space, then the file, so a package can keep its space's `flow.build: build` and still supply
  its own `build` command. A name missing from all three levels is an error naming the package. `flow.login` cannot be
  set per package: login runs [once per space, in the space folder](./spaces.md#flowlogin), gating every publish of the
  space, and a per-package login would contradict all three.
- `scripts` merges **name by name**: a name set here wins, the space's other names survive, and the file's names stay
  under both. A name only this package defines belongs to this package alone, so `dispat run <name>` reaches no other
  package with it. See [`scripts` and `dispat run`](./spaces.md#scripts-and-dispat-run).
- `versioning`/`versionGroup` are one axis: a layer setting either supersedes both inherited values (so a package sets
  `versioning: independent` to opt out of its space's group, or `versionGroup: <name>` to join another). Setting both in
  one layer is a contradiction and is rejected.
- `autoVersion` replaces **wholesale**: its empty fields already carry meaning relative to their siblings (no `kinds`
  means all four), so a field-level overlay could never express them against a non-empty base. An override of
  `{"enabled": false}` switches the space's block off for the package.
- `manifestNames` replaces **wholesale**, like every other list: the layer nearest the package states what the package
  is called, and adding to an inherited list could never take a name away again.
- `tagFormat` overrides like everywhere else: package over space over repository.

Two keys are refused on an entry wherever it is written: `packages` and `spaces`. An entry configures one package, so it
holds neither packages nor spaces of its own. `path` is refused everywhere except the file's top-level map, where it
declares a [standalone package](#standalone-packages-path).

## The override ladder

One package's configuration can be spoken to from six places. They apply in this order, each overlaying the one before
it field by field, and the layer nearest the package wins:

| # | Layer                     | Where it lives                                    |
|---|---------------------------|----------------------------------------------------|
| 1 | space config              | root file, `spaces.<space>`                        |
| 2 | space configuration file  | `<space folder>/dispat.json`, its top-level object |
| 3 | root package entry        | root file, `packages.<package>`                    |
| 4 | space package entry       | root file, `spaces.<space>.packages.<package>`     |
| 5 | space file package entry  | `<space folder>/dispat.json`, `packages.<package>` |
| 6 | package configuration file| `<space folder>/<package>/dispat.json`             |

Layers 1 and 2 are the space, and describe every package in it. Layers 3 to 6 each name one package, ordered by how
close to it they are written: the repository as a whole, then the space, then the space's own folder, then the package's
own folder.

"Nearest wins" is per field, not per layer. A farther layer still supplies everything the nearer ones leave unset, so
setting `changelog.file` at the top level and `revertOnFail` in the package's own file gives the package both.

A [standalone package](#standalone-packages-path) has no space, so only layers 3 and 6 apply, over an empty base.

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

`core` releases under `core/core@1.2.3` (layer 6 beat layer 1), with `revertOnFail` on (layer 4) and its changelog in
`HISTORY.md` (layer 3, which nothing nearer contradicted).

## `manifestNames`

dispat works out which package a dependency refers to by reading the name each package's manifests declare. A
`package.json` says `"name": "@acme/core"`, so a sibling depending on `@acme/core` is depending on that folder. That
covers most repositories without any configuration at all.

Some packages declare no name anything here can read. A Gradle module keeps its coordinate in a build script that is a
program rather than a manifest. A folder built by a Makefile declares nothing. A project in an ecosystem dispat has no
parser for is opaque by definition. Nothing points at these packages, so `dispat compute` derives no edges into them
and auto-versioning never reconciles the declarations that name them.

`manifestNames` is how you say what such a package is called:

```yaml
packages:
  core:
    manifestNames: [ "com.acme:core" ]
```

From then on a dependency spelled `com.acme:core` anywhere in the workspace resolves to the `core` package, for
`dispat compute` and for [auto-versioning](./spaces.md#autoversion) alike. The two share one index, so they cannot
disagree about what a name means.

Two rules keep it honest. A stated name **outranks** one a manifest declares, because it is you saying so rather than
a file happening to say it. And no two packages may state the same name: a manifest name identifies one package, and a
collision here is a typo in your configuration rather than a fact about the repository, so it fails to load.

The key belongs to a package, not to a space, so it lives in a `packages` entry or in the package's own
[in-folder file](#in-folder-configuration-files).

## Package weights: `concurrency`

A package entry's `concurrency` is a *weight*, scalar or `[build, publish]` pair: how many slots of the stage budgets
the package's tasks occupy. Absent and `0` mean `1`, the ordinary cost. This deliberately differs from the top-level
key, where `0` means the CPU count; a weight has no CPU reading.

A package whose weight reaches a stage's budget runs that stage **alone**. That is the slot for the Android build that
would starve every neighbour of memory. Weights change slot accounting, never ordering, and a waiting heavy package is
never overtaken by lighter ones that became ready after it.

## Standalone packages: `path`

An entry with a `path` is a package **outside every space**: a tools folder next to the workspaces, a deploy bundle at
the repository top, or anything else that releases like a package but shares no parent folder with one. The path is
relative to the monorepo root, must stay inside the repository (no absolute paths, no `..`), and must name an existing
folder.

A standalone package is a full package in every respect: it plans, versions, builds, publishes, tags and writes records
exactly like a space package. Its effective configuration is built through the same layers as an override, starting from
an empty base instead of a space: the entry, then the package's own
[in-folder file](#in-folder-configuration-files), field by field. Three consequences of having no space:

- The package is its own single-package space, named after the entry key: its implicit
  [versioning group](./spaces.md#versioning-groups) is its own name, and `versionGroup` joins it to any other group.
- There is no `flow.login`, because login is a space-level stage. A standalone package that needs authentication puts it
  in
  `flow.beforePublish`.
- `.dispatignore` does not apply; the entry alone decides that the folder is a package.

Config map keys are lowercased by the loader, so a standalone package's name (the entry key) is effectively lowercase,
like space names.

## Package dependencies

A package may declare the providers it depends on directly in its entry (or in its in-folder file): one provider name or
an array of names. The consumer is the package itself and the edge kind is the default (`dependencies`); an edge that
needs another kind or [`keep`](./README.md#dependencies) is declared in the top-level
[`dependencies`](./README.md#dependencies) list instead.

All declarations (the top-level list, every entry's list, every in-folder list) **merge into one list**; where an edge
is declared changes nothing about how it plans. The top-level list also accepts a shorthand item keyed by consumer name
(`{ "web": ["core", "utils"] }` or `{ "web": "core" }`), normalized into full entries at load.

`dispat compute` treats every declaration source as one merged list and edits each declaration **in the entry that holds
it**, whichever layer that is: a stale edge declared in a space's `packages` entry is removed from the root config, one
declared in a space file from that file, one declared in a package's own file from there. Every suggestion names its
source, so `spaces["libs"]: packages["core"]: dependencies[0]` says exactly what an applied change would touch. A kind
correction moves the edge to the top-level list (the string form cannot carry a kind), detected additions always land
there too, and each edited file gets its own `.backup`.

## In-folder configuration files

A package folder may carry a dispat config file of its own, under the same names and formats the root config resolves
through (`dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml`, first match wins, and a
[`.dispatignore`](./spaces.md#choosing-between-two-config-files) in the folder chooses between them). Its top-level
object is exactly the package entry object above minus `path` (a file cannot move the folder it lives in), and it is the
**most local** layer, the last rung of [the ladder](#the-override-ladder). The same merge rules apply, unknown keys are
rejected with the file named, and the file travels with the package: a package moved between spaces keeps its
exceptions.

```json
// packages/core/dispat.json
{
  "revertOnFail": true,
  "versionGroup": "platform"
}
```

A package folder's file that declares `spaces` or `packages` is refused with guidance: the folder holds a monorepo root
of its own (a vendored or nested repository) and must be excluded via [`.dispatignore`](./spaces.md#dispatignore), not
half-merged. A space folder's file is the one place `packages` belongs outside the root config; see
[the space configuration file](./spaces.md#the-space-configuration-file).

Config resolution is aware of every one of these files: running the CLI from inside a package folder ascends **past**
the package's own file, and past its space's, to the monorepo root, so `cd packages/core && dispat lint` works whatever
the folders on the way carry.
