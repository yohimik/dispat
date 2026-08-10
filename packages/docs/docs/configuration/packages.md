# Packages

The top-level `packages` map holds per-package configuration, keyed by package name (matched case-insensitively, like
every config map key). An entry plays one of two roles:

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
- `tagFormat` overrides like everywhere else: package over space over repository.

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

`dispat compute` treats every declaration source as one merged list and edits each declaration **in the file that holds
it**: a stale package-declared edge is removed from the package's own config (the suggestion names the file), a kind
correction on one moves the edge to the top-level list (the string form cannot carry a kind), and detected additions
always land in the top-level list. Each edited file gets its own `.backup`.

## In-folder configuration files

A package folder may carry a dispat config file of its own, under the same names and formats the root config resolves
through (`dispat.json`, `dispat.yaml`, `dispat.yml`, `dispat.toml`, first match wins). Its top-level object is exactly
the package entry object above minus `path` (a file cannot move the folder it lives in), and it is the **most local**
layer: space config (or the standalone entry's base), then the `packages` entry, then the in-folder file, field by
field. The same merge rules apply, unknown keys are rejected with the file named, and the file travels with the package:
a package moved between spaces keeps its exceptions.

```json
// packages/core/dispat.json
{
  "revertOnFail": true,
  "versionGroup": "platform"
}
```

An in-folder file that declares `spaces` or `packages` is refused with guidance: the folder holds a monorepo root of its
own (a vendored or nested repository) and must be excluded via
[`.dispatignore`](./spaces.md#dispatignore), not half-merged. Config resolution is aware of these files too: running the
CLI from inside a package folder ascends **past** the package's own file to the config that declares spaces or packages,
so `cd packages/core && dispat lint` works with or without one.
